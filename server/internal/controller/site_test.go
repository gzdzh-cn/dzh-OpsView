package controller

import (
	"encoding/json"
	"opsview/internal/model"
	"strings"
	"testing"
)

func TestSiteCreateOwnsPathsAndKeepsLegacyProcessRetired(t *testing.T) {
	c, h := testController(t)
	cookie, csrf := setupAndLogin(t, h)
	nginx := model.Resource{ID: "nginx-fixture", Kind: "instance", Name: "nginx", Status: "ready", Config: model.Config{"software": "nginx", "version": "test", "mode": "native"}}
	if err := c.App.Store.Create(&nginx); err != nil {
		t.Fatal(err)
	}
	body := model.Config{"kind": "site", "name": "app.example.com", "config": model.Config{"type": "managed", "instanceId": "nginx-fixture", "domains": "app.example.com", "port": 3000, "executable": "/usr/bin/node", "arguments": `["server.js"]`, "user": "isolated", "documentRoot": "/etc", "runningRoot": "/etc"}}
	w := request(h, "POST", "/api/v1/resources", body, cookie, csrf, "")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var r model.Resource
	json.Unmarshal(w.Body.Bytes(), &r)
	if r.Config.String("rootDirectory") != "/www/wwwroot/app.example.com" || r.Config.String("documentRoot") != "" || r.Config.String("runningRoot") != "" {
		t.Fatal(r.Config)
	}
	r.Config["documentRoot"] = "/www/wwwroot/app.example.com"
	if err := c.App.Store.Update(&r); err != nil {
		t.Fatal(err)
	}
	cfg := model.Config{}
	for k, v := range r.Config {
		cfg[k] = v
	}
	cfg["rootDirectory"] = "/www/wwwroot/other"
	w = request(h, "PUT", "/api/v1/resources/"+r.ID, model.Config{"name": r.Name, "config": cfg, "revision": r.Revision}, cookie, csrf, "")
	if w.Code != 409 {
		t.Fatal("root migration permitted", w.Code)
	}
	cfg["rootDirectory"] = r.Config["rootDirectory"]
	cfg["documentRoot"] = "/etc"
	w = request(h, "PUT", "/api/v1/resources/"+r.ID, model.Config{"name": r.Name, "config": cfg, "revision": r.Revision}, cookie, csrf, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &r)
	if r.Config.String("documentRoot") == "/etc" {
		t.Fatal("trusted path overwritten")
	}
}

func TestSiteProvisionAndCandidateDoNotExposeOrCommitDrafts(t *testing.T) {
	c, h := testController(t)
	cookie, csrf := setupAndLogin(t, h)
	for _, r := range []model.Resource{{ID: "nginx-one", Kind: "instance", Name: "nginx", Status: "ready", Config: model.Config{"software": "nginx", "version": "1.30.6", "mode": "native"}}, {ID: "mysql-one", Kind: "instance", Name: "mysql", Status: "ready", Config: model.Config{"software": "mysql", "version": "8.4.13", "mode": "native"}}} {
		if err := c.App.Store.Create(&r); err != nil {
			t.Fatal(err)
		}
	}
	w := request(h, "POST", "/api/v1/sites", model.Config{"name": "project", "config": model.Config{"type": "static", "domains": "example.test", "instanceId": "forged", "siteBaseDirectory": "/etc"}, "database": model.Config{"instanceId": "mysql-one", "name": "site_example", "username": "site_example", "password": "test-private-db-password"}}, cookie, csrf, "")
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out model.Config
	json.Unmarshal(w.Body.Bytes(), &out)
	site, err := c.App.Store.Resource(out.String("siteId"))
	if err != nil {
		t.Fatal(err)
	}
	if site.Config.String("instanceId") != "nginx-one" || site.Config.String("rootDirectory") != "/www/wwwroot/example.test" || site.Config.String("password") != "" || site.Config.String("siteBaseDirectory") != "/www/wwwroot" {
		t.Fatal(site)
	}
	c.App.Store.Finish(out.String("taskId"), "failed", "deployment failed")
	originalRevision := site.Revision
	draft := model.Config{"type": "static", "domains": "changed.test", "instanceId": "forged", "databaseId": "forged"}
	w = request(h, "POST", "/api/v1/sites/"+site.ID+"/settings", model.Config{"name": "changed", "revision": originalRevision - 1, "config": draft}, cookie, csrf, "")
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "POST", "/api/v1/sites/"+site.ID+"/settings", model.Config{"name": "changed", "revision": originalRevision, "config": draft}, cookie, csrf, "")
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	current, _ := c.App.Store.Resource(site.ID)
	if current.Name != "project" || current.Revision != originalRevision || current.Config.String("domains") != "example.test" {
		t.Fatal("draft committed before validation", current)
	}
	w = request(h, "GET", "/api/v1/tasks/"+out.String("taskId"), nil, cookie, csrf, "")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code)
	}
	if strings.Contains(w.Body.String(), "test-private") {
		t.Fatal("task exposed database credential")
	}
}
