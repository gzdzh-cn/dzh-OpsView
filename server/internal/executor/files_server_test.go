package executor

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"opsview/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fileFixture(t *testing.T) (*Engine, string) {
	t.Helper()
	root := t.TempDir()
	e := &Engine{Root: filepath.Join(root, "executor")}
	if err := os.MkdirAll(e.Root, 0755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "files")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	dir, _ = filepath.EvalSymlinks(dir)
	e.Root, _ = filepath.EvalSymlinks(e.Root)
	return e, dir
}
func TestServerFilesRevisionMetadataAndSecrets(t *testing.T) {
	e, dir := fileFixture(t)
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("private=value"), 0640); err != nil {
		t.Fatal(err)
	}
	first, err := e.readFileText(model.Config{"path": path})
	if err != nil || first.String("text") != "private=value" {
		t.Fatal(first, err)
	}
	st, _ := os.Stat(path)
	if _, err = e.writeFileText(model.Config{"path": path, "text": "updated", "revision": first.String("revision")}); err != nil {
		t.Fatal(err)
	}
	index, err := e.trashIndex()
	if err != nil || len(index) != 1 {
		t.Fatal("previous text revision not recoverable", index, err)
	}
	for _, entry := range index {
		old, err := os.ReadFile(entry.Stored)
		if err != nil || string(old) != "private=value" {
			t.Fatal("previous text changed", err)
		}
	}
	after, _ := os.Stat(path)
	u, g, _ := fileOwner(st)
	au, ag, _ := fileOwner(after)
	if after.Mode().Perm() != 0640 || u != au || g != ag {
		t.Fatal("metadata lost")
	}
	_, err = e.writeFileText(model.Config{"path": path, "text": "stale", "revision": first.String("revision")})
	var conflict *model.RevisionConflict
	if !errors.As(err, &conflict) {
		t.Fatal("stale editor overwrote", err)
	}
	secret := filepath.Join(e.Root, "executor.key")
	_ = os.WriteFile(secret, []byte("secret"), 0600)
	for _, name := range []string{"linked-secret", "hard-secret"} {
		target := filepath.Join(dir, name)
		if name == "linked-secret" {
			err = os.Symlink(secret, target)
		} else {
			err = os.Link(secret, target)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err = e.readFileText(model.Config{"path": target}); err == nil {
			t.Fatal("secret alias exposed", name)
		}
	}
	for _, p := range []string{"/proc/self/environ", "/dev/null", filepath.Join(e.Root, "file-manager", "trash.json"), filepath.Join(dir, ".opsview-upload-x")} {
		if _, err = e.filePath(p, true); err == nil {
			t.Fatal("protected path accepted", p)
		}
	}
}
func TestServerFilesRecycleCopyAndCancellation(t *testing.T) {
	e, dir := fileFixture(t)
	src := filepath.Join(dir, "src")
	_ = os.Mkdir(src, 0755)
	_ = os.WriteFile(filepath.Join(src, "hello"), []byte("hello"), 0640)
	dst := filepath.Join(dir, "out")
	_ = os.Mkdir(dst, 0755)
	log := func(string) {}
	if _, err := e.fileBatch(context.Background(), "copy", model.Config{"paths": []string{src}, "target": dst}, log); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dst, "src", "hello"))
	if err != nil || string(b) != "hello" {
		t.Fatal(string(b), err)
	}
	item, err := e.trashFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("source not removed")
	}
	if _, err = e.fileTrash(model.Config{"action": "purge", "id": item.ID}); err == nil {
		t.Fatal("unconfirmed permanent delete accepted")
	}
	_ = os.Mkdir(src, 0755)
	if _, err = e.fileTrash(model.Config{"action": "restore", "id": item.ID}); err == nil {
		t.Fatal("restore overwrote collision")
	}
	_ = os.Remove(src)
	if _, err = e.fileTrash(model.Config{"action": "restore", "id": item.ID}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = e.fileSearch(ctx, model.Config{"path": dir, "query": "hello"}, log); !errors.Is(err, context.Canceled) {
		t.Fatal("search ignored cancellation", err)
	}
	// Publication failure restores the displaced destination automatically.
	target, _ := e.filePath(filepath.Join(src, "hello"), false)
	defer target.close()
	root, err := os.OpenRoot(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err = e.publishFileEntry(root, "missing", target, "overwrite"); err == nil {
		t.Fatal("missing source published")
	}
	b, err = os.ReadFile(filepath.Join(src, "hello"))
	if err != nil || string(b) != "hello" {
		t.Fatal("failed publish lost old destination", err)
	}
}
func TestFileUploadResumeRangeAndExpiry(t *testing.T) {
	e, dir := fileFixture(t)
	path := filepath.Join(dir, "upload.bin")
	v, err := e.fileUploadOperation(model.Config{"path": path, "size": 6}, "upload-create")
	if err != nil {
		t.Fatal(err)
	}
	id := v.String("id")
	chunk := func(offset int, body string, want int) {
		t.Helper()
		req := httptest.NewRequest("PUT", fmtUpload(id, offset), strings.NewReader(body))
		rec := httptest.NewRecorder()
		e.handleFileTransfer(rec, req)
		if rec.Code != want {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
	chunk(0, "abc", 200)
	chunk(0, "abc", 409)
	state, err := e.fileUploadOperation(model.Config{"id": id}, "upload-status")
	if err != nil || state["offset"].(int64) != 3 {
		t.Fatal(state, err)
	}
	chunk(3, "def", 200)
	if _, err = e.fileUploadOperation(model.Config{"id": id, "sha256": strings.Repeat("0", 64)}, "upload-finish"); err == nil {
		t.Fatal("wrong digest accepted")
	}
	if _, err = e.fileUploadOperation(model.Config{"id": id}, "upload-finish"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/file-transfer?path="+path, nil)
	req.Header.Set("Range", "bytes=1-3")
	rec := httptest.NewRecorder()
	e.handleFileTransfer(rec, req)
	if rec.Code != 206 || rec.Body.String() != "bcd" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	v, err = e.fileUploadOperation(model.Config{"path": filepath.Join(dir, "expired"), "size": 10}, "upload-create")
	if err != nil {
		t.Fatal(err)
	}
	s, err := e.uploadSession(v.String("id"))
	if err != nil {
		t.Fatal(err)
	}
	s.ExpiresAt = time.Now().Add(-time.Hour)
	_ = e.writeFileMeta("upload-"+s.ID, s)
	e.cleanupExpiredUploads()
	if _, err = os.Stat(s.Temporary); !os.IsNotExist(err) {
		t.Fatal("expired temporary retained", err)
	}
}
func fmtUpload(id string, offset int) string {
	b, _ := json.Marshal(offset)
	return "/file-transfer?id=" + id + "&offset=" + string(b)
}
func TestArchiveValidationBeforePublication(t *testing.T) {
	for _, kind := range []string{"zip-traversal", "zip-link", "tar-special", "valid"} {
		t.Run(kind, func(t *testing.T) {
			e, dir := fileFixture(t)
			dest := filepath.Join(dir, "dest")
			_ = os.Mkdir(dest, 0755)
			var b bytes.Buffer
			name := "sample.zip"
			if kind == "tar-special" {
				name = "sample.tar.gz"
				gz := gzip.NewWriter(&b)
				tw := tar.NewWriter(gz)
				_ = tw.WriteHeader(&tar.Header{Name: "fifo", Typeflag: tar.TypeFifo, Mode: 0600})
				_ = tw.Close()
				_ = gz.Close()
			} else {
				zw := zip.NewWriter(&b)
				safe, _ := zw.Create("safe.txt")
				_, _ = io.WriteString(safe, "safe")
				if kind == "zip-traversal" {
					bad, _ := zw.Create("../../escape")
					_, _ = io.WriteString(bad, "evil")
				} else if kind == "zip-link" {
					h := &zip.FileHeader{Name: "secret"}
					h.SetMode(os.ModeSymlink | 0777)
					bad, _ := zw.CreateHeader(h)
					_, _ = io.WriteString(bad, "/etc/passwd")
				}
				_ = zw.Close()
			}
			archive := filepath.Join(dir, name)
			_ = os.WriteFile(archive, b.Bytes(), 0600)
			_, err := e.extractFiles(context.Background(), model.Config{"path": archive, "target": dest}, func(string) {})
			if kind == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				got, _ := os.ReadFile(filepath.Join(dest, "safe.txt"))
				if string(got) != "safe" {
					t.Fatal("missing result")
				}
			} else {
				if err == nil {
					t.Fatal("unsafe archive accepted")
				}
				entries, _ := os.ReadDir(dest)
				if len(entries) != 0 {
					t.Fatal("invalid archive partially published", entries)
				}
			}
		})
	}
}

func TestServerFileRevisionDetectsPreservedModificationTime(t *testing.T) {
	e, dir := fileFixture(t)
	path := filepath.Join(dir, "preserve.txt")
	if err := os.WriteFile(path, []byte("private=value"), 0644); err != nil {
		t.Fatal(err)
	}
	before, err := e.readFileText(model.Config{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(path)
	if err = os.WriteFile(path, []byte("private=other"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(path, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	_, err = e.writeFileText(model.Config{"path": path, "revision": before.String("revision"), "text": "stale"})
	var conflict *model.RevisionConflict
	if !errors.As(err, &conflict) {
		t.Fatal("external change with preserved mtime overwritten", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "private=other" {
		t.Fatal(string(data))
	}
}

func TestPermissionMaskKeepsMixedBitsAndChecksRevision(t *testing.T) {
	e, dir := fileFixture(t)
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	os.WriteFile(a, []byte("a"), 0600)
	os.WriteFile(b, []byte("b"), 0640)
	p := model.Config{"paths": []string{a, b}, "permissionMask": 1, "permissionValue": 1}
	if _, err := e.fileBatch(context.Background(), "permissions", p, func(string) {}); err != nil {
		t.Fatal(err)
	}
	sa, _ := os.Stat(a)
	sb, _ := os.Stat(b)
	if sa.Mode().Perm() != 0601 || sb.Mode().Perm() != 0641 {
		t.Fatal("mixed permissions overwritten", sa.Mode(), sb.Mode())
	}
	rev := fileRevision(sa)
	os.WriteFile(a, []byte("external-modification"), 0601)
	p = model.Config{"paths": []string{a}, "mode": "777", "revisions": map[string]any{a: rev}}
	if _, err := e.fileBatch(context.Background(), "permissions", p, func(string) {}); err == nil {
		t.Fatal("stale file revision accepted")
	}
	sa, _ = os.Stat(a)
	if sa.Mode().Perm() == 0777 {
		t.Fatal("permission changed despite conflict")
	}
}
