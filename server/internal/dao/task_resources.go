package dao

import (
	"database/sql"
	"opsview/internal/model"
	"opsview/internal/scheduling"
	"opsview/internal/security"
	"strings"
)

func (s *Store) taskKeys(tx *sql.Tx, op model.Operation) []string {
	keys := map[string]bool{}
	add := func(id string) {
		if id != "" {
			keys[id] = true
		}
	}
	var visit func(model.Resource, int)
	visit = func(r model.Resource, depth int) {
		if depth > 2 {
			return
		}
		add(r.ID)
		if r.Kind == "site" || r.Kind == "instance" && r.Config.String("software") == "nginx" {
			add("nginx-entry")
		}
		ids := []string{r.Config.String("instanceId"), r.Config.String("phpInstanceId")}
		ids = append(ids, scheduling.TargetIDs(r.Config)...)
		for _, id := range ids {
			if id == "" {
				continue
			}
			add(id)
			var cfg string
			var other model.Resource
			if tx.QueryRow(`SELECT id,kind,config FROM resources WHERE id=?`, id).Scan(&other.ID, &other.Kind, &cfg) == nil && s.decode(cfg, &other.Config) == nil {
				visit(other, depth+1)
			}
		}
	}
	add(op.Params.String("scheduleId"))
	visit(op.Resource, 0)
	// Exported params cannot supply locks; compute the set exclusively from registered resource relations.
	if op.Name == "panel.backup" || strings.HasPrefix(op.Name, "panel.archive-") || op.Name == "schedule.execute" && op.Resource.Config.String("operation") == "panel" {
		add("panel-backup")
	}
	out := []string{}
	for k := range keys {
		out = append(out, k)
	}
	return out
}
func (s *Store) backfillScheduleRuns() error {
	rows, err := s.DB.Query(`SELECT tasks.id,settings.value,tasks.created_at FROM tasks JOIN settings ON settings.key='schedule-task:'||tasks.id WHERE NOT EXISTS(SELECT 1 FROM schedule_runs WHERE task_id=tasks.id)`)
	if err != nil {
		return err
	}
	type entry struct{ id, schedule, created string }
	entries := []entry{}
	for rows.Next() {
		var e entry
		var enc string
		if err = rows.Scan(&e.id, &enc, &e.created); err != nil {
			rows.Close()
			return err
		}
		if err = s.decode(enc, &e.schedule); err != nil {
			rows.Close()
			return err
		}
		entries = append(entries, e)
	}
	err = rows.Close()
	if err != nil {
		return err
	}
	for _, e := range entries {
		if _, err = s.DB.Exec(`INSERT OR IGNORE INTO schedule_runs(task_id,schedule_id,created_at) VALUES(?,?,?)`, e.id, e.schedule, e.created); err != nil {
			return err
		}
	}
	return nil
}

// Skipped ticks are independent terminal history entries and never enter the work queue.
func (s *Store) RecordSkippedSchedule(id, reason string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tid, now := security.ID(), Now()
	if _, err = tx.Exec(`INSERT INTO tasks(id,resource_id,operation,state,error,created_at,updated_at,payload) VALUES(?,?,?,'skipped',?,?,?,?)`, tid, id, "schedule.execute", reason, now, now, s.encode(model.Operation{})); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO schedule_runs(task_id,schedule_id,created_at) VALUES(?,?,?)`, tid, id, now); err != nil {
		return err
	}
	return tx.Commit()
}
