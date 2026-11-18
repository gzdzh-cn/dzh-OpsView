package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPinnedMirrorRetainsDigestAndCancellation(t *testing.T) {
	payload := []byte("verified dependency archive")
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	hits := 0
	badMirror := false
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path == "/primary" {
			w.WriteHeader(403)
			return
		}
		if badMirror {
			w.Write([]byte("tampered"))
			return
		}
		w.Write(payload)
	}))
	defer srv.Close()
	previous := http.DefaultTransport
	http.DefaultTransport = srv.Client().Transport
	defer func() { http.DefaultTransport = previous }()
	path := filepath.Join(t.TempDir(), "archive")
	sources := []string{srv.URL + "/primary", srv.URL + "/mirror"}
	if err := downloadPinnedMirrors(context.Background(), sources, digest, path, func(string) {}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(payload) || hits != 2 {
		t.Fatal(string(got), hits, err)
	}
	badMirror = true
	if err := downloadPinnedMirrors(context.Background(), sources, digest, path, func(string) {}); err == nil {
		t.Fatal("bad mirror checksum accepted")
	}
	got, err = os.ReadFile(path)
	if err != nil || string(got) != string(payload) {
		t.Fatal("verified archive overwritten on failure", err)
	}
	before := hits
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := downloadPinnedMirrors(ctx, sources, digest, path, func(string) {}); err != context.Canceled || hits != before {
		t.Fatal("cancellation retried", err, hits)
	}
}
