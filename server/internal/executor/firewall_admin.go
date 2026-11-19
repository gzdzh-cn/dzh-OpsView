package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"opsview/internal/hostfirewall"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type firewallChange struct {
	Before  *hostfirewall.Rule `json:"before,omitempty"`
	After   *hostfirewall.Rule `json:"after,omitempty"`
	Started bool               `json:"started"`
}
type firewallTransaction struct {
	Backend       string           `json:"backend"`
	Changes       []firewallChange `json:"changes"`
	Deadline      int64            `json:"deadline"`
	RollbackError string           `json:"rollbackError,omitempty"`
}
type firewallObservation struct {
	Rules     []hostfirewall.Rule `json:"rules"`
	UpdatedAt string              `json:"updatedAt"`
}

func (e *Engine) hostFirewall() *hostfirewall.Client {
	if e.firewallClient != nil {
		return e.firewallClient
	}
	c := hostfirewall.Default()
	var backend model.Config
	if e.readFirewallAdmin("backend", &backend) == nil {
		c.Preferred = backend.String("name")
	}
	return c
}
func (e *Engine) firewallAdminPath(name string) string {
	return filepath.Join(e.Root, "firewall-admin", name+".json")
}
func (e *Engine) writeFirewallAdmin(name string, v any) error {
	p := e.firewallAdminPath(name)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return security.WriteAtomic(p, b, 0600)
}
func (e *Engine) readFirewallAdmin(name string, v any) error {
	b, err := os.ReadFile(e.firewallAdminPath(name))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
func (e *Engine) firewallAdminStatus(ctx context.Context) (model.Config, error) {
	c := e.hostFirewall()
	s, err := e.firewallSnapshot(ctx)
	// Diagnostics remain visible when one firewall command failed.
	if err != nil {
		s.Reason = err.Error()
	}
	if s.Backend != "" {
		_ = e.writeFirewallAdmin("backend", model.Config{"name": s.Backend})
	}
	ports, portErr := e.firewallPorts(ctx)
	portReason := ""
	if portErr != nil {
		portReason = portErr.Error()
	}
	var metadata firewallObservation
	_ = e.readFirewallAdmin("observed", &metadata)
	notes := map[string]hostfirewall.Rule{}
	for _, r := range metadata.Rules {
		notes[r.Key()] = r
	}
	listening, _ := output(ctx, "ss", "-H", "-tuln")
	for i := range s.Rules {
		r := &s.Rules[i]
		if r.Note == "" {
			r.Note = notes[r.Key()].Note
		}
		if r.CreatedAt == "" {
			r.CreatedAt = notes[r.Key()].CreatedAt
		}
		for _, line := range strings.Split(listening, "\n") {
			if r.Protocol == "tcp" && strings.HasPrefix(line, "tcp") || r.Protocol == "udp" && strings.HasPrefix(line, "udp") {
				for _, field := range strings.Fields(line) {
					_, p, e := net.SplitHostPort(field)
					if e == nil {
						n, _ := strconv.Atoi(p)
						if hostfirewall.Contains(r.Ports, n) {
							r.Listening = true
						}
					}
				}
			}
		}
	}

	counts := map[string]int{"port": 0, "ip": 0, "forward": 0, "region": 0}
	for _, r := range s.Rules {
		counts[r.Kind]++
	}
	pending := model.Config{}
	var tx firewallTransaction
	if e.readFirewallAdmin("pending", &tx) == nil {
		pending = model.Config{"deadline": tx.Deadline, "rollbackError": tx.RollbackError}
	}
	if p, er := e.loadFirewallMode(); er == nil {
		pending = model.Config{"deadline": p.Deadline, "rollbackError": p.RollbackError, "mode": true}
	}
	if _, er := os.Stat(e.firewallAdminPath("feature-pending")); er == nil {
		pending = model.Config{"feature": true, "deadline": time.Now().Unix(), "rollbackError": "扩展变更回退待重试"}
	}
	docker, _ := output(ctx, "iptables", "-S", "DOCKER-USER")
	docker6, _ := output(ctx, "ip6tables", "-S", "DOCKER-USER")
	modeReason := ""
	if s.Compatible && s.Backend == "ufw" {
		if err := c.CheckUFWMode(ctx); err != nil {
			modeReason = err.Error()
		}
	}
	if s.Compatible && s.Backend == "firewalld" {
		if err := c.CheckForeignTables(ctx); err != nil {
			modeReason = err.Error()
		}
	}
	ping := false
	var settings firewallExtensionState
	_ = e.readFirewallAdmin("extensions", &settings)
	ping = settings.Ping
	return model.Config{"backend": s.Backend, "installed": s.Installed, "enabled": s.Enabled, "compatible": s.Compatible, "reason": s.Reason, "warning": s.Warning, "modeAvailable": s.Compatible && modeReason == "", "modeReason": modeReason, "rules": s.Rules, "revision": s.Revision, "counts": counts, "zones": s.Zones, "defaultZone": firewallDefaultZone(ctx, c, s.Backend), "raw": s.Raw, "protectedPorts": ports, "portReason": portReason, "pending": pending, "pingBlocked": ping, "dockerRules": docker, "dockerIPv6Rules": docker6, "externalRestrictions": "云安全组需单独配置；监听和放行不代表公网可达"}, nil
}
func decodeFirewallRule(v any) (hostfirewall.Rule, error) {
	var r hostfirewall.Rule
	b, err := json.Marshal(v)
	if err == nil {
		err = json.Unmarshal(b, &r)
	}
	if err != nil {
		return r, err
	}
	r = cleanFirewallInput(r)
	if r.Family == "dual" {
		if r.Source != "any" || r.Destination != "any" {
			return r, errors.New("双栈规则仅适用于所有地址，请分开添加来源限制")
		}
		copy := r
		copy.Family = "ipv4"
		err := copy.Validate()
		r.Ports = copy.Ports
		return r, err
	}
	return r, r.Validate()
}
func (e *Engine) firewallAdmin(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	e.firewallMu.Lock()
	defer e.firewallMu.Unlock()
	action := strings.TrimPrefix(op.Name, "system.firewall-admin-")
	p := op.Params
	if action == "country-status" {
		return e.firewallCountryStatus()
	}
	if action == "status" {
		return e.firewallAdminStatus(ctx)
	}
	if action == "rollback-feature" {
		return model.Config{}, e.recoverFirewallExtension(ctx)
	}
	if action == "rollback" {
		return model.Config{}, e.rollbackFirewallAdmin(ctx)
	}
	if action == "confirm" {
		var tx firewallTransaction
		if err := e.readFirewallAdmin("pending", &tx); err != nil {
			return nil, errors.New("没有待确认的规则修改")
		}
		if time.Now().Unix() >= tx.Deadline || !p.Bool("connected") {
			return nil, errors.New("确认已超时或尚未确认重新连接")
		}
		snapshot, err := e.firewallSnapshot(ctx)
		if err != nil {
			return nil, err
		}
		if err = e.recordFirewallChanges(tx, snapshot); err != nil {
			return nil, err
		}
		return model.Config{"confirmed": true}, os.Remove(e.firewallAdminPath("pending"))
	}
	c := e.hostFirewall()
	s, err := e.firewallSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	if s.Backend != "" {
		_ = e.writeFirewallAdmin("backend", model.Config{"name": s.Backend})
	}
	if action == "export" {
		rules := s.Rules

		selected := map[string]bool{}
		if v, ok := p["ids"].([]any); ok {
			for _, id := range v {
				selected[fmt.Sprint(id)] = true
			}
		}
		out := []hostfirewall.Rule{}
		for _, r := range rules {
			if len(selected) > 0 && !selected[r.ID] || p.String("kind") != "" && r.Kind != p.String("kind") {
				continue
			}
			out = append(out, r)
		}
		return model.Config{"format": "opsview-firewall", "version": 1, "exportedAt": time.Now().UTC().Format(time.RFC3339), "rules": out}, nil
	}
	if action == "import-preview" {
		return e.previewFirewallImport(p, s)
	}
	if action == "sync-preview" {
		return e.previewFirewallSync(s), nil
	}
	if action == "protection-status" {
		return e.firewallProtectionStatus(ctx), nil
	}
	if _, er := os.Stat(e.firewallAdminPath("feature-pending")); !os.IsNotExist(er) {
		return nil, errors.New("防火墙扩展回退尚未完成，请通过 ov firewall rollback 重试")
	}
	if !s.Compatible {
		return nil, errors.New(s.Reason)
	}
	if err := e.ensureFirewallIdle(); err != nil {
		return nil, err
	}

	if action == "sync-observe" {
		if p.String("revision") != s.Revision {
			return nil, &model.RevisionConflict{Revision: s.Revision}
		}
		return model.Config{"synced": true}, e.writeFirewallAdmin("observed", firewallObservation{Rules: e.selectedObservations(p, s), UpdatedAt: time.Now().UTC().Format(time.RFC3339)})
	}
	if action == "dependencies" {
		return e.firewallDependencies(ctx, p, log)
	}
	if action == "ping" || action == "region-update" || action == "protection-save" || action == "protection-install" || action == "protection-unban" {
		return e.firewallFeature(ctx, action, p, s, log)
	}
	if action == "mode" {
		return e.firewallAdminMode(ctx, p, s, log)
	}
	if action != "save" && action != "delete" && action != "import-apply" && action != "sync-restore" {
		return nil, errors.New("不支持的防火墙操作")
	}
	if p.String("revision") != s.Revision {
		return nil, &model.RevisionConflict{Revision: s.Revision}
	}
	tx := firewallTransaction{Backend: s.Backend, Deadline: time.Now().Add(120 * time.Second).Unix()}
	var old *hostfirewall.Rule
	if p.String("id") != "" {
		for _, r := range s.Rules {
			if r.ID == p.String("id") {
				copy := r
				old = &copy
				break
			}
		}
		if old == nil {
			return nil, &model.RevisionConflict{Revision: s.Revision}
		}
		if strings.HasPrefix(old.Note, "opsview-db-") {
			return nil, errors.New("该规则关联数据库授权，请从数据库权限入口修改，避免规则与账号授权不一致")
		}
		if !old.Editable {
			return nil, errors.New(old.Reason)
		}
	}
	if action == "delete" {
		if old == nil {
			return nil, errors.New("未指定规则")
		}
		tx.Changes = append(tx.Changes, firewallChange{Before: old})
	} else {
		rules := []hostfirewall.Rule{}
		if action == "save" {
			r, er := decodeFirewallRule(p["rule"])
			if er != nil {
				return nil, er
			}
			rules = append(rules, expandFirewallRules(r, s.Backend)...)
		} else {
			var er error
			rules, er = e.selectedFirewallRules(p, s, action)
			if er != nil {
				return nil, er
			}
		}
		for i, r := range rules {
			if s.Backend == "ufw" {
				r.Zone = ""
			}
			if s.Backend == "firewalld" && r.Direction == "out" {
				r.Zone = "opsview-egress"
			}
			for _, existing := range s.Rules {
				if existing.Key() == r.Key() && (old == nil || existing.ID != old.ID) {
					return nil, errors.New("已存在相同规则，请同步后管理已有规则")
				}
			}
			if r.Kind == "region" {
				if _, er := c.Run(ctx, "ipset", "--version"); er != nil && s.Backend == "ufw" {
					return nil, errors.New("地区规则需要 ipset，请先通过地区数据安装依赖入口安装")
				}
			}
			if r.Zone == "" && s.Backend == "firewalld" {
				zone, er := c.Run(ctx, "firewall-cmd", "--get-default-zone")
				if er != nil {
					return nil, er
				}
				r.Zone = strings.TrimSpace(zone)
			}

			r.ID = "ov-" + security.ID()
			r.CreatedAt = time.Now().UTC().Format(time.RFC3339)
			if old != nil && i == 0 {
				r.CreatedAt = old.CreatedAt
			}
			r.Editable = true
			r.Runtime = s.Enabled
			r.Permanent = true
			if old != nil && i == 0 {
				r.Position = old.Position
			}
			change := firewallChange{After: &r}
			if i == 0 {
				change.Before = old
			}
			tx.Changes = append(tx.Changes, change)
		}
	}
	if len(tx.Changes) == 0 {
		return nil, errors.New("没有选择可应用的规则")
	}
	// Protect the real management listeners. A broad country/IP deny is unsafe without
	// a source-specific exception; never silently turn that exception into a world allow.
	ports, er := e.firewallPorts(ctx)
	if er != nil {
		return nil, er
	}
	danger := false
	for _, ch := range tx.Changes {
		serviceAttachment := ch.Before != nil && ch.Before.Protocol == "service" && ch.Before.Service != ""
		if ch.Before != nil && (ch.Before.Action == "allow" || serviceAttachment) && (ch.After == nil || ch.Before.Key() != ch.After.Key()) {
			for _, port := range ports {
				if ch.Before.Direction == "in" && (hostfirewall.Contains(ch.Before.Ports, port) || serviceAttachment) {
					danger = true
				}
			}
		}
		if r := ch.After; r != nil && r.Direction == "in" && (r.Action != "allow" || r.Kind == "forward") && (r.Protocol != "udp") {
			for _, port := range ports {
				if hostfirewall.Contains(r.Ports, port) {
					danger = true
				}
			}
		}
	}

	if danger {
		if !p.Bool("confirmedImpact") || !validSource(p.String("source"), false) {
			return nil, errors.New("涉及管理端口，请确认影响并填写实际客户端 IP/CIDR")
		}
		for _, port := range ports {
			family := "ipv4"
			if strings.Contains(p.String("source"), ":") {
				family = "ipv6"
			}
			guard := hostfirewall.Rule{Kind: "port", Protocol: "tcp", Ports: strconv.Itoa(port), Source: p.String("source"), Destination: "any", Action: "allow", Direction: "in", Family: family, Zone: "", Note: "opsview-access", Position: 1, Editable: true, Permanent: true, Runtime: s.Enabled}
			if s.Backend == "firewalld" {
				z, ze := c.Run(ctx, "firewall-cmd", "--get-default-zone")
				if ze != nil {
					return nil, ze
				}
				guard.Zone = strings.TrimSpace(z)
			}
			found := false
			for _, r := range s.Rules {
				if r.Key() == guard.Key() {
					found = true
				}
			}
			if !found {
				copy := guard
				tx.Changes = append([]firewallChange{{After: &copy}}, tx.Changes...)
			}
		}
	}
	for _, ch := range tx.Changes {
		if ch.After != nil {
			if err = c.ValidateCandidate(ctx, s.Backend, *ch.After); err != nil {
				return nil, err
			}
		}
	}
	if err = e.writeFirewallAdmin("pending", tx); err != nil {
		return nil, err
	}
	expected := s.Revision
	for i := range tx.Changes {
		current, er := e.firewallSnapshot(ctx)
		if er != nil {
			err = er
			break
		}
		if current.Revision != expected {
			err = &model.RevisionConflict{Revision: current.Revision}
			break
		}
		if tx.Changes[i].Before != nil && s.Backend == "ufw" {
			current, er := c.Status(ctx)
			if er != nil {
				err = er
				break
			}
			for _, r := range current.Rules {
				if r.ID == tx.Changes[i].Before.ID {
					tx.Changes[i].Before.Position = r.Position
					if tx.Changes[i].After != nil && tx.Changes[i].After.Family == r.Family {
						tx.Changes[i].After.Position = r.Position
					}
				}
			}
		}
		tx.Changes[i].Started = true
		if err = e.writeFirewallAdmin("pending", tx); err != nil {
			break
		}
		ch := tx.Changes[i]
		if ch.Before != nil {
			err = e.changeFirewallRule(ctx, s.Backend, *ch.Before, true, log)
			if err != nil {
				break
			}
		}
		if ch.After != nil {
			err = e.changeFirewallRule(ctx, s.Backend, *ch.After, false, log)
			if err != nil {
				break
			}
		}
		snapshot, er := e.firewallSnapshot(ctx)
		if er != nil {
			err = er
			break
		}
		if !firewallDeltaMatches(current, snapshot, ch) {
			err = errors.New("执行期间发现外部规则变化，停止后续修改并回退本次变更")
			break
		}
		if tx.Changes[i].After != nil {
			for _, actual := range snapshot.Rules {
				if actual.Key() == tx.Changes[i].After.Key() {
					actual.Note = tx.Changes[i].After.Note
					actual.CreatedAt = tx.Changes[i].After.CreatedAt
					tx.Changes[i].After = &actual
					break
				}
			}
			if err = e.writeFirewallAdmin("pending", tx); err != nil {
				break
			}
		}
		expected = snapshot.Revision
	}

	if err != nil {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if rb := e.rollbackFirewallAdmin(rollbackCtx); rb != nil {
			return nil, fmt.Errorf("%v；回退需重试：%w", err, rb)
		}
		return nil, err
	}
	updated, er := e.firewallSnapshot(ctx)
	if er != nil {
		return nil, er
	}
	if !danger {
		if er = e.recordFirewallChanges(tx, updated); er != nil {
			return nil, er
		}
	}

	if !danger {
		if err = os.Remove(e.firewallAdminPath("pending")); err != nil {
			return nil, err
		}
	} else {
		tx.Deadline = time.Now().Add(120 * time.Second).Unix()
		if err = e.writeFirewallAdmin("pending", tx); err != nil {
			return nil, err
		}
		log("规则已修改，请在 120 秒内重新连接并确认，否则自动回退")
	}
	log("系统防火墙规则已保存")
	return model.Config{"deadline": tx.Deadline, "requiresConfirmation": danger}, nil
}
func (e *Engine) changeFirewallRule(ctx context.Context, backend string, r hostfirewall.Rule, remove bool, log func(string)) error {
	if r.Kind == "forward" && r.Raw != "" {
		return e.hostFirewall().Change(ctx, backend, r, remove)
	}
	if r.Kind == "forward" || r.Kind == "region" {
		return e.changeFirewallExtension(ctx, backend, r, remove, log)
	}
	if err := e.hostFirewall().Change(ctx, backend, r, remove); err != nil {
		return err
	}
	if remove && strings.HasPrefix(r.Note, "opsview-db-") {
		id := strings.TrimPrefix(r.Note, "opsview-")
		if stored, err := e.load(id); err == nil {
			if err = e.restrictDatabaseAfterRule(ctx, stored); err != nil {
				return err
			}
			_ = os.Remove(filepath.Join(e.Root, "firewall", id+".json"))
			_ = e.forget(id)
		}
	}
	return nil
}
func (e *Engine) rollbackFirewallAdmin(ctx context.Context) error {
	var tx firewallTransaction
	if err := e.readFirewallAdmin("pending", &tx); err != nil {
		return err
	}
	for i := len(tx.Changes) - 1; i >= 0; i-- {
		ch := tx.Changes[i]
		if !ch.Started {
			continue
		}
		s, err := e.firewallSnapshot(ctx)
		if err != nil {
			return err
		}

		for _, actual := range s.Rules {
			var target *hostfirewall.Rule
			if ch.After != nil && actual.Key() == ch.After.Key() {
				target = ch.After
			} else if ch.Before != nil && actual.Key() == ch.Before.Key() {
				target = ch.Before
			}
			if target != nil && ((target.Revision != "" && actual.Revision != target.Revision) || (tx.Backend == "ufw" && actual.Note != target.Note)) {
				tx.RollbackError = "规则被外部修改，保留当前规则；请核对差异后重试回退"
				_ = e.writeFirewallAdmin("pending", tx)
				return errors.New(tx.RollbackError)
			}
		}

		present := func(target hostfirewall.Rule) bool {
			for _, r := range s.Rules {
				if r.Key() == target.Key() && (r.Note == target.Note || tx.Backend == "firewalld") {
					return true
				}
			}
			return false
		}
		if ch.After != nil && present(*ch.After) {
			if err = e.changeFirewallRule(ctx, tx.Backend, *ch.After, true, func(string) {}); err != nil {
				tx.RollbackError = err.Error()
				_ = e.writeFirewallAdmin("pending", tx)
				return err
			}
		}
		if ch.Before != nil {
			s, err = e.firewallSnapshot(ctx)
			if err != nil {
				return err
			}
		}
		if ch.Before != nil && !present(*ch.Before) {
			if err = e.changeFirewallRule(ctx, tx.Backend, *ch.Before, false, func(string) {}); err != nil {
				tx.RollbackError = err.Error()
				_ = e.writeFirewallAdmin("pending", tx)
				return err
			}
		}
		tx.Changes[i].Started = false
		if err = e.writeFirewallAdmin("pending", tx); err != nil {
			return err
		}
	}
	return os.Remove(e.firewallAdminPath("pending"))
}
func (e *Engine) firewallAdminMode(ctx context.Context, p model.Config, s hostfirewall.Status, log func(string)) (model.Config, error) {
	if !p.Bool("confirmedImpact") {
		return nil, errors.New("请确认防火墙启停影响")
	}
	enabled, ok := p["enabled"].(bool)
	if !ok {
		return nil, errors.New("缺少目标状态")
	}
	if s.Backend == "ufw" {
		return nil, errors.New("请使用防火墙状态事务入口")
	}
	if enabled == s.Enabled {
		return nil, errors.New("状态没有变化")
	}
	// firewalld mode changes use the same existing durable mode journal.
	var undo []hostfirewall.Rule
	if enabled {
		if !validSource(p.String("source"), false) {
			return nil, errors.New("请填写客户端 IP/CIDR")
		}
		ports, err := e.firewallPorts(ctx)
		if err != nil {
			return nil, err
		}
		zone, err := e.hostFirewall().Run(ctx, "firewall-offline-cmd", "--get-default-zone")
		if err != nil {
			return nil, err
		}
		for _, port := range ports {
			family := "ipv4"
			if strings.Contains(p.String("source"), ":") {
				family = "ipv6"
			}
			r := hostfirewall.Rule{Kind: "port", Protocol: "tcp", Ports: strconv.Itoa(port), Family: family, Source: p.String("source"), Destination: "any", Direction: "in", Action: "allow", Zone: strings.TrimSpace(zone), Note: "opsview-access", Permanent: true}
			found := false
			for _, existing := range s.Rules {
				if existing.Key() == r.Key() {
					found = true
				}
			}
			if !found {
				undo = append(undo, r)
			}
		}
	}
	journal := pendingFirewallMode{OldActive: s.Enabled, NextActive: enabled, Deadline: time.Now().Add(120 * time.Second).Unix(), Backend: "firewalld", UndoHost: undo}
	if err := e.saveFirewallMode(journal); err != nil {
		return nil, err
	}
	for _, r := range undo {
		if err := e.hostFirewall().Change(ctx, "firewalld", r, false); err != nil {
			return nil, err
		}
	}
	if err := e.hostFirewall().SetEnabled(ctx, s.Backend, enabled); err != nil {
		_ = e.rollbackFirewallMode(ctx, journal)
		return nil, err
	}
	if err := e.syncFirewallAux(ctx, enabled, log); err != nil {
		_ = e.rollbackFirewallMode(ctx, journal)
		return nil, err
	}
	log("防火墙状态待确认，120 秒未确认自动回退")
	return model.Config{"deadline": journal.Deadline}, nil
}

func (e *Engine) firewallSnapshot(ctx context.Context) (hostfirewall.Status, error) {
	s, err := e.hostFirewall().Status(ctx)
	if err != nil {
		return s, err
	}
	extras := e.firewallExtensionsStatus(ctx, s)
	rows := []hostfirewall.Rule{}
	var extensionState firewallExtensionState
	_ = e.readFirewallAdmin("extensions", &extensionState)
	for _, r := range s.Rules {
		owned := false
		for _, ping := range extensionState.PingRules {
			if r.Zone == ping.Zone && r.Raw == firewallPingRich(ping.Family) {
				owned = true
			}
		}
		for _, ex := range extras {
			for _, raw := range extensionRichRules(ex) {
				if r.Zone == ex.Zone && r.Raw == raw {
					owned = true
					break
				}
			}
			if owned {
				break
			}
		}
		if !owned {
			rows = append(rows, r)
		}
	}
	s.Rules = append(rows, extras...)
	var observed firewallObservation
	_ = e.readFirewallAdmin("observed", &observed)
	notes := map[string]hostfirewall.Rule{}
	for _, r := range observed.Rules {
		notes[r.Key()] = r
	}
	for i := range s.Rules {
		r := &s.Rules[i]
		if r.Note == "" {
			r.Note = notes[r.Key()].Note
		}
		if r.CreatedAt == "" {
			r.CreatedAt = notes[r.Key()].CreatedAt
		}
	}

	resources := e.states()
	for i := range s.Rules {
		r := &s.Rules[i]
		for _, resource := range resources {
			if resource.Kind != "firewall" || resource.Config.String("scope") != "host" {
				continue
			}
			cfg := resource.Config
			ports, _ := hostfirewall.PortSpec(cfg.String("ports"), false)
			source, _ := hostfirewall.Address(cfg.String("source"), r.Family)
			if r.Kind == "port" && r.Protocol == cfg.String("protocol") && r.Ports == ports && r.Source == source && r.Destination == "any" && r.Direction == "in" && r.Action == cfg.String("action") {
				r.Note = "opsview-" + resource.ID
			}
		}
	}

	revision := s.Revision
	for _, r := range s.Rules {
		revision += "\x00" + r.ID + r.Note + r.CreatedAt
	}
	for _, r := range extras {
		revision += "\x00" + r.ID + ":" + r.Key() + ":" + r.Revision
	}
	var state firewallExtensionState
	_ = e.readFirewallAdmin("extensions", &state)
	revision += strconv.FormatBool(state.Ping)
	s.Revision = hostfirewall.Hash(revision)
	return s, nil
}
func firewallDefaultZone(ctx context.Context, c *hostfirewall.Client, backend string) string {
	if backend != "firewalld" {
		return ""
	}
	bin := "firewall-cmd"
	if !c.Detect(ctx).Enabled {
		bin = "firewall-offline-cmd"
	}
	zone, err := c.Run(ctx, bin, "--get-default-zone")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(zone)
}
func expandFirewallRules(r hostfirewall.Rule, backend string) []hostfirewall.Rule {
	rows := []hostfirewall.Rule{}
	families := []hostfirewall.Rule{r}
	if r.Family == "dual" {
		a, b := r, r
		a.Family = "ipv4"
		b.Family = "ipv6"
		families = []hostfirewall.Rule{a, b}
	}
	for _, family := range families {
		for _, v := range hostfirewall.Expand(family) {
			if backend == "firewalld" && v.Kind == "port" {
				for _, port := range strings.Split(v.Ports, ",") {
					copy := v
					copy.Ports = port
					rows = append(rows, copy)
				}
			} else {
				rows = append(rows, v)
			}
		}
	}
	return rows
}

func (e *Engine) recordFirewallChanges(tx firewallTransaction, s hostfirewall.Status) error {
	var old firewallObservation
	_ = e.readFirewallAdmin("observed", &old)
	byID := map[string]hostfirewall.Rule{}
	for _, r := range old.Rules {
		byID[r.ID] = r
	}
	for _, ch := range tx.Changes {
		if ch.Before != nil {
			delete(byID, ch.Before.ID)
		}
		if ch.After != nil {
			for _, actual := range s.Rules {
				if actual.Key() == ch.After.Key() {
					actual.Note = ch.After.Note
					actual.CreatedAt = ch.After.CreatedAt
					byID[actual.ID] = actual
				}
			}
		}
	}
	out := firewallObservation{Rules: []hostfirewall.Rule{}, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	for _, r := range byID {
		out.Rules = append(out.Rules, r)
	}
	return e.writeFirewallAdmin("observed", out)
}

// Position shifts caused by inserting an UFW rule are expected, but any other
// rule identity/semantics change stops the remaining multi-step operation.
func firewallDeltaMatches(before, after hostfirewall.Status, ch firewallChange) bool {
	signature := func(s hostfirewall.Status) map[string]string {
		out := map[string]string{}
		for _, r := range s.Rules {
			if ch.Before != nil && r.Key() == ch.Before.Key() {
				continue
			}
			if ch.After != nil && r.Key() == ch.After.Key() {
				continue
			}
			out[r.ID] = r.Revision + r.Key() + strconv.FormatBool(r.Runtime) + strconv.FormatBool(r.Permanent)
		}
		return out
	}
	a, b := signature(before), signature(after)
	if len(a) != len(b) {
		return false
	}
	for id, v := range a {
		if b[id] != v {
			return false
		}
	}
	return before.Enabled == after.Enabled && before.Backend == after.Backend
}
