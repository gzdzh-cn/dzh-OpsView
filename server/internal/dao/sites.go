package dao

import (
	"opsview/internal/model"
	"opsview/internal/security"
)

func (s *Store) migrateV6() error {
	_, err := s.DB.Exec(`CREATE TABLE IF NOT EXISTS site_provisions(site_id TEXT PRIMARY KEY REFERENCES resources(id) ON DELETE CASCADE,database_id TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,request TEXT NOT NULL); PRAGMA user_version=6;`)
	return err
}

// Reserve related resources and their task together, so interrupted HTTP requests cannot leave an unqueued partial creation.
func (s *Store) CreateSiteProvision(site *model.Resource, db *model.Resource, request ...model.Config) (model.Task, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return model.Task{}, err
	}
	defer tx.Rollback()
	insert := func(r *model.Resource) error {
		r.ID = security.ID()
		r.Revision = 1
		r.Status = "pending"
		r.CreatedAt = Now()
		r.UpdatedAt = r.CreatedAt
		_, er := tx.Exec(`INSERT INTO resources VALUES(?,?,?,?,?,?,?,?)`, r.ID, r.Kind, r.Name, r.Status, s.encode(r.Config), r.Revision, r.CreatedAt, r.UpdatedAt)
		return er
	}
	if db != nil {
		if err = insert(db); err != nil {
			return model.Task{}, err
		}
		if _, err = tx.Exec(`INSERT INTO database_bindings VALUES(?,?,?,?,?)`, db.ID, db.Config.String("instanceId"), db.Name, db.Config.String("username"), false); err != nil {
			return model.Task{}, err
		}
		site.Config["databaseId"] = db.ID
	}
	if err = insert(site); err != nil {
		return model.Task{}, err
	}
	if _, err = tx.Exec(`INSERT INTO site_provisions VALUES(?,?,?,?)`, site.ID, site.Config.String("databaseId"), Now(), s.encode(provisionRequest(request))); err != nil {
		return model.Task{}, err
	}
	t, err := s.enqueueTx(tx, model.Operation{Name: "site.provision", Resource: *site, Params: provisionRequest(request)})
	if err != nil {
		return t, err
	}
	return t, tx.Commit()
}

func provisionRequest(values []model.Config) model.Config {
	if len(values) > 0 {
		return values[0]
	}
	return model.Config{}
}
func (s *Store) SiteProvisionRequest(id string) (model.Config, error) {
	var enc string
	err := s.DB.QueryRow(`SELECT request FROM site_provisions WHERE site_id=?`, id).Scan(&enc)
	v := model.Config{}
	if err == nil {
		err = s.decode(enc, &v)
	}
	return v, err
}
