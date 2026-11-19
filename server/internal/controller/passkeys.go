package controller

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"net"
	"net/http"
	"opsview/internal/model"
	"opsview/internal/runtimeconfig"
	"opsview/internal/security"
	"strings"
	"sync"
	"time"
)

var passkeyMutation sync.Mutex

type passkeyUser struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Credentials []webauthn.Credential `json:"credentials"`
}

func (u passkeyUser) WebAuthnID() []byte                         { return []byte(u.ID) }
func (u passkeyUser) WebAuthnName() string                       { return u.Name }
func (u passkeyUser) WebAuthnDisplayName() string                { return u.Name }
func (u passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.Credentials }
func (c *Controller) webAuthn(r *http.Request) (*webauthn.WebAuthn, passkeyUser, error) {
	a, err := c.currentAdmin()
	if err != nil {
		return nil, passkeyUser{}, err
	}
	host := r.Host
	if h, _, e := net.SplitHostPort(host); e == nil {
		host = h
	}
	if r.TLS == nil && !(c.App.Development && host == "localhost") {
		return nil, passkeyUser{}, errors.New("Passkey 需要有效 HTTPS 域名")
	}
	if net.ParseIP(host) != nil {
		return nil, passkeyUser{}, errors.New("Passkey 请使用已绑定的 HTTPS 域名访问")
	}
	// Only an explicitly configured public/allowed domain may define the RP in production.
	if !c.App.Development {
		cfg, err := runtimeconfig.Load(c.App.PanelConfigPath)
		if err != nil || !(strings.EqualFold(host, cfg.AllowedHost) || strings.EqualFold(host, cfg.PublicHost)) {
			return nil, passkeyUser{}, errors.New("请先保存面板公网域名，再通过该域名绑定 Passkey")
		}
	}
	u := passkeyUser{}
	_ = c.App.Store.Setting("passkeys", &u)
	if u.ID == "" {
		u.ID = security.ID() + security.ID()
	}
	u.Name = a.Username
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	wa, err := webauthn.New(&webauthn.Config{RPID: host, RPDisplayName: "OpsView", RPOrigins: []string{scheme + "://" + r.Host}, AuthenticatorSelection: protocol.AuthenticatorSelection{UserVerification: protocol.VerificationRequired}})
	return wa, u, err
}
func (c *Controller) passkeys(w http.ResponseWriter, r *http.Request) {
	step := r.PathValue("step")
	expectedMethod := http.MethodPost
	if step == "list" {
		expectedMethod = http.MethodGet
	}
	if r.Method != expectedMethod {
		fail(w, 405, "请求方法不支持")
		return
	}
	passkeyMutation.Lock()
	defer passkeyMutation.Unlock()
	if step == "list" {
		u := passkeyUser{}
		_ = c.App.Store.Setting("passkeys", &u)
		out := []model.Config{}
		for _, cred := range u.Credentials {
			out = append(out, model.Config{"id": base64.RawURLEncoding.EncodeToString(cred.ID), "backupEligible": cred.Flags.BackupEligible, "backedUp": cred.Flags.BackupState})
		}
		_, _, err := c.webAuthn(r)
		reason := ""
		if err != nil {
			reason = err.Error()
		}
		send(w, 200, model.Config{"rows": out, "reason": reason})
		return
	}
	if step == "delete" {
		var b struct {
			ID       string `json:"id"`
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
		u := passkeyUser{}
		_ = c.App.Store.Setting("passkeys", &u)
		out := []webauthn.Credential{}
		for _, cred := range u.Credentials {
			if base64.RawURLEncoding.EncodeToString(cred.ID) != b.ID {
				out = append(out, cred)
			}
		}
		u.Credentials = out
		if err := c.App.Store.Set("passkeys", u); err != nil {
			fail(w, 500, err.Error())
			return
		}
		c.App.Store.Audit("passkey.delete", "", clientIP(r))
		send(w, 200, model.Config{"ok": true})
		return
	}
	wa, u, err := c.webAuthn(r)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	register := strings.HasPrefix(step, "register")
	if strings.HasSuffix(step, "begin") {
		if register {
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
		} else {
			if len(u.Credentials) == 0 {
				fail(w, 400, "尚未绑定可用 Passkey")
				return
			}
			if c.loginLimited(r) {
				fail(w, 429, "登录尝试过多，请稍后再试")
				return
			}
		}
		var options any
		var session *webauthn.SessionData
		if register {
			options, session, err = wa.BeginRegistration(u, webauthn.WithExclusions(webauthn.Credentials(u.Credentials).CredentialDescriptors()))
		} else {
			options, session, err = wa.BeginLogin(u)
		}
		if err != nil {
			fail(w, 400, "无法创建 Passkey 挑战")
			return
		}
		hash, _, _ := c.checkSession(r)
		raw, _ := json.Marshal(session)
		a, _ := c.currentAdmin()
		token := security.ID() + security.ID()
		kind := "passkey-login"
		if register {
			kind = "passkey-register"
			if err = c.App.Store.Set("passkeys", u); err != nil {
				fail(w, 500, err.Error())
				return
			}
		}
		err = c.App.Store.CreateToken(security.ID(), kind, security.Hash(token), model.Config{"session": json.RawMessage(raw), "owner": hash, "adminRevision": security.Hash(a.PasswordHash)}, time.Now().Add(3*time.Minute).Unix())
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "opsview_webauthn", Value: token, Path: "/api/v1/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: 180})
		send(w, 200, options)
		return
	}
	if !strings.HasSuffix(step, "finish") {
		fail(w, 404, "Passkey 操作不存在")
		return
	}
	cookie, err := r.Cookie("opsview_webauthn")
	if err != nil {
		fail(w, 400, "Passkey 挑战已失效")
		return
	}
	id, kind, state, expires, err := c.App.Store.Token(security.Hash(cookie.Value))
	expected := "passkey-login"
	if register {
		expected = "passkey-register"
	}
	a, _ := c.currentAdmin()
	hash, _, _ := c.checkSession(r)
	if err != nil || kind != expected || expires <= time.Now().Unix() || state.String("adminRevision") != security.Hash(a.PasswordHash) || (register && state.String("owner") != hash) {
		fail(w, 400, "Passkey 挑战无效")
		return
	}
	res, err := c.App.Store.DB.Exec(`DELETE FROM security_tokens WHERE id=?`, id)
	if err != nil {
		fail(w, 500, "无法消费挑战")
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		fail(w, 409, "挑战已使用")
		return
	}
	var session webauthn.SessionData
	raw, _ := json.Marshal(state["session"])
	if json.Unmarshal(raw, &session) != nil {
		fail(w, 400, "挑战状态损坏")
		return
	}
	var cred *webauthn.Credential
	if register {
		cred, err = wa.FinishRegistration(u, session, r)
	} else {
		cred, err = wa.FinishLogin(u, session, r)
	}
	if err != nil {
		if !register {
			c.recordLoginFailure(r)
		}
		fail(w, 403, "Passkey 验证失败，请重新发起")
		return
	}
	if register {
		u.Credentials = append(u.Credentials, *cred)
	} else {
		if a.TOTP != "" && !security.VerifyTOTP(a.TOTP, r.Header.Get("X-OpsView-TOTP")) {
			c.recordLoginFailure(r)
			fail(w, 403, "请输入有效双因素验证码")
			return
		}
		for i := range u.Credentials {
			if string(u.Credentials[i].ID) == string(cred.ID) {
				u.Credentials[i] = *cred
			}
		}
	}
	if err = c.App.Store.Set("passkeys", u); err != nil {
		fail(w, 500, err.Error())
		return
	}
	if register {
		c.App.Store.Audit("passkey.register", "", clientIP(r))
		send(w, 200, model.Config{"ok": true})
		return
	}
	c.openAdminSession(w, r, a)
}
func (c *Controller) loginLimited(r *http.Request) bool {
	c.auth.Lock()
	defer c.auth.Unlock()
	a := c.auth.attempts[clientIP(r)]
	return a.Count >= 5 && time.Now().Before(a.Until)
}
func (c *Controller) recordLoginFailure(r *http.Request) {
	c.auth.Lock()
	defer c.auth.Unlock()
	ip := clientIP(r)
	for k, v := range c.auth.attempts {
		if time.Now().After(v.Until) {
			delete(c.auth.attempts, k)
		}
	}
	if len(c.auth.attempts) > 4096 {
		return
	}
	a := c.auth.attempts[ip]
	a.Count++
	a.Until = time.Now().Add(15 * time.Minute)
	c.auth.attempts[ip] = a
	c.App.Store.Audit("auth.failed", "", ip)
}
func (c *Controller) openAdminSession(w http.ResponseWriter, r *http.Request, a admin) {
	token := security.ID() + security.ID()
	csrf := security.ID()
	minutes := c.panelPreferences().Int("sessionMinutes")
	expires := time.Now().Add(time.Duration(minutes) * time.Minute)
	if err := c.App.Store.AddSession(a, security.Hash(token), csrf, expires.Unix()); err != nil {
		fail(w, 500, "创建会话失败")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName(r), Value: token, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: minutes * 60, Expires: expires})
	_, _ = c.App.Store.DB.Exec(`INSERT OR REPLACE INTO session_activity(hash,last_seen,ip,agent) VALUES(?,?,?,?)`, security.Hash(token), time.Now().Unix(), clientIP(r), truncateAgent(r.UserAgent()))
	c.App.Store.Audit("auth.login", "", clientIP(r))
	send(w, 200, model.Config{"csrf": csrf, "username": a.Username})
}
