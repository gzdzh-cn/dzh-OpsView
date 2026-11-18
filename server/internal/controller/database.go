package controller

import (
	"context"
	"encoding/json"
	"errors"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"opsview/internal/model"
	"opsview/internal/security"
	"strings"
	"time"
)

func (c *Controller) databases(w http.ResponseWriter, r *http.Request) {
	rows, err := c.App.Store.Resources("database")
	if err != nil {
		fail(w, 500, "读取数据库失败")
		return
	}
	instances, err := c.App.Store.Resources("instance")
	if err != nil {
		fail(w, 500, "读取实例失败")
		return
	}
	byID := map[string]model.Resource{}
	for _, i := range instances {
		byID[i.ID] = i
	}
	engine := r.URL.Query().Get("engine")
	instanceID := r.URL.Query().Get("instanceId")
	recycled := r.URL.Query().Get("recycled") == "true"
	out := []model.Resource{}
	for _, db := range rows {
		i := byID[db.Config.String("instanceId")]
		if (engine != "" && i.Config.String("software") != engine) || (instanceID != "" && i.ID != instanceID) || (db.Status == "recycled") != recycled {
			continue
		}
		db.Config["engine"] = i.Config.String("software")
		db.Config["instanceName"] = i.Name
		db.Config["instanceStatus"] = i.Status
		hasPassword := db.Config.String("password") != ""
		db.Config = security.Redact(db.Config)
		db.Config["hasPassword"] = hasPassword
		db.Config["availableActions"] = []string{"tables", "permissions", "password", "backup", "recycle"}
		if recycled {
			db.Config["availableActions"] = []string{"restore", "purge"}
		}
		if i.ID == "" {
			db.Config["availableActions"] = []string{}
		}
		out = append(out, db)
	}
	send(w, 200, model.Config{"rows": out})
}
func (c *Controller) databaseOperation(w http.ResponseWriter, r *http.Request) {
	resource, err := c.App.Store.Resource(r.PathValue("id"))
	if err != nil || (resource.Kind != "instance" && resource.Kind != "database") {
		fail(w, 404, "数据库资源不存在")
		return
	}
	operation := r.PathValue("operation")
	params := model.Config{}
	if r.Method == "GET" {
		for k, v := range r.URL.Query() {
			if len(v) > 0 {
				params[k] = v[0]
			}
		}
	} else if !decode(w, r, &params) {
		return
	}
	if operation == "note" && r.Method == "POST" && resource.Kind == "database" {
		if params.Int("revision") != resource.Revision {
			fail(w, 409, "记录已修改，请刷新")
			return
		}
		if len(params.String("note")) > 1000 {
			fail(w, 400, "备注过长")
			return
		}
		resource.Config["note"] = params.String("note")
		if err = c.App.Store.Update(&resource); err != nil {
			fail(w, 409, err.Error())
			return
		}
		c.App.Store.Audit("database.note", resource.ID, clientIP(r))
		send(w, 200, model.Config{"ok": true})
		return
	}
	reads := map[string]bool{"inventory": true, "tables": true, "structure": true, "permissions": true, "capabilities": true}
	writes := map[string]bool{"adopt": true, "password": true, "grant": true, "maintain": true, "recycle": true, "restore": true, "purge": true, "access": true}
	if reads[operation] && r.Method == "GET" {
		c.databaseExecute(w, r, model.Operation{Name: "dbadmin." + operation, Resource: resource, Params: params})
		return
	}
	if !writes[operation] || r.Method != "POST" {
		fail(w, 404, "数据库操作不存在")
		return
	}
	task, err := c.App.Store.Enqueue(model.Operation{Name: "dbadmin." + operation, Resource: resource, Params: params})
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	c.App.Store.Audit("database."+operation, resource.ID, clientIP(r))
	send(w, 202, task)
}
func (c *Controller) databaseExecute(w http.ResponseWriter, r *http.Request, op model.Operation) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	data, err := c.App.Executor.Execute(ctx, op, func(string) {})
	if err != nil {
		var conflict *model.RevisionConflict
		if errors.As(err, &conflict) {
			send(w, 409, model.Config{"error": "内容已变化，请重新载入", "revision": conflict.Revision})
			return
		}
		fail(w, 422, err.Error())
		return
	}
	send(w, 200, data)
}
func (c *Controller) redisOperation(w http.ResponseWriter, r *http.Request) {
	resource, err := c.App.Store.Resource(r.PathValue("id"))
	if err != nil || resource.Kind != "instance" || resource.Config.String("software") != "redis" {
		fail(w, 404, "Redis 实例不存在")
		return
	}
	op := r.PathValue("operation")
	if op != "databases" && op != "scan" && op != "read" && op != "write" && op != "download" {
		fail(w, 404, "Redis 操作不存在")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1500000)
	var p model.Config
	if !decode(w, r, &p) {
		return
	}
	r.Body.Close()
	raw, _ := json.Marshal(p)
	if len(raw) > 1500000 {
		fail(w, 413, "单次请求过大")
		return
	}
	if op == "download" {
		downloader, ok := c.App.Executor.(interface {
			Download(context.Context, model.Operation, http.ResponseWriter) error
		})
		if !ok {
			fail(w, 501, "开发模式不支持实例下载")
			return
		}
		if err = downloader.Download(r.Context(), model.Operation{Name: "redisdata.download", Resource: resource, Params: p}, w); err != nil {
			fail(w, 422, "下载失败，请检查实例状态")
		}
		return
	}
	if op == "write" {
		c.App.Store.Audit("redis.key."+p.String("operation"), resource.ID, clientIP(r))
	}
	c.databaseExecute(w, r, model.Operation{Name: "redisdata." + op, Resource: resource, Params: p})
}
func (c *Controller) databaseCredential(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decode(w, r, &p) {
		return
	}
	resource, err := c.App.Store.Resource(r.PathValue("id"))
	if err != nil || (resource.Kind != "database" && !(resource.Kind == "instance" && strings.Contains("|mysql|postgresql|redis|", "|"+resource.Config.String("software")+"|"))) {
		fail(w, 404, "数据库资源不存在")
		return
	}
	cookie, err := r.Cookie(sessionCookieName(r))
	if err != nil {
		fail(w, 401, "请先登录")
		return
	}
	session := security.Hash(cookie.Value)
	ip := "credentials:" + clientIP(r)
	c.auth.Lock()
	defer c.auth.Unlock()
	now := time.Now()
	if c.auth.credentials == nil {
		c.auth.credentials = map[string]time.Time{}
	}
	for k, t := range c.auth.credentials {
		if now.After(t) {
			delete(c.auth.credentials, k)
		}
	}
	if !now.Before(c.auth.credentials[session]) {
		at := c.auth.attempts[ip]
		if at.Count >= 5 && now.Before(at.Until) {
			fail(w, 429, "验证尝试过多，请 15 分钟后重试")
			return
		}
		if now.After(at.Until) {
			at.Count = 0
		}
		a, er := c.currentAdmin()
		valid := er == nil && bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(p.Password)) == nil
		if valid && a.TOTP != "" {
			valid = security.VerifyTOTP(a.TOTP, p.Code)
		}
		if !valid {
			at.Count++
			at.Until = now.Add(15 * time.Minute)
			c.auth.attempts[ip] = at
			fail(w, 403, "请验证当前管理员密码及双因素验证码")
			return
		}
		delete(c.auth.attempts, ip)
		if len(c.auth.credentials) > 4096 {
			fail(w, 429, "会话验证过多")
			return
		}
		c.auth.credentials[session] = now.Add(5 * time.Minute)
	}
	if resource.Config.String("password") == "" {
		fail(w, 409, "未保存此账号密码，请重置后使用")
		return
	}
	c.App.Store.Audit("database.credential.read", resource.ID, clientIP(r))
	send(w, 200, model.Config{"password": resource.Config.String("password"), "displaySeconds": 30})
}
