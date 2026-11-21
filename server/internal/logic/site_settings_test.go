package logic

import (
	"golang.org/x/crypto/bcrypt"
	"opsview/internal/model"
	"opsview/internal/security"
	"strings"
	"testing"
)

func TestWebsiteBindingsAndSubdirectories(t *testing.T) {
	c := model.Config{"domains": "example.test *.example.test api.example.test:8081 [2001:db8::1]:8082", "subdirectoryBindings": []SiteBinding{{Host: "docs.example.test", Directory: "docs/public"}}}
	rows, err := SiteBindings(c, 8080)
	if err != nil || len(rows) != 5 || rows[2].Port != 8081 || rows[3].Host != "2001:db8::1" || rows[4].Port != 8080 {
		t.Fatal(rows, err)
	}
	if DefaultSiteRoot("example.test:8081") != "/www/wwwroot/example.test" {
		t.Fatal("port leaked to directory")
	}
	for _, domains := range []string{"example.test example.test:8080", "example.test;include", "[::1]:443", "example.test:0", "*bad.example.test", "example.test:65536", "https://example.test"} {
		if _, err := SiteBindings(model.Config{"domains": domains}, 8080); err == nil {
			t.Fatal("accepted invalid binding", domains)
		}
	}
	c = model.Config{"domains": "example.test", "subdirectoryBindings": []SiteBinding{{Host: "docs.test", Directory: "../etc"}}}
	if _, err := SiteBindings(c, 80); err == nil {
		t.Fatal("accepted escaped directory")
	}
}
func TestWebsitePoliciesRejectInjection(t *testing.T) {
	for _, c := range []model.Config{
		{"denyPaths": "/x;root"}, {"denyPaths": "/"}, {"denyPaths": "/.well-known"}, {"allowAddresses": "all"}, {"denyAddresses": "127.0.0.1;"},
		{"cacheEnabled": true, "cacheExtensions": "php", "cacheSeconds": 60}, {"hotlinkDomains": "example.test;"}, {"limitEnabled": true},
		{"responseHeaders": map[string]string{"Set-Cookie": "oops"}}, {"responseHeaders": map[string]string{"Content-Security-Policy": "x\nX-Foo: y"}},
		{"corsEnabled": true, "corsOrigin": "*", "corsCredentials": true}, {"corsEnabled": true, "corsOrigin": "https://example.test/path"}, {"corsEnabled": true, "corsOrigin": "*", "corsMethods": "TRACE"},
		{"redirects": []SiteRedirect{{Path: "/a", Target: "javascript:alert(1)", Code: 301}}}, {"redirects": []SiteRedirect{{Path: "/a", Target: "https://example.test/$uri", Code: 301}}},
		{"type": "php", "proxyRoutes": []ProxyRoute{{Path: "/", Targets: []string{"http://localhost:80"}}}},
		{"type": "static", "proxyRoutes": []ProxyRoute{{Path: "/api/", Targets: []string{"http://localhost:80"}}}, "redirects": []SiteRedirect{{Path: "/api/", Target: "https://example.test", Code: 302}}},
	} {
		if ValidateSiteOptions(c) == nil {
			t.Fatal("accepted unsafe policy", c)
		}
	}
	c := model.Config{"limitEnabled": true, "maxConnections": 300, "perIPConnections": 25, "rateKB": 512, "corsEnabled": true, "corsOrigin": "https://example.test", "corsMethods": "GET POST OPTIONS", "corsHeaders": "Content-Type Authorization", "responseHeaders": map[string]string{"Content-Security-Policy": "default-src 'self'"}, "redirects": []SiteRedirect{{Path: "/old/", Target: "https://example.test/new/", Code: 308}}}
	if err := ValidateSiteOptions(c); err != nil {
		t.Fatal(err)
	}
}
func TestWebsiteAccessCredentialsNeverReturnPlaintext(t *testing.T) {
	c := model.Config{"accessRules": map[string]any{"admin": map[string]any{"path": "/admin", "username": "reader", "password": "website-test-secret"}}}
	if err := HashSiteAccessPasswords(c); err != nil {
		t.Fatal(err)
	}
	rules, err := SiteAccessRules(c)
	if err != nil {
		t.Fatal(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(rules["admin"].Password), []byte("website-test-secret")) != nil {
		t.Fatal("password not hashed")
	}
	masked := model.Config(security.Redact(c))
	if strings.Contains(masked.String("accessRules"), "website-test-secret") {
		t.Fatal("secret exposed")
	}
	raw := masked["accessRules"].(map[string]any)["admin"].(map[string]any)
	if raw["password"] != "••••••••" {
		t.Fatal("hash not masked")
	}
	raw["path"] = "/private"
	security.PreserveMasked(masked, c)
	if err = HashSiteAccessPasswords(masked); err != nil {
		t.Fatal(err)
	}
	next, _ := SiteAccessRules(masked)
	if next["admin"].Password != rules["admin"].Password {
		t.Fatal("masked password changed")
	}
}
