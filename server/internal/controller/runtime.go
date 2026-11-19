package controller

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"opsview/internal/model"
	"opsview/internal/runtimeconfig"
	"opsview/internal/security"
	"strconv"
	"strings"
	"time"
)

func (c *Controller) runtimeSettings(w http.ResponseWriter, r *http.Request) {
	op := "system.panel-status"
	params := model.Config{}
	if r.Method == "PUT" {
		c.accessMu.Lock()
		defer c.accessMu.Unlock()
		var pending model.Config
		_ = c.App.Store.Setting("preference-pending", &pending)
		if pending.Int("deadline") > 0 {
			fail(w, 409, "请先确认安全设置或等待回退")
			return
		}
		if !decode(w, r, &params) {
			return
		}
		if !c.verifyIdentity(r, params.String("password"), params.String("code")) {
			fail(w, 403, "修改访问设置需重新验证管理员身份")
			return
		}
		if current := c.panelPreferences(); current.Bool("clientCertificateEnabled") {
			raw, _ := json.Marshal(params["config"])
			var next model.Config
			_ = json.Unmarshal(raw, &next)
			if next.String("protocol") == "http" {
				fail(w, 409, "请先关闭客户端证书认证并确认，再关闭 HTTPS")
				return
			}
		}
		delete(params, "password")
		delete(params, "code")
		op = "system.panel-apply"
		if params.String("source") == "" {
			params["source"] = clientIP(r)
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	data, err := c.App.Executor.Execute(ctx, model.Operation{Name: op, Params: params}, func(string) {})
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	if r.Method == "PUT" {
		c.App.Store.Audit("settings.runtime", "", clientIP(r))
	}
	send(w, 200, data)
}
func (c *Controller) runtimeConfirm(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.Token) != 64 {
		fail(w, 400, "确认凭证无效")
		return
	}
	if c.App.PanelConfigPath != "" {
		cfg, err := runtimeconfig.Load(c.App.PanelConfigPath)
		if err != nil {
			fail(w, 503, "运行配置不可用")
			return
		}
		if (r.TLS != nil) != (cfg.Scheme() == "https") {
			fail(w, 409, "请从新协议的登录地址重新连接确认")
			return
		}
		if address, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
			_, port, er := net.SplitHostPort(address.String())
			if er != nil || port != strconv.Itoa(cfg.Port) {
				fail(w, 409, "请从新端口重新连接确认")
				return
			}
		}
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if cfg.AllowedHost != "" && !strings.EqualFold(host, cfg.AllowedHost) {
			fail(w, 403, "请从受限域名重新连接确认")
			return
		}
		if cfg.AllowedCIDRs != nil && strings.TrimSpace(*cfg.AllowedCIDRs) != "" {
			allowed := false
			for _, item := range strings.Fields(*cfg.AllowedCIDRs) {
				if _, cidr, err := net.ParseCIDR(item); err == nil && cidr.Contains(net.ParseIP(clientIP(r))) {
					allowed = true
				}
			}
			if !allowed {
				fail(w, 403, "来源 IP 不在新访问范围内，设置将自动回退")
				return
			}
		}
	}
	data, err := c.App.Executor.Execute(r.Context(), model.Operation{Name: "system.panel-confirm", Params: model.Config{"token": body.Token}}, func(string) {})
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	send(w, 200, data)
}
func (c *Controller) accessGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c.App.PanelConfigPath == "" || r.URL.Path == "/api/v1/health" || r.URL.Path == "/api/v1/runtime/confirm" {
			next.ServeHTTP(w, r)
			return
		}
		cfg, err := runtimeconfig.Load(c.App.PanelConfigPath)
		if err != nil {
			fail(w, 503, "运行配置不可用")
			return
		}
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		} else {
			host = strings.Trim(host, "[]")
		}
		if cfg.AllowedCIDRs != nil && strings.TrimSpace(*cfg.AllowedCIDRs) != "" {
			allowed := false
			ip := net.ParseIP(clientIP(r))
			for _, item := range strings.Fields(*cfg.AllowedCIDRs) {
				if _, cidr, er := net.ParseCIDR(item); er == nil && cidr.Contains(ip) {
					allowed = true
				}
			}
			if !allowed {
				fail(w, 403, "来源 IP 不在允许范围内")
				return
			}
		}
		if cfg.AllowedHost != "" && !strings.EqualFold(host, cfg.AllowedHost) {
			http.NotFound(w, r)
			return
		}
		if cfg.Entry != "" {
			expected := security.Hash(cfg.Entry)
			if r.Method == "GET" && r.URL.Path == "/"+cfg.Entry {
				http.SetCookie(w, &http.Cookie{Name: entranceCookieName(r), Value: expected, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
				r.URL.Path = "/"
			} else {
				cookie, er := r.Cookie(entranceCookieName(r))
				// Preserve pre-upgrade HTTP entrance cookies until the next visit.
				if er != nil && r.TLS == nil {
					cookie, er = r.Cookie("opsview_entry")
				}
				if er != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(expected)) != 1 {
					http.NotFound(w, r)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// HTTP must not overwrite the Secure cookie left by the HTTPS listener.
func entranceCookieName(r *http.Request) string {
	if r.TLS != nil {
		return "opsview_entry"
	}
	return "opsview_entry_http"
}
