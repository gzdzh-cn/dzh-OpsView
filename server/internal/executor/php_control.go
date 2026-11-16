package executor

import (
	"context"
	"errors"
	"fmt"
	"opsview/internal/model"
	"path/filepath"
	"strings"
	"time"
)

// siteMu keeps website application and expiry from racing service control.
func (e *Engine) controlPHP(ctx context.Context, r model.Resource, action string, p model.Config, log func(string)) (model.Config, error) {
	e.siteMu.Lock()
	defer e.siteMu.Unlock()
	if action != "start" && action != "stop" && action != "restart" && action != "reload" {
		return nil, errors.New("PHP 服务操作无效")
	}
	if r.Config.String("mode") != "native" {
		return nil, errors.New("容器实例请通过 Docker 管理")
	}
	type target struct {
		site                  model.Resource
		unit                  string
		wasActive, wasEnabled bool
	}
	targets := []target{}
	id := p.String("siteId")
	if id == "" {
		targets = append(targets, target{unit: serviceName(r)})
	}
	for _, site := range e.states() {
		if site.Kind != "site" || site.Config.String("phpInstanceId") != r.ID || (id != "" && site.ID != id) {
			continue
		}
		if action != "stop" && (siteUnavailable(site) || (id == "" && site.Config.Bool("phpPaused"))) {
			continue
		}
		targets = append(targets, target{site: site, unit: phpSiteUnit(site)})
	}
	if id != "" && len(targets) == 0 {
		return nil, errors.New("站点不属于此 PHP 版本，或站点已停止、到期")
	}
	if action == "reload" && r.Config.Bool("servicesPaused") {
		return nil, errors.New("PHP 版本服务已暂停，请先启动")
	}
	if id != "" && action != "stop" && r.Config.Bool("servicesPaused") {
		return nil, errors.New("PHP 版本服务已暂停，请先启动该版本")
	}
	for i := range targets {
		target := &targets[i]
		active, _ := output(ctx, "systemctl", "is-active", target.unit)
		enabled, _ := output(ctx, "systemctl", "is-enabled", target.unit)
		target.wasActive = strings.TrimSpace(active) == "active"
		target.wasEnabled = strings.TrimSpace(enabled) == "enabled"
		if action == "reload" && !target.wasActive {
			return nil, errors.New("所选范围包含未运行的 PHP 服务，请先启动，或选择运行中的站点")
		}
		if action != "stop" {
			base := filepath.Join(e.Root, "instances", r.ID, "conf")
			if target.site.ID != "" {
				base = filepath.Join(e.Root, "sites", target.site.ID, "conf")
			}
			runtime := filepath.Join(e.Root, "instances", r.ID, "runtime")
			if _, err := output(ctx, runtime+"/sbin/php-fpm", "-t", "-y", base+"/php-fpm.conf", "-c", base+"/php.ini"); err != nil {
				return nil, errors.New("FPM 配置校验失败，服务未修改")
			}
		}
	}
	// Preserve original state so an unsuccessful batch never silently leaves a mixed set.
	restore := func(cause error) (model.Config, error) {
		recovery, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		failed := false
		for _, target := range targets {
			boot := "disable"
			if target.wasEnabled {
				boot = "enable"
			}
			if systemctl(recovery, func(string) {}, boot, target.unit) != nil {
				failed = true
			}
			state := "stop"
			if target.wasActive {
				state = "restart"
			}
			if systemctl(recovery, func(string) {}, state, target.unit) != nil {
				failed = true
			}
		}
		if failed {
			return nil, fmt.Errorf("PHP 批量操作失败，部分服务回退失败，请查看服务日志：%w", cause)
		}
		return nil, fmt.Errorf("PHP 操作失败，已恢复原服务状态：%w", cause)
	}
	for _, target := range targets {
		if action == "stop" {
			if err := systemctl(ctx, log, "disable", target.unit); err != nil {
				return restore(err)
			}
		}
		if action == "start" || action == "restart" {
			boot := "enable"
			if guardian, ok := r.Config["guardian"].(map[string]any); ok {
				if enabled, ok := guardian["autostart"].(bool); ok && !enabled {
					boot = "disable"
				}
			}
			if err := systemctl(ctx, log, boot, target.unit); err != nil {
				return restore(err)
			}
		}
		if err := systemctl(ctx, log, action, target.unit); err != nil {
			return restore(err)
		}
		if action != "stop" {
			address := phpStatusAddress(r)
			if target.site.ID != "" {
				address = filepath.Join(e.Root, "sites", target.site.ID, "run", "php.sock")
			}
			if err := waitFPM(ctx, address); err != nil {
				return restore(err)
			}
		}
	}
	if id != "" {
		site := targets[0].site
		site.Config["phpPaused"] = action == "stop"
		if err := e.save(site); err != nil {
			return restore(err)
		}
		return model.Config{"siteService": action, "runtimeStatus": r.Status}, nil
	}
	r.Config["servicesPaused"] = action == "stop"
	r.Status = "ready"
	if action == "stop" {
		r.Status = "stopped"
	}
	if err := e.save(r); err != nil {
		return restore(err)
	}
	return model.Config{"runtimeStatus": r.Status, "servicesPaused": r.Config["servicesPaused"]}, nil
}
func waitFPM(ctx context.Context, address string) error {
	deadline, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for {
		if _, err := fpmStatus(deadline, address); err == nil {
			return nil
		}
		select {
		case <-deadline.Done():
			return errors.New("FPM 协议健康检查失败")
		case <-time.After(250 * time.Millisecond):
		}
	}
}
