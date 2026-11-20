package controller

import (
	"net/http"
	"opsview/internal/model"
	"opsview/internal/security"
	"strings"
	"time"
)

func siteDeleteSession(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookieName(r))
	if err != nil {
		return ""
	}
	return security.Hash(cookie.Value)
}
func (c *Controller) siteDeletePreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var p struct {
		Items []model.SiteDeleteItem `json:"items"`
	}
	if !decode(w, r, &p) {
		return
	}
	plan, err := c.App.SiteDeletePlan(r.Context(), p.Items)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	id := security.ID()
	preview := model.SiteDeletePreview{Items: p.Items, Fingerprint: plan.String("fingerprint"), Session: siteDeleteSession(r), CreatedAt: time.Now().Unix()}
	if err = c.App.Store.Set("site-delete-preview:"+id, preview); err != nil {
		fail(w, 500, "保存删除预览失败")
		return
	}
	send(w, 200, model.Config{"previewId": id, "items": plan["items"], "confirmAfterSeconds": 5, "expiresAt": preview.CreatedAt + 300})
}
func (c *Controller) submitSiteDelete(w http.ResponseWriter, r *http.Request) {
	var p struct {
		PreviewID            string `json:"previewId"`
		Confirmed            bool   `json:"confirmed"`
		MaintenanceConfirmed bool   `json:"maintenanceConfirmed"`
	}
	if !decode(w, r, &p) {
		return
	}
	c.applySiteDelete(w, r, p.PreviewID, p.Confirmed, p.MaintenanceConfirmed, "")
}
func (c *Controller) applySiteDelete(w http.ResponseWriter, r *http.Request, id string, confirmed bool, maintenance bool, legacyID string) {
	var preview model.SiteDeletePreview
	if !confirmed || c.App.Store.Setting("site-delete-preview:"+id, &preview) != nil || preview.Session == "" || preview.Session != siteDeleteSession(r) {
		fail(w, 409, "请先预览并确认删除影响")
		return
	}
	for _, item := range preview.Items {
		if item.DeleteDatabase && !maintenance {
			fail(w, 409, "请确认已停止相关业务写入")
			return
		}
	}
	age := time.Now().Unix() - preview.CreatedAt
	if age < 5 || age > 300 {
		fail(w, 409, "删除确认尚未到时或已过期，请重新预览")
		return
	}
	if legacyID != "" && (len(preview.Items) != 1 || preview.Items[0].ID != legacyID) {
		fail(w, 409, "确认范围与站点不一致")
		return
	}
	task, err := c.App.QueueSiteDeletion(r.Context(), preview.Items, preview.Fingerprint)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	for _, item := range preview.Items {
		c.App.Store.Audit("site.delete", item.ID, clientIP(r))
	}
	send(w, 202, model.Config{"taskId": task.ID})
}
func (c *Controller) siteDeleteResult(w http.ResponseWriter, r *http.Request) {
	task, err := c.App.Store.Task(r.PathValue("id"))
	if err != nil || task.Operation != "sites.delete" {
		fail(w, 404, "网站删除任务不存在")
		return
	}
	result, _ := c.App.Store.TaskResult(task.ID)
	w.Header().Set("Cache-Control", "no-store")
	send(w, 200, model.Config{"state": task.State, "error": task.Error, "result": result})
}

// Old clients must use the same preview; they cannot submit unchecked paths.
func (c *Controller) legacySiteDelete(w http.ResponseWriter, r *http.Request, id string, p model.Config) {
	c.applySiteDelete(w, r, strings.TrimSpace(p.String("previewId")), p.Bool("confirmed"), p.Bool("maintenanceConfirmed"), id)
}
