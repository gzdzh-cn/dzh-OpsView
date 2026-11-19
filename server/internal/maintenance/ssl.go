package maintenance

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"opsview/internal/executor"
	"opsview/internal/model"
	"opsview/internal/runtimeconfig"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (r *Runner) ssl(args []string) error {
	if len(args) == 0 {
		if e := r.execSSLStatus(); e != nil {
			return e
		}
		fmt.Fprintln(r.Out, "用法：ov ssl status|on|off|issue|renew")
		return nil
	}
	op := args[0]
	if op == "status" {
		return r.execSSLStatus()
	}
	if op == "issue" || op == "renew" {
		p := model.Config{}
		if op == "renew" {
			p["renew"] = true
		}
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--host", "--email":
				if i+1 >= len(args) {
					return errors.New("参数缺少值")
				}
				p[strings.TrimPrefix(args[i], "--")] = args[i+1]
				i++
			case "--accept-terms":
				p["acceptTerms"] = true
			default:
				return errors.New("用法：ov ssl issue --host 域名或公网IP --email 邮箱 --accept-terms")
			}
		}
		client := executor.NewClient("/run/opsview/executor.sock")
		data, err := client.Execute(context.Background(), model.Operation{Name: "system.panel-certificate", Params: p}, func(s string) { fmt.Fprintln(r.Out, s) })
		if err == nil {
			fmt.Fprintln(r.Out, "证书已部署，有效期：", data["expires"])
		}
		return err
	}
	if op != "on" && op != "off" {
		return errors.New("用法：ov ssl status|on|off|issue|renew")
	}
	yes := len(args) == 2 && args[1] == "--yes"
	if len(args) > 1 && !yes {
		return errors.New("仅支持 --yes")
	}
	prompt := "启用 SSL 将切换为 HTTPS 并重启面板，确认继续？"
	if op == "off" {
		prompt = "关闭 SSL 后将使用 HTTP 明文连接，证书文件保留，面板将重启，确认继续？"
	}
	if err := r.confirm(prompt, yes); err != nil {
		return err
	}
	if err := CheckOverrides(); err != nil {
		return err
	}
	old, err := runtimeconfig.Load(r.configPath())
	if err != nil {
		return err
	}
	next := old
	next.Protocol = "https"
	if op == "off" {
		next.Protocol = "http"
	}
	if next.Scheme() == old.Scheme() {
		host := old.PublicHost
		if old.AllowedHost != "" {
			host = old.AllowedHost
		}
		if host == "" {
			host = runtimeconfig.LocalHost()
		}
		fmt.Fprintf(r.Out, "面板已使用 %s，无需切换。\n登录地址：%s\n", old.Scheme(), old.URL(host))
		return nil
	}
	return r.applyPanelAccess(next, "")
}
func (r *Runner) execSSLStatus() error {
	cfg, err := runtimeconfig.Load(r.configPath())
	if err == nil {
		fmt.Fprintf(r.Out, "面板协议：%s\n", cfg.Scheme())
		b, readErr := os.ReadFile("/var/lib/opsview/panel/panel.pem")
		if readErr != nil {
			fmt.Fprintln(r.Out, "证书信息：不可读取")
			return nil
		}
		block, _ := pem.Decode(b)
		if block == nil {
			fmt.Fprintln(r.Out, "证书信息：PEM 格式无效")
			return nil
		}
		crt, parseErr := x509.ParseCertificate(block.Bytes)
		if parseErr != nil {
			fmt.Fprintln(r.Out, "证书信息：解析失败")
			return nil
		}
		fmt.Fprintf(r.Out, "证书签发者：%s\n证书到期：%s\n", crt.Issuer.CommonName, crt.NotAfter.Format(time.RFC3339))
		if time.Now().After(crt.NotAfter) {
			fmt.Fprintln(r.Out, "证书状态：已过期")
		}
		if cfg.Scheme() == "http" {
			fmt.Fprintln(r.Out, "自动续期：已随 HTTPS 暂停")
		} else if _, e := os.Stat("/var/lib/opsview/managed/panel-acme/renewal.enc"); e == nil {
			fmt.Fprintln(r.Out, "自动续期：已登记")
		} else {
			fmt.Fprintln(r.Out, "自动续期：未登记")
		}
	}
	return err
}
func (r *Runner) entry(args []string) error {
	if len(args) != 1 || (args[0] != "on" && args[0] != "off" && args[0] != "reset") {
		return errors.New("用法：ov entry on|off|reset")
	}
	unlock, err := runtimeconfig.Lock(r.configPath())
	if err != nil {
		return err
	}
	defer unlock()
	pending := "/var/lib/opsview/managed/panel-pending.json"
	if r.configPath() != runtimeconfig.Path {
		pending = filepath.Join(filepath.Dir(r.configPath()), "panel-pending.json")
	}
	if _, err = os.Stat(pending); !os.IsNotExist(err) {
		return errors.New("访问设置正在等待确认，请先确认或回退，再修改安全入口")
	}
	cfg, err := runtimeconfig.Load(r.configPath())
	if err != nil {
		return err
	}
	cfg.Entry = ""
	if args[0] == "on" {
		cfg.Entry = security.ID()
	}
	if err = runtimeconfig.Save(r.configPath(), cfg); err != nil {
		return err
	}
	// The access middleware reads this file on every request; no service restart is needed.
	if cfg.Entry == "" {
		fmt.Fprintln(r.Out, "安全入口已关闭")
	} else {
		host := cfg.AllowedHost
		if host == "" {
			host = cfg.PublicHost
		}
		if host == "" {
			host = runtimeconfig.LocalHost()
		}
		fmt.Fprintf(r.Out, "安全入口已开启（旧入口已失效）\n登录地址：%s\n", cfg.URL(host))
	}
	return nil
}
