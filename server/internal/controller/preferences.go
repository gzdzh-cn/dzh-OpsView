package controller

import (
	"errors"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"opsview/internal/dao"
	"opsview/internal/model"
	"opsview/internal/preferences"
	"opsview/internal/runtimeconfig"
	"opsview/internal/security"
	"regexp"
	"time"
)

func (c *Controller) panelPreferences() model.Config {
	v := model.Config{}
	_ = c.App.Store.Setting("panel", &v)
	return preferences.Merge(v)
}
func (c *Controller) preferenceGroup(w http.ResponseWriter, r *http.Request) {
	group := r.PathValue("group")
	if _, ok := preferences.Groups[group]; !ok {
		fail(w, 404, "设置分组不存在")
		return
	}
	if r.Method == "GET" {
		v := model.Config{}
		rev, err := c.App.Store.SettingRevision("panel", &v)
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		v = preferences.Merge(v)
		out := model.Config{}
		for _, k := range preferences.Groups[group] {
			out[k] = v[k]
		}
		delete(out, "basicPasswordHash")
		delete(out, "clientCA")
		send(w, 200, model.Config{"config": out, "revision": rev})
		return
	}
	c.accessMu.Lock()
	defer c.accessMu.Unlock()
	if c.App.PanelConfigPath != "" {
		status, err := c.App.Executor.Execute(r.Context(), model.Operation{Name: "system.panel-status"}, func(string) {})
		if err != nil {
			fail(w, 503, "无法检查访问设置事务")
			return
		}
		if status.Bool("pending") {
			fail(w, 409, "请先确认运行地址修改或等待回退")
			return
		}
	}
	var b struct {
		Patch         model.Config `json:"patch"`
		Revision      int          `json:"revision"`
		Password      string       `json:"password"`
		Code          string       `json:"code"`
		BasicPassword string       `json:"basicPassword"`
	}
	if !decode(w, r, &b) {
		return
	}
	if group == "security" || b.Patch["webDisabled"] != nil {
		if !c.verifyIdentity(r, b.Password, b.Code) {
			fail(w, 403, "请重新验证管理员身份")
			return
		}
	}
	if b.BasicPassword != "" {
		if len(b.BasicPassword) < 12 || len(b.BasicPassword) > 72 {
			fail(w, 400, "BasicAuth 密码须为 12–72 字节")
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(b.BasicPassword), bcrypt.DefaultCost)
		if err != nil {
			fail(w, 500, "密码哈希失败")
			return
		}
		b.Patch["basicPasswordHash"] = string(hash)
	} else {
		delete(b.Patch, "basicPasswordHash")
	}
	delete(b.Patch, "clientCA")
	if err := preferences.Validate(group, b.Patch); err != nil {
		fail(w, 400, err.Error())
		return
	}
	current := c.panelPreferences()
	if b.Patch["passwordExpiryDays"] != nil && b.Patch.Int("passwordExpiryDays") > 0 && current.Int("passwordExpiryDays") == 0 {
		b.Patch["passwordPolicySince"] = time.Now().Unix()
	}
	for k, v := range b.Patch {
		current[k] = v
	}
	if current.Bool("basicEnabled") && (current.String("basicPasswordHash") == "" || !regexp.MustCompile(`^[a-zA-Z0-9_-]{2,40}$`).MatchString(current.String("basicUsername"))) {
		fail(w, 400, "请先填写 BasicAuth 用户名及密码")
		return
	}
	if current.Bool("clientCertificateEnabled") && current.String("clientCA") == "" {
		fail(w, 400, "请先生成客户端证书")
		return
	}
	if pattern := current.String("userAgentAllow"); pattern != "" {
		re, _ := regexp.Compile(pattern)
		if !re.MatchString(r.UserAgent()) {
			fail(w, 400, "User-Agent 规则须包含当前浏览器")
			return
		}
	}
	if b.Patch["loginCountries"] != nil || b.Patch["unknownCountry"] != nil {
		if err := c.checkRegion(r, current); err != nil {
			fail(w, 400, err.Error())
			return
		}
	}
	for _, k := range []string{"siteDirectory", "backupDirectory"} {
		if b.Patch[k] != nil {
			_, err := c.App.Executor.Execute(r.Context(), model.Operation{Name: "schedule.validate-directory", Params: model.Config{"path": b.Patch[k], "panelStorage": k == "backupDirectory"}}, func(string) {})
			if err != nil {
				fail(w, 400, err.Error())
				return
			}
		}
	}
	if b.Patch["backupStorageId"] != nil {
		if _, err := c.App.BackupStorage(r.Context(), b.Patch.String("backupStorageId")); err != nil {
			fail(w, 400, err.Error())
			return
		}
	}
	// Access-affecting preferences are staged with the same 120-second confirmation used by runtime changes.
	dangerous := group == "security" && (b.Patch["basicEnabled"] != nil || b.Patch["clientCertificateEnabled"] != nil || b.Patch["userAgentAllow"] != nil || b.Patch["loginCountries"] != nil || b.Patch["unauthenticatedStatus"] != nil || b.Patch["basicUsername"] != nil || b.BasicPassword != "" || b.Patch["unknownCountry"] != nil)
	var rollback model.Config
	if dangerous {
		rollback = model.Config{}
		for k := range b.Patch {
			rollback[k] = c.panelPreferences()[k]
		}
	}

	if current.Bool("clientCertificateEnabled") {
		cfg, err := runtimeconfig.Load(c.App.PanelConfigPath)
		if err != nil || cfg.Protocol != "https" {
			fail(w, 400, "客户端证书认证需要 HTTPS")
			return
		}
	}
	var value model.Config
	var rev int
	var err error
	token := ""
	if dangerous {
		token = security.ID()
		value, rev, err = c.App.Store.StagePreferences(b.Revision, b.Patch, rollback, security.Hash(token), clientIP(r))
	} else {
		value, rev, err = c.App.Store.PatchSetting("panel", b.Revision, b.Patch, "preferences."+group, clientIP(r))
	}
	if errors.Is(err, dao.ErrRevision) {
		fail(w, 409, err.Error())
		return
	}
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	result := model.Config{"config": preferencePublic(value), "revision": rev}
	if dangerous {
		result["confirmToken"] = token
		result["confirmSeconds"] = 120
	}
	c.App.ReloadSchedules()
	send(w, 200, result)
}
func (c *Controller) publicPreferences(w http.ResponseWriter, r *http.Request) {
	v := c.panelPreferences()
	send(w, 200, model.Config{"name": v["name"], "theme": v["theme"], "primaryColor": v["primaryColor"], "fontSize": v["fontSize"], "loginLogo": v["loginLogo"], "loginBackground": v["loginBackground"], "favicon": v["favicon"], "passkeyAvailable": func() bool {
		u := passkeyUser{}
		_ = c.App.Store.Setting("passkeys", &u)
		_, _, err := c.webAuthn(r)
		return err == nil && len(u.Credentials) > 0
	}()})
}
func (c *Controller) confirmPreferences(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &b) {
		return
	}
	if err := c.App.Store.ConfirmPreferences(security.Hash(b.Token)); err != nil {
		fail(w, 409, err.Error())
		return
	}
	send(w, 200, model.Config{"ok": true})
}
func preferencePublic(v model.Config) model.Config {
	out := security.Redact(v)
	delete(out, "basicPasswordHash")
	delete(out, "clientCA")
	return out
}
