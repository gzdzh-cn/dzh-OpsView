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
	path, err := c.App.EnsurePanelBackup(r.Context(), name)
	if err != nil {
		fail(w, 502, "备份读取失败")
		return
	}
	http.ServeFile(w, r, path)
}

func (c *Controller) panelBackups(w http.ResponseWriter, r *http.Request) {
	files := []string{}
	for _, dir := range c.App.PanelBackupDirectories() {
		f, _ := filepath.Glob(filepath.Join(dir, "*.ovp"))
		files = append(files, f...)
	}
	items := []model.Config{}
	for i := len(files) - 1; i >= 0; i-- {
		stat, err := os.Stat(files[i])
		if err != nil {
			continue
		}
		name := filepath.Base(files[i])
		items = append(items, model.Config{"name": name, "size": stat.Size(), "storage": filepath.Dir(files[i]), "createdAt": stat.ModTime(), "url": "/api/v1/settings/backup/" + name})
	}
	seen := map[string]bool{}
	for _, m := range items {
		seen[m.String("name")] = true
	}
	var remote map[string]model.Config
	_ = c.App.Store.Setting("panel-s3-history", &remote)
	for name, m := range remote {
		if seen[name] {
			continue
		}
		items = append(items, model.Config{"name": name, "size": m["size"], "storage": "S3（可下载原加密副本）", "createdAt": m["createdAt"], "url": "/api/v1/settings/backup/" + name})
	}
	send(w, 200, items)
}
