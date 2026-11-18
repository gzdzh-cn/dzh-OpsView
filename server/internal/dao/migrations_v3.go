package dao

import (
	"opsview/internal/model"
)

// Database names are scoped to an instance. Existing IDs and encrypted payloads
// are copied verbatim; downgrade requires the pre-upgrade metadata backup.
func (s *Store) migrateV3() error {
	var version int
	if err := s.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version >= 3 {
		return nil
	}
	resources, err := s.Resources("database")
	if err != nil {
		return err
	}
	if _, err = s.DB.Exec("PRAGMA foreign_keys=OFF"); err != nil {
		return err
	}
	defer s.DB.Exec("PRAGMA foreign_keys=ON")
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`CREATE TABLE resources_v3 (id TEXT PRIMARY KEY, kind TEXT NOT NULL, name TEXT NOT NULL, status TEXT NOT NULL, config TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
 INSERT INTO resources_v3 SELECT * FROM resources;
 DROP TABLE resources;
 ALTER TABLE resources_v3 RENAME TO resources;
 CREATE UNIQUE INDEX resource_name ON resources(kind,name) WHERE kind!='database';
 CREATE TABLE IF NOT EXISTS database_bindings(resource_id TEXT PRIMARY KEY REFERENCES resources(id) ON DELETE CASCADE, instance_id TEXT NOT NULL, database_name TEXT NOT NULL, account_name TEXT NOT NULL, imported INTEGER NOT NULL DEFAULT 0, UNIQUE(instance_id,database_name));
 CREATE TABLE IF NOT EXISTS database_recycle(resource_id TEXT PRIMARY KEY REFERENCES resources(id) ON DELETE CASCADE, expires_at TEXT NOT NULL);
 PRAGMA user_version=3;`)
	if err != nil {
		return err
	}
	for _, r := range resources {
		if _, err = tx.Exec(`INSERT OR IGNORE INTO database_bindings VALUES(?,?,?,?,?)`, r.ID, r.Config.String("instanceId"), r.Name, r.Config.String("username"), r.Config.Bool("imported")); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ApplyDatabaseResources commits executor results together, including account
// changes shared by several databases. It never accepts browser-provided rows.
func (s *Store) ApplyDatabaseResources(resources []model.Resource) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range resources {
		if r.Kind != "database" && r.Kind != "instance" && r.Kind != "backup" && r.Kind != "firewall" {
			continue
		}
		var current string
		if er := tx.QueryRow(`SELECT config FROM resources WHERE id=?`, r.ID).Scan(&current); er == nil {
			var old model.Config
			if s.decode(current, &old) == nil {
				if note, exists := old["note"]; exists {
					r.Config["note"] = note
				}
			}
		}
		if r.CreatedAt == "" {
			r.CreatedAt = Now()
		}
		if r.Status == "" {
			r.Status = "ready"
		}
		_, err = tx.Exec(`INSERT INTO resources VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status,config=excluded.config,revision=resources.revision+1,updated_at=excluded.updated_at`, r.ID, r.Kind, r.Name, r.Status, s.encode(r.Config), 1, r.CreatedAt, Now())
		if err != nil {
			return err
		}
		if r.Kind == "database" {
			if _, err = tx.Exec(`DELETE FROM database_bindings WHERE resource_id=?`, r.ID); err != nil {
				return err
			}
			if r.Status == "recycled" {
				_, err = tx.Exec(`INSERT OR REPLACE INTO database_recycle VALUES(?,?)`, r.ID, r.Config.String("expiresAt"))
			} else {
				_, err = tx.Exec(`INSERT INTO database_bindings VALUES(?,?,?,?,?)`, r.ID, r.Config.String("instanceId"), r.Name, r.Config.String("username"), r.Config.Bool("imported"))
			}
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
