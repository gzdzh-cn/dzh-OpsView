package logic

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"opsview/internal/model"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

type SiteBinding struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Directory string `json:"directory,omitempty"`
}
type SiteRedirect struct {
	Path   string `json:"path"`
	Target string `json:"target"`
	Code   int    `json:"code"`
	Exact  bool   `json:"exact"`
}
type SiteAccess struct {
	Path     string `json:"path"`
	Username string `json:"username"`
	Password string `json:"password"`
}

var sitePath = regexp.MustCompile(`^/[A-Za-z0-9/._~-]*$`)
var siteExtension = regexp.MustCompile(`^[a-zA-Z0-9]{1,16}$`)

// SiteBindingValue does not change the general domain validator used by other resources.
func SiteBindingValue(value string, defaultPort int) (SiteBinding, error) {
	b := SiteBinding{Port: defaultPort}
	value = strings.ToLower(strings.TrimSpace(value))
	if strings.HasPrefix(value, "[") || strings.Count(value, ":") == 1 {
		host, port, err := net.SplitHostPort(value)
		if err != nil {
			return b, errors.New("域名端口格式无效，IPv6 请使用 [地址]:端口")
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || n == 443 {
			return b, errors.New("端口须为 1–65535；443 请通过 SSL 证书配置")
		}
		b.Host, b.Port = host, n
	} else {
		b.Host = value
	}
	host := strings.TrimPrefix(b.Host, "*.")
	if len(host) > 253 || (net.ParseIP(host) == nil && !Domain.MatchString(host)) || (strings.HasPrefix(b.Host, "*.") && (net.ParseIP(host) != nil || !strings.Contains(host, "."))) {
		return b, errors.New("域名无效：" + value)
	}
	return b, nil
}
func SiteBindings(c model.Config, port int) ([]SiteBinding, error) {
	values := strings.Fields(c.String("domains"))
	if len(values) == 0 || len(values) > 100 {
		return nil, errors.New("请填写 1–100 个域名")
	}
	out := []SiteBinding{}
	seen := map[string]bool{}
	for _, v := range values {
		b, e := SiteBindingValue(v, port)
		if e != nil {
			return nil, e
		}
		key := fmt.Sprintf("%s:%d", b.Host, b.Port)
		if seen[key] {
			return nil, errors.New("域名和端口重复")
		}
		seen[key] = true
		out = append(out, b)
	}
	var children []SiteBinding
	if err := decodeSiteOption(c, "subdirectoryBindings", &children, 32768); err != nil {
		return nil, err
	}
	if len(children) > 32 {
		return nil, errors.New("子目录绑定最多 32 项")
	}
	for _, b := range children {
		value := b.Host
		if b.Port != 0 {
			value = net.JoinHostPort(b.Host, strconv.Itoa(b.Port))
		}
		parsed, err := SiteBindingValue(value, port)
		if err != nil {
			return nil, err
		}
		if b.Directory == "" || validateSiteRelative(b.Directory) != nil {
			return nil, errors.New("请选择根目录下的非隐藏子目录")
		}
		parsed.Directory = b.Directory
		key := fmt.Sprintf("%s:%d", parsed.Host, parsed.Port)
		if seen[key] {
			return nil, errors.New("子目录域名和端口重复")
		}
		seen[key] = true
		out = append(out, parsed)
	}
	directoryByHost := map[string]string{}
	for _, binding := range out {
		if prior, ok := directoryByHost[binding.Host]; ok && prior != binding.Directory {
			return nil, errors.New("同一域名不能指向不同子目录，请使用独立子域名")
		}
		directoryByHost[binding.Host] = binding.Directory
	}

	return out, nil
}
func SiteDomainNames(c model.Config) []string {
	bindings, _ := SiteBindings(c, 80)
	out := []string{}
	seen := map[string]bool{}
	for _, b := range bindings {
		if !seen[b.Host] {
			out = append(out, b.Host)
			seen[b.Host] = true
		}
	}
	return out
}
func validateSiteRelative(s string) error {
	if s == "" || strings.HasPrefix(s, "/") || strings.HasPrefix(s, ".") || strings.Contains(s, "/.") || strings.Contains(s, "..") || len(s) > 256 || !regexp.MustCompile(`^[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)*$`).MatchString(s) {
		return errors.New("目录格式无效")
	}
	return nil
}
func decodeSiteOption(c model.Config, key string, v any, max int) error {
	if c[key] == nil {
		return nil
	}
	b, e := json.Marshal(c[key])
	if e != nil || len(b) > max || json.Unmarshal(b, v) != nil {
		return errors.New(key + " 配置格式或长度无效")
	}
	return nil
}
func SiteRedirects(c model.Config) ([]SiteRedirect, error) {
	var rules []SiteRedirect
	if err := decodeSiteOption(c, "redirects", &rules, 32768); err != nil {
		return nil, err
	}
	if len(rules) > 32 {
		return nil, errors.New("重定向最多 32 项")
	}
	seen := map[string]bool{}
	for _, r := range rules {
		if !sitePath.MatchString(r.Path) || strings.Contains(r.Path, "..") || len(r.Path) > 256 || strings.HasPrefix(r.Path, "/.well-known/") || seen[r.Path] {
			return nil, errors.New("重定向路径无效或重复")
		}
		seen[r.Path] = true
		if r.Code != 301 && r.Code != 302 && r.Code != 307 && r.Code != 308 {
			return nil, errors.New("重定向状态码无效")
		}
		if err := ValidateSiteRedirectTarget(r.Target); err != nil {
			return nil, err
		}
	}
	return rules, nil
}
func ValidateSiteRedirectTarget(target string) error {
	u, e := url.Parse(target)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || len(target) > 2048 || strings.ContainsAny(target, "\x00\r\n\t ;{}\"'\\$") {
		return errors.New("重定向目标须为不含凭据的 HTTP/HTTPS 地址")
	}
	return nil
}
func SiteAccessRules(c model.Config) (map[string]SiteAccess, error) {
	rules := map[string]SiteAccess{}
	if err := decodeSiteOption(c, "accessRules", &rules, 32768); err != nil {
		return nil, err
	}
	if len(rules) > 16 {
		return nil, errors.New("加密访问最多 16 项")
	}
	paths := map[string]bool{}
	for id, r := range rules {
		if !Name.MatchString(id) || !sitePath.MatchString(r.Path) || strings.Contains(r.Path, "..") || len(r.Path) > 256 || paths[r.Path] || !regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`).MatchString(r.Username) {
			return nil, errors.New("访问规则标识、路径或用户名无效")
		}
		paths[r.Path] = true
		if cost, err := bcrypt.Cost([]byte(r.Password)); err != nil || cost < 10 || cost > 14 {
			return nil, errors.New("访问密码未正确保存")
		}
	}
	return rules, nil
}

// Called before enqueueing, so task payloads and general metadata never contain plaintext.
func HashSiteAccessPasswords(c model.Config) error {
	if c["accessRules"] == nil {
		return nil
	}
	var rules map[string]SiteAccess
	if err := decodeSiteOption(c, "accessRules", &rules, 32768); err != nil {
		return err
	}
	for id, r := range rules {
		if strings.HasPrefix(r.Password, "$2") {
			if cost, err := bcrypt.Cost([]byte(r.Password)); err != nil || cost < 10 || cost > 14 {
				return errors.New("访问密码格式无效")
			}
			continue
		}
		if len(r.Password) < 12 || len(r.Password) > 72 || strings.ContainsAny(r.Password, "\x00\r\n") {
			return errors.New("访问密码须为 12–72 字节")
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(r.Password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		r.Password = string(hash)
		rules[id] = r
	}
	// PreserveMasked expects ordinary nested maps, including when called by older clients.
	b, _ := json.Marshal(rules)
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	c["accessRules"] = raw
	return nil
}
func ValidateSiteSettings(c model.Config) error {
	if c.String("redirect404Target") != "" {
		if err := ValidateSiteRedirectTarget(c.String("redirect404Target")); err != nil {
			return err
		}
	}
	if n := c.Int("hotlinkStatus"); n != 0 && n != 403 && n != 404 {
		return errors.New("防盗链拒绝状态只允许 403 或 404")
	}

	if value, ok := c["gzipEnabled"]; ok {
		if _, ok := value.(bool); !ok {
			return errors.New("Gzip 开关格式无效")
		}
		lines := []string{}
		for _, line := range strings.Split(c.String("advanced"), "\n") {
			fields := strings.Fields(strings.TrimSuffix(strings.TrimSpace(line), ";"))
			if len(fields) > 0 && fields[0] == "gzip" {
				continue
			}
			lines = append(lines, line)
		}
		if c.String("advanced") != "" {
			c["advanced"] = strings.Join(lines, "\n")
		}
	}

	if err := ValidateSiteClientAuth(c); err != nil {
		return err
	}
	if _, err := SiteRedirects(c); err != nil {
		return err
	}
	if _, err := SiteAccessRules(c); err != nil {
		return err
	}
	for _, key := range []string{"denyPaths", "allowAddresses", "denyAddresses", "hotlinkDomains"} {
		if len(c.String(key)) > 8192 {
			return errors.New(key + " 过长")
		}
	}
	for _, p := range strings.Fields(c.String("denyPaths")) {
		if !sitePath.MatchString(p) || strings.Contains(p, "..") || p == "/" || strings.HasPrefix(p, "/.well-known") || len(p) > 256 {
			return errors.New("禁止访问路径须为非根路径")
		}
	}
	for _, key := range []string{"allowAddresses", "denyAddresses"} {
		values := strings.Fields(c.String(key))
		if len(values) > 100 {
			return errors.New("IP 规则最多 100 项")
		}
		for _, v := range values {
			if net.ParseIP(v) == nil {
				if _, _, err := net.ParseCIDR(v); err != nil {
					return errors.New("访问来源须为 IP 或 CIDR")
				}
			}
		}
	}
	for _, key := range []string{"denyExtensions", "hotlinkExtensions", "cacheExtensions"} {
		values := strings.Fields(strings.ReplaceAll(c.String(key), ",", " "))
		if len(values) > 32 {
			return errors.New("扩展名最多 32 项")
		}
		for _, v := range values {
			if !siteExtension.MatchString(v) || (key != "denyExtensions" && (strings.HasPrefix(strings.ToLower(v), "php") || strings.EqualFold(v, "phtml") || strings.EqualFold(v, "phar"))) {
				return errors.New("扩展名只允许普通静态类型，不可为 PHP")
			}
		}
	}
	for _, v := range strings.Fields(c.String("hotlinkDomains")) {
		if _, err := SiteBindingValue(v, 80); err != nil || strings.Contains(v, ":") {
			return errors.New("防盗链允许来源须为域名，不含协议或端口")
		}
	}
	for key, max := range map[string]int{"maxConnections": 1000000, "perIPConnections": 100000, "rateKB": 1048576, "cacheSeconds": 31536000, "gzipLevel": 9} {
		v := c.Int(key)
		if v < 0 || v > max {
			return fmt.Errorf("%s 超出允许范围", key)
		}
	}
	if c.Bool("limitEnabled") && (c.Int("maxConnections") < 1 || c.Int("perIPConnections") < 1) {
		return errors.New("启用流量限制时并发数须大于零")
	}
	if c.Bool("hotlinkEnabled") && (c.String("hotlinkExtensions") == "" || c.String("hotlinkDomains") == "") {
		return errors.New("开启防盗链需填写扩展名与允许域名")
	}
	if c.Bool("cacheEnabled") && (c.Int("cacheSeconds") < 1 || c.String("cacheExtensions") == "") {
		return errors.New("开启静态缓存需填写扩展名与有效期")
	}
	var headers map[string]string
	if err := decodeSiteOption(c, "responseHeaders", &headers, 8192); err != nil {
		return err
	}
	allowed := map[string]bool{"Content-Security-Policy": true, "Referrer-Policy": true, "X-Content-Type-Options": true, "X-Frame-Options": true, "Permissions-Policy": true}
	for key, value := range headers {
		if !allowed[key] || len(value) > 2048 || strings.ContainsAny(value, "\x00\r\n\\\"$") {
			return errors.New("响应头名称或内容无效")
		}
	}
	if c.Bool("corsEnabled") {
		origin := c.String("corsOrigin")
		if origin != "*" {
			u, e := url.Parse(origin)
			if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(origin, "\x00\r\n\t ;{}\"'\\$") {
				return errors.New("跨域来源须为单个 HTTP/HTTPS Origin 或 *")
			}
		} else if c.Bool("corsCredentials") {
			return errors.New("携带凭据时不能允许任意跨域来源")
		}
		for _, method := range strings.Fields(strings.ReplaceAll(c.String("corsMethods"), ",", " ")) {
			switch method {
			case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
			default:
				return errors.New("跨域方法无效")
			}
		}
		for _, h := range strings.Fields(strings.ReplaceAll(c.String("corsHeaders"), ",", " ")) {
			if !regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`).MatchString(h) {
				return errors.New("跨域请求头无效")
			}
		}
	}
	return nil
}
func SortedSiteAccess(c model.Config) []string {
	rules, _ := SiteAccessRules(c)
	keys := []string{}
	for id := range rules {
		keys = append(keys, id)
	}
	sort.Slice(keys, func(i, j int) bool { return len(rules[keys[i]].Path) > len(rules[keys[j]].Path) })
	return keys
}
