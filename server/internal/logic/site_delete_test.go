package logic

import (
	"context"
	"errors"
	"opsview/internal/dao"
	"opsview/internal/model"
	"testing"
)

type siteDeleteExecutor struct {
	deleted []string
	fail    string
}

func (e *siteDeleteExecutor) Execute(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	if op.Name == "site.delete-preview" {
		return model.Config{"directory": "/www/wwwroot/" + op.Resource.Name, "identity": "stable", "phase": "未执行"}, nil
	}
	if op.Name == "site.delete" {
		if op.Resource.ID == e.fail {
			return nil, errors.New("Nginx validation failed; restored")
		}
		e.deleted = append(e.deleted, op.Resource.ID)
		return model.Config{"steps": model.Config{"phase": "completed"}}, nil
	}
	return model.Config{}, nil
}
func TestBatchSiteDeletionPreservesFailuresBackupsAndSchedules(t *testing.T) {
	store, err := dao.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Close()
	executor := &siteDeleteExecutor{}
	app := &App{Store: store, Executor: executor}
	sites := []model.Resource{{Kind: "site", Name: "first.test", Status: "ready", Config: model.Config{"type": "static", "domains": "first.test", "instanceId": "nginx"}}, {Kind: "site", Name: "second.test", Status: "ready", Config: model.Config{"type": "static", "domains": "second.test", "instanceId": "nginx"}}}
	items := []model.SiteDeleteItem{}
	for i := range sites {
		if err = store.Create(&sites[i]); err != nil {
			t.Fatal(err)
		}
		items = append(items, model.SiteDeleteItem{ID: sites[i].ID, Revision: sites[i].Revision})
	}
	executor.fail = sites[1].ID
	backup := model.Resource{Kind: "backup", Name: "old-backup", Status: "ready", Config: model.Config{"targetId": sites[0].ID, "artifact": "retained.ovb"}}
	_ = store.Create(&backup)
	schedule := model.Resource{Kind: "schedule", Name: "old-schedule", Config: model.Config{"targetId": sites[0].ID, "enabled": true}}
	_ = store.Create(&schedule)
	task, err := app.QueueSiteDeletion(context.Background(), items, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.QueueSiteDeletion(context.Background(), items, ""); err == nil {
		t.Fatal("duplicate batch accepted")
	}
	app.runNext(context.Background())
	task, _ = store.Task(task.ID)
	if task.State != "failed" {
		t.Fatal(task)
	}
	if _, err = store.Resource(sites[0].ID); err == nil {
		t.Fatal("successful site retained")
	}
	if _, err = store.Resource(sites[1].ID); err != nil {
		t.Fatal("failed site removed")
	}
	retained, _ := store.Resource(backup.ID)
	if retained.Config.String("retainedTargetName") != sites[0].Name {
		t.Fatal(retained)
	}
	paused, _ := store.Resource(schedule.ID)
	if paused.Config.Bool("enabled") {
		t.Fatal("schedule still enabled")
	}
	result, err := store.TaskResult(task.ID)
	if err != nil || result.Int("failed") != 1 {
		t.Fatal(result, err)
	}
}
func TestSiteDeletionRejectsSharedDataAndChanges(t *testing.T) {
	store, _ := dao.Open(t.TempDir())
	defer store.DB.Close()
	app := &App{Store: store, Executor: &siteDeleteExecutor{}}
	db := model.Resource{Kind: "database", Name: "shared", Status: "ready", Config: model.Config{}}
	_ = store.Create(&db)
	a := model.Resource{Kind: "site", Name: "a.test", Config: model.Config{"type": "static", "domains": "a.test", "databaseId": db.ID, "instanceId": "nginx"}}
	_ = store.Create(&a)
	b := a
	b.ID = ""
	b.Name = "b.test"
	_ = store.Create(&b)
	if _, err := app.SiteDeletePlan(context.Background(), []model.SiteDeleteItem{{ID: a.ID, Revision: a.Revision, DeleteDatabase: true}}); err == nil {
		t.Fatal("shared database accepted")
	}
}
