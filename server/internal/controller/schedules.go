package controller

import (
	"net/http"
	"opsview/internal/model"
	"opsview/internal/scheduling"
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
		parsed, err := scheduling.Cron(row.Config)
		if err != nil {
			reason = "周期无效"
		} else if row.Config.Bool("enabled") {
			next = parsed.Next(now).Format(time.RFC3339)
		}
		targetID := row.Config.String("targetId")
		if row.Config.String("operation") == "backup" {
			ids := scheduling.TargetIDs(row.Config)
			if len(ids) > 0 {
				targetID = ids[0]
			}
		}
		target, err := c.App.Store.Resource(targetID)
		if err != nil && (row.Config.String("operation") == "backup" || row.Config.String("operation") == "renew" || row.Config.String("operation") == "rotate" || row.Config.String("operation") == "site-state") {
			reason = "目标不存在"
			next = ""
		}
		if row.Config.String("operation") == "renew" && targetID == "panel" {
			reason = ""
			target.Name = "面板证书"
			if parsed != nil && row.Config.Bool("enabled") {
				next = parsed.Next(now).Format(time.RFC3339)
			}
		}
		last := model.Config{}
		var tid, state, created string
		if c.App.Store.DB.QueryRow(`SELECT tasks.id,state,tasks.created_at FROM schedule_runs JOIN tasks ON tasks.id=schedule_runs.task_id WHERE schedule_id=? ORDER BY tasks.created_at DESC LIMIT 1`, row.ID).Scan(&tid, &state, &created) == nil {
			last = model.Config{"id": tid, "state": state, "createdAt": created}
		}
		out = append(out, model.Config{"lastRun": last, "resource": model.Config{"id": row.ID, "kind": row.Kind, "name": row.Name, "status": row.Status, "revision": row.Revision, "config": scheduling.Public(row.Config)}, "targetName": target.Name, "nextRun": next, "reason": reason})
	}
	zone, _ := now.Zone()
	send(w, 200, model.Config{"rows": out, "timezone": time.Local.String(), "zone": zone, "serverTime": now.Format(time.RFC3339)})
}
