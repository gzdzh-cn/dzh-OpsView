package executor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"opsview/internal/hostfirewall"
	"opsview/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFirewallImportsNeverFlattenUnsupportedRecords(t *testing.T) {
	rules, issues := parseFirewallImport("1|tcp|80,443|accept|192.0.2.1|web|2026-11-18|\n2|tcp|22|drop|all|test|2026-11-18|dynamic.example\n", "port")
	if len(rules) != 1 || rules[0].Source != "192.0.2.1" || len(issues) != 1 {
		t.Fatal(rules, issues)
	}
	rules, issues = parseFirewallImport(`{"format":"opsview-firewall","version":99,"rules":[]}`, "")
	if len(rules) != 0 || len(issues) != 1 {
		t.Fatal(rules, issues)
	}
	rules, issues = parseFirewallImport("1|8080|192.0.2.2|80|tcp|2026-11-18\n", "forward")
	if len(rules) != 1 || rules[0].Target != "192.0.2.2" || len(issues) > 0 {
		t.Fatal(rules, issues)
	}
}
func TestFirewallInputCannotSupplyNativeCommands(t *testing.T) {
	r, err := decodeFirewallRule(model.Config{"kind": "port", "family": "ipv4", "protocol": "tcp", "ports": "8080", "direction": "in", "action": "allow", "source": "any", "destination": "any", "raw": "rule family=ipv4 accept", "service": "ssh", "position": 1})
	if err != nil || r.Raw != "" || r.Service != "" || r.Position != 0 {
		t.Fatal(r, err)
	}
	r.Ports = "8080; reboot"
	if r.Validate() == nil {
		t.Fatal("injection accepted")
	}
}
func TestFirewallUFWBlocksPreserveOtherOwners(t *testing.T) {
	original := "*filter\n:ufw-before-input - [0:0]\n:foreign-chain - [0:0]\n-A ufw-before-input -j foreign-chain\nCOMMIT\n"
	block := ":OPSVIEW-IN - [0:0]\n-A ufw-before-input -j OPSVIEW-IN\n-A OPSVIEW-IN -j RETURN\n"
	changed, err := replaceFirewallBlock(original, block, "filter")
	if err != nil || !strings.Contains(changed, "-A ufw-before-input -j foreign-chain") || strings.Index(changed, "-j OPSVIEW-IN") > strings.Index(changed, "-j foreign-chain") {
		t.Fatal(changed, err)
	}
	removed, err := replaceFirewallBlock(changed, "", "filter")
	if err != nil || removed != original {
		t.Fatal(removed, err)
	}
	if _, err = replaceFirewallBlock(original+fwBegin, "", "filter"); err == nil {
		t.Fatal("corrupt block overwritten")
	}
}
func TestFirewallPreviewIsBoundToRevisionAndToken(t *testing.T) {
	e := &Engine{Root: t.TempDir()}
	s := hostfirewall.Status{Revision: "one", Rules: []hostfirewall.Rule{}}
	p, err := e.previewFirewallImport(model.Config{"content": `{"format":"opsview-firewall","version":1,"rules":[{"kind":"port","protocol":"tcp","ports":"8080","family":"ipv4","action":"allow","direction":"in","source":"any","destination":"any"}]}`}, s)
	if err != nil {
		t.Fatal(err)
	}
	input := model.Config{"token": p.String("token"), "indices": []any{float64(0)}}
	rows, err := e.selectedFirewallRules(input, s, "import-apply")
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	s.Revision = "two"
	if _, err = e.selectedFirewallRules(input, s, "import-apply"); err == nil {
		t.Fatal("stale preview accepted")
	}
}
func TestFirewallAdminRejectsStaleRevisionBeforeMutation(t *testing.T) {
	e, _ := securityFixture(t)
	dir := filepath.Join(e.Root, "ufw")
	_ = os.Mkdir(dir, 0700)
	_ = os.WriteFile(filepath.Join(dir, "user.rules"), []byte("-A ufw-user-input -p tcp --dport 8080 -j ACCEPT\n"), 0600)
	e.firewallClient = &hostfirewall.Client{ConfigDir: dir, Run: func(_ context.Context, name string, args ...string) (string, error) {
		if name == "ufw" {
			return "Status: active", nil
		}
		return "", nil
	}}
	_, err := e.firewallAdmin(context.Background(), model.Operation{Name: "system.firewall-admin-delete", Params: model.Config{"id": "anything", "revision": "stale"}}, func(string) {})
	if err == nil {
		t.Fatal("stale mutation accepted")
	}
	if _, err = os.Stat(e.firewallAdminPath("pending")); !os.IsNotExist(err) {
		t.Fatal("created mutation journal for stale request")
	}
}
func TestFirewallConfigContainsNoContentCommands(t *testing.T) {
	r := hostfirewall.Rule{Kind: "port", Protocol: "tcp", Ports: "8080", Source: "any", Destination: "any", Family: "ipv4", Direction: "in", Action: "allow", Note: "$(touch /tmp/no)"}
	args := hostfirewall.UFWArgs(r)
	b, _ := json.Marshal(args)
	if !strings.Contains(string(b), "$(touch /tmp/no)") {
		t.Fatal("note was treated as executable code")
	}
}

func TestFirewallCountryVariantsAndScopedForwarding(t *testing.T) {
	r := hostfirewall.Rule{Kind: "region", Protocol: "tcp", Ports: "80,443", Family: "ipv4", Countries: []string{"CN", "US"}, Source: "any", Destination: "any", Action: "deny", Sets: map[string]string{"CN": "ovfw-4-cn-test", "US": "ovfw-4-us-test"}}
	rules := extensionRichRules(r)
	if len(rules) != 4 {
		t.Fatal(rules)
	}
	for _, raw := range rules {
		if strings.Contains(raw, `port="80,443"`) {
			t.Fatal("firewalld single rich port cannot contain a list")
		}
	}
	forward := hostfirewall.Rule{Kind: "forward", Protocol: "tcp", Ports: "8000-8002", TargetPorts: "9000-9002"}
	variants := firewallForwardVariants(forward)
	if len(variants) != 3 || variants[2].Ports != "8002" || variants[2].TargetPorts != "9002" {
		t.Fatal(variants)
	}
}
func TestFirewallDeltaDetectsUnrelatedExternalChange(t *testing.T) {
	a := hostfirewall.Rule{ID: "a", Revision: "one", Kind: "port", Ports: "80"}
	b := hostfirewall.Rule{ID: "b", Revision: "two", Kind: "port", Ports: "443"}
	before := hostfirewall.Status{Enabled: true, Backend: "ufw", Rules: []hostfirewall.Rule{a}}
	after := before
	after.Rules = []hostfirewall.Rule{a, b}
	if !firewallDeltaMatches(before, after, firewallChange{After: &b}) {
		t.Fatal("own addition treated as drift")
	}
	a.Revision = "external"
	after.Rules[0] = a
	if firewallDeltaMatches(before, after, firewallChange{After: &b}) {
		t.Fatal("external drift ignored")
	}
}

func TestFirewallImportRejectsUnknownConditionsAndExpandsDual(t *testing.T) {
	base := `{"kind":"port","protocol":"tcp","ports":"80","source":"any","destination":"any","action":"allow","direction":"in","family":"dual"}`
	rules, issues := parseFirewallImport(base, "")
	if len(issues) != 0 || len(rules) != 2 || rules[0].Family != "ipv4" || rules[1].Family != "ipv6" {
		t.Fatal(rules, issues)
	}
	for _, extra := range []string{`,"interface":"eth0"`, `,"service":"https"`} {
		rules, issues = parseFirewallImport(strings.TrimSuffix(base, "}")+extra+"}", "")
		if len(rules) != 0 || len(issues) != 1 {
			t.Fatal(rules, issues)
		}
	}
}
func TestFirewallIdleIncludesEveryAccessJournal(t *testing.T) {
	e := &Engine{Root: t.TempDir()}
	paths := []string{e.firewallAdminPath("pending"), e.firewallAdminPath("feature-pending"), e.firewallModePath(), e.sshPendingPath(), e.pendingPath(), filepath.Join(e.Root, "firewall", "old.json")}
	for _, p := range paths {
		_ = os.MkdirAll(filepath.Dir(p), 0700)
		_ = os.WriteFile(p, []byte(`{"confirmed":false}`), 0600)
		if e.ensureFirewallIdle() == nil {
			t.Fatal("pending ignored", p)
		}
		_ = os.Remove(p)
	}
	if err := e.ensureFirewallIdle(); err != nil {
		t.Fatal(err)
	}
}
func TestFirewallCountryFailurePreservesValidatedFile(t *testing.T) {
	e := &Engine{Root: t.TempDir()}
	p := e.countryPath("CN", "ipv4")
	_ = os.MkdirAll(filepath.Dir(p), 0700)
	old := []byte("192.0.2.0/24\n")
	_ = os.WriteFile(p, old, 0600)
	for _, data := range []string{"", "not-a-cidr", "2001:db8::/32"} {
		e.countryTransport = dockerRoundTrip(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host != "www.ipdeny.com" || r.URL.Scheme != "https" {
				t.Fatal(r.URL)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(data)), Header: make(http.Header)}, nil
		})
		if e.prepareCountry(context.Background(), "CN", "ipv4", true, func(string) {}) == nil {
			t.Fatal("invalid download accepted")
		}
		b, _ := os.ReadFile(p)
		if string(b) != string(old) {
			t.Fatal("old data changed")
		}
	}
	e.countryTransport = dockerRoundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })
	if e.prepareCountry(context.Background(), "CN", "ipv4", true, func(string) {}) == nil {
		t.Fatal("offline accepted")
	}
	b, _ := os.ReadFile(p)
	if string(b) != string(old) {
		t.Fatal("old data changed")
	}
	_ = e.writeFirewallAdmin("extensions", firewallExtensionState{Rules: []hostfirewall.Rule{{Kind: "region", DataUpdatedAt: time.Now().Add(-8 * 24 * time.Hour).Format(time.RFC3339)}}})
	status, err := e.firewallCountryStatus()
	if err != nil || !status.Bool("due") {
		t.Fatal(status, err)
	}
}
