package executor

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
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
	program := `#!/usr/bin/env python3
import os,sys,json,pathlib
name=pathlib.Path(sys.argv[0]).name;a=sys.argv[1:];root=pathlib.Path(os.environ['OPSVIEW_SECURITY_TEST'])
with (root/'commands').open('a') as f:f.write(json.dumps([name]+a)+'\n')
if name=='sshd':
 p=pathlib.Path(a[a.index('-f')+1]);s=p.read_text()
 if 'BAD' in s:sys.exit(1)
 if '-T' in a:
  keys={'port','passwordauthentication','pubkeyauthentication','permitrootlogin','maxauthtries','logingracetime','clientaliveinterval','clientalivecountmax'}
  for line in s.splitlines():
   p=line.split()
   if p and p[0].lower() in keys:print(p[0].lower(),p[1])
  print('authorizedkeysfile .ssh/authorized_keys .ssh/authorized_keys2\nauthorizedkeyscommand none')
  if (root/'include-conflict').exists():print('port 9999')
elif name=='systemctl':
 if a[0]=='is-active':
  for unit in a[1:]:print('active' if unit=='ssh.service' else 'inactive')
  if a[1]!='ssh.service':sys.exit(3)
 elif a[0]=='show' and '--property=ExecStart' in a:print('/usr/sbin/sshd -D')
 elif a[0]=='reload' and (root/'reload-fail').exists():sys.exit(1)
elif name=='ufw':
 state=root/'ufw-active';rules=root/'ufw-rules';saved=json.loads(rules.read_text()) if rules.exists() else []
 if a[0]=='status':print('Status: '+('active' if state.exists() else 'inactive'))
 elif a==['show','added']:
  for rule in saved:print('ufw '+' '.join(rule))
 elif a==['--force','enable']:state.touch()
 elif a==['--force','disable']:state.unlink(missing_ok=True)
 elif a[:2]==['--force','delete']:
  if a[2:] in saved:saved.remove(a[2:])
 elif a[0]=='allow':saved.append(a)
 rules.write_text(json.dumps(saved))
elif name in ['iptables','ip6tables']:print('-P INPUT ACCEPT')
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
