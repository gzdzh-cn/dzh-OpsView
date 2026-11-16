package runtimeconfig

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.json")
	c, e := Load(path)
	if e != nil || c.Port != 18790 {
		t.Fatal(c, e)
	}
	c.Port = 18888
	c.PublicHost = "panel.example.com"
	if e = Save(path, c); e != nil {
		t.Fatal(e)
	}
	got, e := Load(path)
	if e != nil || got != c {
		t.Fatal(got, e)
	}
	if c.LocalHealthURL() != "https://127.0.0.1:18888/api/v1/health" || c.URL(c.PublicHost) != "https://panel.example.com:18888" {
		t.Fatal("incorrect URL")
	}
	stat, _ := os.Stat(path)
	if stat.Mode().Perm() != 0644 {
		t.Fatal("runtime config not readable")
	}
	for _, s := range []string{"https://panel.example.com", "bad;id", "a..com", "-a.com", "1.2.3.4:80", "0.0.0.0", "::1"} {
		if ValidHost(s) {
			t.Errorf("accepted host %q", s)
		}
	}
	c.Port = 0
	if Save(path, c) == nil {
		t.Fatal("accepted invalid port")
	}
}
func TestPublicAddressValidation(t *testing.T) {
	for _, body := range []string{"8.8.8.8", "127.0.0.1", "10.0.0.1", "<html>", "::1", strings.Repeat("1", 100)} {
		client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != "https://api.ipify.org" {
				t.Fatal("insecure endpoint")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		host, e := DetectPublicHost(client)
		if body == "8.8.8.8" {
			if e != nil || host != body {
				t.Fatal(e)
			}
		} else if e == nil {
			t.Fatal("accepted invalid public address")
		}
	}
	_, e := DetectPublicHost(&http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })})
	if e == nil {
		t.Fatal("accepted offline response")
	}
}

func TestProtocolCompatibilityAndEntryURL(t *testing.T) {
	cfg := Default()
	if cfg.Scheme() != "https" {
		t.Fatal("legacy HTTPS default lost")
	}
	cfg.Protocol = "http"
	cfg.Entry = "secret_entry"
	if cfg.URL("panel.example.com") != "http://panel.example.com:18790/secret_entry" {
		t.Fatal(cfg.URL("panel.example.com"))
	}
	if cfg.LocalHealthURL() != "http://127.0.0.1:18790/api/v1/health" {
		t.Fatal(cfg.LocalHealthURL())
	}
	cfg.Entry = "../escape"
	if cfg.Validate() == nil {
		t.Fatal("invalid security entry accepted")
	}
	cfg.Entry = ""
	cfg.Protocol = "file"
	if cfg.Validate() == nil {
		t.Fatal("invalid protocol accepted")
	}
}
