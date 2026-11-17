package controller

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"opsview/internal/catalog"
	"opsview/internal/logic"
	"opsview/internal/model"
	"opsview/internal/security"
)

func (c *Controller) plugins(w http.ResponseWriter, r *http.Request) {
	items := []any{}
	for _, p := range catalog.Plugins() {
		items = append(items, p)
	}
	var imported []map[string]any
	if c.App.Store.Setting("market-imports", &imported) == nil {
		for _, p := range imported {
			items = append(items, p)
		}
	}
	send(w, 200, items)
}
func (c *Controller) refreshRepository(w http.ResponseWriter, r *http.Request) {
	repository, e := c.App.Store.Resource(r.PathValue("id"))
	if e != nil || repository.Kind != "repository" {
		fail(w, 404, "仓库不存在")
		return
	}
	data, e := c.App.Executor.Execute(r.Context(), model.Operation{Name: "plugin.repository", Resource: repository}, func(string) {})
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	b, _ := json.Marshal(data["plugins"])
	var fetched []map[string]any
	if json.Unmarshal(b, &fetched) != nil {
		fail(w, 502, "仓库结果格式无效")
		return
	}
	var old []map[string]any
	_ = c.App.Store.Setting("market-imports", &old)
	items := []map[string]any{}
	for _, p := range old {
		if p["repositoryId"] != repository.ID {
			items = append(items, p)
		}
	}
	for _, p := range fetched {
		p["repositoryId"] = repository.ID
		items = append(items, p)
	}
	if e = c.App.Store.Set("market-imports", items); e != nil {
		fail(w, 500, "仓库同步保存失败")
		return
	}
	c.App.Store.Audit("repository.refresh", repository.ID, clientIP(r))
	send(w, 200, map[string]any{"count": len(fetched)})
}
func (c *Controller) importPlugin(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Name     string           `json:"name"`
		Envelope catalog.Envelope `json:"envelope"`
	}
	if !decode(w, r, &b) {
		return
	}
	payload, err := base64.StdEncoding.DecodeString(b.Envelope.Payload)
	var manifest catalog.Plugin
	if err != nil || json.Unmarshal(payload, &manifest) != nil || !logic.Name.MatchString(manifest.ID) {
		fail(w, 400, "插件清单缺少有效 ID")
		return
	}
	b.Name = manifest.ID
	res := model.Resource{ID: security.ID(), Kind: "plugin", Name: b.Name, Config: model.Config{"envelope": b.Envelope, "builtin": false}}
	if !validPluginName(b.Name) {
		fail(w, 400, "插件名称无效")
		return
	}
	if e := c.App.Store.Create(&res); e != nil {
		fail(w, 409, "插件记录创建失败")
		return
	}
	c.App.Store.Audit("plugin.import", res.ID, clientIP(r))
	res.Config = security.Redact(res.Config)
	send(w, 201, res)
}
func validPluginName(s string) bool { return len(s) > 0 && len(s) < 64 }
func (c *Controller) importCatalog(w http.ResponseWriter, r *http.Request) {
	var env catalog.Envelope
	if !decode(w, r, &env) {
		return
	}
	data, e := c.App.Executor.Execute(r.Context(), model.Operation{Name: "catalog.import", Params: model.Config{"envelope": env}}, func(string) {})
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	b, _ := json.Marshal(data["entry"])
	var entry catalog.Entry
	if e = json.Unmarshal(b, &entry); e != nil {
		fail(w, 500, "执行器返回了无效目录")
		return
	}
	if e = catalog.Register(entry); e != nil {
		fail(w, 400, e.Error())
		return
	}
	var imports []catalog.Entry
	_ = c.App.Store.Setting("catalog-imports", &imports)
	imports = append(imports, entry)
	if e = c.App.Store.Set("catalog-imports", imports); e != nil {
		fail(w, 500, "目录已导入执行器，但面板记录失败，请重启后重新同步")
		return
	}
	c.App.Store.Audit("catalog.import", entry.ID, clientIP(r))
	send(w, 201, entry)
}
