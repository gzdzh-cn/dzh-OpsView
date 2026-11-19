package executor

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"opsview/internal/hostfirewall"
	"opsview/internal/model"
	"opsview/internal/runtimeconfig"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLinuxFirewallAdmin(t *testing.T) {
	backend := os.Getenv("OPSVIEW_FIREWALL_ADMIN_SMOKE")
	if backend != "ufw" && backend != "firewalld" {
		t.Skip("disposable Linux firewall namespace only")
	}
	ctx := context.Background()
	root := t.TempDir()
	e := &Engine{Root: root, PanelConfigPath: filepath.Join(root, "panel.json"), firewallClient: hostfirewall.Default()}
	if err := e.Initialize(); err != nil {
		t.Fatal(err)
	}
	if err := runtimeconfig.Save(e.PanelConfigPath, runtimeconfig.Default()); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(root, "firewall"), 0700)
	e.firewallConfigRoot = filepath.Join(root, "etc")
	for _, dir := range []string{"systemd/system", "sysctl.d", "opsview"} {
		_ = os.MkdirAll(filepath.Join(e.firewallConfigRoot, dir), 0755)
	}
	native := e.firewallClient.Run
	e.firewallClient.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "systemctl" {
			return "", nil
		}
		return native(ctx, name, args...)
	}
	e.firewallClient.Preferred = backend
	if backend == "ufw" {
		if _, err := native(ctx, "ufw", "--force", "enable"); err != nil {
			t.Fatal(err)
		}
		_, _ = native(ctx, "ufw", "allow", "18861/tcp", "comment", "inherited")
		// Reproduce a migrated host: Baota/Fail2ban's independent INPUT chains
		// must not disable UFW management, and UFW must never flush them.
		for _, bin := range []string{"iptables", "ip6tables"} {
			for _, args := range [][]string{{"-N", "IN_BT"}, {"-A", "IN_BT", "-p", "tcp", "--dport", "19998", "-j", "RETURN"}, {"-A", "INPUT", "-j", "IN_BT"}} {
				if _, err := native(ctx, bin, args...); err != nil {
					t.Fatal(err)
				}
			}
		}
	} else {
		if _, err := native(ctx, "firewall-cmd", "--state"); err != nil {
			t.Fatal(err)
		}
		for _, plane := range [][]string{{"--permanent"}, {}} {
			_, err := native(ctx, "firewall-cmd", append(plane, "--zone=public", "--add-port=18861/tcp")...)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	s, err := e.firewallSnapshot(ctx)
	if err != nil || s.Backend != backend || !s.Enabled || !s.Compatible {
		t.Fatal(s, err)
	}
	inherited := false
	for _, r := range s.Rules {
		if strings.Contains(r.Ports, "18861") {
			inherited = true
		}
	}
	if !inherited {
		t.Fatal("inherited system rules missing", s.Rules)
	}
	rule := hostfirewall.Rule{Kind: "port", Protocol: "tcp", Ports: "18862", Source: "any", Destination: "any", Family: "ipv4", Direction: "in", Action: "allow", Zone: "public", Note: "opsview-test"}
	save := func(rule hostfirewall.Rule, id string) {
		t.Helper()
		s, er := e.firewallSnapshot(ctx)
		if er != nil {
			t.Fatal(er)
		}
		if _, er = e.firewallAdmin(ctx, model.Operation{Name: "system.firewall-admin-save", Params: model.Config{"revision": s.Revision, "id": id, "rule": rule}}, func(s string) { t.Log(s) }); er != nil {
			t.Fatal(er)
		}
	}
	t.Run("edit-and-delete-inherited-system-port", func(t *testing.T) {
		var inherited hostfirewall.Rule
		for _, row := range s.Rules {
			if row.Ports == "18861" && (row.Family == "ipv4" || row.Family == "dual") {
				inherited = row
				break
			}
		}
		if inherited.ID == "" {
			t.Fatal("inherited port not found")
		}
		candidate := inherited
		candidate.Ports = "18860"
		if _, err := e.firewallAdmin(ctx, model.Operation{Name: "system.firewall-admin-save", Params: model.Config{"revision": s.Revision, "id": inherited.ID, "rule": candidate}}, func(line string) { t.Log(line) }); err != nil {
			t.Fatal(err)
		}
		actual, err := e.firewallSnapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range actual.Rules {
			if row.Ports == "18861" && (backend == "firewalld" || row.Family == "ipv4") {
				t.Fatal("old inherited port remained")
			}
			if row.Ports == "18860" {
				found = true
				if !row.Runtime || !row.Permanent {
					t.Fatal("edited rule was not applied in both planes", row)
				}
				if _, err := e.firewallAdmin(ctx, model.Operation{Name: "system.firewall-admin-delete", Params: model.Config{"revision": actual.Revision, "id": row.ID}}, func(line string) { t.Log(line) }); err != nil {
					t.Fatal(err)
				}
				// A firewalld attachment is dual-stack; a modification expands it
				// into individual rules, so refresh the revision before each delete.
				actual, err = e.firewallSnapshot(ctx)
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		if !found {
			t.Fatal("edited port missing")
		}
		for _, row := range actual.Rules {
			if row.Ports == "18860" {
				t.Fatal("deleted port remained in system state", row)
			}
		}
	})
	save(rule, "")
	s, err = e.firewallSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var added hostfirewall.Rule
	for _, r := range s.Rules {
		if r.Ports == "18862" && r.Family == "ipv4" {
			added = r
			break
		}
	}
	if added.ID == "" || !added.Editable {
		t.Fatalf("new rule missing: %+v", s.Rules)
	}
	rule.Ports = "18863"
	rule.Note = "modified"
	save(rule, added.ID)
	s, err = e.firewallSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var modified hostfirewall.Rule
	for _, r := range s.Rules {
		if r.Ports == "18862" {
			t.Fatal("old rule remained")
		}
		if r.Ports == "18863" {
			modified = r
		}
	}
	if modified.ID == "" {
		t.Fatal("replacement not applied")
	}
	if _, err = e.firewallAdmin(ctx, model.Operation{Name: "system.firewall-admin-delete", Params: model.Config{"revision": s.Revision, "id": modified.ID}}, func(s string) { t.Log(s) }); err != nil {
		t.Fatal(err)
	}
	// Ping configuration must not remove ICMPv6 neighbor-discovery rules.
	if _, err = e.firewallAdmin(ctx, model.Operation{Name: "system.firewall-admin-ping", Params: model.Config{"enabled": true}}, func(s string) { t.Log(s) }); err != nil {
		t.Fatal(err)
	}
	if backend == "ufw" {
		b, er := os.ReadFile("/etc/ufw/before6.rules")
		if er != nil || !strings.Contains(string(b), "--icmpv6-type echo-request -j DROP") || (!strings.Contains(string(b), "--icmpv6-type 135") && !strings.Contains(string(b), "--icmpv6-type neighbor-solicitation")) {
			t.Fatal("ICMPv6 policy invalid", er)
		}
	}
	if _, err = e.firewallAdmin(ctx, model.Operation{Name: "system.firewall-admin-ping", Params: model.Config{"enabled": false}}, func(s string) { t.Log(s) }); err != nil {
		t.Fatal(err)
	}
	// Source-limited native redirect, persisted without touching foreign NAT chains.
	forward := hostfirewall.Rule{Kind: "forward", Protocol: "tcp", Ports: "18864", TargetPorts: "18865", Source: "192.0.2.0/24", Destination: "any", Family: "ipv4", Direction: "in", Action: "allow", Zone: "public", Note: "redirect"}
	save(forward, "")
	// Versioned country data is deterministic and can be replaced atomically.
	for _, family := range []string{"ipv4", "ipv6"} {
		path := e.countryPath("CN", family)
		_ = os.MkdirAll(filepath.Dir(path), 0700)
		content := "192.0.2.0/24\n"
		if peer := os.Getenv("OPSVIEW_FIREWALL_PEER_IP"); peer != "" {
			content = peer + "/32\n"
		}
		if family == "ipv6" {
			content = "2001:db8::/32\n"
		}
		if err = os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	region := hostfirewall.Rule{Kind: "region", Protocol: "tcp", Ports: "18866", Countries: []string{"CN"}, Source: "any", Destination: "any", Family: "ipv4", Direction: "in", Action: "deny", Zone: "public", Note: "country-test"}
	save(region, "")
	s, err = e.firewallSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range s.Rules {
		if r.Kind == "region" {
			found = true
			if !r.Runtime {
				t.Fatal("region not active", r)
			}
		}
	}
	if !found {
		t.Fatal("region state missing")
	}

	if peer := os.Getenv("OPSVIEW_FIREWALL_PEER_IP"); peer != "" {
		// Verify an actual outgoing connection, including first firewalld policy creation.
		outgoing := rule
		outgoing.Direction = "out"
		outgoing.Source = "any"
		outgoing.Destination = peer
		outgoing.Ports = "18901"
		outgoing.Action = "deny"
		save(outgoing, "")
		address := net.JoinHostPort(peer, "18901")
		if conn, er := net.DialTimeout("tcp", address, time.Second); er == nil {
			conn.Close()
			t.Fatal("outgoing deny did not block")
		}
		current, er := e.firewallSnapshot(ctx)
		if er != nil {
			t.Fatal(er)
		}
		found := false
		for _, r := range current.Rules {
			if r.Direction == "out" && r.Ports == "18901" && r.Destination == peer {
				found = true
				if _, er = e.firewallAdmin(ctx, model.Operation{Name: "system.firewall-admin-delete", Params: model.Config{"revision": current.Revision, "id": r.ID}}, func(string) {}); er != nil {
					t.Fatal(er)
				}
				break
			}
		}
		if !found {
			t.Fatal("outgoing rule missing")
		}
		conn, er := net.DialTimeout("tcp", address, time.Second)
		if er != nil {
			t.Fatal("outgoing removal did not restore connection", er)
		}
		conn.Close()
	}
	// Docker/foreign NAT sentinels live only in this disposable network namespace.
	for _, args := range [][]string{{"-N", "DOCKER-USER"}, {"-A", "DOCKER-USER", "-s", "198.51.100.0/24", "-j", "DROP"}, {"-t", "nat", "-N", "FOREIGN-NAT"}, {"-t", "nat", "-A", "FOREIGN-NAT", "-p", "tcp", "--dport", "19999", "-j", "RETURN"}} {
		if _, er := native(ctx, "iptables", args...); er != nil {
			t.Fatal(er)
		}
	}
	if os.Getenv("OPSVIEW_FIREWALL_PEER") != "" {
		t.Run("independent-traffic", func(t *testing.T) { testFirewallTraffic(t, e, backend, save, rule, region) })
	}
	// Exercise restart recovery of a durable, expired transaction.
	newRule := rule
	newRule.Ports = "18867"
	if backend == "ufw" {
		newRule.Zone = ""
	}
	tx := firewallTransaction{Backend: backend, Deadline: time.Now().Add(-time.Second).Unix(), Changes: []firewallChange{{After: &newRule, Started: true}}}
	if err = e.writeFirewallAdmin("pending", tx); err != nil {
		t.Fatal(err)
	}
	if err = e.hostFirewall().Change(ctx, backend, newRule, false); err != nil {
		t.Fatal(err)
	}
	e.reconcileFirewall(ctx)
	if _, err = os.Stat(e.firewallAdminPath("pending")); !os.IsNotExist(err) {
		t.Fatal("expired transaction not rolled back", err)
	}
	text, _ := native(ctx, "iptables-save")
	if backend == "ufw" && !strings.Contains(text, "ovfw-") {
		t.Fatal("native extension rules missing")
	}
	foreign, err := native(ctx, "iptables-save")
	if err != nil || !strings.Contains(foreign, "-A DOCKER-USER -s 198.51.100.0/24 -j DROP") || !strings.Contains(foreign, "-A FOREIGN-NAT -p tcp") {
		t.Fatal("foreign / Docker rules changed", err, foreign)
	}
	if backend == "ufw" {
		t.Run("coexisting-ufw-switch", func(t *testing.T) {
			source := os.Getenv("OPSVIEW_FIREWALL_PEER_IP")
			if source == "" {
				source = "127.0.0.1"
			}
			for _, enabled := range []bool{false, true} {
				if _, err := e.firewallSystem(ctx, model.Operation{Name: "system.firewall-mode", Params: model.Config{"enabled": enabled, "confirmedImpact": true, "source": source}}, func(line string) { t.Log(line) }); err != nil {
					t.Fatal(err)
				}
				actual, err := e.firewallSnapshot(ctx)
				if err != nil || actual.Enabled != enabled || !actual.Compatible || len(actual.Rules) == 0 {
					t.Fatal("mode did not reach the real system", actual, err)
				}
				if _, err := e.firewallSystem(ctx, model.Operation{Name: "system.firewall-confirm", Params: model.Config{"connected": true}}, func(string) {}); err != nil {
					t.Fatal(err)
				}
				for _, bin := range []string{"iptables-save", "ip6tables-save"} {
					text, err := native(ctx, bin)
					if err != nil || !strings.Contains(text, "-A INPUT -j IN_BT") || !strings.Contains(text, "-A IN_BT -p tcp") {
						t.Fatal("external INPUT chain lost", text, err)
					}
				}
				text, err := native(ctx, "iptables-save")
				if err != nil || !strings.Contains(text, "-A DOCKER-USER -s 198.51.100.0/24 -j DROP") || !strings.Contains(text, "-A FOREIGN-NAT -p tcp") {
					t.Fatal("external Docker/NAT sentinel lost on UFW toggle", text, err)
				}
			}
		})
	}
	if os.Getenv("OPSVIEW_FIREWALL_PEER") != "" {
		t.Run("fail2ban-log-ban", func(t *testing.T) { testFirewallProtectionLive(t, e, backend) })
	}
	fmt.Fprintln(os.Stdout, "verified firewall backend:", backend)
}

func firewallPeerProbe(t *testing.T, path, target string, port int) bool {
	t.Helper()
	u := os.Getenv("OPSVIEW_FIREWALL_PEER") + path + "?target=" + url.QueryEscape(target) + "&port=" + strconv.Itoa(port)
	client := http.Client{Timeout: 5 * time.Second}
	res, err := client.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	return res.StatusCode == 200
}
func testFirewallTraffic(t *testing.T, e *Engine, backend string, save func(hostfirewall.Rule, string), base, region hostfirewall.Rule) {
	ctx := context.Background()
	server := os.Getenv("OPSVIEW_FIREWALL_SERVER_IP")
	peer := os.Getenv("OPSVIEW_FIREWALL_PEER_IP")
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	srv := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })}
	defer srv.Close()
	go func() { _ = srv.Serve(listener) }()
	port := listener.Addr().(*net.TCPAddr).Port
	allow := base
	allow.Ports = strconv.Itoa(port)
	allow.Source = peer
	save(allow, "")
	if !firewallPeerProbe(t, "/probe", server, port) {
		t.Fatal("allowed source cannot connect")
	}
	snapshot, _ := e.firewallSnapshot(ctx)
	id := ""
	for _, r := range snapshot.Rules {
		if r.Key() == allow.Key() || r.Ports == allow.Ports && r.Source == allow.Source {
			id = r.ID
		}
	}
	if id == "" {
		t.Fatal("live rule missing")
	}
	allow.Action = "deny"
	save(allow, id)
	if firewallPeerProbe(t, "/probe", server, port) {
		t.Fatal("deny rule did not block an independent client")
	}
	snapshot, _ = e.firewallSnapshot(ctx)
	for _, r := range snapshot.Rules {
		if r.Ports == allow.Ports && r.Source == allow.Source {
			id = r.ID
		}
	}
	allow.Action = "allow"
	save(allow, id)
	if !firewallPeerProbe(t, "/ping", server, 0) {
		t.Fatal("baseline ICMP unavailable")
	}
	_, err = e.firewallAdmin(ctx, model.Operation{Name: "system.firewall-admin-ping", Params: model.Config{"enabled": true}}, func(s string) { t.Log(s) })
	if err != nil {
		t.Fatal(err)
	}
	if firewallPeerProbe(t, "/ping", server, 0) {
		t.Fatal("echo request still allowed")
	}
	// Neighbor discovery must still work after enabling the IPv6 Echo filter.
	if address := os.Getenv("OPSVIEW_FIREWALL_SERVER_IPV6"); address != "" {
		if firewallPeerProbe(t, "/ping", address, 0) {
			t.Fatal("IPv6 echo request still allowed")
		}
	}
	_, err = e.firewallAdmin(ctx, model.Operation{Name: "system.firewall-admin-ping", Params: model.Config{"enabled": false}}, func(s string) { t.Log(s) })
	if err != nil {
		t.Fatal(err)
	}
	if !firewallPeerProbe(t, "/ping", server, 0) {
		t.Fatal("ICMP restoration failed")
	}
	if address := os.Getenv("OPSVIEW_FIREWALL_SERVER_IPV6"); address != "" && !firewallPeerProbe(t, "/ping", address, 0) {
		t.Fatal("IPv6 neighbor discovery / echo restoration failed")
	}
	redirect := base
	redirect.Kind = "forward"
	redirect.Ports = "18881"
	redirect.TargetPorts = strconv.Itoa(port)
	redirect.Source = peer
	save(redirect, "")
	if !firewallPeerProbe(t, "/probe", server, 18881) {
		t.Fatal("local redirect failed")
	}
	remote := redirect
	remote.Ports = "18882"
	remote.Target = peer
	remote.TargetPorts = "18901"
	remote.Masquerade = true
	save(remote, "")
	if !firewallPeerProbe(t, "/probe", server, 18882) {
		t.Fatal("remote DNAT/MASQUERADE failed")
	}
	// Saved country CIDRs are fixture data, no IPdeny request is made by this test.
	region.Ports = allow.Ports
	save(region, "")
	if firewallPeerProbe(t, "/probe", server, port) {
		t.Fatal("country set did not block its source")
	}
	snapshot, _ = e.firewallSnapshot(ctx)
	for _, r := range snapshot.Rules {
		if r.Kind == "region" && r.Ports == allow.Ports {
			_, err = e.firewallAdmin(ctx, model.Operation{Name: "system.firewall-admin-delete", Params: model.Config{"revision": snapshot.Revision, "id": r.ID}}, func(s string) { t.Log(s) })
			if err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if !firewallPeerProbe(t, "/probe", server, port) {
		t.Fatal("country removal did not restore access")
	}
	// Reload from persistent configuration, then verify the source-limited rules.
	command := "ufw"
	args := []string{"reload"}
	if backend == "firewalld" {
		command = "firewall-cmd"
		args = []string{"--reload"}
	}
	if backend == "firewalld" {
		if e.hostFirewall().SafeReloadReady(ctx) == nil {
			t.Fatal("unsafe reload accepted with foreign chains")
		}
		// Remove only the test's own sentinels to verify supported reload semantics;
		// the implementation itself refused to touch the foreign configuration.
		for _, args := range [][]string{{"-F", "DOCKER-USER"}, {"-X", "DOCKER-USER"}, {"-t", "nat", "-F", "FOREIGN-NAT"}, {"-t", "nat", "-X", "FOREIGN-NAT"}} {
			if _, err = e.hostFirewall().Run(ctx, "iptables", args...); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err = e.hostFirewall().Run(ctx, command, args...); err != nil {
		t.Fatal(err)
	}
	if backend == "firewalld" {
		for _, args := range [][]string{{"-N", "DOCKER-USER"}, {"-A", "DOCKER-USER", "-s", "198.51.100.0/24", "-j", "DROP"}, {"-t", "nat", "-N", "FOREIGN-NAT"}, {"-t", "nat", "-A", "FOREIGN-NAT", "-p", "tcp", "--dport", "19999", "-j", "RETURN"}} {
			if _, err = e.hostFirewall().Run(ctx, "iptables", args...); err != nil {
				t.Fatal(err)
			}
		}
	}

	if !firewallPeerProbe(t, "/probe", server, port) || !firewallPeerProbe(t, "/probe", server, 18881) || !firewallPeerProbe(t, "/probe", server, 18882) {
		t.Fatal("reload lost persisted allow/forward rules")
	}
	// A management-port deny installs only source-specific guards and rolls back
	// durably after expiry; a fresh network connection validates both states.
	management, err := net.Listen("tcp4", "0.0.0.0:18790")
	if err != nil {
		t.Fatal(err)
	}
	defer management.Close()
	go func() { _ = srv.Serve(management) }()
	deny := base
	deny.Ports = "18790"
	deny.Action = "deny"
	deny.Source = "any"
	snapshot, err = e.firewallSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.firewallAdmin(ctx, model.Operation{Name: "system.firewall-admin-save", Params: model.Config{"revision": snapshot.Revision, "rule": deny, "source": peer, "confirmedImpact": true}}, func(s string) { t.Log(s) })
	if err != nil || !result.Bool("requiresConfirmation") {
		t.Fatal(result, err)
	}
	if !firewallPeerProbe(t, "/probe", server, 18790) {
		t.Fatal("source-specific management guard failed")
	}
	var pending firewallTransaction
	if err = e.readFirewallAdmin("pending", &pending); err != nil {
		t.Fatal(err)
	}
	pending.Deadline = time.Now().Add(-time.Second).Unix()
	_ = e.writeFirewallAdmin("pending", pending)
	e.reconcileFirewall(ctx)
	if _, err = os.Stat(e.firewallAdminPath("pending")); !os.IsNotExist(err) {
		t.Fatal("management rollback incomplete", err)
	}
	if firewallPeerProbe(t, "/probe", server, 18790) {
		t.Fatal("rollback retained an unexpected management allow")
	}

}
func testFirewallProtectionLive(t *testing.T, e *Engine, backend string) {
	ctx := context.Background()
	native := e.hostFirewall().Run
	config := filepath.Join(e.firewallConfigRoot, "fail2ban")
	if err := exec.Command("cp", "-a", "/etc/fail2ban", config).Run(); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(config, "jail.d", "defaults-debian.conf"))
	prefix := []string{"-c", config, "-s", filepath.Join(e.Root, "fail2ban.sock"), "-p", filepath.Join(e.Root, "fail2ban.pid")}
	e.firewallClient.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "fail2ban-client" {
			return native(ctx, name, append(append([]string{}, prefix...), args...)...)
		}
		if name == "systemctl" && len(args) > 1 && args[0] == "start" && args[1] == "fail2ban.service" {
			if text, err := native(ctx, "fail2ban-client", append(append([]string{}, prefix...), "ping")...); err == nil {
				return text, nil
			}
			return native(ctx, "fail2ban-client", append(append([]string{}, prefix...), "start")...)
		}
		return native(ctx, name, args...)
	}
	defer func() { _, _ = e.hostFirewall().Run(ctx, "fail2ban-client", "stop"); e.firewallClient.Run = native }()
	instance := model.Resource{ID: "firewall-nginx-test", Kind: "instance", Config: model.Config{"software": "nginx"}}
	if err := e.save(instance); err != nil {
		t.Fatal(err)
	}
	logs := filepath.Join(e.Root, "instances", instance.ID, "logs")
	_ = os.MkdirAll(logs, 0755)
	path := filepath.Join(logs, "error.log")
	_ = os.WriteFile(path, nil, 0644)
	configData := firewallProtectionConfig{Nginx: true, MaxRetry: 3, FindTime: 30, BanTime: 60, Whitelist: []string{"192.0.2.1"}}
	status := e.hostFirewall().Detect(ctx)
	if _, err := e.firewallProtection(ctx, "protection-save", model.Config{"config": configData}, status, func(s string) { t.Log(s) }); err != nil {
		t.Fatal(err)
	}
	peer := os.Getenv("OPSVIEW_FIREWALL_PEER_IP")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		fmt.Fprintf(f, "%s [error] 100#100: *%d user \"bad\": password mismatch, client: %s, server: localhost, request: \"GET / HTTP/1.0\", host: \"localhost\"\n", time.Now().Format("2006/01/02 15:04:05"), i, peer)
	}
	_ = f.Close()
	banned := false
	for n := 0; n < 40; n++ {
		text, _ := e.hostFirewall().Run(ctx, "fail2ban-client", "get", "opsview-nginx-http-auth", "banip")
		if strings.Contains(text, peer) {
			banned = true
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !banned {
		t.Fatal("real nginx auth log did not trigger Fail2ban")
	}
	if _, err = e.firewallProtection(ctx, "protection-unban", model.Config{"jail": "opsview-nginx-http-auth", "ip": peer}, status, func(s string) { t.Log(s) }); err != nil {
		t.Fatal(err)
	}
	text, _ := e.hostFirewall().Run(ctx, "fail2ban-client", "get", "opsview-nginx-http-auth", "banip")
	if strings.Contains(text, peer) {
		t.Fatal("unban failed")
	}
}
