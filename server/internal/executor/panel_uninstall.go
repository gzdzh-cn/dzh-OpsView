package executor

import (
	"context"
	"errors"
	"fmt"
	"opsview/internal/model"
	"os"
)

// CleanupPanelUninstall is a local, root maintenance operation, deliberately
// absent from Execute and the web API. Panel/executor services must already be
// stopped. Ordinary firewall rules and the system's SSH configuration survive.
func (e *Engine) CleanupPanelUninstall(ctx context.Context) error {
	if os.Geteuid() != 0 {
		return errors.New("卸载防护配置需要 root")
	}
	var state firewallExtensionState
	err := e.readFirewallAdmin("extensions", &state)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil && (state.Ping || len(state.PingRules) > 0 || len(state.Rules) > 0) {
		status := e.hostFirewall().Detect(ctx)
		if status.Backend != "ufw" && status.Backend != "firewalld" {
			return errors.New("无法确定受管防火墙扩展的后端，保留现场")
		}
		if err := e.commitFirewallExtensions(ctx, status.Backend, state, firewallExtensionState{}, status.Enabled, func(string) {}); err != nil {
			return fmt.Errorf("移除受管地区、转发和 Ping 扩展失败：%w", err)
		}
	}
	path := e.extensionConfigPath("fail2ban/jail.d/opsview-firewall.local")
	if _, err := os.Lstat(path); err == nil {
		var config firewallProtectionConfig
		if err := e.readFirewallAdmin("protection", &config); err != nil {
			return err
		}
		status := e.hostFirewall().Detect(ctx)
		status.Enabled = false
		if _, err := e.firewallProtection(ctx, "protection-save", model.Config{"config": config}, status, func(string) {}); err != nil {
			return err
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}
