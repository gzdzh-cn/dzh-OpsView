package maintenance

import (
	"errors"
	"fmt"
	"net"
	"opsview/internal/runtimeconfig"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func sourceValid(s string) bool {
	if net.ParseIP(s) != nil {
		return true
	}
	_, _, e := net.ParseCIDR(s)
	return e == nil
}
func (r *Runner) port(args []string) error {
	value, source := "", ""
	yes := false
	for len(args) > 0 {
		switch args[0] {
		case "--yes":
			yes = true
			args = args[1:]
		case "--source":
			if len(args) < 2 {
				return errors.New("--source 缺少 IP/CIDR")
			}
			source = args[1]
			args = args[2:]
		default:
			if value != "" {
				return errors.New("用法：ov port <端口> [--source IP/CIDR] [--yes]")
			}
			value = args[0]
			args = args[1:]
		}
	}
	if value == "" {
		if yes {
			return errors.New("缺少新端口")
		}
		v, e := r.line("新面板端口（留空取消）：")
		if e != nil {
			return e
		}
		value = v
	}
	port, e := strconv.Atoi(value)
	if e != nil || port < 1 || port > 65535 {
		return errors.New("端口须为 1–65535")
	}
	unlock, lockErr := runtimeconfig.Lock(r.configPath())
	if lockErr != nil {
		return lockErr
	}
	defer unlock()
	if _, err := os.Stat("/var/lib/opsview/managed/panel-pending.json"); err == nil {
		return errors.New("网页访问设置等待确认，请先确认或执行 ov recover-access --all")
	}
	old, e := runtimeconfig.Load(r.configPath())
	if e != nil {
		return e
	}
	if port == old.Port {
		fmt.Fprintln(r.Out, "端口未变化。")
		return nil
	}
	if e = CheckOverrides(); e != nil {
		return e
	}
	next := old
	next.Port = port
	listener, e := net.Listen("tcp", next.Address())
	if e != nil {
		return fmt.Errorf("端口已占用或不能监听：%w", e)
	}
	listener.Close()
	fmt.Fprintf(r.Out, "面板将从 TCP %d 改为 TCP %d，需要重启。旧防火墙规则保留；请同步放行云安全组。\n", old.Port, port)
	if e = r.confirm("确认修改端口？", yes); e != nil {
		return e
	}
	active := compatibleUFW()
	var undo []string
	if active {
		if source == "" {
			ssh := strings.Fields(os.Getenv("SSH_CONNECTION"))
			if len(ssh) > 0 && net.ParseIP(ssh[0]) != nil {
				source = ssh[0]
			}
		}
		if !yes {
			v, e := r.line(fmt.Sprintf("新端口允许来源 IP/CIDR（当前 %s，留空保留）：", source))
			if e != nil {
				return e
			}
			if v != "" {
				source = v
			}
		}
		if !sourceValid(source) {
			return errors.New("启用 UFW 时必须指定有效来源，可用 --source IP/CIDR")
		}
		rule := []string{"allow", "from", source, "to", "any", "port", strconv.Itoa(port), "proto", "tcp", "comment", "opsview-panel"}
		before, e := exec.Command("ufw", "show", "added").Output()
		if e != nil {
			return e
		}
		// UFW's canonical output is used to avoid deleting an existing user rule on rollback.
		existed := existingUFWRule(string(before), source, port)

		if !existed {
			if e = r.exec("ufw", rule...); e != nil {
				return e
			}
			undo = append([]string{"--force", "delete"}, rule...)
		}
	} else {
		fmt.Fprintln(r.Out, "未检测到兼容且启用的 UFW，仅修改面板配置。请手动检查宿主机防火墙及云安全组。")
	}
	committed := false
	defer func() {
		if !committed && len(undo) > 0 {
			if err := r.exec("ufw", undo...); err != nil {
				fmt.Fprintln(r.Err, "撤销本次 UFW 规则失败，请人工核对：", err)
			}
		}
	}()
	if e = runtimeconfig.Save(r.configPath(), next); e != nil {
		return e
	}
	if e = r.exec("systemctl", "restart", panelUnit); e == nil {
		e = r.health(next)
	}
	if e != nil {
		restoreErr := runtimeconfig.Save(r.configPath(), old)
		restartErr := r.exec("systemctl", "restart", panelUnit)
		return fmt.Errorf("新端口不可用，已回退（配置恢复：%v，服务重启：%v）：%w", restoreErr, restartErr, e)
	}
	committed = true
	fmt.Fprintf(r.Out, "端口已改为 %d。\n", port)
	return r.info(nil)
}
func compatibleUFW() bool { return runtimeconfig.CompatibleUFW() }
func existingUFWRule(output, source string, port int) bool {
	return runtimeconfig.ExistingUFWRule(output, source, port)
}
