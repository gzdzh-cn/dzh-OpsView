package executor

import (
	"context"
	"encoding/json"
	"opsview/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func logFixture(t *testing.T, software, content string) (*Engine, model.Resource, string) {
	t.Helper()
	e, r, _ := softwareFixture(t)
	r.Config["software"] = software
	if err := e.save(r); err != nil {
		t.Fatal(err)
	}
	path, _, _, err := e.softwareLogTarget(r, model.Config{"kind": "error"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte(content), 0640); err != nil {
		t.Fatal(err)
	}
	return e, r, path
}

func TestSoftwareLogHeadersAndTimezones(t *testing.T) {
	tests := []struct{ line, level, timestamp string }{
		{"2026/11/17 12:34:56 [warn] 1#1: upstream failed", "warning", ""},
		{"2026-11-17T12:34:56.123456Z 0 [ERROR] [MY-010] broken", "error", "2026-11-17T12:34:56.123456Z"},
		{"# Time: 2026-11-17T12:34:56.123456Z", "", "2026-11-17T12:34:56.123456Z"},
		{"[17-Nov-2026 20:34:56 Asia/Shanghai] PHP Warning: missing", "warning", "2026-11-17T12:34:56Z"},
		{"[17-Nov-2026 12:34:56 UTC] PHP Fatal error: failed", "critical", "2026-11-17T12:34:56Z"},
		{"[17-Nov-2026 12:34:56] NOTICE: fpm is running", "notice", ""},
		{"123:M 17 Nov 2026 12:34:56.123 # warning", "warning", ""},
		{"123:M 17 Nov 2026 12:34:56.123 * ready", "notice", ""},
	}
	for _, tc := range tests {
		at, level, header := softwareLogHeader(tc.line)
		if !header || level != tc.level {
			t.Fatalf("%s: %v %s", tc.line, header, level)
		}
		if tc.timestamp != "" && at.UTC().Format(time.RFC3339Nano) != tc.timestamp {
			t.Fatalf("timezone lost: %s %s", tc.line, at)
		}
	}
	for _, p := range []model.Config{
		{"level": "debug|info"}, {"level": "--all"}, {"since": "2026-11-17"},
		{"since": "2026-11-18T00:00:00Z", "until": "2026-11-17T00:00:00Z"},
		{"search": strings.Repeat("a", 257)}, {"limit": "0"}, {"limit": "2001"},
	} {
		if _, err := parseLogQuery(p); err == nil {
			t.Fatal("invalid filters accepted", p)
		}
	}
}

func TestSoftwareLogsPreserveMultilineAndMaskBeforeSearch(t *testing.T) {
	e, r, _ := logFixture(t, "mysql", "orphan line\n# Time: 2026-11-17T12:00:00Z\n# User@Host: local\n# Query_time: 2\nSELECT 'a-super-secret';\nFROM example;\n# Time: 2026-11-17T13:00:00Z\nSELECT 2;\n")
	// MySQL's error and slow files have distinct allowlisted paths.
	q := model.Config{"kind": "error", "since": "2026-11-17T11:00:00Z", "until": "2026-11-17T12:30:00Z", "search": "FROM example"}
	result, err := e.softwareLogs(context.Background(), r, q)
	if err != nil {
		t.Fatal(err)
	}
	text := result.String("content")
	if strings.Contains(text, "a-super-secret") || strings.Contains(text, "SELECT 2") || strings.Contains(text, "orphan") || !strings.Contains(text, "# Query_time: 2") || !strings.Contains(text, "FROM example") {
		t.Fatal(result)
	}
	q["search"] = "a-super-secret"
	result, err = e.softwareLogs(context.Background(), r, q)
	if err != nil || result.String("content") != "" {
		t.Fatal("secret search exposed a match", result, err)
	}
	q["search"] = ""
	q["since"] = "2026-11-17T00:00:00Z"
	delete(q, "until")
	q["limit"] = "1"
	result, err = e.softwareLogs(context.Background(), r, q)
	if err != nil || !result.Bool("limited") || result["unknownTimeRecords"] != 1 || !strings.Contains(result.String("content"), "SELECT 2") {
		t.Fatal(result, err)
	}

	masked := e.filteredSoftwareLog(r, "2026-11-17T12:00:00Z [error] redis://user:p%40ss@localhost tcp://localhost?auth=encoded%21&timeout=2", logQuery{limit: 10}, false).String("content")
	if strings.Contains(masked, "p%40ss") || strings.Contains(masked, "encoded%21") || !strings.Contains(masked, "timeout=2") {
		t.Fatal(masked)
	}
}

func TestSoftwareLogsBoundedReadAndSymlinkHardlinkRefusal(t *testing.T) {
	e, r, path := logFixture(t, "nginx", strings.Repeat("x", softwareLogWindow+100)+"\n2026/11/17 12:00:00 [error] recent\n")
	result, err := e.softwareLogs(context.Background(), r, model.Config{"kind": "error"})
	if err != nil || !result.Bool("windowTruncated") || !strings.Contains(result.String("content"), "recent") || strings.Contains(result.String("content"), "xxxxx") {
		t.Fatal(result, err)
	}
	if _, err = e.softwareLogs(context.Background(), r, model.Config{"kind": "../conf/redis.conf"}); err == nil {
		t.Fatal("arbitrary file read")
	}
	_ = os.Rename(path, path+".outside")
	_ = os.Symlink(path+".outside", path)
	if _, err = e.softwareLogs(context.Background(), r, model.Config{"kind": "error"}); err == nil {
		t.Fatal("symlink accepted")
	}
	_ = os.Remove(path)
	_ = os.Link(path+".outside", path)
	if _, err = e.softwareLogs(context.Background(), r, model.Config{"kind": "error"}); err == nil {
		t.Fatal("hardlink accepted")
	}
}

func mockLogService(t *testing.T, state string) {
	t.Helper()
	bin := t.TempDir()
	t.Setenv("OPSVIEW_TEST_SERVICE_STATE", state)
	if err := os.WriteFile(bin+"/systemctl", []byte("#!/bin/sh\nprintf '%s\\n' \"$OPSVIEW_TEST_SERVICE_STATE\"\n[ \"$OPSVIEW_TEST_SERVICE_STATE\" = active ]\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
}

func TestSoftwareLogArchiveLifecycleAndGuards(t *testing.T) {
	e, r, path := logFixture(t, "redis", "123:M 17 Nov 2026 12:00:00.000 * a-super-secret\n")
	mockLogService(t, "active")
	read := func(p model.Config) model.Config {
		t.Helper()
		result, err := e.softwareLogs(context.Background(), r, p)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	p := model.Config{"kind": "error", "baseRevision": read(model.Config{"kind": "error"}).String("revision"), "confirmed": true}
	if _, err := e.manageSoftwareLog(context.Background(), r, p, "rotate-log", func(string) {}); err == nil {
		t.Fatal("running Redis log rotated")
	}
	t.Setenv("OPSVIEW_TEST_SERVICE_STATE", "inactive")
	stBefore, _ := os.Stat(path)
	if _, err := e.manageSoftwareLog(context.Background(), r, model.Config{"kind": "error"}, "rotate-log", func(string) {}); err == nil {
		t.Fatal("unconfirmed cleanup")
	}
	p["baseRevision"] = "stale"
	if _, err := e.manageSoftwareLog(context.Background(), r, p, "rotate-log", func(string) {}); err == nil {
		t.Fatal("stale cleanup")
	}
	p["baseRevision"] = read(model.Config{"kind": "error"}).String("revision")
	var taskLogs strings.Builder
	result, err := e.manageSoftwareLog(context.Background(), r, p, "rotate-log", func(s string) { taskLogs.WriteString(s) })
	if err != nil {
		t.Fatal(err)
	}
	name := result.String("archive")
	stAfter, _ := os.Stat(path)
	if stAfter.Size() != 0 || stAfter.Mode().Perm() != stBefore.Mode().Perm() || os.SameFile(stBefore, stAfter) {
		t.Fatal("new log not created or permissions changed")
	}
	archivePath := filepath.Join(e.logArchiveDir(r, r.ID, "error"), name)
	archiveInfo, _ := os.Stat(archivePath)
	archiveDir, _ := os.Stat(filepath.Dir(archivePath))
	if archiveInfo.Mode().Perm() != 0600 || archiveDir.Mode().Perm() != 0700 {
		t.Fatal("archive is not private")
	}
	if strings.Contains(taskLogs.String(), "a-super-secret") {
		t.Fatal("archive contents logged")
	}
	archived := read(model.Config{"kind": "error", "archive": name})
	if !strings.Contains(archived.String("content"), "已隐藏") || archived.Bool("canRotate") {
		t.Fatal(archived)
	}
	p["archive"], p["baseRevision"] = name, archived.String("revision")
	if _, err = e.manageSoftwareLog(context.Background(), r, p, "delete-log-archive", func(string) {}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(archivePath); !os.IsNotExist(err) {
		t.Fatal("archive not removed")
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("current log removed")
	}
	for _, archive := range []string{"../redis.conf", "/etc/shadow", "a.log"} {
		if _, err = e.softwareLogs(context.Background(), r, model.Config{"kind": "error", "archive": archive}); err == nil {
			t.Fatal("bad archive allowed")
		}
	}
}

func TestNginxLogReopenFailureRestoresOriginal(t *testing.T) {
	e, r, path := logFixture(t, "nginx", "2026/11/17 12:00:00 [error] original\n")
	mockLogService(t, "active")
	bin := filepath.Join(e.Root, "instances", r.ID, "runtime", "sbin")
	_ = os.MkdirAll(bin, 0755)
	_ = os.WriteFile(bin+"/nginx", []byte("#!/bin/sh\nexit 1\n"), 0755)
	before, _ := os.Stat(path)
	result, _ := e.softwareLogs(context.Background(), r, model.Config{"kind": "error"})
	_, err := e.manageSoftwareLog(context.Background(), r, model.Config{"kind": "error", "confirmed": true, "baseRevision": result.String("revision")}, "rotate-log", func(string) {})
	if err == nil {
		t.Fatal("failed reopen accepted")
	}
	after, _ := os.Stat(path)
	content, _ := os.ReadFile(path)
	if !os.SameFile(before, after) || before.Mode().Perm() != after.Mode().Perm() || !strings.Contains(string(content), "original") {
		t.Fatal("original file was not restored")
	}
	archives := e.softwareLogArchives(r, r.ID, "error")
	if len(archives) != 1 {
		t.Fatal("replacement inode was not retained on rollback", archives)
	}
	archived, err := os.Stat(filepath.Join(e.logArchiveDir(r, r.ID, "error"), archives[0].String("name")))
	if err != nil || archived.Mode().Perm() != 0600 {
		t.Fatal("partial reopen log archive is not private", err)
	}
}

func TestPHPLogScopeAndJournalFilters(t *testing.T) {
	e, r, _ := logFixture(t, "php", "[17-Nov-2026 12:00:00 UTC] PHP Warning: version\n")
	site := model.Resource{ID: "site-logs", Kind: "site", Name: "private", Config: model.Config{"phpInstanceId": r.ID}}
	if err := e.save(site); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(e.Root, "sites", site.ID, "logs")
	_ = os.MkdirAll(dir, 0750)
	_ = os.WriteFile(dir+"/php-error.log", []byte("[17-Nov-2026 12:00:00 UTC] PHP Warning: site only\n"), 0640)
	result, err := e.softwareLogs(context.Background(), r, model.Config{"siteId": site.ID, "kind": "error"})
	if err != nil || !strings.Contains(result.String("content"), "site only") || strings.Contains(result.String("content"), "version") {
		t.Fatal(result, err)
	}
	site.Config["phpInstanceId"] = "another"
	_ = e.save(site)
	if _, err = e.softwareLogs(context.Background(), r, model.Config{"siteId": site.ID, "kind": "error"}); err == nil {
		t.Fatal("unrelated site read")
	}
	entry, _ := json.Marshal(map[string]string{"MESSAGE": "first\nstack line", "PRIORITY": "4", "__REALTIME_TIMESTAMP": "1794916800000000"})
	text := journalLogText(string(entry) + "\ninvalid\n")
	filtered := e.filteredSoftwareLog(r, text, logQuery{level: "warning", limit: 10}, false)
	if !strings.Contains(filtered.String("content"), "stack line") || filtered["count"] != 1 {
		t.Fatal(filtered)
	}
}

func TestSoftwareLogEmptyAndLimit(t *testing.T) {
	e, r, _ := logFixture(t, "nginx", "")
	result, err := e.softwareLogs(context.Background(), r, model.Config{"kind": "error"})
	if err != nil || result["count"] != 0 {
		t.Fatal(result, err)
	}
	result = e.filteredSoftwareLog(r, strings.Repeat("continuation\n", 50000), logQuery{limit: 1}, false)
	if result["count"] != 1 {
		t.Fatal("multiline record split")
	}
	query, _ := parseLogQuery(model.Config{"since": "2026-11-17T00:00:00Z"})
	result = e.filteredSoftwareLog(r, "2026-11-17T12:00:00Z [error] valid\n2026-99-99T12:00:00Z [error] invalid timestamp\nstack\n[17-Nov-2026 12:00:00 Unknown/Zone] PHP Warning: unparseable\n", query, false)
	if result["count"] != 1 || strings.Contains(result.String("content"), "invalid timestamp") || strings.Contains(result.String("content"), "unparseable") || result["unknownTimeRecords"] != 2 {
		t.Fatal("unparseable headers inherited a previous timestamp", result)
	}
}

func TestSoftwareJournalUsesScopedArgvAndBoundedOutput(t *testing.T) {
	e, r, _ := logFixture(t, "nginx", "")
	bin := t.TempDir()
	argsFile := filepath.Join(bin, "args")
	entry, _ := json.Marshal(map[string]string{"MESSAGE": "failure a-super-secret\nstack", "PRIORITY": "3", "__REALTIME_TIMESTAMP": "1794916800000000"})
	t.Setenv("OPSVIEW_TEST_ARGS", argsFile)
	t.Setenv("OPSVIEW_TEST_JOURNAL", string(entry))
	_ = os.WriteFile(bin+"/journalctl", []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$OPSVIEW_TEST_ARGS\"\nprintf '%s\\n' \"$OPSVIEW_TEST_JOURNAL\"\n"), 0755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	result, err := e.softwareLogs(context.Background(), r, model.Config{"kind": "service", "since": "2026-11-17T00:00:00+08:00", "level": "error", "search": "failure"})
	if err != nil || !strings.Contains(result.String("content"), "stack") || strings.Contains(result.String("content"), "a-super-secret") {
		t.Fatal(result, err)
	}
	args, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args), "--unit\n"+serviceName(r)+"\n") || !strings.Contains(string(args), "--since\n2026-11-17T00:00:00+08:00\n") || strings.Contains(string(args), "failure") {
		t.Fatal("unexpected argv", string(args))
	}
	w := &logTailWriter{}
	_, _ = w.Write([]byte(strings.Repeat("x", softwareLogWindow+500)))
	_, _ = w.Write([]byte("\n" + string(entry) + "\n"))
	if len(w.data) > softwareLogWindow || !w.truncated || !strings.HasSuffix(string(w.data), string(entry)+"\n") {
		t.Fatal("journal output was not bounded")
	}
}
