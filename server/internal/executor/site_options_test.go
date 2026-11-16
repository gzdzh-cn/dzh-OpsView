package executor

import (
	"opsview/internal/logic"
	"opsview/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSiteRuntimeDirectoryAndProxyRendering(t *testing.T) {
	root := t.TempDir()
	if err := prepareSiteRunningDirectory(root, "public/assets"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "external")); err != nil {
		t.Fatal(err)
	}
	if err := prepareSiteRunningDirectory(root, "external/secret"); err == nil {
		t.Fatal("symlink runtime accepted")
	}
	r := model.Resource{ID: "site1234", Config: model.Config{"type": "php", "runningDirectory": "public", "defaultDocuments": "home.php index.html", "rewriteTemplate": "thinkphp"}}
	if siteRunningRoot(r, "/managed/site") != "/managed/site/www/public" || siteIndex(r) != "home.php index.html" || !strings.Contains(siteTryFiles(r), "index.php?s=") {
		t.Fatal("runtime options ignored")
	}
	r.Config = model.Config{"type": "proxy", "proxyRoutes": []logic.ProxyRoute{{Path: "/", Targets: []string{"http://127.0.0.1:3000", "http://[::1]:3001"}, Host: "$host", Websocket: true}, {Path: "/api/", Targets: []string{"https://api.example.com"}, Host: "api.example.com", ReadTimeout: 120}}}
	upstream, locations := renderProxyRoutes(r, "")
	for _, want := range []string{"server 127.0.0.1:3000;", "server [::1]:3001;", "server api.example.com:443;"} {
		if !strings.Contains(upstream, want) {
			t.Fatal(upstream)
		}
	}
	for _, want := range []string{"location /api/", "proxy_read_timeout 120s;", "proxy_set_header Host api.example.com;", "proxy_set_header Upgrade $http_upgrade;", "proxy_ssl_name api.example.com;"} {
		if !strings.Contains(locations, want) {
			t.Fatal(locations)
		}
	}
}
