package executor

import (
	"context"
	"fmt"
	"opsview/internal/logic"
	"opsview/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebsitePolicyRenderingAndListenerIsolation(t *testing.T) {
	e := &Engine{Root: "/managed"}
	r := model.Resource{ID: "site1234", Config: model.Config{"type": "static", "domains": "example.test example.test:8081 other.test:8081", "documentRoot": "/www/wwwroot/example.test", "probeToken": "abcdef", "subdirectoryBindings": []logic.SiteBinding{{Host: "docs.test", Port: 8082, Directory: "docs"}}, "limitEnabled": true, "maxConnections": 300, "perIPConnections": 25, "rateKB": 512, "cacheEnabled": true, "cacheExtensions": "jpg css", "cacheSeconds": 3600, "gzipEnabled": true, "denyPaths": "/private", "denyAddresses": "192.0.2.0/24", "allowAddresses": "127.0.0.1", "hotlinkEnabled": true, "hotlinkExtensions": "jpg css", "hotlinkDomains": "example.test", "corsEnabled": true, "corsOrigin": "https://frontend.test", "responseHeaders": map[string]string{"X-Frame-Options": "DENY"}, "redirects": []logic.SiteRedirect{{Path: "/old", Target: "https://example.test/new", Code: 302, Exact: true}}}}
	conf := e.renderSite(r, model.Resource{Config: model.Config{"port": 8080}}, "/managed/sites/site1234")
	for _, want := range []string{"listen 8080;", "listen 8081;", "listen 8082;", "root /www/wwwroot/example.test/docs;", "limit_conn_zone $ov_site1234_key", "limit_conn ov_site1234_site 300;", "deny 192.0.2.0/24;", "location = /old { return 302 https://example.test/new; }", "valid_referers example.test;", "gzip on;", "expires $ov_site1234_expires;", "Access-Control-Allow-Origin \"https://frontend.test\"", "X-Frame-Options \"DENY\""} {
		if !strings.Contains(conf, want) {
			t.Fatal("missing", want, conf)
		}
	}
	if strings.Count(conf, "limit_conn_zone $ov_site1234_key") != 1 || strings.Count(conf, "listen 8081;") != 1 {
		t.Fatal("duplicate shared declarations", conf)
	}
	r.Config["tls"] = true
	r.Config["redirectHTTPS"] = true
	conf = e.renderSite(r, model.Resource{Config: model.Config{"port": 8080}}, "/managed/sites/site1234")
	if strings.Count(conf, "listen 443 ssl;") != 3 {
		t.Fatal("duplicate TLS host groups", conf)
	}
}
func TestWebsiteAuthUsesGenerationFiles(t *testing.T) {
	base := t.TempDir()
	r := model.Resource{ID: "site1234", Config: model.Config{"probeToken": "abc123", "accessRules": map[string]any{"private": map[string]any{"path": "/private", "username": "reader", "password": "test-auth-password"}}}}
	if err := logic.HashSiteAccessPasswords(r.Config); err != nil {
		t.Fatal(err)
	}
	if err := prepareSiteAccess(r, base); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(base, "auth", "abc123", "private"))
	if err != nil || !strings.HasPrefix(string(b), "reader:$2") || strings.Contains(string(b), "test-auth-password") {
		t.Fatal("auth file", err)
	}
	conf := siteSettingsHTTP(r, base) + siteSettingsServer(r)
	if strings.Contains(conf, "$2") || strings.Contains(conf, "test-auth-password") || !strings.Contains(conf, "auth_basic $ov_site1234_realm;") {
		t.Fatal("credentials in config", conf)
	}
	r.Config["probeToken"] = "next123"
	if err = prepareSiteAccess(r, base); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(base, "auth", "abc123", "private")); err != nil {
		t.Fatal("prior generation lost before reload")
	}
}

func TestDisabledWebsiteCannotBypassStopViaRedirectOrProxy(t *testing.T) {
	r := model.Resource{ID: "site1234", Config: model.Config{"type": "proxy", "disabled": true, "domains": "example.test", "redirects": []logic.SiteRedirect{{Path: "/old/", Target: "https://public.test", Code: 302}}, "proxyRoutes": []logic.ProxyRoute{{Path: "/", Targets: []string{"http://unresolvable.invalid"}}}, "corsEnabled": true, "corsOrigin": "*"}}
	e := &Engine{}
	conf := e.renderSite(r, model.Resource{Config: model.Config{"port": 8080}}, "/managed/site1234")
	if strings.Contains(conf, "proxy_pass") || strings.Contains(conf, "unresolvable.invalid") || strings.Contains(conf, "return 302") || strings.Contains(conf, "OPTIONS") {
		t.Fatal("disabled website still forwards public requests", conf)
	}
	if !strings.Contains(conf, "location / { return 403; }") {
		t.Fatal("missing access stop", conf)
	}
}

func TestWebsiteCredentialFilesHiddenFromFileManager(t *testing.T) {
	root := t.TempDir()
	e := &Engine{Root: root}
	auth := filepath.Join(root, "sites", "site1234", "auth", "generation", "rule")
	if err := os.MkdirAll(filepath.Dir(auth), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(auth, []byte("reader:hashed-test-value"), 0644); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "public-alias")
	if err := os.Symlink(auth, alias); err != nil {
		t.Fatal(err)
	}
	visibility, err := e.fileVisibility(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{auth, alias} {
		st, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if visibility.visible(path, st) {
			t.Fatal("credential exposed", path)
		}
		if handle, err := e.filePath(path, true); err == nil {
			handle.close()
			t.Fatal("direct credential read allowed", path)
		}
	}
}

func TestWholeWebsiteRedirectDoesNotDuplicateDefaultLocation(t *testing.T) {
	e := &Engine{}
	for _, kind := range []string{"static", "php", "proxy"} {
		r := model.Resource{ID: "site1234", Config: model.Config{"type": kind, "domains": "example.test", "redirects": []logic.SiteRedirect{{Path: "/", Target: "https://new.test", Code: 301}}, "upstream": "http://127.0.0.1:3000"}}
		conf := e.renderSite(r, model.Resource{Config: model.Config{"port": 80}}, "/managed/site1234")
		if strings.Contains(conf, " location / {") || !strings.Contains(conf, "location ^~ / { return 301 https://new.test; }") {
			t.Fatal("root redirect clashes with runtime", kind, conf)
		}
	}
}

func TestWebsiteHotlinkRejectionStatus(t *testing.T) {
	for _, status := range []int{403, 404} {
		r := model.Resource{ID: "site1234", Config: model.Config{"hotlinkEnabled": true, "hotlinkDomains": "example.test", "hotlinkExtensions": "jpg", "hotlinkStatus": status}}
		conf := siteSettingsServer(r)
		if !strings.Contains(conf, fmt.Sprintf("if ($ov_site1234_hotlink) { return %d; }", status)) {
			t.Fatal("incorrect rejection status", conf)
		}
	}
}
