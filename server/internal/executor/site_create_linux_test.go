//go:build linux

package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Runs only on an explicitly opted-in disposable systemd host, never a customer server.
func TestLinuxWebsiteCreation(t *testing.T) {
	if os.Getenv("OPSVIEW_SITE_SMOKE") != "true" || os.Getenv("GITHUB_ACTIONS") != "true" {
		t.Skip("disposable GitHub systemd host required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	root, err := os.MkdirTemp("/var/tmp", "opsview-site-smoke-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	e := &Engine{Root: root}
	if err = e.Initialize(); err != nil {
		t.Fatal(err)
	}
	log := func(s string) { t.Log(s) }
	inst := model.Resource{ID: security.ID(), Kind: "instance", Name: "nginx-test", Status: "ready", Config: model.Config{"software": "nginx", "version": "1.24.0", "catalogId": "nginx-1.24.0", "installMethod": "binary", "mode": "native", "port": unusedSessionPort(t)}}
	base := filepath.Join(root, "instances", inst.ID)
	for _, p := range []string{"runtime/sbin", "runtime/conf", "logs"} {
		if err = os.MkdirAll(filepath.Join(base, p), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.Symlink("/usr/sbin/nginx", base+"/runtime/sbin/nginx"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("/etc/nginx/fastcgi_params")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(base+"/runtime/conf/fastcgi_params", b, 0644)
	if err = e.writeNginxBase(inst, base); err != nil {
		t.Fatal(err)
	}
	if err = e.save(inst); err != nil {
		t.Fatal(err)
	}
	unitBody := fmt.Sprintf("Type=forking\nPIDFile=%s/logs/nginx.pid\nExecStart=%s/runtime/sbin/nginx -p %s/runtime/ -c %s/conf/nginx.conf\nExecReload=%s/runtime/sbin/nginx -p %s/runtime/ -c %s/conf/nginx.conf -s reload\nExecStop=%s/runtime/sbin/nginx -p %s/runtime/ -c %s/conf/nginx.conf -s quit\n", base, base, base, base, base, base, base, base, base, base)
	if err = installUnit(ctx, inst, unitBody, log); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupSiteUnit(t, serviceName(inst)) })
	request := func(domain, path string) (int, string, http.Header) {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://127.0.0.1:%d%s", inst.Config.Int("port"), path), nil)
		req.Host = domain
		res, er := (&http.Client{Timeout: 3 * time.Second}).Do(req)
		if er != nil {
			t.Fatal(er)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b), res.Header
	}
	newSite := func(kind string) model.Resource {
		id := security.ID()
		domain := "smoke-" + id + ".test"
		dir := "/www/wwwroot/" + domain
		t.Cleanup(func() { os.RemoveAll(dir) })
		return model.Resource{ID: id, Kind: "site", Name: domain, Config: model.Config{"type": kind, "domains": domain, "instanceId": inst.ID, "securityHeaders": true}}
	}
	oldMask := syscall.Umask(0027)
	defer syscall.Umask(oldMask)
	static := newSite("static")
	if _, err = e.Execute(ctx, model.Operation{Name: "site.apply", Resource: static}, log); err != nil {
		t.Fatal(err)
	}
	stored, _ := e.load(static.ID)
	doc := stored.Config.String("documentRoot")
	if doc != "/www/wwwroot/"+static.Name {
		t.Fatal("wrong actual root", doc)
	}
	security.WriteAtomic(doc+"/index.html", []byte("new-root-content"), 0644)
	security.WriteAtomic(doc+"/.env", []byte("must-not-leak"), 0644)
	security.WriteAtomic(doc+"/backup.sql", []byte("must-not-leak"), 0644)
	if code, body, h := request(static.Name, "/"); code != 200 || body != "new-root-content" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal(code, body, h)
	}
	for _, p := range []string{"/.env", "/backup.sql"} {
		if code, body, _ := request(static.Name, p); code != 403 || strings.Contains(body, "must-not-leak") {
			t.Fatal(code, body)
		}
	}
	security.WriteAtomic(doc+"/secret.PHP", []byte("<?php must-not-leak"), 0644)
	if code, body, _ := request(static.Name, "/secret.PHP"); code != 403 || strings.Contains(body, "must-not-leak") {
		t.Fatal("PHP source exposed", code)
	}
	if code, body, _ := request(static.Name, "/missing"); code != 404 || !strings.Contains(body, "页面未找到") {
		t.Fatal("not a real custom 404", code, body)
	}
	result, err := e.siteSecurity(stored)
	if err != nil || !strings.Contains(fmt.Sprint(result["checks"]), ".env") {
		t.Fatal(result, err)
	}
	// Existing paths are preserved and forged metadata cannot redirect server reads.
	stored.Config["documentRoot"] = "/etc"
	if got, err := e.trustedSiteRoot(stored); err != nil || got != doc {
		t.Fatal(got, err)
	}
	// A second resource must never claim the same directory, including adoption mode.
	duplicate := newSite("static")
	duplicate.Config["rootDirectory"] = doc
	duplicate.Config["adoptDirectory"] = true
	if _, err = e.Execute(ctx, model.Operation{Name: "site.apply", Resource: duplicate}, log); err == nil {
		t.Fatal("duplicate project root accepted")
	}
	// Real isolated PHP service serves a script from the new root.
	php := model.Resource{ID: security.ID(), Kind: "instance", Name: "php-test", Config: model.Config{"software": "php", "mode": "native"}}
	pb := filepath.Join(root, "instances", php.ID)
	for _, p := range []string{"runtime/sbin", "conf/php.d"} {
		os.MkdirAll(filepath.Join(pb, p), 0755)
	}
	for _, p := range []string{pb, pb + "/runtime", pb + "/runtime/sbin"} {
		os.Chmod(p, 0755)
	}
	os.Symlink("/usr/sbin/php-fpm8.3", pb+"/runtime/sbin/php-fpm")
	os.WriteFile(pb+"/conf/php.ini", []byte("[PHP]\n"), 0644)
	e.save(php)
	phpsite := newSite("php")
	phpsite.Config["phpInstanceId"] = php.ID
	phpsite.Config["denyUploadPHP"] = true
	t.Cleanup(func() {
		cleanupSiteUnit(t, phpSiteUnit(phpsite))
		output(context.Background(), "userdel", "ovs_"+phpsite.ID[:12])
	})
	if _, err = e.Execute(ctx, model.Operation{Name: "site.apply", Resource: phpsite}, log); err != nil {
		t.Fatal(err)
	}
	pdir := "/www/wwwroot/" + phpsite.Name
	security.WriteAtomic(pdir+"/index.php", []byte("<?php echo 'php-new-root';"), 0644)
	if code, body, _ := request(phpsite.Name, "/index.php"); code != 200 || body != "php-new-root" {
		t.Fatal(code, body)
	}
	os.MkdirAll(pdir+"/uploads", 0755)
	os.Chmod(pdir+"/uploads", 0755)
	security.WriteAtomic(pdir+"/uploads/test.php", []byte("<?php echo 'forbidden';"), 0644)
	if code, _, _ := request(phpsite.Name, "/uploads/test.php"); code != 403 {
		t.Fatal(code)
	}
	phpStored, _ := e.load(phpsite.ID)
	phpStored.Config["type"] = "static"
	if _, err = e.Execute(ctx, model.Operation{Name: "site.apply", Resource: phpStored}, log); err != nil {
		t.Fatal("PHP to static", err)
	}
	if code, body, _ := request(phpsite.Name, "/index.php"); code != 403 || strings.Contains(body, "<?php") {
		t.Fatal("PHP source leaked", code, body)
	}
	if active, _ := output(ctx, "systemctl", "is-active", phpSiteUnit(phpsite)); strings.TrimSpace(active) == "active" {
		t.Fatal("FPM still active")
	}
	if enabled, _ := output(ctx, "systemctl", "is-enabled", phpSiteUnit(phpsite)); strings.TrimSpace(enabled) == "enabled" {
		t.Fatal("FPM still enabled")
	}
	phpStored, _ = e.load(phpsite.ID)
	phpStored.Config["type"] = "php"
	phpStored.Config["phpInstanceId"] = php.ID
	if _, err = e.Execute(ctx, model.Operation{Name: "site.apply", Resource: phpStored}, log); err != nil {
		t.Fatal("static to PHP", err)
	}
	if code, body, _ := request(phpsite.Name, "/index.php"); code != 200 || body != "php-new-root" {
		t.Fatal("PHP restoration", code, body)
	}
	// Managed process starts as a non-root user and is proxied on its loopback port.
	app := newSite("managed")
	port := unusedSessionPort(t)
	app.Config["port"] = port
	app.Config["executable"] = "/usr/bin/python3"
	app.Config["arguments"] = fmt.Sprintf(`["-m","http.server","%d","--bind","127.0.0.1"]`, port)
	app.Config["autoStart"] = false
	app.Config["healthURL"] = fmt.Sprintf("http://127.0.0.1:%d/", port)
	proc := managedSiteProcess(app)
	unit := serviceName(proc)
	t.Cleanup(func() { cleanupSiteUnit(t, unit); output(context.Background(), "userdel", "ovs_"+app.ID[:12]) })
	if _, err = e.Execute(ctx, model.Operation{Name: "site.apply", Resource: app}, log); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := request(app.Name, "/"); code != 200 {
		t.Fatal(code)
	}
	uid, _ := output(ctx, "systemctl", "show", unit, "--property=User", "--value")
	if strings.TrimSpace(uid) != "ovs_"+app.ID[:12] {
		t.Fatal("wrong process user", uid)
	}
	if enabled, _ := output(ctx, "systemctl", "is-enabled", unit); strings.TrimSpace(enabled) != "disabled" {
		t.Fatal("autostart not disabled", enabled)
	}
	// A failed replacement restores the old running service and nginx config.
	oldApp, _ := e.load(app.ID)
	broken := oldApp
	broken.Config = cloneConfig(oldApp.Config)
	broken.Config["executable"] = "/usr/bin/false"
	failed, cancelFail := context.WithTimeout(ctx, 4*time.Second)
	_, err = e.Execute(failed, model.Operation{Name: "site.apply", Resource: broken}, log)
	cancelFail()
	if err == nil {
		t.Fatal("broken process accepted")
	}
	if err = waitPort(ctx, port); err != nil {
		t.Fatal("old service not restored", err)
	}
	if code, _, _ := request(app.Name, "/"); code != 200 {
		t.Fatal("rollback lost proxy", code)
	}
	// Expiry stops only this dedicated process and removes its boot activation.
	oldApp.Config["expiresAt"] = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	e.save(oldApp)
	e.enforceSiteExpiry(ctx)
	active, _ := output(ctx, "systemctl", "is-active", unit)
	if strings.TrimSpace(active) == "active" {
		t.Fatal("expired process still active")
	}
	if code, _, _ := request(app.Name, "/"); code != 403 {
		t.Fatal("expired site exposed", code)
	}
	beforeSites := []model.Resource{}
	for _, r := range e.states() {
		if r.Kind == "site" {
			beforeSites = append(beforeSites, r)
		}
	}
	replacement, err := e.Execute(ctx, model.Operation{Name: "instance.switch-version", Resource: inst, Params: model.Config{"catalogId": "nginx-1.30.6", "installMethod": "binary", "maintenanceConfirmed": true}}, log)
	if err != nil {
		t.Fatal("in-place Nginx replacement", err)
	}
	if replacement.String("version") != "1.30.6" {
		t.Fatal(replacement)
	}
	if code, body, _ := request(static.Name, "/"); code != 200 || body != "new-root-content" {
		t.Fatal("site lost after replacement", code, body)
	}
	for _, r := range beforeSites {
		now, er := e.load(r.ID)
		if er != nil || now.Config.String("instanceId") != inst.ID {
			t.Fatal("site association changed", er)
		}
	}
	if _, er := os.Stat(e.nginxRecovery(inst.ID) + "/runtime/sbin/nginx"); er != nil {
		t.Fatal("fallback not retained", er)
	}

	// Deletion uses real Nginx validation/reload and removes only owned units.
	backup := model.Resource{ID: security.ID(), Kind: "backup", Name: "preserved-site", Config: model.Config{"targetId": static.ID, "destination": "local", "retention": 7}}
	if _, err = e.Execute(ctx, model.Operation{Name: "backup.backup", Resource: backup}, log); err != nil {
		t.Fatal("prepare retained site backup", err)
	}
	if _, err = e.Execute(ctx, model.Operation{Name: "site.delete", Resource: static, Params: model.Config{"deleteDirectory": true, "deleteLogs": true}}, log); err != nil {
		t.Fatal("real site recycle", err)
	}
	if code, _, _ := request(static.Name, "/"); code != 404 {
		t.Fatal("deleted site still served", code)
	}
	index, err := e.trashIndex()
	if err != nil {
		t.Fatal(err)
	}
	restoredDirectory := false
	for id, item := range index {
		if item.Original == doc {
			if _, err = e.fileTrash(model.Config{"action": "restore", "id": id}); err != nil {
				t.Fatal(err)
			}
			restoredDirectory = true
		}
	}
	if !restoredDirectory {
		t.Fatal("missing directory recycle record")
	}
	if b, err := os.ReadFile(doc + "/index.html"); err != nil || string(b) != "new-root-content" {
		t.Fatal("recycle restoration", err)
	}
	if _, err = e.Execute(ctx, model.Operation{Name: "backup.restore", Resource: backup}, log); err != nil {
		t.Fatal("backup unusable after site removal", err)
	}
	// Legacy interrupted deletion: the vhost and executor state are gone, but
	// the site's generated FPM unit remains. Recover only this verified unit.
	phpStored, _ = e.load(phpsite.ID)
	if err = os.Remove(filepath.Join(base, "conf", "sites", phpsite.ID+".conf")); err != nil {
		t.Fatal(err)
	}
	if err = e.nginx(ctx, inst, log, "-s", "reload"); err != nil {
		t.Fatal(err)
	}
	if err = e.forget(phpsite.ID); err != nil {
		t.Fatal(err)
	}
	preview, err := e.inspectSiteDeletion(phpStored)
	if err != nil || !strings.Contains(preview.String("phase"), "已核验归属") {
		t.Fatal("legacy FPM preview", preview, err)
	}
	if _, err = e.Execute(ctx, model.Operation{Name: "site.delete", Resource: phpStored}, log); err != nil {
		t.Fatal("legacy PHP site cleanup", err)
	}
	if _, err = os.Lstat(filepath.Join("/etc/systemd/system", phpSiteUnit(phpsite))); !os.IsNotExist(err) {
		t.Fatal("legacy FPM unit not removed", err)
	}
	if active, _ := output(ctx, "systemctl", "is-active", phpSiteUnit(phpsite)); strings.TrimSpace(active) == "active" {
		t.Fatal("deleted FPM still active")
	}
	if _, err = os.Stat(pdir + "/index.php"); err != nil {
		t.Fatal("default PHP deletion lost files", err)
	}
	if _, err = e.Execute(ctx, model.Operation{Name: "site.delete", Resource: app}, log); err != nil {
		t.Fatal("managed site cleanup", err)
	}
	if active, _ := output(ctx, "systemctl", "is-active", unit); strings.TrimSpace(active) == "active" {
		t.Fatal("deleted managed process still active")
	}
	t.Log("PASS real Nginx/PHP/systemd: /www/wwwroot, protection, isolated process, rollback and expiry")
	testWebsiteSettings(t, e, inst)

}
func cleanupSiteUnit(t *testing.T, unit string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	systemctl(ctx, func(string) {}, "stop", unit)
	systemctl(ctx, func(string) {}, "disable", unit)
	os.Remove(filepath.Join("/etc/systemd/system", unit))
	output(ctx, "systemctl", "daemon-reload")
}
