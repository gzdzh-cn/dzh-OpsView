package controller

import (
	"net/http"
	"opsview/internal/model"
)

func (c *Controller) scheduleCategories(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		rows, err := c.App.Store.ScheduleCategories()
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		send(w, 200, rows)
		return
	}
	var b struct {
		Old    string `json:"old"`
		Name   string `json:"name"`
		Remove bool   `json:"remove"`
	}
	if !decode(w, r, &b) {
		return
	}
	if err := c.App.Store.ChangeScheduleCategory(b.Old, b.Name, b.Remove); err != nil {
		fail(w, 409, err.Error())
		return
	}
	c.App.ReloadSchedules()
	c.App.Store.Audit("schedule.category", "", clientIP(r))
	send(w, 200, model.Config{"ok": true})
}
