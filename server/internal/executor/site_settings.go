package executor

import (
	"encoding/json"
	"fmt"
	"opsview/internal/logic"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

func siteVariable(r model.Resource) string {
	return "ov_" + strings.NewReplacer("-", "_", ".", "_").Replace(r.ID)
}
func staticExtensions(c model.Config, key string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(c.String(key), ",", " ")), "|")
}
func siteSettingsHTTP(r model.Resource, base string) string {
	c := r.Config
	v := siteVariable(r)
	s := ""
	if c.Bool("limitEnabled") {
		s += fmt.Sprintf("map $host $%s_key { default %s; }\nlimit_conn_zone $%s_key zone=%s_site:1m;\nlimit_conn_zone $binary_remote_addr zone=%s_ip:1m;\n", v, v, v, v, v)
	}
	rules, _ := logic.SiteAccessRules(c)
	if len(rules) > 0 {
		s += "map $uri $" + v + "_realm {\n default off;\n"
		for _, id := range logic.SortedSiteAccess(c) {
			p := regexp.QuoteMeta(rules[id].Path)
			if p != "/" {
				p = strings.TrimSuffix(p, "/") + "(?:/|$)"
			}
			s += " ~^" + p + " \"Restricted\";\n"
		}
		s += "}\nmap $uri $" + v + "_auth {\n default \"\";\n"
		for _, id := range logic.SortedSiteAccess(c) {
			p := regexp.QuoteMeta(rules[id].Path)
			if p != "/" {
				p = strings.TrimSuffix(p, "/") + "(?:/|$)"
			}
			s += " ~^" + p + " " + filepath.Join(base, "auth", c.String("probeToken"), id) + ";\n"
		}
		s += "}\n"
	}
	if c.Bool("hotlinkEnabled") {
		s += "map \"$invalid_referer:$uri\" $" + v + "_hotlink {\n default 0;\n ~*^1:.*\\.(?:" + staticExtensions(c, "hotlinkExtensions") + ")$ 1;\n}\n"
	}
	if c.Bool("cacheEnabled") {
		s += "map $uri $" + v + "_expires {\n default off;\n ~*\\.(?:" + staticExtensions(c, "cacheExtensions") + ")$ " + strconv.Itoa(c.Int("cacheSeconds")) + "s;\n}\n"
	}
	if c.Bool("clientAuthEnabled") {
		s += "map \"$uri:$ssl_client_verify\" $" + v + "_mtls {\n default 1;\n ~:SUCCESS$ 0;\n ~^/\\.opsview-health-" + c.String("probeToken") + ": 0;\n ~^/\\.well-known/acme-challenge/ 0;\n}\n"
	}

	return s
}
func siteSettingsServer(r model.Resource) string {
	if siteUnavailable(r) {
		return ""
	}
	c := r.Config
	v := siteVariable(r)
	s := ""
	if rules, _ := logic.SiteAccessRules(c); len(rules) > 0 {
		s += " auth_basic $" + v + "_realm;\n auth_basic_user_file $" + v + "_auth;\n"
	}
	for _, p := range strings.Fields(c.String("denyPaths")) {
		s += " if ($uri ~ \"^" + regexp.QuoteMeta(strings.TrimSuffix(p, "/")) + "(?:/|$)\") { return 403; }\n"
	}
	if ext := staticExtensions(c, "denyExtensions"); ext != "" {
		s += " if ($uri ~* \"\\.(?:" + ext + ")$\") { return 403; }\n"
	}
	// Explicit denials precede the optional allow-list. Nginx uses the first matching rule.
	for _, ip := range strings.Fields(c.String("denyAddresses")) {
		s += " deny " + ip + ";\n"
	}
	allow := strings.Fields(c.String("allowAddresses"))
	for _, ip := range allow {
		s += " allow " + ip + ";\n"
	}
	if len(allow) > 0 {
		s += " deny all;\n"
	}
	if c.Bool("limitEnabled") {
		s += fmt.Sprintf(" limit_conn %s_site %d;\n limit_conn %s_ip %d;\n limit_conn_status 429;\n", v, c.Int("maxConnections"), v, c.Int("perIPConnections"))
		if c.Int("rateKB") > 0 {
			s += " limit_rate " + strconv.Itoa(c.Int("rateKB")) + "k;\n"
		}
	}
	if _, exists := c["gzipEnabled"]; exists && !c.Bool("gzipEnabled") {
		s += " gzip off;\n"
	}
	if c.Bool("gzipEnabled") {
		level := c.Int("gzipLevel")
		if level == 0 {
			level = 5
		}
		s += fmt.Sprintf(" gzip on;\n gzip_vary on;\n gzip_comp_level %d;\n gzip_min_length 1024;\n gzip_types text/plain text/css application/json application/javascript application/xml image/svg+xml;\n", level)
	}
	if c.Bool("hotlinkEnabled") {
		s += " valid_referers "
		if c.Bool("hotlinkAllowEmpty") {
			s += "none blocked "
		}
		status := c.Int("hotlinkStatus")
		if status == 0 {
			status = 403
		}
		s += strings.Join(strings.Fields(c.String("hotlinkDomains")), " ") + ";\n if ($" + v + "_hotlink) { return " + strconv.Itoa(status) + "; }\n"
	}
	if c.Bool("cacheEnabled") {
		s += " expires $" + v + "_expires;\n"
	}
	headers := map[string]string{}
	b, _ := json.Marshal(c["responseHeaders"])
	_ = json.Unmarshal(b, &headers)
	if headers == nil {
		headers = map[string]string{}
	}
	if c.Bool("securityHeaders") {
		if headers["X-Content-Type-Options"] == "" {
			headers["X-Content-Type-Options"] = "nosniff"
		}
		if headers["Referrer-Policy"] == "" {
			headers["Referrer-Policy"] = "strict-origin-when-cross-origin"
		}
	}
	if c.Bool("denyFrames") && headers["X-Frame-Options"] == "" {
		headers["X-Frame-Options"] = "SAMEORIGIN"
	}
	keys := []string{}
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if headers[key] != "" {
			s += " add_header " + key + " \"" + headers[key] + "\" always;\n"
		}
	}
	if c.Bool("corsEnabled") {
		methods := strings.Join(strings.Fields(strings.ReplaceAll(c.String("corsMethods"), ",", " ")), ", ")
		if methods == "" {
			methods = "GET, HEAD, POST, OPTIONS"
		}
		s += " add_header Access-Control-Allow-Origin \"" + c.String("corsOrigin") + "\" always;\n add_header Vary Origin always;\n add_header Access-Control-Allow-Methods \"" + methods + "\" always;\n"
		if c.Bool("corsCredentials") {
			s += " add_header Access-Control-Allow-Credentials true always;\n"
		}
		if h := strings.Join(strings.Fields(strings.ReplaceAll(c.String("corsHeaders"), ",", " ")), ", "); h != "" {
			s += " add_header Access-Control-Allow-Headers \"" + h + "\" always;\n"
		}
		s += " if ($request_method = OPTIONS) { return 204; }\n"
	}
	rules, _ := logic.SiteRedirects(c)
	for _, rule := range rules {
		modifier := "^~ "
		if rule.Exact {
			modifier = "= "
		}
		s += fmt.Sprintf(" location %s%s { return %d %s; }\n", modifier, rule.Path, rule.Code, rule.Target)
	}
	if c.Bool("clientAuthEnabled") {
		s += " if ($" + v + "_mtls) { return 403; }\n"
	}

	if target := c.String("redirect404Target"); target != "" {
		s += " error_page 404 =302 " + target + ";\n proxy_intercept_errors on;\n"
	}

	return s
}
func prepareSiteAccess(r model.Resource, base string) error {
	rules, err := logic.SiteAccessRules(r.Config)
	if err != nil {
		return err
	}
	if len(rules) == 0 && !r.Config.Bool("clientAuthEnabled") {
		return nil
	}
	dir := filepath.Join(base, "auth", r.Config.String("probeToken"))
	if err = os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	for _, path := range []string{filepath.Join(base, "auth"), dir} {
		if err = os.Chmod(path, 0755); err != nil {
			return err
		}
	}
	for id, rule := range rules {
		if err = security.WriteAtomic(filepath.Join(dir, id), []byte(rule.Username+":"+rule.Password+"\n"), 0644); err != nil {
			return err
		}
	}
	if r.Config.Bool("clientAuthEnabled") {
		if err = security.WriteAtomic(filepath.Join(dir, "client-ca.pem"), []byte(r.Config.String("clientCA")), 0644); err != nil {
			return err
		}
	}

	return nil
}

// One generated server per listener/directory group; all groups share the site's policy.
func (e *Engine) renderSite(r, inst model.Resource, base string) string {
	bindings, _ := logic.SiteBindings(r.Config, inst.Config.Int("port"))
	type group struct {
		port      int
		directory string
		hosts     []string
	}
	groups := []group{}
	for _, b := range bindings {
		at := -1
		for i, g := range groups {
			if g.port == b.Port && g.directory == b.Directory {
				at = i
				break
			}
		}
		if at < 0 {
			groups = append(groups, group{port: b.Port, directory: b.Directory})
			at = len(groups) - 1
		}
		groups[at].hosts = append(groups[at].hosts, b.Host)
	}
	result := siteSettingsHTTP(r, base)
	if !siteUnavailable(r) {
		upstreams, _ := renderProxyRoutes(r, "")
		result += upstreams
	}
	tlsHosts := map[string]bool{}
	for _, g := range groups {
		copy := r
		copy.Config = model.Config{}
		for k, v := range r.Config {
			copy.Config[k] = v
		}
		copy.Config["domains"] = strings.Join(g.hosts, " ")
		if g.directory != "" {
			copy.Config["runningDirectory"] = g.directory
		}
		listener := inst
		listener.Config = model.Config{}
		for k, v := range inst.Config {
			listener.Config[k] = v
		}
		listener.Config["port"] = g.port
		// A hostname can be bound to several plain ports, but is emitted only once for TLS.
		copy.Config["_hasTLS"] = r.Config.Bool("tls")
		copy.Config["tls"] = false
		result += e.renderSiteServer(copy, listener, base, false)
		if r.Config.Bool("tls") {
			hosts := []string{}
			for _, h := range g.hosts {
				if !tlsHosts[h] {
					hosts = append(hosts, h)
					tlsHosts[h] = true
				}
			}
			if len(hosts) > 0 {
				copy.Config["domains"] = strings.Join(hosts, " ")
				copy.Config["tls"] = true
				result += e.renderSiteServer(copy, listener, base, true)
			}
		}
	}
	return result
}
