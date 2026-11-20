package executor

import (
	"context"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSiteTemplatesPreserveAnd404(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(root+"/index.html", []byte("customer-index"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := createSiteTemplates(root); err != nil {
		t.Fatal(err)
	}
	if err := createSiteTemplates(root); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(root + "/index.html")
	if string(b) != "customer-index" {
		t.Fatal("customer page overwritten")
	}
	notFound, _ := os.ReadFile(root + "/404.html")
	if !strings.Contains(string(notFound), "页面未找到") || strings.Contains(string(notFound), root) {
		t.Fatal("invalid page")
	}
	e := &Engine{}
	r := model.Resource{Config: model.Config{"type": "static", "documentRoot": root, "domains": "example.test"}}
	conf := e.renderSite(r, model.Resource{Config: model.Config{"port": 80}}, root)
	for _, want := range []string{"try_files $uri $uri/ =404;", "error_page 404 /404.html;", "php[0-9]?|phtml|phar"} {
		if !strings.Contains(conf, want) {
			t.Fatal(want, conf)
		}
	}
	r.Config["rewriteTemplate"] = "spa"
	conf = e.renderSite(r, model.Resource{Config: model.Config{"port": 80}}, root)
	if !strings.Contains(conf, "/index.html") {
		t.Fatal("explicit SPA lost")
	}
}
func TestInterruptedNginxReplacementRestoresOriginalRuntime(t *testing.T) {
	e := &Engine{Root: t.TempDir()}
	var err error
	e.Vault, err = security.OpenVault(e.Root + "/key")
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(e.Root+"/state", 0700)
	r := model.Resource{ID: "nginx-one", Kind: "instance", Status: "stopped", Config: model.Config{"software": "nginx", "version": "old"}}
	base := filepath.Join(e.Root, "instances", r.ID)
	os.MkdirAll(base, 0700)
	dir := e.nginxRecovery(r.ID)
	os.MkdirAll(dir+"/runtime", 0700)
	os.WriteFile(dir+"/runtime/identity", []byte("old"), 0600)
	j := nginxReplacement{Original: r, Phase: "swapping"}
	if err = e.saveNginxReplacement(r.ID, j); err != nil {
		t.Fatal(err)
	}
	if err = e.recoverNginxReplacements(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(base + "/runtime/identity")
	if err != nil || string(b) != "old" {
		t.Fatal(string(b), err)
	}
	stored, err := e.load(r.ID)
	if err != nil || stored.Config.String("version") != "old" {
		t.Fatal(stored, err)
	}
	if err = e.recoverNginxReplacements(context.Background()); err != nil {
		t.Fatal("recovery not idempotent", err)
	}
}
