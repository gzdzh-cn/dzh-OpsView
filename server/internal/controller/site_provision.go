package controller

import (
	"database/sql"
	"errors"
	"net/http"
	"opsview/internal/logic"
	"opsview/internal/model"
	"opsview/internal/security"
	"path/filepath"
)

func (c *Controller) siteCategories(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var p struct {
			Old    string `json:"old"`
			Name   string `json:"name"`
			Remove bool   `json:"remove"`
		}
		if !decode(w, r, &p) {
			return
		}
		if err := c.App.Store.ChangeSiteCategory(p.Old, p.Name, p.Remove); err != nil {
			fail(w, 409, err.Error())
			return
		}
		c.App.Store.Audit("site.category", "", clientIP(r))
	}
	rows, err := c.App.Store.SiteCategories()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	send(w, 200, model.Config{"rows": rows})
}
func (c *Controller) bindSite(r *model.Resource) error {
	instances, err := c.App.Store.Resources("instance")
	if err != nil {
		return err
	}
	candidates := []model.Resource{}
	for _, i := range instances {
		if i.Config.String("software") == "nginx" && i.Status != "uninstalled" {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) > 1 && r.ID != "" {
		if stored, er := c.App.Store.Resource(r.ID); er == nil && stored.Kind == "site" {
			for _, i := range candidates {
				if i.ID == stored.Config.String("instanceId") {
					candidates = []model.Resource{i}
					break
				}
			}
		}
	}
	if len(candidates) != 1 {
		return errors.New("需要唯一受管 Nginx；请先安装，或合并清理历史多实例")
	}
	i := candidates[0]
	if i.Config.Bool("installationIncomplete") || (i.Status != "ready" && i.Status != "running" && i.Status != "stopped") {
		return errors.New("Nginx 尚未完成安装，请检查实例")
	}
	if _, err := logic.SiteBindings(r.Config, i.Config.Int("port")); err != nil {
		return err
	}
	r.Config["instanceId"] = i.ID
	if r.Config.String("category") == "" {
		r.Config["category"] = "默认分类"
	}
	categories, err := c.App.Store.SiteCategories()
	if err != nil {
		return err
	}
	for _, name := range categories {
		if name == r.Config.String("category") {
			return nil
		}
	}
	return errors.New("网站分类不存在，请重新选择")
}
func (c *Controller) createSite(w http.ResponseWriter, r *http.Request) {
	var p struct {
		RequestID   string       `json:"requestId"`
		Name        string       `json:"name"`
		Certificate string       `json:"certificate"`
		PrivateKey  string       `json:"privateKey"`
		Config      model.Config `json:"config"`
		Database    *struct {
			InstanceID string `json:"instanceId"`
			Name       string `json:"name"`
			Username   string `json:"username"`
			Password   string `json:"password"`
			Encoding   string `json:"encoding"`
			Collation  string `json:"collation"`
		} `json:"database"`
	}
	if !decode(w, r, &p) {
		return
	}
	if p.RequestID != "" {
		if !logic.Name.MatchString(p.RequestID) {
			fail(w, 400, "创建请求标识无效")
			return
		}
		if existing, er := c.App.Store.SiteProvisionByRequest(p.RequestID); er == nil {
			send(w, 202, existing)
			return
		} else if !errors.Is(er, sql.ErrNoRows) {
			fail(w, 500, "创建请求查询失败，请核对原请求后重试")
			return
		}
	}
	if p.Config == nil {
		p.Config = model.Config{}
	}
	site := model.Resource{Kind: "site", Name: p.Name, Config: p.Config}
	site.Config["siteBaseDirectory"] = c.panelPreferences().String("siteDirectory")
	for _, k := range []string{"documentRoot", "runningRoot", "databaseId"} {
		delete(site.Config, k)
	}
	if err := c.bindSite(&site); err != nil {
		fail(w, 409, err.Error())
		return
	}
	if site.Config.String("rootDirectory") == "" && site.Config.String("type") != "proxy" {
		site.Config["rootDirectory"] = filepath.Join(c.panelPreferences().String("siteDirectory"), filepath.Base(logic.DefaultSiteRoot(site.Config.String("domains"))))
	}
	if err := logic.HashSiteAccessPasswords(site.Config); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if err := c.validate(&site); err != nil {
		fail(w, 400, err.Error())
		return
	}
	var db *model.Resource
	if p.Database != nil && p.Database.InstanceID != "" {
		d := p.Database
		i, err := c.App.Store.Resource(d.InstanceID)
		if err != nil || i.Kind != "instance" || (i.Config.String("software") != "mysql" && i.Config.String("software") != "postgresql") || (i.Status != "ready" && i.Status != "running") {
			fail(w, 409, "请选择运行中的 MySQL 或 PostgreSQL 实例")
			return
		}
		db = &model.Resource{Kind: "database", Name: d.Name, Config: model.Config{"instanceId": i.ID, "username": d.Username, "password": d.Password, "source": "127.0.0.1", "encoding": d.Encoding, "collation": d.Collation}}
		if err = c.validate(db); err != nil {
			fail(w, 400, err.Error())
			return
		}
	}
	task, err := c.App.Store.CreateSiteProvision(&site, db, model.Config{"certificate": p.Certificate, "privateKey": p.PrivateKey, "requestId": p.RequestID})
	if err != nil {
		fail(w, 409, "创建未提交："+err.Error())
		return
	}
	c.App.Store.Audit("site.create", site.ID, clientIP(r))
	send(w, 202, model.Config{"siteId": site.ID, "databaseId": site.Config.String("databaseId"), "taskId": task.ID})
}
func (c *Controller) retrySite(w http.ResponseWriter, r *http.Request) {
	site, err := c.App.Store.Resource(r.PathValue("id"))
	if err != nil || site.Kind != "site" {
		fail(w, 404, "站点不存在")
		return
	}
	var candidate struct {
		Name     string       `json:"name"`
		Config   model.Config `json:"config"`
		Revision int          `json:"revision"`
	}
	if !decode(w, r, &candidate) {
		return
	}
	if site.Status != "error" && site.Status != "pending" {
		fail(w, 409, "站点已完成创建，请通过设置修改")
		return
	}
	if candidate.Config != nil {
		if candidate.Revision != site.Revision {
			fail(w, 409, "站点已变化，请重新载入")
			return
		}
		for _, key := range []string{"documentRoot", "runningRoot", "rootDirectory", "databaseId", "siteBaseDirectory", "instanceId", "category"} {
			candidate.Config[key] = site.Config[key]
		}
		security.PreserveMasked(candidate.Config, site.Config)
		site.Name = candidate.Name
		site.Config = candidate.Config
		if err = c.validate(&site); err != nil {
			fail(w, 400, err.Error())
			return
		}
		// Updating and enqueueing the retry is atomic and cannot race a running task.
	}
	// The relation is read from persisted resources, never from browser supplied credentials.
	params, requestErr := c.App.Store.SiteProvisionRequest(site.ID)
	if requestErr != nil {
		fail(w, 409, "站点没有可重试的创建记录")
		return
	}
	task, err := c.App.Store.ResumeSiteProvision(site, candidate.Config != nil, params)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	c.App.Store.Audit("site.retry", site.ID, clientIP(r))
	send(w, 202, model.Config{"taskId": task.ID, "siteId": site.ID})
}

// Queue the candidate without replacing panel metadata before runtime validation succeeds.
func (c *Controller) saveSiteSettings(w http.ResponseWriter, r *http.Request) {
	site, err := c.App.Store.Resource(r.PathValue("id"))
	if err != nil || site.Kind != "site" {
		fail(w, 404, "站点不存在")
		return
	}
	var p struct {
		Name     string       `json:"name"`
		Config   model.Config `json:"config"`
		Revision int          `json:"revision"`
	}
	if !decode(w, r, &p) {
		return
	}
	if p.Revision != site.Revision {
		fail(w, 409, "配置已变化，请重新载入后合并草稿")
		return
	}
	if p.Config == nil {
		fail(w, 400, "配置不能为空")
		return
	}
	old := site.Config
	for _, k := range []string{"documentRoot", "runningRoot", "rootDirectory", "databaseId", "siteBaseDirectory", "externalApplication", "legacyAppId"} {
		p.Config[k] = old[k]
	}
	if old.String("type") != p.Config.String("type") && !(isLocalSite(old.String("type")) && isLocalSite(p.Config.String("type"))) {
		fail(w, 400, "此项目不支持更改运行类型")
		return
	}
	security.PreserveMasked(p.Config, old)
	if err = logic.HashSiteAccessPasswords(p.Config); err != nil {
		fail(w, 400, err.Error())
		return
	}
	site.Name = p.Name
	site.Config = p.Config
	if err = c.validate(&site); err != nil {
		fail(w, 400, err.Error())
		return
	}
	task, err := c.App.Store.Enqueue(model.Operation{Name: "site.apply", Resource: site, Params: model.Config{}})
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	c.App.Store.Audit("site.settings", site.ID, clientIP(r))
	send(w, 202, model.Config{"taskId": task.ID})
}
func isLocalSite(kind string) bool { return kind == "php" || kind == "static" }

func (c *Controller) taskStatus(w http.ResponseWriter, r *http.Request) {
	t, err := c.App.Store.Task(r.PathValue("id"))
	if err != nil {
		fail(w, 404, "任务不存在")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	send(w, 200, t)
}

func (c *Controller) siteProvisionStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	v, err := c.App.Store.SiteProvisionByRequest(r.PathValue("requestId"))
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			fail(w, 500, "创建请求查询失败，请核对任务现场后重试")
			return
		}
		fail(w, 404, "创建请求尚未登记")
		return
	}
	send(w, 200, v)
}
