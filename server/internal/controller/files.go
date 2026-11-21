package controller

import (
	"context"
	"errors"
	"net/http"
	"opsview/internal/model"
	"strings"
)

// File APIs accept structured operations; the root executor enforces the same
// policy for the new server scope and the legacy resource-relative adapter.
func (c *Controller) files(w http.ResponseWriter, r *http.Request) {
	var p model.Config
	if !decode(w, r, &p) {
		return
	}
	action := p.String("action")
	allowed := map[string]bool{"identities": true, "list": true, "read": true, "write": true, "metadata": true, "favorite": true, "note": true, "mkdir": true, "stat": true, "search": true, "size": true, "trash": true, "copy": true, "move": true, "rename": true, "permissions": true, "chmod": true, "delete": true, "compress": true, "extract": true, "upload-create": true, "upload-status": true, "upload-finish": true, "upload-cancel": true}
	if !allowed[action] {
		fail(w, 400, "文件操作无效")
		return
	}
	op := model.Operation{Name: "files." + action, Params: p}
	if p.String("scope") != "server" {
		res, err := c.App.Store.Resource(p.String("resourceId"))
		if err != nil || (res.Kind != "site" && res.Kind != "app" && res.Kind != "instance") {
			fail(w, 400, "请选择有效受管资源，或使用服务器文件管理")
			return
		}
		op.Resource = res
	}
	// Trash has its own sub-action; keep it separate from the outer operation.
	if action == "trash" {
		p["action"] = p.String("trashAction")
	}
	async := action == "copy" || action == "move" || action == "compress" || action == "extract" || action == "search" || action == "size" || (action == "permissions" && p.Bool("recursive")) || (action == "trash" && (p.String("action") == "purge" || p.String("action") == "empty"))
	if async {
		op.Resource = model.Resource{ID: "files-host", Name: "服务器文件", Kind: "system"}
		task, err := c.App.Store.Enqueue(op)
		if err != nil {
			fail(w, 409, err.Error())
			return
		}
		c.auditFileOperation(action, "queued", p, r)
		send(w, 202, task)
		return
	}
	auditAction := action
	if action == "trash" {
		auditAction += "." + p.String("action")
	}
	data, err := c.App.Executor.Execute(r.Context(), op, func(string) {})
	if err != nil {
		if action != "read" && action != "list" && action != "metadata" && action != "upload-status" {
			c.auditFileOperation(auditAction, "failed", p, r)
		}
		var conflict *model.RevisionConflict
		if errors.As(err, &conflict) {
			send(w, 409, model.Config{"error": err.Error(), "code": "revision_conflict", "revision": conflict.Revision})
			return
		}
		fail(w, 422, err.Error())
		return
	}
	if action != "read" && action != "list" && action != "metadata" && action != "upload-status" {
		if p.String("path") == "" && data.String("path") != "" {
			p["path"] = data.String("path")
		}
		if paths, ok := data["paths"]; ok {
			p["paths"] = paths
		}
		c.auditFileOperation(auditAction, "succeeded", p, r)
	}
	send(w, 200, data)
}
func (c *Controller) fileTransfer(w http.ResponseWriter, r *http.Request) {
	transport, ok := c.App.Executor.(interface {
		FileTransfer(context.Context, *http.Request, http.ResponseWriter) error
	})
	if !ok {
		fail(w, 503, "开发模拟模式不连接服务器文件；请使用 Linux 执行器")
		return
	}
	if r.Method == "PUT" {
		r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	}
	if r.Method == "GET" {
		c.App.Store.Audit("files.download:"+r.URL.Query().Get("path"), "", clientIP(r))
	}
	if err := transport.FileTransfer(r.Context(), r, w); err != nil {
		fail(w, 502, err.Error())
	}
}
func (c *Controller) fileTaskResult(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, err := c.App.Store.Task(id)
	if err != nil || !strings.HasPrefix(task.Operation, "files.") {
		fail(w, 404, "文件任务不存在")
		return
	}
	if task.State != "succeeded" {
		send(w, 200, model.Config{"state": task.State, "error": task.Error})
		return
	}
	var result model.Config
	if err = c.App.Store.Setting("task-result:"+id, &result); err != nil {
		fail(w, 404, "结果不存在")
		return
	}
	send(w, 200, model.Config{"state": task.State, "result": result})
}

func (c *Controller) auditFileOperation(action, result string, p model.Config, r *http.Request) {
	paths := []string{}
	if path := p.String("path"); path != "" {
		paths = append(paths, path)
	}
	if batch, ok := p["paths"].([]any); ok {
		for _, v := range batch {
			if path, ok := v.(string); ok {
				paths = append(paths, path)
			}
		}
	}
	if len(paths) == 0 {
		paths = []string{""}
	}
	if len(paths) > 1000 {
		paths = paths[:1000]
	}
	for _, path := range paths {
		c.App.Store.Audit("files."+action+":"+result+":"+path, "files-host", clientIP(r))
	}
}
