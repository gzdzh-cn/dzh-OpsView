package controller

import (
	"encoding/json"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"opsview/internal/account"
	"opsview/internal/model"
	"opsview/internal/scheduling"
	"opsview/internal/security"
	"strconv"
	"time"
)

func (c *Controller) verifyIdentity(r *http.Request, password, code string) bool {
	ip := "reauth:" + clientIP(r)
	c.auth.Lock()
	defer c.auth.Unlock()
	now := time.Now()
	at := c.auth.attempts[ip]
	if at.Count >= 5 && now.Before(at.Until) {
		return false
	}
	var a account.Admin
	ok := c.App.Store.Setting("admin", &a) == nil && !c.passwordExpired(a) && bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(password)) == nil && (a.TOTP == "" || security.VerifyTOTP(a.TOTP, code))
	if ok {
		delete(c.auth.attempts, ip)
	} else {
		if len(c.auth.attempts) > 4096 {
			return false
		}
		if now.After(at.Until) {
			at.Count = 0
		}
		at.Count++
		at.Until = now.Add(15 * time.Minute)
		c.auth.attempts[ip] = at
		c.App.Store.Audit("auth.failed", "", clientIP(r))
	}
	return ok
}
func publicResourceConfig(r model.Resource) model.Config {
	if r.Kind == "site" {
		out := security.Redact(r.Config)
		delete(out, "siteRootApproval")
		delete(out, "permissionPreview")
		delete(out, "runtimeTransaction")
		return out
	}
	if r.Kind == "notification-channel" {
		return model.Config{"type": r.Config["type"], "enabled": r.Config["enabled"]}
	}
	if r.Kind == "schedule" || r.Kind == "script" {
		return scheduling.Public(r.Config)
	}
	return security.Redact(r.Config)
}
func (c *Controller) prepareSchedule(w http.ResponseWriter, r *http.Request, res *model.Resource, old model.Config) bool {
	cfg := res.Config
	if cfg == nil {
		fail(w, 400, "计划配置不能为空")
		return false
	}
	if cfg.String("timezone") == "" && old == nil {
		cfg["timezone"] = c.panelPreferences().String("timezone")
	}
	if cfg.String("operation") == "directory" && cfg.String("storageDirectory") == "" {
		cfg["storageDirectory"] = c.panelPreferences().String("backupDirectory")
	}
	if op := cfg.String("operation"); op == "backup" || op == "renew" || op == "rotate" || op == "site-state" {
		ids := []string{cfg.String("targetId")}
		if op == "backup" {
			ids = scheduling.TargetIDs(cfg)
		}
		if len(ids) == 0 {
			fail(w, 400, "请选择备份目标")
			return false
		}
		for _, id := range ids {
			if op == "renew" && id == "panel" {
				continue
			}
			target, err := c.App.Store.Resource(id)
			expected := "site"
			if op == "backup" {
				expected = "backup"
			}
			if err != nil || target.Kind != expected {
				fail(w, 400, "计划类型与目标不匹配或目标不存在")
				return false
			}
		}
	}
	if cfg.String("operation") == "directory" && cfg.String("storageId") != "" {
		if _, err := c.App.BackupStorage(r.Context(), cfg.String("storageId")); err != nil {
			fail(w, 400, err.Error())
			return false
		}
	}
	// Generic resource writes cannot manufacture an approval or overwrite secrets omitted from list responses.
	delete(cfg, "rootApproval")
	for _, k := range []string{"script", "environment", "headers", "body"} {
		if _, ok := cfg[k]; !ok && old[k] != nil {
			cfg[k] = old[k]
		}
	}
	security.PreserveMasked(cfg, old)
	if (cfg.String("operation") == "shell" || cfg.String("operation") == "python") && cfg.String("user") == "root" && r.URL.Path == "/api/v1/schedules/import" && !cfg.Bool("enabled") {
		return true
	}
	if (cfg.String("operation") == "shell" || cfg.String("operation") == "python") && cfg.String("user") == "root" {
		digest := scheduling.Digest(cfg)
		if old.String("rootApproval") == digest && (!cfg.Bool("enabled") || old.Bool("enabled")) {
			cfg["rootApproval"] = digest
		} else {
			if !c.verifyIdentity(r, r.Header.Get("X-OpsView-Password"), r.Header.Get("X-OpsView-TOTP")) {
				fail(w, 403, "root 脚本需重新验证管理员密码及双因素验证码")
				return false
			}
			cfg["rootApproval"] = digest
		}
	}
	return true
}
func (c *Controller) scheduleAdmin(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	view := r.PathValue("view")
	expected := http.MethodGet
	if id == "preview" || view == "stop" || view == "clear-history" {
		expected = http.MethodPost
	}
	if r.Method != expected {
		fail(w, http.StatusMethodNotAllowed, "请求方法不支持")
		return
	}
	if view != "" && view != "runs" && view != "export" && view != "stop" && view != "clear-history" {
		fail(w, 404, "计划操作不存在")
		return
	}
	if id == "capabilities" {
		c.exec(w, r, model.Operation{Name: "schedule.capabilities"})
		return
	}
	if id == "preview" {
		var cfg model.Config
		if !decode(w, r, &cfg) {
			return
		}
		parsed, err := scheduling.Cron(cfg)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		out := []string{}
		now := time.Now()
		for i := 0; i < 5; i++ {
			now = parsed.Next(now)
			out = append(out, now.Format(time.RFC3339))
		}
		send(w, 200, model.Config{"next": out})
		return
	}
	row, err := c.App.Store.Resource(id)
	if err != nil || row.Kind != "schedule" {
		fail(w, 404, "计划不存在")
		return
	}
	if view == "clear-history" {
		var b struct {
			Before string `json:"before"`
		}
		if !decode(w, r, &b) {
			return
		}
		cutoff, err := time.Parse(time.RFC3339, b.Before)
		if err != nil || cutoff.After(time.Now()) {
			fail(w, 400, "清理时间无效")
			return
		}
		if err = c.App.Store.PruneScheduleHistory(id, cutoff.UTC().Format(time.RFC3339)); err != nil {
			fail(w, 500, err.Error())
			return
		}
		c.App.Store.Audit("schedule.history.clear", id, clientIP(r))
		send(w, 200, model.Config{"ok": true})
		return
	}
	if view == "runs" {
		runs, err := c.App.Store.ScheduleRuns(id)
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		for _, run := range runs {
			var truncated bool
			_ = c.App.Store.Setting("task-log-truncated:"+run.String("id"), &truncated)
			run["truncated"] = truncated
		}
		send(w, 200, runs)
		return
	}
	if view == "stop" {
		runs, _ := c.App.Store.ScheduleRuns(id)
		for _, run := range runs {
			if run.String("state") == "running" || run.String("state") == "queued" {
				if err = c.App.CancelInstall(run.String("id")); err != nil {
					fail(w, 409, err.Error())
					return
				}
			}
		}
		send(w, 200, model.Config{"ok": true})
		return
	}
	if view == "export" {
		delete(row.Config, "environment")
		delete(row.Config, "headers")
		delete(row.Config, "body")
		delete(row.Config, "rootApproval")
		row.Config["enabled"] = false
		send(w, 200, model.Config{"name": row.Name, "config": row.Config})
		return
	}
	delete(row.Config, "rootApproval")
	row.Config = security.Redact(row.Config)
	send(w, 200, row)
}
func (c *Controller) scriptLibrary(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if r.Method == "GET" {
		if id != "" {
			v, err := c.App.Store.Resource(id)
			if err != nil || v.Kind != "script" {
				fail(w, 404, "脚本不存在")
				return
			}
			if r.URL.Query().Get("history") == "1" {
				history, err := c.App.Store.ScriptRevisions(id)
				if err != nil {
					fail(w, 500, err.Error())
					return
				}
				send(w, 200, history)
				return
			}
			if n, _ := strconv.Atoi(r.URL.Query().Get("revision")); n > 0 {
				cfg, err := c.App.Store.ScriptRevision(id, n)
				if err != nil {
					fail(w, 404, "脚本版本不存在")
					return
				}
				v.Config = cfg
			}
			send(w, 200, v)
			return
		}
		v, _ := c.App.Store.Resources("script")
		for i := range v {
			v[i].Config = scheduling.Public(v[i].Config)
		}
		send(w, 200, v)
		return
	}
	if r.Method == "DELETE" {
		v, err := c.App.Store.Resource(id)
		if err != nil || v.Kind != "script" {
			fail(w, 404, "脚本不存在")
			return
		}
		err = c.App.Store.Delete(id)
		if err != nil {
			fail(w, 409, err.Error())
			return
		}
		c.App.Store.Audit("script.delete", id, clientIP(r))
		send(w, 200, model.Config{"ok": true})
		return
	}
	var b struct {
		Name     string       `json:"name"`
		Config   model.Config `json:"config"`
		Revision int          `json:"revision"`
	}
	if !decode(w, r, &b) {
		return
	}
	var err error
	row := model.Resource{Kind: "script", Name: b.Name, Config: b.Config, Status: "ready"}
	if id != "" {
		old, err := c.App.Store.Resource(id)
		if err != nil || old.Kind != "script" {
			fail(w, 404, "脚本不存在")
			return
		}
		if old.Revision != b.Revision {
			fail(w, 409, "脚本已修改")
			return
		}
		row.ID = id
		row.Revision = b.Revision
	}
	if row.Config.String("operation") != "shell" && row.Config.String("operation") != "python" {
		fail(w, 400, "脚本类型无效")
		return
	}
	row.Config["cron"] = "0 0 * * *"
	row.Config["enabled"] = false
	row.Config["user"] = "opsview-task"
	row.Config["workingDirectory"] = "/tmp"
	row.Config["timeoutSeconds"] = 3600
	if err = scheduling.Validate(row.Config); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if err = logicValidateScript(row); err != nil {
		fail(w, 400, err.Error())
		return
	}
	err = c.App.Store.SaveScript(&row)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	c.App.Store.Audit("script.save", row.ID, clientIP(r))
	send(w, 200, row)
}
func logicValidateScript(r model.Resource) error { _, err := json.Marshal(r.Config); return err }
