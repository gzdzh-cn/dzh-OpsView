package executor

import (
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSiteDocumentRootAndSecurity(t *testing.T) {
	rootDir, _ := filepath.EvalSymlinks(t.TempDir())
	e := &Engine{Root: rootDir}
	var err error
	e.Vault, err = security.OpenVault(filepath.Join(e.Root, "key"))
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(e.Root, "state"), 0700)
	r := model.Resource{ID: "site-one", Kind: "site", Config: model.Config{"type": "php", "documentRoot": "/www/wwwroot/example.com", "runningDirectory": "public", "securityHeaders": true, "denyFrames": true, "denyUploadPHP": true}}
	conf := e.renderSite(r, model.Resource{ID: "nginx", Config: model.Config{"port": 8080}}, "/managed/site-one")
	for _, want := range []string{"root /www/wwwroot/example.com/public;", "X-Content-Type-Options nosniff always", "X-Frame-Options SAMEORIGIN always", "^/(?:uploads?|storage)/.*", "deny all;"} {
		if !strings.Contains(conf, want) {
			t.Fatal("missing security/root", want, conf)
		}
	}
	if strings.Count(conf, "location ~ /\\.") != 1 {
		t.Fatal("duplicate hidden-file location", conf)
	}
	r.Config = model.Config{"type": "managed", "port": 3001, "proxyHost": "$http_host"}
	conf = e.renderSite(r, model.Resource{Config: model.Config{"port": 8080}}, "/managed/site-one")
	if !strings.Contains(conf, "proxy_pass http://127.0.0.1:3001;") || !strings.Contains(conf, "proxy_set_header Host $http_host;") {
		t.Fatal(conf)
	}
	legacy := filepath.Join(e.Root, "sites", r.ID, "www")
	os.MkdirAll(legacy, 0755)
	r.Config = model.Config{"type": "static"}
	if err = e.save(r); err != nil {
		t.Fatal(err)
	}
	r.Config["documentRoot"] = "/etc"
	root, err := e.trustedSiteRoot(r)
	if err != nil || root != legacy {
		t.Fatal("untrusted browser path accepted", root, err)
	}
	// Directory preparation must preserve legacy content and ignore a new suggested root.
	r.Config["rootDirectory"] = "/www/wwwroot/changed.test"
	if err = e.prepareSiteRoot(&r, filepath.Dir(legacy)); err != nil {
		t.Fatal(err)
	}
	if r.Config.String("documentRoot") != legacy {
		t.Fatal("legacy site moved")
	}
	if got := unitQuote("$USER %i $(touch /tmp/no)"); strings.Contains(got, "$USER") && !strings.Contains(got, "$$USER") {
		t.Fatal("systemd variable expansion", got)
	}
}
