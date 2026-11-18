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

func TestFirewallAdminAPISessionCSRFAndRevision(t *testing.T) {
	_, h := testController(t)
	for _, action := range []string{"save", "delete", "export", "import-preview", "sync-preview", "protection-save", "rollback"} {
		if w := request(h, "POST", "/api/v1/system/firewall/"+action, map[string]any{}, nil, "", ""); w.Code != 401 {
			t.Fatal(action, w.Code)
		}
	}
	cookie, csrf := setupAndLogin(t, h)
	if w := request(h, "POST", "/api/v1/system/firewall/save", map[string]any{}, cookie, "", ""); w.Code != 403 {
		t.Fatal("CSRF bypass", w.Code)
	}
	if w := request(h, "POST", "/api/v1/system/firewall/shell", map[string]any{}, cookie, csrf, ""); w.Code != 400 {
		t.Fatal("action not allowlisted", w.Code)
	}
	if w := request(h, "POST", "/api/v1/system/firewall/save", map[string]any{"revision": "stale"}, cookie, csrf, ""); w.Code != 409 {
		t.Fatal("stale mutation queued", w.Code)
	}
	if w := request(h, "POST", "/api/v1/system/firewall/export", map[string]any{}, cookie, csrf, ""); w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
}
