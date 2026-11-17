package controller

import (
	"encoding/json"
	"opsview/internal/model"
	"testing"
)

func TestApplicationMutationsRetiredAndInstanceNamesGenerated(t *testing.T) {
	c, h := testController(t)
	cookie, csrf := setupAndLogin(t, h)
	app := model.Resource{Kind: "app", Name: "legacy-running", Config: model.Config{"port": 3000}}
	if err := c.App.Store.Create(&app); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		body         any
	}{{"POST", "/api/v1/resources", map[string]any{"kind": "app", "name": "new"}}, {"PUT", "/api/v1/resources/" + app.ID, map[string]any{}}, {"DELETE", "/api/v1/resources/" + app.ID, nil}, {"POST", "/api/v1/resources/" + app.ID + "/actions/stop", map[string]any{}}} {
		w := request(h, tc.method, tc.path, tc.body, cookie, csrf, "")
		if w.Code != 410 {
			t.Fatal(tc.path, w.Code, w.Body.String())
		}
	}
	if _, err := c.App.Store.Resource(app.ID); err != nil {
		t.Fatal("retired service metadata deleted", err)
	}
	body := map[string]any{"kind": "instance", "name": "ignored-name", "config": map[string]any{"catalogId": "mysql-8.4.8", "mode": "docker", "port": 3306}}
	if w := request(h, "POST", "/api/v1/resources", body, cookie, csrf, ""); w.Code != 400 {
		t.Fatal("new Docker install accepted", w.Code, w.Body.String())
	}
	body["config"].(map[string]any)["mode"] = "native"
	w := request(h, "POST", "/api/v1/resources", body, cookie, csrf, "")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var res model.Resource
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res.Name != "mysql-8.4.8" {
		t.Fatal("client-controlled instance name", res.Name)
	}
	w = request(h, "POST", "/api/v1/resources", body, cookie, csrf, "")
	if w.Code != 409 {
		t.Fatal("duplicate accepted", w.Code)
	}
	var duplicate map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &duplicate)
	if duplicate["resourceId"] != res.ID {
		t.Fatal("missing existing instance", duplicate)
	}
}
func TestFileResultsAreNotLimitedToLatestTaskPage(t *testing.T) {
	c, h := testController(t)
	cookie, csrf := setupAndLogin(t, h)
	task, err := c.App.Store.Enqueue(model.Operation{Name: "files.size", Resource: model.Resource{ID: "files-host"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = c.App.Store.Finish(task.ID, "succeeded", "")
	_ = c.App.Store.Set("task-result:"+task.ID, model.Config{"count": 3})
	for i := 0; i < 201; i++ {
		_, err = c.App.Store.DB.Exec(`INSERT INTO tasks VALUES(?,?,'system.inspect','succeeded','','{}',?,?)`, string(rune(1000+i)), "other", "9999", "9999")
		if err != nil {
			t.Fatal(err)
		}
	}
	w := request(h, "GET", "/api/v1/files/tasks/"+task.ID, nil, cookie, csrf, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
