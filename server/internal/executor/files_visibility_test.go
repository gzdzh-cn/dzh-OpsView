package executor

import (
	"context"
	"opsview/internal/model"
	"os"
	"path/filepath"
	"testing"
)

func TestFileVisibilityFiltersBeforePaginationAndSearch(t *testing.T) {
	e, dir := fileFixture(t)
	secret := filepath.Join(e.Root, "executor.key")
	if err := os.WriteFile(secret, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "b.txt", ".env", ".opsview-write-hidden"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("text"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range map[string]string{"secret-link": secret, "runtime-link": "/dev", "public-link": filepath.Join(dir, "a.txt")} {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Link(secret, filepath.Join(dir, "secret-hard")); err != nil {
		t.Fatal(err)
	}
	for _, hidden := range []bool{false, true} {
		count := 3
		if hidden {
			count++
		}
		got, err := e.listFiles(context.Background(), model.Config{"path": dir, "hidden": hidden, "pageSize": 1, "page": 2})
		if err != nil || got.Int("total") != count || len(got["items"].([]model.Config)) != 1 {
			t.Fatal(got, err)
		}
		all, err := e.listFiles(context.Background(), model.Config{"path": dir, "hidden": hidden})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range all["items"].([]model.Config) {
			if item.String("name") == "secret-link" || item.String("name") == "secret-hard" || item.String("name") == "runtime-link" || item.Bool("protected") {
				t.Fatal("protected entry disclosed", item)
			}
		}
	}
	result, err := e.fileSearch(context.Background(), model.Config{"path": dir, "query": "secret"}, func(string) {})
	if err != nil || result.Int("total") != 0 {
		t.Fatal(result, err)
	}
	for _, path := range []string{"/proc", "/sys", "/dev", "/run", filepath.Join(dir, "runtime-link"), filepath.Join(dir, "secret-link")} {
		if _, err := e.listFiles(context.Background(), model.Config{"path": path}); err == nil {
			t.Fatal("protected directory browsable", path)
		}
	}
	// Whole-directory mutation restrictions do not hide browsable system directories.
	v, err := e.fileVisibility(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/etc", "/usr", "/var"} {
		st, err := os.Lstat(path)
		if err == nil && !v.visible(path, st) {
			t.Fatal("system directory hidden", path)
		}
	}
}
func TestFileMetadataFiltersStaleProtectedDirectories(t *testing.T) {
	e, dir := fileFixture(t)
	protected := filepath.Join(e.Root, "state")
	if err := os.Mkdir(protected, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(protected, alias); err != nil {
		t.Fatal(err)
	}
	paths := []string{dir, protected, alias, "/proc", filepath.Join(dir, "missing")}
	if err := e.writeFileMeta("favorites", paths); err != nil {
		t.Fatal(err)
	}
	result, err := e.fileMetadata(context.Background(), model.Config{"recentPaths": paths})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"favorites", "recentPaths"} {
		got := result[key].([]string)
		if len(got) != 1 || got[0] != dir {
			t.Fatal(key, got)
		}
	}
	if _, err := e.fileMetadata(context.Background(), model.Config{"recentPaths": make([]string, 16)}); err == nil {
		t.Fatal("unbounded recent paths")
	}
}
