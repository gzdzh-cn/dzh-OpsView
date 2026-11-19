package controller

import (
	"github.com/robfig/cron/v3"
	"net/http"
	"opsview/internal/model"
	"opsview/internal/security"
	"time"
)

func (c *Controller) schedules(w http.ResponseWriter, r *http.Request) {
	rows, err := c.App.Store.Resources("schedule")
	if err != nil {
		fail(w, 500, "读取计划失败")
		return
	}
	out := []model.Config{}
	now := time.Now()
	for _, row := range rows {
		next := ""
		reason := ""
		parsed, err := cron.ParseStandard(row.Config.String("cron"))
		if err != nil {
			reason = "周期无效"
		} else if row.Config.Bool("enabled") {
			next = parsed.Next(now).Format(time.RFC3339)
		}
		target, err := c.App.Store.Resource(row.Config.String("targetId"))
		if err != nil {
			reason = "目标不存在"
			next = ""
		}
		out = append(out, model.Config{"resource": model.Config{"id": row.ID, "kind": row.Kind, "name": row.Name, "status": row.Status, "revision": row.Revision, "config": security.Redact(row.Config)}, "targetName": target.Name, "nextRun": next, "reason": reason})
	}
	zone, _ := now.Zone()
	send(w, 200, model.Config{"rows": out, "timezone": time.Local.String(), "zone": zone, "serverTime": now.Format(time.RFC3339)})
}
