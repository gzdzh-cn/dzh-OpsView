package dao

func (s *Store) PruneScheduleHistory(id, cutoff string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	query := `SELECT tasks.id FROM tasks JOIN schedule_runs ON schedule_runs.task_id=tasks.id WHERE schedule_runs.schedule_id=? AND tasks.created_at<? AND tasks.state NOT IN ('queued','running')`
	for _, table := range []string{"events", "task_resource_locks"} {
		if _, err = tx.Exec(`DELETE FROM `+table+` WHERE task_id IN (`+query+`)`, id, cutoff); err != nil {
			return err
		}
	}
	for _, prefix := range []string{"task-result:", "schedule-task:", "task-log-truncated:"} {
		if _, err = tx.Exec(`DELETE FROM settings WHERE key IN (SELECT ?||id FROM (`+query+`))`, prefix, id, cutoff); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`DELETE FROM tasks WHERE id IN (`+query+`)`, id, cutoff); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM schedule_runs WHERE schedule_id=? AND created_at<? AND NOT EXISTS(SELECT 1 FROM tasks WHERE tasks.id=task_id)`, id, cutoff); err != nil {
		return err
	}
	return tx.Commit()
}
