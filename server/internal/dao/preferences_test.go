package dao

import (
	"errors"
	"opsview/internal/model"
	"opsview/internal/security"
	"testing"
	"time"
)

func TestPreferenceCASAndDurableRollback(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	_, rev, err := s.PatchSetting("panel", 0, model.Config{"name": "before", "basicEnabled": false}, "test", "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.PatchSetting("panel", 0, model.Config{"name": "stale"}, "test", "local"); !errors.Is(err, ErrRevision) {
		t.Fatal("stale accepted", err)
	}
	_, _, err = s.StagePreferences(rev, model.Config{"basicEnabled": true}, model.Config{"basicEnabled": false}, security.Hash("token"), "local")
	if err != nil {
		t.Fatal(err)
	}
	var pending model.Config
	if err = s.Setting("preference-pending", &pending); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.PatchSetting("panel", rev+1, model.Config{"name": "concurrent"}, "test", "local"); err == nil {
		t.Fatal("pending setting overwritten")
	}
	if err = s.ConfirmPreferences(security.Hash("wrong")); err == nil {
		t.Fatal("wrong token accepted")
	}
	pending["deadline"] = time.Now().Add(-time.Second).Unix()
	if err = s.Set("preference-pending", pending); err != nil {
		t.Fatal(err)
	}
	if err = s.RollbackPreferences(); err != nil {
		t.Fatal(err)
	}
	var p model.Config
	revision, err := s.SettingRevision("panel", &p)
	if err != nil || p.Bool("basicEnabled") || p.String("name") != "before" || revision != rev+2 {
		t.Fatal(revision, p, err)
	}
}
func TestScheduleHistoryLockAndPruning(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	r := model.Resource{Kind: "schedule", Name: "test", Config: model.Config{"operation": "panel"}}
	if err = s.Create(&r); err != nil {
		t.Fatal(err)
	}
	task, err := s.Enqueue(model.Operation{Name: "panel.backup", Resource: model.Resource{ID: "target"}, Params: model.Config{"scheduleId": r.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Update(&r); err == nil {
		t.Fatal("running schedule edited")
	}
	if err = s.Delete(r.ID); err == nil {
		t.Fatal("running schedule deleted")
	}
	if _, err = s.Enqueue(model.Operation{Name: "panel.backup", Resource: model.Resource{ID: "other"}, Params: model.Config{"scheduleId": r.ID}}); err == nil {
		t.Fatal("overlap accepted")
	}
	s.Event(task.ID, "test")
	if err = s.SaveTaskResult(task.ID, model.Config{"exitCode": 7}); err != nil {
		t.Fatal(err)
	}
	if err = s.Finish(task.ID, "failed", "test"); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordSkippedSchedule(r.ID, "conflict"); err != nil {
		t.Fatal(err)
	}
	runs, err := s.ScheduleRuns(r.ID)
	if err != nil || len(runs) != 2 {
		t.Fatal(runs, err)
	}
	if err = s.PruneScheduleHistory(r.ID, time.Now().Add(time.Hour).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	runs, err = s.ScheduleRuns(r.ID)
	if err != nil || len(runs) != 0 {
		t.Fatal(runs, err)
	}
	var count int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE task_id=?`, task.ID).Scan(&count)
	if count != 0 {
		t.Fatal("logs retained")
	}
	if err = s.Update(&r); err != nil {
		t.Fatal(err)
	}
}

func TestScriptRevisionsAndCategoryMigrationPreserveTaskDigest(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	script := model.Resource{Name: "template", Config: model.Config{"script": "echo first"}}
	if err = s.SaveScript(&script); err != nil {
		t.Fatal(err)
	}
	stale := script
	script.Config = model.Config{"script": "echo second"}
	if err = s.SaveScript(&script); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveScript(&stale); err == nil {
		t.Fatal("stale script overwrote newer revision")
	}
	original, err := s.ScriptRevision(script.ID, 1)
	if err != nil || original.String("script") != "echo first" {
		t.Fatal(original, err)
	}
	plan := model.Resource{Kind: "schedule", Name: "categorized", Config: model.Config{"category": "before", "operation": "shell", "rootApproval": "saved-digest", "script": "echo first"}}
	_ = s.Create(&plan)
	if err = s.ChangeScheduleCategory("before", "after", false); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Resource(plan.ID)
	if got.Config.String("category") != "after" || got.Config.String("rootApproval") != "saved-digest" {
		t.Fatal(got)
	}
	if err = s.ChangeScheduleCategory("after", "", true); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Resource(plan.ID)
	if got.Config.String("category") != "默认分类" {
		t.Fatal(got)
	}
}
