package dao

import (
	"fmt"
	"opsview/internal/model"
	"strconv"
	"strings"
)

// Every retained instance occupies its exact software version, including failed
// installations and uninstalled instances whose business data is retained.
func (s *Store) migrateV2() error {
	var schemaVersion int
	if err := s.DB.QueryRow("PRAGMA user_version").Scan(&schemaVersion); err != nil {
		return err
	}
	if _, err := s.DB.Exec(`CREATE TABLE IF NOT EXISTS software_versions (software TEXT NOT NULL, version TEXT NOT NULL, resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE, PRIMARY KEY(software,version));`); err != nil {
		return err
	}
	resources, err := s.Resources("")
	if err != nil {
		return err
	}
	apps := map[string]model.Resource{}
	for _, r := range resources {
		if r.Kind == "app" {
			apps[r.ID] = r
		}
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range resources {
		if r.Kind == "instance" && r.Config.String("software") != "" && r.Config.String("version") != "" {
			if _, err = tx.Exec(`INSERT OR IGNORE INTO software_versions VALUES(?,?,?)`, r.Config.String("software"), r.Config.String("version"), r.ID); err != nil {
				return err
			}
		}
		if r.Kind == "site" && r.Config.String("type") == "app" && !r.Config.Bool("externalApplication") {
			if r.Config.String("upstream") == "" {
				app, ok := apps[r.Config.String("appId")]
				if !ok || app.Config.Int("port") < 1 {
					return fmt.Errorf("网站 %s 缺少旧应用端口，请修复元数据后升级", r.Name)
				}
				r.Config["upstream"] = "http://127.0.0.1:" + strconv.Itoa(app.Config.Int("port"))
			}
			r.Config["legacyAppId"] = r.Config.String("appId")
			delete(r.Config, "appId")
			r.Config["externalApplication"] = true
			if _, err = tx.Exec(`UPDATE resources SET config=?,revision=revision+1,updated_at=? WHERE id=?`, s.encode(r.Config), Now(), r.ID); err != nil {
				return err
			}
		}
	}
	if _, err = tx.Exec(`INSERT INTO audit(action,resource_id,ip,created_at) SELECT 'app.retired:queued-task-cancelled',resource_id,'local-migration',? FROM tasks WHERE operation LIKE 'app.%' AND state='queued'`, Now()); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE tasks SET state='cancelled',error='应用进程功能已下线，旧服务与数据保留',updated_at=? WHERE operation LIKE 'app.%' AND state='queued'`, Now()); err != nil {
		return err
	}
	if schemaVersion < 2 {
		if _, err = tx.Exec(`PRAGMA user_version=2`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func instanceVersion(r *model.Resource) (string, string) {
	if r.Kind != "instance" {
		return "", ""
	}
	return strings.ToLower(r.Config.String("software")), r.Config.String("version")
}
