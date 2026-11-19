package executor

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"opsview/internal/model"
	"opsview/internal/runtimeconfig"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// This test owns an sshd in a disposable container. It never edits the host's
// sshd_config or reloads the host's service. Real UFW rules stay in its netns.
func TestLinuxSecuritySSHAndFirewall(t *testing.T) {
	if os.Getenv("OPSVIEW_SECURITY_SMOKE") != "true" {
		t.Skip("disposable security container only")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil || os.Geteuid() != 0 {
		t.Fatal("requires a disposable root Docker container")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	dir, dirErr := os.MkdirTemp("/root", "opsview-security-")
	if dirErr != nil {
		t.Fatal(dirErr)
	}
	defer os.RemoveAll(dir)
	e := &Engine{Root: dir, sshConfigPath: filepath.Join(dir, "sshd_config"), PanelConfigPath: filepath.Join(dir, "panel.json")}
	_ = os.MkdirAll(filepath.Join(dir, "firewall"), 0700)
	if err := runtimeconfig.Save(e.PanelConfigPath, runtimeconfig.Default()); err != nil {
		t.Fatal(err)
	}
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(private, "OpsView disposable host key")
	if err != nil {
		t.Fatal(err)
	}
	hostKey := filepath.Join(dir, "host_key")
	if err = os.WriteFile(hostKey, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}
	key, _ := ssh.NewPublicKey(pub)
	authorized := filepath.Join(dir, "authorized_keys")
	_ = os.WriteFile(authorized, ssh.MarshalAuthorizedKey(key), 0600)
	freePort := func() int {
		l, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		return l.Addr().(*net.TCPAddr).Port
	}
	oldPort, newPort := freePort(), freePort()
	for oldPort == newPort {
		newPort = freePort()
	}
	s := sshSettings{Ports: []int{oldPort}, PublicKey: true, RootLogin: "yes", MaxAuthTries: 6, LoginGraceTime: 120, ClientAliveCountMax: 3}
	base := []byte(fmt.Sprintf("PidFile %s\nHostKey %s\nAuthorizedKeysFile %s\nUsePAM no\nStrictModes yes\nSubsystem sftp internal-sftp\n", filepath.Join(dir, "sshd.pid"), hostKey, authorized))
	if err = os.WriteFile(e.sshPath(), buildSSHConfig(base, s), 0600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin")
	_ = os.MkdirAll(bin, 0700)
	shim := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\n is-active) for unit in \"$@\"; do [ \"$unit\" = is-active ] && continue; if [ \"$unit\" = ssh.service ]; then echo active; else echo inactive; fi; done;;\n show) case \"$*\" in *ExecStart*) echo /usr/sbin/sshd -D;; esac;;\n reload) kill -HUP \"$(cat '%s')\";;\n *) exit 1;;\nesac\n", filepath.Join(dir, "sshd.pid"))
	if err = os.WriteFile(filepath.Join(bin, "systemctl"), []byte(shim), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	daemon := exec.CommandContext(ctx, "/usr/sbin/sshd", "-D", "-e", "-f", e.sshPath())
	daemon.Stderr = os.Stderr
	if err = daemon.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = daemon.Process.Kill(); _ = daemon.Wait() }()
	if err = probeSSHPorts(ctx, s.Ports); err != nil {
		t.Fatal(err)
	}
	signer, _ := ssh.NewSignerFromKey(private)
	login := func(port int) error {
		client, er := ssh.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), &ssh.ClientConfig{User: "root", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.FixedHostKey(key), Timeout: 3 * time.Second})
		if er == nil {
			client.Close()
		}
		return er
	}
	if err = login(oldPort); err != nil {
		t.Fatal("initial key login", err)
	}
	s.Ports = []int{newPort}
	if _, err = e.sshOperation(ctx, model.Operation{Name: "system.ssh-apply", Params: sshParams(t, e, s)}, func(string) {}); err != nil {
		t.Fatal("apply", err)
	}
	if err = login(newPort); err != nil {
		t.Fatal("new port login", err)
	}
	if err = login(oldPort); err != nil {
		t.Fatal("old port not retained", err)
	}
	if _, err = e.sshOperation(ctx, model.Operation{Name: "system.ssh-confirm", Params: model.Config{"connected": true}}, func(string) {}); err != nil {
		t.Fatal("confirm", err)
	}
	if err = login(newPort); err != nil {
		t.Fatal("confirmed port login", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, er := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(oldPort)), 100*time.Millisecond)
		if er != nil {
			break
		}
		c.Close()
		time.Sleep(50 * time.Millisecond)
	}
	if c, er := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(oldPort)), 100*time.Millisecond); er == nil {
		c.Close()
		t.Fatal("old port still listening after confirmation")
	}
	s.Ports = []int{oldPort}
	if _, err = e.sshOperation(ctx, model.Operation{Name: "system.ssh-apply", Params: sshParams(t, e, s)}, func(string) {}); err != nil {
		t.Fatal("staged rollback", err)
	}
	p, err := e.loadSSH()
	if err != nil {
		t.Fatal(err)
	}
	p.Deadline = time.Now().Add(-time.Second).Unix()
	_ = e.saveSSH(p)
	watchCtx, stop := context.WithCancel(ctx)
	go e.watchSSH(watchCtx)
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, er := os.Stat(e.sshPendingPath()); os.IsNotExist(er) {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	stop()
	if _, er := os.Stat(e.sshPendingPath()); !os.IsNotExist(er) {
		t.Fatal("restart-safe rollback not completed")
	}
	if err = login(newPort); err != nil {
		t.Fatal("login after rollback", err)
	}
	testLinuxSSHAccountLifecycle(t, e, ctx)
	if os.Getenv("OPSVIEW_FIREWALL_SMOKE") != "true" {
		return
	}
	// The container's own network namespace is isolated from the Docker host.
	status, err := e.firewallSystem(ctx, model.Operation{Name: "system.firewall-status"}, func(string) {})
	if err != nil || !status.Bool("compatible") {
		t.Fatal("UFW environment", status, err)
	}
	if status.Bool("enabled") {
		t.Fatal("unexpected pre-existing container firewall")
	}
	if _, err = e.firewallSystem(ctx, model.Operation{Name: "system.firewall-mode", Params: model.Config{"enabled": true, "confirmedImpact": true, "source": "127.0.0.1"}}, func(line string) { t.Log(line) }); err != nil {
		t.Fatal("real UFW enable", err)
	}
	actual, _ := output(ctx, "ufw", "status", "numbered")
	if !strings.Contains(actual, strconv.Itoa(newPort)) || !strings.Contains(actual, "18790") {
		t.Fatal("missing protected rules", actual)
	}
	fw, err := e.loadFirewallMode()
	if err != nil {
		t.Fatal(err)
	}
	fw.Deadline = time.Now().Add(-time.Second).Unix()
	_ = e.saveFirewallMode(fw)
	e.reconcileFirewall(ctx)
	actual, _ = output(ctx, "ufw", "status")
	if !strings.Contains(actual, "Status: inactive") {
		t.Fatal("real UFW rollback failed", actual)
	}
	if err = login(newPort); err != nil {
		t.Fatal("SSH login after firewall rollback", err)
	}
	testLinuxSSHServiceRecovery(t, e, ctx, bin, func() error { return login(newPort) })
}

func testLinuxSSHAccountLifecycle(t *testing.T, e *Engine, ctx context.Context) {
	t.Helper()
	name := "ov-smoke-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	defer func() { _ = exec.Command("userdel", "--", name).Run(); _ = os.RemoveAll(filepath.Join("/home", name)) }()
	change := func(action, password string) {
		rows, _, err := sshLocalUsers()
		if err != nil {
			t.Fatal("read local users", err)
		}
		revision := ""
		for _, row := range rows {
			if row.String("username") == name {
				revision = row.String("revision")
			}
		}
		_, err = e.changeSSHUser(ctx, model.Config{"username": name, "operation": action, "password": password, "revision": revision, "confirmedImpact": true})
		if err != nil {
			t.Fatalf("account %s failed: %v", action, err)
		}
	}
	change("create", "")
	change("password", "OpsView-disposable-user-test-Only-2026!")
	change("lock", "")
	change("unlock", "")
	change("delete", "")
	if _, err := os.Stat(filepath.Join("/home", name)); err != nil {
		t.Fatal("account removal did not preserve home")
	}
	if _, err := output(ctx, "getent", "passwd", name); err == nil {
		t.Fatal("deleted account still exists")
	}
}

func testLinuxSSHServiceRecovery(t *testing.T, e *Engine, ctx context.Context, bin string, verify func() error) {
	t.Helper()
	pid := filepath.Join(e.Root, "sshd.pid")
	shim := fmt.Sprintf(`#!/bin/sh
case "$1" in
 is-active) for unit in "$@"; do [ "$unit" = is-active ] && continue; if [ "$unit" = ssh.service ] && [ -s '%s' ] && kill -0 "$(cat '%s')" 2>/dev/null; then echo active; else echo inactive; fi; done;;
 is-enabled) echo enabled;;
 show) case "$*" in *ExecStart*) echo /usr/sbin/sshd -D;; *LoadState*) echo loaded;; esac;;
 disable) case "$*" in *--now*) kill "$(cat '%s')"; sleep .2;; esac;;
 enable) exit 0;;
 start) /usr/sbin/sshd -f '%s';;
 reload) kill -HUP "$(cat '%s')";;
 *) exit 1;;
esac
`, pid, pid, pid, e.sshPath(), pid)
	if err := os.WriteFile(filepath.Join(bin, "systemctl"), []byte(shim), 0700); err != nil {
		t.Fatal(err)
	}
	defer func() {
		b, _ := os.ReadFile(pid)
		p, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		if p > 1 {
			_ = exec.Command("kill", strconv.Itoa(p)).Run()
		}
	}()
	if _, err := e.sshOperation(ctx, model.Operation{Name: "system.ssh-service", Params: model.Config{"operation": "stop", "confirmedImpact": true}}, func(string) {}); err != nil {
		t.Fatal("real service stop", err)
	}
	p, err := e.loadSSHService()
	if err != nil {
		t.Fatal(err)
	}
	p.Deadline = time.Now().Add(-time.Second).Unix()
	b, _ := json.Marshal(p)
	if err = os.WriteFile(e.sshServicePath(), b, 0600); err != nil {
		t.Fatal(err)
	}
	watcher, cancel := context.WithCancel(ctx)
	defer cancel()
	go e.watchSSH(watcher)
	deadline := time.Now().Add(7 * time.Second)
	recovered := false
	for time.Now().Before(deadline) {
		if _, err = os.Stat(e.sshServicePath()); os.IsNotExist(err) {
			recovered = true
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if !recovered {
		t.Fatal("real service did not recover on deadline")
	}
	if err = verify(); err != nil {
		t.Fatal("real SSH login after service recovery", err)
	}
}
