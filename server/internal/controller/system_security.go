package controller

import (
	"net/http"
	"opsview/internal/model"
)

func (c *Controller) systemSecurity(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == "GET" {
		name := map[string]string{"/api/v1/system/ssh": "ssh-status", "/api/v1/system/ssh/logs": "ssh-logs", "/api/v1/system/firewall": "firewall-status"}[r.URL.Path]
		c.exec(w, r, model.Operation{Name: "system." + name})
		return
	}
	action := r.PathValue("action")
	allowed := map[string]bool{"ssh-apply": true, "ssh-confirm": true, "ssh-rollback": true, "ssh-key-add": true, "ssh-key-delete": true, "firewall-mode": true, "firewall-confirm": true, "firewall-rollback": true, "firewall-reload": true}
	if !allowed[action] {
		fail(w, 400, "不支持的安全操作")
		return
	}
	var p model.Config
	if !decode(w, r, &p) {
		return
	}
	if action == "ssh-confirm" || action == "ssh-rollback" || action == "firewall-confirm" || action == "firewall-rollback" {
		data, err := c.App.Executor.Execute(r.Context(), model.Operation{Name: "system." + action, Params: p}, func(string) {})
		if err != nil {
			fail(w, 409, err.Error())
			return
		}
		c.App.Store.Audit("system."+action, "", clientIP(r))
		send(w, 200, data)
		return
	}
	// All host-security changes share a persistent queue key. The executor also
	// serializes them, including changes initiated by database/firewall resources.
	t, err := c.App.Store.Enqueue(model.Operation{Name: "system." + action, Resource: model.Resource{ID: "system-security", Kind: "system", Name: "系统与安全"}, Params: p})
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	c.App.Store.Audit("system."+action, "", clientIP(r))
	send(w, 202, t)
}
