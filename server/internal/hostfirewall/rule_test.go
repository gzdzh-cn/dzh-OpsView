package hostfirewall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPortNormalizationAndValidation(t *testing.T) {
	for _, tc := range []struct{ in, out string }{{"443,80,8000:8010,8005-8020", "80,443,8000-8020"}, {"1,2,3,65535", "1-3,65535"}} {
		got, err := PortSpec(tc.in, false)
		if err != nil || got != tc.out {
			t.Fatal(got, err)
		}
	}
	for _, s := range []string{"0", "65536", "1-0", "22;id", "22,,80", "1-2-3", "-1"} {
		if _, err := PortSpec(s, false); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}
func TestUFWInheritedRulesPreserveFamilyCommentsAndUnsafeMatches(t *testing.T) {
	text := `*filter
### tuple ### allow tcp 555 0.0.0.0/0 any 0.0.0.0/0 in comment=737368
-A ufw-user-input -p tcp --dport 555 -j ACCEPT
### tuple ### deny tcp 80,443 192.0.2.0/24 any 0.0.0.0/0 out comment=74657374
-A ufw-user-output -p tcp -m multiport --dports 80,443 -d 192.0.2.0/24 -j DROP
### tuple ### allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 in
-A ufw-user-input -p tcp -s 192.0.2.0/24 --dport 22 -m conntrack --ctstate NEW -j ACCEPT
COMMIT
`
	rows := ParseUFW(text, "ipv4")
	if len(rows) != 3 || rows[0].Note != "ssh" || !rows[0].Editable || rows[1].Direction != "out" || rows[1].Note != "test" || rows[1].Destination != "192.0.2.0/24" || rows[2].Editable {
		t.Fatalf("%+v", rows)
	}
	other := ParseUFW(strings.ReplaceAll(text, "ufw-user-", "ufw6-user-"), "ipv6")
	if rows[0].ID == other[0].ID {
		t.Fatal("family identity collided")
	}
	changed := ParseUFW(strings.ReplaceAll(text, "737368", "535348"), "ipv4")
	if rows[0].Revision == changed[0].Revision {
		t.Fatal("comment drift not detected")
	}
}
func TestFirewalldRichRulesAndGroupAttachments(t *testing.T) {
	r := ParseRich(`rule family="ipv6" source address="2001:db8::/32" port port="8080" protocol="tcp" accept`, "public")
	if !r.Editable || r.Source != "2001:db8::/32" || r.Ports != "8080" {
		t.Fatal(r)
	}
	advanced := ParseRich(`rule family="ipv4" source NOT address="192.0.2.0/24" port port="22" protocol="tcp" accept`, "public")
	if advanced.Editable {
		t.Fatal("inverted match simplified")
	}
	ctx := context.Background()
	var calls []string
	c := &Client{Run: func(_ context.Context, name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if len(args) == 1 && args[0] == "--state" {
			return "running", nil
		}
		return "success", nil
	}}
	r.Permanent = true
	r.Runtime = false
	if err := c.Change(ctx, "firewalld", r, true); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || !strings.Contains(calls[1], "--permanent") {
		t.Fatal(calls)
	}
	calls = nil
	r.Service = "@port:8080/tcp"
	r.Family = "dual"
	if err := c.Change(ctx, "firewalld", r, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(calls[1], "--remove-port=8080/tcp") {
		t.Fatal(calls)
	}
}
func TestNoFirewalldReloadWithExternalDrift(t *testing.T) {
	calls := []string{}
	c := &Client{Run: func(_ context.Context, name string, args ...string) (string, error) {
		s := strings.Join(args, " ")
		calls = append(calls, s)
		if strings.Contains(s, "--permanent") {
			return "ports: 22/tcp", nil
		}
		return "ports: 22/tcp 8080/tcp", nil
	}}
	if c.SafeReloadReady(context.Background()) == nil {
		t.Fatal("runtime-only rule was overwritten")
	}
	for _, s := range calls {
		if strings.Contains(s, "--reload") {
			t.Fatal(calls)
		}
	}
}
func TestInactiveUFWReadsSavedRules(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "user.rules"), []byte("-A ufw-user-input -p tcp --dport 8080 -j ACCEPT\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c := &Client{ConfigDir: dir, Run: func(_ context.Context, name string, args ...string) (string, error) {
		if name == "ufw" {
			return "Status: inactive", nil
		}
		return "", errors.New("missing")
	}}
	s, err := c.Status(context.Background())
	if err != nil || len(s.Rules) != 1 || s.Rules[0].Runtime || !s.Rules[0].Permanent {
		t.Fatal(s, err)
	}
}
func TestWordsNoShellExpansion(t *testing.T) {
	got, e := Words(`rule family="ipv4" log prefix="$(id) 'hello'"`)
	if e != nil || !reflect.DeepEqual(got, []string{"rule", "family=ipv4", "log", "prefix=$(id) 'hello'"}) {
		t.Fatal(got, e)
	}
}

func TestUFWParserIgnoresRateLimitImplementationChains(t *testing.T) {
	text := `### tuple ### allow tcp 443 0.0.0.0/0 any 0.0.0.0/0 in
-A ufw-user-input -p tcp --dport 443 -j ACCEPT
### END RULES ###
-A ufw-user-limit -m limit --limit 3/minute -j LOG
-A ufw-user-limit -j REJECT
-A ufw-user-limit-accept -j ACCEPT
`
	rows := ParseUFW(text, "ipv4")
	if len(rows) != 1 || rows[0].Position != 1 {
		t.Fatal(rows)
	}
}
func TestUFWIPv6InsertionUsesCurrentCombinedPosition(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "user.rules"), []byte("-A ufw-user-input -p tcp --dport 80 -j ACCEPT\n"), 0600)
	_ = os.WriteFile(filepath.Join(dir, "user6.rules"), []byte("-A ufw6-user-input -p tcp --dport 443 -j ACCEPT\n"), 0600)
	var args []string
	c := &Client{ConfigDir: dir, Run: func(_ context.Context, _ string, a ...string) (string, error) { args = a; return "", nil }}
	r := Rule{Kind: "port", Protocol: "tcp", Ports: "22", Family: "ipv6", Source: "any", Destination: "any", Direction: "in", Action: "allow", Position: 1}
	if err := c.Change(context.Background(), "ufw", r, false); err != nil {
		t.Fatal(err)
	}
	if len(args) < 2 || args[0] != "insert" || args[1] != "2" {
		t.Fatal(args)
	}
	r.Position = 2
	if err := c.Change(context.Background(), "ufw", r, false); err != nil {
		t.Fatal(err)
	}
	if args[0] == "insert" {
		t.Fatal("last position must append", args)
	}
}

func TestSafeReloadIgnoresDefaultZoneActiveAnnotation(t *testing.T) {
	c := &Client{Run: func(_ context.Context, name string, args ...string) (string, error) {
		if name == "iptables-save" || name == "ip6tables-save" {
			return "", nil
		}
		if strings.Contains(strings.Join(args, " "), "--list-all-zones") {
			if len(args) > 1 && args[0] == "--permanent" {
				return "public (default)\n  services: ssh", nil
			}
			return "public (default, active)\n  services: ssh", nil
		}
		return "", nil
	}}
	if err := c.SafeReloadReady(context.Background()); err != nil {
		t.Fatal(err)
	}
}
