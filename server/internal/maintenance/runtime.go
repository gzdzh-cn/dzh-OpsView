package maintenance

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"opsview/internal/runtimeconfig"
	"os"
)

// Runtime is the small deployment-script interface. Credentials enter only via stdin.
func Runtime(args []string) error {
	if len(args) == 0 {
		return errors.New("runtime-config: init | port | health-url | summary | check-overrides")
	}
	c, e := runtimeconfig.Load(runtimeconfig.Path)
	if e != nil {
		return e
	}
	switch args[0] {
	case "init":
		if os.Geteuid() != 0 {
			return errors.New("需要 root")
		}
		if len(args) > 1 {
			if len(args) != 3 || args[1] != "--public-host" || !runtimeconfig.ValidHost(args[2]) {
				return errors.New("--public-host 须为域名或 IPv4")
			}
			c.PublicHost = args[2]
		} else if c.PublicHost == "" {
			host, err := runtimeconfig.DetectPublicHost(nil)
			if err != nil {
				fmt.Fprintln(os.Stderr, "公网 IP 获取失败，将使用本机地址；可用 ov info --public-host 修正。")
			} else {
				c.PublicHost = host
			}
		}
		return runtimeconfig.Save(runtimeconfig.Path, c)
	case "port":
		fmt.Println(c.Port)
	case "health-url":
		fmt.Println(c.LocalHealthURL())
	case "check-overrides":
		return CheckOverrides()
	case "summary":
		var creds credentials
		if e = json.NewDecoder(io.LimitReader(os.Stdin, 4096)).Decode(&creds); e != nil {
			return e
		}
		host := c.PublicHost
		if host == "" {
			host = runtimeconfig.LocalHost()
			fmt.Println("公网 IP 获取失败，登录地址使用本机地址。")
		}
		fmt.Printf("\nOpsView 安装完成\n\n登录地址：%s\n本机地址：%s\n用户名：%s\n密码：%s\n\n密码仅显示一次，请立即保存。\n终端管理命令：ov\n请在防火墙和云安全组放行 TCP %d。\n首次使用自签名 HTTPS 证书，请核对服务器地址。\n出口 IP 检测不代表公网入站已经验证。\n", c.URL(host), c.URL(runtimeconfig.LocalHost()), creds.Username, creds.Password, c.Port)
	default:
		return errors.New("未知 runtime-config 操作")
	}
	return nil
}
