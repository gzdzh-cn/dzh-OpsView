package executor

import (
	"context"
	"fmt"
	"net"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This opt-in protocol test uses distribution PHP inside a disposable container.
// It does not qualify new catalog versions or native installation combinations.
func TestPHPSessionMemcachedSmoke(t *testing.T) {
	if os.Getenv("OPSVIEW_PHP_SESSION_SMOKE") != "true" {
		t.Skip("set OPSVIEW_PHP_SESSION_SMOKE in a disposable root Linux container")
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Fatal("requires disposable root Linux")
	}
	root, err := os.MkdirTemp("", "opsview-session-smoke-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	e := &Engine{Root: root}
	if err := e.Initialize(); err != nil {
		t.Fatal(err)
	}
	r := model.Resource{ID: security.ID(), Kind: "instance", Name: "session-smoke", Status: "ready", Config: model.Config{"software": "php", "mode": "native", "version": "8.3.0", "port": unusedSessionPort(t)}}
	base := filepath.Join(e.Root, "instances", r.ID)
	for _, dir := range []string{"conf/php.d", "runtime/bin", "logs", "sessions"} {
		if err := os.MkdirAll(filepath.Join(base, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	// Runtime wrapper selects the distribution's real extension configuration.
	php := "#!/bin/sh\nexport PHP_INI_SCAN_DIR=/etc/php/8.3/cli/conf.d\nexec /usr/bin/php \"$@\"\n"
	if err := os.WriteFile(base+"/runtime/bin/php", []byte(php), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base+"/conf/php.ini", []byte("[PHP]\nerror_log = "+base+"/logs/error.log\n[Session]\nsession.save_handler=files\nsession.save_path="+base+"/sessions\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fpm := fmt.Sprintf("[global]\nerror_log=%s/logs/fpm.log\n[www]\nuser=nobody\ngroup=nogroup\nlisten=127.0.0.1:%d\nlisten.allowed_clients=127.0.0.1\npm=ondemand\npm.max_children=2\npm.status_path=/opsview-fpm-status\n", base, r.Config.Int("port"))
	if err := os.WriteFile(base+"/conf/php-fpm.conf", []byte(fpm), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/sbin/php-fpm8.3", "--nodaemonize", "-y", base+"/conf/php-fpm.conf", "-c", base+"/conf/php.ini")
	cmd.Env = append(os.Environ(), "PHP_INI_SCAN_DIR=/etc/php/8.3/cli/conf.d")
	isolateProcess(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Cancel(); _ = cmd.Wait() }()
	if err := waitFPM(ctx, phpStatusAddress(r)); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	shim := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\nis-active) echo active;;\nreload) kill -USR2 %d;;\n*) exit 1;;\nesac\n", cmd.Process.Pid)
	if err := os.WriteFile(bin+"/systemctl", []byte(shim), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	if err := e.save(r); err != nil {
		t.Fatal(err)
	}
	testPHPMemcachedSessions(t, e, r)
}

func unusedSessionPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func testPHPMemcachedSessions(t *testing.T, e *Engine, r model.Resource) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	bin, err := exec.LookPath("memcached")
	if err != nil {
		t.Fatal(err)
	}
	port := unusedSessionPort(t)
	start := func(sasl bool, conf string) *exec.Cmd {
		args := []string{"-l", "127.0.0.1", "-p", strconv.Itoa(port), "-U", "0", "-u", "root", "-B", "binary"}
		if sasl {
			args = append(args, "-S")
		}
		cmd := exec.CommandContext(ctx, bin, args...)
		if sasl {
			cmd.Env = append(os.Environ(), "SASL_CONF_PATH="+conf)
		}
		isolateProcess(cmd)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		if err := waitPort(ctx, port); err != nil {
			_ = cmd.Cancel()
			_ = cmd.Wait()
			t.Fatal(err)
		}
		return cmd
	}
	cmd := start(false, "")
	defer func() {
		if cmd != nil {
			_ = cmd.Cancel()
			_ = cmd.Wait()
		}
	}()
	path := filepath.Join(e.Root, "instances", r.ID, "conf", "php.ini")
	save := func(p model.Config) error {
		old, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		p["operation"], p["handler"], p["confirmed"], p["baseRevision"], p["memcachedPort"] = "save", "memcached", true, security.Hash(string(old)), port
		_, err = e.softwareOperation(ctx, model.Operation{Name: "instance.session", Resource: r, Params: p}, func(s string) { t.Log(s) })
		return err
	}
	if err := save(model.Config{}); err != nil {
		t.Fatal("real memcached session round trip failed", err)
	}
	_ = cmd.Cancel()
	_ = cmd.Wait()
	cmd = nil
	before, _ := os.ReadFile(path)
	if err := save(model.Config{}); err == nil {
		t.Fatal("stopped memcached accepted")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("connection failure replaced running config")
	}

	// A private disposable SASL database verifies correct and incorrect credentials.
	sasl := t.TempDir()
	db := filepath.Join(sasl, "sasldb")
	if err := os.WriteFile(filepath.Join(sasl, "memcached.conf"), []byte("mech_list: plain\nsasldb_path: "+db+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	password := security.ID() + security.ID()
	if err := run(ctx, func(string) {}, "", nil, strings.NewReader(password+"\n"), "saslpasswd2", "-p", "-c", "-f", db, "-a", "memcached", "opsview-smoke"); err != nil {
		t.Fatal("could not prepare private SASL test account")
	}
	cmd = start(true, sasl)
	p := model.Config{"saslEnabled": true, "saslUsername": "opsview-smoke", "saslPassword": password}
	if err := save(p); err != nil {
		t.Fatal("SASL memcached session round trip failed", err)
	}
	before, _ = os.ReadFile(path)
	p["saslPassword"] = "wrong-disposable-password"
	if err := save(p); err == nil {
		t.Fatal("incorrect SASL credentials accepted")
	}
	after, _ = os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("incorrect credentials modified active config")
	}
	p["saslPassword"] = "••••••••"
	if err := save(p); err != nil {
		t.Fatal("masked existing SASL password not preserved", err)
	}
	// Candidates for two sites use the same backend and ID but separate prefixes.
	for _, scope := range []string{"first-scope", "second-scope"} {
		ini := sitePHPIni(string(before), filepath.Join(e.Root, "sites", scope))
		candidate := filepath.Join(sasl, scope+".ini")
		if err := os.WriteFile(candidate, []byte(ini), 0600); err != nil {
			t.Fatal(err)
		}
		code := `session_id('opsview-isolation'); session_start(); `
		if scope == "first-scope" {
			code += `$_SESSION['private']='first-scope'; session_write_close();`
		} else {
			code += `$ok=!isset($_SESSION['private']); session_destroy(); exit($ok?0:1);`
		}
		if err := run(ctx, func(string) {}, "", nil, nil, filepath.Join(e.Root, "instances", r.ID, "runtime", "bin", "php"), "-c", candidate, "-d", "log_errors=0", "-d", "display_errors=0", "-r", code); err != nil {
			t.Fatal("site namespace isolation failed", err)
		}
	}
	t.Log("PASS real PHP memcached sessions, SASL, wrong-credential rejection, masked credential reuse, failure preservation and site namespace isolation")
}
