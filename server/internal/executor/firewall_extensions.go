package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"opsview/internal/hostfirewall"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type firewallPingRule struct {
	Zone      string `json:"zone"`
	Family    string `json:"family"`
	Runtime   bool   `json:"runtime"`
	Permanent bool   `json:"permanent"`
}
type firewallExtensionState struct {
	PingRules  []firewallPingRule  `json:"pingRules,omitempty"`
	Rules      []hostfirewall.Rule `json:"rules"`
	Ping       bool                `json:"ping"`
	Exceptions []hostfirewall.Rule `json:"exceptions,omitempty"`
}

func (e *Engine) firewallExtensionsStatus(ctx context.Context, s hostfirewall.Status) []hostfirewall.Rule {
	var state firewallExtensionState
	_ = e.readFirewallAdmin("extensions", &state)
	rows := []hostfirewall.Rule{}
	runtime4, runtime6 := "", ""
	if s.Enabled && s.Backend == "ufw" {
		runtime4, _ = e.hostFirewall().Run(ctx, "iptables-save")
		runtime6, _ = e.hostFirewall().Run(ctx, "ip6tables-save")
	}
	for _, r := range state.Rules {
		r.Permanent = true
		r.Runtime = false
		r.Editable = true
		sets, _ := json.Marshal(r.Sets)
		r.Revision = hostfirewall.Hash(r.Key() + r.Note + string(sets))
		if s.Enabled {
			if s.Backend == "ufw" {
				text := runtime4
				if r.Family == "ipv6" {
					text = runtime6
				}
				r.Runtime = strings.Contains(text, "ovfw-"+r.ID)
			} else {
				r.Runtime = true
				for _, raw := range extensionRichRules(r) {
					text, err := e.hostFirewall().Run(ctx, "firewall-cmd", "--zone="+r.Zone, "--query-rich-rule="+raw)
					if err != nil || strings.TrimSpace(text) != "yes" {
						r.Runtime = false
					}
				}
			}
		}
		rows = append(rows, r)
	}
	return rows
}
func (e *Engine) changeFirewallExtension(ctx context.Context, backend string, r hostfirewall.Rule, remove bool, log func(string)) error {
	if err := r.Validate(); err != nil {
		return err
	}
	var state firewallExtensionState
	_ = e.readFirewallAdmin("extensions", &state)
	old := state
	next := []hostfirewall.Rule{}
	for _, existing := range state.Rules {
		if existing.Key() != r.Key() {
			next = append(next, existing)
		}
	}
	if !remove {
		if r.ID == "" {
			r.ID = security.ID()
		}
		if r.Kind == "region" && len(r.Sets) == 0 {
			for _, country := range r.Countries {
				if err := e.prepareCountry(ctx, country, r.Family, false, log); err != nil {
					return err
				}
			}
			r.Sets = map[string]string{}
			for _, country := range r.Countries {
				name, er := e.versionCountry(country, r.Family)
				if er != nil {
					return er
				}
				r.Sets[country] = name
			}
			r.DataUpdatedAt = time.Now().UTC().Format(time.RFC3339)
		}
		next = append(next, r)
	}
	state.Rules = next
	if current, er := e.hostFirewall().Status(ctx); er == nil {
		state.Exceptions = nil
		for _, rule := range current.Rules {
			if rule.Note == "opsview-access" {
				state.Exceptions = append(state.Exceptions, rule)
			}
		}
	}
	s := e.hostFirewall().Detect(ctx)
	return e.commitFirewallExtensions(ctx, backend, old, state, s.Enabled, log)
}

const fwBegin = "# BEGIN OPSVIEW FIREWALL - managed block\n"
const fwEnd = "# END OPSVIEW FIREWALL\n"

func replaceFirewallBlock(original, block, table string) (string, error) {
	if start := strings.Index(original, fwBegin); start >= 0 {
		end := strings.Index(original[start:], fwEnd)
		if end < 0 {
			return "", errors.New("OpsView 规则块不完整，拒绝覆盖")
		}
		original = original[:start] + original[start+end+len(fwEnd):]
	}
	if block == "" {
		return original, nil
	}
	if table == "nat" {
		return original + "\n" + fwBegin + "*nat\n" + block + "COMMIT\n" + fwEnd, nil
	}
	start := strings.Index(original, "*filter\n")
	if start < 0 {
		return "", errors.New("UFW before 规则缺少 filter 表")
	}
	offset := start + len("*filter\n")
	lines := strings.SplitAfter(original[offset:], "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, ":") || strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			offset += len(line)
			continue
		}
		break
	}
	return original[:offset] + fwBegin + block + fwEnd + original[offset:], nil
}
func (e *Engine) extensionConfigPath(name string) string {
	if e.firewallConfigRoot != "" {
		return filepath.Join(e.firewallConfigRoot, name)
	}
	return filepath.Join("/etc", name)
}
func (e *Engine) applyFirewallExtensions(ctx context.Context, backend string, old, next firewallExtensionState, active bool, log func(string)) error {
	if backend == "firewalld" {
		return e.applyFirewalldExtensions(ctx, old, next, active, log)
	}
	// Both address families are validated before either file is activated.
	type change struct {
		path          string
		before, after []byte
	}
	changes := []change{}
	for _, family := range []string{"ipv4", "ipv6"} {
		file := "before.rules"
		if family == "ipv6" {
			file = "before6.rules"
		}
		path := filepath.Join(e.hostFirewall().ConfigDir, file)
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Remove our previous NAT block independently from the filter block.
		original := string(b)
		natBegin := "# BEGIN OPSVIEW NAT\n"
		natEnd := "# END OPSVIEW NAT\n"
		if start := strings.Index(original, natBegin); start >= 0 {
			end := strings.Index(original[start:], natEnd)
			if end < 0 {
				return errors.New("OpsView NAT 规则块不完整")
			}
			original = original[:start] + original[start+end+len(natEnd):]
		}
		filter, nat, err := e.renderUFWExtensions(ctx, next, family, log)
		if err != nil {
			return err
		}
		text, err := replaceFirewallBlock(original, filter, "filter")
		if err != nil {
			return err
		}
		if nat != "" {
			text += "\n" + natBegin + "*nat\n" + nat + "COMMIT\n" + natEnd
		}
		changes = append(changes, change{path, b, []byte(text)})
	}
	for i, ch := range changes {
		if err := security.WriteAtomic(ch.path, ch.after, 0640); err != nil {
			for _, prev := range changes[:i] {
				_ = security.WriteAtomic(prev.path, prev.before, 0640)
			}
			return err
		}
	}
	if active {
		for _, family := range []string{"ipv4", "ipv6"} {
			if err := e.detachFirewallNAT(ctx, family); err != nil {
				return err
			}
		}
		if _, err := e.hostFirewall().Run(ctx, "ufw", "reload"); err != nil {
			for _, ch := range changes {
				_ = security.WriteAtomic(ch.path, ch.before, 0640)
			}
			_, _ = e.hostFirewall().Run(context.Background(), "ufw", "reload")
			return err
		}
	}
	return nil
}
func (e *Engine) renderUFWExtensions(ctx context.Context, state firewallExtensionState, family string, log func(string)) (string, string, error) {
	before := "ufw-before-input"
	forward := "ufw-before-forward"
	icmp := "icmp"
	if family == "ipv6" {
		before = "ufw6-before-input"
		forward = "ufw6-before-forward"
		icmp = "ipv6-icmp"
	}
	filter := []string{":OPSVIEW-IN - [0:0]", "-A " + before + " -j OPSVIEW-IN"}
	nat := []string{}
	has := state.Ping
	for _, r := range state.Exceptions {
		if r.Family != family {
			continue
		}
		line := "-A OPSVIEW-IN -p tcp -s " + r.Source + " --dport " + r.Ports + " -j RETURN"
		filter = append(filter, line)
	}
	if state.Ping {
		filter = append(filter, "-A OPSVIEW-IN -p "+icmp+" "+map[string]string{"ipv4": "--icmp-type echo-request", "ipv6": "--icmpv6-type echo-request"}[family]+" -j DROP")
	}
	for _, r := range state.Rules {
		if r.Family != family {
			continue
		}
		has = true
		comment := " -m comment --comment ovfw-" + r.ID
		if r.Kind == "region" {
			for _, country := range r.Countries {
				set, err := e.ensureCountrySet(ctx, r.Sets[country], family, log)
				if err != nil {
					return "", "", err
				}
				line := "-A OPSVIEW-IN -m set --match-set " + set + " src"
				if r.Protocol != "any" {
					line += " -p " + r.Protocol
				}
				if r.Ports != "" {
					line += " -m multiport --dports " + strings.ReplaceAll(r.Ports, "-", ":")
				}
				target := "ACCEPT"
				if r.Action == "deny" {
					target = "DROP"
				}
				filter = append(filter, line+comment+" -j "+target)
			}
		}
		if r.Kind == "forward" {
			for _, r := range firewallForwardVariants(r) {
				line := "-A OPSVIEW-NAT -p " + r.Protocol
				if r.Source != "any" {
					line += " -s " + r.Source
				}
				if r.Destination != "any" {
					line += " -d " + r.Destination
				}
				line += " --dport " + strings.ReplaceAll(r.Ports, "-", ":") + comment
				if r.Target == "" {
					line += " -j REDIRECT --to-ports " + r.TargetPorts
					src := ""
					if r.Source != "any" {
						src = " -s " + r.Source
					}
					filter = append(filter, "-A OPSVIEW-IN -p "+r.Protocol+src+" --dport "+strings.ReplaceAll(r.TargetPorts, "-", ":")+" -m conntrack --ctstate DNAT --ctorigdstport "+strings.ReplaceAll(r.Ports, "-", ":")+comment+" -j ACCEPT")
				} else {
					target := r.Target
					if family == "ipv6" {
						target = "[" + target + "]"
					}
					line += " -j DNAT --to-destination " + target + ":" + r.TargetPorts
					src := ""
					if r.Source != "any" {
						src = " -s " + r.Source
					}
					filter = append(filter, "-A "+forward+" -p "+r.Protocol+src+" -d "+r.Target+" --dport "+strings.ReplaceAll(r.TargetPorts, "-", ":")+" -m conntrack --ctstate DNAT --ctorigdstport "+strings.ReplaceAll(r.Ports, "-", ":")+comment+" -j ACCEPT")
					if r.Masquerade {
						nat = append(nat, "-A OPSVIEW-SNAT -p "+r.Protocol+src+" -d "+r.Target+" --dport "+strings.ReplaceAll(r.TargetPorts, "-", ":")+" -m conntrack --ctstate DNAT --ctorigdstport "+strings.ReplaceAll(r.Ports, "-", ":")+comment+" -j MASQUERADE")
					}
					if err := e.ensureForwarding(ctx, family, log); err != nil {
						return "", "", err
					}
				}
				nat = append(nat, line)
			}
		}
	}
	if !has {
		return "", "", nil
	}
	filter = append(filter, "-A OPSVIEW-IN -j RETURN")
	natText := ""
	if len(nat) > 0 {
		natText = ":OPSVIEW-NAT - [0:0]\n:OPSVIEW-SNAT - [0:0]\n-A PREROUTING -j OPSVIEW-NAT\n-A POSTROUTING -j OPSVIEW-SNAT\n-A OPSVIEW-NAT -m conntrack --ctstate DNAT -j RETURN\n" + strings.Join(nat, "\n") + "\n"
	}
	return strings.Join(filter, "\n") + "\n", natText, nil
}
func (e *Engine) ensureForwarding(ctx context.Context, family string, log func(string)) error {
	key := "net.ipv4.ip_forward"
	if family == "ipv6" {
		key = "net.ipv6.conf.all.forwarding"
	}
	path := e.extensionConfigPath("sysctl.d/91-opsview-forward-" + family + ".conf")
	if err := security.WriteAtomic(path, []byte(key+" = 1\n"), 0644); err != nil {
		return err
	}
	if value, err := e.hostFirewall().Run(ctx, "sysctl", "-n", key); err == nil && strings.TrimSpace(value) == "1" {
		return nil
	}
	return run(ctx, log, "", nil, nil, "sysctl", "-w", key+"=1")
}
func countrySet(country, family string) string {
	return "ovfw-" + map[string]string{"ipv4": "4", "ipv6": "6"}[family] + "-" + strings.ToLower(country)
}
func (e *Engine) countryPath(country, family string) string {
	return filepath.Join(e.Root, "firewall-admin", "countries", family+"-"+country+".zone")
}
func validateCountry(country, family string) bool {
	return len(country) == 2 && country[0] >= 'A' && country[0] <= 'Z' && country[1] >= 'A' && country[1] <= 'Z' && (family == "ipv4" || family == "ipv6")
}
func (e *Engine) prepareCountry(ctx context.Context, country, family string, force bool, log func(string)) error {
	if !validateCountry(country, family) {
		return errors.New("国家或地址族无效")
	}
	path := e.countryPath(country, family)
	if st, err := os.Stat(path); err == nil && !force && time.Since(st.ModTime()) < 7*24*time.Hour {
		return nil
	}
	url := "https://www.ipdeny.com/ipblocks/data/aggregated/" + strings.ToLower(country) + "-aggregated.zone"
	if family == "ipv6" {
		url = "https://www.ipdeny.com/ipv6/ipaddresses/aggregated/" + strings.ToLower(country) + "-aggregated.zone"
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	client := http.Client{Transport: e.countryTransport, Timeout: 30 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if r.URL.Scheme != "https" || r.URL.Hostname() != "www.ipdeny.com" {
			return errors.New("地区数据下载拒绝跨站重定向")
		}
		if len(via) > 3 {
			return errors.New("重定向过多")
		}
		return nil
	}}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("地区数据下载失败 HTTP %d，保留现有数据", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, (8<<20)+1))
	if err != nil || len(b) > 8<<20 {
		return errors.New("地区数据过大或读取失败")
	}
	lines := []string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		p, er := netip.ParsePrefix(line)
		if er != nil || p.Addr().Is4() != (family == "ipv4") {
			return errors.New("地区数据包含无效 CIDR，保留旧集合")
		}
		lines = append(lines, p.Masked().String())
	}
	if len(lines) == 0 {
		return errors.New("地区数据为空，保留旧集合")
	}
	sort.Strings(lines)
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err = security.WriteAtomic(filepath.Join(filepath.Dir(path), "Copyrights.txt"), []byte(hostfirewall.IPdenyCopyright), 0600); err != nil {
		return err
	}
	if err = security.WriteAtomic(path, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		return err
	}
	log("地区数据已校验：" + country + " " + family + "，" + strconv.Itoa(len(lines)) + " 条网段")
	return e.writeFirewallAdmin("country-"+family+"-"+country, model.Config{"source": url, "sha256": hostfirewall.Hash(string(b)), "updatedAt": time.Now().UTC().Format(time.RFC3339)})
}
func (e *Engine) ensureCountrySet(ctx context.Context, name, family string, log func(string)) (string, error) {
	if !strings.HasPrefix(name, "ovfw-") || strings.ContainsAny(name, "/\\ \n") {
		return "", errors.New("地区地址集标识无效")
	}
	b, err := os.ReadFile(filepath.Join(e.Root, "firewall-admin", "countries", "sets", name+".zone"))
	if err != nil {
		return "", err
	}
	inet := "inet"
	if family == "ipv6" {
		inet = "inet6"
	}
	temp := name + "-new"
	text := "create " + name + " hash:net family " + inet + " maxelem 1048576 -exist\ncreate " + temp + " hash:net family " + inet + " maxelem 1048576 -exist\nflush " + temp + "\n"
	for _, line := range strings.Fields(string(b)) {
		text += "add " + temp + " " + line + " -exist\n"
	}
	text += "swap " + temp + " " + name + "\ndestroy " + temp + "\n"
	if err = run(ctx, log, "", nil, strings.NewReader(text), "ipset", "restore", "-exist"); err != nil {
		return "", err
	}
	// Boot ordering restores only OpsView sets before UFW restores its references.
	path := e.extensionConfigPath("opsview/firewall-ipsets.restore")
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	all := ""
	files, _ := filepath.Glob(filepath.Join(e.Root, "firewall-admin", "countries", "sets", "*.zone"))
	for _, file := range files {
		set := strings.TrimSuffix(filepath.Base(file), ".zone")
		fields := strings.Split(set, "-")
		if len(fields) != 4 {
			continue
		}
		fam := "ipv4"
		if fields[1] == "6" {
			fam = "ipv6"
		}
		i := "inet"
		if fam == "ipv6" {
			i = "inet6"
		}
		data, er := os.ReadFile(file)
		if er != nil {
			return "", er
		}
		all += "create " + set + " hash:net family " + i + " maxelem 1048576 -exist\n"
		for _, line := range strings.Fields(string(data)) {
			all += "add " + set + " " + line + " -exist\n"
		}
	}
	if err = security.WriteAtomic(path, []byte(all), 0600); err != nil {
		return "", err
	}
	unit := "[Unit]\nDescription=OpsView country address sets\nBefore=ufw.service\n[Service]\nType=oneshot\nExecStart=/usr/sbin/ipset restore -exist -file " + path + "\nRemainAfterExit=yes\n[Install]\nWantedBy=multi-user.target\n"
	if err = security.WriteAtomic(e.extensionConfigPath("systemd/system/opsview-firewall-sets.service"), []byte(unit), 0644); err != nil {
		return "", err
	}
	if _, err = e.hostFirewall().Run(ctx, "systemctl", "daemon-reload"); err != nil {
		return "", err
	}
	if _, err = e.hostFirewall().Run(ctx, "systemctl", "enable", "opsview-firewall-sets.service"); err != nil {
		return "", err
	}
	return name, nil
}
func extensionRichRule(r hostfirewall.Rule) string {
	q := strconv.Quote
	parts := []string{"rule family=" + q(r.Family)}
	if r.Source != "any" {
		parts = append(parts, "source address="+q(r.Source))
	}
	if r.Destination != "any" {
		parts = append(parts, "destination address="+q(r.Destination))
	}
	if r.Kind == "forward" {
		v := "forward-port port=" + q(r.Ports) + " protocol=" + q(r.Protocol) + " to-port=" + q(r.TargetPorts)
		if r.Target != "" {
			v += " to-addr=" + q(r.Target)
		}
		parts = append(parts, v)
	} else {
		parts = append(parts, "source ipset="+q(r.Sets[r.Countries[0]]))
		if r.Ports != "" {
			parts = append(parts, "port port="+q(r.Ports)+" protocol="+q(r.Protocol))
		}
		if r.Action == "allow" {
			parts = append(parts, "accept")
		} else {
			parts = append(parts, "drop")
		}
	}
	return strings.Join(parts, " ")
}

// Explicit one-to-one mapping avoids NAT port pools randomly selecting a port.
func firewallForwardVariants(r hostfirewall.Rule) []hostfirewall.Rule {
	if r.Kind != "forward" || !strings.Contains(r.Ports, "-") {
		return []hostfirewall.Rule{r}
	}
	src := strings.Split(r.Ports, "-")
	dst := strings.Split(r.TargetPorts, "-")
	first, _ := strconv.Atoi(src[0])
	last, _ := strconv.Atoi(src[1])
	target, _ := strconv.Atoi(dst[0])
	out := []hostfirewall.Rule{}
	for port := first; port <= last; port++ {
		v := r
		v.Ports = strconv.Itoa(port)
		v.TargetPorts = strconv.Itoa(target + port - first)
		out = append(out, v)
	}
	return out
}
func extensionRichRules(r hostfirewall.Rule) []string {
	if r.Kind != "region" {
		out := []string{}
		for _, v := range firewallForwardVariants(r) {
			out = append(out, extensionRichRule(v))
		}
		if r.Masquerade && r.Target != "" {
			raw := "rule family=" + strconv.Quote(r.Family)
			if r.Source != "any" {
				raw += " source address=" + strconv.Quote(r.Source)
			}
			raw += " destination address=" + strconv.Quote(r.Target) + " masquerade"
			out = append(out, raw)
		}
		return out
	}
	out := []string{}
	ports := strings.Split(r.Ports, ",")
	for _, country := range r.Countries {
		for _, port := range ports {
			v := r
			v.Countries = []string{country}
			v.Ports = port
			out = append(out, extensionRichRule(v))
		}
	}
	return out
}
func (e *Engine) applyFirewalldExtensions(ctx context.Context, old, next firewallExtensionState, active bool, log func(string)) error {
	c := e.hostFirewall()
	bin := "firewall-cmd"
	if !active {
		bin = "firewall-offline-cmd"
	}
	// Prepare immutable sets before changing a live rule. Initialization may need
	// a reload and must therefore precede any runtime/permanent divergence.
	for _, r := range next.Rules {
		if r.Kind == "region" {
			for _, cc := range r.Countries {
				if err := e.ensureFirewalldCountry(ctx, r.Sets[cc], r.Family, active, log); err != nil {
					return err
				}
			}
		}
		if r.Kind == "forward" && r.Target != "" {
			if err := e.ensureForwarding(ctx, r.Family, log); err != nil {
				return err
			}
		}
	}
	type entry struct {
		zone, raw string
		permanent bool
	}
	flatten := func(state firewallExtensionState) map[string]entry {
		out := map[string]entry{}
		add := func(zone, raw string, runtime, permanent bool) {
			if permanent {
				out[zone+"\x00"+raw+"permanent"] = entry{zone, raw, true}
			}
			if runtime && active {
				out[zone+"\x00"+raw+"runtime"] = entry{zone, raw, false}
			}
		}
		for _, r := range state.Rules {
			for _, raw := range extensionRichRules(r) {
				add(r.Zone, raw, true, true)
			}
		}
		for _, r := range state.PingRules {
			add(r.Zone, firewallPingRich(r.Family), r.Runtime, r.Permanent)
		}
		return out
	}
	before, after := flatten(old), flatten(next)
	for _, change := range []struct {
		rows, other map[string]entry
		flag        string
	}{{after, before, "--add-rich-rule="}, {before, after, "--remove-rich-rule="}} {
		keys := []string{}
		for key := range change.rows {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if _, exists := change.other[key]; exists {
				continue
			}
			r := change.rows[key]
			args := []string{"--zone=" + r.zone, change.flag + r.raw}
			if active && r.permanent {
				args = append([]string{"--permanent"}, args...)
			}
			if _, err := c.Run(ctx, bin, args...); err != nil {
				return err
			}
		}
	}

	return nil
}
func (e *Engine) ensureFirewalldCountry(ctx context.Context, name, family string, active bool, log func(string)) error {
	c := e.hostFirewall()
	path := filepath.Join(e.Root, "firewall-admin", "countries", "sets", name+".zone")
	if !strings.HasPrefix(name, "ovfw-") || strings.ContainsAny(name, "/\\ \n") {
		return errors.New("地区地址集标识无效")
	}
	if _, err := os.Stat(path); err != nil {
		return err
	}
	bin := "firewall-cmd"
	prefix := []string{"--permanent"}
	if !active {
		bin = "firewall-offline-cmd"
		prefix = nil
	}
	_, permanentErr := c.Run(ctx, bin, append(append([]string{}, prefix...), "--info-ipset="+name)...)
	if active {
		if _, err := c.Run(ctx, "firewall-cmd", "--info-ipset="+name); err == nil {
			return nil
		}
		if permanentErr != nil {
			if err := c.SafeReloadReady(ctx); err != nil {
				return err
			}
		} else {
			return errors.New("地区集合仅存在于永久配置，请先检查 firewalld 未完成的配置差异")
		}
	}
	inet := "inet"
	if family == "ipv6" {
		inet = "inet6"
	}
	if permanentErr != nil {
		if _, err := c.Run(ctx, bin, append(append([]string{}, prefix...), "--new-ipset="+name, "--type=hash:net", "--option=family="+inet, "--option=maxelem=1048576")...); err != nil {
			return err
		}
	}
	if _, err := c.Run(ctx, bin, append(append([]string{}, prefix...), "--ipset="+name, "--add-entries-from-file="+path)...); err != nil {
		return err
	}
	if active {
		// Recheck all foreign objects after creating our permanent-only set; another
		// local administrator may have made a temporary change during preparation.
		if err := c.SafeReloadIgnoringSets(ctx, map[string]bool{name: true}); err != nil {
			if permanentErr != nil {
				_, _ = c.Run(context.Background(), "firewall-cmd", "--permanent", "--delete-ipset="+name)
			}
			return err
		}
		log("初始化独立地区集合：已核对运行和永久配置一致")
		if _, err := c.Run(ctx, "firewall-cmd", "--reload"); err != nil {
			return err
		}
	}
	return nil
}
func (e *Engine) versionCountry(country, family string) (string, error) {
	b, err := os.ReadFile(e.countryPath(country, family))
	if err != nil {
		return "", err
	}
	name := countrySet(country, family) + "-" + hostfirewall.Hash(string(b))[:10]
	dir := filepath.Join(e.Root, "firewall-admin", "countries", "sets")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name+".zone")
	if _, err = os.Stat(path); os.IsNotExist(err) {
		err = security.WriteAtomic(path, b, 0600)
	}
	return name, err
}
func (e *Engine) firewallFeature(ctx context.Context, action string, p model.Config, s hostfirewall.Status, log func(string)) (model.Config, error) {
	if action == "protection-save" || action == "protection-install" || action == "protection-unban" {
		return e.firewallProtection(ctx, action, p, s, log)
	}
	if action == "region-update" {
		var state firewallExtensionState
		_ = e.readFirewallAdmin("extensions", &state)
		return e.updateCountryRules(ctx, state, s, true, log)
	}

	var state firewallExtensionState
	_ = e.readFirewallAdmin("extensions", &state)
	next := state
	enabled, ok := p["enabled"].(bool)
	if !ok {
		return nil, errors.New("缺少禁 Ping 状态")
	}
	next.Ping = enabled
	if s.Backend == "firewalld" {
		if !enabled {
			next.PingRules = nil
		} else if !state.Ping {
			var err error
			next.PingRules, err = e.prepareFirewallPing(ctx, s)
			if err != nil {
				return nil, err
			}
		}
	}
	return model.Config{}, e.commitFirewallExtensions(ctx, s.Backend, state, next, s.Enabled, log)
}

func (e *Engine) detachFirewallNAT(ctx context.Context, family string) error {
	bin := "iptables"
	if family == "ipv6" {
		bin = "ip6tables"
	}
	for _, hook := range [][2]string{{"PREROUTING", "OPSVIEW-NAT"}, {"POSTROUTING", "OPSVIEW-SNAT"}} {
		text, err := e.hostFirewall().Run(ctx, bin, "-t", "nat", "-S", hook[1])
		if err != nil || !strings.Contains(text, "-N "+hook[1]) {
			continue
		}
		for n := 0; n < 64; n++ {
			if _, err := e.hostFirewall().Run(ctx, bin, "-t", "nat", "-C", hook[0], "-j", hook[1]); err != nil {
				break
			}
			if _, err := e.hostFirewall().Run(ctx, bin, "-t", "nat", "-D", hook[0], "-j", hook[1]); err != nil {
				return err
			}
		}
	}
	return nil
}
func (e *Engine) syncFirewallAux(ctx context.Context, enabled bool, log func(string)) error {
	if !enabled {
		var config firewallProtectionConfig
		if e.readFirewallAdmin("protection", &config) == nil {
			status := e.hostFirewall().Detect(ctx)
			status.Enabled = false
			if _, err := e.firewallProtection(ctx, "protection-save", model.Config{"config": config}, status, log); err != nil {
				return err
			}
		}
		for _, family := range []string{"ipv4", "ipv6"} {
			if err := e.detachFirewallNAT(ctx, family); err != nil {
				return err
			}
		}
		for _, jail := range []string{"opsview-sshd", "opsview-nginx-http-auth"} {
			if _, err := e.hostFirewall().Run(ctx, "fail2ban-client", "status", jail); err == nil {
				if _, err = e.hostFirewall().Run(ctx, "fail2ban-client", "stop", jail); err != nil {
					return err
				}
			}
		}
		return nil
	}
	var state firewallExtensionState
	if e.readFirewallAdmin("extensions", &state) == nil {
		backend := e.hostFirewall().Detect(ctx).Backend
		if err := e.applyFirewallExtensions(ctx, backend, state, state, true, log); err != nil {
			return err
		}
	}
	var config firewallProtectionConfig
	if e.readFirewallAdmin("protection", &config) == nil {
		_, err := e.firewallProtection(ctx, "protection-save", model.Config{"config": config}, e.hostFirewall().Detect(ctx), log)
		if err != nil {
			return err
		}
	}
	return nil
}

func firewallPingRich(family string) string {
	return "rule priority=\"-32760\" family=" + strconv.Quote(family) + " icmp-type name=\"echo-request\" drop"
}
func (e *Engine) prepareFirewallPing(ctx context.Context, s hostfirewall.Status) ([]firewallPingRule, error) {
	c := e.hostFirewall()
	bin := "firewall-cmd"
	if !s.Enabled {
		bin = "firewall-offline-cmd"
	}
	zone, err := c.Run(ctx, bin, "--get-default-zone")
	if err != nil {
		return nil, err
	}
	zones := map[string]bool{strings.TrimSpace(zone): true}
	if s.Enabled {
		text, err := c.Run(ctx, bin, "--get-active-zones")
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(text, "\n") {
			if line != "" && !strings.HasPrefix(line, " ") {
				zones[strings.Fields(line)[0]] = true
			}
		}
	}
	out := []firewallPingRule{}
	for zone := range zones {
		for _, family := range []string{"ipv4", "ipv6"} {
			r := firewallPingRule{Zone: zone, Family: family}
			args := []string{"--zone=" + zone, "--query-rich-rule=" + firewallPingRich(family)}
			_, runtimeErr := c.Run(ctx, bin, args...)
			if s.Enabled {
				_, permanentErr := c.Run(ctx, bin, append([]string{"--permanent"}, args...)...)
				r.Runtime = runtimeErr != nil
				r.Permanent = permanentErr != nil
			} else {
				r.Permanent = runtimeErr != nil
				r.Runtime = r.Permanent
			}
			if r.Runtime || r.Permanent {
				out = append(out, r)
			}
		}
	}
	return out, nil
}
