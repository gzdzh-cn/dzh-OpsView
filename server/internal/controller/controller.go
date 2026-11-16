package controller

import (
	"encoding/json"
	"errors"
	"github.com/robfig/cron/v3"
	"io"
	"io/fs"
	"net"
	"net/http"
	"opsview/internal/api"
	"opsview/internal/catalog"
	"opsview/internal/logic"
	"opsview/internal/model"
	"opsview/internal/security"
	"opsview/internal/version"
	"strconv"
	"strings"
	"time"
)

type Controller struct {
	App     *logic.App
	DataDir string
	Public  fs.FS
	auth    authState
}

func New(app *logic.App, data string, public fs.FS) *Controller {
	app.PanelDir = data
	return &Controller{App: app, DataDir: data, Public: public, auth: authState{attempts: map[string]attempt{}}}
}
func send(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, code int, msg string) { send(w, code, map[string]any{"error": msg}) }
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		fail(w, 400, "请求数据格式无效："+e.Error())
		return false
	}
	if e := d.Decode(new(any)); e != io.EOF {
		fail(w, 400, "仅允许一个 JSON 对象")
		return false
	}
	return true
}
func (c *Controller) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		send(w, 200, map[string]any{"status": "ok", "version": version.Current})
	})
	mux.HandleFunc("GET /api/v1/auth", c.authStatus)
	mux.HandleFunc("POST /api/v1/auth/setup", c.setup)
	mux.HandleFunc("POST /api/v1/auth/login", c.login)
	routes := map[string]http.HandlerFunc{
		"GET /api/v1/resources/{id}/download": c.download,
		"GET /api/v1/settings/backups":        c.panelBackups, "POST /api/v1/settings/backup": c.backupPanel, "GET /api/v1/settings/backup/{file}": c.downloadPanelBackup,
		"POST /api/v1/auth/logout": c.logout, "POST /api/v1/auth/two-factor": c.twoFactor, "POST /api/v1/auth/password": c.password,
		"GET /api/v1/overview": c.overview, "GET /api/v1/resources": c.resources, "POST /api/v1/resources": c.createResource, "PUT /api/v1/resources/{id}": c.updateResource, "DELETE /api/v1/resources/{id}": c.deleteResource, "POST /api/v1/resources/{id}/actions/{action}": c.action,
		"GET /api/v1/catalog": func(w http.ResponseWriter, r *http.Request) { send(w, 200, catalog.Entries()) }, "GET /api/v1/plugins": c.plugins, "POST /api/v1/plugins/import": c.importPlugin, "POST /api/v1/catalog/import": c.importCatalog,
		"GET /api/v1/tasks": c.tasks, "GET /api/v1/tasks/{id}/events": c.events, "GET /api/v1/audit": c.audit, "GET /api/v1/settings": c.settings, "POST /api/v1/settings/certificate": c.panelCertificate, "POST /api/v1/repositories/{id}/refresh": c.refreshRepository, "PUT /api/v1/settings": c.saveSettings,
		"POST /api/v1/files": c.files, "GET /api/v1/system": c.system, "POST /api/v1/system/actions/{action}": c.systemAction,
		"GET /api/v1/docker/{collection}": c.dockerList, "POST /api/v1/docker/containers/{id}/{action}": c.dockerAction,
		"POST /api/v1/docker/manage/{collection}/{action}": c.dockerManage,
		"GET /api/v1/openapi.json": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(api.Spec)
		},
	}
	for path, h := range routes {
		mux.HandleFunc(path, c.authenticated(h))
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "接口不存在") })
	mux.HandleFunc("/", c.static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Cache-Control", "no-store")
		if r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if origin := r.Header.Get("Origin"); origin != "" {
				scheme := "http"
				if r.TLS != nil {
					scheme = "https"
				}
				if origin != scheme+"://"+r.Host {
					fail(w, 403, "不允许跨来源请求")
					return
				}
			}
		}
		mux.ServeHTTP(w, r)
	})
}
func (c *Controller) static(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		fail(w, 405, "请求方法不支持")
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" {
		p = "index.html"
	}
	if !fs.ValidPath(p) {
		http.NotFound(w, r)
		return
	}
	b, e := fs.ReadFile(c.Public, p)
	if e != nil {
		if strings.Contains(p, ".") {
			http.NotFound(w, r)
			return
		}
		b, e = fs.ReadFile(c.Public, "index.html")
		p = "index.html"
	}
	if e != nil {
		fail(w, 503, "前端资源尚未构建")
		return
	}
	switch {
	case strings.HasSuffix(p, ".js"):
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	case strings.HasSuffix(p, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(p, ".svg"):
		w.Header().Set("Content-Type", "image/svg+xml")
	case strings.HasSuffix(p, ".html"):
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	_, _ = w.Write(b)
}
func (c *Controller) resources(w http.ResponseWriter, r *http.Request) {
	rs, e := c.App.Store.Resources(r.URL.Query().Get("kind"))
	if e != nil {
		fail(w, 500, "读取资源失败")
		return
	}
	for i := range rs {
		rs[i].Config = security.Redact(rs[i].Config)
	}
	send(w, 200, rs)
}
func (c *Controller) createResource(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind   string       `json:"kind"`
		Name   string       `json:"name"`
		Config model.Config `json:"config"`
	}
	if !decode(w, r, &body) {
		return
	}
	res := model.Resource{Kind: body.Kind, Name: body.Name, Config: body.Config}
	if res.Config == nil {
		res.Config = model.Config{}
	}
	if e := c.validate(&res); e != nil {
		fail(w, 400, e.Error())
		return
	}
	if e := c.App.Store.Create(&res); e != nil {
		fail(w, 409, "创建失败：名称可能已存在")
		return
	}
	c.App.Store.Audit("resource.create", res.ID, clientIP(r))
	if res.Kind == "schedule" || res.Kind == "repository" {
		res.Status = "ready"
		_ = c.App.Store.Update(&res)
		c.App.ReloadSchedules()
		res.Config = security.Redact(res.Config)
		send(w, 201, res)
		return
	}
	res.Config = security.Redact(res.Config)
	send(w, 201, res)
}
func (c *Controller) validate(res *model.Resource) error {
	if res.Kind == "instance" {
		entry, ok := catalog.Find(res.Config.String("catalogId"))
		if !ok {
			return errors.New("软件版本不在已验证目录中；请先导入签名配方")
		}
		res.Config["software"] = entry.Software
		res.Config["version"] = entry.Version
		if res.Config.String("mode") == "native" && entry.Source == "" {
			return errors.New("此版本未提供原生安装配方")
		}
	}
	if res.Kind == "schedule" {
		if _, e := cron.ParseStandard(res.Config.String("cron")); e != nil {
			return errors.New("Cron 表达式无效，使用五段格式")
		}
	}
	return logic.Validate(*res)
}
func (c *Controller) updateResource(w http.ResponseWriter, r *http.Request) {
	res, e := c.App.Store.Resource(r.PathValue("id"))
	if e != nil {
		fail(w, 404, "资源不存在")
		return
	}
	if res.Kind == "instance" {
		fail(w, 409, "软件实例和插件配置不可直接修改，请创建新实例")
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
	if b.Revision != res.Revision {
		fail(w, 409, "配置已变化，请刷新")
		return
	}

	if res.Kind == "plugin" && b.Config.String("pluginId") != res.Config.String("pluginId") {
		fail(w, 400, "插件标识不可修改")
		return
	}
	security.PreserveMasked(b.Config, res.Config)

	res.Name = b.Name
	res.Config = b.Config
	if e = c.validate(&res); e != nil {
		fail(w, 400, e.Error())
		return
	}
	tasks, _ := c.App.Store.Tasks()
	for _, t := range tasks {
		if t.ResourceID == res.ID && (t.State == "queued" || t.State == "running") {
			fail(w, 409, "资源有运行中的任务")
			return
		}
	}
	if e = c.App.Store.Update(&res); e != nil {
		fail(w, 409, e.Error())
		return
	}
	c.App.Store.Audit("resource.update", res.ID, clientIP(r))
	c.App.ReloadSchedules()
	res.Config = security.Redact(res.Config)
	send(w, 200, res)
}
func (c *Controller) deleteResource(w http.ResponseWriter, r *http.Request) {
	res, e := c.App.Store.Resource(r.PathValue("id"))
	if e != nil {
		fail(w, 404, "资源不存在")
		return
	}
	if res.Kind != "schedule" && res.Kind != "repository" && res.Status != "pending" {
		fail(w, 409, "请使用资源的卸载或删除任务，以便同步处理服务器状态")
		return
	}
	all, _ := c.App.Store.Resources("")
	for _, x := range all {
		if x.Config.String("instanceId") == res.ID || x.Config.String("targetId") == res.ID {
			fail(w, 409, "资源仍被引用")
			return
		}
	}
	tasks, _ := c.App.Store.Tasks()
	for _, t := range tasks {
		if t.ResourceID == res.ID && (t.State == "queued" || t.State == "running") {
			fail(w, 409, "资源正在执行任务")
			return
		}
	}
	if e = c.App.Store.Delete(res.ID); e != nil {
		fail(w, 500, "删除失败")
		return
	}
	c.App.ReloadSchedules()
	c.App.Store.Audit("resource.delete", res.ID, clientIP(r))
	send(w, 200, map[string]any{"ok": true})
}
func (c *Controller) action(w http.ResponseWriter, r *http.Request) {
	res, e := c.App.Store.Resource(r.PathValue("id"))
	if e != nil {
		fail(w, 404, "资源不存在")
		return
	}
	var p model.Config
	if !decode(w, r, &p) {
		return
	}
	if r.PathValue("action") == "logs" || (res.Kind == "site" && r.PathValue("action") == "configuration") {
		c.exec(w, r, model.Operation{Name: res.Kind + "." + r.PathValue("action"), Resource: res, Params: p})
		return
	}
	task, e := c.App.Queue(res, r.PathValue("action"), p)
	if e != nil {
		fail(w, 409, e.Error())
		return
	}
	c.App.Store.Audit("action."+r.PathValue("action"), res.ID, clientIP(r))
	send(w, 202, task)
}
func (c *Controller) tasks(w http.ResponseWriter, r *http.Request) {
	v, e := c.App.Store.Tasks()
	if e != nil {
		fail(w, 500, "读取任务失败")
		return
	}
	send(w, 200, v)
}
func (c *Controller) events(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	if h := r.Header.Get("Last-Event-ID"); h != "" {
		after, _ = strconv.ParseInt(h, 10, 64)
	}
	if r.URL.Query().Get("stream") != "true" {
		v, e := c.App.Store.Events(id, after)
		if e != nil {
			fail(w, 500, "读取日志失败")
			return
		}
		send(w, 200, v)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, "不支持流式响应")
		return
	}
	_, _ = io.WriteString(w, ": connected\n\n")
	flusher.Flush()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:

			for {
				if r.Context().Err() != nil {
					return
				}
				events, err := c.App.Store.Events(id, after)
				if err != nil {
					return
				}
				for _, event := range events {
					data, _ := json.Marshal(event)
					if _, err = io.WriteString(w, "id: "+strconv.FormatInt(event.Sequence, 10)+"\ndata: "+string(data)+"\n\n"); err != nil {
						return
					}
					after = event.Sequence
				}
				flusher.Flush()
				if len(events) < 500 {
					break
				}
			}
			_, _ = io.WriteString(w, ": heartbeat\n\n")
			flusher.Flush()
			var state string
			if c.App.Store.DB.QueryRow(`SELECT state FROM tasks WHERE id=?`, id).Scan(&state) != nil {
				return
			}
			if state != "queued" && state != "running" {
				_, _ = io.WriteString(w, "event: done\ndata: {}\n\n")
				flusher.Flush()
				return
			}
		}
	}
}
func (c *Controller) audit(w http.ResponseWriter, r *http.Request) {
	v, e := c.App.Store.Audits()
	if e != nil {
		fail(w, 500, "读取审计失败")
		return
	}
	send(w, 200, v)
}
func (c *Controller) settings(w http.ResponseWriter, r *http.Request) {
	v := model.Config{}
	_ = c.App.Store.Setting("panel", &v)
	send(w, 200, security.Redact(v))
}
func (c *Controller) saveSettings(w http.ResponseWriter, r *http.Request) {
	var v model.Config
	if !decode(w, r, &v) {
		return
	}
	for k := range v {
		if k != "name" && k != "allowedCIDRs" {
			fail(w, 400, "不支持的面板设置")
			return
		}
	}
	ip := net.ParseIP(clientIP(r))
	allowed := strings.TrimSpace(v.String("allowedCIDRs")) == ""
	for _, s := range strings.Fields(v.String("allowedCIDRs")) {
		_, cidr, e := net.ParseCIDR(s)
		if e != nil {
			fail(w, 400, "IP 限制须使用 CIDR 格式")
			return
		}
		if cidr.Contains(ip) {
			allowed = true
		}
	}
	if !allowed {
		fail(w, 400, "允许范围必须包含当前连接 IP")
		return
	}
	if e := c.App.Store.Set("panel", v); e != nil {
		fail(w, 500, "设置保存失败")
		return
	}
	c.App.Store.Audit("settings.update", "", clientIP(r))
	send(w, 200, v)
}
func (c *Controller) exec(w http.ResponseWriter, r *http.Request, op model.Operation) {
	data, e := c.App.Executor.Execute(r.Context(), op, func(string) {})
	if e != nil {
		fail(w, 502, e.Error())
		return
	}
	send(w, 200, data)
}
func (c *Controller) files(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ResourceID string `json:"resourceId"`
		Action     string `json:"action"`
		Path       string `json:"path"`
		Content    string `json:"content"`
		Mode       string `json:"mode"`
	}
	if !decode(w, r, &b) {
		return
	}
	res, e := c.App.Store.Resource(b.ResourceID)
	if e != nil || (res.Kind != "site" && res.Kind != "app" && res.Kind != "instance") {
		fail(w, 400, "请选择受管理的网站或应用")
		return
	}
	c.App.Store.Audit("files."+b.Action, res.ID, clientIP(r))
	c.exec(w, r, model.Operation{Name: "files." + b.Action, Resource: res, Params: model.Config{"path": b.Path, "content": b.Content, "mode": b.Mode}})
}
func (c *Controller) system(w http.ResponseWriter, r *http.Request) {
	c.exec(w, r, model.Operation{Name: "system.inspect", Params: model.Config{}})
}
func (c *Controller) systemAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	if action != "install-docker" && action != "service" && action != "logs" && action != "import-catalog" && action != "reconcile" {
		fail(w, 400, "不支持的系统操作")
		return
	}
	var p model.Config
	if !decode(w, r, &p) {
		return
	}
	if action == "logs" || action == "reconcile" {
		c.exec(w, r, model.Operation{Name: "system." + action, Params: p})
		return
	}
	res := model.Resource{ID: "system-" + action, Kind: "system", Name: "system"}
	t, e := c.App.Store.Enqueue(model.Operation{Name: "system." + action, Resource: res, Params: p})
	if e != nil {
		fail(w, 409, e.Error())
		return
	}
	c.App.Store.Audit("system."+action, "", clientIP(r))
	send(w, 202, t)
}
func (c *Controller) dockerList(w http.ResponseWriter, r *http.Request) {
	c.exec(w, r, model.Operation{Name: "docker.list", Params: model.Config{"collection": r.PathValue("collection")}})
}
func (c *Controller) dockerAction(w http.ResponseWriter, r *http.Request) {
	var p model.Config
	if !decode(w, r, &p) {
		return
	}
	p["id"] = r.PathValue("id")
	p["action"] = r.PathValue("action")
	if p.String("action") == "logs" || p.String("action") == "stats" {
		c.exec(w, r, model.Operation{Name: "docker.action", Params: p})
		return
	}
	t, e := c.App.Store.Enqueue(model.Operation{Name: "docker.action", Resource: model.Resource{ID: "docker-" + p.String("id"), Name: "docker", Kind: "system"}, Params: p})
	if e != nil {
		fail(w, 409, e.Error())
		return
	}
	c.App.Store.Audit("docker."+p.String("action"), p.String("id"), clientIP(r))
	send(w, 202, t)
}

func (c *Controller) dockerManage(w http.ResponseWriter, r *http.Request) {
	collection, action := r.PathValue("collection"), r.PathValue("action")
	if !((collection == "images" && (action == "pull" || action == "delete")) || ((collection == "networks" || collection == "volumes") && (action == "create" || action == "delete"))) {
		fail(w, 400, "Docker 管理操作无效")
		return
	}
	var p model.Config
	if !decode(w, r, &p) {
		return
	}
	if p == nil || p.String("name") == "" {
		fail(w, 400, "请填写资源名称")
		return
	}
	p["collection"], p["action"] = collection, action
	task, err := c.App.Store.Enqueue(model.Operation{Name: "docker.manage", Resource: model.Resource{ID: "docker-management", Name: "docker", Kind: "system"}, Params: p})
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	c.App.Store.Audit("docker."+collection+"."+action, p.String("name"), clientIP(r))
	send(w, 202, task)
}
