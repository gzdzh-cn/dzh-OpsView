package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"opsview/internal/hostfirewall"
	"opsview/internal/model"
	"opsview/internal/runtimeconfig"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var ufwNumbered = regexp.MustCompile(`^\[\s*(\d+)\]\s+(.+?)\s+(ALLOW|DENY|REJECT|LIMIT)(?:\s+(IN|OUT|FWD))?\s+(.+)$`)

func parseUFWNumbered(text string) []model.Config {
	rows := []model.Config{}
	for _, line := range strings.Split(text, "\n") {
		m := ufwNumbered.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		source, note, _ := strings.Cut(m[5], " # ")
		number, _ := strconv.Atoi(m[1])
		managed := strings.HasPrefix(strings.TrimSpace(note), "opsview-")
		rows = append(rows, model.Config{"number": number, "destination": strings.TrimSpace(m[2]), "action": m[3], "direction": m[4], "source": strings.TrimSpace(source), "note": strings.TrimSpace(note), "managed": managed, "ipv6": strings.Contains(line, "(v6)")})
	}
	return rows
}

type pendingFirewallMode struct {
	Backend       string              `json:"backend,omitempty"`
	UndoHost      []hostfirewall.Rule `json:"undoHost,omitempty"`
	OldActive     bool                `json:"oldActive"`
	NextActive    bool                `json:"nextActive"`
	Deadline      int64               `json:"deadline"`
	UndoRules     [][]string          `json:"undoRules"`
	RollbackError string              `json:"rollbackError,omitempty"`
}

func (e *Engine) firewallModePath() string { return filepath.Join(e.Root, "firewall-mode.json") }
func (e *Engine) saveFirewallMode(p pendingFirewallMode) error {
	b, _ := json.Marshal(p)
	return security.WriteAtomic(e.firewallModePath(), b, 0600)
}
func (e *Engine) loadFirewallMode() (pendingFirewallMode, error) {
	var p pendingFirewallMode
	b, err := os.ReadFile(e.firewallModePath())
	if err == nil {
		err = json.Unmarshal(b, &p)
	}
	return p, err
}
func (e *Engine) firewallPorts(ctx context.Context) ([]int, error) {
	settings, effective, err := e.sshEffective(ctx, e.sshPath())
	if err != nil || len(settings.Ports) == 0 {
		return nil, errors.New("无法检测实际 SSH 端口，禁止启用防火墙")
	}
	cfg, err := runtimeconfig.Load(e.panelConfigPath())
	if err != nil {
		return nil, err
	}
	return uniquePorts(append(sshProtectedPorts(effective), cfg.Port)), nil
}
func (e *Engine) firewallSystem(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	e.firewallMu.Lock()
	defer e.firewallMu.Unlock()
	status, statusErr := output(ctx, "ufw", "status", "verbose")
	active := strings.Contains(status, "Status: active")
	compatible := statusErr == nil && runtimeconfig.UFWEnvironmentCompatible()
	switch op.Name {
	case "system.firewall-status":
		numbered, _ := output(ctx, "ufw", "status", "numbered")
		docker, _ := output(ctx, "iptables", "-S", "DOCKER-USER")
		docker6, _ := output(ctx, "ip6tables", "-S", "DOCKER-USER")
		ports, portErr := e.firewallPorts(ctx)
		portReason := ""
		if portErr != nil {
			portReason = portErr.Error()
		}
		reason := ""
		if statusErr != nil {
			reason = "UFW 尚未安装或检测失败，请先在主机安装 UFW"
		} else if !compatible {
			reason = "检测到其他防火墙或不兼容的入站规则，仅提供诊断"
		}
		pending := model.Config{}
		if p, err := e.loadFirewallMode(); err == nil {
			pending = model.Config{"deadline": p.Deadline, "enabled": p.NextActive, "rollbackError": p.RollbackError}
		}
		return model.Config{"installed": statusErr == nil, "enabled": active, "compatible": compatible, "reason": reason, "rules": parseUFWNumbered(numbered), "raw": status, "dockerRules": docker, "dockerIPv6Rules": docker6, "protectedPorts": ports, "portReason": portReason, "pending": pending, "externalRestrictions": "云安全组需在云控制台单独配置；规则放行不代表公网可达"}, nil
	case "system.firewall-confirm":
		if p, er := e.loadFirewallMode(); er == nil && p.Backend == "firewalld" {
			if time.Now().Unix() >= p.Deadline || !op.Params.Bool("connected") {
				return nil, errors.New("确认已超时或尚未重新连接")
			}
			s := e.hostFirewall().Detect(ctx)
			if s.Enabled != p.NextActive {
				return nil, errors.New("防火墙状态已变化")
			}
			return model.Config{"confirmed": true}, os.Remove(e.firewallModePath())
		}
		p, err := e.loadFirewallMode()
		if err != nil {
			return nil, errors.New("没有待确认的防火墙状态修改")
		}
		if time.Now().Unix() >= p.Deadline {
			return nil, errors.New("确认已超时，等待回退")
		}
		if !op.Params.Bool("connected") {
			return nil, errors.New("请确认面板和 SSH 可以重新连接")
		}
		if !compatible || active != p.NextActive {
			return nil, errors.New("防火墙实际状态已变化，请检查或回退")
		}
		return model.Config{"confirmed": true}, os.Remove(e.firewallModePath())
	case "system.firewall-rollback":
		p, err := e.loadFirewallMode()
		if err != nil {
			return nil, err
		}
		return model.Config{"rolledBack": true}, e.rollbackFirewallMode(ctx, p)
	case "system.firewall-reload":
		if _, err := os.Stat(e.firewallAdminPath("pending")); !os.IsNotExist(err) {
			return nil, errors.New("规则事务待确认，请先确认或回退")
		}
		if !compatible || !active {
			return nil, errors.New("仅支持重载兼容且已启用的 UFW")
		}
		return model.Config{}, run(ctx, log, "", nil, nil, "ufw", "reload")
	case "system.firewall-mode":
		if err := e.ensureFirewallIdle(); err != nil {
			return nil, err
		}

		if s := e.hostFirewall().Detect(ctx); s.Backend == "firewalld" && s.Compatible {
			if _, er := os.Stat(e.firewallModePath()); !os.IsNotExist(er) {
				return nil, errors.New("已有状态修改待确认")
			}
			return e.firewallAdminMode(ctx, op.Params, s, log)
		}
		if !compatible {
			return nil, errors.New("UFW 不可用或防火墙体系不兼容")
		}
		if !op.Params.Bool("confirmedImpact") {
			return nil, errors.New("请确认防火墙启停影响")
		}
		if _, err := os.Stat(e.firewallModePath()); !os.IsNotExist(err) {
			return nil, errors.New("已有防火墙状态修改等待确认")
		}
		next, ok := op.Params["enabled"].(bool)
		if !ok {
			return nil, errors.New("缺少防火墙目标状态")
		}
		if next == active {
			return nil, errors.New("防火墙已经处于目标状态")
		}
		// Do not race an SSH port migration or pending rule confirmation.
		if _, err := os.Stat(e.sshPendingPath()); !os.IsNotExist(err) {
			return nil, errors.New("SSH 修改待确认，请稍后修改防火墙状态")
		}
		files, _ := filepath.Glob(filepath.Join(e.Root, "firewall", "*.json"))
		for _, path := range files {
			b, _ := os.ReadFile(path)
			var p pendingFirewall
			if json.Unmarshal(b, &p) == nil && !p.Confirmed && !p.RolledBack {
				return nil, errors.New("请先确认或等待待生效规则回退")
			}
		}
		p := pendingFirewallMode{OldActive: active, NextActive: next, Deadline: time.Now().Add(120 * time.Second).Unix()}
		var ports []int
		var err error
		if next {
			ports, err = e.firewallPorts(ctx)
			if err != nil {
				return nil, err
			}
			if !validSource(op.Params.String("source"), false) {
				return nil, errors.New("请输入面板与 SSH 放行来源 IP/CIDR")
			}
		}
		if err = e.saveFirewallMode(p); err != nil {
			return nil, err
		}
		fail := func(cause error) (model.Config, error) {
			rbctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if rb := e.rollbackFirewallMode(rbctx, p); rb != nil {
				return nil, fmt.Errorf("%v；回退待重试：%w", cause, rb)
			}
			return nil, cause
		}
		if next {
			added, er := output(ctx, "ufw", "show", "added")
			if er != nil {
				return fail(er)
			}
			for _, port := range ports {
				source := op.Params.String("source")
				if !runtimeconfig.ExistingUFWRule(added, source, port) {
					rule := []string{"allow", "from", source, "to", "any", "port", strconv.Itoa(port), "proto", "tcp", "comment", "opsview-access"}
					p.UndoRules = append(p.UndoRules, append([]string{"--force", "delete"}, rule...))
					if er = e.saveFirewallMode(p); er != nil {
						return fail(er)
					}
					if er = run(ctx, log, "", nil, nil, "ufw", rule...); er != nil {
						return fail(er)
					}
				}
			}
		}
		action := "disable"
		if next {
			action = "enable"
		}
		if err = run(ctx, log, "", nil, nil, "ufw", "--force", action); err != nil {
			return fail(err)
		}
		if err = e.syncFirewallAux(ctx, next, log); err != nil {
			return fail(err)
		}
		log("防火墙状态已修改，请在 120 秒内重新连接面板和 SSH 并确认，否则自动回退")
		return model.Config{"deadline": p.Deadline}, nil
	}
	return nil, errors.New("防火墙系统操作无效")
}
func (e *Engine) rollbackFirewallMode(ctx context.Context, p pendingFirewallMode) error {
	if p.Backend == "firewalld" {
		if err := e.hostFirewall().SetEnabled(ctx, "firewalld", p.OldActive); err != nil {
			return err
		}
		for _, r := range p.UndoHost {
			if err := e.hostFirewall().Change(ctx, "firewalld", r, true); err != nil {
				return err
			}
		}
		if err := e.syncFirewallAux(ctx, p.OldActive, func(string) {}); err != nil {
			return err
		}
		return os.Remove(e.firewallModePath())
	}
	action := "disable"
	if p.OldActive {
		action = "enable"
	}
	if err := run(ctx, func(string) {}, "", nil, nil, "ufw", "--force", action); err != nil {
		return err
	}
	for _, rule := range p.UndoRules {
		if err := undoSecurityUFW(ctx, rule); err != nil {
			return err
		}
	}
	status, err := output(ctx, "ufw", "status")
	if err != nil {
		return err
	}
	if strings.Contains(status, "Status: active") != p.OldActive {
		return errors.New("防火墙尚未恢复目标状态")
	}
	if err = e.syncFirewallAux(ctx, p.OldActive, func(string) {}); err != nil {
		return err
	}
	return os.Remove(e.firewallModePath())
}

// A write-ahead record may contain a rule whose creation never completed. Also
// make retries after a partial rollback safe without deleting an external rule.
func undoSecurityUFW(ctx context.Context, args []string) error {
	source, comment, port := "", "", 0
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "from":
			source = args[i+1]
		case "port":
			port, _ = strconv.Atoi(args[i+1])
		case "comment":
			comment = args[i+1]
		}
	}
	if source == "" || port == 0 || !strings.HasPrefix(comment, "opsview-") {
		return errors.New("回退规则记录无效")
	}
	added, err := output(ctx, "ufw", "show", "added")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(added, "\n") {
		if !runtimeconfig.ExistingUFWRule(line, source, port) {
			continue
		}
		fields := strings.Fields(line)
		for i := 0; i+1 < len(fields); i++ {
			if fields[i] == "comment" && strings.Trim(fields[i+1], "\"'") == comment {
				return run(ctx, func(string) {}, "", nil, nil, "ufw", args...)
			}
		}
	}
	return nil
}
