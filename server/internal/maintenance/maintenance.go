// Package maintenance owns the root-only ov entry point. Database work is
// delegated to the same executable, which drops to the opsview system user.
package maintenance

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/term"
	"io"
	"net/http"
	"opsview/internal/account"
	"opsview/internal/runtimeconfig"
	"opsview/internal/service"
	"opsview/internal/version"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var Commands = []string{"status", "start", "stop", "restart", "username", "password", "port", "info", "recover-access", "reset-2fa", "logs", "backup", "diagnose", "update", "ssl off"}
var labels = []string{"查看服务状态", "启动面板服务", "停止面板服务", "重启面板服务", "修改管理员用户名", "修改或重置密码", "修改面板端口", "查看登录链接和用户名", "清除面板 IP 访问限制", "关闭双因素认证", "查看服务日志", "备份面板配置", "导出诊断信息", "更新面板", "关闭 SSL 证书（切换 HTTP）"}

const panelUnit = "opsview-panel.service"
const executorUnit = "opsview-executor.service"

type Runner struct {
	In          *os.File
	Out, Err    io.Writer
	Executable  string
	reader      *bufio.Reader
	ConfigPath  string
	CheckHealth func(runtimeconfig.Config) error
	Executor    service.Executor
}

func (r *Runner) configPath() string {
	if r.ConfigPath != "" {
		return r.ConfigPath
	}
	return runtimeconfig.Path
}
func (r *Runner) health(c runtimeconfig.Config) error {
	if r.CheckHealth != nil {
		return r.CheckHealth(c)
	}
	return Health(c)
}
func Run(args []string) error {
	if os.Geteuid() != 0 {
		return errors.New("ov 维护命令需要 root，请使用 sudo ov")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return err
	}
	r := &Runner{In: os.Stdin, Out: os.Stdout, Err: os.Stderr, Executable: exe}
	r.reader = bufio.NewReader(r.In)
	if len(args) == 0 {
		if !term.IsTerminal(int(r.In.Fd())) {
			r.help()
			return nil
		}
		return r.menu()
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		r.help()
		return nil
	}
	return r.command(args)
}
func (r *Runner) help() {
	fmt.Fprintf(r.Out, "OpsView v%s 终端管理\n用法：ov <命令>\n", version.Current)
	for i, c := range Commands {
		fmt.Fprintf(r.Out, "%2d  %-16s %s\n", i+1, c, labels[i])
	}
	fmt.Fprintln(r.Out, "    init             显式初始化随机管理员（仅未初始化时）\n密码输入：ov password [--generate | --stdin]\n端口修改：ov port <端口> [--source IP/CIDR] [--yes]\n地址修正：ov info --public-host <域名或IPv4>\n版本更新：ov update [--version v0.1.3-rc.17]\n防火墙事务：ov firewall status|rollback [--yes]\n网页入口：ov panel on|off\n高级安全：ov security status|recover [--yes]\n备份恢复：ov restore --file <.ovp> [--switch --yes]")
}
func (r *Runner) line(prompt string) (string, error) {
	fmt.Fprint(r.Out, prompt)
	v, e := r.reader.ReadString('\n')
	if e != nil {
		return "", e
	}
	return strings.TrimSpace(v), nil
}
func (r *Runner) confirm(prompt string, yes bool) error {
	if yes {
		return nil
	}
	if !term.IsTerminal(int(r.In.Fd())) {
		return errors.New("非交互操作需要显式 --yes 确认")
	}
	s, e := r.line(prompt + " [y/N]：")
	if e != nil {
		return e
	}
	if s != "y" && s != "Y" {
		return errors.New("已取消")
	}
	return nil
}
func (r *Runner) menu() error {
	for {
		fmt.Fprintf(r.Out, "\nOpsView v%s 终端管理\n", version.Current)
		for i, l := range labels {
			fmt.Fprintf(r.Out, "%2d. %s\n", i+1, l)
		}
		fmt.Fprintln(r.Out, " 0. 退出")
		v, e := r.line("请输入编号：")
		if errors.Is(e, io.EOF) {
			return nil
		}
		if e != nil {
			return e
		}
		if v == "0" {
			return nil
		}
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > len(Commands) {
			fmt.Fprintf(r.Out, "无效编号，请输入 0–%d\n", len(Commands))
			continue
		}
		if e = r.command(strings.Fields(Commands[n-1])); e != nil {
			if errors.Is(e, io.EOF) {
				return nil
			}
			fmt.Fprintln(r.Err, "操作失败：", e)
		}
	}
}
func (r *Runner) exec(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = r.In
	cmd.Stdout = r.Out
	cmd.Stderr = r.Err
	return cmd.Run()
}
func (r *Runner) metadata(command string, input any) ([]byte, error) {
	var in bytes.Buffer
	if input != nil {
		if e := json.NewEncoder(&in).Encode(input); e != nil {
			return nil, e
		}
	}
	cmd := exec.Command(r.Executable, command, "--data", "/var/lib/opsview/panel")
	cmd.Stdin = &in
	cmd.Stderr = r.Err
	return cmd.Output()
}
func (r *Runner) command(args []string) error {
	command := args[0]
	args = args[1:]
	// Maintenance operations and deployments share one lock. ov update lets the bootstrap acquire it.
	if command != "update" && command != "logs" && command != "status" {
		lock, e := os.OpenFile("/run/lock/opsview-install.lock", os.O_CREATE|os.O_RDWR, 0600)
		if e != nil {
			return e
		}
		defer lock.Close()
		if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
			return errors.New("另一个 OpsView 维护操作正在运行")
		}
		defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	}
	switch command {
	case "panel":
		return r.panel(args)
	case "security":
		return r.security(args)
	case "restore":
		return r.restore(args)
	case "firewall":
		return r.firewall(args)
	case "ssl":
		return r.ssl(args)
	case "entry":
		if len(args) != 1 || args[0] != "reset" {
			return errors.New("用法：ov entry reset")
		}
		return r.resetEntry()
	case "status":
		return r.exec("systemctl", "--no-pager", "status", panelUnit, executorUnit)
	case "start":
		if e := r.exec("systemctl", "start", executorUnit); e != nil {
			return e
		}
		return r.exec("systemctl", "start", panelUnit)
	case "stop", "restart":
		return r.exec("systemctl", command, panelUnit)
	case "logs":
		return r.exec("journalctl", "-u", panelUnit, "-u", executorUnit, "-n", "100", "--no-pager")
	case "recover-access", "backup", "diagnose":
		if command == "recover-access" {
			if len(args) > 1 || (len(args) == 1 && args[0] != "--all") {
				return errors.New("用法：ov recover-access [--all]")
			}
			unlock, err := runtimeconfig.Lock(r.configPath())
			if err != nil {
				return err
			}
			defer unlock()
			_ = os.Remove("/var/lib/opsview/managed/panel-pending.json")
			cfg, er := runtimeconfig.Load(r.configPath())
			if er != nil {
				return er
			}
			empty := ""
			cfg.AllowedCIDRs = &empty
			if len(args) == 1 && args[0] == "--all" {
				cfg.Entry = ""
				cfg.AllowedHost = ""
			}
			if er = runtimeconfig.Save(r.configPath(), cfg); er != nil {
				return er
			}
		}
		op := command
		if op == "backup" {
			op = "backup-config"
		}
		b, e := r.metadata(op, nil)
		if e == nil {
			fmt.Fprint(r.Out, string(b))
		}
		return e
	case "init":
		if len(args) != 0 {
			return errors.New("用法：ov init")
		}
		b, e := r.metadata("admin-init", nil)
		if e != nil {
			return e
		}
		var c credentials
		if e = json.Unmarshal(b, &c); e != nil {
			return e
		}
		fmt.Fprintf(r.Out, "用户名：%s\n密码：%s\n密码仅显示一次，请立即保存。\n", c.Username, c.Password)
		return nil
	case "username":
		var name string
		if len(args) == 1 {
			name = args[0]
		} else if len(args) == 0 {
			if !term.IsTerminal(int(r.In.Fd())) {
				return errors.New("用法：ov username <新用户名>")
			}
			v, e := r.line("新用户名（留空取消）：")
			if e != nil {
				return e
			}
			name = v
		} else {
			return errors.New("用法：ov username <新用户名>")
		}
		if !account.ValidUsername(name) {
			return errors.New("已取消或用户名无效")
		}
		_, e := r.metadata("admin-change", map[string]any{"username": name})
		if e == nil {
			fmt.Fprintln(r.Out, "用户名已修改，旧会话已失效；双因素配置保留。")
		}
		return e
	case "password":
		password, e := r.password(args)
		if e != nil {
			return e
		}
		generated := password == ""
		if generated {
			_, password, e = account.RandomCredentials()
			if e != nil {
				return e
			}
		}
		_, e = r.metadata("admin-change", map[string]any{"password": password})
		if e == nil {
			fmt.Fprintln(r.Out, "密码已修改，旧会话已失效；双因素配置保留。")
			if generated {
				fmt.Fprintf(r.Out, "新密码：%s\n密码仅显示一次，请立即保存。\n", password)
			}
		}
		return e
	case "reset-2fa":
		yes := len(args) == 1 && args[0] == "--yes"
		if len(args) > 0 && !yes {
			return errors.New("用法：ov reset-2fa [--yes]")
		}
		if e := r.confirm("确认关闭管理员双因素认证并使旧会话失效？", yes); e != nil {
			return e
		}
		_, e := r.metadata("admin-change", map[string]any{"resetTOTP": true})
		if e == nil {
			fmt.Fprintln(r.Out, "双因素认证已关闭，旧会话已失效。")
		}
		return e
	case "info":
		return r.info(args)
	case "port":
		return r.port(args)
	case "update":
		if len(args) != 0 && (len(args) != 2 || args[0] != "--version" || !regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.-]+)?$`).MatchString(args[1])) {
			return errors.New("用法：ov update [--version v0.1.1]")
		}
		return r.exec("bash", append([]string{"/opt/opsview/bin/opsview-install.sh", "--update"}, args...)...)
	default:
		r.help()
		return fmt.Errorf("未知命令：%s", command)
	}
}
func (r *Runner) password(args []string) (string, error) {
	if len(args) == 1 && args[0] == "--generate" {
		return "", nil
	}
	if len(args) == 1 && args[0] == "--stdin" {
		b, e := io.ReadAll(io.LimitReader(r.reader, 74))
		if e != nil {
			return "", e
		}
		s := strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r")
		if !account.ValidPassword(s) {
			return "", errors.New("密码须为 12–72 字节")
		}
		return s, nil
	}
	if len(args) != 0 {
		return "", errors.New("用法：ov password [--generate | --stdin]")
	}
	if !term.IsTerminal(int(r.In.Fd())) {
		return "", errors.New("交互修改密码需要终端；可使用 --generate 或 --stdin")
	}
	fmt.Fprint(r.Out, "新密码（留空生成随机密码）：")
	a, e := term.ReadPassword(int(r.In.Fd()))
	fmt.Fprintln(r.Out)
	if e != nil {
		return "", e
	}
	if len(a) == 0 {
		return "", nil
	}
	fmt.Fprint(r.Out, "再次输入新密码：")
	b, e := term.ReadPassword(int(r.In.Fd()))
	fmt.Fprintln(r.Out)
	if e != nil {
		return "", e
	}
	if !bytes.Equal(a, b) {
		return "", errors.New("两次密码不一致")
	}
	if !account.ValidPassword(string(a)) {
		return "", errors.New("密码须为 12–72 字节")
	}
	return string(a), nil
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (r *Runner) info(args []string) error {
	if len(args) > 0 {
		unlock, err := runtimeconfig.Lock(r.configPath())
		if err != nil {
			return err
		}
		defer unlock()
		if _, er := os.Stat("/var/lib/opsview/managed/panel-pending.json"); er == nil {
			return errors.New("面板访问设置等待确认，请先确认或恢复访问")
		}
	}
	c, e := runtimeconfig.Load(r.configPath())
	if e != nil {
		return e
	}
	if len(args) > 0 {
		if len(args) != 2 || args[0] != "--public-host" || !runtimeconfig.ValidHost(args[1]) {
			return errors.New("用法：ov info --public-host <域名或IPv4>")
		}
		c.PublicHost = args[1]
		if e = runtimeconfig.Save(r.configPath(), c); e != nil {
			return e
		}
	}
	fmt.Fprintf(r.Out, "OpsView v%s\n监听地址：%s\n", version.Current, c.Address())
	host := c.PublicHost
	if host == "" {
		host = runtimeconfig.LocalHost()
		fmt.Fprintln(r.Out, "未配置公网地址，以下为本机地址。")
	}
	fmt.Fprintln(r.Out, "登录地址："+c.URL(host))
	b, e := r.metadata("admin-info", nil)
	if e != nil {
		return e
	}
	fmt.Fprint(r.Out, string(b))
	fmt.Fprintln(r.Out, "密码不保存，请通过 ov password 重置。\n公网入站连通性尚需验证，云安全组由用户配置。")
	return r.exec("systemctl", "--no-pager", "is-active", panelUnit, executorUnit)
}

func Health(c runtimeconfig.Config) error {
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // loopback/configured local host only
	for i := 0; i < 30; i++ {
		res, e := client.Get(c.LocalHealthURL())
		if e == nil {
			var v struct {
				Status string `json:"status"`
			}
			e = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&v)
			res.Body.Close()
			if e == nil && res.StatusCode == 200 && v.Status == "ok" {
				return nil
			}
		}
		time.Sleep(time.Second)
	}
	return errors.New("本机健康检查失败")
}

// CheckOverrides preserves drop-ins while refusing overrides that defeat port management.
func CheckOverrides() error {
	b, e := exec.Command("systemctl", "show", panelUnit, "--property=DropInPaths", "--value").Output()
	if e != nil {
		return e
	}
	re := regexp.MustCompile(`(?m)^\s*ExecStart\s*=`)
	for _, p := range strings.Fields(string(b)) {
		v, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		if re.Match(v) {
			return fmt.Errorf("systemd 启动覆盖与端口管理冲突，请先调整：%s", p)
		}
	}
	b, e = exec.Command("systemctl", "show", panelUnit, "--property=ExecStart", "--value").Output()
	if e != nil {
		return e
	}
	s := string(b)
	if !strings.Contains(s, "/opt/opsview/bin/opsview serve --config /etc/opsview/panel.json") && !strings.Contains(s, "/opt/opsview/bin/opsview serve --listen 0.0.0.0:18790 --data /var/lib/opsview/panel --socket /run/opsview/executor.sock") {
		return errors.New("现有 systemd ExecStart 与受控端口配置不兼容，请核对自定义服务启动配置")
	}
	return nil
}
