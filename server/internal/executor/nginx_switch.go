package executor

import (
	"context"
	"errors"
	"fmt"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
)

func (e *Engine) switchNginx(ctx context.Context, source model.Resource, p model.Config, log func(string)) (model.Config, error) {
	target, err := e.load(p.String("targetInstanceId"))
	if err != nil {
		return nil, err
	}
	if source.ID == target.ID || source.Config.String("software") != "nginx" || target.Config.String("software") != "nginx" || source.Config.String("mode") != "native" || target.Config.String("mode") != "native" || target.Status != "stopped" {
		return nil, errors.New("请选择停止中的另一原生 Nginx 版本")
	}
	if source.Config.Int("port") != target.Config.Int("port") {
		return nil, errors.New("两个入口实例必须使用相同网站端口")
	}
	if !p.Bool("maintenanceConfirmed") {
		return nil, errors.New("版本切换需要确认短暂维护窗口")
	}
	sites := []model.Resource{}
	for _, r := range e.states() {
		if r.Kind == "site" && r.Config.String("instanceId") == target.ID {
			return nil, errors.New("目标实例已有站点，不能覆盖")
		}
		if r.Kind == "site" && r.Config.String("instanceId") == source.ID {
			sites = append(sites, r)
		}
	}
	targetBase := filepath.Join(e.Root, "instances", target.ID)
	paths := []string{}
	committed := false
	snapshots := map[string][]byte{}
	for _, original := range append(append([]model.Resource{}, sites...), source, target) {
		path := filepath.Join(e.Root, "state", original.ID+".json")
		b, er := os.ReadFile(path)
		if er != nil {
			return nil, er
		}
		snapshots[path] = b
	}
	defer func() {
		if !committed {
			for path, b := range snapshots {
				_ = security.WriteAtomic(path, b, 0600)
			}
			for _, path := range paths {
				_ = os.Remove(path)
			}
		}
	}()
	if err = e.ensureNginxObservability(target); err != nil {
		return nil, err
	}
	log("1/4 对比源与目标构建能力，校验全部网站")
	for i := range sites {
		sites[i].Config = cloneConfig(sites[i].Config)
		sites[i].Config["instanceId"] = target.ID
		sites[i].Config["probeToken"] = security.ID()
		if err := prepareSiteAccess(sites[i], filepath.Join(e.Root, "sites", sites[i].ID)); err != nil {
			return nil, err
		}
		candidate := e.renderSite(sites[i], target, filepath.Join(e.Root, "sites", sites[i].ID))
		path := filepath.Join(targetBase, "conf", "sites", sites[i].ID+".conf")
		if _, er := os.Stat(path); er == nil {
			return nil, errors.New("目标网站配置文件已存在，请先核对")
		}
		if err = security.WriteAtomic(path, []byte(candidate), 0644); err != nil {
			return nil, err
		}
		paths = append(paths, path)
		sites[i].Config["configHash"] = security.Hash(candidate)
		sites[i].Config["renderedConfig"] = candidate
	}
	if err = e.nginx(ctx, target, func(string) {}, "-t"); err != nil {
		return nil, errors.New("目标版本不支持当前网站配置，原入口未停止")
	}
	rollback := func(cause error) (model.Config, error) {
		recovery, cancel := context.WithTimeout(context.Background(), 90e9)
		defer cancel()
		_ = systemctl(recovery, log, "stop", serviceName(target))
		_ = systemctl(recovery, log, "disable", serviceName(target))
		_ = systemctl(recovery, log, "enable", serviceName(source))
		er := systemctl(recovery, log, "start", serviceName(source))
		if er != nil {
			return nil, fmt.Errorf("切换失败，原入口恢复失败：%v；原因：%w", er, cause)
		}
		return nil, fmt.Errorf("切换失败，已恢复原入口：%w", cause)
	}
	log("2/4 停止原入口并启动目标版本")
	if err = systemctl(ctx, log, "stop", serviceName(source)); err != nil {
		return nil, err
	}
	if err = systemctl(ctx, log, "start", serviceName(target)); err != nil {
		return rollback(err)
	}
	log("3/4 验证目标进程及每个站点")
	if err = e.softwareHealth(ctx, target); err != nil {
		return rollback(err)
	}
	for _, site := range sites {
		if err = probeNginx(ctx, target, []byte(site.Config.String("renderedConfig"))); err != nil {
			return rollback(err)
		}
	}
	if err = systemctl(ctx, log, "disable", serviceName(source)); err != nil {
		return rollback(err)
	}
	if err = systemctl(ctx, log, "enable", serviceName(target)); err != nil {
		return rollback(err)
	}
	log("4/4 更新入口和网站关联")
	source.Status = "stopped"
	target.Status = "ready"
	source.Config["activeEntry"] = false
	target.Config["activeEntry"] = true
	target.Config["deferStart"] = false
	for _, r := range append(sites, source, target) {
		if err = e.save(r); err != nil {
			return rollback(err)
		}
	}
	committed = true
	return model.Config{"activeEntry": false, "runtimeStatus": "stopped", "linkedResources": append(sites, target)}, nil
}
