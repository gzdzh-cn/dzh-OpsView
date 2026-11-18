package hostfirewall

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Runner func(context.Context, string, ...string) (string, error)
type Client struct {
	Preferred  string
	Run        Runner
	ConfigDir  string
	Compatible func() bool
}
type Status struct {
	Backend    string   `json:"backend"`
	Installed  bool     `json:"installed"`
	Enabled    bool     `json:"enabled"`
	Compatible bool     `json:"compatible"`
	Reason     string   `json:"reason"`
	Rules      []Rule   `json:"rules"`
	Revision   string   `json:"revision"`
	Zones      []string `json:"zones"`
	Raw        string   `json:"raw"`
}

func Default() *Client {
	return &Client{Run: func(ctx context.Context, name string, args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		b, e := exec.CommandContext(ctx, name, args...).CombinedOutput()
		if e != nil {
			return string(b), fmt.Errorf("%s: %w: %s", name, e, strings.TrimSpace(string(b)))
		}
		return string(b), nil
	}, ConfigDir: "/etc/ufw"}
}
func (c *Client) Detect(ctx context.Context) Status {
	s := Status{Rules: []Rule{}, Zones: []string{}}
	fw, fe := c.Run(ctx, "firewall-cmd", "--state")
	u, ue := c.Run(ctx, "ufw", "status")
	ufwActive := ue == nil && strings.Contains(u, "Status: active")
	firewalldActive := fe == nil && strings.TrimSpace(fw) == "running"
	if ufwActive && firewalldActive {
		s.Installed = true
		s.Reason = "UFW 与 firewalld 同时运行，请先在主机明确唯一管理后端"
		return s
	}
	if firewalldActive {
		s.Backend = "firewalld"
		s.Installed = true
		s.Enabled = true
		s.Compatible = true
		s.Raw = fw
		return s
	}
	if c.Preferred == "firewalld" && !ufwActive {
		s.Backend = "firewalld"
		s.Installed = true
		s.Compatible = true
		s.Reason = "firewalld 已停止，展示已保存规则"
		return s
	}
	if ue == nil {
		s.Backend = "ufw"
		s.Installed = true
		s.Enabled = ufwActive
		s.Compatible = c.Compatible == nil || c.Compatible()
		s.Raw = u
		if !s.Compatible {
			s.Reason = "检测到不兼容的自定义防火墙，仅提供诊断"
		}
		return s
	}
	// Offline firewalld can be started explicitly; offline rules are read without starting it.
	if _, e := exec.LookPath("firewall-offline-cmd"); e == nil {
		s.Backend = "firewalld"
		s.Installed = true
		s.Compatible = true
		s.Reason = "firewalld 已停止，展示已保存规则"
		return s
	}
	s.Reason = "未安装受支持的 UFW 或 firewalld，请先安装一个防火墙后端"
	return s
}
func (c *Client) Status(ctx context.Context) (Status, error) {
	s := c.Detect(ctx)
	if !s.Installed || s.Backend == "" {
		return s, nil
	}
	var err error
	if s.Backend == "ufw" {
		s.Rules, err = c.ufwRules()
	} else {
		s.Rules, s.Zones, err = c.firewalldRules(ctx, s.Enabled)
	}
	if err != nil {
		s.Compatible = false
		s.Reason = "读取系统规则失败：" + err.Error()
		return s, err
	}
	kernel := map[string]map[string]int{}
	if s.Backend == "ufw" && s.Enabled {
		for _, family := range []string{"ipv4", "ipv6"} {
			bin := "iptables-save"
			if family == "ipv6" {
				bin = "ip6tables-save"
			}
			text, err := c.Run(ctx, bin, "-t", "filter")
			if err != nil {
				return s, err
			}
			kernel[family] = map[string]int{}
			for _, r := range ParseUFW(text, family) {
				kernel[family][r.Key()]++
			}
		}
	}
	keys := []string{}
	for i := range s.Rules {
		r := &s.Rules[i]
		if s.Backend == "ufw" {
			r.Runtime = s.Enabled && kernel[r.Family][r.Key()] > 0
			if r.Runtime {
				kernel[r.Family][r.Key()]--
			}
			r.Permanent = true
		}
		keys = append(keys, r.ID+":"+r.Revision+":"+strconv.Itoa(r.Position)+":"+strconv.FormatBool(r.Runtime)+":"+strconv.FormatBool(r.Permanent))
	}
	sort.Strings(keys)
	s.Revision = Hash(strings.Join(keys, "\n") + strconv.FormatBool(s.Enabled))
	return s, nil
}
func (c *Client) ufwRules() ([]Rule, error) {
	rows := []Rule{}
	for _, family := range []string{"ipv4", "ipv6"} {
		file := "user.rules"
		if family == "ipv6" {
			file = "user6.rules"
		}
		b, err := os.ReadFile(filepath.Join(c.ConfigDir, file))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		rows = append(rows, ParseUFW(string(b), family)...)
	}
	return rows, nil
}
func ParseUFW(text, family string) []Rule {
	rows := []Rule{}
	occ := map[string]int{}
	position := 0
	tuple, previousTuple := "", ""
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "### tuple ### ") {
			tuple = line
			continue
		}
		if strings.HasPrefix(line, "### END RULES ###") {
			break
		}
		if !strings.HasPrefix(line, "-A ufw-user-input ") && !strings.HasPrefix(line, "-A ufw-user-output ") && !strings.HasPrefix(line, "-A ufw-user-forward ") && !strings.HasPrefix(line, "-A ufw6-user-input ") && !strings.HasPrefix(line, "-A ufw6-user-output ") && !strings.HasPrefix(line, "-A ufw6-user-forward ") {
			continue
		}
		if tuple != "" && tuple == previousTuple && len(rows) > 0 {
			last := &rows[len(rows)-1]
			last.Editable = false
			last.Reason = "包含多条底层匹配，保留原始规则"
			last.Raw += "\n" + line
			last.Identify("ufw:"+family, last.Raw, 1)
			continue
		}
		previousTuple = tuple
		position++
		r := Rule{Kind: "ip", Family: family, Direction: "in", Action: "allow", Protocol: "any", Source: "any", Destination: "any", Editable: true, Position: position, Permanent: true}
		if _, comment, ok := strings.Cut(tuple, " comment="); ok {
			h := strings.Fields(comment)
			if len(h) > 0 {
				if note, e := hex.DecodeString(h[0]); e == nil {
					r.Note = string(note)
				}
			}
		}
		fields, err := Words(line)
		if err != nil {
			r.Editable = false
			r.Reason = err.Error()
		}
		if len(fields) > 1 {
			switch {
			case strings.HasSuffix(fields[1], "-input"):
			case strings.HasSuffix(fields[1], "-output"):
				r.Direction = "out"
			default:
				r.Editable = false
				r.Reason = "高级路由规则，请通过原管理工具维护"
			}
		}
		for i := 2; i < len(fields); i++ {
			f := fields[i]
			next := func() string {
				if i+1 >= len(fields) {
					r.Editable = false
					return ""
				}
				i++
				return fields[i]
			}
			switch f {
			case "-p", "--protocol":
				r.Protocol = next()
			case "-s", "--source":
				r.Source = next()
			case "-d", "--destination":
				r.Destination = next()
			case "--dport", "--dports", "--destination-port", "--destination-ports":
				r.Ports = strings.ReplaceAll(next(), ":", "-")
				r.Kind = "port"
			case "-j", "--jump":
				switch next() {
				case "ACCEPT":
					r.Action = "allow"
				case "DROP":
					r.Action = "deny"
				case "REJECT":
					r.Action = "reject"
				default:
					r.Editable = false
				}
			case "--comment":
				r.Note = next()
			case "-m":
				m := next()
				if m != "tcp" && m != "udp" && m != "multiport" && m != "comment" {
					r.Editable = false
					r.Reason = "包含高级匹配条件"
				}
			case "--reject-with":
				_ = next()
				r.Editable = false
				r.Reason = "含自定义拒绝响应"
			default:
				r.Editable = false
				r.Reason = "含不支持的匹配条件：" + f
			}
		}
		if r.Source == anyAddress(family) {
			r.Source = "any"
		}
		if r.Destination == anyAddress(family) {
			r.Destination = "any"
		}
		if r.Editable {
			copy := r
			if e := copy.Validate(); e != nil {
				r.Editable = false
				r.Reason = "高级系统规则：" + e.Error()
			} else {
				r.Ports = copy.Ports
				r.Source = copy.Source
				r.Destination = copy.Destination
			}
		}
		occ[line]++
		r.Identify("ufw:"+family, tuple+"\n"+line, occ[line])
		rows = append(rows, r)
	}
	return rows
}
func UFWArgs(r Rule) []string {
	action := r.Action
	args := []string{action, r.Direction}
	if r.Protocol != "any" {
		args = append(args, "proto", r.Protocol)
	}
	source, dest := r.Source, r.Destination
	if source == "any" {
		source = anyAddress(r.Family)
	}
	if dest == "any" {
		dest = anyAddress(r.Family)
	}
	args = append(args, "from", source, "to", dest)
	if r.Ports != "" {
		args = append(args, "port", strings.ReplaceAll(r.Ports, "-", ":"))
	}
	if r.Note != "" {
		args = append(args, "comment", r.Note)
	}
	return args
}
func (c *Client) SetEnabled(ctx context.Context, backend string, enabled bool) error {
	if backend == "ufw" {
		action := "disable"
		if enabled {
			action = "enable"
		}
		_, e := c.Run(ctx, "ufw", "--force", action)
		return e
	}
	if err := c.CheckForeignTables(ctx); err != nil {
		return err
	}
	action := "disable"
	if enabled {
		action = "enable"
	}
	args := []string{action, "--now", "firewalld.service"}
	_, e := c.Run(ctx, "systemctl", args...)
	return e
}
func (c *Client) Change(ctx context.Context, backend string, r Rule, remove bool) error {
	if r.Service == "" {
		if err := r.Validate(); err != nil {
			return err
		}
	}
	if r.Kind != "port" && r.Kind != "ip" && r.Kind != "forward" {
		return errors.New("该规则由扩展适配器管理")
	}
	if backend == "ufw" {
		args := UFWArgs(r)
		if remove {
			args = append([]string{"--force", "delete"}, args...)
		} else if r.Position > 0 {
			rows, err := c.ufwRules()
			if err != nil {
				return err
			}
			count, offset := 0, 0
			for _, row := range rows {
				if row.Family == r.Family {
					count++
				}
				if row.Family == "ipv4" && r.Family == "ipv6" {
					offset++
				}
			}
			// UFW insert cannot insert beyond the last family rule. Append handles
			// restoring/replacing the last item without shifting the other family.
			if r.Position <= count {
				args = append([]string{"insert", strconv.Itoa(r.Position + offset)}, args...)
			}
		}
		_, e := c.Run(ctx, "ufw", args...)
		return e
	}
	rule := RichRule(r)
	args := []string{"--zone=" + r.Zone}
	if r.Direction == "out" {
		if err := c.ensureEgress(ctx); err != nil {
			return err
		}
		args = []string{"--policy=opsview-egress"}
	}
	if r.Zone == "" {
		return errors.New("缺少 firewalld 区域")
	}
	flag := "--add-rich-rule="
	if remove {
		flag = "--remove-rich-rule="
		if r.Raw != "" {
			rule = r.Raw
		}
	}
	if strings.HasPrefix(r.Service, "@port:") {
		flag = "--add-port="
		if remove {
			flag = "--remove-port="
		}
		rule = strings.TrimPrefix(r.Service, "@port:")
	} else if strings.HasPrefix(r.Service, "@forward:") {
		flag = "--add-forward-port="
		if remove {
			flag = "--remove-forward-port="
		}
		rule = strings.TrimPrefix(r.Service, "@forward:")
	} else if r.Service != "" {
		flag = "--add-service="
		if remove {
			flag = "--remove-service="
		}
		rule = r.Service
	}
	// Only touch this rule in each plane. Never runtime-to-permanent or reload a zone.
	active, e := c.Run(ctx, "firewall-cmd", "--state")
	if e != nil || strings.TrimSpace(active) != "running" {
		_, e = c.Run(ctx, "firewall-offline-cmd", append(args, flag+rule)...)
		return e
	}
	if !remove && r.Raw == "" || r.Permanent {
		if _, e = c.Run(ctx, "firewall-cmd", append(append([]string{}, args...), "--permanent", flag+rule)...); e != nil {
			return e
		}
	}
	if !remove && r.Raw == "" || r.Runtime {
		_, e = c.Run(ctx, "firewall-cmd", append(args, flag+rule)...)
	}
	return e
}
func RichRule(r Rule) string {
	q := func(s string) string { return strconv.Quote(s) }
	parts := []string{"rule", "family=" + q(r.Family)}
	if r.Note == "opsview-access" {
		parts = append(parts, "priority="+q("-32768"))
	}
	// Preserve explicit rich priorities; the caller must not simplify unsupported rules.
	if r.Source != "any" {
		parts = append(parts, "source address="+q(r.Source))
	}
	if r.Destination != "any" {
		parts = append(parts, "destination address="+q(r.Destination))
	}
	if r.Kind == "port" {
		parts = append(parts, "port port="+q(r.Ports)+" protocol="+q(r.Protocol))
	}
	if r.Kind == "forward" {
		s := "forward-port port=" + q(r.Ports) + " protocol=" + q(r.Protocol) + " to-port=" + q(r.TargetPorts)
		if r.Target != "" {
			s += " to-addr=" + q(r.Target)
		}
		return strings.Join(append(parts, s), " ")
	}
	a := map[string]string{"allow": "accept", "deny": "drop", "reject": "reject"}[r.Action]
	parts = append(parts, a)
	return strings.Join(parts, " ")
}
func ParseRich(raw, zone string) Rule {
	r := Rule{Kind: "ip", Protocol: "any", Source: "any", Destination: "any", Direction: "in", Action: "allow", Family: "ipv4", Zone: zone, Editable: true, Raw: raw}
	f, e := Words(raw)
	if e != nil {
		r.Editable = false
		r.Reason = e.Error()
		return r
	}
	context := ""
	for _, v := range f {
		key, val, kv := strings.Cut(v, "=")
		if kv {
			switch key {
			case "family":
				r.Family = val
			case "priority":
				if val != "-32768" {
					r.Editable = false
				} else {
					r.Note = "opsview-access"
				}
			case "address":
				if context == "source" {
					r.Source = val
				} else if context == "destination" {
					r.Destination = val
				} else {
					r.Editable = false
				}
			case "to-port":
				r.TargetPorts = val
				r.Kind = "forward"
			case "to-addr":
				r.Target = val
				r.Kind = "forward"
			case "port":
				r.Ports = val
				r.Kind = "port"
			case "protocol":
				r.Protocol = val
			default:
				r.Editable = false
			}
		} else {
			switch v {
			case "rule":
			case "forward-port":
				r.Kind = "forward"
				context = v
			case "source", "destination", "port":
				context = v
			case "accept":
				r.Action = "allow"
			case "drop":
				r.Action = "deny"
			case "reject":
				r.Action = "reject"
			default:
				r.Editable = false
			}
		}
	}
	if r.TargetPorts != "" {
		r.Kind = "forward"
	}
	if r.Editable {
		if e := r.Validate(); e != nil {
			r.Editable = false
			r.Reason = e.Error()
		}
	}
	if !r.Editable && r.Reason == "" {
		r.Reason = "高级 firewalld 规则，保留原始条件"
	}
	return r
}
func (c *Client) firewalldRules(ctx context.Context, active bool) ([]Rule, []string, error) {
	if active {
		if rules, zones, err := c.fastFirewalldRules(ctx); err == nil {
			return rules, zones, nil
		}
	}
	bin := "firewall-cmd"
	if !active {
		bin = "firewall-offline-cmd"
	}
	zonesText, err := c.Run(ctx, bin, "--get-zones")
	if err != nil {
		return nil, nil, err
	}
	zones := strings.Fields(zonesText)
	byKey := map[string]*Rule{}
	out := []Rule{}
	planes := []bool{true}
	if active {
		planes = append(planes, false)
	}
	for _, permanent := range planes {
		for _, zone := range zones {
			args := []string{"--zone=" + zone}
			if active && permanent {
				args = append(args, "--permanent")
			}
			add := func(r Rule, raw string) {
				key := zone + "\x00" + raw
				if p := byKey[key]; p != nil {
					if permanent {
						p.Permanent = true
					} else {
						p.Runtime = true
					}
					return
				}
				r.Identify("firewalld:"+zone, raw, 1)
				r.Permanent = permanent
				r.Runtime = !permanent
				cp := r
				byKey[key] = &cp
			}
			text, e := c.Run(ctx, bin, append(args, "--list-rich-rules")...)
			if e != nil {
				return nil, zones, e
			}
			for _, raw := range strings.Split(strings.TrimSpace(text), "\n") {
				if raw != "" {
					add(ParseRich(raw, zone), raw)
				}
			}
			text, e = c.Run(ctx, bin, append(args, "--list-ports")...)
			if e != nil {
				return nil, zones, e
			}
			for _, p := range strings.Fields(text) {
				port, proto, ok := strings.Cut(p, "/")
				if !ok {
					continue
				}
				{
					family := "dual"
					r := Rule{Kind: "port", Ports: port, Protocol: proto, Family: family, Direction: "in", Action: "allow", Source: "any", Destination: "any", Zone: zone, Editable: true, Service: "@port:" + p, Raw: "port:" + p}
					add(r, "port:"+p+":"+family)
				}
			}
			text, e = c.Run(ctx, bin, append(args, "--list-services")...)
			if e != nil {
				return nil, zones, e
			}
			for _, service := range strings.Fields(text) {
				r := Rule{Kind: "port", Ports: service, Protocol: "service", Family: "dual", Direction: "in", Action: "allow", Source: "any", Destination: "any", Zone: zone, Editable: true, Reason: "区域服务附件；修改将影响该服务全部端口", Service: service}
				add(r, "service:"+service)
			}
		}
	}
	for _, permanent := range planes {
		args := []string{"--policy=opsview-egress", "--list-rich-rules"}
		if active && permanent {
			args = append([]string{"--permanent"}, args...)
		}
		text, er := c.Run(ctx, bin, args...)
		if er == nil {
			for _, raw := range strings.Split(strings.TrimSpace(text), "\n") {
				if raw == "" {
					continue
				}
				key := "egress:" + raw
				if existing := byKey[key]; existing != nil {
					if permanent {
						existing.Permanent = true
					} else {
						existing.Runtime = true
					}
					continue
				}
				r := ParseRich(raw, "opsview-egress")
				r.Direction = "out"
				r.Identify("firewalld:egress", raw, 1)
				r.Runtime = !permanent
				r.Permanent = permanent
				byKey[key] = &r
			}
		}
	}
	for _, r := range byKey {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, zones, nil
}

// Validate the candidate before removing the running rule. Neither command
// installs rules or executes user-supplied command text.
func (c *Client) ValidateCandidate(ctx context.Context, backend string, r Rule) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if r.Kind == "region" || r.Kind == "forward" {
		return nil
	}
	if backend == "ufw" {
		_, err := c.Run(ctx, "ufw", append([]string{"--dry-run"}, UFWArgs(r)...)...)
		return err
	}
	if r.Action == "limit" {
		return errors.New("firewalld 不支持此 UFW 频率限制策略")
	}
	_, err := c.Run(ctx, "python3", "-c", "import sys;from firewall.core.rich import Rich_Rule;Rich_Rule(rule_str=sys.argv[1])", RichRule(r))
	return err
}
