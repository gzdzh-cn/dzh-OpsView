package dao

import (
	"database/sql"
	"errors"
	"fmt"
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
	requestID := provisionRequest(request).String("requestId")
	if requestID != "" {
		var enc string
		if er := tx.QueryRow(`SELECT value FROM settings WHERE key=?`, "site-request:"+requestID).Scan(&enc); er == nil {
			var previous model.Config
			if er = s.decode(enc, &previous); er != nil {
				return model.Task{}, er
			}
			var t model.Task
			er = tx.QueryRow(`SELECT id,resource_id,operation,state,error,created_at,updated_at FROM tasks WHERE id=?`, previous.String("taskId")).Scan(&t.ID, &t.ResourceID, &t.Operation, &t.State, &t.Error, &t.CreatedAt, &t.UpdatedAt)
			site.ID = previous.String("siteId")
			return t, er
		} else if !errors.Is(er, sql.ErrNoRows) {
			return model.Task{}, er
		}
	}
	insert := func(r *model.Resource) error {
		if r.ID == "" {
			r.ID = security.ID()
		}
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
	if requestID != "" {
		if _, err = tx.Exec(`INSERT INTO settings(key,value) VALUES(?,?)`, "site-request:"+requestID, s.encode(model.Config{"siteId": site.ID, "taskId": t.ID})); err != nil {
			return t, err
		}
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

// Only identifiers and task state are returned; provision credentials stay encrypted.
func (s *Store) SiteProvisionByRequest(id string) (model.Config, error) {
	var result model.Config
	if err := s.Setting("site-request:"+id, &result); err != nil {
		return nil, err
	}
	site, err := s.Resource(result.String("siteId"))
	if err != nil {
		return nil, fmt.Errorf("创建记录关联站点不可用: %v", err)
	}
	task, err := s.Task(result.String("taskId"))
	if err != nil {
		return nil, fmt.Errorf("创建记录关联任务不可用: %v", err)
	}
	// A resumed attempt updates the pointer without changing the original identity.
	result["state"] = task.State
	result["revision"] = site.Revision
	result["databaseReady"] = false
	if db, er := s.Resource(site.Config.String("databaseId")); er == nil {
		result["databaseReady"] = db.Status == "ready"
	}
	return result, nil
}
func (s *Store) ResumeSiteProvision(site model.Resource, update bool, params model.Config) (model.Task, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return model.Task{}, err
	}
	defer tx.Rollback()
	if update {
		result, er := tx.Exec(`UPDATE resources SET name=?,config=?,status='pending',revision=revision+1,updated_at=? WHERE id=? AND revision=? AND status IN ('pending','error')`, site.Name, s.encode(site.Config), Now(), site.ID, site.Revision)
		if er != nil {
			return model.Task{}, er
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return model.Task{}, ErrRevision
		}
		site.Revision++
	}
	if !update {
		result, er := tx.Exec(`UPDATE resources SET status='pending',revision=revision+1,updated_at=? WHERE id=? AND revision=? AND status IN ('pending','error')`, Now(), site.ID, site.Revision)
		if er != nil {
			return model.Task{}, er
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return model.Task{}, ErrRevision
		}
		site.Revision++
	}
	site.Status = "pending"
	task, err := s.enqueueTx(tx, model.Operation{Name: "site.provision", Resource: site, Params: params})
	if err != nil {
		return task, err
	}
	if id := params.String("requestId"); id != "" {
		if err = s.setTx(tx, "site-request:"+id, model.Config{"siteId": site.ID, "taskId": task.ID}); err != nil {
			return task, err
		}
	}
	return task, tx.Commit()
}
