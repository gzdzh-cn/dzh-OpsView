package logic

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"opsview/internal/model"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var Name = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)
var Domain = regexp.MustCompile(`^(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)*[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)
var SQLName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,62}$`)
var Kinds = map[string]bool{"site": true, "instance": true, "database": true, "app": true, "compose": true, "firewall": true, "backup": true, "schedule": true, "plugin": true, "repository": true}

func Validate(r model.Resource) error {
	if !Kinds[r.Kind] {
		return errors.New("不支持的资源类型")
	}
	if !Name.MatchString(r.Name) {
		return errors.New("名称须为 1–63 位字母、数字、点、横线或下划线")
	}
	c := r.Config
	for _, k := range []string{"port", "httpPort", "httpsPort"} {
		if r.Kind == "instance" && c.String("software") == "nodejs" && k == "port" {
			continue
		}
		if _, ok := c[k]; ok && (c.Int(k) < 1 || c.Int(k) > 65535) {
			return fmt.Errorf("%s 必须在 1–65535 之间", k)
		}
	}
	switch r.Kind {
	case "site":
		if !Name.MatchString(c.String("instanceId")) {
			return errors.New("请选择 Nginx 实例")
		}
		if c.String("type") != "static" && c.String("type") != "proxy" {
			return errors.New("网站类型无效")
		}
		domains := strings.Fields(c.String("domains"))
		if len(domains) == 0 {
			return errors.New("至少填写一个域名")
		}
		for _, d := range domains {
			if len(d) > 253 || !Domain.MatchString(d) {
				return errors.New("域名无效")
			}
		}
		if err := ValidateNginxAdvanced(c.String("advanced")); err != nil {
			return err
		}
		if c.String("type") == "proxy" {
			u, e := url.Parse(c.String("upstream"))
			if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || strings.ContainsAny(c.String("upstream"), ";{}\r\n\t \"'\\$") {
				return errors.New("反向代理地址无效")
			}
		}
	case "instance":
		if c.String("mode") != "native" && c.String("mode") != "docker" {
			return errors.New("请选择安装方式")
		}
		if !Name.MatchString(c.String("catalogId")) {
			return errors.New("请选择软件版本")
		}
		if c.Int("port") == 0 && c.String("software") != "nodejs" {
			return errors.New("请填写实例端口")
		}
	case "database":
		if !SQLName.MatchString(r.Name) || !SQLName.MatchString(c.String("username")) {
			return errors.New("数据库和用户名只允许字母、数字、下划线，且必须以字母开头")
		}
		if !Name.MatchString(c.String("instanceId")) {
			return errors.New("请选择数据库实例")
		}
		if len(c.String("password")) < 12 || len(c.String("password")) > 128 || strings.ContainsAny(c.String("password"), "\x00\n\r") {
			return errors.New("密码需为 12–128 位且不包含换行")
		}
		if c.String("source") == "" {
			c["source"] = "127.0.0.1"
		}
		if net.ParseIP(c.String("source")) == nil {
			if _, _, e := net.ParseCIDR(c.String("source")); e != nil {
				return errors.New("连接来源必须是 IP 或 CIDR")
			}
		}
	case "app":
		if !filepath.IsAbs(c.String("executable")) || strings.ContainsAny(c.String("executable"), "\r\n%\"\\") {
			return errors.New("启动程序须为服务器上的绝对路径")
		}
		if len(c.String("arguments")) > 4096 || strings.ContainsAny(c.String("arguments"), "\x00\r\n") {
			return errors.New("启动参数无效")
		}
		if !Name.MatchString(c.String("user")) {
			return errors.New("请指定非 root 运行用户")
		}
		if c.String("user") == "root" {
			return errors.New("应用不能以 root 运行")
		}
	case "firewall":
		if c.String("protocol") != "tcp" && c.String("protocol") != "udp" {
			return errors.New("协议须为 tcp 或 udp")
		}
		if c.String("action") != "allow" && c.String("action") != "deny" {
			return errors.New("规则动作无效")
		}
		p := strings.Split(c.String("ports"), ":")
		if len(p) > 2 || len(p) == 0 {
			return errors.New("端口格式无效")
		}
		last := 0
		for _, x := range p {
			n, e := strconv.Atoi(x)
			if e != nil || n < 1 || n > 65535 || n < last {
				return errors.New("端口范围无效")
			}
			last = n
		}
		src := c.String("source")
		if src != "any" && net.ParseIP(src) == nil {
			if _, _, e := net.ParseCIDR(src); e != nil {
				return errors.New("来源须为 any、IP 或 CIDR")
			}
		}
		if c.String("scope") != "host" && c.String("scope") != "docker" {
			return errors.New("规则范围无效")
		}
	case "backup":
		if !Name.MatchString(c.String("targetId")) {
			return errors.New("请选择备份资源")
		}
		if c.String("destination") != "local" && c.String("destination") != "s3" {
			return errors.New("备份目标无效")
		}
		if c.String("destination") == "s3" {
			u, e := url.Parse(c.String("endpoint"))
			if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
				return errors.New("S3 端点须使用 HTTPS")
			}
			if c.String("bucket") == "" || c.String("accessKey") == "" || c.String("secretKey") == "" {
				return errors.New("请填写 S3 存储桶及凭据")
			}
		}
	case "schedule":
		if c.String("cron") == "" || !Name.MatchString(c.String("targetId")) {
			return errors.New("请填写 Cron 表达式和任务目标")
		}
		if c.String("operation") != "backup" && c.String("operation") != "renew" {
			return errors.New("计划任务仅支持备份和证书续期")
		}
	case "repository":
		u, e := url.Parse(c.String("url"))
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
			return errors.New("仓库地址须使用 HTTPS")
		}
	}
	return nil
}

// Only these scalar directives may be entered through the advanced editor.
func ValidateNginxAdvanced(text string) error {
	if len(text) > 4096 {
		return errors.New("高级配置最多 4096 字节")
	}
	sizes := regexp.MustCompile(`^[1-9][0-9]{0,5}[kKmMgG]?$`)
	times := regexp.MustCompile(`^[1-9][0-9]{0,5}[smh]?$`)
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasSuffix(line, ";") {
			return errors.New("高级指令以分号结束")
		}
		fields := strings.Fields(strings.TrimSuffix(line, ";"))
		if len(fields) != 2 || seen[fields[0]] {
			return errors.New("高级配置需为不重复的单值指令")
		}
		seen[fields[0]] = true
		valid := false
		switch fields[0] {
		case "client_max_body_size":
			valid = sizes.MatchString(fields[1])
		case "proxy_read_timeout", "proxy_connect_timeout", "proxy_send_timeout", "send_timeout":
			valid = times.MatchString(fields[1])
		case "gzip":
			valid = fields[1] == "on" || fields[1] == "off"
		}
		if !valid {
			return errors.New("只允许 client_max_body_size、proxy/send 超时和 gzip 的安全单值配置")
		}
	}
	return nil
}
