package controller

import (
	"encoding/json"
	"opsview/internal/model"
	"testing"
)

func TestSiteCreateOwnsPathsAndKeepsLegacyProcessRetired(t *testing.T) {
	c, h := testController(t)
	cookie, csrf := setupAndLogin(t, h)
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
