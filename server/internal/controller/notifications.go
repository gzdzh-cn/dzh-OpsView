package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"opsview/internal/model"
	"opsview/internal/notify"
	"opsview/internal/security"
	"regexp"
	"strconv"
	"time"
)

func (c *Controller) notificationAdmin(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	id := r.PathValue("id")
	resourceKind := "notification-channel"
	if kind == "rules" {
		resourceKind = "alert-rule"
	} else if kind == "records" {
		rows, err := c.App.Store.Notifications()
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		send(w, 200, rows)
		return
	} else if kind != "channels" {
		fail(w, 404, "通知入口不存在")
		return
	}
	if r.Method == "GET" {
		rows, err := c.App.Store.Resources(resourceKind)
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		for i := range rows {
			rows[i].Config = security.Redact(rows[i].Config)
			if rows[i].Config.String("url") != "" {
				rows[i].Config["url"] = "••••••••"
			}
		}
		send(w, 200, rows)
		return
	}
	if r.Method == "DELETE" {
		old, err := c.App.Store.Resource(id)
		if err != nil || old.Kind != resourceKind {
			fail(w, 404, "记录不存在")
			return
		}
		if resourceKind == "notification-channel" {
			rules, _ := c.App.Store.Resources("alert-rule")
			for _, rule := range rules {
				var ids []string
				b, _ := json.Marshal(rule.Config["channels"])
				_ = json.Unmarshal(b, &ids)
				for _, cid := range ids {
					if cid == id {
						fail(w, 409, "渠道仍被告警规则引用")
						return
					}
				}
			}
		}
		if err = c.App.Store.Delete(id); err != nil {
			fail(w, 409, err.Error())
			return
		}
		c.App.Store.Audit("notification.delete", id, clientIP(r))
		send(w, 200, model.Config{"ok": true})
		return
	}
	var b struct {
		Name     string       `json:"name"`
		Config   model.Config `json:"config"`
		Revision int          `json:"revision"`
	}
	if !decode(w, r, &b) {
		return
	}
	if len(b.Name) == 0 || len(b.Name) > 160 {
		fail(w, 400, "名称须为 1–160 字节")
		return
	}
	row := model.Resource{Kind: resourceKind, Name: b.Name, Config: b.Config, Status: "ready"}
	if row.Config == nil {
		fail(w, 400, "配置不能为空")
		return
	}
	if id != "" {
		old, err := c.App.Store.Resource(id)
		if err != nil || old.Kind != resourceKind {
			fail(w, 404, "记录不存在")
			return
		}
		if old.Revision != b.Revision {
			fail(w, 409, "记录已修改")
			return
		}
		row.ID = id
		row.Revision = b.Revision
		security.PreserveMasked(row.Config, old.Config)
		if row.Config.String("url") == "••••••••" {
			row.Config["url"] = old.Config["url"]
		}
	}
	if resourceKind == "notification-channel" {
		if err := notify.Validate(row.Config); err != nil {
			fail(w, 400, err.Error())
			return
		}
	} else {
		if !map[string]bool{"cpu": true, "memory": true, "disk": true, "inode": true, "service": true, "website": true, "certificate": true, "task-failed": true, "backup-failed": true, "login-failed": true}[row.Config.String("type")] {
			fail(w, 400, "告警类型无效")
			return
		}
		if row.Config.Int("threshold") < 0 || row.Config.Int("threshold") > 365 || row.Config.Int("samples") < 1 || row.Config.Int("samples") > 60 || row.Config.Int("cooldownMinutes") < 1 || row.Config.Int("cooldownMinutes") > 1440 {
			fail(w, 400, "阈值、连续次数或冷却时间无效")
			return
		}
		for _, k := range []string{"quietStart", "quietEnd"} {
			if row.Config.String(k) != "" && !regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`).MatchString(row.Config.String(k)) {
				fail(w, 400, "静默时间使用 HH:mm")
				return
			}
		}
		kind := row.Config.String("type")
		if kind == "service" || kind == "website" || kind == "certificate" {
			target, err := c.App.Store.Resource(row.Config.String("targetId"))
			if err != nil || kind == "service" && target.Kind != "instance" || kind != "service" && target.Kind != "site" {
				fail(w, 400, "告警对象类型不匹配")
				return
			}
		}
		if (kind == "disk" || kind == "inode") && row.Config.String("path") == "" {
			row.Config["path"] = "/"
		}
		if (kind == "cpu" || kind == "memory" || kind == "disk" || kind == "inode") && row.Config.Int("threshold") > 100 {
			fail(w, 400, "使用率阈值不能大于 100%")
			return
		}
		var ids []string
		raw, _ := json.Marshal(row.Config["channels"])
		if json.Unmarshal(raw, &ids) != nil || len(ids) == 0 {
			fail(w, 400, "至少选择一个通知渠道")
			return
		}
		for _, cid := range ids {
			ch, err := c.App.Store.Resource(cid)
			if err != nil || ch.Kind != "notification-channel" {
				fail(w, 400, "通知渠道不存在")
				return
			}
		}
	}
	var err error
	if id == "" {
		err = c.App.Store.Create(&row)
		if err == nil && resourceKind == "alert-rule" {
			var cursor int64
			_ = c.App.Store.DB.QueryRow(`SELECT COALESCE(MAX(id),0) FROM audit`).Scan(&cursor)
			err = c.App.Store.Set("alert-state:"+row.ID, model.Config{"eventCursor": strconv.FormatInt(cursor, 10)})
		}
	} else {
		err = c.App.Store.Update(&row)
	}
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	c.App.Store.Audit("notification.save", row.ID, clientIP(r))
	row.Config = security.Redact(row.Config)
	send(w, 200, row)
}
func (c *Controller) testNotification(w http.ResponseWriter, r *http.Request) {
	row, err := c.App.Store.Resource(r.PathValue("id"))
	if err != nil || row.Kind != "notification-channel" {
		fail(w, 404, "渠道不存在")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	err = notify.Send(ctx, row.Config, "OpsView 测试通知", "通知渠道连接测试，不包含服务器凭据。")
	state := "succeeded"
	if err != nil {
		state = "failed"
	}
	cfg := model.Config{"channelId": row.ID, "title": "渠道测试"}
	if err != nil {
		cfg["error"] = security.MaskLog(err.Error(), row.Config)
	}
	_ = c.App.Store.Notification("test", state, cfg)
	c.App.Store.Audit("notification.test", row.ID, clientIP(r))
	if err != nil {
		fail(w, 502, cfg.String("error"))
		return
	}
	send(w, 200, model.Config{"ok": true})
}
