package executor

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafePathsAndSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	_ = os.Symlink(outside, filepath.Join(root, "link"))
	for _, p := range []string{"../escape", "/etc/passwd", "link/file"} {
		if _, err := safePath(root, p); err == nil {
			t.Fatalf("accepted %s", p)
		}
	}
	if _, err := safePath(root, "safe/sub/file"); err != nil {
		t.Fatal(err)
	}
}
func TestArchiveTraversalRejected(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "bad.tar.gz")
	f, _ := os.Create(archive)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "../../escape", Size: 4, Mode: 0644})
	_, _ = tw.Write([]byte("evil"))
	tw.Close()
	gz.Close()
	f.Close()
	dest := filepath.Join(dir, "out")
	_ = os.MkdirAll(dest, 0755)
	if err := extractArchive(context.Background(), archive, "bad.tar.gz", dest, false); err == nil {
		t.Fatal("tar traversal accepted")
	}
}
func TestBackupAuthenticatedOrderingAndTruncation(t *testing.T) {
	dir := t.TempDir()
	v, err := security.OpenVault(filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "source")
	payload := bytes.Repeat([]byte("backup-secret"), 200000)
	_ = os.WriteFile(src, payload, 0600)
	enc := filepath.Join(dir, "data.ovb")
	if err = encryptBackup(v, src, enc); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "restore")
	if err = decryptBackup(v, enc, out); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, payload) {
		t.Fatal("round trip failed")
	}
	data, _ := os.ReadFile(enc)
	if bytes.Contains(data, []byte("backup-secret")) {
		t.Fatal("unencrypted backup")
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	lines[1], lines[2] = lines[2], lines[1]
	bad := filepath.Join(dir, "bad")
	_ = os.WriteFile(bad, []byte(strings.Join(lines, "\n")+"\n"), 0600)
	if err = decryptBackup(v, bad, filepath.Join(dir, "bad-out")); err == nil {
		t.Fatal("reordered blocks accepted")
	}
	lines = strings.Split(strings.TrimSpace(string(data)), "\n")
	_ = os.WriteFile(bad, []byte(strings.Join(lines[:len(lines)-1], "\n")+"\n"), 0600)
	if err = decryptBackup(v, bad, filepath.Join(dir, "truncated-out")); err == nil {
		t.Fatal("truncated backup accepted")
	}
}
func TestComposePrivilegeBoundaries(t *testing.T) {
	image := "redis@sha256:" + strings.Repeat("a", 64)
	valid := "services:\n  web:\n    image: " + image + "\n    ports: [\"127.0.0.1:6379:6379\"]\n    volumes: [\"data:/data\"]\nvolumes:\n  data: {}\n"
	if err := validateCompose(valid, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(valid, "image: "+image, "image: redis:latest", 1), strings.Replace(valid, "ports:", "privileged: true\n    ports:", 1), strings.Replace(valid, "data:/data", "/var/run/docker.sock:/var/run/docker.sock", 1), strings.Replace(valid, "data: {}", "data:\n    external: true", 1), strings.Replace(valid, "127.0.0.1", "${HOST}", 1)} {
		if err := validateCompose(bad, t.TempDir()); err == nil {
			t.Fatal("privileged compose accepted", bad)
		}
	}
}
func TestFilesDoNotEscapeOrExposeEnvironment(t *testing.T) {
	dir := t.TempDir()
	e := &Engine{Root: dir}
	resource := model.Resource{ID: "app1", Kind: "app", Name: "app"}
	base := filepath.Join(dir, "apps", "app1")
	_ = os.MkdirAll(base, 0755)
	outside := filepath.Join(dir, "outside")
	_ = os.WriteFile(outside, []byte("secret"), 0600)
	_ = os.Symlink(outside, base+"/evil")
	for _, p := range []string{"../outside", "evil"} {
		if _, err := e.files(context.Background(), model.Operation{Name: "files.read", Resource: resource, Params: model.Config{"path": p}}, func(string) {}); err == nil {
			t.Fatal("file escaped", p)
		}
	}
	_, err := e.files(context.Background(), model.Operation{Name: "files.write", Resource: resource, Params: model.Config{"path": "index.html", "content": base64.StdEncoding.EncodeToString([]byte("hello"))}}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(base + "/index.html")
	if string(b) != "hello" {
		t.Fatal(string(b))
	}
}
