package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var sshAccountName = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,30}$`)
var sshLoginPattern = regexp.MustCompile(`(?:Accepted|Failed) (\S+) for (?:invalid user )?(\S+) from (\S+) port (\d+)`)

func parseSSHLogins(text string) model.Config {
	rows := []model.Config{}
	success, failed := 0, 0
	for _, line := range strings.Split(text, "\n") {
		status := ""
		if strings.Contains(line, "Accepted ") {
			status = "成功"
			success++
		} else if strings.Contains(line, "Failed ") || strings.Contains(line, "Invalid user") || strings.Contains(line, "authentication failure") {
			status = "失败"
			failed++
		}
		if status == "" {
			continue
		}
		fields := strings.Fields(line)
		row := model.Config{"status": status, "detail": line, "time": "", "username": "—", "source": "—", "method": "—"}
		if len(fields) > 0 {
			row["time"] = fields[0]
		}
		if m := sshLoginPattern.FindStringSubmatch(line); len(m) > 0 {
			row["method"], row["username"], row["source"] = m[1], m[2], m[3]
		}
		rows = append(rows, row)
	}
	return model.Config{"rows": rows, "content": text, "success": success, "failed": failed, "notice": "所选时间范围最多 2000 行 journal 采样；计数为已读取记录，日志轮转或截断时不代表完整登录总量"}
}
func (e *Engine) readSSHLogins(ctx context.Context, period string) (model.Config, error) {
	since := map[string]string{"": "24 hours ago", "24h": "24 hours ago", "7d": "7 days ago", "30d": "30 days ago"}[period]
	if since == "" {
		return nil, errors.New("登录日志时间范围无效")
	}
	text, err := output(ctx, "journalctl", "_COMM=sshd", "--since", since, "-n", "2000", "--no-pager", "-o", "short-iso")
	if err != nil {
		return nil, errors.New("无法读取 SSH journal 日志")
	}
	return parseSSHLogins(text), nil
}

// Account operations never accept a shell, path, UID, group, or sudo policy.
// Passwords travel only over the authenticated Unix socket and chpasswd stdin.
func sshLocalUsers() ([]model.Config, string, error) {
	b, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return nil, "", err
	}
	shadow, err := os.ReadFile("/etc/shadow")
	if err != nil {
		return nil, "", err
	}
	return parseSSHLocalUsers(b, shadow), security.Hash(string(b) + string(shadow)), nil
}
func parseSSHLocalUsers(b, shadow []byte) []model.Config {
	states := map[string]string{}
	for _, line := range strings.Split(string(shadow), "\n") {
		p := strings.Split(line, ":")
		if len(p) > 1 {
			states[p[0]] = p[1]
		}
	}
	rows := []model.Config{}
	for _, line := range strings.Split(string(b), "\n") {
		p := strings.Split(line, ":")
		if len(p) != 7 {
			continue
		}
		uid, err := strconv.Atoi(p[2])
		if err != nil || (uid != 0 && (uid < 1000 || uid >= 65534)) {
			continue
		}
		if p[0] != "root" && (!sshAccountName.MatchString(p[0]) || strings.HasPrefix(p[0], "opsview")) {
			continue
		}
		hash := states[p[0]]
		rows = append(rows, model.Config{"username": p[0], "uid": uid, "home": p[5], "shell": p[6], "locked": strings.HasPrefix(hash, "!") || strings.HasPrefix(hash, "*") || hash == "", "loginShell": p[6] == "/bin/bash" || p[6] == "/bin/sh", "revision": security.Hash(line + "\x00" + hash), "root": uid == 0})
	}
	return rows
}
func (e *Engine) managedSSHUsers() map[string]bool {
	rows := map[string]bool{}
	b, _ := os.ReadFile(filepath.Join(e.Root, "ssh-managed-users.json"))
	_ = json.Unmarshal(b, &rows)
	if rows == nil {
		rows = map[string]bool{}
	}
	return rows
}
func (e *Engine) sshUsers() (model.Config, error) {
	rows, _, err := sshLocalUsers()
	if err != nil {
		return nil, err
	}
	owned := e.managedSSHUsers()
	for _, r := range rows {
		r["managed"] = owned[r.String("username")]
	}
	return model.Config{"rows": rows, "notice": "本机 root 与普通账号；不修改 sudo 权限。密码锁定不撤销公钥或现有会话。仅允许删除由 OpsView 创建且无运行进程的普通账号，保留主目录。"}, nil
}
func (e *Engine) changeSSHUser(ctx context.Context, p model.Config) (model.Config, error) {
	if !p.Bool("confirmedImpact") {
		return nil, errors.New("请确认系统账号修改影响")
	}
	if _, err := os.Stat(e.sshPendingPath()); !os.IsNotExist(err) {
		return nil, errors.New("SSH 配置尚待确认，暂不可修改账号")
	}
	if _, err := os.Stat(e.sshServicePath()); !os.IsNotExist(err) {
		return nil, errors.New("SSH 服务停用待确认，暂不可修改账号")
	}
	name, action := p.String("username"), p.String("operation")
	if !sshAccountName.MatchString(name) || strings.HasPrefix(name, "opsview") {
		return nil, errors.New("账号名称无效或属于受保护账号")
	}
	rows, _, err := sshLocalUsers()
	if err != nil {
		return nil, err
	}
	var current model.Config
	for _, row := range rows {
		if row.String("username") == name {
			current = row
		}
	}
	owned := e.managedSSHUsers()
	if action == "create" {
		if name == "root" || current != nil {
			return nil, errors.New("系统账号已存在")
		}
		if _, err := output(ctx, "getent", "passwd", name); err == nil {
			return nil, errors.New("账号已存在于本地或外部身份源")
		}
		// A newly-created account remains password locked until an explicit reset.
		if err = run(ctx, func(string) {}, "", nil, nil, "useradd", "--create-home", "--shell", "/bin/bash", "--", name); err != nil {
			return nil, errors.New("创建系统账号失败，请检查主机用户策略")
		}
		owned[name] = true
		b, _ := json.Marshal(owned)
		if err = security.WriteAtomic(filepath.Join(e.Root, "ssh-managed-users.json"), b, 0600); err != nil {
			return nil, errors.New("账号已创建，但管理归属保存失败，请在主机核对；账号仍为密码锁定状态")
		}
		return model.Config{"updated": true, "notice": "账号已创建，密码默认锁定；请设置密码或在主机配置该账号公钥"}, nil
	}
	if current == nil {
		return nil, errors.New("不支持此系统或外部身份源账号")
	}
	if p.String("revision") != current.String("revision") {
		return nil, &model.RevisionConflict{Revision: current.String("revision")}
	}
	if current.Int("uid") == 0 && (name != "root" || action != "password") {
		return nil, errors.New("禁止锁定或删除 root")
	}
	switch action {
	case "password":
		password := p.String("password")
		if len(password) < 12 || len(password) > 256 || strings.ContainsAny(password, "\x00\r\n:") {
			return nil, errors.New("密码须为 12–256 字节，不得包含冒号、换行或空字符")
		}
		cmd := exec.CommandContext(ctx, "chpasswd")
		cmd.Stdin = strings.NewReader(name + ":" + password + "\n")
		if err = cmd.Run(); err != nil {
			return nil, errors.New("系统密码修改失败，请检查主机密码策略")
		}
	case "lock":
		if err = run(ctx, func(string) {}, "", nil, nil, "usermod", "--lock", "--", name); err != nil {
			return nil, errors.New("密码锁定失败")
		}
	case "unlock":
		// usermod refuses unlocking accounts without a valid underlying hash.
		if err = run(ctx, func(string) {}, "", nil, nil, "usermod", "--unlock", "--", name); err != nil {
			return nil, errors.New("解锁失败，请先设置有效密码")
		}
	case "delete":
		if !owned[name] {
			return nil, errors.New("已有系统账号只能在主机删除")
		}
		cmd := exec.CommandContext(ctx, "pgrep", "-u", strconv.Itoa(current.Int("uid")))
		err = cmd.Run()
		if err == nil {
			return nil, errors.New("账号仍有运行进程，请先在主机确认并停止")
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return nil, errors.New("无法检查账号进程，拒绝删除")
		}
		if err = run(ctx, func(string) {}, "", nil, nil, "userdel", "--", name); err != nil {
			return nil, errors.New("账号删除失败，主目录保留")
		}
		delete(owned, name)
		b, _ := json.Marshal(owned)
		if err = security.WriteAtomic(filepath.Join(e.Root, "ssh-managed-users.json"), b, 0600); err != nil {
			return nil, errors.New("账号已删除且主目录保留，管理记录清理失败")
		}
	default:
		return nil, errors.New("账号操作无效")
	}
	return model.Config{"updated": true}, nil
}

type pendingSSHService struct {
	Unit     string `json:"unit"`
	Enabled  bool   `json:"enabled"`
	Deadline int64  `json:"deadline"`
	Error    string `json:"error,omitempty"`
}

func (e *Engine) sshServicePath() string { return filepath.Join(e.Root, "ssh-service-pending.json") }
func (e *Engine) loadSSHService() (pendingSSHService, error) {
	var p pendingSSHService
	b, err := os.ReadFile(e.sshServicePath())
	if err == nil {
		err = json.Unmarshal(b, &p)
	}
	return p, err
}
func (e *Engine) restoreSSHService(ctx context.Context, p pendingSSHService) error {
	if p.Unit != "ssh.service" && p.Unit != "sshd.service" {
		return errors.New("SSH 服务恢复记录无效")
	}
	action := "disable"
	if p.Enabled {
		action = "enable"
	}
	if err := systemctl(ctx, func(string) {}, action, p.Unit); err != nil {
		return err
	}
	if err := systemctl(ctx, func(string) {}, "start", p.Unit); err != nil {
		return err
	}
	settings, _, err := e.sshEffective(ctx, e.sshPath())
	if err != nil {
		return err
	}
	if err = probeSSHPorts(ctx, settings.Ports); err != nil {
		return err
	}
	return os.Remove(e.sshServicePath())
}
func (e *Engine) sshServiceUnit(ctx context.Context) (string, error) {
	return e.sshUnitState(ctx, true)
}
func (e *Engine) changeSSHService(ctx context.Context, p model.Config) (model.Config, error) {
	if !p.Bool("confirmedImpact") {
		return nil, errors.New("请确认 SSH 服务影响及备用控制台")
	}
	if _, err := os.Stat(e.sshPendingPath()); !os.IsNotExist(err) {
		return nil, errors.New("SSH 配置修改待确认")
	}
	if err := e.ensureFirewallIdle(); err != nil {
		return nil, err
	}
	unit, err := e.sshServiceUnit(ctx)
	if err != nil {
		return nil, err
	}
	action := p.String("operation")
	if action != "start" && action != "stop" && action != "restart" {
		return nil, errors.New("SSH 服务操作无效")
	}
	if old, err := e.loadSSHService(); err == nil {
		if action == "start" {
			return model.Config{"restored": true}, e.restoreSSHService(ctx, old)
		}
		return nil, errors.New("SSH 停用尚待确认，请先恢复或确认")
	}
	if action == "stop" {
		running, _ := output(ctx, "systemctl", "is-active", unit)
		if strings.TrimSpace(running) != "active" {
			return nil, errors.New("SSH 已停止")
		}
		enabled, _ := output(ctx, "systemctl", "is-enabled", unit)
		mode := strings.TrimSpace(enabled)
		if mode != "enabled" && mode != "disabled" {
			return nil, errors.New("SSH 使用静态、运行时或自定义启动状态，请在主机管理")
		}
		pending := pendingSSHService{Unit: unit, Enabled: strings.TrimSpace(enabled) == "enabled", Deadline: time.Now().Add(120 * time.Second).Unix()}
		b, _ := json.Marshal(pending)
		if err = security.WriteAtomic(e.sshServicePath(), b, 0600); err != nil {
			return nil, err
		}
		if err = run(ctx, func(string) {}, "", nil, nil, "systemctl", "disable", "--now", unit); err != nil {
			_ = e.restoreSSHService(ctx, pending)
			return nil, err
		}
		return model.Config{"deadline": pending.Deadline}, nil
	}
	if err = systemctl(ctx, func(string) {}, action, unit); err != nil {
		return nil, err
	}
	settings, _, err := e.sshEffective(ctx, e.sshPath())
	if err == nil {
		err = probeSSHPorts(ctx, settings.Ports)
	}
	return model.Config{"updated": true}, err
}
func (e *Engine) confirmSSHService(p model.Config) error {
	old, err := e.loadSSHService()
	if err != nil {
		return errors.New("没有待确认的 SSH 停用")
	}
	if time.Now().Unix() >= old.Deadline {
		return errors.New("已超时，等待自动恢复")
	}
	if !p.Bool("consoleConfirmed") {
		return fmt.Errorf("请先验证云控制台或备用管理通道")
	}
	return os.Remove(e.sshServicePath())
}
