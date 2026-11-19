package controller

import (
	"net/http"
	"net/http/httptest"
	"opsview/internal/runtimeconfig"
	"path/filepath"
	"testing"
)

func TestProtocolEntranceCookiesAndRotation(t *testing.T) {
	c, _ := testController(t)
	cfg := runtimeconfig.Default()
	cfg.Entry = "first_secure_entrance"
	path := filepath.Join(t.TempDir(), "panel.json")
	if err := runtimeconfig.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	c.App.PanelConfigPath = path
	handler := c.accessGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	call := func(scheme, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", scheme+"://panel.test"+path, nil)
		for _, cookie := range cookies {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	secure := call("https", "/"+cfg.Entry).Result().Cookies()[0]
	plain := call("http", "/"+cfg.Entry).Result().Cookies()[0]
	if secure.Name != "opsview_entry" || !secure.Secure || !secure.HttpOnly || plain.Name != "opsview_entry_http" || plain.Secure || !plain.HttpOnly {
		t.Fatal("entrance protocol cookies are not isolated")
	}
	if call("https", "/", plain).Code != 404 || call("http", "/").Code != 404 {
		t.Fatal("entrance gate bypassed")
	}
	for _, scheme := range []string{"https", "http"} {
		if call(scheme, "/", secure, plain).Code != 204 {
			t.Fatal("coexisting cookies blocked entrance", scheme)
		}
	}
	legacy := *plain
	legacy.Name = "opsview_entry"
	if call("http", "/", &legacy).Code != 204 {
		t.Fatal("legacy HTTP entrance cookie lost on upgrade")
	}
	cfg.Entry = "rotated_secure_entrance"
	if err := runtimeconfig.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	for _, scheme := range []string{"https", "http"} {
		if call(scheme, "/", secure, plain).Code != 404 {
			t.Fatal("old entrance remained valid", scheme)
		}
	}
}
