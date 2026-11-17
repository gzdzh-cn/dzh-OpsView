package logic

import (
	"context"
	"errors"
	"opsview/internal/dao"
	"opsview/internal/model"
	"opsview/internal/runtimeconfig"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testExecutor struct {
	calls int
	fail  bool
}

func (e *testExecutor) Execute(_ context.Context, op model.Operation, log func(string)) (model.Config, error) {
	e.calls++
	log("configured password=" + op.Resource.Config.String("password"))
	if e.fail {
		return nil, errors.New("failure with " + op.Resource.Config.String("password"))
	}
	return model.Config{"workingDirectory": "/managed/apps/test"}, nil
}
func TestTaskExecutionFailureRedactionAndRestart(t *testing.T) {
	store, err := dao.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Close()
	executor := &testExecutor{}
	app := &App{Store: store, Executor: executor}
	resource := model.Resource{Kind: "app", Name: "test", Config: model.Config{"password": "task-secret-value", "executable": "/usr/bin/test", "user": "nobody"}}
	if err = store.Create(&resource); err != nil {
		t.Fatal(err)
	}
	task, err := app.Queue(resource, "apply", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.Queue(resource, "apply", nil); err == nil {
		t.Fatal("duplicate resource mutation accepted")
	}
	app.runNext(context.Background())
	tasks, _ := store.Tasks()
	if tasks[0].State != "succeeded" {
		t.Fatal(tasks)
	}
	events, _ := store.Events(task.ID, 0)
	for _, event := range events {
		if strings.Contains(event.Message, "task-secret-value") {
			t.Fatal("secret leaked")
		}
	}
	current, _ := store.Resource(resource.ID)
	if current.Status != "ready" || current.Config.String("workingDirectory") == "" {
		t.Fatal(current)
	}
	executor.fail = true
	if _, err = app.Queue(current, "apply", nil); err != nil {
		t.Fatal(err)
	}
	app.runNext(context.Background())
	tasks, _ = store.Tasks()
	for _, task := range tasks {
		if strings.Contains(task.Error, "task-secret-value") {
			t.Fatal("error leaked secret")
		}
	}
	current, _ = store.Resource(resource.ID)
	if current.Status != "error" {
		t.Fatal(current)
	}
	if _, err = app.Queue(current, "apply", nil); err != nil {
		t.Fatal(err)
	}
	app.Start()
	app.Stop()
	tasks, _ = store.Tasks()
	interrupted := 0
	for _, task := range tasks {
		if task.State == "interrupted" {
			interrupted++
		}
	}
	if interrupted != 1 || executor.calls != 2 {
		t.Fatal("restart replayed an operation", interrupted, executor.calls)
	}
}
func TestPanelBackupRestoreIntoNewDirectory(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	store, err := dao.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Close()
	if err = store.Set("admin", model.Config{"username": "test-admin", "passwordHash": "protected-value"}); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "panel.pem"), []byte("certificate-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "runtime.json")
	cfg := runtimeconfig.Default()
	cfg.Port = 18881
	cfg.PublicHost = "panel.example.com"
	if err = runtimeconfig.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	app := &App{Store: store, PanelDir: source, PanelConfigPath: configPath}
	result, err := app.BackupPanel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(source, "backups", result.String("metadataBackup"))
	raw, _ := os.ReadFile(artifact)
	if strings.Contains(string(raw), "certificate-fixture") || strings.Contains(string(raw), "protected-value") {
		t.Fatal("unencrypted snapshot")
	}
	destination := filepath.Join(dir, "restored")
	if err = RestorePanel(source, artifact, destination); err != nil {
		t.Fatal(err)
	}
	restored, err := dao.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.DB.Close()
	var admin model.Config
	if err = restored.Setting("admin", &admin); err != nil || admin.String("username") != "test-admin" {
		t.Fatal(admin, err)
	}
	snapshot, e := runtimeconfig.Load(filepath.Join(destination, "panel.json"))
	if e != nil || snapshot != cfg {
		t.Fatal("runtime snapshot lost", snapshot, e)
	}
	original, e := runtimeconfig.Load(configPath)
	if e != nil || original != cfg {
		t.Fatal("restore changed runtime config")
	}
	cert, _ := os.ReadFile(filepath.Join(destination, "panel.pem"))
	if string(cert) != "certificate-fixture" {
		t.Fatal("certificate lost")
	}
	if err = RestorePanel(source, artifact, destination); err == nil {
		t.Fatal("overwrote existing directory")
	}
	raw[len(raw)/2] ^= 1
	corrupted := filepath.Join(dir, "corrupted.ovp")
	_ = os.WriteFile(corrupted, raw, 0600)
	if err = RestorePanel(source, corrupted, filepath.Join(dir, "bad")); err == nil {
		t.Fatal("accepted tampered snapshot")
	}
}
func TestNginxAdvancedDirectiveBoundaries(t *testing.T) {
	if err := ValidateNginxAdvanced("client_max_body_size 32m;\nproxy_read_timeout 60s;\ngzip on;"); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"include /etc/passwd;", "root /etc;", "gzip on; gzip off;", "proxy_read_timeout 1s;\nproxy_read_timeout 2s;", "client_max_body_size 1m;\n}"} {
		if ValidateNginxAdvanced(text) == nil {
			t.Fatal("accepted unsafe directive", text)
		}
	}
}

func TestLogMaintenancePreservesInstanceStateAndMetadata(t *testing.T) {
	store, err := dao.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Close()
	app := &App{Store: store, Executor: &testExecutor{}}
	r := model.Resource{Kind: "instance", Name: "stopped-db", Config: model.Config{"software": "mysql"}}
	if err = store.Create(&r); err != nil {
		t.Fatal(err)
	}
	r.Status = "stopped"
	if err = store.Update(&r); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"rotate-log", "delete-log-archive"} {
		if _, err = app.Queue(r, action, model.Config{"kind": "error", "confirmed": true}); err != nil {
			t.Fatal(err)
		}
		app.runNext(context.Background())
		current, err := store.Resource(r.ID)
		if err != nil || current.Status != "stopped" || current.Revision != r.Revision || current.Config.String("workingDirectory") != "" {
			t.Fatal("log maintenance changed instance state", current, err)
		}
	}
}
