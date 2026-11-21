package executor

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"opsview/internal/logic"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Called only by the disposable Linux website acceptance fixture.
func testWebsiteSettings(t *testing.T, e *Engine, inst model.Resource) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	extra := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	r := model.Resource{ID: security.ID(), Kind: "site", Name: "settings-smoke", Config: model.Config{"type": "static", "instanceId": inst.ID, "domains": fmt.Sprintf("settings-smoke.test settings-smoke.test:%d", extra), "subdirectoryBindings": []logic.SiteBinding{{Host: "docs-smoke.test", Directory: "docs"}}, "gzipEnabled": true, "cacheEnabled": true, "cacheExtensions": "css jpg", "cacheSeconds": 120, "hotlinkEnabled": true, "hotlinkAllowEmpty": true, "hotlinkExtensions": "jpg", "hotlinkDomains": "settings-smoke.test", "denyPaths": "/private", "allowAddresses": "127.0.0.1", "limitEnabled": true, "maxConnections": 300, "perIPConnections": 25, "rateKB": 512, "corsEnabled": true, "corsOrigin": "https://frontend.test", "corsMethods": "GET, POST, OPTIONS", "responseHeaders": map[string]string{"X-Frame-Options": "DENY"}, "redirects": []logic.SiteRedirect{{Path: "/old", Target: "https://example.test/new", Code: 302, Exact: true}}, "accessRules": map[string]any{"secure": map[string]any{"path": "/secure", "username": "reader", "password": "settings-real-password"}}}}
	if err = logic.HashSiteAccessPasswords(r.Config); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "proxied:"+r.URL.Path) }))
	defer upstream.Close()
	r.Config["proxyRoutes"] = []logic.ProxyRoute{{Path: "/api/", Targets: []string{upstream.URL}}}
	base := filepath.Join(e.Root, "sites", r.ID)
	root := base + "/www"
	if err = os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"private", "secure", "docs"} {
		if err = os.MkdirAll(root+"/"+dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err = os.Chmod(root+"/"+dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for path, content := range map[string]string{"index.html": "settings-root", "secure/index.html": "authenticated-content", "private/index.html": "must-stay-private", "docs/index.html": "child-directory", "style.css": "body{color:red}", "image.jpg": "test-image"} {
		if err = os.WriteFile(root+"/"+path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = e.Execute(ctx, model.Operation{Name: "site.apply", Resource: r}, func(s string) { t.Log(s) }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := e.Execute(context.Background(), model.Operation{Name: "site.delete", Resource: r}, func(string) {}); err != nil {
			t.Error(err)
		}
	})
	request := func(host, path string, port int, password, ref string) (int, string, http.Header) {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
		req.Host = host
		if password != "" {
			req.SetBasicAuth("reader", password)
		}
		if ref != "" {
			req.Header.Set("Referer", ref)
		}
		res, err := (&http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(body), res.Header
	}
	port := inst.Config.Int("port")
	for _, check := range []struct {
		host, path    string
		port          int
		password, ref string
		code          int
		body          string
	}{
		{"settings-smoke.test", "/", port, "", "", 200, "settings-root"}, {"settings-smoke.test", "/", extra, "", "", 200, "settings-root"}, {"docs-smoke.test", "/", port, "", "", 200, "child-directory"},
		{"settings-smoke.test", "/secure/", port, "", "", 401, ""}, {"settings-smoke.test", "/secure/", port, "settings-real-password", "", 200, "authenticated-content"}, {"settings-smoke.test", "/secure/", port, "wrong-password", "", 401, ""},
		{"settings-smoke.test", "/private/", port, "", "", 403, ""}, {"settings-smoke.test", "/old", port, "", "", 302, ""}, {"settings-smoke.test", "/api/items", port, "", "", 200, "proxied:/api/items"},
		{"settings-smoke.test", "/image.jpg", port, "", "https://untrusted.test/", 403, ""}, {"settings-smoke.test", "/image.jpg", port, "", "https://settings-smoke.test/", 200, "test-image"},
	} {
		code, body, _ := request(check.host, check.path, check.port, check.password, check.ref)
		if code != check.code || (check.body != "" && body != check.body) {
			t.Fatalf("%s%s port=%d: code=%d body=%q", check.host, check.path, check.port, code, body)
		}
	}
	_, _, headers := request("settings-smoke.test", "/style.css", port, "", "")
	if headers.Get("X-Frame-Options") != "DENY" || headers.Get("Access-Control-Allow-Origin") != "https://frontend.test" || headers.Get("Expires") == "" {
		t.Fatal("headers/cache not applied", headers)
	}
	bad, _ := e.load(r.ID)
	bad.Config["proxyRoutes"] = []logic.ProxyRoute{{Path: "/api/", Targets: []string{"http://unresolvable-" + r.ID + ".invalid"}}}
	configPath := filepath.Join(e.Root, "instances", inst.ID, "conf", "sites", r.ID+".conf")
	original, _ := os.ReadFile(configPath)
	if _, err = e.Execute(ctx, model.Operation{Name: "site.apply", Resource: bad}, func(s string) { t.Log(s) }); err == nil {
		t.Fatal("unresolvable upstream unexpectedly applied")
	}
	preserved, _ := os.ReadFile(configPath)
	if string(original) != string(preserved) {
		t.Fatal("Nginx validation failure did not restore config")
	}
	if code, body, _ := request("settings-smoke.test", "/api/after-failure", port, "", ""); code != 200 || body != "proxied:/api/after-failure" {
		t.Fatal("prior runtime lost after candidate failure", code, body)
	}
	// A failed candidate must leave both actual content and the prior configuration intact.
	stored, _ := e.load(r.ID)
	before, _ := os.ReadFile(filepath.Join(e.Root, "instances", inst.ID, "conf", "sites", r.ID+".conf"))
	stored.Config["advanced"] = "client_body_temp_path /nonexistent/opsview-test-path;" // unsupported directive rejected before runtime
	if _, err = e.Execute(ctx, model.Operation{Name: "site.apply", Resource: stored}, func(string) {}); err == nil {
		t.Fatal("invalid candidate accepted")
	}
	after, _ := os.ReadFile(filepath.Join(e.Root, "instances", inst.ID, "conf", "sites", r.ID+".conf"))
	if string(before) != string(after) {
		t.Fatal("failed candidate changed configuration")
	}
	testWebsiteClientAuth(t, e, r)
	t.Log("PASS real domain/port/subdirectory, bcrypt auth, restrictions, limits, redirects, local proxy, hotlink, cache, CORS, headers and rejected-candidate preservation")
}
