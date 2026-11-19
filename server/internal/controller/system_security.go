package controller

import (
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"opsview/internal/model"
	"opsview/internal/security"
	"time"
)

func (c *Controller) systemSecurity(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == "GET" {
		name := map[string]string{"/api/v1/system/ssh/users": "ssh-users", "/api/v1/system/ssh": "ssh-status", "/api/v1/system/ssh/logs": "ssh-logs", "/api/v1/system/firewall": "firewall-admin-status"}[r.URL.Path]
		if name == "firewall-admin-status" {
			data, err := c.App.Executor.Execute(r.Context(), model.Operation{Name: "system." + name}, func(string) {})
			if err != nil {
				fail(w, 502, err.Error())
				return
			}
			if err = c.App.Store.ObserveFirewall(data); err != nil {
				fail(w, 500, "保存防火墙观察记录失败")
				return
			}
			data["managementSource"] = clientIP(r)
			send(w, 200, data)
			return
		}
		c.exec(w, r, model.Operation{Name: "system." + name, Params: model.Config{"period": r.URL.Query().Get("period")}})
		return
	}
	action := r.PathValue("action")
	allowed := map[string]bool{"ssh-user": true, "ssh-service": true, "ssh-service-confirm": true, "ssh-apply": true, "ssh-confirm": true, "ssh-rollback": true, "ssh-key-add": true, "ssh-key-delete": true, "firewall-mode": true, "firewall-confirm": true, "firewall-rollback": true, "firewall-reload": true}
	if !allowed[action] {
		fail(w, 400, "不支持的安全操作")
		return
	}
	var p model.Config
	if !decode(w, r, &p) {
		return
	}
	if action == "ssh-user" {
		if !c.verifySSHAdmin(w, r, p) {
			return
		}
		delete(p, "currentPassword")
		delete(p, "code")
		data, err := c.App.Executor.Execute(r.Context(), model.Operation{Name: "system.ssh-user", Params: p}, func(string) {})
		if err != nil {
			fail(w, 409, err.Error())
			return
		}
		c.App.Store.Audit("system.ssh-user."+p.String("operation"), p.String("username"), clientIP(r))
		send(w, 200, data)
		return
	}
	if action == "ssh-service-confirm" || action == "ssh-confirm" || action == "ssh-rollback" || action == "firewall-confirm" || action == "firewall-rollback" {
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

// Fresh administrator verification is required for each host-account change.
// Secrets are never placed in task payloads, audit records, args or environment.
func (c *Controller) verifySSHAdmin(w http.ResponseWriter, r *http.Request, p model.Config) bool {
	c.auth.Lock()
	defer c.auth.Unlock()
	ip := clientIP(r)
	now := time.Now()
	at := c.auth.attempts[ip]
	if at.Count >= 5 && now.Before(at.Until) {
		fail(w, 429, "验证尝试过多，请 15 分钟后重试")
		return false
	}
	if now.After(at.Until) {
		at.Count = 0
	}
	admin, err := c.currentAdmin()
	valid := err == nil && bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(p.String("currentPassword"))) == nil
	if valid && admin.TOTP != "" {
		valid = security.VerifyTOTP(admin.TOTP, p.String("code"))
	}
	if !valid {
		at.Count++
		at.Until = now.Add(15 * time.Minute)
		c.auth.attempts[ip] = at
		fail(w, 403, "请验证当前面板管理员密码及双因素验证码")
		return false
	}
	delete(c.auth.attempts, ip)
	return true
}
