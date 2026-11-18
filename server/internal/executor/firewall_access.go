package executor

import (
	"context"
	"encoding/json"
	"errors"
	"opsview/internal/hostfirewall"
	"opsview/internal/model"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// prepareFirewalldAccess returns the exact inverse rules before any addition.
// Callers must save this plan in their existing durable access transaction first.
func (e *Engine) prepareFirewalldAccess(ctx context.Context, ports []int, source string) ([]hostfirewall.Rule, error) {
	if !validSource(source, false) {
		return nil, errors.New("启用防火墙时请填写管理来源 IP/CIDR")
	}
	s, err := e.hostFirewall().Status(ctx)
	if err != nil {
		return nil, err
	}
	zone := firewallDefaultZone(ctx, e.hostFirewall(), "firewalld")
	if zone == "" {
		return nil, errors.New("无法读取默认防火墙区域")
	}
	out := []hostfirewall.Rule{}
	family := "ipv4"
	if strings.Contains(source, ":") {
		family = "ipv6"
	}
	for _, port := range ports {
		r := hostfirewall.Rule{Kind: "port", Protocol: "tcp", Ports: strconv.Itoa(port), Source: source, Destination: "any", Direction: "in", Family: family, Action: "allow", Zone: zone, Note: "opsview-access", Permanent: true, Runtime: true}
		found := false
		for _, existing := range s.Rules {
			if existing.Key() == r.Key() {
				found = true
				break
			}
		}
		if !found {
			out = append(out, r)
		}
	}
	return out, nil
}
func (e *Engine) applyHostAccess(ctx context.Context, rules []hostfirewall.Rule, remove bool) error {
	for _, r := range rules {
		if remove {
			s, err := e.hostFirewall().Status(ctx)
			if err != nil {
				return err
			}
			found := false
			for _, existing := range s.Rules {
				if existing.Key() == r.Key() {
					r = existing
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		if err := e.hostFirewall().Change(ctx, "firewalld", r, remove); err != nil {
			return err
		}
	}
	return nil
}
func (e *Engine) firewallDependencies(ctx context.Context, p model.Config, log func(string)) (model.Config, error) {
	if !p.Bool("confirmedImpact") {
		return nil, errors.New("请确认安装防火墙扩展依赖")
	}
	if err := checkOS(); err != nil {
		return nil, err
	}
	if err := run(ctx, log, "", []string{"DEBIAN_FRONTEND=noninteractive"}, nil, "apt-get", "update"); err != nil {
		return nil, err
	}
	return model.Config{}, run(ctx, log, "", []string{"DEBIAN_FRONTEND=noninteractive"}, nil, "apt-get", "install", "-y", "ipset")
}

// Caller holds firewallMu. Never nest a second lock while checking journals.
func (e *Engine) ensureFirewallIdle() error {
	for _, path := range []string{e.firewallAdminPath("pending"), e.firewallAdminPath("feature-pending"), e.firewallModePath(), e.sshPendingPath(), e.pendingPath()} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			return errors.New("已有访问或防火墙变更待确认或回退")
		}
	}
	files, err := filepath.Glob(filepath.Join(e.Root, "firewall", "*.json"))
	if err != nil {
		return err
	}
	for _, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var p pendingFirewall
		if json.Unmarshal(b, &p) != nil {
			return errors.New("旧防火墙事务损坏，请先检查")
		}
		if !p.Confirmed && !p.RolledBack {
			return errors.New("旧防火墙规则待确认或回退")
		}
	}
	return nil
}
