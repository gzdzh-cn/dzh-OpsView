package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"opsview/internal/dao"
	"opsview/internal/logic"
	"opsview/internal/model"
	"opsview/internal/runtimeconfig"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

type noopExecutor struct{}

func (noopExecutor) Execute(context.Context, model.Operation, func(string)) (model.Config, error) {
	return model.Config{}, nil
}
func testController(t *testing.T) (*Controller, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	s, e := dao.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.DB.Close() })
	_ = os.WriteFile(filepath.Join(dir, "setup.token"), []byte("local-bootstrap-token"), 0600)
	var files fs.FS = fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>OpsView</html>")}}
	c := New(&logic.App{Store: s, Executor: noopExecutor{}, Development: true}, dir, files)
	return c, c.Handler()
}
func request(h http.Handler, method, path string, body any, cookie *http.Cookie, csrf string, origin string) *httptest.ResponseRecorder {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Host = "panel.test"
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func setupAndLogin(t *testing.T, h http.Handler) (*http.Cookie, string) {
	t.Helper()
	b := map[string]any{"token": "local-bootstrap-token", "username": "admin", "password": "test-long-password-123"}
	w := request(h, "POST", "/api/v1/auth/setup", b, nil, "", "")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "POST", "/api/v1/auth/login", map[string]any{"username": "admin", "password": "test-long-password-123", "code": ""}, nil, "", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	return w.Result().Cookies()[0], result["csrf"].(string)
}
func TestAuthenticationCSRFAndCredentialRedaction(t *testing.T) {
	_, h := testController(t)
	if w := request(h, "GET", "/api/v1/resources", nil, nil, "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	cookie, csrf := setupAndLogin(t, h)
	resource := map[string]any{"kind": "instance", "name": "mysql-local", "config": map[string]any{"catalogId": "mysql-8.4.8", "mode": "native", "port": 3306, "password": "private-db-password"}}
	if w := request(h, "POST", "/api/v1/resources", resource, cookie, "", ""); w.Code != 403 {
		t.Fatal("missing CSRF accepted", w.Code)
	}
	if w := request(h, "POST", "/api/v1/resources", resource, cookie, csrf, "http://evil.test"); w.Code != 403 {
		t.Fatal("cross-origin accepted", w.Code)
	}
	w := request(h, "POST", "/api/v1/resources", resource, cookie, csrf, "")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "private-db-password") {
		t.Fatal("create leaked credentials")
	}
	w = request(h, "GET", "/api/v1/resources", nil, cookie, "", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "private-db-password") {
		t.Fatal("list leaked credentials", w.Body.String())
	}
	if w = request(h, "POST", "/api/v1/auth/setup", resource, nil, "", ""); w.Code != 409 {
		t.Fatal("second setup accepted")
	}
	w = request(h, "POST", "/api/v1/auth/logout", map[string]any{}, cookie, csrf, "")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w = request(h, "GET", "/api/v1/resources", nil, cookie, "", ""); w.Code != 401 {
		t.Fatal("logged-out session accepted")
	}
}
func TestAccessRestrictionPreventsSelfLockout(t *testing.T) {
	_, h := testController(t)
	cookie, csrf := setupAndLogin(t, h)
	w := request(h, "PUT", "/api/v1/settings", map[string]any{"name": "OpsView", "allowedCIDRs": "203.0.113.0/24"}, cookie, csrf, "")
	if w.Code != 400 {
		t.Fatal("self lockout accepted", w.Code, w.Body.String())
	}
	w = request(h, "PUT", "/api/v1/settings", map[string]any{"name": "OpsView", "allowedCIDRs": "192.0.2.0/24"}, cookie, csrf, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestInvalidConfigDoesNotCreateResources(t *testing.T) {
	c, h := testController(t)
	cookie, csrf := setupAndLogin(t, h)
	for _, resource := range []map[string]any{{"kind": "firewall", "name": "bad", "config": map[string]any{"protocol": "tcp", "action": "allow", "scope": "host", "ports": "22;reboot", "source": "any"}}, {"kind": "site", "name": "badsite", "config": map[string]any{"instanceId": "nginx", "domains": "example.com;include /etc/*", "type": "static"}}} {
		w := request(h, "POST", "/api/v1/resources", resource, cookie, csrf, "")
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	rs, _ := c.App.Store.Resources("")
	if len(rs) != 0 {
		t.Fatal("invalid resources persisted")
	}
}

type capturingExecutor struct {
	name   string
	params model.Config
}

func (e *capturingExecutor) Execute(_ context.Context, op model.Operation, _ func(string)) (model.Config, error) {
	e.name = op.Name
	e.params = op.Params
	return model.Config{}, nil
}

func TestSoftwareLogQueryDispatch(t *testing.T) {
	c, h := testController(t)
	cookie, _ := setupAndLogin(t, h)
	r := model.Resource{Kind: "instance", Name: "php-logs", Config: model.Config{"software": "php"}}
	if err := c.App.Store.Create(&r); err != nil {
		t.Fatal(err)
	}
	capture := &capturingExecutor{}
	c.App.Executor = capture
	w := request(h, "GET", "/api/v1/instances/"+r.ID+"/logs?kind=fpm&siteId=site-a&since=2026-11-17T00%3A00%3A00Z&until=2026-11-18T00%3A00%3A00Z&level=warning&search=failure&archive=example&limit=200", nil, cookie, "", "")
	if w.Code != 200 || capture.name != "instance.logs" {
		t.Fatal(w.Code, w.Body.String())
	}
	for key, want := range map[string]string{"kind": "fpm", "siteId": "site-a", "since": "2026-11-17T00:00:00Z", "until": "2026-11-18T00:00:00Z", "level": "warning", "search": "failure", "archive": "example", "limit": "200"} {
		if capture.params.String(key) != want {
			t.Fatalf("lost query %s: %v", key, capture.params)
		}
	}
}
func TestConfigurationDispatchAndCompleteSSEHistory(t *testing.T) {
	c, h := testController(t)
	capture := &capturingExecutor{}
	c.App.Executor = capture
	cookie, csrf := setupAndLogin(t, h)
	site := model.Resource{Kind: "site", Name: "test-site", Config: model.Config{"instanceId": "nginx1"}}
	if err := c.App.Store.Create(&site); err != nil {
		t.Fatal(err)
	}
	response := request(h, "POST", "/api/v1/resources/"+site.ID+"/actions/configuration", model.Config{}, cookie, csrf, "")
	if response.Code != 200 || capture.name != "site.configuration" {
		t.Fatal(response.Code, capture.name)
	}
	task, err := c.App.Store.Enqueue(model.Operation{Name: "test", Resource: site})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1007; i++ {
		c.App.Store.Event(task.ID, "history-entry")
	}
	if err = c.App.Store.Finish(task.ID, "succeeded", ""); err != nil {
		t.Fatal(err)
	}
	response = request(h, "GET", "/api/v1/tasks/"+task.ID+"/events?stream=true", nil, cookie, "", "")
	if count := strings.Count(response.Body.String(), "history-entry"); count != 1007 {
		t.Fatal("SSE truncated history", count)
	}
	if !strings.Contains(response.Body.String(), "event: done") {
		t.Fatal("missing done")
	}
}

func TestAccessGateParsesHostAndEnforcesEntry(t *testing.T) {
	c, _ := testController(t)
	c.App.PanelConfigPath = filepath.Join(t.TempDir(), "panel.json")
	cfg := runtimeconfig.Default()
	cfg.AllowedHost = "panel.example.com"
	cfg.Entry = "safe_entry"
	if err := runtimeconfig.Save(c.App.PanelConfigPath, cfg); err != nil {
		t.Fatal(err)
	}
	gate := c.accessGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	call := func(host, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.Host = host
		if cookie != nil {
			req.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		gate.ServeHTTP(w, req)
		return w
	}
	if w := call("other.example.com:18790", "/safe_entry", nil); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := call("panel.example.com:18790", "/", nil); w.Code != 404 {
		t.Fatal(w.Code)
	}
	w := call("panel.example.com:18790", "/safe_entry", nil)
	if w.Code != 204 || len(w.Result().Cookies()) != 1 {
		t.Fatal(w.Code)
	}
	if w = call("panel.example.com:18790", "/", w.Result().Cookies()[0]); w.Code != 204 {
		t.Fatal(w.Code)
	}
}

func TestRootIPPolicyOverridesLegacySQLiteRestriction(t *testing.T) {
	c, h := testController(t)
	cookie, _ := setupAndLogin(t, h)
	if err := c.App.Store.Set("panel", model.Config{"allowedCIDRs": "203.0.113.0/24"}); err != nil {
		t.Fatal(err)
	}
	c.App.PanelConfigPath = filepath.Join(t.TempDir(), "panel.json")
	cfg := runtimeconfig.Default()
	empty := ""
	cfg.AllowedCIDRs = &empty
	if err := runtimeconfig.Save(c.App.PanelConfigPath, cfg); err != nil {
		t.Fatal(err)
	}
	if w := request(h, "GET", "/api/v1/resources", nil, cookie, "", ""); w.Code != 200 {
		t.Fatal("cleared root policy still used legacy restriction", w.Code, w.Body.String())
	}
	denied := "203.0.113.0/24"
	cfg.AllowedCIDRs = &denied
	_ = runtimeconfig.Save(c.App.PanelConfigPath, cfg)
	if w := request(h, "GET", "/api/v1/auth", nil, nil, "", ""); w.Code != 403 {
		t.Fatal("root policy did not protect login", w.Code)
	}
}

func TestRuntimeConfirmationRequiresNewListenerAndSource(t *testing.T) {
	c, _ := testController(t)
	capture := &capturingExecutor{}
	c.App.Executor = capture
	c.App.PanelConfigPath = filepath.Join(t.TempDir(), "panel.json")
	cfg := runtimeconfig.Default()
	cfg.Protocol = "http"
	cfg.Port = 19001
	_ = runtimeconfig.Save(c.App.PanelConfigPath, cfg)
	call := func(port int) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/v1/runtime/confirm", strings.NewReader(`{"token":"`+strings.Repeat("a", 64)+`"}`))
		req = req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}))
		w := httptest.NewRecorder()
		c.runtimeConfirm(w, req)
		return w
	}
	if w := call(18790); w.Code != 409 || capture.name != "" {
		t.Fatal("confirmed through old listener", w.Code)
	}
	if w := call(19001); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	denied := "203.0.113.0/24"
	cfg.AllowedCIDRs = &denied
	_ = runtimeconfig.Save(c.App.PanelConfigPath, cfg)
	if w := call(19001); w.Code != 403 {
		t.Fatal("confirmed from blocked source", w.Code)
	}
}
