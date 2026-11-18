package controller

import (
	"net/http"
	"opsview/internal/model"
)

func (c *Controller) firewallAdmin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	action := r.PathValue("action")
	reads := map[string]bool{"export": true, "import-preview": true, "sync-preview": true, "protection-status": true}
	writes := map[string]bool{"dependencies": true, "save": true, "delete": true, "import-apply": true, "sync-observe": true, "sync-restore": true, "ping": true, "region-update": true, "protection-save": true, "protection-install": true, "protection-unban": true}
	immediate := action == "confirm" || action == "rollback" || action == "rollback-feature"
	if !reads[action] && !writes[action] && !immediate {
		fail(w, 400, "不支持的防火墙操作")
		return
	}
	var p model.Config
	if !decode(w, r, &p) {
		return
	}
	if p == nil {
		p = model.Config{}
	}
	if action == "protection-save" {
		p["_managementIP"] = clientIP(r)
	}
	op := model.Operation{Name: "system.firewall-admin-" + action, Params: p, Resource: model.Resource{ID: "system-security", Kind: "system", Name: "系统防火墙"}}
	if reads[action] || immediate {
		result, err := c.App.Executor.Execute(r.Context(), op, func(string) {})
		if err != nil {
			fail(w, 409, err.Error())
			return
		}
		if immediate {
			c.App.Store.Audit(op.Name, "", clientIP(r))
		}
		send(w, 200, result)
		return
	}
	// Validate against a fresh snapshot before queueing. The executor repeats this at
	// execution time, since an external administrator can change rules while queued.
	if action == "save" || action == "delete" || action == "import-apply" || action == "sync-observe" || action == "sync-restore" {
		status, err := c.App.Executor.Execute(r.Context(), model.Operation{Name: "system.firewall-admin-status"}, func(string) {})
		if err != nil {
			fail(w, 409, err.Error())
			return
		}
		if p.String("revision") == "" || p.String("revision") != status.String("revision") {
			send(w, 409, model.Config{"error": "系统规则已变化，请重新载入后提交", "revision": status.String("revision")})
			return
		}
	}
	t, err := c.App.Store.Enqueue(op)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	c.App.Store.Audit(op.Name, p.String("id"), clientIP(r))
	send(w, 202, t)
}
