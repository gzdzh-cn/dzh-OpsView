package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"opsview/internal/security"
	"strings"
	"testing"
	"time"
)

func TestProtocolSessionCookies(t *testing.T) {
	_, handler := testController(t)
	setupAndLogin(t, handler)
	call := func(scheme, method, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		encoded, _ := json.Marshal(body)
		r := httptest.NewRequest(method, scheme+"://panel.test"+path, bytes.NewReader(encoded))
		for _, cookie := range cookies {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	credentials := map[string]string{"username": "admin", "password": "test-long-password-123"}
	httpsLogin := call("https", "POST", "/api/v1/auth/login", credentials)
	if httpsLogin.Code != 200 {
		t.Fatal("HTTPS login failed", httpsLogin.Code)
	}
	secure := httpsLogin.Result().Cookies()[0]
	if secure.Name != "opsview_session" || !secure.Secure || !secure.HttpOnly {
		t.Fatal("HTTPS cookie compatibility or protection lost")
	}
	httpLogin := call("http", "POST", "/api/v1/auth/login", credentials)
	if httpLogin.Code != 200 {
		t.Fatal("HTTP login failed", httpLogin.Code)
	}
	plain := httpLogin.Result().Cookies()[0]
	if plain.Name != "opsview_session_http" || plain.Secure || !plain.HttpOnly {
		t.Fatal("HTTP login must not overwrite the HTTPS cookie")
	}
	for _, scenario := range []struct {
		scheme string
		cookie *http.Cookie
		want   int
	}{
		{"https", secure, 200}, {"http", plain, 200},
		{"http", secure, 401}, {"https", plain, 401},
	} {
		w := call(scenario.scheme, "GET", "/api/v1/resources", nil, scenario.cookie)
		if w.Code != scenario.want {
			t.Fatalf("%s accepted wrong protocol session: %d", scenario.scheme, w.Code)
		}
	}
	// Both stored cookies can coexist; each listener must use its own session.
	for _, scheme := range []string{"http", "https"} {
		w := call(scheme, "GET", "/api/v1/auth", nil, secure, plain)
		var status struct {
			Authenticated bool `json:"authenticated"`
		}
		if json.Unmarshal(w.Body.Bytes(), &status) != nil || !status.Authenticated {
			t.Fatal("coexisting cookies prevented login", scheme)
		}
	}
	var session struct {
		CSRF string `json:"csrf"`
	}
	if err := json.Unmarshal(httpLogin.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://panel.test/api/v1/auth/logout", bytes.NewBufferString("{}"))
	r.AddCookie(secure)
	r.AddCookie(plain)
	r.Header.Set("X-CSRF-Token", session.CSRF)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 || w.Result().Cookies()[0].Name != plain.Name || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("HTTP logout did not expire its own cookie", w.Code)
	}
	if call("http", "GET", "/api/v1/resources", nil, plain).Code != 401 || call("https", "GET", "/api/v1/resources", nil, secure).Code != 200 {
		t.Fatal("logout invalidated the wrong protocol session")
	}
}

func TestPasswordThenTOTPChallenge(t *testing.T) {
	c, h := testController(t)
	setupAndLogin(t, h)
	a, _ := c.currentAdmin()
	a.TOTP = security.NewTOTP()
	if err := c.App.Store.Set("admin", a); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := c.App.Store.DB.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&before); err != nil {
		t.Fatal(err)
	}
	credentials := map[string]string{"username": "admin", "password": "test-long-password-123"}
	for i := 0; i < 6; i++ {
		w := request(h, "POST", "/api/v1/auth/login", credentials, nil, "", "")
		if w.Code != 428 || !strings.Contains(w.Body.String(), "totp_required") || len(w.Result().Cookies()) != 0 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("missing-code challenge changed authentication state", w.Code)
		}
	}
	var after int
	c.App.Store.DB.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&after)
	if after != before || len(c.auth.attempts) != 0 {
		t.Fatal("challenge created sessions or counted failures")
	}
	credentials["password"] = "incorrect-password"
	if w := request(h, "POST", "/api/v1/auth/login", credentials, nil, "", ""); w.Code != 401 || strings.Contains(w.Body.String(), "totp_required") {
		t.Fatal("challenge exposed before verifying password", w.Code)
	}
	credentials["password"] = "test-long-password-123"
	credentials["code"] = "invalid"
	if w := request(h, "POST", "/api/v1/auth/login", credentials, nil, "", ""); w.Code != 401 {
		t.Fatal("invalid TOTP accepted", w.Code)
	}
	credentials["code"] = security.TOTP(a.TOTP, time.Now())
	if w := request(h, "POST", "/api/v1/auth/login", credentials, nil, "", ""); w.Code != 200 || len(w.Result().Cookies()) != 1 {
		t.Fatal("valid TOTP rejected", w.Code)
	}
}

func TestFixedBrandingCannotBeModified(t *testing.T) {
	c, h := testController(t)
	cookie, csrf := setupAndLogin(t, h)
	for _, key := range []string{"name", "sidebarTitle"} {
		for _, endpoint := range []string{"/api/v1/settings", "/api/v1/settings/groups/panel"} {
			method := "PUT"
			var body any = map[string]any{key: "changed"}
			if strings.Contains(endpoint, "groups") {
				method = "PATCH"
				body = map[string]any{"patch": body, "revision": 0}
			}
			w := request(h, method, endpoint, body, cookie, csrf, "")
			if w.Code != 400 {
				t.Fatal("branding mutation not rejected", endpoint, key, w.Code)
			}
		}
	}
	if c.panelPreferences().String("name") != "OpsView" || c.panelPreferences().String("sidebarTitle") != "OpsView" {
		t.Fatal("branding changed")
	}
}
