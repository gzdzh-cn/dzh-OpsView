package executor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSitePHPIniKeepsFilesAndLogsInsideEachSite(t *testing.T) {
	text := "[PHP]\n; retained comment\nerror_log = /version/logs/error.log\n[Session]\nsession.save_handler = \"files\" ; local store\nsession.save_path = /version/sessions\n"
	for _, base := range []string{"/managed/site-a", "/managed/site-b"} {
		result := sitePHPIni(text, base)
		if phpIniValue(result, "session.save_path", "") != base+"/sessions" || phpIniValue(result, "error_log", "") != base+"/logs/php-error.log" || !strings.Contains(result, "; retained comment") {
			t.Fatal("site did not isolate its writable paths", result)
		}
		if sitePHPIni(result, base) != result {
			t.Fatal("normalization changed an already managed document")
		}
	}
	redis := "[Session]\nsession.save_handler = 'redis' ; enabled\nsession.save_path = \"tcp://127.0.0.1:6379?auth=hidden\"\n"
	if phpIniValue(sitePHPIni(redis, "/managed/site"), "session.save_path", "") != "tcp://127.0.0.1:6379?auth=hidden" {
		t.Fatal("Redis connection was overwritten with a file directory")
	}
	if phpIniValue("; session.save_handler = redis\n", "session.save_handler", "files") != "files" {
		t.Fatal("comment was treated as effective configuration")
	}
}

func TestPHPSessionCleanupRejectsLinksAndPreservesLiveFiles(t *testing.T) {
	base, outside := t.TempDir(), t.TempDir()
	path := filepath.Join(base, "sessions")
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := phpSessionFiles(base, true); err == nil {
		t.Fatal("cleanup followed an external directory link")
	}
	_ = os.Remove(path)
	_ = os.Mkdir(path, 0700)
	for _, name := range []string{"sess_old", "sess_live", "other-old"} {
		_ = os.WriteFile(filepath.Join(path, name), []byte(name), 0600)
	}
	old := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(filepath.Join(path, "sess_old"), old, old)
	_ = os.Chtimes(filepath.Join(path, "other-old"), old, old)
	_ = os.WriteFile(filepath.Join(outside, "sess_external"), []byte("untouched"), 0600)
	_ = os.Chtimes(filepath.Join(outside, "sess_external"), old, old)
	_ = os.Symlink(filepath.Join(outside, "sess_external"), filepath.Join(path, "sess_link"))
	if n, err := phpSessionFiles(base, false); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err := os.Stat(filepath.Join(path, "sess_old")); err != nil {
		t.Fatal("preview deleted data", err)
	}
	if n, err := phpSessionFiles(base, true); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	for _, name := range []string{filepath.Join(path, "sess_live"), filepath.Join(path, "other-old"), filepath.Join(outside, "sess_external")} {
		if _, err := os.Stat(name); err != nil {
			t.Fatal("unrelated or active file removed", name, err)
		}
	}
}
