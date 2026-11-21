package logic

import (
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"opsview/internal/model"
	"path"
	"regexp"
	"strconv"
	"strings"
)

type ProxyRoute struct {
	Path           string   `json:"path"`
	Targets        []string `json:"targets"`
	Host           string   `json:"host"`
	Websocket      bool     `json:"websocket"`
	ConnectTimeout int      `json:"connectTimeout"`
	ReadTimeout    int      `json:"readTimeout"`
	SendTimeout    int      `json:"sendTimeout"`
}

func SiteProxyRoutes(c model.Config) ([]ProxyRoute, error) {
	raw, ok := c["proxyRoutes"]
	if !ok || raw == nil {
		return nil, nil
	}
	b, err := json.Marshal(raw)
	if err != nil || len(b) > 32768 {
		return nil, errors.New("代理规则过大")
	}
	var routes []ProxyRoute
	if json.Unmarshal(b, &routes) != nil {
		return nil, errors.New("代理规则格式无效")
	}
	if len(routes) > 16 {
		return nil, errors.New("最多 16 条路径规则")
	}
	seen := map[string]bool{}
	for i := range routes {
		r := &routes[i]
		if !regexp.MustCompile(`^/[A-Za-z0-9/._~-]*$`).MatchString(r.Path) || len(r.Path) > 256 || strings.Contains(r.Path, "..") || seen[r.Path] {
			return nil, errors.New("代理路径须为不重复的 / 开头前缀，不允许正则或目录跳转")
		}
		if c.String("type") != "proxy" && r.Path == "/" {
			return nil, errors.New("本地网站的代理路径不能覆盖 / 根路径")
		}
		redirects, _ := SiteRedirects(c)
		for _, rule := range redirects {
			if rule.Path == r.Path {
				return nil, errors.New("代理和重定向路径重复")
			}
		}
		seen[r.Path] = true
		if len(r.Targets) < 1 || len(r.Targets) > 16 {
			return nil, errors.New("每条规则需要 1–16 个上游")
		}
		scheme := ""
		for _, target := range r.Targets {
			u, err := url.Parse(target)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || strings.ContainsAny(target, ";{}\r\n\t \"'\\$") {
				return nil, errors.New("上游须为不含路径及凭据的 HTTP/HTTPS 地址")
			}
			if net.ParseIP(u.Hostname()) == nil && !Domain.MatchString(u.Hostname()) {
				return nil, errors.New("上游主机名无效")
			}
			if p := u.Port(); p != "" {
				n, err := strconv.Atoi(p)
				if err != nil || n < 1 || n > 65535 {
					return nil, errors.New("上游端口无效")
				}
			}
			if scheme != "" && scheme != u.Scheme {
				return nil, errors.New("同组上游必须使用相同协议")
			}
			scheme = u.Scheme
		}
		if r.Host == "" {
			r.Host = "$host"
		}
		if r.Host != "$host" && r.Host != "$proxy_host" && (net.ParseIP(r.Host) == nil && !Domain.MatchString(r.Host)) {
			return nil, errors.New("Host 须为域名、IP、$host 或 $proxy_host")
		}
		for _, v := range []*int{&r.ConnectTimeout, &r.ReadTimeout, &r.SendTimeout} {
			if *v == 0 {
				*v = 60
			}
			if *v < 1 || *v > 3600 {
				return nil, errors.New("代理超时须为 1–3600 秒")
			}
		}
	}
	if c.String("type") == "proxy" && len(routes) > 0 && !seen["/"] {
		return nil, errors.New("请保留 / 默认代理路径")
	}
	return routes, nil
}

func ValidateSiteOptions(c model.Config) error {
	if err := ValidateSiteSettings(c); err != nil {
		return err
	}
	if err := ValidateSiteCreate(c); err != nil {
		return err
	}
	dir := c.String("runningDirectory")
	if dir != "" && dir != "/" && (!regexp.MustCompile(`^[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)*$`).MatchString(dir) || len(dir) > 256 || path.Clean(dir) != dir || strings.HasPrefix(dir, ".") || strings.Contains(dir, "/.") || strings.Contains(dir, "..")) {
		return errors.New("运行目录须为网站根目录下的相对目录，例如 public，不允许隐藏目录或目录跳转")
	}
	docs := strings.Fields(c.String("defaultDocuments"))
	if len(docs) > 16 || len(c.String("defaultDocuments")) > 512 {
		return errors.New("默认文档最多 16 项")
	}
	for _, doc := range docs {
		if !regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,63}$`).MatchString(doc) || strings.Contains(doc, "..") {
			return errors.New("默认文档只允许文件名，以空格分隔")
		}
	}
	if value := c.String("rewriteTemplate"); value != "" && value != "none" && value != "front-controller" && value != "wordpress" && value != "thinkphp" && value != "laravel" && value != "spa" {
		return errors.New("伪静态模板无效")
	}
	_, err := SiteProxyRoutes(c)
	return err
}
