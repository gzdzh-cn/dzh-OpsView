package hostfirewall

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
)

// Independent nftables base chains still filter traffic after UFW stops. Keep
// their diagnostic separate from ordinary UFW edits; never replace their policy.
func (c *Client) CheckUFWMode(ctx context.Context) error {
	if err := c.CheckUFWLifecycle(); err != nil {
		return err
	}
	text, err := c.Run(ctx, "nft", "list", "ruleset")
	if err != nil {
		return nil // Legacy iptables hosts need not have nft installed.
	}
	hooks := regexp.MustCompile(`(?s)chain\s+([^\s{]+)\s*\{[^{}]*?\bhook\s+input\b`)
	for _, hook := range hooks.FindAllStringSubmatch(text, -1) {
		if hook[1] != "INPUT" && !strings.HasPrefix(strings.ToLower(hook[1]), "ufw") {
			return errors.New("另有独立 nftables 入站链（" + hook[1] + "），不能用 UFW 开关代表全部防火墙状态；普通 UFW 规则仍可修改，请在主机核对独立过滤策略")
		}
	}
	tables := regexp.MustCompile(`(?s)table\s+(?:inet|ip|ip6)\s+([^\s{]+)\s*\{(.*?)\n\}`)
	for _, table := range tables.FindAllStringSubmatch(text, -1) {
		if strings.Contains(table[2], "hook input") && table[1] != "filter" && !strings.Contains(strings.ToLower(table[1]), "ufw") {
			return errors.New("另有独立 nftables 入站表（" + table[1] + "），请先在主机核对独立过滤策略；普通 UFW 规则仍可修改")
		}
	}
	return nil
}

// UFW's MANAGE_BUILTINS=yes flushes chains owned by other software on start,
// stop and reload. The normal no setting only changes UFW's own chains. Do not
// treat independent INPUT chains (e.g. IN_BT or Fail2ban) as a conflicting
// firewall backend, and never silently change this host setting.
func (c *Client) CheckUFWLifecycle() error {
	if c.DefaultsPath == "" {
		return nil // Structured test clients supply their own command implementation.
	}
	b, err := os.ReadFile(c.DefaultsPath)
	if err != nil {
		return errors.New("无法读取 UFW 默认配置，不能确认外部规则会被保留：" + err.Error())
	}
	value := ""
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		key, v, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(key) == "MANAGE_BUILTINS" {
			v, _, _ = strings.Cut(v, "#")
			value = strings.Trim(strings.TrimSpace(v), "\"'")
		}
	}
	if value != "no" {
		return errors.New("UFW 的 MANAGE_BUILTINS 必须为 no，当前配置可能清空 Docker 或其他软件的规则；请先在主机核对 /etc/default/ufw")
	}
	return nil
}
