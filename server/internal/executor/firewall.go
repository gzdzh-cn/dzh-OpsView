package executor

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"opsview/internal/logic"
	"opsview/internal/model"
	"opsview/internal/runtimeconfig"
	"opsview/internal/security"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type pendingFirewall struct {
	Resource   model.Resource `json:"resource"`
	Deadline   int64          `json:"deadline"`
	Confirmed  bool           `json:"confirmed"`
	RolledBack bool           `json:"rolledBack"`
}

func (e *Engine) firewall(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	e.firewallMu.Lock()
	defer e.firewallMu.Unlock()
	r := op.Resource
	action := strings.TrimPrefix(op.Name, "firewall.")
	path := filepath.Join(e.Root, "firewall", r.ID+".json")
	if action == "confirm" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var p pendingFirewall
		if err = json.Unmarshal(b, &p); err != nil {
			return nil, err
		}
		if time.Now().Unix() > p.Deadline && !p.Confirmed {
			return nil, errors.New("确认窗口已过期，规则将回退")
		}
		p.Confirmed = true
		b, _ = json.Marshal(p)
		if err = security.WriteAtomic(path, b, 0600); err != nil {
			return nil, err
		}
		if err = e.save(p.Resource); err != nil {
			return nil, err
		}
		return model.Config{"confirmed": true}, nil
	}
	if action == "delete" {
		stored, err := e.load(r.ID)
		if err != nil {
			if b, re := os.ReadFile(path); re == nil {
				var p pendingFirewall
				if json.Unmarshal(b, &p) == nil {
					stored = p.Resource
					err = nil
				}
			}
		}
		if err != nil {
			return nil, err
		}
		if err = e.firewallCommand(ctx, stored, true, log); err != nil {
			return nil, err
		}
		_ = os.Remove(path)
		if err = e.restrictDatabaseAfterRule(ctx, stored); err != nil {
			return nil, err
		}
		_ = e.forget(r.ID)
		return model.Config{}, nil
	}
	if action != "apply" {
		return nil, errors.New("防火墙操作无效")
	}
	if _, err := os.Stat(path); err == nil {
		return nil, errors.New("该规则已应用，禁止重复添加；请确认或先删除")
	}
	if err := e.firewallCompatible(ctx, r, log); err != nil {
		return nil, err
	}
	if r.Config.String("action") == "deny" {
		cfg, configErr := runtimeconfig.Load(runtimeconfig.Path)
		if configErr != nil {
			return nil, configErr
		}
		protected := []string{"22", strconv.Itoa(cfg.Port), "18791"}
		if config, err := output(ctx, "sshd", "-T"); err == nil {
			for _, line := range strings.Split(config, "\n") {
				fields := strings.Fields(line)
				if len(fields) == 2 && fields[0] == "port" {
					protected = append(protected, fields[1])
				}
			}
		}
		for _, p := range protected {
			if portContains(r.Config.String("ports"), p) {
				return nil, errors.New("拒绝规则不得覆盖 SSH 或面板保留端口")
			}
		}
	}
	pending := pendingFirewall{Resource: r, Deadline: time.Now().Add(2 * time.Minute).Unix()}
	b, _ := json.Marshal(pending)
	if err := security.WriteAtomic(path, b, 0600); err != nil {
		return nil, err
	}
	if err := e.firewallCommand(ctx, r, false, log); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	log("规则已应用，请在 120 秒内确认；未确认将自动回退")
	return model.Config{"confirmed": false, "confirmDeadline": pending.Deadline}, nil
}
func portContains(spec, port string) bool {
	p := strings.Split(spec, ":")
	n, _ := strconv.Atoi(port)
	lo, _ := strconv.Atoi(p[0])
	hi := lo
	if len(p) == 2 {
		hi, _ = strconv.Atoi(p[1])
	}
	return n >= lo && n <= hi
}
func (e *Engine) firewallCompatible(ctx context.Context, r model.Resource, log func(string)) error {
	if _, err := exec.LookPath("nft"); err == nil {
		rules, _ := output(ctx, "nft", "list", "ruleset")
		if strings.Contains(rules, "hook input") && !strings.Contains(rules, "ufw") {
			return errors.New("检测到现有原生 nftables 入站规则，仅提供诊断，禁止自动接管")
		}
	}
	if r.Config.String("scope") == "docker" {
		if _, err := output(ctx, "iptables", "-S", "DOCKER-USER"); err != nil {
			return errors.New("DOCKER-USER 不存在；请检查 Docker 是否采用受支持的 iptables 后端")
		}
		return nil
	}
	status, err := output(ctx, "ufw", "status")
	if err != nil {
		return errors.New("UFW 未安装，请先在主机安装并配置 UFW")
	}
	if !strings.Contains(status, "Status: active") {
		return errors.New("UFW 尚未启用；请先在主机确认 SSH 放行并启用，面板不会自动替换现有策略")
	}
	return nil
}
func (e *Engine) firewallCommand(ctx context.Context, r model.Resource, remove bool, log func(string)) error {
	c := r.Config
	if err := logic.Validate(r); err != nil {
		return err
	}
	if c.String("scope") == "host" {
		args := []string{}
		if remove {
			args = append(args, "--force", "delete")
		}
		args = append(args, c.String("action"), "proto", c.String("protocol"), "from", c.String("source"), "to", "any", "port", c.String("ports"), "comment", "opsview-"+r.ID)
		return run(ctx, log, "", nil, nil, "ufw", args...)
	}
	action := "-I"
	if remove {
		action = "-D"
	}
	src := c.String("source")
	families := []string{"iptables", "ip6tables"}
	if src != "any" {
		ip := net.ParseIP(src)
		if strings.Contains(src, "/") {
			_, n, _ := net.ParseCIDR(src)
			if n != nil {
				ip = n.IP
			}
		}
		if ip.To4() != nil {
			families = []string{"iptables"}
		} else {
			families = []string{"ip6tables"}
		}
	}
	applied := []string{}
	for _, bin := range families {
		args := []string{action, "DOCKER-USER", "-p", c.String("protocol")}
		if src != "any" {
			args = append(args, "-s", src)
		}
		ports := strings.Split(c.String("ports"), ":")
		target := "RETURN"
		if c.String("action") == "deny" {
			target = "DROP"
		}
		args = append(args, "-m", "conntrack", "--ctstate", "DNAT", "--ctdir", "ORIGINAL", "--ctorigdstport", strings.Join(ports, ":"), "-m", "comment", "--comment", "opsview-"+r.ID, "-j", target)
		if err := run(ctx, log, "", nil, nil, bin, args...); err != nil {
			if !remove {
				for _, prev := range applied {
					undo := append([]string{}, args...)
					undo[0] = "-D"
					_ = run(ctx, log, "", nil, nil, prev, undo...)
				}
			}
			return err
		}
		applied = append(applied, bin)
	}
	return nil
}
func (e *Engine) watchFirewall(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	restore := true
	for {
		if restore {
			e.reconcileFirewall(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			e.reconcileFirewall(ctx)
		}
		restore = false
	}
}
func (e *Engine) reconcileFirewall(ctx context.Context) {
	e.firewallMu.Lock()
	defer e.firewallMu.Unlock()
	files, _ := filepath.Glob(filepath.Join(e.Root, "firewall", "*.json"))
	for _, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var p pendingFirewall
		if json.Unmarshal(b, &p) != nil {
			continue
		}
		if !p.Confirmed && time.Now().Unix() > p.Deadline {

			if !p.RolledBack {
				if err = e.firewallCommand(ctx, p.Resource, true, func(string) {}); err != nil {
					continue
				}
				p.RolledBack = true
				data, _ := json.Marshal(p)
				if security.WriteAtomic(path, data, 0600) != nil {
					continue
				}
			}
			if err = e.restrictDatabaseAfterRule(ctx, p.Resource); err == nil {
				_ = os.Remove(path)
			}
			continue
		}
		if p.Confirmed && p.Resource.Config.String("scope") == "docker" {
			comment := "opsview-" + p.Resource.ID
			rules, _ := output(ctx, "iptables", "-S", "DOCKER-USER")
			if !strings.Contains(rules, comment) {
				_ = e.firewallCommand(ctx, p.Resource, false, func(string) {})
			}
		}
	}
}

// Closing the last managed remote rule also restores loopback binding, even if a broader host rule exists.
func (e *Engine) restrictDatabaseAfterRule(ctx context.Context, firewall model.Resource) error {
	id := firewall.Config.String("databaseInstanceId")
	if id == "" {
		return nil
	}
	paths, _ := filepath.Glob(filepath.Join(e.Root, "firewall", "*.json"))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var other pendingFirewall
		if json.Unmarshal(data, &other) == nil && other.Resource.ID != firewall.ID && other.Resource.Config.String("databaseInstanceId") == id && (other.Confirmed || time.Now().Unix() <= other.Deadline) {
			return nil
		}
	}
	inst, err := e.load(id)
	if err != nil {
		return err
	}
	return e.setDatabaseBind(ctx, inst, "127.0.0.1", func(string) {})
}
