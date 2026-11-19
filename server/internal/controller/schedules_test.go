package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"opsview/internal/model"
	"strings"
	"testing"
)

func TestSchedulesUseFreshTargetsPauseAndTaskAttribution(t *testing.T) {
	c, h := testController(t)
	cookie, csrf := setupAndLogin(t, h)
	instance := model.Resource{ID: "nginx-test", Kind: "instance", Name: "nginx", Status: "ready", Config: model.Config{"software": "nginx"}}
	if err := c.App.Store.Create(&instance); err != nil {
		t.Fatal(err)
	}
	site := model.Resource{ID: "site-test", Kind: "site", Name: "test-site", Status: "ready", Config: model.Config{"instanceId": instance.ID}}
	_ = c.App.Store.Create(&site)
	data := map[string]any{"kind": "schedule", "name": "daily-renew", "config": model.Config{"targetId": site.ID, "operation": "renew", "cron": "0 3 * * *", "enabled": false}}
	w := request(h, "POST", "/api/v1/resources", data, cookie, csrf, "")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var schedule model.Resource
	_ = json.Unmarshal(w.Body.Bytes(), &schedule)
	if _, err := c.App.QueueSchedule(schedule.ID, true); err == nil {
		t.Fatal("paused schedule ran automatically")
	}
	w = request(h, "POST", "/api/v1/resources/"+schedule.ID+"/actions/run", model.Config{}, cookie, csrf, "")
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	tasks, _ := c.App.Store.Tasks()
	if len(tasks) != 1 || tasks[0].ScheduleID != schedule.ID || tasks[0].ResourceID != site.ID {
		t.Fatal(tasks)
	}
	w = request(h, "POST", "/api/v1/resources/"+schedule.ID+"/actions/run", model.Config{}, cookie, csrf, "")
	if w.Code != 409 {
		t.Fatal("overlap accepted", w.Code)
	}
	w = request(h, "GET", "/api/v1/schedules", nil, cookie, "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "timezone") || !strings.Contains(w.Body.String(), `"nextRun":""`) {
		t.Fatal(w.Code, w.Body.String())
	}
	data["name"] = "invalid-target"
	data["config"] = model.Config{"targetId": instance.ID, "operation": "renew", "cron": "0 3 * * *"}
	if w = request(h, "POST", "/api/v1/resources", data, cookie, csrf, ""); w.Code != 400 {
		t.Fatal("bad target accepted", w.Code)
	}
	if w = request(h, "GET", "/api/v1/schedules", nil, nil, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatal(w.Code)
	}
}

type sshRecordingExecutor struct{ op model.Operation }

func (e *sshRecordingExecutor) Execute(_ context.Context, op model.Operation, _ func(string)) (model.Config, error) {
	e.op = op
	return model.Config{"updated": true}, nil
}
func TestSSHAccountSecretsNeverEnterTaskOrAudit(t *testing.T) {
	c, h := testController(t)
	cookie, csrf := setupAndLogin(t, h)
	rec := &sshRecordingExecutor{}
	c.App.Executor = rec
	body := model.Config{"operation": "password", "username": "root", "password": "new-disposable-test-secret", "currentPassword": "wrong", "confirmedImpact": true}
	if w := request(h, "POST", "/api/v1/system/security/ssh-user", body, cookie, csrf, ""); w.Code != 403 {
		t.Fatal(w.Code)
	}
	body["currentPassword"] = "test-long-password-123"
	w := request(h, "POST", "/api/v1/system/security/ssh-user", body, cookie, csrf, "")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	if rec.op.Params.String("currentPassword") != "" || rec.op.Params.String("password") != "new-disposable-test-secret" {
		t.Fatal("credential transport invalid")
	}
	tasks, _ := c.App.Store.Tasks()
	if len(tasks) != 0 {
		t.Fatal("password was queued")
	}
	audits, _ := c.App.Store.Audits()
	b, _ := json.Marshal(audits)
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), "password-123") {
		t.Fatal("secret audited")
	}
}
