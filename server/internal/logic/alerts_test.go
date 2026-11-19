package logic

import (
	"context"
	"opsview/internal/dao"
	"opsview/internal/model"
	"testing"
	"time"
)

func TestAlertPersistenceRecoveryCooldownAndEvents(t *testing.T) {
	s, err := dao.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	a := &App{Store: s}
	site := model.Resource{Kind: "site", Name: "certificate-fixture", Config: model.Config{"certificateExpires": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}}
	if err = s.Create(&site); err != nil {
		t.Fatal(err)
	}
	rule := model.Resource{Kind: "alert-rule", Name: "certificate", Config: model.Config{"type": "certificate", "targetId": site.ID, "enabled": true, "threshold": 7, "samples": 3, "cooldownMinutes": 15, "recovery": true, "channels": []string{"channel"}}}
	_ = s.Create(&rule)
	for i := 0; i < 2; i++ {
		a.evaluateAlerts(context.Background(), false)
	}
	events, _ := s.Notifications()
	if len(events) != 0 {
		t.Fatal("premature alert")
	}
	a.evaluateAlerts(context.Background(), false)
	events, _ = s.Notifications()
	if len(events) != 1 {
		t.Fatal(events)
	}
	a.evaluateAlerts(context.Background(), false)
	events, _ = s.Notifications()
	if len(events) != 1 {
		t.Fatal("cooldown bypass")
	}
	site.Config["certificateExpires"] = time.Now().Add(90 * 24 * time.Hour).UTC().Format(time.RFC3339)
	_ = s.Update(&site)
	a.evaluateAlerts(context.Background(), false)
	events, _ = s.Notifications()
	if len(events) != 2 {
		t.Fatal("recovery missing", events)
	}
	eventRule := model.Resource{Kind: "alert-rule", Name: "failed-plan", Config: model.Config{"type": "backup-failed", "enabled": true, "channels": []string{"channel"}}}
	_ = s.Create(&eventRule)
	task, err := s.Enqueue(model.Operation{Name: "schedule.execute", Resource: model.Resource{ID: "plan", Config: model.Config{"operation": "backup"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Finish(task.ID, "failed", "redacted"); err != nil {
		t.Fatal(err)
	}
	a.evaluateAlerts(context.Background(), true)
	events, _ = s.Notifications()
	if len(events) != 3 {
		t.Fatal("backup failure event missing", events)
	}
	a.evaluateAlerts(context.Background(), true)
	events, _ = s.Notifications()
	if len(events) != 3 {
		t.Fatal("event replayed")
	}
}
