package executor

import (
	"context"
	"net"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRedisRestoreRejectsAmbiguousAndExternalPersistence(t *testing.T) {
	old := "dir /managed/data\nappendonly yes\nsave 900 1\nsave 300 10\nrequirepass hidden\n"
	next, err := redisRestoreConfiguration(old, "/managed/data")
	if err != nil || !strings.Contains(next, "appendonly no") || !strings.Contains(next, "requirepass hidden") {
		t.Fatal(next, err)
	}
	for _, bad := range []string{strings.Replace(old, "/managed/data", "/etc", 1), old + "dir /managed/data\n", old + "dbfilename ../secret\n", old + "appenddirname /etc\n", old + "include /etc/x\n", old + "loadmodule /tmp/x.so\n"} {
		if _, err = redisRestoreConfiguration(bad, "/managed/data"); err == nil {
			t.Fatal("accepted", bad)
		}
	}
}

func TestRedisSnapshotCopyIsPrivateAndExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dump.rdb")
	if err := copyRedisSnapshot(strings.NewReader("snapshot"), path); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0600 {
		t.Fatal(st.Mode())
	}
	if err := copyRedisSnapshot(strings.NewReader("overwrite"), path); err == nil {
		t.Fatal("overwrote existing snapshot")
	}
}

// Called by the disposable Ubuntu systemd acceptance test after the actual Redis source build.
func testRedisBackupRecovery(t *testing.T, e *Engine, source model.Resource) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	for _, args := range [][]string{{"SET", "opsview-backup-probe", "private-backup-value"}, {"-n", "1", "SET", "second-database", "preserved"}} {
		if _, err := e.redisCommand(ctx, source, args...); err != nil {
			t.Fatal(err)
		}
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	target := model.Resource{ID: security.ID(), Kind: "instance", Name: "restore-target", Status: "ready", Config: cloneConfig(source.Config)}
	target.Config["port"] = port
	target.Config["deferStart"] = false
	base := filepath.Join(e.Root, "instances", target.ID)
	for _, dir := range []string{"conf", "data", "logs"} {
		if err = os.MkdirAll(base+"/"+dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.Symlink(filepath.Join(e.Root, "instances", source.ID, "runtime"), base+"/runtime"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = systemctl(context.Background(), func(string) {}, "stop", serviceName(target))
		_ = systemctl(context.Background(), func(string) {}, "disable", serviceName(target))
		_ = os.Remove(filepath.Join("/etc/systemd/system", serviceName(target)))
	}()
	if err = e.configureNative(ctx, target, base, func(s string) { t.Log(s) }); err != nil {
		t.Fatal(err)
	}
	if err = e.waitRedisReady(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err = e.save(target); err != nil {
		t.Fatal(err)
	}
	backup := model.Resource{ID: security.ID(), Kind: "backup", Name: "redis-backup", Config: model.Config{"targetId": source.ID, "destination": "local", "retention": 2}}
	var logs strings.Builder
	log := func(s string) { logs.WriteString(s + "\n"); t.Log(s) }
	result, err := e.backup(ctx, model.Operation{Name: "backup.backup", Resource: backup}, log)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := os.ReadFile(filepath.Join(e.Root, "backups", backup.ID, result.String("artifact")))
	if err != nil || strings.Contains(string(artifact), "private-backup-value") || strings.Contains(logs.String(), "private-backup-value") {
		t.Fatal("plaintext leaked", err)
	}
	_, err = e.backup(ctx, model.Operation{Name: "backup.restore", Resource: backup, Params: model.Config{"targetInstanceId": target.ID, "maintenanceConfirmed": true}}, log)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []model.Resource{source, target} {
		value, err := e.redisCommand(ctx, r, "GET", "opsview-backup-probe")
		if err != nil || strings.TrimSpace(value) != "private-backup-value" {
			t.Fatal("source or restored value lost", err)
		}
		value, err = e.redisCommand(ctx, r, "-n", "1", "GET", "second-database")
		if err != nil || strings.TrimSpace(value) != "preserved" {
			t.Fatal("second logical DB lost", err)
		}
	}
	if err = systemctl(ctx, log, "restart", serviceName(target)); err != nil {
		t.Fatal(err)
	}
	if err = e.waitRedisReady(ctx, target); err != nil {
		t.Fatal(err)
	}
	value, err := e.redisCommand(ctx, target, "GET", "opsview-backup-probe")
	if err != nil || strings.TrimSpace(value) != "private-backup-value" {
		t.Fatal("AOF restore did not survive restart", err)
	}
	if _, err = e.backup(ctx, model.Operation{Name: "backup.restore", Resource: backup, Params: model.Config{"targetInstanceId": target.ID, "maintenanceConfirmed": true}}, log); err == nil {
		t.Fatal("overwrote nonempty restore target")
	}
	t.Log("PASS encrypted Redis backup, empty-instance restore, AOF restart and source preservation")
}
