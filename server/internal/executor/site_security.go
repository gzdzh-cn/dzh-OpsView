package executor

import (
	"fmt"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func siteUpstream(r model.Resource) string {
	if r.Config.String("type") == "managed" {
		return "http://127.0.0.1:" + strconv.Itoa(r.Config.Int("port"))
	}
	return r.Config.String("upstream")
}
func siteProxyHost(r model.Resource) string {
	if host := r.Config.String("proxyHost"); host != "" {
		return host
	}
	return "$host"
}
func siteSecurityConfig(r model.Resource) string {
	s := ""
	// Preserve upstream application routes; protect local document roots unconditionally.
	if r.Config.String("type") == "php" || r.Config.String("type") == "static" {
		s += " location ~ /\\.(?!well-known/) { deny all; }\n location ~* \\.(?:sql|bak|old|orig|ini|log)$ { deny all; }\n"
	}
	if r.Config.String("type") == "php" && r.Config.Bool("denyUploadPHP") {
		s += " location ~* ^/(?:uploads?|storage)/.*\\.php(?:/|$) { deny all; }\n"
	}
	return s
}
func (e *Engine) siteSecurity(r model.Resource) (model.Config, error) {
	stored, err := e.load(r.ID)
	if err != nil {
		return nil, err
	}
	r = stored
	checks := []model.Config{}
	add := func(name, status, detail string) {
		checks = append(checks, model.Config{"name": name, "status": status, "detail": detail})
	}
	if r.Config.Bool("tls") {
		expiry, er := time.Parse(time.RFC3339, r.Config.String("certificateExpires"))
		if er != nil {
			add("HTTPS 证书", "unknown", "无法读取有效期")
		} else if time.Until(expiry) < 7*24*time.Hour {
			add("HTTPS 证书", "warning", "证书已到期或剩余不足 7 天")
		} else {
			add("HTTPS 证书", "pass", "有效至 "+expiry.Format(time.RFC3339))
		}
	} else {
		add("HTTPS 证书", "warning", "尚未启用 HTTPS")
	}
	base := filepath.Join(e.Root, "sites", r.ID)
	confpath := filepath.Join(e.Root, "instances", r.Config.String("instanceId"), "conf", "sites", r.ID+".conf")
	b, er := os.ReadFile(confpath)
	if er != nil {
		add("运行配置", "unknown", "无法读取 Nginx 配置")
	} else if security.Hash(string(b)) != r.Config.String("configHash") {
		add("运行配置", "warning", "存在外部修改，请核对后再应用")
	} else {
		add("运行配置", "pass", "与已部署修订一致")
	}
	for _, item := range []struct{ key, label, token string }{{"securityHeaders", "安全响应头", "X-Content-Type-Options nosniff"}, {"denyFrames", "同源嵌入限制", "X-Frame-Options SAMEORIGIN"}} {
		if strings.Contains(string(b), item.token) {
			add(item.label, "pass", "已写入当前配置")
		} else {
			add(item.label, "warning", "尚未启用，可在网站安全中设置")
		}
	}
	if r.Config.String("type") == "static" || r.Config.String("type") == "php" || r.Config.String("type") == "managed" {
		root, er := e.trustedSiteRoot(r)
		if er != nil {
			return nil, er
		}
		scoped, er := os.OpenRoot(root)
		if er != nil {
			return nil, er
		}
		defer scoped.Close()
		st, er := scoped.Stat(".")
		if er != nil {
			return nil, er
		}
		if st.Mode().Perm()&0002 != 0 {
			add("项目目录权限", "warning", "目录允许所有用户写入")
		} else {
			add("项目目录权限", "pass", "未开放全局写权限")
		}
		for _, name := range []string{".env", ".git", ".svn", "backup.sql", "database.sql", "phpinfo.php", "info.php"} {
			st, er := scoped.Lstat(name)
			if os.IsNotExist(er) {
				continue
			}
			if er != nil {
				add("敏感文件检查", "unknown", "无法检查 "+name)
				continue
			}
			kind := "文件"
			if st.IsDir() {
				kind = "目录"
			}
			add("敏感文件检查", "warning", fmt.Sprintf("发现 %s（%s），请确认是否需要保留；存在不代表可公网下载", name, kind))
		}
		add("扫描范围", "info", "仅检查项目根目录的常见敏感条目和权限；不读取文件内容、不执行漏洞利用、不连接第三方扫描服务")
	} else {
		add("上游应用", "info", "反代上游的代码与文件不在本机扫描范围")
	}
	return model.Config{"checkedAt": time.Now().UTC().Format(time.RFC3339), "checks": checks, "documentRoot": siteDocumentRoot(r, base), "scope": "local-baseline"}, nil
}
