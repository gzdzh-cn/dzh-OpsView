package executor

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSitePHPIniKeepsFilesAndLogsInsideEachSite(t *testing.T) {
	text := "[PHP]\n; retained comment\nerror_log = /version/logs/error.log\n[Session]\nsession.save_handler = \"files\" ; local store\nsession.save_path = /version/sessions\n"
	for _, base := range []string{"/managed/site-a", "/managed/site-b"} {
		result := sitePHPIni(text, base)
		if phpIniValue(result, "session.save_path", "") != base+"/sessions" || phpIniValue(result, "error_log", "") != base+"/logs/php-error.log" || !strings.Contains(result, "; retained comment") {
			t.Fatal("site did not isolate its writable paths", result)
		}
		if sitePHPIni(result, base) != result {
			t.Fatal("normalization changed an already managed document")
		}
	}
	redis := "[Session]\nsession.save_handler = 'redis' ; enabled\nsession.save_path = \"tcp://127.0.0.1:6379?auth=hidden\"\n"
	if got := phpIniValue(sitePHPIni(redis, "/managed/site"), "session.save_path", ""); !strings.HasPrefix(got, "tcp://127.0.0.1:6379?") || !strings.Contains(got, "auth=hidden") || !strings.Contains(got, "prefix=ov.site.") {
		t.Fatal("Redis connection or private namespace lost", got)
	}
	if phpIniValue("; session.save_handler = redis\n", "session.save_handler", "files") != "files" {
		t.Fatal("comment was treated as effective configuration")
	}
}

func TestMemcachedSessionSettingsAndPrivateNamespace(t *testing.T) {
	legacy := "[Session]\nsession.save_handler=redis\nsession.save_path=\"tcp://127.0.0.1:6379?auth=hidden\"\n"
	if prefix, isolated := phpSessionNamespace(legacy, "redis", "/managed/site/sessions", "site"); prefix != "" || isolated {
		t.Fatal("legacy shared prefix misreported as isolated")
	}
	if prefix, isolated := phpSessionNamespace(sitePHPIni(legacy, "/managed/site"), "redis", "/managed/site/sessions", "site"); prefix != "ov.site." || !isolated {
		t.Fatal("managed site prefix not detected")
	}
	if prefix, isolated := phpSessionNamespace("session.save_path=private-connection-string", "custom", "/managed/site/sessions", "site"); prefix != "" || isolated {
		t.Fatal("unknown handler exposed its opaque connection string")
	}
	old := "[Session]\nmemcached.sess_sasl_password = \"keep-this-password\"\n"
	p := model.Config{"memcachedPort": 11212, "saslEnabled": true, "saslUsername": "local-user", "saslPassword": "••••••••"}
	settings, err := memcachedSessionSettings(p, old, "site-one")
	if err != nil || settings["memcached.sess_sasl_password"] != "keep-this-password" || settings["session.save_path"] != "127.0.0.1:11212" || settings["memcached.sess_prefix"] != "ov.site-one." {
		t.Fatal("masked password or local endpoint lost", err)
	}
	settings["session.save_handler"] = "memcached"
	text := patchSessionIni("; keep comment\n[PHP]\nmemory_limit=128M\n"+old, settings)
	if !strings.Contains(text, "; keep comment") || !strings.Contains(text, "memory_limit=128M") || strings.Count(text, "memcached.sess_sasl_password =") != 1 {
		t.Fatal("unrelated ini changed or duplicate credentials")
	}
	view := inspectMemcachedSession(text)
	if view.Int("port") != 11212 || !view.Bool("saslEnabled") || view.String("saslPassword") != "••••••••" || strings.Contains(maskConfig(text), "keep-this-password") || strings.Contains(maskConfig(text), "local-user") {
		t.Fatal("credentials exposed or inspect mismatch")
	}
	for _, site := range []string{"site-one", "site-two"} {
		ini := sitePHPIni(text, "/managed/"+site)
		if phpIniValue(ini, "memcached.sess_prefix", "") != "ov."+site+"." || phpIniValue(ini, "memcached.sess_sasl_password", "") != "keep-this-password" {
			t.Fatal("site inheritance lost credentials or isolation")
		}
	}
	for _, port := range []any{nil, 0, 65536, "11211;evil", "127.0.0.1:11211", "11211.5"} {
		if _, err := memcachedSessionSettings(model.Config{"memcachedPort": port}, "", "site"); err == nil {
			t.Fatal("invalid endpoint accepted", port)
		}
	}
	for _, password := range []string{"", "••••••••", "x\ny", "x\"y", "x\\y", "${HOME}", "x\x01y", strings.Repeat("a", 257)} {
		p["saslPassword"] = password
		if _, err := memcachedSessionSettings(p, "", "site"); err == nil {
			t.Fatal("unsafe credential accepted")
		}
	}
	p["saslEnabled"] = false
	settings, err = memcachedSessionSettings(p, old, "site")
	if err != nil || settings["memcached.sess_sasl_username"] != "" || settings["memcached.sess_sasl_password"] != "" {
		t.Fatal("disabled SASL retained credentials")
	}
}

func sessionFixture(t *testing.T) (*Engine, model.Resource, string, string) {
	t.Helper()
	e, r, _ := softwareFixture(t)
	r.Config["software"], r.Config["version"] = "php", "8.4.27"
	base := filepath.Join(e.Root, "instances", r.ID)
	path := base + "/conf/php.ini"
	if err := os.WriteFile(path, []byte("[PHP]\n; preserved\n[Session]\nsession.save_handler = files\nsession.save_path = /tmp\n"), 0640); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	r.Config["port"] = listener.Addr().(*net.TCPAddr).Port
	if err = e.save(r); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(2 * time.Second))
				var input []byte
				for {
					h := make([]byte, 8)
					if _, err := io.ReadFull(c, h); err != nil {
						return
					}
					n := int(binary.BigEndian.Uint16(h[4:6]))
					part := make([]byte, n+int(h[6]))
					if _, err := io.ReadFull(c, part); err != nil {
						return
					}
					if h[1] == 5 {
						input = append(input, part[:n]...)
					}
					if h[1] == 5 && n == 0 {
						break
					}
				}
				var request model.Config
				_ = json.Unmarshal(input, &request)
				result := model.Config{"verified": true, "token": request.String("token")}
				if os.Getenv("OPSVIEW_SESSION_FPM_BAD") == "1" {
					result = model.Config{"verified": false, "reason": "configuration"}
				}
				response, _ := json.Marshal(result)
				payload := append([]byte("Content-Type: application/json\r\n\r\n"), response...)
				h := []byte{1, 6, 0, 1, 0, 0, 0, 0}
				binary.BigEndian.PutUint16(h[4:6], uint16(len(payload)))
				_, _ = c.Write(append(h, payload...))
				_, _ = c.Write([]byte{1, 3, 0, 1, 0, 8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
			}()
		}
	}()
	bin := t.TempDir()
	for name, script := range map[string]string{
		"systemctl": `if [ "$1" = is-active ]; then echo active; exit 0; fi
printf '%s\n' "$*" >> "$OPSVIEW_SESSION_TRACE"
if [ "$OPSVIEW_SESSION_RELOAD_FAIL" = 1 ] && [ ! -f "$OPSVIEW_SESSION_TRACE.failed" ]; then touch "$OPSVIEW_SESSION_TRACE.failed"; exit 1; fi
exit 0
`,
		"chown": "exit 0\n",
	} {
		if err := os.WriteFile(bin+"/"+name, []byte("#!/bin/sh\n"+script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(base+"/runtime/bin", 0755); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
printf '%s\n' "$@" > "$OPSVIEW_SESSION_TRACE.args"
cat > "$OPSVIEW_SESSION_TRACE.input"
if [ "$OPSVIEW_SESSION_DRIFT" = 1 ]; then printf '\n; external-change\n' >> "$OPSVIEW_SESSION_LIVE"; fi
if [ "$OPSVIEW_SESSION_PROBE_FAIL" = 1 ]; then echo 'unsafe-secret-probe-output'; exit 1; fi
exit 0
`
	if err := os.WriteFile(base+"/runtime/bin/php", []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	trace := filepath.Join(bin, "trace")
	t.Setenv("OPSVIEW_SESSION_TRACE", trace)
	t.Setenv("OPSVIEW_SESSION_LIVE", path)
	return e, r, path, trace
}

func TestPHPSessionSaveTransactionAndCredentialRedaction(t *testing.T) {
	e, r, path, trace := sessionFixture(t)
	before, _ := os.ReadFile(path)
	p := model.Config{"operation": "save", "confirmed": true, "handler": "memcached", "baseRevision": security.Hash(string(before)), "memcachedPort": 11211, "saslEnabled": true, "saslUsername": "cache-user", "saslPassword": "sasl-test-secret"}
	var logs []string
	result, err := e.softwareOperation(context.Background(), model.Operation{Name: "instance.session", Resource: r, Params: p}, func(s string) { logs = append(logs, s) })
	if err != nil || result.String("handler") != "memcached" {
		t.Fatal(result, err)
	}
	after, _ := os.ReadFile(path)
	st, _ := os.Stat(path)
	if phpIniValue(string(after), "session.save_path", "") != "127.0.0.1:11211" || st.Mode().Perm() != 0600 {
		t.Fatal("private candidate was not applied")
	}
	args, _ := os.ReadFile(trace + ".args")
	if strings.Contains(string(args), "sasl-test-secret") || strings.Contains(strings.Join(logs, "\n"), "sasl-test-secret") || len(logs) != 4 {
		t.Fatal("credentials leaked into argv/logs or missing steps")
	}
	input, _ := os.ReadFile(trace + ".input")
	var expected struct {
		Values map[string]string `json:"values"`
	}
	if json.Unmarshal(input, &expected) != nil || expected.Values["memcached.sess_sasl_password"] != "sasl-test-secret" {
		t.Fatal("probe did not receive expected config privately")
	}
	state, _ := os.ReadFile(filepath.Join(e.Root, "state", r.ID+".json"))
	if !strings.HasPrefix(string(state), "enc:") || strings.Contains(string(state), "sasl-test-secret") {
		t.Fatal("credential state was not encrypted")
	}
	view, err := e.softwareOperation(context.Background(), model.Operation{Name: "instance.session", Resource: r, Params: model.Config{}}, func(string) {})
	public, _ := json.Marshal(view)
	if err != nil || strings.Contains(string(public), "sasl-test-secret") || !strings.Contains(string(public), "••••••••") {
		t.Fatal("inspect exposed plaintext credentials", err)
	}
	filtered := e.filteredSoftwareLog(r, "2026/11/17 12:00:00 [error] sasl-test-secret", logQuery{limit: 10}, false)
	if strings.Contains(filtered.String("content"), "sasl-test-secret") {
		t.Fatal("registered session credentials not masked in logs")
	}
	for _, pattern := range []string{filepath.Join(filepath.Dir(path), ".session-*"), filepath.Join(filepath.Dir(filepath.Dir(path)), "run", ".session-probe-*")} {
		files, _ := filepath.Glob(pattern)
		if len(files) != 0 {
			t.Fatal("private validation file not removed")
		}
	}
	for _, failure := range []string{"stale", "unconfirmed", "unrelated-site", "invalid-port"} {
		candidate := model.Config{}
		for k, v := range p {
			candidate[k] = v
		}
		candidate["baseRevision"] = security.Hash(string(after))
		switch failure {
		case "stale":
			candidate["baseRevision"] = "old"
		case "unconfirmed":
			candidate["confirmed"] = false
		case "unrelated-site":
			candidate["siteId"] = "unrelated"
		case "invalid-port":
			candidate["memcachedPort"] = "11211;touch /tmp/bad"
		}
		if _, err := e.softwareOperation(context.Background(), model.Operation{Name: "instance.session", Resource: r, Params: candidate}, func(string) {}); err == nil {
			t.Fatal("invalid save accepted", failure)
		}
		live, _ := os.ReadFile(path)
		if string(live) != string(after) {
			t.Fatal("rejected save modified running config", failure)
		}
	}
}

func TestPHPSessionFailedProbeAndReloadPreserveConfig(t *testing.T) {
	for _, scenario := range []string{"probe", "reload", "fpm"} {
		t.Run(scenario, func(t *testing.T) {
			e, r, path, _ := sessionFixture(t)
			before, _ := os.ReadFile(path)
			ctx := context.Background()
			if scenario == "probe" {
				t.Setenv("OPSVIEW_SESSION_PROBE_FAIL", "1")
			} else if scenario == "reload" {
				t.Setenv("OPSVIEW_SESSION_RELOAD_FAIL", "1")
			} else {
				t.Setenv("OPSVIEW_SESSION_FPM_BAD", "1")
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 300*time.Millisecond)
				defer cancel()
			}
			_, err := e.softwareOperation(ctx, model.Operation{Name: "instance.session", Resource: r, Params: model.Config{"operation": "save", "handler": "memcached", "memcachedPort": 11211, "baseRevision": security.Hash(string(before)), "confirmed": true}}, func(string) {})
			if err == nil || strings.Contains(err.Error(), "unsafe-secret") {
				t.Fatal("failure accepted or raw probe output escaped", err)
			}
			after, _ := os.ReadFile(path)
			st, _ := os.Stat(path)
			if string(before) != string(after) || st.Mode().Perm() != 0640 {
				t.Fatal("failure changed configuration or original permission")
			}
		})
	}
}

func TestPHPSessionPreflightDetectsExternalDrift(t *testing.T) {
	e, r, path, _ := sessionFixture(t)
	before, _ := os.ReadFile(path)
	t.Setenv("OPSVIEW_SESSION_DRIFT", "1")
	_, err := e.softwareOperation(context.Background(), model.Operation{Name: "instance.session", Resource: r, Params: model.Config{"operation": "save", "handler": "memcached", "memcachedPort": 11211, "baseRevision": security.Hash(string(before)), "confirmed": true}}, func(string) {})
	var conflict *model.RevisionConflict
	if !errors.As(err, &conflict) {
		t.Fatal("preflight drift was not reported", err)
	}
	after, _ := os.ReadFile(path)
	if !strings.Contains(string(after), "; external-change") || phpIniValue(string(after), "session.save_handler", "") != "files" {
		t.Fatal("external edit was overwritten")
	}
}

func TestPHPSessionExtensionDependencyProtectsDefaultAndStoppedSites(t *testing.T) {
	for _, scope := range []string{"default", "stopped-site"} {
		t.Run(scope, func(t *testing.T) {
			e, r, path, _ := sessionFixture(t)
			if scope == "stopped-site" {
				site := model.Resource{ID: "session-site", Kind: "site", Name: "paused", Config: model.Config{"phpInstanceId": r.ID, "disabled": true, "phpPaused": true}}
				if err := e.save(site); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(e.Root, "sites", site.ID, "conf", "php.ini")
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(path, []byte("[Session]\nsession.save_handler=memcached\n"), 0600); err != nil {
				t.Fatal(err)
			}
			for _, operation := range []string{"disable", "uninstall"} {
				_, err := e.phpExtension(context.Background(), r, model.Config{"name": "memcached", "operation": operation}, true, func(string) {})
				if err == nil || !strings.Contains(err.Error(), "Session 使用") {
					t.Fatal("active Session dependency ignored", err)
				}
			}
		})
	}
}

func TestPHPSessionCleanupRejectsLinksAndPreservesLiveFiles(t *testing.T) {
	base, outside := t.TempDir(), t.TempDir()
	path := filepath.Join(base, "sessions")
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := phpSessionFiles(base, true); err == nil {
		t.Fatal("cleanup followed an external directory link")
	}
	_ = os.Remove(path)
	_ = os.Mkdir(path, 0700)
	for _, name := range []string{"sess_old", "sess_live", "other-old"} {
		_ = os.WriteFile(filepath.Join(path, name), []byte(name), 0600)
	}
	old := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(filepath.Join(path, "sess_old"), old, old)
	_ = os.Chtimes(filepath.Join(path, "other-old"), old, old)
	_ = os.WriteFile(filepath.Join(outside, "sess_external"), []byte("untouched"), 0600)
	_ = os.Chtimes(filepath.Join(outside, "sess_external"), old, old)
	_ = os.Symlink(filepath.Join(outside, "sess_external"), filepath.Join(path, "sess_link"))
	if n, err := phpSessionFiles(base, false); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err := os.Stat(filepath.Join(path, "sess_old")); err != nil {
		t.Fatal("preview deleted data", err)
	}
	if n, err := phpSessionFiles(base, true); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	for _, name := range []string{filepath.Join(path, "sess_live"), filepath.Join(path, "other-old"), filepath.Join(outside, "sess_external")} {
		if _, err := os.Stat(name); err != nil {
			t.Fatal("unrelated or active file removed", name, err)
		}
	}
}
