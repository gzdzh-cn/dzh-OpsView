package preferences

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	_ "golang.org/x/image/webp"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net"
	"opsview/internal/model"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var Defaults = model.Config{"name": "OpsView", "sidebarTitle": "OpsView", "refreshSeconds": 15, "sessionMinutes": 480, "idleMinutes": 60, "backupRetention": 7, "siteDirectory": "/www/wwwroot", "backupDirectory": "/www/backup", "backupStorageId": "", "timezone": "Local", "webDisabled": false, "theme": "light", "primaryColor": "#2563eb", "fontSize": 14, "hiddenMenus": []string{}, "loginLogo": "", "loginBackground": "", "favicon": "", "idleOptimization": true, "passwordMinLength": 12, "passwordRequireMixed": false, "passwordExpiryDays": 0, "unauthenticatedStatus": 0, "basicEnabled": false, "basicUsername": "", "basicPasswordHash": "", "clientCertificateEnabled": false, "clientCA": "", "apiEnabled": false, "userAgentAllow": "", "loginCountries": []string{}, "unknownCountry": "deny"}
var Groups = map[string][]string{
	"panel":      {"refreshSeconds", "siteDirectory", "backupDirectory", "timezone", "webDisabled", "idleOptimization"},
	"security":   {"sessionMinutes", "idleMinutes", "passwordMinLength", "passwordRequireMixed", "passwordExpiryDays", "unauthenticatedStatus", "basicEnabled", "basicUsername", "basicPasswordHash", "clientCertificateEnabled", "clientCA", "apiEnabled", "userAgentAllow", "loginCountries", "unknownCountry"},
	"appearance": {"theme", "primaryColor", "fontSize", "hiddenMenus", "loginLogo", "loginBackground", "favicon"},
	"backup":     {"backupRetention", "backupDirectory", "backupStorageId"},
}

func Merge(c model.Config) model.Config {
	v := model.Config{}
	for k, x := range Defaults {
		v[k] = x
	}
	for k, x := range c {
		v[k] = x
	}
	return v
}
func Validate(group string, p model.Config) error {
	keys, ok := Groups[group]
	if !ok {
		return errors.New("设置分组无效")
	}
	allowed := map[string]bool{}
	for _, k := range keys {
		allowed[k] = true
	}
	for k, v := range p {
		if !allowed[k] {
			return errors.New("不支持的设置字段：" + k)
		}
		switch Defaults[k].(type) {
		case int:
			n, err := strconv.ParseInt(fmt.Sprint(v), 10, 32)
			if err != nil || int64(p.Int(k)) != n {
				return errors.New("数值须为整数")
			}
		case string:
			if _, ok := v.(string); !ok {
				return errors.New("文本须为字符串")
			}
		}
		switch k {
		case "name", "sidebarTitle":
			if len(p.String(k)) == 0 || len(p.String(k)) > 80 {
				return errors.New("名称长度须为 1–80 字节")
			}
		case "refreshSeconds":
			if p.Int(k) != 5 && p.Int(k) != 15 && p.Int(k) != 30 && p.Int(k) != 60 {
				return errors.New("刷新周期无效")
			}
		case "sessionMinutes":
			if p.Int(k) < 15 || p.Int(k) > 10080 {
				return errors.New("会话期限须为 15–10080 分钟")
			}
		case "idleMinutes":
			if p.Int(k) < 5 || p.Int(k) > 10080 {
				return errors.New("无操作超时须为 5–10080 分钟")
			}
		case "backupRetention":
			if p.Int(k) < 1 || p.Int(k) > 30 {
				return errors.New("备份保留须为 1–30 份")
			}
		case "backupStorageId":
			if p.String(k) != "" && !regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`).MatchString(p.String(k)) {
				return errors.New("备份存储标识无效")
			}
		case "siteDirectory", "backupDirectory":
			path := p.String(k)
			if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") || filepath.Clean(path) == "/" {
				return errors.New("默认目录须为有效绝对路径")
			}
		case "timezone":
			if _, err := time.LoadLocation(p.String(k)); err != nil {
				return errors.New("时区无效")
			}
		case "theme":
			if p.String(k) != "auto" && p.String(k) != "light" && p.String(k) != "dark" {
				return errors.New("主题无效")
			}
		case "primaryColor":
			if !regexp.MustCompile(`^#[a-fA-F0-9]{6}$`).MatchString(p.String(k)) {
				return errors.New("主题色无效")
			}
		case "fontSize":
			if p.Int(k) < 12 || p.Int(k) > 18 {
				return errors.New("字号须为 12–18")
			}
		case "passwordMinLength":
			if p.Int(k) < 12 || p.Int(k) > 64 {
				return errors.New("密码最小长度须为 12–64")
			}
		case "passwordExpiryDays":
			if p.Int(k) != 0 && (p.Int(k) < 10 || p.Int(k) > 1825) {
				return errors.New("密码有效期须为 10–1825 天，0 表示关闭")
			}
		case "unauthenticatedStatus":
			if p.Int(k) != 0 && p.Int(k) != 403 && p.Int(k) != 404 {
				return errors.New("未认证响应仅支持登录、403 或 404")
			}
		case "userAgentAllow":
			if len(p.String(k)) > 512 {
				return errors.New("User-Agent 规则过长")
			}
			if p.String(k) != "" {
				if _, err := regexp.Compile(p.String(k)); err != nil {
					return errors.New("User-Agent 正则无效")
				}
			}
		case "unknownCountry":
			if p.String(k) != "allow" && p.String(k) != "deny" {
				return errors.New("未知地区策略无效")
			}
		case "loginCountries":
			b, ok := v.([]any)
			if values, isStrings := v.([]string); isStrings {
				b = make([]any, len(values))
				for i, x := range values {
					b[i] = x
				}
				ok = true
			}
			if !ok {
				if _, ok = v.([]string); !ok {
					return errors.New("地区须为数组")
				}
			}
			for _, x := range b {
				str, ok := x.(string)
				if !ok || !regexp.MustCompile(`^[A-Z]{2}$`).MatchString(str) {
					return errors.New("地区使用两位大写国家代码")
				}
			}
		case "hiddenMenus":
			b, ok := v.([]any)
			if values, isStrings := v.([]string); isStrings {
				b = make([]any, len(values))
				for i, x := range values {
					b[i] = x
				}
				ok = true
			}
			if !ok {
				if _, ok = v.([]string); !ok {
					return errors.New("菜单须为数组")
				}
			}
			allowed := map[string]bool{"/": true, "/sites": true, "/databases": true, "/docker": true, "/software": true, "/files": true, "/backups": true, "/system": true}
			for _, x := range b {
				str, ok := x.(string)
				if !ok || !allowed[str] {
					return errors.New("菜单不可隐藏或不存在")
				}
			}
		case "loginLogo", "loginBackground", "favicon":
			if p.String(k) != "" && !strings.HasPrefix(p.String(k), "data:image/png;base64,") && !strings.HasPrefix(p.String(k), "data:image/jpeg;base64,") && !strings.HasPrefix(p.String(k), "data:image/webp;base64,") {
				return errors.New("只允许上传 PNG/JPEG/WebP 图片")
			}
			if value := p.String(k); value != "" {
				parts := strings.SplitN(value, ",", 2)
				decoded, err := base64.StdEncoding.DecodeString(parts[1])
				if err != nil {
					return errors.New("图片编码无效")
				}
				info, format, err := image.DecodeConfig(bytes.NewReader(decoded))
				if err != nil || info.Width < 1 || info.Height < 1 || info.Width > 8192 || info.Height > 8192 || !strings.HasPrefix(parts[0], "data:image/"+format+";") {
					return errors.New("图片内容或尺寸无效")
				}
			}
			if len(p.String(k)) > 2<<20 {
				return errors.New("图片须小于 1.5 MiB")
			}
		case "basicEnabled", "clientCertificateEnabled", "apiEnabled", "webDisabled", "idleOptimization", "passwordRequireMixed":
			if _, ok := v.(bool); !ok {
				return errors.New("开关须为布尔值")
			}
		}
	}
	return nil
}
func AllowedSource(ip, list string) bool {
	if strings.TrimSpace(list) == "" {
		return true
	}
	for _, s := range strings.Fields(list) {
		if s == ip {
			return true
		}
		if _, n, err := net.ParseCIDR(s); err == nil && n.Contains(net.ParseIP(ip)) {
			return true
		}
	}
	return false
}
