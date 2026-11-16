package executor

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"opsview/internal/model"
	"opsview/internal/runtimeconfig"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type pendingPanel struct {
	Old       runtimeconfig.Config `json:"old"`
	Next      runtimeconfig.Config `json:"next"`
	TokenHash string               `json:"tokenHash"`
	Deadline  int64                `json:"deadline"`
	RestartAt int64                `json:"restartAt"`
	Restarted bool                 `json:"restarted"`
	UndoUFW   []string             `json:"undoUFW,omitempty"`
}

func (e *Engine) pendingPath() string { return filepath.Join(e.Root, "panel-pending.json") }
func (e *Engine) loadPending() (pendingPanel, error) {
	var p pendingPanel
	b, err := os.ReadFile(e.pendingPath())
	if err == nil {
		err = json.Unmarshal(b, &p)
	}
	return p, err
}
func (e *Engine) savePending(p pendingPanel) error {
	b, _ := json.Marshal(p)
	return security.WriteAtomic(e.pendingPath(), b, 0600)
}
func (e *Engine) panelRuntime(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	e.runtimeMu.Lock()
	defer e.runtimeMu.Unlock()
	unlock, lockErr := runtimeconfig.Lock(e.panelConfigPath())
	if lockErr != nil {
		return nil, lockErr
	}
	defer unlock()
	switch op.Name {
	case "system.panel-status":
		cfg, err := runtimeconfig.Load(e.panelConfigPath())
		return model.Config{"config": cfg, "certificate": e.panelCertificateInfo(), "url": cfg.URL(cfg.PublicHost), "pending": func() bool { _, er := e.loadPending(); return er == nil }()}, err
	case "system.panel-confirm":
		p, err := e.loadPending()
		if err != nil {
			return nil, errors.New("没有待确认的设置")
		}
		if time.Now().Unix() > p.Deadline || subtle.ConstantTimeCompare([]byte(p.TokenHash), []byte(security.Hash(op.Params.String("token")))) != 1 {
			return nil, errors.New("确认凭证无效或已超时")
		}
		return model.Config{"confirmed": true}, os.Remove(e.pendingPath())
	case "system.panel-apply":
		if _, err := e.loadPending(); err == nil {
			return nil, errors.New("已有访问设置等待确认，请先确认或等待回退")
		}
		if !op.Params.Bool("confirmedImpact") {
			return nil, errors.New("需要确认协议、端口及访问限制的影响")
		}
		old, err := runtimeconfig.Load(e.panelConfigPath())
		if err != nil {
			return nil, err
		}
		next := old
		raw, _ := json.Marshal(op.Params["config"])
		if err = json.Unmarshal(raw, &next); err != nil {
			return nil, err
		}
		if err = next.Validate(); err != nil {
			return nil, err
		}
		if next.Port != old.Port || next.ListenHost != old.ListenHost {
			l, er := net.Listen("tcp", next.Address())
			if er != nil {
				return nil, errors.New("新端口已占用或监听地址不可用")
			}
			l.Close()
		}
		start, er := output(ctx, "systemctl", "show", "opsview-panel.service", "--property=ExecStart", "--value")
		if er != nil || !strings.Contains(start, "--config /etc/opsview/panel.json") || strings.Contains(start, "--listen") {
			return nil, errors.New("自定义 systemd 启动命令与运行配置管理不兼容")
		}
		token := security.ID() + security.ID()
		p := pendingPanel{Old: old, Next: next, TokenHash: security.Hash(token), Deadline: time.Now().Add(120 * time.Second).Unix(), RestartAt: time.Now().Add(3 * time.Second).Unix()}
		if next.Port != old.Port {
			if runtimeconfig.CompatibleUFW() {
				source := op.Params.String("source")
				if net.ParseIP(source) == nil {
					if _, _, er = net.ParseCIDR(source); er != nil {
						return nil, errors.New("请填写新端口放行来源 IP/CIDR")
					}
				}
				added, er := output(ctx, "ufw", "show", "added")
				if er != nil {
					return nil, er
				}
				rule := []string{"allow", "from", source, "to", "any", "port", strconv.Itoa(next.Port), "proto", "tcp", "comment", "opsview-panel"}
				if !runtimeconfig.ExistingUFWRule(added, source, next.Port) {
					if er = run(ctx, log, "", nil, nil, "ufw", rule...); er != nil {
						return nil, er
					}
					p.UndoUFW = append([]string{"--force", "delete"}, rule...)
				}
			} else {
				log("未检测到兼容且启用的 UFW，请手动核对宿主机防火墙及云安全组")
			}
		}
		if err = e.savePending(p); err != nil {
			if len(p.UndoUFW) > 0 {
				_ = run(ctx, log, "", nil, nil, "ufw", p.UndoUFW...)
			}
			return nil, err
		}
		if err = runtimeconfig.Save(e.panelConfigPath(), next); err != nil {
			_ = os.Remove(e.pendingPath())
			if len(p.UndoUFW) > 0 {
				_ = run(ctx, log, "", nil, nil, "ufw", p.UndoUFW...)
			}
			return nil, err
		}
		host := next.PublicHost
		if host == "" {
			host = runtimeconfig.LocalHost()
		}
		log("运行配置已暂存，120 秒内通过新地址确认；旧防火墙规则保留")
		return model.Config{"url": next.URL(host), "confirmToken": token, "deadline": p.Deadline}, nil
	}
	return nil, errors.New("未知面板配置操作")
}
func (e *Engine) watchPanelRuntime(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			e.checkPanelRuntime(ctx)
		}
	}
}
func (e *Engine) checkPanelRuntime(ctx context.Context) {
	e.runtimeMu.Lock()
	defer e.runtimeMu.Unlock()
	unlock, lockErr := runtimeconfig.Lock(e.panelConfigPath())
	if lockErr != nil {
		return
	}
	defer unlock()
	p, err := e.loadPending()
	if err != nil {
		return
	}
	if time.Now().Unix() > p.Deadline {
		if err = runtimeconfig.Save(e.panelConfigPath(), p.Old); err != nil {
			return
		}
		if err = systemctl(ctx, func(string) {}, "restart", "opsview-panel.service"); err != nil {
			return
		}
		if len(p.UndoUFW) > 0 {
			_ = run(ctx, func(string) {}, "", nil, nil, "ufw", p.UndoUFW...)
		}
		_ = os.Remove(e.pendingPath())
		return
	}
	if !p.Restarted && time.Now().Unix() >= p.RestartAt {
		p.Restarted = true
		if e.savePending(p) == nil {
			_ = systemctl(ctx, func(string) {}, "restart", "opsview-panel.service")
		}
	}
}
