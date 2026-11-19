package executor

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDirectoryArchiveIntegrityAndRestore(t *testing.T) {
	e, dir := fileFixture(t)
	e.Vault, _ = security.OpenVault(filepath.Join(e.Root, "executor.key"))
	if err := os.MkdirAll(filepath.Join(e.Root, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data.txt"), []byte("backup-data"), 0640); err != nil {
		t.Fatal(err)
	}
	r := model.Resource{ID: "schedule-backup", Kind: "schedule", Name: "directory", Config: model.Config{"operation": "directory", "cron": "* * * * *", "path": dir, "retention": 2}}
	result, err := e.directoryBackup(context.Background(), model.Operation{Resource: r}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	p := model.Config{"backupId": r.ID, "file": result.String("file")}
	if _, err = e.archiveOperation(context.Background(), model.Operation{Name: "archive.verify", Params: p}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(filepath.Dir(dir), "restored")
	p["destination"] = dest
	if _, err = e.archiveOperation(context.Background(), model.Operation{Name: "archive.restore-directory", Params: p}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dest, "data.txt"))
	if string(data) != "backup-data" {
		t.Fatal(string(data))
	}
	if _, err = e.archiveOperation(context.Background(), model.Operation{Name: "archive.restore-directory", Params: p}, func(string) {}); err == nil {
		t.Fatal("existing destination replaced")
	}
	_ = os.Remove(filepath.Join(e.Root, "backup.key"))
	p["destination"] = filepath.Join(filepath.Dir(dir), "missing-key")
	if _, err = e.archiveOperation(context.Background(), model.Operation{Name: "archive.restore-directory", Params: p}, func(string) {}); err == nil {
		t.Fatal("restored without key")
	}
	if _, err = os.Stat(filepath.Join(e.Root, "backup.key")); !os.IsNotExist(err) {
		t.Fatal("missing restore key regenerated")
	}
}

func TestDirectoryS3LifecycleAndFailureRetention(t *testing.T) {
	e, dir := fileFixture(t)
	e.Vault, _ = security.OpenVault(filepath.Join(e.Root, "executor.key"))
	_ = os.MkdirAll(filepath.Join(e.Root, "state"), 0700)
	objects := map[string][]byte{}
	var mu sync.Mutex
	failUpload := false
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
			t.Error("unsigned S3 request")
		}
		switch r.Method {
		case "PUT":
			if failUpload {
				http.Error(w, "unavailable", 503)
				return
			}
			raw, _ := io.ReadAll(r.Body)
			objects[r.URL.Path] = raw
			w.Header().Set("ETag", `"fixture"`)
			w.WriteHeader(200)
		case "GET":
			raw, ok := objects[r.URL.Path]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
			w.Write(raw)
		case "DELETE":
			delete(objects, r.URL.Path)
			w.WriteHeader(204)
		default:
			w.WriteHeader(405)
		}
	}))
	defer remote.Close()
	storage := model.Resource{ID: "backup-store-fixture", Kind: "backup-storage", Config: model.Config{"endpoint": remote.URL, "bucket": "backups", "region": "us-east-1", "accessKey": "fixture-access", "secretKey": "fixture-secret", "immutableId": "backup-store-fixture"}}
	if err := e.save(storage); err != nil {
		t.Fatal(err)
	}
	storage.ID = "backup-storage-test"
	if err := e.save(storage); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data"), []byte("restore-from-s3"), 0600); err != nil {
		t.Fatal(err)
	}
	r := model.Resource{ID: "s3-schedule", Kind: "schedule", Name: "remote", Config: model.Config{"path": dir, "retention": 1, "storageId": "test"}}
	result, err := e.directoryBackup(context.Background(), model.Operation{Resource: r}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(e.Root, "backups", r.ID, result.String("file"))
	if err = os.Remove(local); err != nil {
		t.Fatal(err)
	}
	p := model.Config{"backupId": r.ID, "file": result.String("file"), "destination": filepath.Join(filepath.Dir(dir), "s3-restored")}
	if _, err = e.archiveOperation(context.Background(), model.Operation{Name: "archive.restore-directory", Params: p}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(p.String("destination"), "data"))
	if string(got) != "restore-from-s3" {
		t.Fatal(string(got))
	}
	mu.Lock()
	failUpload = true
	mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err = e.directoryBackup(ctx, model.Operation{Resource: r}, func(string) {}); err == nil {
		t.Fatal("failed upload reported success")
	}
	if _, err = os.Stat(local); err != nil {
		t.Fatal("previous success deleted", err)
	}
	saved, err := e.load(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(saved.Config["history"])
	var h []model.Config
	_ = json.Unmarshal(raw, &h)
	if len(h) != 1 {
		t.Fatal("failed archive entered trusted history", string(raw))
	}
	mu.Lock()
	failUpload = false
	mu.Unlock()
	second, err := e.directoryBackup(context.Background(), model.Operation{Resource: r}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	_, exists := objects["/backups/"+r.ID+"/"+result.String("file")]
	mu.Unlock()
	if exists {
		t.Fatal("remote retention did not remove oldest archive")
	}
	p["file"] = second.String("file")
	if _, err = e.archiveOperation(context.Background(), model.Operation{Name: "archive.delete", Params: p}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(objects) != 0 {
		t.Fatal("remote delete left backup objects", len(objects))
	}
}
