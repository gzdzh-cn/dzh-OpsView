package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"opsview/internal/model"
	"strconv"
	"time"
)

// Read endpoints never write executor results into resource metadata or task logs.
func (c *Controller) softwareRead(w http.ResponseWriter, r *http.Request) {
	resource, err := c.App.Store.Resource(r.PathValue("id"))
	if err != nil || resource.Kind != "instance" {
		fail(w, 404, "软件实例不存在")
		return
	}
	view := r.PathValue("view")
	allowed := map[string]string{"settings": "inspect", "configuration": "configuration", "metrics": "metrics", "logs": "logs", "extensions": "extensions", "binlogs": "binlogs", "session": "session", "memory-budget": "memory-budget"}
	operation, ok := allowed[view]
	if !ok {
		fail(w, 404, "软件设置接口不存在")
		return
	}
	params := model.Config{}
	for _, key := range []string{"file", "revision", "kind", "search", "siteId", "budgetMB", "since", "until", "level", "archive", "limit"} {
		params[key] = r.URL.Query().Get(key)
	}
	if operation == "memory-budget" {
		if budget := r.URL.Query().Get("budgetMB"); budget != "" {
			n, err := strconv.Atoi(budget)
			if err != nil {
				fail(w, 422, "内存档位无效")
				return
			}
			params["budgetMB"] = n
		}
		if raw := r.URL.Query().Get("values"); raw != "" {
			values := map[string]string{}
			if len(raw) > 8192 || json.Unmarshal([]byte(raw), &values) != nil {
				fail(w, 422, "内存参数无效")
				return
			}
			params["values"] = values
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	data, err := c.App.Executor.Execute(ctx, model.Operation{Name: "instance." + operation, Resource: resource, Params: params}, func(string) {})
	if err != nil {
		fail(w, 422, err.Error())
		return
	}
	send(w, 200, data)
}
func (c *Controller) softwareValidate(w http.ResponseWriter, r *http.Request) {
	resource, err := c.App.Store.Resource(r.PathValue("id"))
	if err != nil || resource.Kind != "instance" {
		fail(w, 404, "软件实例不存在")
		return
	}
	var params model.Config
	if !decode(w, r, &params) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	data, err := c.App.Executor.Execute(ctx, model.Operation{Name: "instance.validate", Resource: resource, Params: params}, func(string) {})
	if err != nil {
		var conflict *model.RevisionConflict
		if errors.As(err, &conflict) {
			send(w, 409, model.Config{"error": err.Error(), "code": "revision_conflict", "revision": conflict.Revision})
			return
		}
		fail(w, 409, err.Error())
		return
	}
	send(w, 200, data)
}

func (c *Controller) siteRead(w http.ResponseWriter, r *http.Request) {
	resource, err := c.App.Store.Resource(r.PathValue("id"))
	if err != nil || resource.Kind != "site" {
		fail(w, 404, "网站不存在")
		return
	}
	view := r.PathValue("view")
	if view != "traffic" && view != "logs" {
		fail(w, 404, "网站接口不存在")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	data, err := c.App.Executor.Execute(ctx, model.Operation{Name: "site." + view, Resource: resource, Params: model.Config{}}, func(string) {})
	if err != nil {
		fail(w, 422, err.Error())
		return
	}
	send(w, 200, data)
}
