package executor

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"opsview/internal/model"
	"opsview/internal/runtimeconfig"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func securityFixture(t *testing.T) (*Engine, sshSettings) {
	t.Helper()
	dir := t.TempDir()
	e := &Engine{Root: dir, sshConfigPath: filepath.Join(dir, "sshd_config"), PanelConfigPath: filepath.Join(dir, "panel.json")}
	if err := os.MkdirAll(filepath.Join(dir, "firewall"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := runtimeconfig.Save(e.PanelConfigPath, runtimeconfig.Default()); err != nil {
		t.Fatal(err)
	}
	settings := sshSettings{Ports: []int{555}, Password: false, PublicKey: true, RootLogin: "prohibit-password", MaxAuthTries: 6, LoginGraceTime: 120, ClientAliveCountMax: 3}
	if err := os.WriteFile(e.sshPath(), buildSSHConfig([]byte("# original\nSubsystem sftp internal-sftp\n"), settings), 0600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPSVIEW_SECURITY_TEST_EXE", executable)
	t.Setenv("OPSVIEW_SECURITY_COMMAND_HELPER", "1")
	t.Setenv("GORACE", os.Getenv("GORACE")+" atexit_sleep_ms=0")
	program := `#!/bin/sh
exec "$OPSVIEW_SECURITY_TEST_EXE" -test.run=^TestSecurityCommandHelper$ -- "$0" "$@"
`
	for _, name := range []string{"sshd", "systemctl", "ufw", "nft", "iptables", "ip6tables", "who", "fail2ban-client"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(program), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("OPSVIEW_SECURITY_TEST", dir)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	return e, settings
}
func sshParams(t *testing.T, e *Engine, s sshSettings) model.Config {
	t.Helper()
	old, _, err := e.readSSHConfig()
	if err != nil {
		t.Fatal(err)
	}
	_, effective, err := e.sshEffective(context.Background(), e.sshPath())
	if err != nil {
		t.Fatal(err)
	}
	revision, err := sshRevision(old, effective)
	if err != nil {
		t.Fatal(err)
	}
	return model.Config{"revision": revision, "settings": s, "confirmedImpact": true, "source": "192.0.2.10"}
}
func sshBannerServer(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte("SSH-2.0-OpsView-test\r\n"))
			c.Close()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}
func TestSSHConfigPreservesUnmanagedAndMatch(t *testing.T) {
	ports := sshProtectedPorts("port 22\nlistenaddress 127.0.0.1:555\nlistenaddress [::]:2222\n")
	if len(ports) != 3 || ports[1] != 555 || ports[2] != 2222 {
		t.Fatal("explicit ListenAddress ports not protected", ports)
	}
	_, s := securityFixture(t)
	old := []byte("# user comment\nInclude /etc/ssh/sshd_config.d/*.conf\nPort 22\nPasswordAuthentication yes\nSubsystem sftp internal-sftp\nMatch User deploy\n  PasswordAuthentication yes\n")
	got := buildSSHConfig(old, s)
	for _, text := range []string{"# user comment", "Include /etc/ssh/sshd_config.d/*.conf", "Subsystem sftp internal-sftp", "Match User deploy\n  PasswordAuthentication yes", "KbdInteractiveAuthentication no"} {
		if !strings.Contains(string(got), text) {
			t.Fatalf("lost %q", text)
		}
	}
	if strings.Contains(string(got), "Port 22\n") {
		t.Fatal("old global port retained")
	}
	if string(buildSSHConfig(got, s)) != string(got) {
		t.Fatal("managed block not idempotent")
	}
	s.PublicKey = false
	if s.validate() == nil {
		t.Fatal("all authentication disabled")
	}
	s.PublicKey = true
	s.RootLogin = "yes\nForceCommand bad"
	if s.validate() == nil {
		t.Fatal("directive injection")
	}
}
func TestSSHRevisionIncludesConditionalFiles(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "conditional config.conf")
	if err := os.WriteFile(child, []byte("Match User deploy\n PasswordAuthentication no\n"), 0600); err != nil {
		t.Fatal(err)
	}
	main := []byte("Include=\"" + child + "\" # local policy\n")
	one, err := sshRevision(main, "same global settings")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(child, []byte("Match User deploy\n PasswordAuthentication yes\n"), 0600)
	two, err := sshRevision(main, "same global settings")
	if err != nil || one == two {
		t.Fatal("conditional Include change did not change revision", err)
	}
}
func TestSSHApplyConfirmAndPersistentRollback(t *testing.T) {
	e, s := securityFixture(t)
	s.Ports = []int{sshBannerServer(t)}
	if err := sshPortAvailable(s.Ports[0]); err == nil {
		t.Fatalf("test port %d unexpectedly available", s.Ports[0])
	}
	old := buildSSHConfig([]byte("# keep\n"), s)
	_ = os.WriteFile(e.sshPath(), old, 0600)
	s.Password = true
	p := sshParams(t, e, s)
	if _, err := e.sshOperation(context.Background(), model.Operation{Name: "system.ssh-apply", Params: p}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.sshOperation(context.Background(), model.Operation{Name: "system.ssh-apply", Params: p}, func(string) {}); err == nil {
		t.Fatal("pending overwritten")
	}
	if _, err := e.sshOperation(context.Background(), model.Operation{Name: "system.ssh-confirm", Params: model.Config{}}, func(string) {}); err == nil {
		t.Fatal("unverified connection accepted")
	}
	if _, err := e.sshOperation(context.Background(), model.Operation{Name: "system.ssh-confirm", Params: model.Config{"connected": true}}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.sshPendingPath()); !os.IsNotExist(err) {
		t.Fatal("pending state retained")
	}
	s.Password = false
	p = sshParams(t, e, s)
	if _, err := e.sshOperation(context.Background(), model.Operation{Name: "system.ssh-apply", Params: p}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	pending, _ := e.loadSSH()
	pending.Deadline = time.Now().Add(-time.Second).Unix()
	_ = e.saveSSH(pending)
	// A fresh engine proves rollback does not depend on an in-memory timer.
	restarted := &Engine{Root: e.Root, sshConfigPath: e.sshConfigPath}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go restarted.watchSSH(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(e.sshPendingPath()); os.IsNotExist(err) {
			actual, _, _ := e.readSSHConfig()
			if string(actual) != string(pending.Old) {
				t.Fatal("wrong rollback content")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("expired SSH state not restored")
}
func TestSSHRejectsDriftIncludeConflictPortUseAndNoImpact(t *testing.T) {
	e, s := securityFixture(t)
	p := sshParams(t, e, s)
	original, _, _ := e.readSSHConfig()
	p["revision"] = "stale"
	if _, err := e.applySSH(context.Background(), p, func(string) {}); err == nil {
		t.Fatal("drift accepted")
	}
	p = sshParams(t, e, s)
	p["confirmedImpact"] = false
	if _, err := e.applySSH(context.Background(), p, func(string) {}); err == nil {
		t.Fatal("impact ignored")
	}
	s.Ports = []int{sshBannerServer(t)}
	p = sshParams(t, e, s)
	if _, err := e.applySSH(context.Background(), p, func(string) {}); err == nil || !strings.Contains(err.Error(), "占用") {
		t.Fatal("occupied port accepted", err)
	}
	s.Ports = []int{555}
	_ = os.WriteFile(filepath.Join(e.Root, "include-conflict"), nil, 0600)
	p = sshParams(t, e, s)
	if _, err := e.applySSH(context.Background(), p, func(string) {}); err == nil || !strings.Contains(err.Error(), "Include") {
		t.Fatal("conflicting Include accepted", err)
	}
	actual, _, _ := e.readSSHConfig()
	if string(actual) != string(original) {
		t.Fatal("rejected settings changed original")
	}
}
func TestFirewallActualRulesAndTransactionalMode(t *testing.T) {
	rows := parseUFWNumbered("Status: active\n[ 1] 555/tcp ALLOW IN Anywhere # ssh\n[12] 80/tcp (v6) DENY IN 2001:db8::/32 # opsview-test\ninvalid")
	if len(rows) != 2 || rows[0].Int("number") != 1 || rows[1].String("source") != "2001:db8::/32" || !rows[1].Bool("managed") {
		t.Fatal(rows)
	}
	e, _ := securityFixture(t)
	ctx := context.Background()
	apply := model.Operation{Name: "system.firewall-mode", Params: model.Config{"enabled": true, "source": "192.0.2.10", "confirmedImpact": true}}
	if _, err := e.firewallSystem(ctx, apply, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.Root, "ufw-active")); err != nil {
		t.Fatal("not enabled")
	}
	p, err := e.loadFirewallMode()
	if err != nil || len(p.UndoRules) != 2 {
		t.Fatal(p, err)
	}
	commands, _ := os.ReadFile(filepath.Join(e.Root, "commands"))
	if !strings.Contains(string(commands), `"555"`) || !strings.Contains(string(commands), `"18790"`) {
		t.Fatal("management ports not preserved")
	}
	p.Deadline = time.Now().Add(-time.Second).Unix()
	_ = e.saveFirewallMode(p)
	e.reconcileFirewall(ctx)
	if _, err = os.Stat(filepath.Join(e.Root, "ufw-active")); !os.IsNotExist(err) {
		t.Fatal("old inactive mode not restored")
	}
	if _, err = os.Stat(e.firewallModePath()); !os.IsNotExist(err) {
		t.Fatal("rollback not completed")
	}
	if _, err := e.firewallSystem(ctx, apply, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.firewallSystem(ctx, model.Operation{Name: "system.firewall-confirm", Params: model.Config{"connected": true}}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(e.Root, "ufw-active")); err != nil {
		t.Fatal("confirmation disabled firewall")
	}
}
func testPublicKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))) + " test"
}
func TestSSHKeysRevisionDuplicateAndLastKeyProtection(t *testing.T) {
	e, _ := securityFixture(t)
	ctx := context.Background()
	key := testPublicKey(t)
	add := func(value string) error {
		data := e.sshPublicKeys(ctx)
		_, err := e.sshOperation(ctx, model.Operation{Name: "system.ssh-key-add", Params: model.Config{"revision": data.String("revision"), "publicKey": value, "confirmedImpact": true}}, func(string) {})
		return err
	}
	if err := add(key); err != nil {
		t.Fatal(err)
	}
	if err := add(key); err == nil {
		t.Fatal("duplicate accepted")
	}
	if err := add("command=\"whoami\" " + testPublicKey(t)); err == nil {
		t.Fatal("key options accepted")
	}
	data := e.sshPublicKeys(ctx)
	raw, _ := json.Marshal(data["rows"])
	var rows []model.Config
	_ = json.Unmarshal(raw, &rows)
	op := model.Operation{Name: "system.ssh-key-delete", Params: model.Config{"fingerprint": rows[0].String("fingerprint"), "revision": data.String("revision"), "confirmedImpact": true}}
	if _, err := e.sshOperation(ctx, op, func(string) {}); err == nil {
		t.Fatal("last root key removed")
	}
	if err := add(testPublicKey(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.sshOperation(ctx, op, func(string) {}); err == nil {
		t.Fatal("stale key revision accepted")
	}
	op.Params["revision"] = e.sshPublicKeys(ctx).String("revision")
	if _, err := e.sshOperation(ctx, op, func(string) {}); err != nil {
		t.Fatal(err)
	}
	path, _ := e.sshKeysFile(ctx)
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0600 {
		t.Fatal("public key file mode")
	}
}

func TestSSHReloadFailureRetainsRecoveryRecord(t *testing.T) {
	e, s := securityFixture(t)
	s.Ports = []int{sshBannerServer(t)}
	old := buildSSHConfig([]byte("# before failure\n"), s)
	_ = os.WriteFile(e.sshPath(), old, 0600)
	s.Password = true
	p := sshParams(t, e, s)
	marker := filepath.Join(e.Root, "reload-fail")
	_ = os.WriteFile(marker, nil, 0600)
	if _, err := e.sshOperation(context.Background(), model.Operation{Name: "system.ssh-apply", Params: p}, func(string) {}); err == nil || !strings.Contains(err.Error(), "回退待重试") {
		t.Fatal("reload failure not reported", err)
	}
	actual, _, _ := e.readSSHConfig()
	if string(actual) != string(old) {
		t.Fatal("old config not restored after reload failure")
	}
	recovery, err := e.loadSSH()
	if err != nil {
		t.Fatal("recovery record lost", err)
	}
	_ = os.Remove(marker)
	if err = e.rollbackSSH(context.Background(), recovery); err != nil {
		t.Fatal(err)
	}
}

func TestFirewallModeRejectsUnsafeSourceAndUnknownBackend(t *testing.T) {
	e, _ := securityFixture(t)
	ctx := context.Background()
	op := model.Operation{Name: "system.firewall-mode", Params: model.Config{"enabled": true, "source": "any", "confirmedImpact": true}}
	if _, err := e.firewallSystem(ctx, op, func(string) {}); err == nil {
		t.Fatal("unrestricted source accepted")
	}
	op.Params["source"] = "192.0.2.1;id"
	if _, err := e.firewallSystem(ctx, op, func(string) {}); err == nil {
		t.Fatal("source injection accepted")
	}
	op.Params["source"] = "192.0.2.1"
	op.Params["confirmedImpact"] = false
	if _, err := e.firewallSystem(ctx, op, func(string) {}); err == nil {
		t.Fatal("missing impact confirmation")
	}
	bin := strings.Split(os.Getenv("PATH"), ":")[0]
	_ = os.WriteFile(filepath.Join(bin, "nft"), []byte("#!/bin/sh\necho 'table inet custom { chain input { type filter hook input priority 0; policy drop; } }'\n"), 0755)
	op.Params["confirmedImpact"] = true
	if _, err := e.firewallSystem(ctx, op, func(string) {}); err == nil {
		t.Fatal("unknown firewall replaced")
	}
	if _, err := os.Stat(filepath.Join(e.Root, "ufw-active")); !os.IsNotExist(err) {
		t.Fatal("rejected operation changed firewall")
	}
}

// Reuse the compiled Go test binary as command fixtures so minimal offline
// distro images need no Python interpreter or extra package installation.
func TestSecurityCommandHelper(t *testing.T) {
	if os.Getenv("OPSVIEW_SECURITY_COMMAND_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" && i+1 < len(os.Args) {
			os.Exit(securityCommandFixture(os.Args[i+1:]))
		}
	}
	os.Exit(2)
}
func securityCommandFixture(argv []string) int {
	name, args := filepath.Base(argv[0]), argv[1:]
	root := os.Getenv("OPSVIEW_SECURITY_TEST")
	exists := func(name string) bool { _, err := os.Stat(filepath.Join(root, name)); return err == nil }
	f, err := os.OpenFile(filepath.Join(root, "commands"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return 1
	}
	_ = json.NewEncoder(f).Encode(append([]string{name}, args...))
	_ = f.Close()
	if len(args) == 0 {
		return 0
	}
	switch name {
	case "sshd":
		var path string
		for i, a := range args {
			if a == "-f" && i+1 < len(args) {
				path = args[i+1]
			}
		}
		b, err := os.ReadFile(path)
		if err != nil || strings.Contains(string(b), "BAD") {
			return 1
		}
		if args[0] == "-T" {
			keys := map[string]bool{"port": true, "passwordauthentication": true, "pubkeyauthentication": true, "permitrootlogin": true, "maxauthtries": true, "logingracetime": true, "clientaliveinterval": true, "clientalivecountmax": true}
			for _, line := range strings.Split(string(b), "\n") {
				p := strings.Fields(line)
				if len(p) >= 2 && keys[strings.ToLower(p[0])] {
					fmt.Println(strings.ToLower(p[0]), p[1])
				}
			}
			fmt.Println("authorizedkeysfile .ssh/authorized_keys .ssh/authorized_keys2\nauthorizedkeyscommand none")
			if exists("include-conflict") {
				fmt.Println("port 9999")
			}
		}
	case "systemctl":
		switch args[0] {
		case "is-active":
			for _, unit := range args[1:] {
				if unit == "ssh.service" {
					fmt.Println("active")
				} else {
					fmt.Println("inactive")
				}
			}
			if len(args) < 2 || args[1] != "ssh.service" {
				return 3
			}
		case "show":
			for _, a := range args {
				if a == "--property=ExecStart" {
					fmt.Println("/usr/sbin/sshd -D")
				}
			}
		case "reload":
			if exists("reload-fail") {
				return 1
			}
		}
	case "ufw":
		state, rulesPath := filepath.Join(root, "ufw-active"), filepath.Join(root, "ufw-rules")
		var rules [][]string
		if b, err := os.ReadFile(rulesPath); err == nil {
			_ = json.Unmarshal(b, &rules)
		}
		switch {
		case args[0] == "status":
			if exists("ufw-active") {
				fmt.Println("Status: active")
			} else {
				fmt.Println("Status: inactive")
			}
		case strings.Join(args, " ") == "show added":
			for _, rule := range rules {
				fmt.Println("ufw " + strings.Join(rule, " "))
			}
		case strings.Join(args, " ") == "--force enable":
			if err := os.WriteFile(state, nil, 0600); err != nil {
				return 1
			}
		case strings.Join(args, " ") == "--force disable":
			_ = os.Remove(state)
		case len(args) >= 2 && args[0] == "--force" && args[1] == "delete":
			for i, rule := range rules {
				if strings.Join(rule, "\x00") == strings.Join(args[2:], "\x00") {
					rules = append(rules[:i], rules[i+1:]...)
					break
				}
			}
		case args[0] == "allow":
			rules = append(rules, args)
		}
		b, _ := json.Marshal(rules)
		if err := os.WriteFile(rulesPath, b, 0600); err != nil {
			return 1
		}
	case "iptables", "ip6tables":
		fmt.Println("-P INPUT ACCEPT")
	}
	return 0
}
