package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"opsview/internal/logic"
	"opsview/internal/model"
	"opsview/internal/security"
	"strings"
	"testing"
	"time"
)

func TestSiteRootAuthorizationAndStableID(t *testing.T) {
	c, h := testController(t)
	cookie, csrf := setupAndLogin(t, h)
	i := model.Resource{ID: "runtime-nginx", Kind: "instance", Name: "nginx", Status: "ready", Config: model.Config{"software": "nginx", "mode": "native", "port": 80}}
	if err := c.App.Store.Create(&i); err != nil {
		t.Fatal(err)
	}
	cfg := model.Config{"type": "managed", "domains": "root.test", "user": "root", "runtimeIsolation": true, "port": 32123, "executable": "/usr/bin/python3", "arguments": "[]", "environment": "{}", "siteRootApproval": "forged"}
	body := model.Config{"name": "root-test", "config": cfg, "requestId": "root-authorization-test"}
	if w := request(h, "POST", "/api/v1/sites", body, cookie, csrf, ""); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	call := func(method, path string, body any, verify bool) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(b))
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		if verify {
			req.Header.Set("X-OpsView-Password", "test-long-password-123")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	w := call("POST", "/api/v1/sites", body, true)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out model.Config
	json.Unmarshal(w.Body.Bytes(), &out)
	site, err := c.App.Store.Resource(out.String("siteId"))
	if err != nil {
		t.Fatal(err)
	}
	if site.Config.String("siteRootApproval") != logic.SiteRootDigest(site) {
		t.Fatal("approval lost site ID binding", site.ID)
	}
	if err = c.App.Store.Finish(out.String("taskId"), "succeeded", ""); err != nil {
		t.Fatal(err)
	}
	// Unrelated edits retain the server-owned approval, never the browser's value.
	clone := func() model.Config {
		b, _ := json.Marshal(site.Config)
		v := model.Config{}
		json.Unmarshal(b, &v)
		delete(v, "siteRootApproval")
		return v
	}
	note := clone()
	note["note"] = "updated"
	w = call("POST", "/api/v1/sites/"+site.ID+"/settings", model.Config{"name": site.Name, "config": note, "revision": site.Revision}, false)
	if w.Code != 202 {
		t.Fatal("unrelated edit required verification", w.Code, w.Body.String())
	}
	changed := clone()
	changed["executable"] = "/usr/bin/node"
	changed["siteRootApproval"] = "forged"
	for _, endpoint := range []struct{ method, path string }{{"POST", "/api/v1/sites/" + site.ID + "/settings"}, {"PUT", "/api/v1/resources/" + site.ID}} {
		w = call(endpoint.method, endpoint.path, model.Config{"name": site.Name, "config": changed, "revision": site.Revision}, false)
		if w.Code != 403 {
			t.Fatal("root mutation bypass", w.Code, w.Body.String())
		}
	}
	w = request(h, "GET", "/api/v1/resources?kind=site", nil, cookie, "", "")
	if strings.Contains(w.Body.String(), "siteRootApproval") {
		t.Fatal("approval disclosed")
	}
	if w = request(h, "GET", "/api/v1/sites/runtime-users", nil, nil, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatal(w.Code)
	}
}

func TestNewManagedSiteDefaultsAndMetadataOwnership(t *testing.T) {
	c, h := testController(t)
	cookie, csrf := setupAndLogin(t, h)
	i := model.Resource{ID: "runtime-nginx", Kind: "instance", Name: "nginx", Status: "ready", Config: model.Config{"software": "nginx", "mode": "native", "port": 80}}
	c.App.Store.Create(&i)
	w := request(h, "POST", "/api/v1/sites", model.Config{"name": "normal-test", "config": model.Config{"type": "managed", "domains": "normal.test", "port": 32124, "executable": "/usr/bin/python3", "runtimeIsolation": false, "runtimeUser": "root", "runtimeIsolationApplied": true}}, cookie, csrf, "")
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out model.Config
	json.Unmarshal(w.Body.Bytes(), &out)
	site, _ := c.App.Store.Resource(out.String("siteId"))
	if !site.Config.Bool("runtimeIsolation") || site.Config.String("runtimeUser") != "" || site.Config.Bool("runtimeIsolationApplied") {
		t.Fatal(site.Config)
	}
}

func TestSiteRootRequiresEnabledTOTP(t *testing.T) {
	c, h := testController(t)
	setupAndLogin(t, h)
	a, err := c.currentAdmin()
	if err != nil {
		t.Fatal(err)
	}
	a.TOTP = security.NewTOTP()
	if err = c.App.Store.Set("admin", a); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"", "invalid", security.TOTP(a.TOTP, time.Now())} {
		r := model.Resource{ID: "totp-root", Kind: "site", Config: model.Config{"type": "managed", "user": "root", "executable": "/usr/bin/python3", "runtimeIsolation": true}}
		req := httptest.NewRequest("POST", "/api/v1/sites", nil)
		req.Header.Set("X-OpsView-Password", "test-long-password-123")
		req.Header.Set("X-OpsView-TOTP", code)
		w := httptest.NewRecorder()
		ok := c.prepareSiteRuntime(w, req, &r, nil)
		if code == "" || code == "invalid" {
			if ok || w.Code != 403 || r.Config.String("siteRootApproval") != "" {
				t.Fatal("missing/invalid OTP approved root execution")
			}
		} else if !ok || r.Config.String("siteRootApproval") != logic.SiteRootDigest(r) {
			t.Fatal("valid OTP did not approve the bound root configuration", w.Code)
		}
	}
}
