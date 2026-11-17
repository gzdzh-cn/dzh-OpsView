package executor

import (
	"context"
	"encoding/json"
	"net"
	"opsview/internal/catalog"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Explicit opt-in: this test installs build dependencies inside a disposable Linux container.
func TestNativePackageSmoke(t *testing.T) {
	id := os.Getenv("OPSVIEW_NATIVE_ENTRY")
	if id == "" {
		t.Skip("set OPSVIEW_NATIVE_ENTRY in a disposable Linux container")
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Fatal("native smoke requires disposable root Linux")
	}
	entry, ok := catalog.Find(id)
	if !ok {
		t.Fatal("unknown catalog entry")
	}
	if err := catalog.Validate(entry); err != nil {
		t.Fatal(err)
	}
	if entry.Software != "nginx" && entry.Software != "php" && entry.Software != "redis" {
		t.Skip("source package smoke currently covers Nginx/PHP/Redis")
	}
	e := &Engine{Root: "/var/lib/opsview/native-smoke", allowUnverifiedNative: true}
	if err := e.Initialize(); err != nil {
		t.Fatal(err)
	}
	realSystemd := os.Getenv("OPSVIEW_NATIVE_SYSTEMD") == "true"
	if realSystemd && os.Getenv("GITHUB_ACTIONS") != "true" {
		t.Fatal("real native systemd test is limited to disposable GitHub runners")
	}
	if !realSystemd {
		bin := t.TempDir()
		for _, name := range []string{"systemctl", "systemd-analyze"} {
			if err := os.WriteFile(bin+"/"+name, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	r := model.Resource{ID: security.ID(), Kind: "instance", Name: "native-smoke", Config: model.Config{"catalogId": entry.ID, "mode": "native", "port": port, "installMethod": "source", "deferStart": !realSystemd, "password": "only-for-disposable-smoke-test"}}
	if realSystemd {
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = systemctl(cleanup, func(string) {}, "stop", serviceName(r))
			_ = systemctl(cleanup, func(string) {}, "disable", serviceName(r))
			_ = os.Remove(filepath.Join("/etc/systemd/system", serviceName(r)))
			_ = run(cleanup, func(string) {}, "", nil, nil, "systemctl", "daemon-reload")
		}()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	if _, err = e.instance(ctx, model.Operation{Name: "instance.install", Resource: r}, func(s string) { t.Log(s) }); err != nil {
		t.Fatal(err)
	}
	r, err = e.load(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(e.Root, "instances", r.ID)
	runtimePath := base + "/runtime"
	var cmd *exec.Cmd
	switch entry.Software {
	case "nginx":
		cmd = exec.CommandContext(ctx, runtimePath+"/sbin/nginx", "-c", base+"/conf/nginx.conf", "-g", "daemon off;")
	case "redis":
		cmd = exec.CommandContext(ctx, "runuser", "-u", "ov_"+r.ID[:12], "--", runtimePath+"/bin/redis-server", base+"/conf/redis.conf")
	case "php":
		cmd = exec.CommandContext(ctx, runtimePath+"/sbin/php-fpm", "--nodaemonize", "-y", base+"/conf/php-fpm.conf", "-c", base+"/conf/php.ini")
		cmd.Env = append(os.Environ(), "PHP_INI_SCAN_DIR="+base+"/conf/php.d")
	}
	if !realSystemd {
		isolateProcess(cmd)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err = cmd.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = cmd.Cancel(); _ = cmd.Wait() }()
	} else if err = e.softwareHealth(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err = waitPort(ctx, port); err != nil {
		t.Fatal(err)
	}
	switch entry.Software {
	case "redis":
		if _, err = e.redisCommand(ctx, r, "PING"); err != nil {
			t.Fatal(err)
		}
		if realSystemd {
			testRedisBackupRecovery(t, e, r)
		}
	case "php":
		if _, err = fpmStatus(ctx, phpStatusAddress(r)); err != nil {
			t.Fatal(err)
		}
		modules, er := output(ctx, runtimePath+"/bin/php", "-c", base+"/conf/php.ini", "-m")
		if er != nil {
			t.Fatal(er)
		}
		for _, module := range []string{"Zend OPcache", "fileinfo", "mbstring", "curl", "zip", "gd", "intl", "bcmath", "mysqli", "pdo_mysql", "pgsql", "pdo_pgsql"} {
			if !strings.Contains(strings.ToLower(modules), strings.ToLower(module)) {
				t.Fatalf("missing built-in extension %s", module)
			}
		}
		if realSystemd {
			testPHPIndependentSites(t, e, r)
			if err = run(ctx, func(s string) { t.Log(s) }, "", []string{"DEBIAN_FRONTEND=noninteractive"}, nil, "apt-get", "install", "-y", "--no-install-recommends", "memcached", "sasl2-bin", "libsasl2-modules"); err != nil {
				t.Fatal(err)
			}
			if _, err = e.phpExtension(ctx, r, model.Config{"name": "memcached", "operation": "install"}, true, func(s string) { t.Log(s) }); err != nil {
				t.Fatal("memcached extension recipe failed", err)
			}
			testPHPMemcachedSessions(t, e, r)
		}
	case "nginx":
		probe := r
		probe.Config = cloneConfig(r.Config)
		probe.Config["port"] = r.Config["statusPort"]
		if err = probeNginx(ctx, probe, []byte(nginxStatusServer(r.Config.Int("statusPort"), "abcdef"))); err != nil {
			t.Fatal(err)
		}
		if realSystemd {
			testNginxWebsiteOptions(t, e, r)
			testNginxLogRotation(t, e, r)
		}
	}
	if out := os.Getenv("OPSVIEW_NATIVE_OUTPUT"); out != "" {
		if err = os.MkdirAll(out, 0755); err != nil {
			t.Fatal(err)
		}
		archive := filepath.Join(out, entry.ID+"-"+strings.ReplaceAll(hostSystem(), ":", "-")+".tar.gz")
		// Materialize upstream symlinks for portable archives without hardlink entries.
		if err = run(ctx, func(s string) { t.Log(s) }, "", nil, nil, "tar", "--dereference", "--hard-dereference", "-czf", archive, "-C", runtimePath, "."); err != nil {
			t.Fatal(err)
		}
		digest, er := hashFile(archive)
		if er != nil {
			t.Fatal(er)
		}
		report := model.Config{"catalogId": entry.ID, "software": entry.Software, "version": entry.Version, "system": hostSystem(), "sha256": digest, "buildPrefix": runtimePath, "recipeRevision": entry.RecipeRevision, "protocolVerified": true, "systemdVerified": realSystemd}
		b, _ := json.MarshalIndent(report, "", "  ")
		if err = security.WriteAtomic(archive+".json", b, 0644); err != nil {
			t.Fatal(err)
		}
	}
}
