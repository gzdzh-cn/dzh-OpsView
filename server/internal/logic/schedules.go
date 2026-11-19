package logic

import (
	"context"
	"errors"
	"opsview/internal/model"
	"opsview/internal/runtimeconfig"
	"opsview/internal/scheduling"
)

func (a *App) executeSchedule(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	c := op.Resource.Config
	switch c.String("operation") {
	case "backup":
		results := []model.Config{}
		for _, id := range scheduling.TargetIDs(c) {
			if err := ctx.Err(); err != nil {
				return model.Config{"backups": results}, err
			}
			backup, err := a.Store.Resource(id)
			if err != nil || backup.Kind != "backup" || backup.Config.Bool("recycleOnly") {
				return model.Config{"backups": results}, errors.New("备份配置不可用")
			}
			data, err := a.Executor.Execute(ctx, model.Operation{Name: "backup.backup", Resource: backup, Params: model.Config{}}, log)
			if err != nil {
				return model.Config{"backups": results}, err
			}
			for k, v := range data {
				backup.Config[k] = v
			}
			backup.Status = "ready"
			if err = a.Store.Update(&backup); err != nil {
				return model.Config{"backups": results}, err
			}
			results = append(results, data)
		}
		return model.Config{"backups": results}, nil
	case "panel":
		return a.BackupPanel(ctx)
	case "site-state", "renew":
		if c.String("operation") == "renew" && c.String("targetId") == "panel" {
			cfg, err := runtimeconfig.Load(a.PanelConfigPath)
			if err != nil {
				return nil, err
			}
			if cfg.Protocol != "https" {
				return nil, errors.New("HTTPS 已关闭，证书续期已暂停")
			}
			return a.Executor.Execute(ctx, model.Operation{Name: "system.panel-certificate", Params: model.Config{"renew": true}}, log)
		}
		target, err := a.Store.Resource(c.String("targetId"))
		if err != nil || target.Kind != "site" {
			return nil, errors.New("网站目标不存在")
		}
		action := c.String("state")
		if action == "start" {
			action = "resume"
		}
		if c.String("operation") == "renew" {
			action = "renew"
		}
		data, err := a.Executor.Execute(ctx, model.Operation{Name: "site." + action, Resource: target, Params: model.Config{}}, log)
		if err == nil {
			if action == "stop" {
				target.Status = "stopped"
			} else {
				target.Status = "ready"
			}
			err = a.Store.Update(&target)
		}
		return data, err
	case "rotate":
		target, err := a.Store.Resource(c.String("targetId"))
		if err != nil || target.Kind != "site" {
			return nil, errors.New("网站目标不存在")
		}
		op.Params["target"] = target
	}
	return a.Executor.Execute(ctx, op, log)
}
