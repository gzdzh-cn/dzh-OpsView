package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"opsview/internal/logic"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testNginxWebsiteOptions(t *testing.T, e *Engine, inst model.Resource) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	probe := func(path string) (int, string) {
		req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://127.0.0.1:%d%s", inst.Config.Int("port"), path), nil)
		req.Host = "opsview-smoke.test"
		res, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(body)
	}
	root := model.Resource{ID: security.ID(), Kind: "site", Name: "runtime-smoke", Config: model.Config{"instanceId": inst.ID, "type": "static", "domains": "opsview-smoke.test", "runningDirectory": "public", "defaultDocuments": "home.html", "rewriteTemplate": "none"}}
	base := filepath.Join(e.Root, "sites", root.ID)
	if err := os.MkdirAll(base+"/www/public", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base+"/www/public/home.html", []byte("actual-running-directory"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base+"/www/public/.env", []byte("hidden-value"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute(ctx, model.Operation{Name: "site.apply", Resource: root}, func(s string) { t.Log(s) }); err != nil {
		t.Fatal(err)
	}
	if code, body := probe("/"); code != 200 || body != "actual-running-directory" {
		t.Fatal(code, body)
	}
	if code, _ := probe("/missing"); code != 404 {
		t.Fatal("rewrite ignored", code)
	}
	if code, body := probe("/.env"); code != 403 || strings.Contains(body, "hidden-value") {
		t.Fatal("hidden file exposed", code)
	}
	if _, err := e.Execute(ctx, model.Operation{Name: "site.delete", Resource: root}, func(s string) { t.Log(s) }); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "root:"+r.URL.Path) }))
	defer upstream.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "api:"+r.URL.Path+":"+r.Host) }))
	defer api.Close()
	root.ID = security.ID()
	root.Name = "proxy-smoke"
	root.Config = model.Config{"instanceId": inst.ID, "type": "proxy", "domains": "opsview-smoke.test", "proxyRoutes": []logic.ProxyRoute{{Path: "/", Targets: []string{upstream.URL}, Host: "$host", Websocket: true}, {Path: "/api/", Targets: []string{api.URL}, Host: "api.example.com", ReadTimeout: 120}}}
	if _, err := e.Execute(ctx, model.Operation{Name: "site.apply", Resource: root}, func(s string) { t.Log(s) }); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = e.Execute(context.Background(), model.Operation{Name: "site.delete", Resource: root}, func(string) {})
	}()
	if code, body := probe("/hello"); code != 200 || body != "root:/hello" {
		t.Fatal(code, body)
	}
	if code, body := probe("/api/v1"); code != 200 || body != "api:/api/v1:api.example.com" {
		t.Fatal(code, body)
	}
	t.Log("PASS actual running-directory/default-document/rewrite, hidden-file protection and multi-path proxy")
}
