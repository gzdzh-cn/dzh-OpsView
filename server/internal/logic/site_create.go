package logic

import (
	"encoding/json"
	"errors"
	"net/url"
	"opsview/internal/model"
	"path"
	"regexp"
	"strconv"
	"strings"
)

const SiteRoot = "/www/wwwroot"

var siteFolder = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,252}$`)

func DefaultSiteRoot(domains string) string {
	fields := strings.Fields(domains)
	if len(fields) == 0 || !siteFolder.MatchString(fields[0]) {
		return ""
	}
	return SiteRoot + "/" + strings.ToLower(fields[0])
}
func ValidateSiteRoot(root string) error {
	if path.Dir(root) != SiteRoot || !siteFolder.MatchString(path.Base(root)) || path.Clean(root) != root || strings.Contains(path.Base(root), "..") {
		return errors.New("根目录须为 /www/wwwroot 下的独立项目文件夹，只允许字母、数字、点、横线及下划线")
	}
	return nil
}
func ValidateSiteCreate(c model.Config) error {
	if root := c.String("rootDirectory"); root != "" {
		if err := ValidateSiteRoot(root); err != nil {
			return err
		}
	}
	host := c.String("proxyHost")
	if host != "" && host != "$host" && host != "$http_host" && host != "$proxy_host" && !Domain.MatchString(host) {
		return errors.New("发送 Host 须为域名、IP、$host、$http_host 或 $proxy_host")
	}
	if c.String("type") != "managed" {
		return nil
	}
	if c.Int("port") < 1024 || c.Int("port") > 65535 {
		return errors.New("应用端口须为 1024–65535")
	}
	exe := c.String("executable")
	if !strings.HasPrefix(exe, "/") || path.Clean(exe) != exe || strings.ContainsAny(exe, "\x00\r\n\t \"'`$;%\\") {
		return errors.New("执行文件须为不含特殊字符的绝对路径")
	}
	if c.String("user") != "" && c.String("user") != "isolated" && c.String("user") != "www-data" {
		return errors.New("运行用户仅支持独立站点用户或 www-data")
	}
	var args []string
	if raw := c.String("arguments"); raw != "" {
		if len(raw) > 16384 || json.Unmarshal([]byte(raw), &args) != nil || len(args) > 64 {
			return errors.New("启动参数须为最多 64 项的 JSON 字符串数组")
		}
		for _, arg := range args {
			if strings.ContainsAny(arg, "\x00\r\n") {
				return errors.New("启动参数不得包含换行")
			}
		}
	}
	var env map[string]string
	if raw := c.String("environment"); raw != "" {
		if len(raw) > 32768 || json.Unmarshal([]byte(raw), &env) != nil {
			return errors.New("环境变量须为 JSON 对象")
		}
		for k, v := range env {
			if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(k) || strings.ContainsAny(v, "\x00\r\n") {
				return errors.New("环境变量格式无效")
			}
		}
	}
	if raw := c.String("healthURL"); raw != "" {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() != strconv.Itoa(c.Int("port")) || u.User != nil {
			return errors.New("健康检查须为当前应用端口的 http://127.0.0.1 地址")
		}
	}
	return nil
}
