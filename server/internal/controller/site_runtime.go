package controller

import (
	"net/http"
	"opsview/internal/logic"
	"opsview/internal/model"
	"opsview/internal/security"
)

func (c *Controller) prepareSiteRuntime(w http.ResponseWriter, req *http.Request, site *model.Resource, old model.Config) bool {
	for _, k := range logic.SitePrivateFields() {
		delete(site.Config, k)
		if old[k] != nil {
			site.Config[k] = old[k]
		}
	}
	if site.Config.String("type") != "managed" {
		return true
	}
	if site.ID == "" {
		site.ID = security.ID()
	}
	if old == nil {
		site.Config["runtimeIsolation"] = true
	} else if old.Bool("runtimeIsolation") && !site.Config.Bool("runtimeIsolation") {
		fail(w, 400, "已启用的运行限制不能通过普通保存关闭")
		return false
	}
	if site.Config.String("user") == "root" {
		digest := logic.SiteRootDigest(*site)
		if old.String("siteRootApproval") != digest {
			if !c.verifyIdentity(req, req.Header.Get("X-OpsView-Password"), req.Header.Get("X-OpsView-TOTP")) {
				fail(w, 403, "root 项目需重新验证管理员密码及已启用的双因素验证码")
				return false
			}
		}
		site.Config["siteRootApproval"] = digest
	} else {
		delete(site.Config, "siteRootApproval")
	}
	return true
}

func (c *Controller) siteRuntimeUsers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	c.exec(w, r, model.Operation{Name: "system.site-runtime-users"})
}

// This endpoint reads only. The fingerprint binds an adoption/remediation to
// the file identities actually inspected, not a browser supplied file list.
func (c *Controller) siteRuntimePreview(w http.ResponseWriter, r *http.Request) {
	var p struct {
		ID       string       `json:"id"`
		Revision int          `json:"revision"`
		Config   model.Config `json:"config"`
	}
	if !decode(w, r, &p) {
		return
	}
	if p.Config == nil {
		fail(w, 400, "配置不能为空")
		return
	}
	site := model.Resource{Kind: "site", Config: p.Config}
	if p.ID != "" {
		stored, err := c.App.Store.Resource(p.ID)
		if err != nil || stored.Kind != "site" {
			fail(w, 404, "站点不存在")
			return
		}
		if p.Revision != stored.Revision {
			fail(w, 409, "站点已变化，请重新载入")
			return
		}
		site.ID = stored.ID
		for _, k := range []string{"documentRoot", "rootDirectory", "siteBaseDirectory"} {
			site.Config[k] = stored.Config[k]
		}
	} else {
		site.Config["siteBaseDirectory"] = c.panelPreferences().String("siteDirectory")
	}
	if err := logic.ValidateSiteCreate(site.Config); err != nil {
		fail(w, 400, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	c.exec(w, r, model.Operation{Name: "system.site-runtime-preview", Resource: site})
}
