package controller

import (
	"net/http"
	"opsview/internal/model"
	"opsview/internal/scheduling"
)

func (c *Controller) importSchedules(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Schedules []model.Resource `json:"schedules"`
		Preview   bool             `json:"preview"`
	}
	if !decode(w, r, &b) {
		return
	}
	if len(b.Schedules) == 0 || len(b.Schedules) > 100 {
		fail(w, 400, "每次导入 1–100 条计划")
		return
	}
	existing, _ := c.App.Store.Resources("schedule")
	names := map[string]bool{}
	for _, v := range existing {
		names[v.Name] = true
	}
	rows := []model.Resource{}
	for _, v := range b.Schedules {
		if names[v.Name] {
			fail(w, 409, "计划名称重复："+v.Name)
			return
		}
		names[v.Name] = true
		v.ID = ""
		v.Kind = "schedule"
		v.Status = "ready"
		v.Revision = 0
		if v.Config == nil {
			fail(w, 400, "缺少计划配置")
			return
		}
		v.Config["enabled"] = false
		for _, key := range []string{"rootApproval", "environment", "headers", "body"} {
			delete(v.Config, key)
		}
		if !c.prepareSchedule(w, r, &v, model.Config{}) {
			return
		}
		if err := scheduling.Validate(v.Config); err != nil {
			fail(w, 400, err.Error())
			return
		}
		if err := c.validate(&v); err != nil {
			fail(w, 400, err.Error())
			return
		}
		rows = append(rows, v)
	}
	if b.Preview {
		out := []model.Config{}
		for _, v := range rows {
			out = append(out, model.Config{"name": v.Name, "config": scheduling.Public(v.Config), "requiresRootApproval": v.Config.String("user") == "root"})
		}
		send(w, 200, model.Config{"rows": out})
		return
	}
	if err := c.App.Store.CreateSchedules(rows); err != nil {
		fail(w, 409, err.Error())
		return
	}
	c.App.Store.Audit("schedules.import", "", clientIP(r))
	c.App.ReloadSchedules()
	send(w, 201, model.Config{"count": len(rows), "paused": true})
}
