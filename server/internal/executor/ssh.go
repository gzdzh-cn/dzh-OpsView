package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"opsview/internal/hostfirewall"
	"opsview/internal/model"
	"opsview/internal/runtimeconfig"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Only these directives can be changed through the panel. Includes, commands,
// host keys and Match blocks remain under the host administrator's control.
type sshSettings struct {
	Ports               []int  `json:"ports"`
	Password            bool   `json:"password"`
	PublicKey           bool   `json:"publicKey"`
	RootLogin           string `json:"rootLogin"`
	MaxAuthTries        int    `json:"maxAuthTries"`
	LoginGraceTime      int    `json:"loginGraceTime"`
	ClientAliveInterval int    `json:"clientAliveInterval"`
	ClientAliveCountMax int    `json:"clientAliveCountMax"`
}

var sshDirectives = []string{"port", "passwordauthentication", "pubkeyauthentication", "permitrootlogin", "maxauthtries", "logingracetime", "clientaliveinterval", "clientalivecountmax", "kbdinteractiveauthentication", "challengeresponseauthentication"}

type pendingSSH struct {
	Old             []byte              `json:"old"`
	AppliedRevision string              `json:"appliedRevision"`
	AppliedIncludes string              `json:"appliedIncludes"`
	Final           []byte              `json:"final"`
	Settings        sshSettings         `json:"settings"`
	Unit            string              `json:"unit"`
	Deadline        int64               `json:"deadline"`
	Mode            uint32              `json:"mode"`
	UndoHost        []hostfirewall.Rule `json:"undoHost,omitempty"`
	UndoRules       [][]string          `json:"undoRules"`
	RollbackError   string              `json:"rollbackError,omitempty"`
}

func (e *Engine) sshPath() string {
	if e.sshConfigPath != "" {
		return e.sshConfigPath
	}
	return "/etc/ssh/sshd_config"
}
func (e *Engine) sshPendingPath() string { return filepath.Join(e.Root, "ssh-pending.json") }
func (e *Engine) readSSHConfig() ([]byte, os.FileMode, error) {
	st, err := os.Lstat(e.sshPath())
	if err != nil {
		return nil, 0, err
	}
	if !st.Mode().IsRegular() || st.Size() > 1<<20 {
		return nil, 0, errors.New("SSH 主配置必须为不超过 1 MiB 的普通文件")
	}
	b, err := os.ReadFile(e.sshPath())
	return b, st.Mode().Perm(), err
}
func parseSSHSettings(text string) sshSettings {
	s := sshSettings{}
	for _, line := range strings.Split(text, "\n") {
		p := strings.Fields(line)
		if len(p) < 2 {
			continue
		}
		n, _ := strconv.Atoi(p[1])
		switch p[0] {
		case "port":
			s.Ports = append(s.Ports, n)
		case "passwordauthentication":
			s.Password = p[1] == "yes"
		case "pubkeyauthentication":
			s.PublicKey = p[1] == "yes"
		case "permitrootlogin":
			s.RootLogin = p[1]
		case "maxauthtries":
			s.MaxAuthTries = n
		case "logingracetime":
			s.LoginGraceTime = n
		case "clientaliveinterval":
			s.ClientAliveInterval = n
		case "clientalivecountmax":
			s.ClientAliveCountMax = n
		}
	}
	if s.RootLogin == "without-password" {
		s.RootLogin = "prohibit-password"
	}
	s.Ports = uniquePorts(s.Ports)
	return s
}
func sshProtectedPorts(effective string) []int {
	ports := parseSSHSettings(effective).Ports
	for _, line := range strings.Split(effective, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "listenaddress" {
			_, port, err := net.SplitHostPort(fields[1])
			if err == nil {
				p, err := strconv.Atoi(port)
				if err == nil && p > 0 && p <= 65535 {
					ports = append(ports, p)
				}
			}
		}
	}
	return uniquePorts(ports)
}
func uniquePorts(ports []int) []int {
	out := []int{}
	for _, p := range ports {
		found := false
		for _, old := range out {
			if old == p {
				found = true
			}
		}
		if !found {
			out = append(out, p)
		}
	}
	sort.Ints(out)
	return out
}
func (s sshSettings) validate() error {
	if len(s.Ports) == 0 || len(s.Ports) > 8 {
		return errors.New("SSH 需要 1–8 个监听端口")
	}
	for _, p := range s.Ports {
		if p < 1 || p > 65535 {
			return errors.New("SSH 端口须为 1–65535")
		}
	}
	if !s.Password && !s.PublicKey {
		return errors.New("不能同时关闭密码和公钥认证")
	}
	if s.RootLogin != "yes" && s.RootLogin != "no" && s.RootLogin != "prohibit-password" {
		return errors.New("root 登录策略无效")
	}
	if s.MaxAuthTries < 1 || s.MaxAuthTries > 20 || s.LoginGraceTime < 10 || s.LoginGraceTime > 600 || s.ClientAliveInterval < 0 || s.ClientAliveInterval > 3600 || s.ClientAliveCountMax < 1 || s.ClientAliveCountMax > 10 {
		return errors.New("SSH 认证次数、登录等待或保活参数超出范围")
	}
	return nil
}
func sshYes(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
func buildSSHConfig(old []byte, s sshSettings) []byte {
	lines := []string{"# BEGIN OPSVIEW SSH"}
	for _, p := range uniquePorts(s.Ports) {
		lines = append(lines, fmt.Sprintf("Port %d", p))
	}
	lines = append(lines, "PasswordAuthentication "+sshYes(s.Password), "PubkeyAuthentication "+sshYes(s.PublicKey), "PermitRootLogin "+s.RootLogin,
		fmt.Sprintf("MaxAuthTries %d", s.MaxAuthTries), fmt.Sprintf("LoginGraceTime %d", s.LoginGraceTime), fmt.Sprintf("ClientAliveInterval %d", s.ClientAliveInterval), fmt.Sprintf("ClientAliveCountMax %d", s.ClientAliveCountMax))
	if !s.Password {
		lines = append(lines, "KbdInteractiveAuthentication no", "ChallengeResponseAuthentication no")
	}
	lines = append(lines, "# END OPSVIEW SSH")
	managed, match := false, false
	for _, line := range strings.Split(string(old), "\n") {
		if line == "# BEGIN OPSVIEW SSH" {
			managed = true
			continue
		}
		if line == "# END OPSVIEW SSH" {
			managed = false
			continue
		}
		if managed {
			continue
		}
		fields := strings.Fields(line)
		key := ""
		if len(fields) > 0 {
			key = strings.ToLower(strings.SplitN(fields[0], "=", 2)[0])
		}
		if key == "match" {
			match = true
		}
		skip := false
		if !match {
			for _, k := range sshDirectives {
				if key == k && (k != "kbdinteractiveauthentication" && k != "challengeresponseauthentication" || !s.Password) {
					skip = true
				}
			}
		}
		if !skip {
			lines = append(lines, line)
		}
	}
	return []byte(strings.Join(lines, "\n"))
}
func (e *Engine) sshEffective(ctx context.Context, path string) (sshSettings, string, error) {
	text, err := output(ctx, "sshd", "-T", "-f", path)
	return parseSSHSettings(text), text, err
}

// Include changes may leave the global -T output unchanged (e.g. Match blocks).
// Hash their contents too, without returning any included text to the browser.
func sshRevision(main []byte, effective string) (string, error) {
	parts := []string{string(main), effective}
	seen := map[string]bool{}
	total := len(main)
	var visit func([]byte, int) error
	visit = func(content []byte, depth int) error {
		if depth > 16 {
			return errors.New("SSH Include 层级超出管理范围")
		}
		for _, line := range strings.Split(string(content), "\n") {
			line = strings.TrimSpace(line)
			if len(line) < 8 || !strings.EqualFold(line[:7], "include") || !strings.ContainsRune(" \t=", rune(line[7])) {
				continue
			}
			patterns, err := sshIncludePatterns(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line[7:]), "=")))
			if err != nil {
				return err
			}
			for _, pattern := range patterns {
				if !filepath.IsAbs(pattern) {
					pattern = filepath.Join("/etc/ssh", pattern)
				}
				files, err := filepath.Glob(pattern)
				if err != nil {
					return err
				}
				for _, path := range files {
					if seen[path] {
						continue
					}
					seen[path] = true
					if len(seen) > 256 {
						return errors.New("SSH Include 文件数量超出管理范围")
					}
					st, err := os.Stat(path)
					if err != nil {
						return err
					}
					if !st.Mode().IsRegular() || st.Size() > 1<<20 {
						return errors.New("SSH Include 必须为不超过 1 MiB 的普通文件")
					}
					b, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					total += len(b)
					if total > 4<<20 {
						return errors.New("SSH Include 总大小超出管理范围")
					}
					parts = append(parts, path, string(b))
					if err = visit(b, depth+1); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	if err := visit(main, 0); err != nil {
		return "", err
	}
	return security.Hash(strings.Join(parts, "\x00")), nil
}
func sshIncludePatterns(text string) ([]string, error) {
	parts := []string{}
	var b strings.Builder
	quote, escape := false, false
	flush := func() {
		if b.Len() > 0 {
			parts = append(parts, b.String())
			b.Reset()
		}
	}
	for _, r := range text {
		if escape {
			b.WriteRune(r)
			escape = false
			continue
		}
		if r == '\\' {
			escape = true
			continue
		}
		if r == '"' {
			quote = !quote
			continue
		}
		if !quote && r == '#' {
			break
		}
		if !quote && (r == ' ' || r == '\t') {
			flush()
			continue
		}
		b.WriteRune(r)
	}
	if quote || escape {
		return nil, errors.New("SSH Include 路径引号或转义不完整")
	}
	flush()
	return parts, nil
}
func (e *Engine) sshUnit(ctx context.Context) (string, error) {
	if state, _ := output(ctx, "systemctl", "is-active", "ssh.socket", "sshd.socket"); state != "" {
		for _, line := range strings.Fields(state) {
			if line == "active" {
				return "", errors.New("SSH 使用 socket 激活，请先在主机迁移为普通 ssh 服务后再修改")
			}
		}
	}
	for _, unit := range []string{"ssh.service", "sshd.service"} {
		if state, err := output(ctx, "systemctl", "is-active", unit); err == nil && strings.TrimSpace(state) == "active" {
			command, err := output(ctx, "systemctl", "show", unit, "--property=ExecStart", "--value")
			if err != nil || !strings.Contains(command, "sshd") {
				return "", errors.New("无法验证 SSH 服务启动配置")
			}
			for _, flag := range []string{" -f", " -p", " -o"} {
				if strings.Contains(command, flag) {
					return "", errors.New("SSH 自定义启动参数不支持面板修改")
				}
			}
			env, envErr := output(ctx, "systemctl", "show", unit, "--property=Environment", "--value")
			if envErr != nil || strings.Contains(env, "SSHD_OPTS=") {
				return "", errors.New("SSH systemd 环境覆盖需要先在主机核对")
			}
			// Debian's SSHD_OPTS can override the managed configuration.
			defaults, _ := os.ReadFile("/etc/default/ssh")
			for _, line := range strings.Split(string(defaults), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "SSHD_OPTS=") && strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "SSHD_OPTS=")), "\"'") != "" {
					return "", errors.New("/etc/default/ssh 含自定义 SSHD_OPTS，仅提供诊断")
				}
			}
			return unit, nil
		}
	}
	return "", errors.New("SSH 服务未运行或未使用受支持的 systemd 服务")
}
func (e *Engine) validateSSHFile(ctx context.Context, content []byte, want sshSettings) error {
	f, err := os.CreateTemp(filepath.Dir(e.sshPath()), ".opsview-sshd-*")
	if err != nil {
		return err
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err = f.Write(content); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = run(ctx, func(string) {}, "", nil, nil, "sshd", "-t", "-f", path); err != nil {
		return errors.New("sshd -t 配置校验失败，原配置保留")
	}
	got, _, err := e.sshEffective(ctx, path)
	if err != nil {
		return err
	}
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(want)
	if string(a) != string(b) {
		return errors.New("SSH Include 或版本差异导致有效配置与请求不一致，请在主机检查配置")
	}
	return nil
}
func (e *Engine) sshOperation(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	e.sshMu.Lock()
	defer e.sshMu.Unlock()
	e.firewallMu.Lock()
	defer e.firewallMu.Unlock()
	switch op.Name {
	case "system.ssh-status":
		old, _, err := e.readSSHConfig()
		if err != nil {
			return model.Config{"available": false, "reason": err.Error()}, nil
		}
		settings, effective, err := e.sshEffective(ctx, e.sshPath())
		if err != nil {
			return model.Config{"available": false, "reason": "无法读取有效 SSH 配置，请检查 OpenSSH 安装和配置"}, nil
		}
		unit, unitErr := e.sshUnit(ctx)
		revision, revisionErr := sshRevision(old, effective)
		if revisionErr != nil {
			unitErr = revisionErr
		}
		reason := ""
		if unitErr != nil {
			reason = unitErr.Error()
		}
		pending := model.Config{}
		if p, er := e.loadSSH(); er == nil {
			pending = model.Config{"deadline": p.Deadline, "ports": p.Settings.Ports, "rollbackError": p.RollbackError}
		}
		who, _ := output(ctx, "who", "-u")
		fail2ban, _ := output(ctx, "fail2ban-client", "status", "sshd")
		return model.Config{"available": true, "editable": unitErr == nil, "reason": reason, "unit": unit, "settings": settings, "revision": revision, "pending": pending, "sessions": who, "fail2ban": fail2ban, "keys": e.sshPublicKeys(ctx), "matchWarning": "这里修改全局基线；Match 条件、AllowUsers、PAM 和外部认证策略仍可能覆盖或限制登录，请保留现有 SSH 会话测试新连接"}, nil
	case "system.ssh-logs":
		text, err := output(ctx, "journalctl", "_COMM=sshd", "--since", "24 hours ago", "-n", "200", "--no-pager", "-o", "short-iso")
		if err != nil {
			return nil, errors.New("无法读取 SSH journal 日志")
		}
		rows := []model.Config{}
		for _, line := range strings.Split(text, "\n") {
			status := ""
			if strings.Contains(line, "Accepted ") {
				status = "成功"
			} else if strings.Contains(line, "Failed ") || strings.Contains(line, "Invalid user") || strings.Contains(line, "authentication failure") {
				status = "失败"
			}
			if status != "" {
				rows = append(rows, model.Config{"status": status, "detail": line})
			}
		}
		return model.Config{"rows": rows, "content": text, "notice": "最近 24 小时内最多 200 行 journal 记录，非完整登录统计"}, nil
	case "system.ssh-key-add", "system.ssh-key-delete":
		return e.changeSSHPublicKey(ctx, op)
	case "system.ssh-confirm":
		p, err := e.loadSSH()
		if err != nil {
			return nil, errors.New("没有待确认的 SSH 修改")
		}
		if time.Now().Unix() >= p.Deadline {
			return nil, errors.New("确认已超时，等待自动回退")
		}
		if !op.Params.Bool("connected") {
			return nil, errors.New("请先使用新配置完成一次 SSH 登录，再确认")
		}
		current, _, err := e.readSSHConfig()
		if err != nil {
			return nil, err
		}
		if security.Hash(string(current)) != p.AppliedRevision {
			return nil, errors.New("SSH 配置已被外部修改，请等待回退并核对主机配置")
		}
		includes, includeErr := sshRevision(current, "")
		if includeErr != nil {
			return nil, includeErr
		}
		if includes != p.AppliedIncludes {
			return nil, errors.New("SSH Include 已修改，请立即回退并核对配置")
		}
		if err = e.validateSSHFile(ctx, p.Final, p.Settings); err != nil {
			return nil, err
		}
		if err = security.WriteAtomic(e.sshPath(), p.Final, os.FileMode(p.Mode)); err != nil {
			return nil, err
		}
		// Persist the final revision before reloading so a crash still restores Old.
		p.AppliedRevision = security.Hash(string(p.Final))
		p.AppliedIncludes, _ = sshRevision(p.Final, "")
		if err = e.saveSSH(p); err != nil {
			return nil, err
		}
		if err = systemctl(ctx, log, "reload", p.Unit); err != nil {
			_ = e.rollbackSSH(ctx, p)
			return nil, err
		}
		if err = probeSSHPorts(ctx, p.Settings.Ports); err != nil {
			_ = e.rollbackSSH(ctx, p)
			return nil, err
		}
		return model.Config{"confirmed": true}, os.Remove(e.sshPendingPath())
	case "system.ssh-rollback":
		p, err := e.loadSSH()
		if err != nil {
			return nil, err
		}
		return model.Config{"rolledBack": true}, e.rollbackSSH(ctx, p)
	case "system.ssh-apply":
		return e.applySSH(ctx, op.Params, log)
	}
	return nil, errors.New("SSH 操作无效")
}
func (e *Engine) applySSH(ctx context.Context, p model.Config, log func(string)) (model.Config, error) {
	if err := e.ensureFirewallIdle(); err != nil {
		return nil, err
	}

	if !p.Bool("confirmedImpact") {
		return nil, errors.New("请确认 SSH 登录影响")
	}
	if _, err := os.Stat(e.sshPendingPath()); !os.IsNotExist(err) {
		return nil, errors.New("已有 SSH 修改等待确认或回退")
	}
	unit, err := e.sshUnit(ctx)
	if err != nil {
		return nil, err
	}
	old, mode, err := e.readSSHConfig()
	if err != nil {
		return nil, err
	}
	current, effective, err := e.sshEffective(ctx, e.sshPath())
	if err != nil {
		return nil, err
	}
	revision, err := sshRevision(old, effective)
	if err != nil {
		return nil, err
	}
	if p.String("revision") != revision {
		return nil, &model.RevisionConflict{Revision: revision}
	}
	var next sshSettings
	b, _ := json.Marshal(p["settings"])
	if err = json.Unmarshal(b, &next); err != nil {
		return nil, errors.New("SSH 设置格式无效")
	}
	next.Ports = uniquePorts(next.Ports)
	if err = next.validate(); err != nil {
		return nil, err
	}
	panel, err := runtimeconfig.Load(e.panelConfigPath())
	if err != nil {
		return nil, err
	}
	for _, port := range next.Ports {
		known := false
		for _, oldPort := range current.Ports {
			if port == oldPort {
				known = true
			}
		}
		if !known {
			if port == panel.Port || port == 18791 {
				return nil, errors.New("SSH 端口与面板保留端口冲突")
			}
			if er := sshPortAvailable(port); er != nil {
				return nil, errors.New("新 SSH 端口已占用")
			}
		}
	}
	final := buildSSHConfig(old, next)
	if err = e.validateSSHFile(ctx, final, next); err != nil {
		return nil, err
	}
	staged := next
	staged.Ports = uniquePorts(append(append([]int{}, next.Ports...), current.Ports...))
	candidate := buildSSHConfig(old, staged)
	if err = e.validateSSHFile(ctx, candidate, staged); err != nil {
		return nil, err
	}
	pending := pendingSSH{Old: old, Final: final, Settings: next, Unit: unit, Deadline: time.Now().Add(120 * time.Second).Unix(), Mode: uint32(mode), AppliedRevision: security.Hash(string(candidate))}
	pending.AppliedIncludes, err = sshRevision(candidate, "")
	if err != nil {
		return nil, err
	}
	// A rollback record exists before any host change, including firewall rules.
	if err = e.saveSSH(pending); err != nil {
		return nil, err
	}
	fail := func(cause error) (model.Config, error) {
		rbctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if rb := e.rollbackSSH(rbctx, pending); rb != nil {
			return nil, fmt.Errorf("%v；回退待重试：%w", cause, rb)
		}
		return nil, cause
	}
	if fw := e.hostFirewall().Detect(ctx); fw.Backend == "firewalld" && fw.Enabled && fw.Compatible {
		pending.UndoHost, err = e.prepareFirewalldAccess(ctx, next.Ports, p.String("source"))
		if err != nil {
			return fail(err)
		}
		if err = e.saveSSH(pending); err != nil {
			return fail(err)
		}
		if err = e.applyHostAccess(ctx, pending.UndoHost, false); err != nil {
			return fail(err)
		}
	} else if runtimeconfig.CompatibleUFW() {
		source := p.String("source")
		if !validSource(source, false) {
			return fail(errors.New("启用 UFW 时请填写新 SSH 端口放行来源 IP/CIDR"))
		}
		added, er := output(ctx, "ufw", "show", "added")
		if er != nil {
			return fail(er)
		}
		for _, port := range next.Ports {
			if !runtimeconfig.ExistingUFWRule(added, source, port) {
				rule := []string{"allow", "from", source, "to", "any", "port", strconv.Itoa(port), "proto", "tcp", "comment", "opsview-ssh"}
				pending.UndoRules = append(pending.UndoRules, append([]string{"--force", "delete"}, rule...))
				if er = e.saveSSH(pending); er != nil {
					return fail(er)
				}
				if er = run(ctx, log, "", nil, nil, "ufw", rule...); er != nil {
					return fail(er)
				}
			}
		}
	} else {
		log("未自动修改主机防火墙；请核对新 SSH 端口与云安全组")
	}
	if err = security.WriteAtomic(e.sshPath(), candidate, mode); err != nil {
		return fail(err)
	}
	if err = systemctl(ctx, log, "reload", unit); err != nil {
		return fail(err)
	}
	if err = probeSSHPorts(ctx, staged.Ports); err != nil {
		return fail(err)
	}
	log("SSH 已重载，旧端口临时保留；请在 120 秒内使用新设置登录并确认，否则自动回退")
	return model.Config{"deadline": pending.Deadline, "ports": next.Ports}, nil
}
func sshPortAvailable(port int) error {
	for _, host := range []string{"127.0.0.1", "::1"} {
		if c, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 150*time.Millisecond); err == nil {
			c.Close()
			return errors.New("端口已有监听")
		}
	}
	for _, family := range []struct{ network, host string }{{"tcp4", "0.0.0.0"}, {"tcp6", "::"}} {
		l, err := net.Listen(family.network, net.JoinHostPort(family.host, strconv.Itoa(port)))
		if err != nil {
			if family.network == "tcp6" && (errors.Is(err, syscall.EAFNOSUPPORT) || errors.Is(err, syscall.EADDRNOTAVAIL)) {
				continue
			}
			return err
		}
		l.Close()
	}
	return nil
}
func validSource(s string, any bool) bool {
	if any && s == "any" {
		return true
	}
	if net.ParseIP(s) != nil {
		return true
	}
	_, _, err := net.ParseCIDR(s)
	return err == nil
}
func probeSSHPorts(ctx context.Context, ports []int) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	for _, port := range ports {
		ok := false
		for !ok && ctx.Err() == nil {
			for _, host := range []string{"127.0.0.1", "::1"} {
				c, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
				if err != nil {
					continue
				}
				_ = c.SetReadDeadline(time.Now().Add(time.Second))
				buf := make([]byte, 256)
				n, _ := c.Read(buf)
				c.Close()
				if strings.HasPrefix(string(buf[:n]), "SSH-") {
					ok = true
					break
				}
			}
			if !ok {
				select {
				case <-ctx.Done():
				case <-time.After(100 * time.Millisecond):
				}
			}
		}
		if !ok {
			return fmt.Errorf("SSH 端口 %d 未通过本机协议检查（仅监听特定地址的实例需在主机管理）", port)
		}
	}
	return nil
}
func (e *Engine) saveSSH(p pendingSSH) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return security.WriteAtomic(e.sshPendingPath(), b, 0600)
}
func (e *Engine) loadSSH() (pendingSSH, error) {
	var p pendingSSH
	b, err := os.ReadFile(e.sshPendingPath())
	if err == nil {
		err = json.Unmarshal(b, &p)
	}
	return p, err
}
func (e *Engine) rollbackSSH(ctx context.Context, p pendingSSH) error {
	current, _, readErr := e.readSSHConfig()
	if readErr == nil && string(current) != string(p.Old) && security.Hash(string(current)) != p.AppliedRevision {
		// Preserve external changes before restoring the last known configuration.
		if err := security.WriteAtomic(filepath.Join(e.Root, "ssh-conflict.conf"), current, 0600); err != nil {
			return err
		}
	}
	if err := security.WriteAtomic(e.sshPath(), p.Old, os.FileMode(p.Mode)); err != nil {
		return err
	}
	if err := run(ctx, func(string) {}, "", nil, nil, "sshd", "-t", "-f", e.sshPath()); err != nil {
		return err
	}
	if err := systemctl(ctx, func(string) {}, "reload", p.Unit); err != nil {
		return err
	}
	settings, _, err := e.sshEffective(ctx, e.sshPath())
	if err != nil {
		return err
	}
	if err = probeSSHPorts(ctx, settings.Ports); err != nil {
		return err
	}
	if err := e.applyHostAccess(ctx, p.UndoHost, true); err != nil {
		return err
	}
	for _, rule := range p.UndoRules {
		if err := undoSecurityUFW(ctx, rule); err != nil {
			return err
		}
	}
	return os.Remove(e.sshPendingPath())
}
func (e *Engine) watchSSH(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		e.sshMu.Lock()
		e.firewallMu.Lock()
		if p, err := e.loadSSH(); err == nil && time.Now().Unix() >= p.Deadline {
			retry, cancel := context.WithTimeout(ctx, 30*time.Second)
			if err = e.rollbackSSH(retry, p); err != nil {
				p.RollbackError = err.Error()
				_ = e.saveSSH(p)
			}
			cancel()
		}
		e.firewallMu.Unlock()
		e.sshMu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
