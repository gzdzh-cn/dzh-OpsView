package controller

import (
	"compress/gzip"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestStaticCompressionAndCache(t *testing.T) {
	script := strings.Repeat("export const status = 'ready';\n", 400)
	c := Controller{Public: fstest.MapFS{
		"index.html":            {Data: []byte("<html>OpsView</html>")},
		"assets/ui-abcdefgh.js": {Data: []byte(script)},
	}}
	for _, encoding := range []string{"gzip, deflate, br", "gzip;q=0.5", "", "gzip;q=0", "gzip;q=invalid", "gzip;q=NaN"} {
		t.Run(encoding, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/assets/ui-abcdefgh.js", nil)
			r.Header.Set("Accept-Encoding", encoding)
			w := httptest.NewRecorder()
			c.static(w, r)
			if w.Code != 200 || w.Header().Get("Cache-Control") != "private, max-age=31536000, immutable" || w.Header().Get("Vary") != "Accept-Encoding" {
				t.Fatal("asset response headers", w.Code, w.Header())
			}
			body := w.Body.String()
			if strings.HasPrefix(encoding, "gzip,") || encoding == "gzip;q=0.5" {
				if w.Header().Get("Content-Encoding") != "gzip" || w.Body.Len() >= len(script)/2 {
					t.Fatal("large script must be compressed")
				}
				reader, err := gzip.NewReader(w.Body)
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := io.ReadAll(reader)
				reader.Close()
				if err != nil {
					t.Fatal(err)
				}
				body = string(decoded)
			} else if w.Header().Get("Content-Encoding") != "" {
				t.Fatal("gzip was not negotiated")
			}
			if body != script {
				t.Fatal("script contents changed")
			}
		})
	}
	for _, path := range []string{"/", "/settings"} {
		w := httptest.NewRecorder()
		w.Header().Set("Cache-Control", "no-store")
		c.static(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("HTML must not be cached")
		}
	}
	w := httptest.NewRecorder()
	c.static(w, httptest.NewRequest("HEAD", "/assets/ui-abcdefgh.js", nil))
	if w.Body.Len() != 0 || w.Header().Get("Content-Length") == "" {
		t.Fatal("HEAD must return size without body")
	}
}
