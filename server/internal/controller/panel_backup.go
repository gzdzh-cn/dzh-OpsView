package controller

import (
	"net/http"
	"opsview/internal/model"
	"os"
	"path/filepath"
	"strings"
)

func (c *Controller) backupPanel(w http.ResponseWriter, r *http.Request) {
	task, err := c.App.Store.Enqueue(model.Operation{Name: "panel.backup", Resource: model.Resource{ID: "panel-metadata", Kind: "system", Name: "panel"}, Params: model.Config{}})
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	c.App.Store.Audit("panel.backup", "", clientIP(r))
	send(w, 202, task)
}
func (c *Controller) downloadPanelBackup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	if filepath.Base(name) != name || !strings.HasSuffix(name, ".ovp") {
		fail(w, 400, "备份文件名无效")
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	c.App.Store.Audit("panel.backup.download", "", clientIP(r))
	http.ServeFile(w, r, filepath.Join(c.DataDir, "backups", name))
}

func (c *Controller) panelBackups(w http.ResponseWriter, r *http.Request) {
	files, _ := filepath.Glob(filepath.Join(c.DataDir, "backups", "*.ovp"))
	items := []model.Config{}
	for i := len(files) - 1; i >= 0; i-- {
		stat, err := os.Stat(files[i])
		if err != nil {
			continue
		}
		name := filepath.Base(files[i])
		items = append(items, model.Config{"name": name, "size": stat.Size(), "createdAt": stat.ModTime(), "url": "/api/v1/settings/backup/" + name})
	}
	send(w, 200, items)
}
