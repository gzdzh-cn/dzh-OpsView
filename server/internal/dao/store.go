package dao

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	_ "modernc.org/sqlite"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"time"
)

type Store struct {
	DB        *sql.DB
	Vault     *security.Vault
	AlertWake chan struct{}
}

func Open(dir string) (*Store, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	v, e := security.OpenVault(filepath.Join(dir, "master.key"))
	if e != nil {
		return nil, e
	}
	db, e := sql.Open("sqlite", filepath.Join(dir, "opsview.db"))
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	var schemaVersion int
	if e = db.QueryRow("PRAGMA user_version").Scan(&schemaVersion); e != nil {
		db.Close()
		return nil, e
	}
	if schemaVersion > 5 {
		db.Close()
		return nil, errors.New("面板数据库来自较新版本，禁止降级打开")
	}

	s := &Store{DB: db, Vault: v, AlertWake: make(chan struct{}, 1)}
	_, e = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS resources (id TEXT PRIMARY KEY, kind TEXT NOT NULL, name TEXT NOT NULL, status TEXT NOT NULL, config TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, UNIQUE(kind,name));
 CREATE TABLE IF NOT EXISTS tasks (id TEXT PRIMARY KEY, resource_id TEXT NOT NULL, operation TEXT NOT NULL, state TEXT NOT NULL, error TEXT NOT NULL DEFAULT '', payload TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS events (sequence INTEGER PRIMARY KEY AUTOINCREMENT, task_id TEXT NOT NULL, message TEXT NOT NULL, created_at TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS events_task ON events(task_id,sequence);
 CREATE TABLE IF NOT EXISTS audit (id INTEGER PRIMARY KEY AUTOINCREMENT, action TEXT NOT NULL, resource_id TEXT NOT NULL, ip TEXT NOT NULL, created_at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS sessions (hash TEXT PRIMARY KEY, csrf TEXT NOT NULL, expires INTEGER NOT NULL);
`)
	if e != nil {
		db.Close()
		return nil, e
	}
	if e = s.migrateV2(); e != nil {
		db.Close()
		return nil, e
	}
	if e = s.migrateV3(); e != nil {
		db.Close()
		return nil, e
	}
	if e = s.migrateV4(); e != nil {
		db.Close()
		return nil, e
	}
	if e = s.migrateV5(); e != nil {
		db.Close()
		return nil, e
	}
	return s, nil
}
func Now() string                    { return time.Now().UTC().Format(time.RFC3339) }
func (s *Store) encode(v any) string { b, _ := json.Marshal(v); return s.Vault.Encrypt(b) }
func (s *Store) decode(v string, out any) error {
	b, e := s.Vault.Decrypt(v)
	if e != nil {
		return e
	}
	return json.Unmarshal(b, out)
}
func (s *Store) Setting(k string, out any) error {
	var v string
	if e := s.DB.QueryRow(`SELECT value FROM settings WHERE key=?`, k).Scan(&v); e != nil {
		return e
	}
	return s.decode(v, out)
}
func (s *Store) Set(k string, v any) error {
	if k == "panel" {
		tx, err := s.DB.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err = s.noPendingTx(tx); err != nil {
			return err
		}
		if err = s.setTx(tx, k, v); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO setting_revisions VALUES('panel',1) ON CONFLICT(key) DO UPDATE SET revision=revision+1`); err != nil {
			return err
		}
		return tx.Commit()
	}

	_, e := s.DB.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, s.encode(v))
	return e
}
func (s *Store) Resources(kind string) ([]model.Resource, error) {
	q := `SELECT id,kind,name,status,config,revision,created_at,updated_at FROM resources`
	args := []any{}
	if kind != "" {
		q += ` WHERE kind=?`
		args = append(args, kind)
	}
	q += ` ORDER BY created_at DESC`
	rows, e := s.DB.Query(q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Resource{}
	for rows.Next() {
		var r model.Resource
		var c string
		if e = rows.Scan(&r.ID, &r.Kind, &r.Name, &r.Status, &c, &r.Revision, &r.CreatedAt, &r.UpdatedAt); e != nil {
			return nil, e
		}
		if e = s.decode(c, &r.Config); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) Resource(id string) (model.Resource, error) {
	var r model.Resource
	var c string
	e := s.DB.QueryRow(`SELECT id,kind,name,status,config,revision,created_at,updated_at FROM resources WHERE id=?`, id).Scan(&r.ID, &r.Kind, &r.Name, &r.Status, &c, &r.Revision, &r.CreatedAt, &r.UpdatedAt)
	if e == nil {
		e = s.decode(c, &r.Config)
	}
	return r, e
}
func (s *Store) Create(r *model.Resource) error {
	if r.ID == "" {
		r.ID = security.ID()
	}
	r.CreatedAt = Now()
	r.UpdatedAt = r.CreatedAt
	r.Revision = 1
	if r.Status == "" {
		r.Status = "pending"
	}
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	sw, ver := instanceVersion(r)
	if sw != "" && ver != "" {
		var existing string
		er := tx.QueryRow(`SELECT resource_id FROM software_versions WHERE software=? AND version=?`, sw, ver).Scan(&existing)
		if er == nil {
			return &model.DuplicateVersion{ResourceID: existing}
		}
		if !errors.Is(er, sql.ErrNoRows) {
			return er
		}
	}
	_, e = tx.Exec(`INSERT INTO resources VALUES(?,?,?,?,?,?,?,?)`, r.ID, r.Kind, r.Name, r.Status, s.encode(r.Config), r.Revision, r.CreatedAt, r.UpdatedAt)
	if e != nil {
		return e
	}
	if r.Kind == "database" {
		if _, e = tx.Exec(`INSERT INTO database_bindings VALUES(?,?,?,?,?)`, r.ID, r.Config.String("instanceId"), r.Name, r.Config.String("username"), r.Config.Bool("imported")); e != nil {
			return e
		}
	}
	if sw != "" && ver != "" {
		if _, e = tx.Exec(`INSERT INTO software_versions VALUES(?,?,?)`, sw, ver, r.ID); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) Update(r *model.Resource) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if r.Kind == "schedule" {
		var n int
		if e = tx.QueryRow(`SELECT COUNT(*) FROM schedule_runs JOIN tasks ON tasks.id=schedule_runs.task_id WHERE schedule_id=? AND state IN ('queued','running')`, r.ID).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			return errors.New("计划正在执行，请先停止")
		}
	}
	res, e := tx.Exec(`UPDATE resources SET name=?,status=?,config=?,revision=revision+1,updated_at=? WHERE id=? AND revision=?`, r.Name, r.Status, s.encode(r.Config), Now(), r.ID, r.Revision)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("资源已被修改，请刷新后重试")
	}
	if r.Kind == "database" {
		if _, e = tx.Exec(`DELETE FROM database_bindings WHERE resource_id=?`, r.ID); e != nil {
			return e
		}
		if r.Status != "recycled" {
			if _, e = tx.Exec(`INSERT INTO database_bindings VALUES(?,?,?,?,?)`, r.ID, r.Config.String("instanceId"), r.Name, r.Config.String("username"), r.Config.Bool("imported")); e != nil {
				return e
			}
		}
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	r.Revision++
	return nil
}
func (s *Store) Delete(id string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM schedule_runs JOIN tasks ON tasks.id=schedule_runs.task_id WHERE schedule_id=? AND state IN ('queued','running')`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return errors.New("计划正在执行，请先停止")
	}
	if _, err = tx.Exec(`DELETE FROM resources WHERE id=?`, id); err != nil {
		return err
	}
	// Historical duplicate instances remain version occupants after the selected
	// representative is removed. Config is encrypted, so rebuild via decoded rows.
	rows, err := tx.Query(`SELECT id,config FROM resources WHERE kind='instance'`)
	if err != nil {
		return err
	}
	type held struct{ id, software, version string }
	remaining := []held{}
	for rows.Next() {
		var id, encrypted string
		if err = rows.Scan(&id, &encrypted); err != nil {
			rows.Close()
			return err
		}
		var config model.Config
		if err = s.decode(encrypted, &config); err != nil {
			rows.Close()
			return err
		}
		if config.String("software") != "" && config.String("version") != "" {
			remaining = append(remaining, held{id, config.String("software"), config.String("version")})
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, r := range remaining {
		if _, err = tx.Exec(`INSERT OR IGNORE INTO software_versions VALUES(?,?,?)`, r.software, r.version, r.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) Audit(action, id, ip string) {
	defer func() {
		if action == "auth.failed" {
			s.WakeAlerts()
		}
	}()
	_, _ = s.DB.Exec(`INSERT INTO audit(action,resource_id,ip,created_at) VALUES(?,?,?,?)`, action, id, ip, Now())
}
func (s *Store) Audits() ([]model.Audit, error) {
	rows, e := s.DB.Query(`SELECT id,action,resource_id,ip,created_at FROM audit ORDER BY id DESC LIMIT 300`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Audit{}
	for rows.Next() {
		var a model.Audit
		if e = rows.Scan(&a.ID, &a.Action, &a.ResourceID, &a.IP, &a.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) Enqueue(op model.Operation) (model.Task, error) {
	t := model.Task{ScheduleID: op.Params.String("scheduleId"), ID: security.ID(), ResourceID: op.Resource.ID, Operation: op.Name, State: "queued", CreatedAt: Now(), UpdatedAt: Now()}
	tx, e := s.DB.Begin()
	if e != nil {
		return t, e
	}
	defer tx.Rollback()
	var n int
	if e = tx.QueryRow(`SELECT count(*) FROM tasks WHERE resource_id=? AND state IN ('queued','running')`, t.ResourceID).Scan(&n); e != nil {
		return t, e
	}
	if n > 0 {
		return t, errors.New("该资源已有待执行或运行中的任务")
	}
	keys := s.taskKeys(tx, op)
	for _, key := range keys {
		if e = tx.QueryRow(`SELECT count(*) FROM task_resource_locks JOIN tasks ON tasks.id=task_resource_locks.task_id WHERE resource_key=? AND state IN ('queued','running')`, key).Scan(&n); e != nil {
			return t, e
		}
		if n > 0 {
			return t, errors.New("关联资源已有待执行或运行中的任务")
		}
	}
	_, e = tx.Exec(`INSERT INTO tasks VALUES(?,?,?,?,?,?,?,?)`, t.ID, t.ResourceID, t.Operation, t.State, "", s.encode(op), t.CreatedAt, t.UpdatedAt)
	if e != nil {
		return t, e
	}
	for _, key := range keys {
		if _, e = tx.Exec(`INSERT INTO task_resource_locks VALUES(?,?)`, t.ID, key); e != nil {
			return t, e
		}
	}
	if t.ScheduleID != "" {
		if _, e = tx.Exec(`INSERT INTO schedule_runs(task_id,schedule_id,created_at) VALUES(?,?,?)`, t.ID, t.ScheduleID, t.CreatedAt); e != nil {
			return t, e
		}
		if _, e = tx.Exec(`INSERT INTO settings(key,value) VALUES(?,?)`, "schedule-task:"+t.ID, s.encode(t.ScheduleID)); e != nil {
			return t, e
		}
	}
	return t, tx.Commit()
}
func (s *Store) Task(id string) (model.Task, error) {
	var t model.Task
	err := s.DB.QueryRow(`SELECT id,resource_id,operation,state,error,created_at,updated_at FROM tasks WHERE id=?`, id).Scan(&t.ID, &t.ResourceID, &t.Operation, &t.State, &t.Error, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}
func (s *Store) Tasks() ([]model.Task, error) {
	rows, e := s.DB.Query(`SELECT tasks.id,resource_id,operation,state,error,created_at,updated_at,COALESCE(settings.value,'') FROM tasks LEFT JOIN settings ON settings.key='schedule-task:'||tasks.id ORDER BY created_at DESC LIMIT 200`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Task{}
	for rows.Next() {
		var t model.Task
		var payload string

		if e = rows.Scan(&t.ID, &t.ResourceID, &t.Operation, &t.State, &t.Error, &t.CreatedAt, &t.UpdatedAt, &payload); e != nil {
			return nil, e
		}
		if payload != "" {
			if e = s.decode(payload, &t.ScheduleID); e != nil {
				return nil, e
			}
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) Claim() (model.Task, model.Operation, error) {
	var t model.Task
	var op model.Operation
	var p string
	tx, e := s.DB.Begin()
	if e != nil {
		return t, op, e
	}
	defer tx.Rollback()
	e = tx.QueryRow(`SELECT id,resource_id,operation,state,error,created_at,updated_at,payload FROM tasks WHERE state='queued' ORDER BY rowid LIMIT 1`).Scan(&t.ID, &t.ResourceID, &t.Operation, &t.State, &t.Error, &t.CreatedAt, &t.UpdatedAt, &p)
	if e != nil {
		return t, op, e
	}
	if e = s.decode(p, &op); e != nil {
		return t, op, e
	}
	_, e = tx.Exec(`UPDATE tasks SET state='running',updated_at=? WHERE id=? AND state='queued'`, Now(), t.ID)
	if e != nil {
		return t, op, e
	}
	return t, op, tx.Commit()
}
func (s *Store) Finish(id, state, msg string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previous, operation string
	if err = tx.QueryRow(`SELECT state,operation FROM tasks WHERE id=?`, id).Scan(&previous, &operation); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE tasks SET state=?,error=?,updated_at=? WHERE id=?`, state, msg, Now(), id); err != nil {
		return err
	}
	failed := (state == "failed" || state == "interrupted") && previous != state
	if failed {
		if _, err = tx.Exec(`INSERT INTO audit(action,resource_id,ip,created_at) VALUES(?,?,'local-task',?)`, "task.failed:"+operation, id, Now()); err != nil {
			return err
		}
		if operation == "schedule.execute" {
			var payload string
			var op model.Operation
			if tx.QueryRow(`SELECT payload FROM tasks WHERE id=?`, id).Scan(&payload) == nil && s.decode(payload, &op) == nil && (op.Resource.Config.String("operation") == "panel" || op.Resource.Config.String("operation") == "directory" || op.Resource.Config.String("operation") == "backup") {
				if _, err = tx.Exec(`INSERT INTO audit(action,resource_id,ip,created_at) VALUES('backup.failed',?,'local-task',?)`, id, Now()); err != nil {
					return err
				}
			}
		}

	}
	err = tx.Commit()
	if err == nil && failed {
		s.WakeAlerts()
	}
	return err
}
func (s *Store) WakeAlerts() {
	select {
	case s.AlertWake <- struct{}{}:
	default:
	}
}
func (s *Store) Event(id, msg string) {
	_, _ = s.DB.Exec(`INSERT INTO events(task_id,message,created_at) VALUES(?,?,?)`, id, msg, Now())
}
func (s *Store) Events(id string, after int64) ([]model.Event, error) {
	rows, e := s.DB.Query(`SELECT sequence,task_id,message,created_at FROM events WHERE task_id=? AND sequence>? ORDER BY sequence LIMIT 500`, id, after)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Event{}
	for rows.Next() {
		var v model.Event
		if e = rows.Scan(&v.Sequence, &v.TaskID, &v.Message, &v.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) Snapshot(ctx context.Context, path string) error {
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		return fmt.Errorf("目标文件已存在")
	}
	_, e := s.DB.ExecContext(ctx, `VACUUM INTO ?`, path)
	return e
}
