package logic

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/robfig/cron/v3"
	"log"
	"opsview/internal/dao"
	"opsview/internal/model"
	"opsview/internal/scheduling"
	"opsview/internal/security"
	"opsview/internal/service"
	"strings"
	"sync"
	"time"
)

type App struct {
	Store           *dao.Store
	Executor        service.Executor
	PanelDir        string
	PanelConfigPath string
	Root            string
	Development     bool
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	taskMu          sync.Mutex
	taskCancels     map[string]context.CancelFunc
	schedule        *cron.Cron
	scheduleMu      sync.Mutex
}

func (a *App) Start() {
	_, _ = a.Store.DB.Exec(`UPDATE tasks SET state='interrupted',error='服务重启，请检查实际状态后重试',updated_at=? WHERE state IN ('queued','running')`, dao.Now())
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	for worker := 0; worker < 4; worker++ {
		a.wg.Add(1)
		go func() {
			defer a.wg.Done()
			tick := time.NewTicker(500 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					a.runNext(ctx)
				}
			}
		}()
	}
	a.schedule = cron.New()
	a.schedule.Start()
	a.ReloadSchedules()
	a.startMaintenance(ctx)
	instances, _ := a.Store.Resources("instance")
	for _, i := range instances {
		if i.Config.String("software") == "mysql" || i.Config.String("software") == "postgresql" {
			_, _ = a.Store.Enqueue(model.Operation{Name: "dbadmin.reconcile", Resource: i, Params: model.Config{}})
		}
	}
}
func (a *App) Stop() {
	if a.schedule != nil {
		<-a.schedule.Stop().Done()
	}
	if a.cancel != nil {
		a.cancel()
	}
	a.wg.Wait()
}
func (a *App) ReloadSchedules() {
	a.scheduleMu.Lock()
	defer a.scheduleMu.Unlock()
	if a.schedule == nil {
		return
	}
	for _, e := range a.schedule.Entries() {
		a.schedule.Remove(e.ID)
	}
	rs, e := a.Store.Resources("schedule")
	if e != nil {
		return
	}
	for _, r := range rs {
		r := r
		if !r.Config.Bool("enabled") {
			continue
		}
		expr := r.Config.String("cron")
		if r.Config.String("timezone") != "" {
			expr = "CRON_TZ=" + r.Config.String("timezone") + " " + expr
		}
		_, e = a.schedule.AddFunc(expr, func() {
			_, e := a.QueueSchedule(r.ID, true)
			if e != nil {
				a.Store.Audit("schedule.skipped", r.ID, "local")
				_ = a.Store.RecordSkippedSchedule(r.ID, "计划冲突或不可执行，请检查关联资源与任务授权")
			}
		})
		if e != nil {
			log.Printf("schedule %s: %v", r.ID, e)
		}
	}
	_, _ = a.schedule.AddFunc("41 * * * *", a.queueFirewallCountryUpdate)

	_, _ = a.schedule.AddFunc("23 * * * *", func() {
		databases, err := a.Store.Resources("database")
		if err != nil {
			return
		}
		for _, db := range databases {
			expires, err := time.Parse(time.RFC3339, db.Config.String("expiresAt"))
			if db.Status == "recycled" && err == nil && time.Now().After(expires) {
				_, _ = a.Store.Enqueue(model.Operation{Name: "dbadmin.purge", Resource: db, Params: model.Config{"confirmed": true}})
			}
		}
	})
	_, _ = a.schedule.AddFunc("17 * * * *", func() {
		sites, err := a.Store.Resources("site")
		if err != nil {
			return
		}
		for _, site := range sites {
			if site.Config.String("certificateProvider") != "acme" {
				continue
			}
			expires, err := time.Parse(time.RFC3339, site.Config.String("certificateExpires"))
			threshold := 30 * 24 * time.Hour
			if start, er := time.Parse(time.RFC3339, site.Config.String("certificateNotBefore")); er == nil && expires.Sub(start) > 0 && expires.Sub(start)/3 < threshold {
				threshold = expires.Sub(start) / 3
			}
			if err == nil && time.Until(expires) < threshold {
				_, _ = a.Queue(site, "renew", model.Config{})
			}
		}
	})
}
func (a *App) Queue(r model.Resource, action string, p model.Config) (model.Task, error) {
	if r.Kind == "app" {
		return model.Task{}, errors.New("应用进程功能已下线，已有服务与数据保留")
	}
	if r.Kind == "instance" && action == "install" && (r.Status == "error" || r.Status == "uninstalled" || r.Config.Bool("retainedData")) {
		return model.Task{}, errors.New("失败或已卸载的实例仍保留版本占用，请卸载并明确清理记录后重新安装")
	}
	if p == nil {
		p = model.Config{}
	}
	if r.Kind == "database" {
		if r.Status == "recycled" {
			return model.Task{}, errors.New("数据库已回收")
		}
		if action == "delete" {
			return a.Store.Enqueue(model.Operation{Name: "dbadmin.recycle", Resource: r, Params: p})
		}
		if action == "access" {
			return model.Task{}, errors.New("请使用数据库专用权限或密码操作")
		}
	}
	allowed := map[string]map[string]bool{
		"site": {"apply": true, "delete": true, "renew": true, "certificate": true, "configuration": true, "logs": true, "stop": true, "resume": true}, "instance": {"install": true, "start": true, "stop": true, "restart": true, "uninstall": true, "reload": true, "configure": true, "guardian": true, "extension": true, "port": true, "snapshot": true, "purge-binlogs": true, "rotate-log": true, "delete-log-archive": true, "move-storage": true, "switch-version": true, "session": true}, "database": {"create": true, "access": true, "delete": true, "import": true, "migrate": true}, "app": {"apply": true, "start": true, "stop": true, "restart": true, "delete": true, "logs": true}, "compose": {"apply": true, "start": true, "stop": true, "delete": true, "logs": true}, "firewall": {"apply": true, "confirm": true, "delete": true}, "backup": {"backup": true, "restore": true, "delete": true}, "plugin": {"install": true, "check": true, "uninstall": true}}
	if !allowed[r.Kind][action] {
		return model.Task{}, errors.New("该资源不支持此操作")
	}
	if action == "uninstall" || action == "delete" {
		all, e := a.Store.Resources("")
		if e != nil {
			return model.Task{}, e
		}
		for _, x := range all {
			if x.ID == r.ID {
				continue
			}
			for _, id := range scheduling.TargetIDs(x.Config) {
				if id == r.ID {
					return model.Task{}, fmt.Errorf("资源仍被 %s 使用", x.Name)
				}
			}
			if x.Config.String("storageId") == r.ID {
				return model.Task{}, fmt.Errorf("存储仍被 %s 使用", x.Name)
			}
			for _, k := range []string{"instanceId", "targetId", "phpInstanceId", "appId", "sessionRedisInstanceId"} {
				if x.Config.String(k) == r.ID {
					return model.Task{}, fmt.Errorf("资源仍被 %s 使用", x.Name)
				}
			}
		}
	}
	if r.Kind == "site" || r.Kind == "database" {
		i, e := a.Store.Resource(r.Config.String("instanceId"))
		if e != nil {
			return model.Task{}, errors.New("关联实例不存在")
		}
		if r.Kind == "site" && i.Config.String("software") != "nginx" {
			return model.Task{}, errors.New("网站必须关联 Nginx")
		}
		if r.Kind == "database" && i.Config.String("software") != "mysql" && i.Config.String("software") != "postgresql" {
			return model.Task{}, errors.New("请选择数据库实例")
		}
		p["instance"] = i
	}
	if r.Kind == "backup" {
		if r.Config.Bool("recycleOnly") {
			return model.Task{}, errors.New("回收备份只能从回收站管理")
		}
		target, e := a.Store.Resource(r.Config.String("targetId"))
		if e != nil {
			return model.Task{}, errors.New("备份目标不存在")
		}
		if target.Status == "recycled" {
			return model.Task{}, errors.New("备份目标已回收，请使用回收站恢复")
		}
		if target.Kind != "site" && target.Kind != "app" && target.Kind != "database" && !(target.Kind == "instance" && target.Config.String("software") == "redis" && target.Config.String("mode") == "native") {
			return model.Task{}, errors.New("仅支持网站、应用、数据库和原生 Redis 备份")
		}
		p["target"] = target
		if target.Kind == "database" {
			inst, e := a.Store.Resource(target.Config.String("instanceId"))
			if e != nil {
				return model.Task{}, e
			}
			p["instance"] = inst
		}
	}
	return a.Store.Enqueue(model.Operation{Name: r.Kind + "." + action, Resource: r, Params: p})
}
func (a *App) runNext(ctx context.Context) {
	a.taskMu.Lock()
	t, op, e := a.Store.Claim()
	taskCtx, cancel := context.WithCancel(ctx)
	if e == nil {
		if a.taskCancels == nil {
			a.taskCancels = map[string]context.CancelFunc{}
		}
		a.taskCancels[t.ID] = cancel
	}
	a.taskMu.Unlock()
	defer func() { cancel(); a.taskMu.Lock(); delete(a.taskCancels, t.ID); a.taskMu.Unlock() }()
	ctx = taskCtx
	if errors.Is(e, sql.ErrNoRows) {
		return
	}
	if e != nil {
		log.Printf("claim task: %v", e)
		return
	}
	a.Store.Event(t.ID, "任务开始："+op.Name)
	logBytes := 0
	truncated := false
	emit := func(line string) {
		if truncated {
			return
		}
		if strings.HasPrefix(line, "[OpsView 日志已截断]") {
			_ = a.Store.Set("task-log-truncated:"+t.ID, true)
		}
		line = security.MaskLog(line, op)
		logBytes += len(line)
		if logBytes > 4<<20 {
			truncated = true
			a.Store.Event(t.ID, "日志达到 4 MiB 上限，后续输出已截断")
			return
		}
		a.Store.Event(t.ID, line)
	}
	defer func() {
		if truncated {
			_ = a.Store.Set("task-log-truncated:"+t.ID, true)
		}
	}()
	var data model.Config
	if op.Name == "schedule.execute" {
		data, e = a.executeSchedule(ctx, op, emit)
		if data != nil {
			_ = a.Store.SaveTaskResult(t.ID, data)
		}
	} else if strings.HasPrefix(op.Name, "panel.archive-") {
		data, e = a.PanelArchive(ctx, op.Params, strings.TrimPrefix(op.Name, "panel.archive-"))
	} else if op.Name == "panel.backup" {
		data, e = a.BackupPanel(ctx)
	} else {
		data, e = a.Executor.Execute(ctx, op, emit)
	}
	if data != nil {
		_ = a.Store.SaveTaskResult(t.ID, data)
	}
	if e != nil {
		msg := security.MaskLog(e.Error(), op)
		state := "failed"
		if errors.Is(ctx.Err(), context.Canceled) {
			state = "cancelled"
		}
		_ = a.Store.Finish(t.ID, state, msg)
		if strings.HasPrefix(op.Name, "files.") {
			a.auditFileTask(op, state)
		}
		a.Store.Event(t.ID, "失败："+msg)
		if r, err := a.Store.Resource(op.Resource.ID); err == nil && (strings.HasSuffix(op.Name, ".install") || strings.HasSuffix(op.Name, ".apply") || strings.HasSuffix(op.Name, ".start")) {
			r.Status = "error"
			_ = a.Store.Update(&r)
		}
		return
	}
	if strings.HasPrefix(op.Name, "archive.") || strings.HasPrefix(op.Name, "panel.archive-") || op.Name == "system.panel-region-update" {
		_ = a.Store.SaveTaskResult(t.ID, data)
		_ = a.Store.Finish(t.ID, "succeeded", "")
		a.Store.Event(t.ID, "归档恢复完成")
		return
	}
	if op.Name == "schedule.execute" {
		_ = a.Store.Finish(t.ID, "succeeded", "")
		a.Store.Event(t.ID, "任务完成")
		return
	}
	if strings.HasPrefix(op.Name, "dbadmin.") {
		var resources []model.Resource
		raw, _ := json.Marshal(data["resources"])
		_ = json.Unmarshal(raw, &resources)
		if e = a.Store.ApplyDatabaseResources(resources); e == nil {
			var ids []string
			raw, _ = json.Marshal(data["deletedIds"])
			_ = json.Unmarshal(raw, &ids)
			for _, id := range ids {
				if e = a.Store.Delete(id); e != nil {
					break
				}
			}
		}
		if e != nil {
			_ = a.Store.Finish(t.ID, "failed", "实际操作已完成，元数据写入失败，请同步检查")
			return
		}
		_ = a.Store.Finish(t.ID, "succeeded", "")
		a.Store.Event(t.ID, "任务完成")
		return
	}
	if strings.HasPrefix(op.Name, "files.") {
		if e = a.Store.Set("task-result:"+t.ID, data); e != nil {
			_ = a.Store.Finish(t.ID, "failed", "文件操作完成，但结果保存失败")
			return
		}
		_ = a.Store.Finish(t.ID, "succeeded", "")
		a.auditFileTask(op, "succeeded")
		a.Store.Event(t.ID, "任务完成")
		return
	}
	if linked, ok := data["linkedResources"]; ok {
		raw, _ := json.Marshal(linked)
		var resources []model.Resource
		if json.Unmarshal(raw, &resources) != nil {
			_ = a.Store.Finish(t.ID, "failed", "关联资源记录无效")
			return
		}
		for _, resource := range resources {
			if resource.Kind != "site" && resource.Kind != "instance" {
				continue
			}
			current, er := a.Store.Resource(resource.ID)
			if er != nil {
				_ = a.Store.Finish(t.ID, "failed", "关联记录不存在，请核对实际入口")
				return
			}
			resource.Revision = current.Revision
			if er = a.Store.Update(&resource); er != nil {
				_ = a.Store.Finish(t.ID, "failed", "入口已切换，关联记录更新失败，请核对实际状态")
				return
			}
		}
		delete(data, "linkedResources")
	}
	for _, linkKey := range []string{"linkedFirewall", "linkedDatabase"} {
		if linked, ok := data[linkKey]; ok {
			b, _ := json.Marshal(linked)
			var linkedResource model.Resource
			if json.Unmarshal(b, &linkedResource) == nil && (linkedResource.Kind == "firewall" || linkedResource.Kind == "database") {

				if existing, err := a.Store.Resource(linkedResource.ID); err == nil {
					linkedResource.Revision = existing.Revision
					if err = a.Store.Update(&linkedResource); err != nil {
						_ = a.Store.Finish(t.ID, "failed", "关联资源已创建，面板记录更新失败")
						return
					}
				} else if errors.Is(err, sql.ErrNoRows) {
					if err = a.Store.Create(&linkedResource); err != nil {
						_ = a.Store.Finish(t.ID, "failed", "关联资源已创建，但面板记录失败，请检查实际状态")
						return
					}
				}
			}
		}
	}
	r, er := a.Store.Resource(op.Resource.ID)
	// Session changes persist only their explicitly linked instance/site. Cleanup
	// and site-scoped changes must not mark the whole PHP version running or
	// replace its configuration revision with a site's revision.
	if er == nil && op.Name != "instance.rotate-log" && op.Name != "instance.delete-log-archive" && op.Name != "instance.session" {
		if strings.HasSuffix(op.Name, ".uninstall") && r.Kind == "instance" {
			r.Status = "uninstalled"
			r.Config["retainedData"] = true
			er = a.Store.Update(&r)
		} else if strings.HasSuffix(op.Name, ".delete") || strings.HasSuffix(op.Name, ".uninstall") {
			er = a.Store.Delete(r.ID)
		} else {
			if r.Config == nil {
				r.Config = model.Config{}
			}
			for k, v := range data {
				r.Config[k] = v
			}
			r.Status = "ready"
			if status, ok := data["runtimeStatus"].(string); ok && (status == "stopped" || status == "expired") {
				r.Status = status
			}
			if strings.HasSuffix(op.Name, ".stop") && op.Params.String("siteId") == "" {
				r.Status = "stopped"
			}
			er = a.Store.Update(&r)
		}
	}
	if er != nil && !errors.Is(er, sql.ErrNoRows) {
		_ = a.Store.Finish(t.ID, "failed", "操作已执行，但记录更新失败："+er.Error())
		return
	}
	_ = a.Store.Finish(t.ID, "succeeded", "")
	a.Store.Event(t.ID, "任务完成")
}

func (a *App) auditFileTask(op model.Operation, result string) {
	paths := []string{}
	if path := op.Params.String("path"); path != "" {
		paths = append(paths, path)
	}
	if batch, ok := op.Params["paths"].([]any); ok {
		for _, v := range batch {
			if path, ok := v.(string); ok {
				paths = append(paths, path)
			}
		}
	}
	if len(paths) == 0 {
		paths = []string{""}
	}
	for _, path := range paths {
		a.Store.Audit(op.Name+":"+result+":"+path, "files-host", "task")
	}
}

func (a *App) queueFirewallCountryUpdate() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	status, err := a.Executor.Execute(ctx, model.Operation{Name: "system.firewall-admin-country-status"}, func(string) {})
	if err != nil || !status.Bool("due") {
		return
	}
	_, _ = a.Store.Enqueue(model.Operation{Name: "system.firewall-admin-region-update", Resource: model.Resource{ID: "system-security", Kind: "system", Name: "地区数据自动更新"}, Params: model.Config{}})
}

// Resolve fresh settings when the timer fires; paused or deleted schedules cannot
// dispatch stale work. The target's existing queue lock prevents overlapping runs.
func (a *App) QueueSchedule(id string, automatic bool) (model.Task, error) {
	r, err := a.Store.Resource(id)
	if err != nil || r.Kind != "schedule" {
		return model.Task{}, errors.New("计划任务不存在")
	}
	if automatic && !r.Config.Bool("enabled") {
		return model.Task{}, errors.New("计划任务已暂停")
	}
	if err = Validate(r); err != nil {
		return model.Task{}, err
	}
	if (r.Config.String("operation") == "shell" || r.Config.String("operation") == "python") && r.Config.String("user") == "root" && r.Config.String("rootApproval") != scheduling.Digest(r.Config) {
		return model.Task{}, errors.New("root 任务需要重新确认管理员身份")
	}
	if (r.Config.String("operation") != "backup" && r.Config.String("operation") != "renew") || len(scheduling.TargetIDs(r.Config)) > 1 || (r.Config.String("operation") == "renew" && r.Config.String("targetId") == "panel") {
		return a.Store.Enqueue(model.Operation{Name: "schedule.execute", Resource: r, Params: model.Config{"scheduleId": r.ID}})
	}
	target, err := a.Store.Resource(r.Config.String("targetId"))
	if err != nil {
		return model.Task{}, errors.New("计划目标不存在")
	}
	if (r.Config.String("operation") == "backup" && target.Kind != "backup") || (r.Config.String("operation") == "renew" && target.Kind != "site") {
		return model.Task{}, errors.New("计划类型与目标不匹配")
	}
	return a.Queue(target, r.Config.String("operation"), model.Config{"scheduleId": r.ID})
}
