package executor

import (
	"context"
	"encoding/json"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func softwareFixture(t *testing.T) (*Engine, model.Resource, string) {
	t.Helper()
	root := t.TempDir()
	vault, err := security.OpenVault(filepath.Join(root, "executor.key"))
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{Root: root, Vault: vault}
	_ = os.MkdirAll(root+"/state", 0700)
	r := model.Resource{ID: "123456789abcde", Name: "redis-test", Kind: "instance", Status: "ready", Config: model.Config{"software": "redis", "version": "8.10.3", "mode": "native", "catalogId": "redis-8.10.3", "port": 6379, "password": "a-super-secret"}}
	_ = e.save(r)
	path := filepath.Join(root, "instances", r.ID, "conf", "redis.conf")
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	_ = os.WriteFile(path, []byte("bind 127.0.0.1\nrequirepass a-super-secret\nmaxmemory 128mb\n"), 0640)
	return e, r, path
}
func TestConfigDraftConflictAndNoCredentialExposure(t *testing.T) {
	e, r, path := softwareFixture(t)
	op := model.Operation{Name: "instance.configuration", Resource: r, Params: model.Config{}}
	doc, err := e.softwareOperation(context.Background(), op, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(doc.String("content"), "a-super-secret") {
		t.Fatal("plaintext secret in response")
	}
	op.Name = "instance.configure"
	op.Params = model.Config{"baseRevision": doc.String("revision"), "content": strings.Replace(doc.String("content"), "128mb", "256mb", 1), "apply": "draft"}
	if _, err = e.softwareOperation(context.Background(), op, func(string) {}); err != nil {
		t.Fatal(err)
	}
	live, _ := os.ReadFile(path)
	if strings.Contains(string(live), "256mb") {
		t.Fatal("draft modified running config")
	}
	draft, _ := os.ReadFile(filepath.Join(e.Root, "instances", r.ID, "history", "main", "draft.enc"))
	if !strings.HasPrefix(string(draft), "enc:") || strings.Contains(string(draft), "a-super-secret") {
		t.Fatal("unencrypted draft")
	}
	_ = os.WriteFile(path, append(live, []byte("# external\n")...), 0640)
	if _, err = e.softwareOperation(context.Background(), op, func(string) {}); err == nil {
		t.Fatal("stale config accepted")
	}
}
func TestConfigRejectsSymlinkAncestor(t *testing.T) {
	e, r, path := softwareFixture(t)
	outside := t.TempDir()
	_ = os.Rename(filepath.Dir(path), outside+"/conf")
	_ = os.Symlink(outside+"/conf", filepath.Dir(path))
	if _, err := e.softwarePath(r, "main"); err == nil {
		t.Fatal("symlink ancestor accepted")
	}
}
func TestFailedConfigApplyRestoresOldFile(t *testing.T) {
	e, r, path := softwareFixture(t)
	bin := t.TempDir()
	_ = os.WriteFile(bin+"/systemctl", []byte("#!/bin/sh\nexit 1\n"), 0755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	before, _ := os.ReadFile(path)
	_, err := e.softwareOperation(context.Background(), model.Operation{Name: "instance.configure", Resource: r, Params: model.Config{"baseRevision": security.Hash(string(before)), "values": map[string]string{"maxmemory": "256mb"}, "apply": "restart"}}, func(string) {})
	if err == nil {
		t.Fatal("failed service accepted")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("old config not restored")
	}
	files, _ := filepath.Glob(filepath.Join(e.Root, "instances", r.ID, "history", "main", "*.enc"))
	if len(files) != 1 {
		t.Fatal("encrypted history missing")
	}
}
func TestTrafficCursorHandlesPartialLinesTruncateAndRotation(t *testing.T) {
	e, _, _ := softwareFixture(t)
	r := model.Resource{ID: "site1234567890", Kind: "site"}
	dir := filepath.Join(e.Root, "sites", r.ID, "logs")
	_ = os.MkdirAll(dir, 0755)
	path := dir + "/access.json"
	now := time.Now().Format(time.RFC3339)
	line := `{"time":"` + now + `","bytes":123}` + "\n"
	_ = os.WriteFile(path, []byte(line+`{"time":`), 0644)
	a, err := e.siteTraffic(r)
	if err != nil {
		t.Fatal(err)
	}
	if a["todayBytes"] != uint64(123) {
		t.Fatal(a)
	}
	b, _ := e.siteTraffic(r)
	if b["todayBytes"] != uint64(123) {
		t.Fatal("double counted", b)
	}
	_ = os.WriteFile(path, []byte(line), 0644)
	c, _ := e.siteTraffic(r)
	if c["todayBytes"] != uint64(246) {
		t.Fatal(c)
	}
	_ = os.Rename(path, path+".1")
	_ = os.WriteFile(path, []byte(line), 0644)
	d, _ := e.siteTraffic(r)
	if d["todayBytes"] != uint64(369) {
		raw, _ := json.Marshal(d)
		t.Fatal(string(raw))
	}
}
func TestExpiryKeepsChallengeAndBlocksPublicAccess(t *testing.T) {
	e, _, _ := softwareFixture(t)
	r := model.Resource{ID: "site123", Kind: "site", Config: model.Config{"domains": "example.test", "expiresAt": time.Now().Add(-time.Minute).Format(time.RFC3339), "probeToken": "abc"}}
	inst := model.Resource{Config: model.Config{"port": 80}}
	text := e.renderSite(r, inst, "/managed/site123")
	if !strings.Contains(text, "location / { return 403;") || !strings.Contains(text, "/.well-known/acme-challenge/") {
		t.Fatal(text)
	}
}

func TestRotationDrainsUnreadOldLog(t *testing.T) {
	e, _, _ := softwareFixture(t)
	site := model.Resource{ID: "site-rotation", Kind: "site"}
	dir := filepath.Join(e.Root, "sites", site.ID, "logs")
	_ = os.MkdirAll(dir, 0755)
	path := dir + "/access.json"
	line := `{"time":"` + time.Now().Format(time.RFC3339) + `","bytes":100}` + "\n"
	_ = os.WriteFile(path, []byte(line), 0644)
	_, _ = e.siteTraffic(site)
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	_, _ = f.WriteString(line)
	_ = f.Close()
	_ = os.Rename(path, path+".1")
	_ = os.WriteFile(path, []byte(line), 0644)
	out, err := e.siteTraffic(site)
	if err != nil || out["todayBytes"] != uint64(300) {
		t.Fatal(out, err)
	}
	out, err = e.siteTraffic(site)
	if err != nil || out["todayBytes"] != uint64(300) {
		t.Fatal("rotation counted twice", out, err)
	}
}
func TestPHPConfigScopeRejectsUnrelatedSite(t *testing.T) {
	e, r, _ := softwareFixture(t)
	r.Config["software"] = "php"
	_ = e.save(r)
	site := model.Resource{ID: "unrelated-site", Kind: "site", Config: model.Config{"phpInstanceId": "another-instance"}}
	_ = e.save(site)
	_, err := e.softwareOperation(context.Background(), model.Operation{Name: "instance.configuration", Resource: r, Params: model.Config{"siteId": site.ID}}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "不属于") {
		t.Fatal(err)
	}
}
func TestNginxStatusListenerIsLoopback(t *testing.T) {
	text := nginxStatusServer(20080, "abc123")
	if !strings.Contains(text, "listen 127.0.0.1:20080;") || !strings.Contains(text, "stub_status;") || !strings.Contains(text, "X-OpsView-Revision abc123") {
		t.Fatal(text)
	}
}

func TestRedisPortRollbackPreservesCredentialPermissions(t *testing.T) {
	e, r, path := softwareFixture(t)
	before := []byte("bind 127.0.0.1\nport 6379\nrequirepass a-super-secret\n")
	_ = os.WriteFile(path, before, 0640)
	bin := t.TempDir()
	_ = os.WriteFile(bin+"/systemctl", []byte("#!/bin/sh\nexit 1\n"), 0755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	_, err := e.softwarePort(context.Background(), r, model.Config{"port": 6401, "baseRevision": security.Hash(string(before))}, func(string) {})
	if err == nil {
		t.Fatal("failed restart accepted")
	}
	after, _ := os.ReadFile(path)
	st, _ := os.Stat(path)
	if string(after) != string(before) || st.Mode().Perm() != 0640 {
		t.Fatal("credential config permissions or contents changed", st.Mode())
	}
}

func TestManagedFileSnapshotRestoresPrivateModeAndRemovesNewFiles(t *testing.T) {
	dir := t.TempDir()
	oldPath, newPath := filepath.Join(dir, "php.ini"), filepath.Join(dir, "fpm.conf")
	if err := os.WriteFile(oldPath, []byte("private-session-credential"), 0600); err != nil {
		t.Fatal(err)
	}
	undo, err := snapshotManagedFiles([]string{oldPath, newPath})
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(oldPath, []byte("candidate"), 0600)
	_ = os.Chmod(oldPath, 0644)
	_ = os.WriteFile(newPath, []byte("new pool"), 0644)
	if err = undo(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(oldPath)
	st, _ := os.Stat(oldPath)
	if string(got) != "private-session-credential" || st.Mode().Perm() != 0600 {
		t.Fatal("snapshot failed to restore private document")
	}
	if _, err = os.Stat(newPath); !os.IsNotExist(err) {
		t.Fatal("new document retained")
	}
}

func TestTrafficLargeRotatedTailIsDrainedAcrossPolls(t *testing.T) {
	e, _, _ := softwareFixture(t)
	site := model.Resource{ID: "site-large-rotation", Kind: "site"}
	dir := filepath.Join(e.Root, "sites", site.ID, "logs")
	_ = os.MkdirAll(dir, 0755)
	path := dir + "/access.json"
	line := `{"time":"` + time.Now().Format(time.RFC3339) + `","bytes":1}` + "\n"
	_ = os.WriteFile(path, []byte(line), 0644)
	if _, err := e.siteTraffic(site); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	const count = 110000
	if _, err = f.WriteString(strings.Repeat(line, count)); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	_ = os.Rename(path, path+".1")
	_ = os.WriteFile(path, []byte(line), 0644)
	var result model.Config
	for i := 0; i < 5; i++ {
		result, err = e.siteTraffic(site)
		if err != nil {
			t.Fatal(err)
		}
	}
	if result["todayBytes"] != uint64(count+2) {
		t.Fatal("lost or repeated rotated bytes", result["todayBytes"])
	}
}

func TestPHPReloadRejectsPausedVersionAndExpiredSite(t *testing.T) {
	e, _, _ := softwareFixture(t)
	php := model.Resource{ID: "php1234567890", Kind: "instance", Config: model.Config{"software": "php", "mode": "native", "servicesPaused": true}}
	if _, err := e.controlPHP(context.Background(), php, "reload", model.Config{}, func(string) {}); err == nil {
		t.Fatal("reloaded paused version")
	}
	site := model.Resource{ID: "site1234567890", Kind: "site", Config: model.Config{"phpInstanceId": php.ID, "expiresAt": time.Now().Add(-time.Hour).Format(time.RFC3339)}}
	if err := e.save(site); err != nil {
		t.Fatal(err)
	}
	if _, err := e.controlPHP(context.Background(), php, "restart", model.Config{"siteId": site.ID}, func(string) {}); err == nil {
		t.Fatal("restarted expired site")
	}
}

func TestRedisSlowLogRedactsArgumentsAndSortsDuration(t *testing.T) {
	entries, err := redactRedisSlowLog([]byte(`[[1,1700000000,5,["SET","password","secret-value"],"private-client","name"],[2,1700000001,50,["AUTH","another-secret"]]]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].String("command") != "AUTH" {
		t.Fatal(entries)
	}
	b, _ := json.Marshal(entries)
	for _, secret := range []string{"secret-value", "another-secret", "private-client", "password"} {
		if strings.Contains(string(b), secret) {
			t.Fatal("slowlog leaked argument or client")
		}
	}
}

func TestMySQLDynamicApplyRejectsStaticAndRollsBackPartialFailure(t *testing.T) {
	e, r, _ := softwareFixture(t)
	r.Config["software"] = "mysql"
	r.Config["version"] = "8.4.13"
	base := filepath.Join(e.Root, "instances", r.ID)
	_ = os.MkdirAll(base+"/runtime/bin", 0755)
	old := "[mysqld]\nmax_connections=151\ntable_open_cache=2000\nthread_stack=262144\n"
	path := base + "/conf/my.cnf"
	_ = os.WriteFile(path, []byte(old), 0640)
	trace := filepath.Join(t.TempDir(), "sql")
	t.Setenv("OPSVIEW_TEST_SQL_TRACE", trace)
	script := `#!/bin/sh
IFS= read -r query || true
printf '%s\n' "$query" >> "$OPSVIEW_TEST_SQL_TRACE"
case "$query" in
 'SELECT @@GLOBAL.max_connections;') echo 151;;
 'SELECT @@GLOBAL.table_open_cache;') echo 2000;;
 'SET GLOBAL table_open_cache=3000;') exit 1;;
esac
`
	_ = os.WriteFile(base+"/runtime/bin/mysql", []byte(script), 0755)
	next := strings.Replace(old, "thread_stack=262144", "thread_stack=524288", 1)
	if _, err := e.applyMySQLConfig(context.Background(), r, path, old, next, func(string) {}); err == nil {
		t.Fatal("static parameter dynamically applied")
	}
	if _, err := os.Stat(trace); !os.IsNotExist(err) {
		t.Fatal("rejection changed runtime values")
	}
	next = strings.Replace(strings.Replace(old, "max_connections=151", "max_connections=200", 1), "table_open_cache=2000", "table_open_cache=3000", 1)
	if _, err := e.applyMySQLConfig(context.Background(), r, path, old, next, func(string) {}); err == nil {
		t.Fatal("partial runtime failure accepted")
	}
	queries, _ := os.ReadFile(trace)
	if !strings.Contains(string(queries), "SET GLOBAL max_connections=151;") {
		t.Fatal("previous dynamic value not restored", string(queries))
	}
	actual, _ := os.ReadFile(path)
	st, _ := os.Stat(path)
	if string(actual) != old || st.Mode().Perm() != 0640 {
		t.Fatal("failed dynamic apply modified configuration")
	}
}
func TestMySQLNumericLiteralCannotContainSQL(t *testing.T) {
	for _, v := range []string{"1;DROP DATABASE x", "1e100", "1\\nSET x=1", "18446744073709551615G", "-1", "NaN"} {
		if _, err := mysqlNumericValue(v); err == nil {
			t.Fatal("accepted unsafe numeric value", v)
		}
	}
	if v, err := mysqlNumericValue("128M"); err != nil || v != "134217728" {
		t.Fatal(v, err)
	}
}
