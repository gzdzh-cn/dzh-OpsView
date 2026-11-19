package dao

import (
	"errors"
	"opsview/internal/model"
	"opsview/internal/security"
)

func (s *Store) SaveScript(r *model.Resource) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := Now()
	if r.ID == "" {
		r.ID = security.ID()
		r.Revision = 1
		r.CreatedAt = now
		if _, err = tx.Exec(`INSERT INTO resources(id,kind,name,status,config,revision,created_at,updated_at) VALUES(?,'script',?,'ready',?,1,?,?)`, r.ID, r.Name, s.encode(r.Config), now, now); err != nil {
			return err
		}
	} else {
		result, er := tx.Exec(`UPDATE resources SET name=?,config=?,revision=revision+1,updated_at=? WHERE id=? AND kind='script' AND revision=?`, r.Name, s.encode(r.Config), now, r.ID, r.Revision)
		if er != nil {
			return er
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return errors.New("脚本已修改，请重新载入")
		}
		r.Revision++
	}
	r.UpdatedAt = now
	if _, err = tx.Exec(`INSERT INTO script_revisions(script_id,revision,name,config,created_at) VALUES(?,?,?,?,?)`, r.ID, r.Revision, r.Name, s.encode(r.Config), now); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) ScriptRevisions(id string) ([]model.Config, error) {
	rows, err := s.DB.Query(`SELECT revision,name,created_at FROM script_revisions WHERE script_id=? ORDER BY revision DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Config{}
	for rows.Next() {
		var rev int
		var name, created string
		if err = rows.Scan(&rev, &name, &created); err != nil {
			return nil, err
		}
		out = append(out, model.Config{"revision": rev, "name": name, "createdAt": created})
	}
	return out, rows.Err()
}
func (s *Store) ScriptRevision(id string, revision int) (model.Config, error) {
	var enc string
	err := s.DB.QueryRow(`SELECT config FROM script_revisions WHERE script_id=? AND revision=?`, id, revision).Scan(&enc)
	out := model.Config{}
	if err == nil {
		err = s.decode(enc, &out)
	}
	return out, err
}
