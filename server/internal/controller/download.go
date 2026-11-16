package controller

import (
	"context"
	"net/http"
	"opsview/internal/model"
)

func (c *Controller) download(w http.ResponseWriter, r *http.Request) {
	res, e := c.App.Store.Resource(r.PathValue("id"))
	if e != nil || (res.Kind != "database" && res.Kind != "backup") {
		fail(w, 404, "下载资源不存在")
		return
	}
	transport, ok := c.App.Executor.(interface {
		Download(context.Context, model.Operation, http.ResponseWriter) error
	})
	if !ok {
		fail(w, 503, "下载执行器不可用")
		return
	}
	operation := res.Kind + ".download"
	if res.Kind == "database" {
		operation = "database.export"
	}
	c.App.Store.Audit(operation, res.ID, clientIP(r))
	if e = transport.Download(r.Context(), model.Operation{Name: operation, Resource: res, Params: model.Config{"artifact": r.URL.Query().Get("artifact")}}, w); e != nil {
		fail(w, 502, e.Error())
	}
}
