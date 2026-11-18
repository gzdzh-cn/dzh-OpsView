package controller

import (
	"encoding/json"
	"opsview/internal/model"
	"opsview/internal/security"
	"strings"
	"testing"
	"time"
)

func TestDatabaseCredentialRequiresRecentIdentityAndNoStore(t *testing.T) {
	c, h := testController(t)
	cookie, csrf := setupAndLogin(t, h)
	inst := model.Resource{Kind: "instance", Name: "mysql", Config: model.Config{"software": "mysql"}}
	if err := c.App.Store.Create(&inst); err != nil {
		t.Fatal(err)
	}
	db := model.Resource{Kind: "database", Name: "business", Status: "ready", Config: model.Config{"instanceId": inst.ID, "username": "business", "password": "protected-db-password"}}
	if err := c.App.Store.Create(&db); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/databases/" + db.ID + "/credential"
	if w := request(h, "POST", path, map[string]string{}, cookie, csrf, ""); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request(h, "POST", path, map[string]string{"password": "test-long-password-123"}, cookie, "", ""); w.Code != 403 {
		t.Fatal("csrf", w.Code)
	}
	w := request(h, "POST", path, map[string]string{"password": "test-long-password-123"}, cookie, csrf, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "protected-db-password") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request(h, "POST", path, map[string]string{}, cookie, csrf, ""); w.Code != 200 {
		t.Fatal("session grant", w.Code)
	}
	c.auth.credentials[security.Hash(cookie.Value)] = time.Now().Add(-time.Second)
	if w = request(h, "POST", path, map[string]string{}, cookie, csrf, ""); w.Code != 403 {
		t.Fatal("expired grant", w.Code)
	}
	w = request(h, "GET", "/api/v1/databases?engine=mysql", nil, cookie, "", "")
	var data struct {
		Rows []model.Resource `json:"rows"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &data)
	if len(data.Rows) != 1 || !data.Rows[0].Config.Bool("hasPassword") || strings.Contains(w.Body.String(), "protected-db-password") {
		t.Fatal("list secret redaction", w.Body.String())
	}
	audits, _ := c.App.Store.Audits()
	for _, a := range audits {
		if strings.Contains(a.Action, "protected-db-password") {
			t.Fatal("audit leak")
		}
	}
}
func TestLegacyDeleteQueuesRecycleAndRejectsDirectMutation(t *testing.T) {
	c, h := testController(t)
	cookie, csrf := setupAndLogin(t, h)
	db := model.Resource{Kind: "database", Name: "legacy", Status: "ready", Config: model.Config{"instanceId": "mysql", "username": "legacy", "password": "password-for-testing"}}
	_ = c.App.Store.Create(&db)
	w := request(h, "POST", "/api/v1/resources/"+db.ID+"/actions/delete", model.Config{"confirmDataDeletion": true}, cookie, csrf, "")
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	tasks, _ := c.App.Store.Tasks()
	if len(tasks) != 1 || tasks[0].Operation != "dbadmin.recycle" {
		t.Fatal(tasks)
	}
	w = request(h, "PUT", "/api/v1/resources/"+db.ID, model.Config{"name": "hijacked", "revision": 1, "config": db.Config}, cookie, csrf, "")
	if w.Code != 409 {
		t.Fatal("direct mutation", w.Code)
	}
}

func TestDatabaseCredentialTOTPAndRecycleBackupProtection(t *testing.T) {
	c, h := testController(t)
	cookie, csrf := setupAndLogin(t, h)
	a, err := c.currentAdmin()
	if err != nil {
		t.Fatal(err)
	}
	a.TOTP = security.NewTOTP()
	if err = c.App.Store.Set("admin", a); err != nil {
		t.Fatal(err)
	}
	db := model.Resource{Kind: "database", Name: "secured", Status: "ready", Config: model.Config{"instanceId": "mysql", "password": "business-password-fixture"}}
	if err = c.App.Store.Create(&db); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/databases/" + db.ID + "/credential"
	if w := request(h, "POST", path, model.Config{"password": "test-long-password-123"}, cookie, csrf, ""); w.Code != 403 {
		t.Fatal("missing OTP accepted", w.Code)
	}
	if w := request(h, "POST", path, model.Config{"password": "test-long-password-123", "code": security.TOTP(a.TOTP, time.Now())}, cookie, csrf, ""); w.Code != 200 {
		t.Fatal("valid OTP rejected", w.Code)
	}
	backup := model.Resource{Kind: "backup", Name: "recycle-secured", Status: "ready", Config: model.Config{"targetId": db.ID, "recycleOnly": true, "artifact": "fixture.ovb"}}
	if err = c.App.Store.Create(&backup); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"PUT", "DELETE"} {
		w := request(h, method, "/api/v1/resources/"+backup.ID, model.Config{"revision": 1, "name": backup.Name, "config": model.Config{"targetId": db.ID}}, cookie, csrf, "")
		if w.Code != 409 {
			t.Fatal("backup protection bypass", method, w.Code)
		}
	}
}
