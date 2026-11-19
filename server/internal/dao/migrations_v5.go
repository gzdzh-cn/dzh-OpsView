package dao

import (
	"database/sql"
	"errors"
	"opsview/internal/model"
)

func (s *Store) migrateV5() error {
	_, err := s.DB.Exec(`CREATE TABLE IF NOT EXISTS script_revisions(script_id TEXT NOT NULL,revision INTEGER NOT NULL,name TEXT NOT NULL,config TEXT NOT NULL,created_at TEXT NOT NULL,PRIMARY KEY(script_id,revision));
 CREATE TABLE IF NOT EXISTS schedule_runs(task_id TEXT PRIMARY KEY, schedule_id TEXT NOT NULL, result TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS schedule_runs_schedule ON schedule_runs(schedule_id,created_at);
 CREATE TABLE IF NOT EXISTS task_resource_locks(task_id TEXT NOT NULL,resource_key TEXT NOT NULL,PRIMARY KEY(task_id,resource_key));
 CREATE INDEX IF NOT EXISTS task_resource_key ON task_resource_locks(resource_key);
 INSERT OR IGNORE INTO task_resource_locks SELECT id,resource_id FROM tasks WHERE state IN ('queued','running');
 CREATE TABLE IF NOT EXISTS session_activity(hash TEXT PRIMARY KEY, last_seen INTEGER NOT NULL, ip TEXT NOT NULL DEFAULT '', agent TEXT NOT NULL DEFAULT '');
 CREATE TABLE IF NOT EXISTS setting_revisions(key TEXT PRIMARY KEY,revision INTEGER NOT NULL DEFAULT 1);
 CREATE TABLE IF NOT EXISTS security_tokens(id TEXT PRIMARY KEY,kind TEXT NOT NULL,hash TEXT NOT NULL UNIQUE,config TEXT NOT NULL,expires INTEGER NOT NULL,last_used INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS notification_events(id TEXT PRIMARY KEY,rule_id TEXT NOT NULL,state TEXT NOT NULL,config TEXT NOT NULL,created_at TEXT NOT NULL);
 PRAGMA user_version=5;`)
	if err != nil {
		return err
	}
	return s.backfillScheduleRuns()
}

var ErrRevision = errors.New("设置已被修改，请重新载入")

func (s *Store) SettingRevision(key string, out any) (int, error) {
	// Read value and revision in one transaction so an editor never receives a mismatched pair.
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var encoded string
	err = tx.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&encoded)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if err == nil {
		if err = s.decode(encoded, out); err != nil {
			return 0, err
		}
	}
	rev := 0
	er := tx.QueryRow(`SELECT revision FROM setting_revisions WHERE key=?`, key).Scan(&rev)
	if er != nil && !errors.Is(er, sql.ErrNoRows) {
		return 0, er
	}
	return rev, tx.Commit()
}
func (s *Store) PatchSetting(key string, revision int, patch model.Config, action, ip string) (model.Config, int, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	if key == "panel" {
		if err = s.noPendingTx(tx); err != nil {
			return nil, 0, err
		}
	}
	value, rev, err := s.patchTx(tx, key, revision, patch, action, ip)
	if err != nil {
		return nil, rev, err
	}
	return value, rev, tx.Commit()
}
func (s *Store) SaveTaskResult(id string, v model.Config) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.setTx(tx, "task-result:"+id, v); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE schedule_runs SET result=? WHERE task_id=?`, s.encode(v), id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) TaskResult(id string) (model.Config, error) {
	v := model.Config{}
	err := s.Setting("task-result:"+id, &v)
	return v, err
}
func (s *Store) ScheduleRuns(id string) ([]model.Config, error) {
	rows, err := s.DB.Query(`SELECT tasks.id,tasks.state,tasks.error,tasks.created_at,tasks.updated_at,COALESCE(schedule_runs.result,'') FROM tasks JOIN schedule_runs ON schedule_runs.task_id=tasks.id WHERE schedule_runs.schedule_id=? ORDER BY tasks.created_at DESC LIMIT 500`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Config{}
	for rows.Next() {
		var tid, state, msg, created, updated, result string
		if err = rows.Scan(&tid, &state, &msg, &created, &updated, &result); err != nil {
			return nil, err
		}
		v := model.Config{"id": tid, "state": state, "error": msg, "createdAt": created, "updatedAt": updated}
		if result != "" {
			var data model.Config
			if err = s.decode(result, &data); err != nil {
				return nil, err
			}
			v["result"] = data
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) CreateToken(id, kind, hash string, c model.Config, expires int64) error {
	_, err := s.DB.Exec(`INSERT INTO security_tokens(id,kind,hash,config,expires) VALUES(?,?,?,?,?)`, id, kind, hash, s.encode(c), expires)
	return err
}
func (s *Store) Token(hash string) (string, string, model.Config, int64, error) {
	var id, kind, enc string
	var expiry int64
	err := s.DB.QueryRow(`SELECT id,kind,config,expires FROM security_tokens WHERE hash=?`, hash).Scan(&id, &kind, &enc, &expiry)
	v := model.Config{}
	if err == nil {
		err = s.decode(enc, &v)
	}
	return id, kind, v, expiry, err
}
func (s *Store) Tokens(kind string) ([]model.Config, error) {
	rows, err := s.DB.Query(`SELECT id,config,expires,last_used FROM security_tokens WHERE kind IN (?,?) ORDER BY rowid DESC`, kind, kind+"-session")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Config{}
	for rows.Next() {
		var id, enc string
		var expires, last int64
		if err = rows.Scan(&id, &enc, &expires, &last); err != nil {
			return nil, err
		}
		v := model.Config{}
		if err = s.decode(enc, &v); err != nil {
			return nil, err
		}
		delete(v, "secret")
		v["id"] = id
		v["expires"] = expires
		v["lastUsed"] = last
		out = append(out, v)
	}
	return out, rows.Err()
}
