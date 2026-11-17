package executor

import (
	"context"
	"fmt"
	"net"
	"opsview/internal/model"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNginxLogReopenSmoke(t *testing.T) {
	if os.Getenv("OPSVIEW_NGINX_LOG_SMOKE") != "true" {
		t.Skip("opt in inside a disposable Nginx container")
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Fatal("requires disposable root Linux")
	}
	binary, err := exec.LookPath("nginx")
	if err != nil {
		t.Fatal(err)
	}
	e, r, path := logFixture(t, "nginx", "2026/11/17 12:00:00 [notice] initial log\n")
	base := filepath.Join(e.Root, "instances", r.ID)
	_ = os.MkdirAll(base+"/runtime/sbin", 0755)
	if err = os.Symlink(binary, base+"/runtime/sbin/nginx"); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	pid := base + "/logs/nginx.pid"
	config := fmt.Sprintf("user root root;\npid %s;\nerror_log %s notice;\nevents { worker_connections 32; }\nhttp { access_log off; server { listen 127.0.0.1:%d; return 200; } }\n", pid, path, port)
	if err = os.WriteFile(base+"/conf/nginx.conf", []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	if _, err = output(ctx, binary, "-c", base+"/conf/nginx.conf"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = output(context.Background(), binary, "-c", base+"/conf/nginx.conf", "-s", "quit") }()
	bin := t.TempDir()
	_ = os.WriteFile(bin+"/systemctl", []byte("#!/bin/sh\ncase \"$1\" in\nis-active) echo active ;;\nshow) cat \"$OPSVIEW_TEST_NGINX_PID\" ;;\n*) exit 1 ;;\nesac\n"), 0755)
	t.Setenv("OPSVIEW_TEST_NGINX_PID", pid)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	testNginxLogRotation(t, e, r)
}

// Called only by the opted-in real-systemd native smoke on disposable GitHub runners.
func testNginxLogRotation(t *testing.T, e *Engine, r model.Resource) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	path, _, _, err := e.softwareLogTarget(r, model.Config{"kind": "error"})
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(time.Now().Format("2006/01/02 15:04:05") + " [notice] opsview rotation verification\n")
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		read, err := e.softwareLogs(ctx, r, model.Config{"kind": "error"})
		if err != nil {
			t.Fatal(err)
		}
		result, err := e.manageSoftwareLog(ctx, r, model.Config{"kind": "error", "confirmed": true, "baseRevision": read.String("revision")}, "rotate-log", func(s string) { t.Log(s) })
		if err != nil {
			t.Fatal(err)
		}
		archive, err := os.ReadFile(filepath.Join(e.logArchiveDir(r, r.ID, "error"), result.String("archive")))
		if err != nil || len(archive) == 0 {
			t.Fatal("archive missing", err)
		}
		// An invalid candidate causes a real diagnostic to the newly opened log.
		base := filepath.Join(e.Root, "instances", r.ID)
		_, _ = output(ctx, base+"/runtime/sbin/nginx", "-e", path, "-c", base+"/conf/does-not-exist.conf", "-t")
		after, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(after), "does-not-exist.conf") {
			t.Fatal("Nginx did not write to the new inode", err)
		}
		active, err := output(ctx, "systemctl", "is-active", serviceName(r))
		if err != nil || strings.TrimSpace(active) != "active" {
			t.Fatal("rotation disrupted Nginx", active, err)
		}
	}
}
