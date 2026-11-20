package executor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func siteDeletionFixture(t *testing.T) (*Engine, model.Resource, string) {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	e := &Engine{Root: root}
	var err error
	e.Vault, err = security.OpenVault(filepath.Join(root, "executor.key"))
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(root+"/state", 0700)
	nginx := model.Resource{ID: "nginx-delete", Kind: "instance", Name: "nginx-delete", Config: model.Config{"software": "nginx"}}
	e.save(nginx)
	doc, _ := filepath.EvalSymlinks(t.TempDir())
	doc = filepath.Join(doc, "project")
	os.MkdirAll(doc, 0755)
	os.WriteFile(doc+"/index.html", []byte("preserved business data"), 0644)
	site := model.Resource{ID: security.ID(), Name: "deletion.test", Kind: "site", Status: "ready", Config: model.Config{"type": "static", "domains": "deletion.test", "instanceId": nginx.ID, "documentRoot": doc, "rootDirectory": doc, "siteBaseDirectory": filepath.Dir(doc)}}
	if err = e.save(site); err != nil {
		t.Fatal(err)
	}
	return e, site, doc
}
func TestSiteDeleteRealTransportAndRecovery(t *testing.T) {
	e, site, doc := siteDeletionFixture(t)
	transport := resultTransport(func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		e.handle(rec, req)
		return rec.Result(), nil
	})
	client := &Client{HTTP: &http.Client{Transport: transport}}
	data, err := client.Execute(context.Background(), model.Operation{Name: "site.delete", Resource: site, Params: model.Config{"deleteDirectory": true}}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(data)
	if strings.Contains(string(raw), "preserved business data") || strings.Contains(string(raw), "oldConfig") {
		t.Fatal("recovery content leaked")
	}
	if _, err = os.Stat(doc); !os.IsNotExist(err) {
		t.Fatal("directory not recycled", err)
	}
	index, err := e.trashIndex()
	if err != nil || len(index) != 1 {
		t.Fatal(index, err)
	}
	for id := range index {
		if _, err = e.fileTrash(model.Config{"action": "restore", "id": id}); err != nil {
			t.Fatal(err)
		}
	}
	bytes, _ := os.ReadFile(doc + "/index.html")
	if string(bytes) != "preserved business data" {
		t.Fatal("restoration failed")
	}
	// A post-completion confirmation can clean up panel metadata without deleting restored data.
	if _, err = client.Execute(context.Background(), model.Operation{Name: "site.delete", Resource: site, Params: model.Config{"deleteDirectory": true}}, func(string) {}); err != nil {
		t.Fatal("idempotent completion", err)
	}
	if _, err = os.Stat(doc); err != nil {
		t.Fatal("replayed directory deletion")
	}
}
func TestSiteDeleteDefaultsAndProtection(t *testing.T) {
	e, site, doc := siteDeletionFixture(t)
	if _, err := e.Execute(context.Background(), model.Operation{Name: "site.delete", Resource: site}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(doc + "/index.html"); err != nil {
		t.Fatal("default deleted content")
	}
	e2, other, _ := siteDeletionFixture(t)
	other.Config["documentRoot"] = "/etc"
	e2.save(other)
	if _, err := e2.Execute(context.Background(), model.Operation{Name: "site.delete", Resource: other, Params: model.Config{"deleteDirectory": true}}, func(string) {}); err == nil {
		t.Fatal("critical path accepted")
	}
}
func TestSiteDeletePreservesSharedAndSubstitutedDirectories(t *testing.T) {
	e, site, doc := siteDeletionFixture(t)
	other := site
	other.ID = security.ID()
	other.Name = "other.test"
	e.save(other)
	if _, err := e.Execute(context.Background(), model.Operation{Name: "site.delete", Resource: site, Params: model.Config{"deleteDirectory": true}}, func(string) {}); err == nil {
		t.Fatal("shared path accepted")
	}
	e.forget(other.ID)
	os.Rename(doc, doc+"-real")
	os.Symlink(doc+"-real", doc)
	if _, err := e.Execute(context.Background(), model.Operation{Name: "site.delete", Resource: site, Params: model.Config{"deleteDirectory": true}}, func(string) {}); err == nil {
		t.Fatal("substituted link accepted")
	}
}
