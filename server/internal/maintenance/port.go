package maintenance

import (
	"errors"
	"fmt"
	"net"
	"opsview/internal/runtimeconfig"
	"os"
	"os/exec"
	"regexp"
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
func compatibleUFW() bool {
	b, e := exec.Command("ufw", "status").Output()
	if e != nil || !strings.Contains(string(b), "Status: active") {
		return false
	}
	// An additional input hook is not an environment we can safely manage automatically.
	if b, e = exec.Command("nft", "list", "ruleset").Output(); e == nil {
		hooks := regexp.MustCompile(`(?s)chain\s+([^\s{]+)\s*\{[^{}]*?\bhook\s+input\b`)
		for _, hook := range hooks.FindAllStringSubmatch(string(b), -1) {
			if hook[1] != "INPUT" && !strings.HasPrefix(strings.ToLower(hook[1]), "ufw") {
				return false
			}
		}
		re := regexp.MustCompile(`(?s)table\s+(?:inet|ip|ip6)\s+([^\s{]+)\s*\{(.*?)\n\}`)
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			if strings.Contains(m[2], "hook input") && !strings.Contains(strings.ToLower(m[1]), "ufw") && m[1] != "filter" {
				return false
			}
		}
	}
	for _, tool := range []string{"iptables", "ip6tables"} {
		b, e = exec.Command(tool, "-S", "INPUT").Output()
		if e == nil {
			for _, line := range strings.Split(string(b), "\n") {
				if strings.HasPrefix(line, "-A INPUT ") && !strings.Contains(line, "ufw-") && !strings.Contains(line, "ufw6-") {
					return false
				}
			}
		}
	}
	return true
}

func normalizedSource(source string) string {
	if ip := net.ParseIP(source); ip != nil {
		if ip.To4() != nil {
			return ip.String() + "/32"
		}
		return ip.String() + "/128"
	}
	if _, network, e := net.ParseCIDR(source); e == nil {
		return network.String()
	}
	return source
}

// UFW normalizes argument order and host CIDRs in 'show added'. Compare meaning,
// including proto/from/to, rather than the order originally supplied by the user.
func existingUFWRule(output, source string, port int) bool {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != "ufw" || fields[1] != "allow" {
			continue
		}
		foundSource, foundPort, proto, to := "", "", "", ""
		for i := 2; i < len(fields); i++ {
			if fields[i] == "comment" {
				break
			}
			if i+1 < len(fields) {
				switch fields[i] {
				case "from":
					foundSource = fields[i+1]
				case "port":
					foundPort = fields[i+1]
				case "proto":
					proto = fields[i+1]
				case "to":
					to = fields[i+1]
				}
			}
		}
		if normalizedSource(foundSource) == normalizedSource(source) && foundPort == strconv.Itoa(port) && proto == "tcp" && to == "any" {
			return true
		}
	}
	return false
}
