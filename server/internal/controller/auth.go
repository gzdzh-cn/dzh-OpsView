package controller

import (
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"net"
	"net/http"
	"opsview/internal/account"
	"opsview/internal/model"
	"opsview/internal/runtimeconfig"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type admin = account.Admin
type attempt struct {
	Count int
	Until time.Time
}
type authState struct {
	sync.Mutex
	attempts    map[string]attempt
	credentials map[string]time.Time
}

func (c *Controller) currentAdmin() (admin, error) {
	var a admin
	e := c.App.Store.Setting("admin", &a)
	return a, e
}
func clientIP(r *http.Request) string {
	ip, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil {
		return r.RemoteAddr
	}
	return ip
}

// Browsers refuse an HTTP response that overwrites an existing Secure cookie.
// Keep HTTPS's existing name for compatibility and use a separate HTTP cookie
// so disabling SSL works without deleting cookies in every browser.
func sessionCookieName(r *http.Request) string {
	if r.TLS != nil {
		return "opsview_session"
	}
	return "opsview_session_http"
}

func (c *Controller) authStatus(w http.ResponseWriter, r *http.Request) {
	a, e := c.currentAdmin()
	_, csrf, _ := c.checkSession(r)
	temporary, scopes, _ := c.temporarySession(r)
	temporary = temporary && csrf == ""
	send(w, 200, map[string]any{"setupRequired": errors.Is(e, sql.ErrNoRows), "authenticated": csrf != "" || temporary, "username": func() string {
		if temporary && csrf == "" {
			return "临时只读访问"
		}
		return a.Username
	}(), "twoFactorEnabled": a.TOTP != "", "csrf": csrf, "development": c.App.Development, "temporary": temporary, "scopes": scopes["scopes"], "passwordExpired": c.passwordExpired(a)})
}
func (c *Controller) setup(w http.ResponseWriter, r *http.Request) {
	c.auth.Lock()
	defer c.auth.Unlock()
	if _, e := c.currentAdmin(); !errors.Is(e, sql.ErrNoRows) {
		fail(w, 409, "管理员已初始化")
		return
	}
	var body struct {
		Token    string `json:"token"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	token, e := os.ReadFile(filepath.Join(c.DataDir, "setup.token"))
	if e != nil || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(string(token))), []byte(body.Token)) != 1 {
		fail(w, 403, "初始化令牌无效，请读取服务器本机 setup.token")
		return
	}
	if !validPassword(body.Password) || !regexpUsername(body.Username) {
		fail(w, 400, "用户名格式无效，或密码不足 12 位（最多 72 字节）")
		return
	}
	if e = account.Initialize(c.App.Store, body.Username, body.Password, clientIP(r)); e != nil {
		fail(w, 409, "初始化失败或管理员已创建")
		return
	}
	_ = os.Remove(filepath.Join(c.DataDir, "setup.token"))
	send(w, 201, map[string]any{"ok": true})
}
func regexpUsername(s string) bool { return account.ValidUsername(s) }
func validPassword(s string) bool  { return account.ValidPassword(s) }
func (c *Controller) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decode(w, r, &body) {
		return
	}
	ip := clientIP(r)
	c.auth.Lock()
	defer c.auth.Unlock()
	now := time.Now()
	for k, v := range c.auth.attempts {
		if now.After(v.Until) {
			delete(c.auth.attempts, k)
		}
	}
	at := c.auth.attempts[ip]
	if at.Count >= 5 && now.Before(at.Until) {
		w.Header().Set("Retry-After", "900")
		fail(w, 429, "登录尝试过多，请 15 分钟后重试")
		return
	}
	a, e := c.currentAdmin()
	valid := e == nil && a.Username == body.Username && bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(body.Password)) == nil
	if valid && a.TOTP != "" {
		if body.Code == "" {
			w.Header().Set("Cache-Control", "no-store")
			send(w, http.StatusPreconditionRequired, model.Config{"code": "totp_required", "error": "请完成验证器验证"})
			return
		}
		valid = security.VerifyTOTP(a.TOTP, body.Code)
	}
	if !valid {
		if len(c.auth.attempts) > 4096 {
			fail(w, 429, "登录限流中")
			return
		}
		at.Count++
		at.Until = now.Add(15 * time.Minute)
		c.auth.attempts[ip] = at
		c.App.Store.Audit("auth.failed", "", ip)
		fail(w, 401, "账号、密码或验证码错误")
		return
	}
	delete(c.auth.attempts, ip)
	token := security.ID() + security.ID()
	csrf := security.ID()
	minutes := 480
	var preferences model.Config
	if c.App.Store.Setting("panel", &preferences) == nil && preferences.Int("sessionMinutes") >= 15 && preferences.Int("sessionMinutes") <= 10080 {
		minutes = preferences.Int("sessionMinutes")
	}
	expires := now.Add(time.Duration(minutes) * time.Minute)
	e = c.App.Store.AddSession(a, security.Hash(token), csrf, expires.Unix())
	if e != nil {
		fail(w, 500, "创建会话失败")
		return
	}
	_, _ = c.App.Store.DB.Exec(`DELETE FROM sessions WHERE expires<?`, now.Unix())
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName(r), Value: token, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: minutes * 60, Expires: expires})
	_, _ = c.App.Store.DB.Exec(`INSERT OR REPLACE INTO session_activity(hash,last_seen,ip,agent) VALUES(?,?,?,?)`, security.Hash(token), now.Unix(), ip, truncateAgent(r.UserAgent()))
	c.App.Store.Audit("auth.login", "", ip)
	send(w, 200, map[string]any{"csrf": csrf, "username": a.Username})
}
func (c *Controller) authenticated(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c.tokenRequest(w, r, next) {
			return
		}
		_, csrf, e := c.checkSession(r)
		if e != nil {
			fail(w, 401, "登录已失效或无操作超时")
			return
		}

		a, _ := c.currentAdmin()
		if c.passwordExpired(a) && r.URL.Path != "/api/v1/auth/password" && r.URL.Path != "/api/v1/auth/logout" {
			fail(w, 403, "管理员密码已到期，请先修改密码")
			return
		}
		var settings map[string]any
		legacyCIDRs := true
		if c.App.PanelConfigPath != "" {
			if cfg, err := runtimeconfig.Load(c.App.PanelConfigPath); err == nil && cfg.AllowedCIDRs != nil {
				legacyCIDRs = false
			}
		}
		if c.App.Store.Setting("panel", &settings) == nil && legacyCIDRs {
			if list, ok := settings["allowedCIDRs"].(string); ok && strings.TrimSpace(list) != "" {
				allowed := false
				ip := net.ParseIP(clientIP(r))
				for _, v := range strings.Fields(list) {
					if _, cidr, e := net.ParseCIDR(v); e == nil && cidr.Contains(ip) {
						allowed = true
					}
				}
				if !allowed {
					fail(w, 403, "来源 IP 不在允许范围内")
					return
				}
			}
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(csrf)) != 1 {
				fail(w, 403, "CSRF 校验失败，请刷新页面")
				return
			}
		}
		next(w, r)
	}
}
func (c *Controller) logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName(r))
	if err != nil {
		fail(w, 401, "请先登录")
		return
	}
	_, e := c.App.Store.DB.Exec(`DELETE FROM sessions WHERE hash=?`, security.Hash(cookie.Value))
	if e != nil {
		fail(w, 500, "注销失败")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName(r), Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil})
	send(w, 200, map[string]any{"ok": true})
}
func (c *Controller) twoFactor(w http.ResponseWriter, r *http.Request) {
	c.auth.Lock()
	defer c.auth.Unlock()
	var b struct {
		Action   string `json:"action"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decode(w, r, &b) {
		return
	}
	a, e := c.currentAdmin()
	if e != nil || bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(b.Password)) != nil {
		fail(w, 403, "当前密码错误")
		return
	}
	aOriginal := a
	previous := a
	switch b.Action {
	case "prepare":
		if a.TOTP != "" {
			fail(w, 409, "双因素认证已开启")
			return
		}
		a.PendingTOTP = security.NewTOTP()
		if e = c.App.Store.PrepareSetting("admin", &previous, "admin.2fa."+b.Action, clientIP(r), func() error {
			if previous != aOriginal {
				return fmt.Errorf("账号已变更，请重新验证")
			}
			previous = a
			return nil
		}); e != nil {
			fail(w, 500, "保存失败")
			return
		}
		send(w, 200, map[string]any{"secret": a.PendingTOTP, "uri": fmt.Sprintf("otpauth://totp/OpsView:%s?secret=%s&issuer=OpsView", a.Username, a.PendingTOTP)})
		return
	case "confirm":
		if a.PendingTOTP == "" || !security.VerifyTOTP(a.PendingTOTP, b.Code) {
			fail(w, 400, "验证码错误")
			return
		}
		a.TOTP = a.PendingTOTP
		a.PendingTOTP = ""
	case "disable":
		if a.TOTP == "" || !security.VerifyTOTP(a.TOTP, b.Code) {
			fail(w, 400, "验证码错误")
			return
		}
		a.TOTP = ""
		a.PendingTOTP = ""
	default:
		fail(w, 400, "无效操作")
		return
	}
	if e = c.App.Store.MutateSetting("admin", &previous, false, "admin.2fa."+b.Action, clientIP(r), func() error {
		if previous != aOriginal {
			return fmt.Errorf("账号已变更，请重新验证")
		}
		previous = a
		return nil
	}); e != nil {
		fail(w, 500, "保存失败")
		return
	}
	send(w, 200, map[string]any{"ok": true, "reauthenticate": true})
}
func (c *Controller) password(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Current  string `json:"current"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decode(w, r, &b) {
		return
	}
	a, e := c.currentAdmin()
	if e != nil || bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(b.Current)) != nil || (a.TOTP != "" && !security.VerifyTOTP(a.TOTP, b.Code)) {
		fail(w, 403, "身份验证失败")
		return
	}
	if err := c.passwordPolicy(b.Password); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if !validPassword(b.Password) {
		fail(w, 400, "密码须为 12–72 字节")
		return
	}
	if e = account.Change(c.App.Store, "", b.Password, false, &a, clientIP(r)); e != nil {
		fail(w, 409, "修改失败，请重新验证")
		return
	}
	send(w, 200, map[string]any{"ok": true})
}

func (c *Controller) username(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decode(w, r, &b) {
		return
	}
	a, err := c.currentAdmin()
	if err != nil || bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(b.Password)) != nil || (a.TOTP != "" && !security.VerifyTOTP(a.TOTP, b.Code)) {
		fail(w, 403, "密码或双因素验证码错误")
		return
	}
	if err = account.Change(c.App.Store, b.Username, "", false, &a, clientIP(r)); err != nil {
		fail(w, 400, err.Error())
		return
	}
	send(w, 200, map[string]bool{"ok": true})
}
