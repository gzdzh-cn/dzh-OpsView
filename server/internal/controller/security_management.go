package controller

import (
	"crypto/subtle"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"net"
	"net/http"
	"opsview/internal/model"
	"opsview/internal/preferences"
	"opsview/internal/security"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func (c *Controller) securityGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefs := c.panelPreferences()
		if r.URL.Path == "/api/v1/health" {
			if prefs.Bool("webDisabled") && !net.ParseIP(clientIP(r)).IsLoopback() {
				fail(w, 503, "面板网页入口已关闭")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if prefs.Bool("webDisabled") {
			fail(w, 503, "面板网页入口已关闭，请通过 ov panel on 开启")
			return
		}
		if r.URL.Path == "/api/v1/auth/login" || strings.HasPrefix(r.URL.Path, "/api/v1/auth/passkeys/login-") || r.URL.Path == "/api/v1/auth/temporary" {
			if err := c.checkRegion(r, prefs); err != nil {
				fail(w, 403, err.Error())
				return
			}
		}
		if prefs.Bool("basicEnabled") {
			user, pass, ok := r.BasicAuth()
			if !ok || subtle.ConstantTimeCompare([]byte(user), []byte(prefs.String("basicUsername"))) != 1 || bcrypt.CompareHashAndPassword([]byte(prefs.String("basicPasswordHash")), []byte(pass)) != nil {
				w.Header().Set("WWW-Authenticate", `Basic realm="OpsView", charset="UTF-8"`)
				fail(w, 401, "需要 HTTP 基础认证")
				return
			}
		}
		if prefs.Bool("clientCertificateEnabled") {
			roots := x509.NewCertPool()
			roots.AppendCertsFromPEM([]byte(prefs.String("clientCA")))
			if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
				fail(w, 403, "需要客户端证书")
				return
			}
			inter := x509.NewCertPool()
			for _, cert := range r.TLS.PeerCertificates[1:] {
				inter.AddCert(cert)
			}
			if _, err := r.TLS.PeerCertificates[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
				fail(w, 403, "客户端证书无效或已过期")
				return
			}
		}
		if pattern := prefs.String("userAgentAllow"); pattern != "" {
			re, err := regexp.Compile(pattern)
			if err != nil || !re.MatchString(r.UserAgent()) {
				fail(w, 403, "客户端类型不被允许")
				return
			}
		}
		if !strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/assets/") && prefs.Int("unauthenticatedStatus") != 0 {
			_, csrf, _ := c.checkSession(r)
			temporary, _, _ := c.temporarySession(r)
			if csrf == "" && !temporary {
				http.Error(w, http.StatusText(prefs.Int("unauthenticatedStatus")), prefs.Int("unauthenticatedStatus"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (c *Controller) checkSession(r *http.Request) (string, string, error) {
	cookie, err := r.Cookie(sessionCookieName(r))
	if err != nil {
		return "", "", err
	}
	hash := security.Hash(cookie.Value)
	var csrf string
	now := time.Now().Unix()
	err = c.App.Store.DB.QueryRow(`SELECT csrf FROM sessions WHERE hash=? AND expires>?`, hash, now).Scan(&csrf)
	if err != nil {
		return "", "", err
	}
	var last int64
	_ = c.App.Store.DB.QueryRow(`SELECT last_seen FROM session_activity WHERE hash=?`, hash).Scan(&last)
	idle := c.panelPreferences().Int("idleMinutes")
	if last > 0 && idle > 0 && now-last > int64(idle*60) {
		_, _ = c.App.Store.DB.Exec(`DELETE FROM sessions WHERE hash=?`, hash)
		return "", "", fmt.Errorf("无操作超时，请重新登录")
	}
	activity, _ := strconv.ParseInt(r.Header.Get("X-OpsView-Activity"), 10, 64)
	if last == 0 || activity > last && activity <= now+5 && activity >= now-60 {
		_, _ = c.App.Store.DB.Exec(`INSERT INTO session_activity(hash,last_seen,ip,agent) VALUES(?,?,?,?) ON CONFLICT(hash) DO UPDATE SET last_seen=excluded.last_seen`, hash, now, clientIP(r), truncateAgent(r.UserAgent()))
	}
	return hash, csrf, nil
}
func truncateAgent(s string) string {
	if len(s) > 512 {
		return s[:512]
	}
	return s
}
func (c *Controller) sessionManagement(w http.ResponseWriter, r *http.Request) {
	hash, _, _ := c.checkSession(r)
	if r.Method == "GET" {
		rows, err := c.App.Store.DB.Query(`SELECT sessions.hash,sessions.expires,COALESCE(session_activity.last_seen,0),COALESCE(session_activity.ip,''),COALESCE(session_activity.agent,'') FROM sessions LEFT JOIN session_activity ON sessions.hash=session_activity.hash WHERE expires>?`, time.Now().Unix())
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		defer rows.Close()
		out := []model.Config{}
		for rows.Next() {
			var id, ip, agent string
			var expires, last int64
			if rows.Scan(&id, &expires, &last, &ip, &agent) == nil {
				out = append(out, model.Config{"id": security.Hash(id), "current": id == hash, "ip": ip, "agent": agent, "expires": expires, "lastSeen": last})
			}
		}
		send(w, 200, out)
		return
	}
	var b struct {
		ID       string `json:"id"`
		All      bool   `json:"all"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decode(w, r, &b) {
		return
	}
	if !c.verifyIdentity(r, b.Password, b.Code) {
		fail(w, 403, "身份验证失败")
		return
	}
	rows, err := c.App.Store.DB.Query(`SELECT hash FROM sessions`)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		if b.All || security.Hash(id) == b.ID {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		_, _ = c.App.Store.DB.Exec(`DELETE FROM sessions WHERE hash=?`, id)
	}
	c.App.Store.Audit("sessions.revoke", "", clientIP(r))
	send(w, 200, model.Config{"ok": true})
}
func (c *Controller) tokenManagement(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	if kind != "api" && kind != "temporary" {
		fail(w, 404, "授权类型不存在")
		return
	}
	if r.Method == "GET" {
		rows, err := c.App.Store.Tokens(kind)
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		send(w, 200, rows)
		return
	}
	var b struct {
		Name     string   `json:"name"`
		Scopes   []string `json:"scopes"`
		Sources  string   `json:"sources"`
		Days     int      `json:"days"`
		ID       string   `json:"id"`
		Password string   `json:"password"`
		Code     string   `json:"code"`
	}
	if !decode(w, r, &b) {
		return
	}
	if !c.verifyIdentity(r, b.Password, b.Code) {
		fail(w, 403, "身份验证失败")
		return
	}
	if b.ID != "" {
		_, err := c.App.Store.DB.Exec(`DELETE FROM security_tokens WHERE id=? AND kind IN (?,?)`, b.ID, kind, kind+"-session")
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		c.App.Store.Audit("token.revoke", b.ID, clientIP(r))
		send(w, 200, model.Config{"ok": true})
		return
	}
	if len(b.Name) == 0 || len(b.Name) > 100 {
		fail(w, 400, "授权名称须为 1–100 字节")
		return
	}
	for _, source := range strings.Fields(b.Sources) {
		if net.ParseIP(source) == nil {
			if _, _, err := net.ParseCIDR(source); err != nil {
				fail(w, 400, "来源必须是 IP/CIDR")
				return
			}
		}
	}
	allowed := map[string]bool{"overview:read": true, "resources:read": true, "tasks:read": true, "sites:write": true, "backups:write": true}
	for _, scope := range b.Scopes {
		if !allowed[scope] || kind == "temporary" && strings.HasSuffix(scope, ":write") {
			fail(w, 400, "授权范围无效")
			return
		}
	}
	if len(b.Scopes) == 0 {
		b.Scopes = []string{"overview:read", "resources:read"}
	}
	expires := time.Now().Add(10 * time.Minute)
	if kind == "api" {
		if b.Days < 1 || b.Days > 365 {
			fail(w, 400, "API 有效期须为 1–365 天")
			return
		}
		expires = time.Now().Add(time.Duration(b.Days) * 24 * time.Hour)
	}
	id, token := security.ID(), security.ID()+security.ID()
	err := c.App.Store.CreateToken(id, kind, security.Hash(token), model.Config{"name": b.Name, "scopes": b.Scopes, "sources": b.Sources}, expires.Unix())
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	c.App.Store.Audit("token.create", id, clientIP(r))
	send(w, 201, model.Config{"id": id, "token": token, "expires": expires.Unix()})
}
func (c *Controller) exchangeTemporary(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &b) {
		return
	}
	id, kind, cfg, expires, err := c.App.Store.Token(security.Hash(b.Token))
	if err != nil || kind != "temporary" || expires <= time.Now().Unix() || !preferences.AllowedSource(clientIP(r), cfg.String("sources")) {
		fail(w, 403, "临时授权无效或已兑换")
		return
	}
	token := security.ID() + security.ID()
	res, err := c.App.Store.DB.Exec(`UPDATE security_tokens SET kind='temporary-session',hash=?,last_used=? WHERE id=? AND kind='temporary'`, security.Hash(token), time.Now().Unix(), id)
	if err != nil {
		fail(w, 500, "兑换失败")
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		fail(w, 409, "临时授权已兑换")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: temporaryCookieName(r), Value: token, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: int(expires - time.Now().Unix())})
	send(w, 200, model.Config{"ok": true, "expires": expires})
}

// Returns true when a token was presented (including rejected tokens).
func (c *Controller) tokenRequest(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) bool {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == r.Header.Get("Authorization") {
		token = ""
	}
	if key := r.Header.Get("X-OpsView-API-Key"); key != "" {
		token = key
	}
	if token == "" {
		if _, csrf, _ := c.checkSession(r); csrf != "" {
			return false
		}
		if cookie, err := r.Cookie(temporaryCookieName(r)); err == nil {
			token = cookie.Value
		}
	}
	if token == "" {
		return false
	}
	id, kind, cfg, expires, err := c.App.Store.Token(security.Hash(token))
	if err != nil || expires <= time.Now().Unix() || !preferences.AllowedSource(clientIP(r), cfg.String("sources")) {
		fail(w, 401, "授权无效或已过期")
		return true
	}
	if kind != "temporary-session" && (kind != "api" || !c.panelPreferences().Bool("apiEnabled") || r.TLS == nil) {
		fail(w, 403, "API 未启用或未使用 HTTPS")
		return true
	}
	if kind == "temporary-session" && r.Method == "POST" && r.URL.Path == "/api/v1/auth/logout" {
		_, _ = c.App.Store.DB.Exec(`DELETE FROM security_tokens WHERE id=?`, id)
		http.SetCookie(w, &http.Cookie{Name: temporaryCookieName(r), Path: "/", MaxAge: -1, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
		send(w, 200, model.Config{"ok": true})
		return true
	}
	scope := ""
	path := r.URL.Path
	if r.Method == "GET" {
		switch {
		case path == "/api/v1/overview":
			scope = "overview:read"
		case path == "/api/v1/resources":
			scope = "resources:read"
		case path == "/api/v1/tasks":
			scope = "tasks:read"
		}
	}
	if r.Method == "POST" && strings.Contains(path, "/resources/") && strings.Contains(path, "/actions/") {
		row, e := c.App.Store.Resource(r.PathValue("id"))
		a := r.PathValue("action")
		if e == nil && row.Kind == "site" && map[string]bool{"resume": true, "stop": true, "renew": true}[a] {
			scope = "sites:write"
		}
		if e == nil && row.Kind == "backup" && a == "backup" {
			scope = "backups:write"
		}
	}
	permitted := false
	raw, _ := json.Marshal(cfg["scopes"])
	var scopes []string
	_ = json.Unmarshal(raw, &scopes)
	for _, s := range scopes {
		if s == scope && s != "" {
			permitted = true
		}
	}
	if !permitted {
		fail(w, 403, "授权不允许此操作")
		return true
	}
	_, _ = c.App.Store.DB.Exec(`UPDATE security_tokens SET last_used=? WHERE id=?`, time.Now().Unix(), id)
	c.App.Store.Audit("api.call:"+r.Method+":"+path, id, clientIP(r))
	next(w, r)
	return true
}
func (c *Controller) clientCertificate(w http.ResponseWriter, r *http.Request) {
	c.accessMu.Lock()
	defer c.accessMu.Unlock()
	var b struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decode(w, r, &b) {
		return
	}
	if !c.verifyIdentity(r, b.Password, b.Code) {
		fail(w, 403, "身份验证失败")
		return
	}
	data, err := c.App.Executor.Execute(r.Context(), model.Operation{Name: "system.panel-client-certificate", Params: model.Config{}}, func(string) {})
	if err != nil {
		fail(w, 502, err.Error())
		return
	}
	var old model.Config
	rev, _ := c.App.Store.SettingRevision("panel", &old)
	_, _, err = c.App.Store.PatchSetting("panel", rev, model.Config{"clientCA": data["ca"]}, "security.client-certificate", clientIP(r))
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	send(w, 200, data)
}

func (c *Controller) temporarySession(r *http.Request) (bool, model.Config, int64) {
	cookie, err := r.Cookie(temporaryCookieName(r))
	if err != nil {
		return false, nil, 0
	}
	_, kind, cfg, expires, err := c.App.Store.Token(security.Hash(cookie.Value))
	return err == nil && kind == "temporary-session" && expires > time.Now().Unix() && preferences.AllowedSource(clientIP(r), cfg.String("sources")), cfg, expires
}

func temporaryCookieName(r *http.Request) string {
	if r.TLS == nil {
		return "opsview_temporary_http"
	}
	return "opsview_temporary"
}
