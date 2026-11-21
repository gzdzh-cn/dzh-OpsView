package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"opsview/internal/hostfirewall"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type sitePortRule struct {
	Backend string            `json:"backend"`
	Rule    hostfirewall.Rule `json:"rule"`
	Owned   bool              `json:"owned"`
	Sites   map[string]bool   `json:"sites"`
}

func (e *Engine) sitePortRecords() (map[string]*sitePortRule, error) {
	v := map[string]*sitePortRule{}
	b, err := os.ReadFile(filepath.Join(e.Root, "site-users", "ports.json"))
	if os.IsNotExist(err) {
		return v, nil
	}
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(b, &v)
	return v, err
}
func (e *Engine) saveSitePorts(v map[string]*sitePortRule) error {
	if err := os.MkdirAll(filepath.Join(e.Root, "site-users"), 0700); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return security.WriteAtomic(filepath.Join(e.Root, "site-users", "ports.json"), b, 0600)
}

// Serialised with all other security changes. An external rule is only referenced;
// deletion requires both the exact rule revision and the original ownership note.
func (e *Engine) syncSitePort(ctx context.Context, r model.Resource, remove bool) error {
	e.firewallMu.Lock()
	defer e.firewallMu.Unlock()
	if err := e.ensureFirewallIdle(); err != nil {
		return err
	}
	rows, err := e.sitePortRecords()
	if err != nil {
		return err
	}
	want := r.Config.Bool("releasePort") && !remove
	if !want {
		referenced := false
		for _, row := range rows {
			if row.Sites[r.ID] {
				referenced = true
			}
		}
		if !referenced {
			return nil
		}
	}
	s, err := e.hostFirewall().Status(ctx)
	if err != nil {
		return err
	}
	if !s.Installed || !s.Compatible {
		return fmt.Errorf("无法处理项目端口放行：%s", s.Reason)
	}
	desired := map[string]bool{}
	if want {
		families := []string{"ipv4", "ipv6"}
		if s.Backend == "ufw" && !ufwIPv6Enabled(readText(e.hostFirewall().DefaultsPath)) {
			families = []string{"ipv4"}
		}
		for _, family := range families {
			candidate := hostfirewall.Rule{Kind: "port", Protocol: "tcp", Ports: strconv.Itoa(r.Config.Int("port")), Source: "any", Destination: "any", Direction: "in", Action: "allow", Family: family, Note: "opsview-site-" + r.ID, Permanent: true, Runtime: s.Enabled}
			if s.Backend == "firewalld" {
				candidate.Zone = firewallDefaultZone(ctx, e.hostFirewall(), s.Backend)
			}
			key := s.Backend + ":" + hostfirewall.Hash(candidate.Key())
			desired[key] = true
			row := rows[key]
			if row != nil && !row.Owned {
				found := false
				for _, current := range s.Rules {
					if current.Key() == candidate.Key() {
						found = true
					}
				}
				if !found {
					delete(rows, key)
					row = nil
				}
			}
			if row == nil {
				row = &sitePortRule{Backend: s.Backend, Rule: candidate, Sites: map[string]bool{}, Owned: true}
				for _, current := range s.Rules {
					if current.Key() == candidate.Key() {
						row.Rule = current
						row.Owned = false
						break
					}
				}
				rows[key] = row
			}
			if row.Sites == nil {
				row.Sites = map[string]bool{}
			}
			row.Sites[r.ID] = true
			// Persist intent first so an interrupted add can be reconciled by note.
			if err = e.saveSitePorts(rows); err != nil {
				return err
			}
			if row.Owned {
				found := false
				for _, current := range s.Rules {
					if current.Key() == row.Rule.Key() && (!sitePortMatches(s.Backend, current, row.Rule) || (row.Rule.Revision != "" && current.Revision != row.Rule.Revision)) {
						return errors.New("项目端口规则归属或修订已改变，保留外部配置，请先核对")
					}
					if current.Key() == row.Rule.Key() && sitePortMatches(s.Backend, current, row.Rule) {
						row.Rule = current
						found = true
						break
					}
				}
				if !found {
					if err = e.hostFirewall().Change(ctx, s.Backend, row.Rule, false); err != nil {
						return err
					}
					updated, er := e.hostFirewall().Status(ctx)
					if er != nil {
						return er
					}
					s = updated
					for _, current := range s.Rules {
						if current.Key() == row.Rule.Key() && sitePortMatches(s.Backend, current, row.Rule) {
							row.Rule = current
							found = true
							break
						}
					}
					if !found {
						return errors.New("端口放行命令完成，但无法核实持久化规则，请核对系统防火墙")
					}
				}
			}
			if err = e.saveSitePorts(rows); err != nil {
				return err
			}
		}
	}
	for key, row := range rows {
		if !row.Sites[r.ID] || desired[key] {
			continue
		}
		if len(row.Sites) > 1 {
			delete(row.Sites, r.ID)
			if err = e.saveSitePorts(rows); err != nil {
				return err
			}
			continue
		}
		if row.Owned {
			if row.Backend != s.Backend {
				return errors.New("防火墙后端已变化，保留旧规则，请先核对")
			}
			for _, current := range s.Rules {
				if current.Key() != row.Rule.Key() {
					continue
				}
				if !sitePortMatches(s.Backend, current, row.Rule) || current.Revision != row.Rule.Revision {
					return errors.New("项目端口规则被外部修改，未自动删除")
				}
				if err = e.hostFirewall().Change(ctx, s.Backend, current, true); err != nil {
					return err
				}
			}
		}
		delete(rows, key)
		if err = e.saveSitePorts(rows); err != nil {
			return err
		}
	}
	return nil
}
func sitePortMatches(backend string, current, expected hostfirewall.Rule) bool {
	if backend == "ufw" {
		return current.Note == expected.Note
	}
	if expected.Raw != "" {
		return current.Raw == expected.Raw
	}
	return current.Raw == hostfirewall.RichRule(expected)
}

func ufwIPv6Enabled(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		k, v, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(k) == "IPV6" {
			return strings.Trim(strings.TrimSpace(v), "\"' ") == "yes"
		}
	}
	return false
}
