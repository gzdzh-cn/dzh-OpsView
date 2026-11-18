package executor

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"opsview/internal/logic"
	"opsview/internal/model"
	"os"
	"path/filepath"
	"strings"
)

func prepareSiteRunningDirectory(root, directory string) error {
	if err := logic.ValidateSiteOptions(model.Config{"runningDirectory": directory}); err != nil {
		return err
	}
	if directory == "" || directory == "/" {
		return nil
	}
	scoped, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer scoped.Close()
	p := ""
	for _, part := range strings.Split(directory, "/") {
		p = filepath.Join(p, part)
		st, er := scoped.Lstat(p)
		if os.IsNotExist(er) {
			if er = scoped.Mkdir(p, 0755); er != nil {
				return er
			}
			if er = scoped.Chmod(p, 0755); er != nil {
				return er
			}
			continue
		}
		if er != nil {
			return er
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return errors.New("运行目录包含符号链接或普通文件")
		}
	}
	return nil
}

func siteRunningRoot(r model.Resource, base string) string {
	dir := r.Config.String("runningDirectory")
	if dir == "/" {
		dir = ""
	}
	return filepath.Join(siteDocumentRoot(r, base), dir)
}

func siteIndex(r model.Resource) string {
	if docs := strings.Fields(r.Config.String("defaultDocuments")); len(docs) > 0 {
		return strings.Join(docs, " ")
	}
	if r.Config.String("type") == "php" {
		return "index.php index.html"
	}
	return "index.html"
}

func siteTryFiles(r model.Resource) string {
	switch r.Config.String("rewriteTemplate") {
	case "none":
		return "try_files $uri $uri/ =404;"
	case "thinkphp":
		return "try_files $uri $uri/ /index.php?s=$uri&$args;"
	case "spa":
		return "try_files $uri $uri/ /index.html;"
	case "front-controller", "laravel", "wordpress":
		return "try_files $uri $uri/ /index.php?$query_string;"
	}
	if r.Config.String("type") == "php" {
		return "try_files $uri $uri/ /index.php?$query_string;"
	}
	return "try_files $uri $uri/ /index.html;"
}

func renderProxyRoutes(r model.Resource, redirect string) (string, string) {
	routes, _ := logic.SiteProxyRoutes(r.Config) // validated at both API and executor entry points
	upstreams, locations := "", ""
	for i, route := range routes {
		name := fmt.Sprintf("ov_%s_%d", strings.NewReplacer(".", "_", "-", "_").Replace(r.ID), i)
		upstreams += "upstream " + name + " {\n"
		var first *url.URL
		for _, target := range route.Targets {
			u, _ := url.Parse(target)
			if first == nil {
				first = u
			}
			port := u.Port()
			if port == "" {
				port = "80"
				if u.Scheme == "https" {
					port = "443"
				}
			}
			upstreams += " server " + net.JoinHostPort(u.Hostname(), port) + ";\n"
		}
		upstreams += "}\n"
		locations += " location " + route.Path + " {\n" + redirect + " proxy_pass " + first.Scheme + "://" + name + ";\n proxy_http_version 1.1;\n proxy_set_header Host " + route.Host + ";\n proxy_set_header X-Real-IP $remote_addr;\n proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n proxy_set_header X-Forwarded-Proto $scheme;\n"
		locations += fmt.Sprintf(" proxy_connect_timeout %ds;\n proxy_read_timeout %ds;\n proxy_send_timeout %ds;\n", route.ConnectTimeout, route.ReadTimeout, route.SendTimeout)
		if route.Websocket {
			locations += " proxy_set_header Upgrade $http_upgrade;\n proxy_set_header Connection $opsview_connection;\n"
		} else {
			locations += " proxy_set_header Connection \"\";\n"
		}
		if first.Scheme == "https" {
			locations += " proxy_ssl_server_name on;\n proxy_ssl_name " + first.Hostname() + ";\n"
		}
		locations += " }\n"
	}
	return upstreams, locations
}
