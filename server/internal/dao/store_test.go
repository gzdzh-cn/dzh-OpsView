package dao

import (
	"database/sql"
	"errors"
	"opsview/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncryptedPersistenceAndRevision(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := model.Resource{Kind: "instance", Name: "mysql", Config: model.Config{"password": "secret-in-test-123", "port": 3306}}
	if err = s.Create(&r); err != nil {
		t.Fatal(err)
	}
	stale := r
	r.Config["port"] = 3307
	if err = s.Update(&r); err != nil {
		t.Fatal(err)
	}
	if err = s.Update(&stale); err == nil {
		t.Fatal("stale revision overwrote newer config")
	}
	s.DB.Close()
	for _, name := range []string{"opsview.db", "opsview.db-wal"} {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		if strings.Contains(string(b), "secret-in-test-123") {
			t.Fatal("plaintext credential in SQLite")
		}
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	loaded, err := s.Resource(r.ID)
	if err != nil || loaded.Config.String("password") != "secret-in-test-123" || loaded.Config.Int("port") != 3307 {
		t.Fatal(loaded, err)
	}
}
func TestTaskLockAndResumeEvents(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	op := model.Operation{Name: "instance.install", Resource: model.Resource{ID: "resource-1", Kind: "instance", Config: model.Config{"password": "dont-print"}}}
	first, err := s.Enqueue(op)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Enqueue(op); err == nil {
		t.Fatal("duplicate resource task accepted")
	}
	claimed, p, err := s.Claim()
	if err != nil || claimed.ID != first.ID || p.Resource.Config.String("password") != "dont-print" {
		t.Fatal(claimed, p, err)
	}
	if _, _, err = s.Claim(); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	s.Event(first.ID, "first")
	s.Event(first.ID, "second")
	events, err := s.Events(first.ID, 0)
	if err != nil || len(events) != 2 {
		t.Fatal(events, err)
	}
	tail, err := s.Events(first.ID, events[0].Sequence)
	if err != nil || len(tail) != 1 || tail[0].Message != "second" {
		t.Fatal(tail, err)
	}
	if err = s.Finish(first.ID, "succeeded", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Enqueue(op); err != nil {
		t.Fatal("lock was not released", err)
	}
}
