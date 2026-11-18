// Package hostfirewall contains the shared, structured host firewall adapter.
// It never accepts shell commands and never flushes a table or a zone.
package hostfirewall

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

type Rule struct {
	ID            string            `json:"id"`
	Revision      string            `json:"revision"`
	Kind          string            `json:"kind"`
	Protocol      string            `json:"protocol"`
	Ports         string            `json:"ports"`
	Source        string            `json:"source"`
	Destination   string            `json:"destination"`
	Action        string            `json:"action"`
	Direction     string            `json:"direction"`
	Family        string            `json:"family"`
	Zone          string            `json:"zone,omitempty"`
	Note          string            `json:"note"`
	CreatedAt     string            `json:"createdAt,omitempty"`
	Editable      bool              `json:"editable"`
	Reason        string            `json:"reason,omitempty"`
	Raw           string            `json:"raw,omitempty"`
	Position      int               `json:"position,omitempty"`
	Target        string            `json:"target,omitempty"`
	TargetPorts   string            `json:"targetPorts,omitempty"`
	Masquerade    bool              `json:"masquerade,omitempty"`
	Countries     []string          `json:"countries,omitempty"`
	Sets          map[string]string `json:"sets,omitempty"`
	DataUpdatedAt string            `json:"dataUpdatedAt,omitempty"`
	Runtime       bool              `json:"runtime"`
	Permanent     bool              `json:"permanent"`
	Listening     bool              `json:"listening"`
	Service       string            `json:"service,omitempty"`
}

func Hash(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
func (r Rule) Key() string {
	return strings.Join([]string{r.Kind, r.Protocol, r.Ports, r.Source, r.Destination, r.Action, r.Direction, r.Family, r.Zone, r.Target, r.TargetPorts, strconv.FormatBool(r.Masquerade), strings.Join(r.Countries, ",")}, "\x00")
}
func (r *Rule) Identify(backend, raw string, ordinal int) {
	r.Raw = raw
	r.Revision = Hash(backend + "\x00" + raw)
	r.ID = Hash(r.Revision + "\x00" + strconv.Itoa(ordinal))[:24]
}
func Address(s string, family string) (string, error) {
	if s == "" || s == "any" || s == "Anywhere" {
		return "any", nil
	}
	if a, e := netip.ParseAddr(s); e == nil {
		if a.Is4() != (family == "ipv4") {
			return "", errors.New("地址与 IPv4/IPv6 不匹配")
		}
		return a.String(), nil
	}
	p, e := netip.ParsePrefix(s)
	if e != nil || p.Addr().Is4() != (family == "ipv4") {
		return "", errors.New("请输入匹配地址族的 IP/CIDR")
	}
	if p.Bits() == 0 {
		return "any", nil
	}
	if p.Bits() == p.Addr().BitLen() {
		return p.Addr().String(), nil
	}
	return p.Masked().String(), nil
}
func PortSpec(s string, optional bool) (string, error) {
	if s == "" && optional {
		return "", nil
	}
	if len(s) > 2048 {
		return "", errors.New("端口列表过长")
	}
	type span struct{ a, b int }
	spans := []span{}
	for _, part := range strings.Split(strings.ReplaceAll(s, ":", "-"), ",") {
		v := strings.Split(strings.TrimSpace(part), "-")
		if len(v) > 2 {
			return "", errors.New("端口范围无效")
		}
		a, e := strconv.Atoi(v[0])
		if e != nil || a < 1 || a > 65535 {
			return "", errors.New("端口须为 1–65535")
		}
		b := a
		if len(v) == 2 {
			b, e = strconv.Atoi(v[1])
			if e != nil || b < a || b > 65535 {
				return "", errors.New("端口范围无效")
			}
		}
		spans = append(spans, span{a, b})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].a < spans[j].a })
	merged := []span{}
	for _, s := range spans {
		if len(merged) > 0 && s.a <= merged[len(merged)-1].b+1 {
			if s.b > merged[len(merged)-1].b {
				merged[len(merged)-1].b = s.b
			}
		} else {
			merged = append(merged, s)
		}
	}
	parts := []string{}
	for _, s := range merged {
		v := strconv.Itoa(s.a)
		if s.b > s.a {
			v += "-" + strconv.Itoa(s.b)
		}
		parts = append(parts, v)
	}
	return strings.Join(parts, ","), nil
}
func Contains(spec string, port int) bool {
	if spec == "" {
		return true
	}
	for _, p := range strings.Split(strings.ReplaceAll(spec, ":", "-"), ",") {
		v := strings.Split(p, "-")
		a, _ := strconv.Atoi(v[0])
		b := a
		if len(v) == 2 {
			b, _ = strconv.Atoi(v[1])
		}
		if port >= a && port <= b {
			return true
		}
	}
	return false
}
func (r *Rule) Validate() error {
	if r.Kind != "port" && r.Kind != "ip" && r.Kind != "forward" && r.Kind != "region" {
		return errors.New("规则类别无效")
	}
	if r.Family != "ipv4" && r.Family != "ipv6" {
		return errors.New("请选择 IPv4 或 IPv6")
	}
	if r.Direction != "in" && r.Direction != "out" {
		return errors.New("方向必须为入站或出站")
	}
	if r.Action != "allow" && r.Action != "deny" && r.Action != "reject" && r.Action != "limit" {
		return errors.New("策略无效")
	}
	if r.Protocol != "tcp" && r.Protocol != "udp" && r.Protocol != "tcp+udp" && !(r.Protocol == "any" && (r.Kind == "ip" || r.Kind == "region")) {
		return errors.New("协议无效")
	}
	var e error
	r.Source, e = Address(r.Source, r.Family)
	if e != nil {
		return e
	}
	r.Destination, e = Address(r.Destination, r.Family)
	if e != nil {
		return e
	}
	r.Ports, e = PortSpec(r.Ports, r.Kind == "ip" || r.Kind == "region")
	if e != nil {
		return e
	}
	if r.Kind == "ip" && (r.Ports != "" || r.Source == "any" && r.Destination == "any") {
		return errors.New("IP 规则需要指定 IP/CIDR，不能包含端口")
	}
	if len(r.Note) > 256 || strings.ContainsAny(r.Note, "\x00\r\n") {
		return errors.New("备注最多 256 字节，不允许换行")
	}
	if len(r.Zone) > 64 || strings.ContainsAny(r.Zone, " \t\r\n/\x00") {
		return errors.New("区域名称无效")
	}
	if r.Kind == "forward" {
		if r.Direction != "in" || r.Action != "allow" {
			return errors.New("转发必须为入站放行")
		}
		if r.Target != "" {
			a, err := netip.ParseAddr(r.Target)
			if err != nil || a.IsUnspecified() || a.IsMulticast() || a.Is4() != (r.Family == "ipv4") {
				return errors.New("转发目标须为同地址族的单个 IP")
			}
			r.Target = a.String()
		}
		r.TargetPorts, e = PortSpec(r.TargetPorts, false)
		if e != nil {
			return e
		}
		if strings.Contains(r.Ports, ",") || strings.Contains(r.TargetPorts, ",") {
			return errors.New("转发仅支持单端口或连续范围")
		}
		width := func(s string) int {
			p := strings.Split(s, "-")
			if len(p) == 1 {
				return 1
			}
			a, _ := strconv.Atoi(p[0])
			b, _ := strconv.Atoi(p[1])
			return b - a + 1
		}
		if width(r.Ports) > 1024 {
			return errors.New("单条转发最多 1024 个端口，请拆分规则")
		}
		if width(r.Ports) != width(r.TargetPorts) {
			return errors.New("入口与目标端口范围必须等长")
		}
	}
	if r.Kind == "region" && r.Ports != "" && r.Protocol == "any" {
		return errors.New("指定地区端口时请选择 TCP 或 UDP")
	}
	if r.Kind == "region" {
		if r.Direction != "in" || r.Source != "any" || r.Action != "allow" && r.Action != "deny" {
			return errors.New("地区规则仅支持入站、放行或阻止")
		}
		if len(r.Countries) == 0 || len(r.Countries) > 20 {
			return errors.New("请选择 1–20 个国家/地区")
		}
		sort.Strings(r.Countries)
		for _, c := range r.Countries {
			if len(c) != 2 || c[0] < 'A' || c[0] > 'Z' || c[1] < 'A' || c[1] > 'Z' {
				return errors.New("国家/地区代码无效")
			}
		}
	}
	return nil
}
func Expand(r Rule) []Rule {
	if r.Protocol != "tcp+udp" {
		return []Rule{r}
	}
	a, b := r, r
	a.Protocol = "tcp"
	b.Protocol = "udp"
	return []Rule{a, b}
}
func anyAddress(family string) string {
	if family == "ipv6" {
		return "::/0"
	}
	return "0.0.0.0/0"
}
func formatError(s string) error {
	return fmt.Errorf("防火墙操作失败：%s", strings.TrimSpace(s))
}

// Words only parses data returned by the installed firewall, never executes it.
func Words(s string) ([]string, error) {
	out := []string{}
	var b strings.Builder
	quote := rune(0)
	escaped, started := false, false
	for _, c := range s {
		if escaped {
			b.WriteRune(c)
			escaped = false
			started = true
			continue
		}
		if c == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			} else {
				b.WriteRune(c)
			}
			started = true
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			started = true
			continue
		}
		if c == ' ' || c == '\t' {
			if started {
				out = append(out, b.String())
				b.Reset()
				started = false
			}
			continue
		}
		b.WriteRune(c)
		started = true
	}
	if quote != 0 || escaped {
		return nil, errors.New("规则引号不完整")
	}
	if started {
		out = append(out, b.String())
	}
	return out, nil
}
