package controller

import (
	"net/http"
	"testing"
)

func TestSystemSecurityRequiresSessionCSRFAndAllowlistedAction(t *testing.T) {
	_, h := testController(t)
	for _, path := range []string{"/api/v1/system/ssh", "/api/v1/system/ssh/logs", "/api/v1/system/firewall"} {
		if w := request(h, "GET", path, nil, nil, "", ""); w.Code != http.StatusUnauthorized {
			t.Fatal(path, w.Code)
		}
	}
	cookie, csrf := setupAndLogin(t, h)
	for _, path := range []string{"/api/v1/system/ssh", "/api/v1/system/ssh/logs", "/api/v1/system/firewall"} {
		w := request(h, "GET", path, nil, cookie, "", "")
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	if w := request(h, "POST", "/api/v1/system/security/ssh-apply", map[string]any{}, cookie, "", ""); w.Code != 403 {
		t.Fatal("CSRF bypass", w.Code)
	}
	if w := request(h, "POST", "/api/v1/system/security/shell", map[string]any{}, cookie, csrf, ""); w.Code != 400 {
		t.Fatal("arbitrary action accepted", w.Code)
	}
	if w := request(h, "POST", "/api/v1/system/security/ssh-apply", map[string]any{"confirmedImpact": true}, cookie, csrf, ""); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request(h, "POST", "/api/v1/system/security/firewall-mode", map[string]any{"confirmedImpact": true}, cookie, csrf, ""); w.Code != 409 {
		t.Fatal("concurrent host-security task accepted", w.Code)
	}
}
