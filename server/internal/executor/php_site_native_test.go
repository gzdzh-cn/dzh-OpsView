package executor

import (
	"context"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testPHPIndependentSites(t *testing.T, e *Engine, inst model.Resource) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	versionBase := filepath.Join(e.Root, "instances", inst.ID)
	doc, err := e.phpSession(ctx, inst, model.Config{"operation": "inspect"}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.phpSession(ctx, inst, model.Config{"operation": "save", "handler": "files", "baseRevision": doc.String("revision"), "confirmed": true}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		site := model.Resource{ID: security.ID(), Kind: "site", Name: "php-isolation-smoke", Config: model.Config{"phpInstanceId": inst.ID}}
		base := filepath.Join(e.Root, "sites", site.ID)
		unit := phpSiteUnit(site)
		t.Cleanup(func() {
			cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
			defer stop()
			_ = systemctl(cleanup, func(string) {}, "stop", unit)
			_ = systemctl(cleanup, func(string) {}, "disable", unit)
			_ = os.Remove(filepath.Join("/etc/systemd/system", unit))
			_ = run(cleanup, func(string) {}, "", nil, nil, "systemctl", "daemon-reload")
		})
		if err = e.setupSitePHP(ctx, site, base, func(s string) { t.Log(s) }); err != nil {
			t.Fatal(err)
		}
		ini, err := os.ReadFile(base + "/conf/php.ini")
		if err != nil {
			t.Fatal(err)
		}
		if phpIniValue(string(ini), "session.save_path", "") != base+"/sessions" || phpIniValue(string(ini), "error_log", "") != base+"/logs/php-error.log" {
			t.Fatal("site inherited version writable directories")
		}
		var statusErr error
		for attempt := 0; attempt < 50; attempt++ {
			_, statusErr = fpmStatus(ctx, base+"/run/php.sock")
			if statusErr == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if statusErr != nil {
			t.Fatal("independent FPM did not serve its private socket", statusErr)
		}
		// Exercise file permissions using the same unprivileged identity as the FPM worker.
		user := "ovs_" + site.ID[:12]
		_, err = output(ctx, "runuser", "-u", user, "--", versionBase+"/runtime/bin/php", "-n", "-d", "session.save_path="+base+"/sessions", "-d", "error_log="+base+"/logs/php-error.log", "-r", `session_id('smoke-session'); if (!session_start()) exit(1); $_SESSION['smoke']='isolated'; session_write_close(); error_log('isolated-site-log');`)
		if err != nil {
			t.Fatal("site worker could not write its own files", err)
		}
		if b, err := os.ReadFile(base + "/sessions/sess_smoke-session"); err != nil || !strings.Contains(string(b), "isolated") {
			t.Fatal("site session missing", err)
		}
		if b, err := os.ReadFile(base + "/logs/php-error.log"); err != nil || !strings.Contains(string(b), "isolated-site-log") {
			t.Fatal("site error log missing", err)
		}
		if st, err := os.Stat(base + "/sessions"); err != nil || st.Mode().Perm() != 0700 {
			t.Fatal("site sessions are not private", err)
		}
	}
	if _, err := os.Stat(versionBase + "/sessions/sess_smoke-session"); !os.IsNotExist(err) {
		t.Fatal("site session leaked into the version directory")
	}
	t.Log("PASS independent PHP FPM sockets, private per-site Sessions and writable error logs")
}
