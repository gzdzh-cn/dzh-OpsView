package dao

import (
	"opsview/internal/model"
	"testing"
)

func TestDatabaseNamesAreScopedAndRecycleReleasesIdentity(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	makeDB := func(id, inst string) model.Resource {
		return model.Resource{ID: id, Kind: "database", Name: "appdb", Config: model.Config{"instanceId": inst, "username": "appuser", "password": "never-plaintext"}}
	}
	a, b := makeDB("db-a", "mysql-a"), makeDB("db-b", "mysql-b")
	if err = s.Create(&a); err != nil {
		t.Fatal(err)
	}
	if err = s.Create(&b); err != nil {
		t.Fatal("different instance rejected", err)
	}
	duplicate := makeDB("db-c", "mysql-a")
	if err = s.Create(&duplicate); err == nil {
		t.Fatal("duplicate accepted")
	}
	a.Status = "recycled"
	a.Config["expiresAt"] = "2099-01-01T00:00:00Z"
	if err = s.ApplyDatabaseResources([]model.Resource{a}); err != nil {
		t.Fatal(err)
	}
	if err = s.Create(&duplicate); err != nil {
		t.Fatal("recycled identity held", err)
	}
	var stored string
	if err = s.DB.QueryRow("SELECT config FROM resources WHERE id=?", a.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == "never-plaintext" || len(stored) < 10 {
		t.Fatal("encryption missing")
	}
	var violations int
	rows, err := s.DB.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		violations++
	}
	rows.Close()
	if violations != 0 {
		t.Fatal("broken references")
	}
}

func TestDatabaseBindingChangesAreTransactional(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	a := model.Resource{Kind: "database", Name: "same", Config: model.Config{"instanceId": "first", "username": "alice"}}
	b := model.Resource{Kind: "database", Name: "same", Config: model.Config{"instanceId": "second", "username": "bob"}}
	if err = s.Create(&a); err != nil {
		t.Fatal(err)
	}
	if err = s.Create(&b); err != nil {
		t.Fatal(err)
	}
	a.Config["instanceId"] = "second"
	if err = s.Update(&a); err == nil {
		t.Fatal("migration conflict accepted")
	}
	stored, _ := s.Resource(a.ID)
	if stored.Config.String("instanceId") != "first" {
		t.Fatal("failed update committed")
	}
}

func TestV2MigrationPreservesEncryptedRowsAndRelations(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	i := model.Resource{Kind: "instance", Name: "mysql", Config: model.Config{"software": "mysql", "version": "8.4.8"}}
	if err = s.Create(&i); err != nil {
		t.Fatal(err)
	}
	db := model.Resource{Kind: "database", Name: "legacy", Config: model.Config{"instanceId": i.ID, "username": "legacy", "password": "must-remain-encrypted"}}
	if err = s.Create(&db); err != nil {
		t.Fatal(err)
	}
	var before string
	_ = s.DB.QueryRow("SELECT config FROM resources WHERE id=?", db.ID).Scan(&before)
	_, err = s.DB.Exec(`PRAGMA foreign_keys=OFF;
        DROP TABLE database_bindings; DROP TABLE database_recycle;
        CREATE TABLE legacy_resources (id TEXT PRIMARY KEY, kind TEXT NOT NULL, name TEXT NOT NULL, status TEXT NOT NULL, config TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, UNIQUE(kind,name));
        INSERT INTO legacy_resources SELECT * FROM resources;
        DROP TABLE resources; ALTER TABLE legacy_resources RENAME TO resources;
        PRAGMA user_version=2;`)
	if err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	var after string
	_ = s.DB.QueryRow("SELECT config FROM resources WHERE id=?", db.ID).Scan(&after)
	if before != after {
		t.Fatal("migration rewrote encrypted payload")
	}
	restored, err := s.Resource(db.ID)
	if err != nil || restored.Config.String("password") != "must-remain-encrypted" {
		t.Fatal("lost encrypted resource", err)
	}
	var held string
	if err = s.DB.QueryRow("SELECT resource_id FROM software_versions WHERE software='mysql'").Scan(&held); err != nil || held != i.ID {
		t.Fatal("lost version reference", err)
	}
	if err = s.DB.QueryRow("SELECT resource_id FROM database_bindings WHERE instance_id=?", i.ID).Scan(&held); err != nil || held != db.ID {
		t.Fatal("missing account binding", err)
	}
}
