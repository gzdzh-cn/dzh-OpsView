package logic

import (
	"context"
	"fmt"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"opsview/internal/model"
	"opsview/internal/notify"
	"opsview/internal/preferences"
	"opsview/internal/security"
	"strconv"
	"time"
)

func (a *App) evaluateAlerts(ctx context.Context, eventsOnly bool) {
	rules, _ := a.Store.Resources("alert-rule")
	for _, rule := range rules {
		if !rule.Config.Bool("enabled") {
			continue
		}
		c := rule.Config
		eventType := c.String("type") == "task-failed" || c.String("type") == "backup-failed" || c.String("type") == "login-failed"
		if eventType != eventsOnly {
			continue
		}
		state := model.Config{}
		_ = a.Store.Setting("alert-state:"+rule.ID, &state)
		trigger, available, message := a.alertCondition(ctx, rule, state)
		if !available {
			continue
		}
		count := state.Int("count")
		if trigger {
			count++
		} else {
			count = 0
		}
		state["count"] = count
		required := c.Int("samples")
		if required < 1 {
			required = 3
		}
		if c.String("type") == "task-failed" || c.String("type") == "backup-failed" || c.String("type") == "login-failed" {
			required = 1
		}
		quiet := false
		if start, end := c.String("quietStart"), c.String("quietEnd"); start != "" && end != "" {
			prefs := model.Config{}
			_ = a.Store.Setting("panel", &prefs)
			location, err := time.LoadLocation(preferences.Merge(prefs).String("timezone"))
			if err != nil {
				location = time.Local
			}
			now := time.Now().In(location).Format("15:04")
			if start < end {
				quiet = now >= start && now < end
			} else {
				quiet = now >= start || now < end
			}
		}
		cooldown := c.Int("cooldownMinutes")
		if cooldown < 1 {
			cooldown = 15
		}
		due := int(time.Now().Unix())-state.Int("lastSent") >= cooldown*60
		title := "OpsView 告警：" + rule.Name
		send := trigger && count >= required && due
		if !trigger && !eventType && state.Bool("active") && c.Bool("recovery") {
			send = true
			title = "OpsView 恢复：" + rule.Name
			message = "条件已恢复正常"
		}
		if send && !quiet {
			for _, id := range stringSlice(c["channels"]) {
				_ = a.Store.QueueNotification(rule.ID, id, title, message)
			}
			state["lastSent"] = time.Now().Unix()
			state["active"] = trigger
		}
		_ = a.Store.Set("alert-state:"+rule.ID, state)
	}
}
func stringSlice(v any) []string {
	out := []string{}
	if values, ok := v.([]any); ok {
		for _, x := range values {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	if values, ok := v.([]string); ok {
		return values
	}
	return out
}
func (a *App) alertCondition(ctx context.Context, rule model.Resource, state model.Config) (bool, bool, string) {
	c := rule.Config
	threshold := float64(c.Int("threshold"))
	kind := c.String("type")
	var value float64
	switch kind {
	case "cpu":
		v, err := cpu.PercentWithContext(ctx, 0, false)
		if err != nil || len(v) == 0 {
			return false, false, ""
		}
		value = v[0]
	case "memory":
		v, err := mem.VirtualMemoryWithContext(ctx)
		if err != nil {
			return false, false, ""
		}
		value = v.UsedPercent
	case "disk", "inode":
		v, err := disk.UsageWithContext(ctx, func() string {
			if c.String("path") != "" {
				return c.String("path")
			}
			return "/"
		}())
		if err != nil {
			return false, false, ""
		}
		value = v.UsedPercent
		if kind == "inode" {
			value = v.InodesUsedPercent
		}
	case "certificate":
		target, err := a.Store.Resource(c.String("targetId"))
		if err != nil {
			return false, false, ""
		}
		until, err := time.Parse(time.RFC3339, target.Config.String("certificateExpires"))
		if err != nil {
			return false, false, ""
		}
		days := time.Until(until).Hours() / 24
		return days <= threshold, true, fmt.Sprintf("证书剩余 %.1f 天", days)
	case "service", "website":
		target, err := a.Store.Resource(c.String("targetId"))
		if err != nil {
			return false, false, ""
		}
		probe, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		data, err := a.Executor.Execute(probe, model.Operation{Name: "schedule.probe", Params: model.Config{"type": kind, "target": target}}, func(string) {})
		if err != nil {
			return false, false, ""
		}
		return !data.Bool("healthy"), true, "受管目标健康检查异常：" + target.Name
	case "task-failed", "backup-failed", "login-failed":
		last, _ := strconv.ParseInt(state.String("eventCursor"), 10, 64)
		var id int64
		var count int
		pattern := "task.failed:%"
		if kind == "backup-failed" {
			pattern = "%"
		}
		if kind == "login-failed" {
			pattern = "auth.failed"
		}
		query := `SELECT COALESCE(MAX(id),0),COUNT(*) FROM audit WHERE id>? AND action LIKE ?`
		args := []any{last, pattern}
		if kind == "backup-failed" {
			query += ` AND (action LIKE 'task.failed:backup.%' OR action='task.failed:panel.backup' OR action='backup.failed')`
		}
		if err := a.Store.DB.QueryRow(query, args...).Scan(&id, &count); err != nil {
			return false, false, ""
		}
		if count == 0 {
			return false, true, ""
		}
		state["eventCursor"] = strconv.FormatInt(id, 10)
		message := "任务失败，请查看任务中心"
		if kind == "login-failed" {
			message = "检测到异常登录，请查看安全审计"
		}
		return true, true, fmt.Sprintf("%s（新增 %d 次）", message, count)
	default:
		return false, false, ""
	}
	return value >= threshold, true, fmt.Sprintf("当前使用率 %.1f%%，阈值 %.0f%%", value, threshold)
}
func (a *App) deliverNotifications(ctx context.Context) {
	events, _ := a.Store.PendingNotifications()
	sent := 0
	for _, event := range events {
		if event.String("state") != "queued" || event.Int("nextAt") > int(time.Now().Unix()) {
			continue
		}
		if sent >= 10 {
			break
		}
		sent++
		channel, err := a.Store.Resource(event.String("channelId"))
		if err != nil || channel.Kind != "notification-channel" || !channel.Config.Bool("enabled") {
			event["error"] = "通知渠道不存在或已暂停"
			_ = a.Store.UpdateNotification(event.String("id"), "failed", event)
			continue
		}
		sendCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		err = notify.Send(sendCtx, channel.Config, event.String("title"), event.String("message"))
		cancel()
		event["attempts"] = event.Int("attempts") + 1
		status := "succeeded"
		delete(event, "error")
		if err != nil {
			event["error"] = security.MaskLog(err.Error(), channel.Config)
			if event.Int("attempts") < 3 {
				status = "queued"
				event["nextAt"] = time.Now().Add(time.Duration(event.Int("attempts")) * time.Minute).Unix()
			} else {
				status = "failed"
			}
		}
		_ = a.Store.UpdateNotification(event.String("id"), status, event)
	}
}
