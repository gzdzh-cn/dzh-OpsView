package controller

import (
	"context"
	"errors"
	"io"
	"net/http"
	"opsview/internal/logic"
	"opsview/internal/model"

	"os"
	"path/filepath"
	"strings"
)

func (c *Controller) archives(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		data, err := c.App.Executor.Execute(r.Context(), model.Operation{Name: "archive.list"}, func(string) {})
		if err != nil {
			fail(w, 502, err.Error())
			return
		}
		send(w, 200, data)
		return
	}
	var p model.Config
	if !decode(w, r, &p) {
		return
	}
	verb := r.PathValue("action")

	if verb == "verify" || verb == "preview" || verb == "delete" || verb == "restore-directory" {
		operation := "archive." + verb
		id := p.String("backupId")
		if p.String("kind") == "panel" {
			if verb == "restore-directory" {
				fail(w, 400, "面板仅支持本机维护恢复")
				return
			}
			name := p.String("file")
			if filepath.Base(name) != name || !strings.HasSuffix(name, ".ovp") {
				fail(w, 400, "文件名无效")
				return
			}
			operation = "panel.archive-" + verb
			id = "panel-metadata"
		}
		if id == "" {
			fail(w, 400, "缺少备份标识")
			return
		}
		task, err := c.App.Store.Enqueue(model.Operation{Name: operation, Resource: model.Resource{ID: id, Kind: "system", Name: "备份归档操作"}, Params: p})
		if err != nil {
			fail(w, 409, err.Error())
			return
		}
		c.App.Store.Audit(operation, id, clientIP(r))
		send(w, 202, task)
		return
	}

	fail(w, 400, "归档操作无效")
}
func (c *Controller) archiveDownload(w http.ResponseWriter, r *http.Request) {
	id, file := r.URL.Query().Get("backupId"), r.URL.Query().Get("file")
	transport, ok := c.App.Executor.(interface {
		Download(context.Context, model.Operation, http.ResponseWriter) error
	})
	if !ok {
		fail(w, 503, "下载执行器不可用")
		return
	}
	err := transport.Download(r.Context(), model.Operation{Name: "archive.download", Resource: model.Resource{ID: id}, Params: model.Config{"artifact": file}}, w)
	if err != nil {
		fail(w, 502, err.Error())
		return
	}
	c.App.Store.Audit("archive.download", id, clientIP(r))
}
func (c *Controller) uploadArchive(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("file")
	if filepath.Base(name) != name || len(name) > 120 || (!strings.HasSuffix(name, ".ovp") && !strings.HasSuffix(name, ".ovb")) {
		fail(w, 400, "仅支持 OpsView .ovp/.ovb 归档")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<30)
	defer r.Body.Close()
	if strings.HasSuffix(name, ".ovb") {
		transport, ok := c.App.Executor.(interface {
			UploadArchive(context.Context, string, string, io.Reader) error
		})
		if !ok {
			fail(w, 503, "上传执行器不可用")
			return
		}
		if err := transport.UploadArchive(r.Context(), r.URL.Query().Get("backupId"), name, r.Body); err != nil {
			fail(w, 400, err.Error())
			return
		}
		c.App.Store.Audit("archive.upload", r.URL.Query().Get("backupId"), clientIP(r))
		send(w, 201, model.Config{"ok": true})
		return
	}
	base := c.App.PanelBackupDirectory()
	if err := os.MkdirAll(base, 0700); err != nil {
		fail(w, 500, err.Error())
		return
	}
	temp, err := os.CreateTemp(base, "upload-*.ovp")
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	defer os.Remove(temp.Name())
	_ = temp.Chmod(0600)
	_, err = io.Copy(temp, r.Body)
	err = errors.Join(err, temp.Close())
	if err != nil {
		fail(w, 400, "上传中断或超过 2 GiB")
		return
	}
	stage, err := os.MkdirTemp(c.DataDir, "verify-*")
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	os.Remove(stage)
	defer os.RemoveAll(stage)
	if err = logic.RestorePanel(c.DataDir, temp.Name(), stage); err != nil {
		fail(w, 400, "归档无法由当前主密钥验证："+err.Error())
		return
	}
	// Exclusive publication never overwrites an existing backup.
	dest := filepath.Join(base, name)
	if err = os.Link(temp.Name(), dest); err != nil {
		fail(w, 409, "同名备份已存在或文件无法发布")
		return
	}
	c.App.Store.Audit("panel.backup.upload", name, clientIP(r))
	send(w, 201, model.Config{"name": name, "verified": true})
}

func (c *Controller) archiveTaskResult(w http.ResponseWriter, r *http.Request) {
	task, err := c.App.Store.Task(r.PathValue("id"))
	if err != nil || (!strings.HasPrefix(task.Operation, "archive.") && !strings.HasPrefix(task.Operation, "panel.archive-")) {
		fail(w, 404, "归档任务不存在")
		return
	}
	result, _ := c.App.Store.TaskResult(task.ID)
	send(w, 200, model.Config{"state": task.State, "error": task.Error, "result": result})
}
