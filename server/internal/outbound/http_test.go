package outbound

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMetadataAndPrivateTargets(t *testing.T) {
	for _, ip := range []string{"169.254.169.254", "100.100.100.200", "168.63.129.16", "fe80::1", "0.0.0.0", "::", "224.0.0.1"} {
		for _, private := range []bool{false, true} {
			if Allowed(net.ParseIP(ip), private) {
				t.Fatal("metadata accepted", ip)
			}
		}
	}
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "100.64.1.1", "::1", "fd00::1"} {
		if Allowed(net.ParseIP(ip), false) || !Allowed(net.ParseIP(ip), true) {
			t.Fatal("private policy", ip)
		}
	}
	for _, raw := range []string{"file:///etc/passwd", "http://user:pass@example.com", "ftp://example.com", "https:///path"} {
		if Validate(raw) == nil {
			t.Fatal("invalid url", raw)
		}
	}
}
func TestHTTPRedirectIsRevalidated(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest", 302)
	}))
	defer s.Close()
	if _, err := Client(time.Second, true).Get(s.URL); err == nil {
		t.Fatal("metadata redirect accepted")
	}
	if _, err := Client(time.Second, false).Get(s.URL); err == nil {
		t.Fatal("private initial request accepted")
	}
}
