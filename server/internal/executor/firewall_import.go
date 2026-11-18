package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"opsview/internal/hostfirewall"
	"opsview/internal/model"
	"opsview/internal/security"
	"strconv"
	"strings"
	"time"
)

type firewallPreviewRow struct {
	Rule   hostfirewall.Rule `json:"rule"`
	Status string            `json:"status"`
	Reason string            `json:"reason,omitempty"`
	Index  int               `json:"index"`
}
type firewallPreview struct {
	Token    string               `json:"token"`
	Revision string               `json:"revision"`
	Rows     []firewallPreviewRow `json:"rows"`
	Expires  int64                `json:"expires"`
}

func cleanFirewallInput(r hostfirewall.Rule) hostfirewall.Rule {
	r.Sets = nil
	r.Raw = ""
	r.ID = ""
	r.Revision = ""
	r.Position = 0
	r.Service = ""
	r.Runtime = false
	r.Permanent = false
	r.CreatedAt = ""
	r.Editable = true
	r.Reason = ""
	return r
}
func (e *Engine) previewFirewallImport(p model.Config, s hostfirewall.Status) (model.Config, error) {
	content := p.String("content")
	if len(content) == 0 || len(content) > 10<<20 {
		return nil, errors.New("规则文件必须为 1 字节至 10 MiB")
	}
	rules, issues := parseFirewallImport(content, p.String("kind"))
	if len(rules)+len(issues) > 10000 {
		return nil, errors.New("单次导入最多 10000 条")
	}
	preview := firewallPreview{Token: security.ID(), Revision: s.Revision, Rows: []firewallPreviewRow{}, Expires: time.Now().Add(15 * time.Minute).Unix()}
	seen := map[string]bool{}
	for _, r := range append(s.Rules, e.firewallExtensionsStatus(context.Background(), s)...) {
		seen[r.Key()] = true
	}
	for _, r := range rules {
		r = cleanFirewallInput(r)
		status, reason := "ready", ""
		if err := r.Validate(); err != nil {
			status = "unsupported"
			reason = err.Error()
		} else if seen[r.Key()] {
			status = "duplicate"
			reason = "系统或文件中已存在相同规则"
		} else {
			seen[r.Key()] = true
		}
		preview.Rows = append(preview.Rows, firewallPreviewRow{Rule: r, Status: status, Reason: reason, Index: len(preview.Rows)})
	}
	for _, issue := range issues {
		preview.Rows = append(preview.Rows, firewallPreviewRow{Status: "unsupported", Reason: issue, Index: len(preview.Rows)})
	}
	if err := e.writeFirewallAdmin("import-preview", preview); err != nil {
		return nil, err
	}
	return model.Config{"token": preview.Token, "revision": preview.Revision, "rows": preview.Rows, "expires": preview.Expires}, nil
}
func parseFirewallImport(content, kind string) ([]hostfirewall.Rule, []string) {
	rules := []hostfirewall.Rule{}
	issues := []string{}
	var document any
	appendObject := func(value any) {
		b, _ := json.Marshal(value)
		var fields map[string]any
		if json.Unmarshal(b, &fields) != nil {
			issues = append(issues, "规则必须为对象")
			return
		}
		var r hostfirewall.Rule
		if _, ok := fields["kind"]; ok {

			// Reject unknown conditions instead of silently broadening the rule.
			var known map[string]json.RawMessage
			template, _ := json.Marshal(hostfirewall.Rule{})
			_ = json.Unmarshal(template, &known)
			extra := []string{"zone", "createdAt", "reason", "raw", "position", "target", "targetPorts", "masquerade", "countries", "sets", "dataUpdatedAt", "service", "unsupported"}
			for _, key := range extra {
				known[key] = nil
			}
			for key := range fields {
				if _, ok := known[key]; !ok {
					issues = append(issues, "OpsView 规则包含不支持的条件："+key)
					return
				}
			}
			if fields["service"] != nil && fields["service"] != "" {
				issues = append(issues, "服务附件需要按原区域纳管，不能转换为普通端口规则")
				return
			}
			if json.Unmarshal(b, &r) != nil {
				issues = append(issues, "OpsView 规则字段格式无效")
				return
			}
			if v, ok := fields["unsupported"].(bool); ok && v || fields["editable"] == false {
				issues = append(issues, "高级只读规则不能无损导入")
				return
			}
			if r.Family == "dual" {
				if r.Source != "any" || r.Destination != "any" {
					issues = append(issues, "双栈规则含有限定地址，请分开导入")
					return
				}
				r.Family = "ipv4"
				rules = append(rules, r)
				r.Family = "ipv6"
			}
			rules = append(rules, r)
			return
		}
		converted, err := convertBaoTaRule(fields, kind)
		if err != nil {
			issues = append(issues, err.Error())
			return
		}
		rules = append(rules, converted...)
	}
	if json.Unmarshal([]byte(content), &document) == nil {
		switch v := document.(type) {
		case []any:
			for _, item := range v {
				appendObject(item)
			}
			return rules, issues
		case map[string]any:
			if v["format"] == "opsview-firewall" {
				version, ok := v["version"].(float64)
				if !ok || version != 1 {
					return nil, []string{"不支持的 OpsView 导出格式版本"}
				}
			}
			if items, ok := v["rules"].([]any); ok {
				for _, item := range items {
					appendObject(item)
				}
			} else {
				appendObject(v)
			}
			return rules, issues
		}
	}
	for n, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var v any
		if json.Unmarshal([]byte(line), &v) == nil {
			appendObject(v)
			continue
		}
		f := strings.Split(strings.TrimSuffix(line, "|"), "|")
		m := map[string]any{}
		keys := []string{}
		switch kind {
		case "port":
			keys = []string{"id", "protocol", "ports", "types", "address", "brief", "addtime", "domain"}
		case "ip":
			keys = []string{"id", "types", "address", "brief", "addtime", "domain"}
		case "forward":
			keys = []string{"id", "start_port", "ended_ip", "ended_port", "protocol", "addtime"}
		case "region":
			keys = []string{"id", "types", "country", "brief", "addtime", "ports"}
		default:
			issues = append(issues, "宝塔分隔记录需选择规则类型")
			continue
		}
		if len(f) > len(keys) || len(f) < len(keys)-1 {
			issues = append(issues, fmt.Sprintf("第 %d 行字段数量不匹配", n+1))
			continue
		}
		for i, value := range f {
			m[keys[i]] = value
		}
		appendObject(m)
	}
	return rules, issues
}
func convertBaoTaRule(m map[string]any, kind string) ([]hostfirewall.Rule, error) {
	allowed := map[string]bool{}
	for _, key := range strings.Fields("id protocol ports types address brief addtime domain chain ip_type ip_version rule_name start_port ended_ip ended_port country") {
		allowed[key] = true
	}
	for key := range m {
		if !allowed[key] {
			return nil, fmt.Errorf("包含不支持的宝塔条件：%s", key)
		}
	}
	str := func(k string) string {
		if m[k] == nil {
			return ""
		}
		return fmt.Sprint(m[k])
	}
	if str("domain") != "" && str("domain") != "None" {
		return nil, errors.New("域名动态规则不支持导入，请保留原规则")
	}
	if str("rule_name") != "" {
		kind = map[string]string{"port_rule": "port", "ip_rule": "ip", "trans_rule": "forward", "country_rule": "region"}[str("rule_name")]
	}
	if kind == "" {
		switch {
		case m["start_port"] != nil:
			kind = "forward"
		case m["country"] != nil:
			kind = "region"
		case m["ports"] != nil:
			kind = "port"
		default:
			kind = "ip"
		}
	}
	r := hostfirewall.Rule{Kind: kind, Source: "any", Destination: "any", Family: "ipv4", Protocol: strings.ToLower(str("protocol")), Action: "allow", Direction: "in", Note: str("brief")}
	if r.Protocol == "" {
		if kind == "ip" || kind == "region" {
			r.Protocol = "any"
		} else {
			r.Protocol = "tcp"
		}
	}
	if r.Protocol == "tcp/udp" || r.Protocol == "tcp,udp" {
		r.Protocol = "tcp+udp"
	}
	switch strings.ToLower(str("types")) {
	case "drop", "deny":
		r.Action = "deny"
	case "accept", "allow", "":
	default:
		return nil, errors.New("未知宝塔策略，不能静默转换")
	}
	address := str("address")
	if address != "" && address != "all" && address != "所有IP" && address != "0.0.0.0/0" {
		r.Source = address
		if strings.Contains(address, ":") {
			r.Family = "ipv6"
		}
	}
	if direction := strings.ToLower(str("chain")); direction != "" {
		switch direction {
		case "input", "in":
		case "output", "out":
			r.Direction = "out"
		default:
			return nil, errors.New("不支持的宝塔规则方向")
		}
	}
	if str("ip_type") == "ipv6" || str("ip_version") == "ipv6" {
		r.Family = "ipv6"
	}
	r.Ports = str("ports")
	if kind == "forward" {
		r.Ports = str("start_port")
		r.Target = str("ended_ip")
		r.TargetPorts = str("ended_port")
		if r.Target == "127.0.0.1" {
			r.Target = ""
		}
		if strings.Contains(r.Target, ":") {
			r.Family = "ipv6"
		}
	}
	if kind == "region" {
		country := strings.ToUpper(str("country"))
		if len(country) != 2 {
			return nil, errors.New("地区导入需要 ISO 两字母国家/地区代码")
		}
		r.Countries = []string{country}
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	out := hostfirewall.Expand(r)
	// BaoTa all-IP records are dual stack unless an explicit family was exported.
	if address == "" || address == "all" || address == "所有IP" {
		if str("ip_type") == "" && str("ip_version") == "" && kind != "forward" {
			for _, row := range append([]hostfirewall.Rule{}, out...) {
				row.Family = "ipv6"
				out = append(out, row)
			}
		}
	}
	return out, nil
}
func (e *Engine) previewFirewallSync(s hostfirewall.Status) model.Config {
	var observed firewallObservation
	_ = e.readFirewallAdmin("observed", &observed)
	actual := map[string]hostfirewall.Rule{}
	for _, r := range s.Rules {
		actual[r.ID] = r
	}
	old := map[string]hostfirewall.Rule{}
	for _, r := range observed.Rules {
		old[r.ID] = r
	}
	rows := []model.Config{}
	for _, r := range s.Rules {
		if _, ok := old[r.ID]; !ok {
			rows = append(rows, model.Config{"rule": r, "change": "system-added", "defaultAction": "observe"})
		}
	}
	for _, r := range observed.Rules {
		if _, ok := actual[r.ID]; !ok {
			rows = append(rows, model.Config{"rule": r, "change": "system-missing", "defaultAction": "observe"})
		}
	}
	return model.Config{"revision": s.Revision, "rows": rows, "observedAt": observed.UpdatedAt}
}
func (e *Engine) selectedFirewallRules(p model.Config, s hostfirewall.Status, action string) ([]hostfirewall.Rule, error) {
	rows := []hostfirewall.Rule{}
	ids, ok := p["indices"].([]any)
	if !ok || len(ids) == 0 {
		return nil, errors.New("请选择预览中的规则")
	}
	if action == "import-apply" {
		var preview firewallPreview
		if err := e.readFirewallAdmin("import-preview", &preview); err != nil {
			return nil, err
		}
		if preview.Token != p.String("token") || preview.Revision != s.Revision || preview.Expires < time.Now().Unix() {
			return nil, errors.New("预览已过期或系统规则已变化，请重新预览")
		}
		for _, id := range ids {
			n, err := strconv.Atoi(fmt.Sprint(id))
			if err != nil || n < 0 || n >= len(preview.Rows) || preview.Rows[n].Status != "ready" {
				return nil, errors.New("选择中包含不可导入项")
			}
			rows = append(rows, hostfirewall.Expand(cleanFirewallInput(preview.Rows[n].Rule))...)
		}
	} else {
		var observed firewallObservation
		if err := e.readFirewallAdmin("observed", &observed); err != nil {
			return nil, err
		}
		for _, id := range ids {
			found := false
			for _, r := range observed.Rules {
				if r.ID == fmt.Sprint(id) {
					if !r.Editable {
						return nil, errors.New("高级规则不能自动恢复")
					}
					rows = append(rows, cleanFirewallInput(r))
					found = true
					break
				}
			}
			if !found {
				return nil, errors.New("历史规则不存在")
			}
		}
	}
	return rows, nil
}

func (e *Engine) selectedObservations(p model.Config, s hostfirewall.Status) []hostfirewall.Rule {
	ids, ok := p["ids"].([]any)
	if !ok || len(ids) == 0 {
		return s.Rules
	}
	selected := map[string]bool{}
	for _, id := range ids {
		selected[fmt.Sprint(id)] = true
	}
	var old firewallObservation
	_ = e.readFirewallAdmin("observed", &old)
	out := []hostfirewall.Rule{}
	for _, r := range old.Rules {
		if !selected[r.ID] {
			out = append(out, r)
		}
	}
	for _, r := range s.Rules {
		if selected[r.ID] {
			out = append(out, r)
		}
	}
	return out
}
