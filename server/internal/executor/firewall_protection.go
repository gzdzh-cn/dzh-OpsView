package executor

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"opsview/internal/hostfirewall"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type firewallProtectionConfig struct {
	SSH       bool     `json:"ssh"`
	Nginx     bool     `json:"nginx"`
	MaxRetry  int      `json:"maxRetry"`
	FindTime  int      `json:"findTime"`
	BanTime   int      `json:"banTime"`
	Whitelist []string `json:"whitelist"`
}

func (e *Engine) firewallProtectionStatus(ctx context.Context) model.Config {
	config := firewallProtectionConfig{MaxRetry: 30, FindTime: 300, BanTime: 600}
	_ = e.readFirewallAdmin("protection", &config)
	_, installedErr := e.hostFirewall().Run(ctx, "fail2ban-client", "--version")
	_, err := e.hostFirewall().Run(ctx, "fail2ban-client", "ping")
	rows := []model.Config{}
	for _, jail := range []string{"opsview-sshd", "opsview-nginx-http-auth"} {
		text, er := e.hostFirewall().Run(ctx, "fail2ban-client", "status", jail)
		if er == nil {
			bans, _ := e.hostFirewall().Run(ctx, "fail2ban-client", "get", jail, "banip", "--with-time")
			rows = append(rows, model.Config{"name": jail, "status": text, "bans": bans})
		}
	}
	nginxLogs := e.firewallNginxLogs()
	ports, sshErr := e.firewallPorts(ctx)
	return model.Config{"installed": installedErr == nil, "running": err == nil, "config": config, "jails": rows, "sshAvailable": sshErr == nil && e.firewallSSHLog() != "", "sshPorts": ports, "nginxAvailable": len(nginxLogs) > 0, "nginxLogs": nginxLogs, "notice": "基于 Fail2ban 日志的服务异常访问防护，不提供任意防护脚本"}
}
func (e *Engine) firewallNginxLogs() []string {
	out := []string{}
	for _, r := range e.states() {
		if r.Kind == "instance" && r.Config.String("software") == "nginx" {
			for _, p := range []string{filepath.Join(e.Root, "instances", r.ID, "logs", "error.log"), filepath.Join(e.Root, "instances", r.ID, "log", "error.log")} {
				if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
					out = append(out, p)
				}
			}
		}
		if r.Kind == "site" {
			p := filepath.Join(e.Root, "sites", r.ID, "logs", "error.log")
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
				out = append(out, p)
			}
		}
	}
	if st, err := os.Stat("/var/log/nginx/error.log"); err == nil && st.Mode().IsRegular() {
		out = append(out, "/var/log/nginx/error.log")
	}
	return out
}
func (e *Engine) firewallProtection(ctx context.Context, action string, p model.Config, s hostfirewall.Status, log func(string)) (model.Config, error) {
	if action == "protection-install" {
		if err := checkOS(); err != nil {
			return nil, err
		}
		if err := run(ctx, log, "", []string{"DEBIAN_FRONTEND=noninteractive"}, nil, "apt-get", "update"); err != nil {
			return nil, err
		}
		if err := run(ctx, log, "", []string{"DEBIAN_FRONTEND=noninteractive"}, nil, "apt-get", "install", "-y", "fail2ban", "python3-systemd"); err != nil {
			return nil, err
		}
		_, err := e.hostFirewall().Run(ctx, "systemctl", "enable", "fail2ban.service")
		return model.Config{}, err
	}
	if action == "protection-unban" {
		jail := p.String("jail")
		if jail != "opsview-sshd" && jail != "opsview-nginx-http-auth" {
			return nil, errors.New("只能管理 OpsView 防护")
		}
		a, err := netip.ParseAddr(p.String("ip"))
		if err != nil {
			return nil, errors.New("解封 IP 无效")
		}
		_, err = e.hostFirewall().Run(ctx, "fail2ban-client", "set", jail, "unbanip", a.String())
		return model.Config{}, err
	}
	var config firewallProtectionConfig
	b, _ := json.Marshal(p["config"])
	if err := json.Unmarshal(b, &config); err != nil {
		return nil, err
	}
	if config.MaxRetry < 1 || config.MaxRetry > 1000 || config.FindTime < 10 || config.FindTime > 86400 || config.BanTime < 10 || config.BanTime > 604800 {
		return nil, errors.New("失败次数 1–1000，统计窗口 10–86400 秒，封禁 10–604800 秒")
	}
	if len(config.Whitelist) == 0 || len(config.Whitelist) > 128 {
		return nil, errors.New("请明确填写管理来源白名单，最多 128 项")
	}
	for _, source := range config.Whitelist {
		if _, err := netip.ParseAddr(source); err != nil {
			if _, err = netip.ParsePrefix(source); err != nil {
				return nil, errors.New("白名单必须为 IP/CIDR")
			}
		}
	}
	if management := p.String("_managementIP"); management != "" && (config.SSH || config.Nginx) {
		addr, err := netip.ParseAddr(management)
		if err != nil {
			return nil, errors.New("无法确定管理来源，请从本机检查代理设置")
		}
		covered := false
		for _, source := range config.Whitelist {
			if a, err := netip.ParseAddr(source); err == nil {
				covered = covered || a == addr
			} else if prefix, err := netip.ParsePrefix(source); err == nil {
				covered = covered || prefix.Contains(addr)
			}
		}
		if !covered {
			return nil, errors.New("白名单必须包含当前管理员连接来源")
		}
	}

	_, err := e.firewallPorts(ctx)
	if err != nil && config.SSH && s.Enabled {
		return nil, err
	}
	if config.SSH && s.Enabled && e.firewallSSHLog() == "" {
		return nil, errors.New("没有可用的 SSH 认证日志或 systemd journal，无法开启防护")
	}
	if config.Nginx && s.Enabled && len(e.firewallNginxLogs()) == 0 {
		return nil, errors.New("没有可用的 Nginx 错误日志")
	}
	ssh, _, err := e.sshEffective(ctx, e.sshPath())
	if err != nil && config.SSH && s.Enabled {
		return nil, err
	}
	portStrings := []string{}
	for _, port := range ssh.Ports {
		portStrings = append(portStrings, strconv.Itoa(port))
	}
	banaction := "ufw"
	if s.Backend == "firewalld" {
		banaction = "firewallcmd-rich-rules"
	}
	base := "findtime = " + strconv.Itoa(config.FindTime) + "\nmaxretry = " + strconv.Itoa(config.MaxRetry) + "\nbantime = " + strconv.Itoa(config.BanTime) + "\nignoreip = 127.0.0.0/8 ::1 " + strings.Join(config.Whitelist, " ") + "\nbanaction = " + banaction + "\n"
	enabled := func(v bool) string {
		if v && s.Enabled {
			return "true"
		}
		return "false"
	}
	sshBackend := "backend = systemd\n"
	if path := e.firewallSSHLog(); path != "" && path != "journal" {
		sshBackend = "backend = auto\nlogpath = " + path + "\n"
	}
	text := "[opsview-sshd]\nenabled = " + enabled(config.SSH) + "\nfilter = sshd\n" + sshBackend + "port = " + strings.Join(portStrings, ",") + "\n" + base + "\n[opsview-nginx-http-auth]\nenabled = " + enabled(config.Nginx) + "\nfilter = nginx-http-auth\nbackend = auto\nlogpath = " + strings.Join(e.firewallNginxLogs(), "\n          ") + "\nport = http,https\n" + base
	path := e.extensionConfigPath("fail2ban/jail.d/opsview-firewall.local")
	old, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return nil, readErr
	}
	if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	if err = security.WriteAtomic(path, []byte(text), 0600); err != nil {
		return nil, err
	}
	restore := func(reload bool) {
		if readErr == nil {
			_ = security.WriteAtomic(path, old, 0600)
		} else {
			_ = os.Remove(path)
		}
		if reload {
			for _, j := range []string{"opsview-sshd", "opsview-nginx-http-auth"} {
				_, _ = e.hostFirewall().Run(context.Background(), "fail2ban-client", "reload", "--restart", j)
			}
		}
	}
	if !s.Enabled {
		if _, er := e.hostFirewall().Run(ctx, "fail2ban-client", "ping"); er != nil {
			return model.Config{}, e.writeFirewallAdmin("protection", config)
		}
	}
	if _, err = e.hostFirewall().Run(ctx, "fail2ban-client", "-t"); err != nil {
		restore(false)
		return nil, err
	}

	if _, err = e.hostFirewall().Run(ctx, "systemctl", "start", "fail2ban.service"); err != nil {
		restore(false)
		return nil, err
	}
	// Reload only the two owned jails; other applications' bans remain untouched.
	for jail, on := range map[string]bool{"opsview-sshd": config.SSH && s.Enabled, "opsview-nginx-http-auth": config.Nginx && s.Enabled} {
		if !on {
			if _, er := e.hostFirewall().Run(ctx, "fail2ban-client", "status", jail); er == nil {
				_, err = e.hostFirewall().Run(ctx, "fail2ban-client", "stop", jail)
			} else {
				err = nil
			}
		} else {
			_, err = e.hostFirewall().Run(ctx, "fail2ban-client", "reload", "--restart", jail)
		}
		if err != nil {
			restore(true)
			return nil, err
		}
	}
	if err = e.writeFirewallAdmin("protection", config); err != nil {
		restore(true)
		return nil, err
	}
	return model.Config{}, nil
}

func (e *Engine) firewallSSHLog() string {
	for _, path := range []string{"/var/log/auth.log", "/var/log/secure"} {
		if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() {
			return path
		}
	}
	for _, path := range []string{"/run/systemd/journal/socket", "/var/log/journal"} {
		if _, err := os.Stat(path); err == nil {
			return "journal"
		}
	}
	return ""
}
