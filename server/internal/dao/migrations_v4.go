package dao

import "opsview/internal/model"

func (s *Store) migrateV4() error {
	_, err := s.DB.Exec(`CREATE TABLE IF NOT EXISTS firewall_observations(system_id TEXT PRIMARY KEY,backend TEXT NOT NULL,resource_id TEXT NOT NULL DEFAULT '',payload TEXT NOT NULL,last_seen TEXT NOT NULL);
 PRAGMA user_version=4;`)
	return err
}

// Observation payloads use the same encrypted vault as existing panel metadata.
// A missing system rule is retained for difference previews, never replayed here.
func (s *Store) ObserveFirewall(status model.Config) error {
	backend := status.String("backend")
	if backend == "" {
		return nil
	}
	rows, ok := status["rules"].([]any)
	if !ok {
		return nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, v := range rows {
		row, ok := v.(map[string]any)
		if !ok {
			continue
		}
		id, _ := row["id"].(string)
		if id == "" {
			continue
		}
		if _, err = tx.Exec(`INSERT INTO firewall_observations(system_id,backend,payload,last_seen) VALUES(?,?,?,?) ON CONFLICT(system_id) DO UPDATE SET backend=excluded.backend,payload=excluded.payload,last_seen=excluded.last_seen`, id, backend, s.encode(row), Now()); err != nil {
			return err
		}
	}
	return tx.Commit()
}
