package dao

import (
	"opsview/internal/model"
	"opsview/internal/security"
)

func (s *Store) CreateSchedules(rows []model.Resource) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range rows {
		r.ID = security.ID()
		r.Config["enabled"] = false
		delete(r.Config, "rootApproval")
		_, err = tx.Exec(`INSERT INTO resources(id,kind,name,status,config,revision,created_at,updated_at) VALUES(?,'schedule',?,'ready',?,1,?,?)`, r.ID, r.Name, s.encode(r.Config), Now(), Now())
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
