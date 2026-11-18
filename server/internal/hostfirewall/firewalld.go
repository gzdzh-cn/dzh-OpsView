package hostfirewall

import (
	"context"
	"errors"
	"sort"
	"strings"
)

// New runtime objects are only loaded when runtime and permanent configuration
// agree. This avoids losing runtime-only changes on firewalld versions whose
// public API cannot add a runtime policy or ipset without reload.
func (c *Client) SafeReloadReady(ctx context.Context) error {
	return c.SafeReloadIgnoringSets(ctx, nil)
}
func (c *Client) SafeReloadIgnoringSets(ctx context.Context, owned map[string]bool) error {
	if err := c.CheckForeignTables(ctx); err != nil {
		return err
	}
	for _, args := range [][]string{{"--list-all-zones"}, {"--direct", "--get-all-rules"}, {"--get-policies"}, {"--get-ipsets"}} {
		runtime, err := c.Run(ctx, "firewall-cmd", args...)
		if err != nil {
			return err
		}
		permanent, err := c.Run(ctx, "firewall-cmd", append([]string{"--permanent"}, args...)...)
		if err != nil {
			return err
		}
		if len(args) == 1 && args[0] == "--get-ipsets" {
			filter := func(text string) string {
				names := []string{}
				for _, name := range strings.Fields(text) {
					if !owned[name] {
						names = append(names, name)
					}
				}
				sort.Strings(names)
				return strings.Join(names, " ")
			}
			runtime, permanent = filter(runtime), filter(permanent)
		}
		normalize := func(s string) string {
			lines := strings.Split(strings.TrimSpace(s), "\n")
			for i := range lines {
				lines[i] = strings.ReplaceAll(strings.ReplaceAll(lines[i], " (active)", ""), ", active)", ")")
			}
			sort.Strings(lines)
			return strings.Join(lines, "\n")
		}
		if normalize(runtime) != normalize(permanent) {
			return errors.New("firewalld 运行与永久配置存在差异，初始化扩展需要先在同步预览中处理差异；未执行重载")
		}
	}
	sets, err := c.Run(ctx, "firewall-cmd", "--get-ipsets")
	if err != nil {
		return err
	}
	for _, set := range strings.Fields(sets) {
		if owned[set] {
			continue
		}
		runtime, err := c.Run(ctx, "firewall-cmd", "--info-ipset="+set)
		if err != nil {
			return err
		}
		permanent, err := c.Run(ctx, "firewall-cmd", "--permanent", "--info-ipset="+set)
		if err != nil {
			return err
		}
		if runtime != permanent {
			return errors.New("firewalld 地址集合内容存在未持久化差异，拒绝重载")
		}
	}
	policies, err := c.Run(ctx, "firewall-cmd", "--get-policies")
	if err != nil {
		return err
	}
	for _, policy := range strings.Fields(policies) {
		runtime, err := c.Run(ctx, "firewall-cmd", "--info-policy="+policy)
		if err != nil {
			return err
		}
		permanent, err := c.Run(ctx, "firewall-cmd", "--permanent", "--info-policy="+policy)
		if err != nil {
			return err
		}
		if strings.ReplaceAll(runtime, " (active)", "") != strings.ReplaceAll(permanent, " (active)", "") {
			return errors.New("firewalld policy 存在未持久化差异，拒绝重载")
		}
	}
	return nil
}
func (c *Client) ensureEgress(ctx context.Context) error {
	if _, err := c.Run(ctx, "firewall-cmd", "--info-policy=opsview-egress"); err == nil {
		return nil
	}
	if err := c.SafeReloadReady(ctx); err != nil {
		return err
	}
	for _, args := range [][]string{{"--permanent", "--new-policy=opsview-egress"}, {"--permanent", "--policy=opsview-egress", "--add-ingress-zone=HOST"}, {"--permanent", "--policy=opsview-egress", "--add-egress-zone=ANY"}, {"--permanent", "--policy=opsview-egress", "--set-target=CONTINUE"}} {
		if _, err := c.Run(ctx, "firewall-cmd", args...); err != nil {
			return err
		}
	}
	_, err := c.Run(ctx, "firewall-cmd", "--reload")
	return err
}

// Older firewalld releases flush legacy iptables chains during reload/stop,
// including chains owned by Docker. Never invoke those lifecycle operations
// when their preservation cannot be guaranteed.
func (c *Client) CheckForeignTables(ctx context.Context) error {
	for _, bin := range []string{"iptables-save", "ip6tables-save"} {
		text, err := c.Run(ctx, bin)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(text, "\n") {
			if !strings.HasPrefix(line, ":") {
				continue
			}
			fields := strings.Fields(strings.TrimPrefix(line, ":"))
			if len(fields) == 0 {
				continue
			}
			name := fields[0]
			switch name {
			case "INPUT", "OUTPUT", "FORWARD", "PREROUTING", "POSTROUTING":
				continue
			}

			return errors.New("存在无法确认归属的 iptables 链（" + name + "）；此环境不执行 firewalld 重载或启停，以免清空外部规则。现有规则可继续逐项管理；新增地址集合/出站策略需在本机维护窗口处理（包括旧版 iptables 后端）")
		}
	}
	return nil
}
