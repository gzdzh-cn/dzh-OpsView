package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
