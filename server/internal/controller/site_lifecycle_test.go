package controller

import (
	"encoding/json"
	"opsview/internal/model"
	"testing"
	"time"
)

func lifecycleSite(t *testing.T, c *Controller) model.Resource {
	t.Helper()
	r := model.Resource{Kind: "site", Name: "delete-test", Status: "ready", Config: model.Config{"type": "static", "domains": "example.test", "instanceId": "nginx-test"}}
	if err := c.App.Store.Create(&r); err != nil {
		t.Fatal(err)
	}
	return r
}
func TestSiteDeleteConfirmationRevisionAndLegacy(t *testing.T) {
	c, h := testController(t)
	cookie, csrf := setupAndLogin(t, h)
	site := lifecycleSite(t, c)
	items := []model.SiteDeleteItem{{ID: site.ID, Revision: site.Revision}}
	w := request(h, "POST", "/api/v1/sites/delete-preview", model.Config{"items": items}, cookie, csrf, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var preview model.Config
	_ = json.Unmarshal(w.Body.Bytes(), &preview)
	id := preview.String("previewId")
	for _, path := range []string{"/api/v1/sites/delete", "/api/v1/resources/" + site.ID + "/actions/delete"} {
		w = request(h, "POST", path, model.Config{"previewId": id, "confirmed": true}, cookie, csrf, "")
		if w.Code != 409 {
			t.Fatal("countdown bypass", w.Code, w.Body.String())
		}
	}
	var stored model.SiteDeletePreview
	_ = c.App.Store.Setting("site-delete-preview:"+id, &stored)
	stored.CreatedAt = time.Now().Unix() - 6
	_ = c.App.Store.Set("site-delete-preview:"+id, stored)
	site.Config["note"] = "external change"
	_ = c.App.Store.Update(&site)
	w = request(h, "POST", "/api/v1/sites/delete", model.Config{"previewId": id, "confirmed": true}, cookie, csrf, "")
	if w.Code != 409 {
		t.Fatal("stale preview accepted", w.Code)
	}
	items[0].Revision = site.Revision
	w = request(h, "POST", "/api/v1/sites/delete-preview", model.Config{"items": items}, cookie, csrf, "")
	_ = json.Unmarshal(w.Body.Bytes(), &preview)
	id = preview.String("previewId")
	_ = c.App.Store.Setting("site-delete-preview:"+id, &stored)
	stored.CreatedAt = time.Now().Unix() - 6
	_ = c.App.Store.Set("site-delete-preview:"+id, stored)
	w = request(h, "POST", "/api/v1/sites/delete", model.Config{"previewId": id, "confirmed": true}, cookie, csrf, "")
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	tasks, _ := c.App.Store.Tasks()
	if len(tasks) != 1 || tasks[0].Operation != "sites.delete" {
		t.Fatal(tasks)
	}
	w = request(h, "DELETE", "/api/v1/resources/"+site.ID, model.Config{}, cookie, csrf, "")
	if w.Code != 409 {
		t.Fatal("direct delete bypass", w.Code)
	}
}
func TestSiteCreationRequestIsIdempotent(t *testing.T) {
	c, h := testController(t)
	cookie, csrf := setupAndLogin(t, h)
	nginx := model.Resource{Kind: "instance", Name: "nginx-test", Status: "ready", Config: model.Config{"software": "nginx", "mode": "native", "version": "1.30.6"}}
	_ = c.App.Store.Create(&nginx)
	body := model.Config{"requestId": "idempotent-create-test", "name": "example.test", "config": model.Config{"type": "static", "domains": "example.test", "rootDirectory": "/www/wwwroot/example.test"}}
	var first model.Config
	for i := 0; i < 2; i++ {
		w := request(h, "POST", "/api/v1/sites", body, cookie, csrf, "")
		if w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		var result model.Config
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		if i == 0 {
			first = result
		} else if result.String("siteId") != first.String("siteId") || result.String("taskId") != first.String("taskId") {
			t.Fatal("duplicate", result)
		}
	}
	resources, _ := c.App.Store.Resources("site")
	tasks, _ := c.App.Store.Tasks()
	if len(resources) != 1 || len(tasks) != 1 {
		t.Fatal(len(resources), len(tasks))
	}
	w := request(h, "GET", "/api/v1/sites/provisions/idempotent-create-test", nil, cookie, csrf, "")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}

func TestSiteDeleteSessionAndDatabaseMaintenanceRequired(t *testing.T) {
	c, h := testController(t)
	cookie, csrf := setupAndLogin(t, h)
	site := lifecycleSite(t, c)
	db := model.Resource{Kind: "database", Name: "linked_db", Config: model.Config{"instanceId": "db-instance"}}
	if err := c.App.Store.Create(&db); err != nil {
		t.Fatal(err)
	}
	site.Config["databaseId"] = db.ID
	if err := c.App.Store.Update(&site); err != nil {
		t.Fatal(err)
	}
	w := request(h, "POST", "/api/v1/sites/delete-preview", model.Config{"items": []model.SiteDeleteItem{{ID: site.ID, Revision: site.Revision, DeleteDatabase: true}}}, cookie, csrf, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result model.Config
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	id := result.String("previewId")
	var preview model.SiteDeletePreview
	_ = c.App.Store.Setting("site-delete-preview:"+id, &preview)
	preview.CreatedAt = time.Now().Unix() - 6
	original := preview.Session
	preview.Session = "different-session"
	_ = c.App.Store.Set("site-delete-preview:"+id, preview)
	w = request(h, "POST", "/api/v1/sites/delete", model.Config{"previewId": id, "confirmed": true, "maintenanceConfirmed": true}, cookie, csrf, "")
	if w.Code != 409 {
		t.Fatal("session bypass", w.Code)
	}
	preview.Session = original
	_ = c.App.Store.Set("site-delete-preview:"+id, preview)
	w = request(h, "POST", "/api/v1/sites/delete", model.Config{"previewId": id, "confirmed": true}, cookie, csrf, "")
	if w.Code != 409 {
		t.Fatal("maintenance bypass", w.Code)
	}
	tasks, _ := c.App.Store.Tasks()
	if len(tasks) != 0 {
		t.Fatal("unsafe task queued")
	}
}

func TestSiteCreationLookupDoesNotHideBrokenRecords(t *testing.T) {
	c, h := testController(t)
	cookie, csrf := setupAndLogin(t, h)
	if err := c.App.Store.Set("site-request:broken-reference", model.Config{"siteId": "missing-site", "taskId": "missing-task"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.App.Store.DB.Exec(`INSERT INTO settings(key,value) VALUES('site-request:broken-payload','not-encrypted-data')`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"broken-reference", "broken-payload"} {
		w := request(h, "GET", "/api/v1/sites/provisions/"+id, nil, cookie, csrf, "")
		if w.Code != 500 {
			t.Fatal("broken lookup disguised as absence", w.Code, w.Body.String())
		}
		w = request(h, "POST", "/api/v1/sites", model.Config{"requestId": id, "name": "must-not-create"}, cookie, csrf, "")
		if w.Code != 500 {
			t.Fatal("creation proceeded after a failed lookup", w.Code, w.Body.String())
		}
	}
	w := request(h, "GET", "/api/v1/sites/provisions/unknown-request", nil, cookie, csrf, "")
	if w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	sites, err := c.App.Store.Resources("site")
	if err != nil || len(sites) != 0 {
		t.Fatal("unexpected site", sites, err)
	}
}
