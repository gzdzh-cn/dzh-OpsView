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
}

func (a *App) Start() {
	_, _ = a.Store.DB.Exec(`UPDATE tasks SET state='interrupted',error='服务重启，请检查实际状态后重试',updated_at=? WHERE state IN ('queued','running')`, dao.Now())
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
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
	a.schedule = cron.New()
	a.schedule.Start()
	a.ReloadSchedules()
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
		_, e = a.schedule.AddFunc(r.Config.String("cron"), func() {
			target, e := a.Store.Resource(r.Config.String("targetId"))
			if e == nil {
				_, e = a.Queue(target, r.Config.String("operation"), model.Config{})
			}
			if e != nil {
				a.Store.Audit("schedule.failed:"+e.Error(), r.ID, "local")
			}
		})
		if e != nil {
			log.Printf("schedule %s: %v", r.ID, e)
		}
	}
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
	if p == nil {
		p = model.Config{}
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
		target, e := a.Store.Resource(r.Config.String("targetId"))
		if e != nil {
			return model.Task{}, errors.New("备份目标不存在")
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
	var data model.Config
	if op.Name == "panel.backup" {
		data, e = a.BackupPanel(ctx)
	} else {
		data, e = a.Executor.Execute(ctx, op, func(s string) { a.Store.Event(t.ID, security.MaskLog(s, op)) })
	}
	if e != nil {
		msg := security.MaskLog(e.Error(), op)
		state := "failed"
		if errors.Is(ctx.Err(), context.Canceled) {
			state = "cancelled"
		}
		_ = a.Store.Finish(t.ID, state, msg)
		a.Store.Event(t.ID, "失败："+msg)
		if r, err := a.Store.Resource(op.Resource.ID); err == nil && (strings.HasSuffix(op.Name, ".install") || strings.HasSuffix(op.Name, ".apply") || strings.HasSuffix(op.Name, ".start")) {
			r.Status = "error"
			_ = a.Store.Update(&r)
		}
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
	if er == nil && op.Name != "instance.rotate-log" && op.Name != "instance.delete-log-archive" {
		if strings.HasSuffix(op.Name, ".delete") || strings.HasSuffix(op.Name, ".uninstall") {
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
