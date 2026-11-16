package account

import (
	"bytes"
	"golang.org/x/crypto/bcrypt"
	"opsview/internal/dao"
	"os"
	"path/filepath"
	"testing"
)

func TestCredentialsAndMutations(t *testing.T) {
	dir := t.TempDir()
	s, e := dao.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	name, password, e := RandomCredentials()
	if e != nil {
		t.Fatal(e)
	}
	if len(name) != 15 || name[:3] != "ov_" || len(password) != 32 {
		t.Fatal("invalid random credentials")
	}
	n, p, _ := RandomCredentials()
	if n == name || p == password {
		t.Fatal("reused randomness")
	}
	if e = Initialize(s, name, password, "local"); e != nil {
		t.Fatal(e)
	}
	if e = Initialize(s, n, p, "local"); e == nil {
		t.Fatal("overwrote administrator")
	}
	var a Admin
	if e = s.Setting("admin", &a); e != nil {
		t.Fatal(e)
	}
	if a.Username != name || bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(password)) != nil {
		t.Fatal("cannot authenticate")
	}
	a.TOTP = "test-secret"
	a.PendingTOTP = "pending"
	if e = s.Set("admin", a); e != nil {
		t.Fatal(e)
	}
	if e = s.AddSession(a, "old", "csrf", 9999999999); e != nil {
		t.Fatal(e)
	}
	if e = Change(s, "new-user", p, false, &a, "local"); e != nil {
		t.Fatal(e)
	}
	var count int
	s.DB.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&count)
	if count != 0 {
		t.Fatal("old sessions retained")
	}
	var changed Admin
	s.Setting("admin", &changed)
	if changed.TOTP != a.TOTP || changed.PendingTOTP != a.PendingTOTP {
		t.Fatal("lost TOTP")
	}
	if e = s.AddSession(a, "stale", "csrf", 9999999999); e == nil {
		t.Fatal("stale login created session")
	}
	if e = Change(s, "", password, false, &a, "local"); e == nil {
		t.Fatal("stale password mutation succeeded")
	}
	if e = Change(s, "", "", true, nil, "local"); e != nil {
		t.Fatal(e)
	}
	s.Setting("admin", &changed)
	if changed.TOTP != "" || changed.PendingTOTP != "" {
		t.Fatal("TOTP not cleared")
	}
	// Decrypting the record still yields only a password hash.
	if changed.PasswordHash == p || changed.PasswordHash == password {
		t.Fatal("saved plaintext")
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		b, _ := os.ReadFile(filepath.Join(dir, entry.Name()))
		if bytes.Contains(b, []byte(password)) || bytes.Contains(b, []byte(p)) {
			t.Fatal("password persisted")
		}
	}
}
func TestTransactionRollsBackOnAuditFailure(t *testing.T) {
	s, e := dao.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	if e = Initialize(s, "original", "long-test-password", "local"); e != nil {
		t.Fatal(e)
	}
	var a Admin
	s.Setting("admin", &a)
	s.AddSession(a, "old", "csrf", 9999999999)
	s.DB.Exec("CREATE TRIGGER fail_audit BEFORE INSERT ON audit BEGIN SELECT RAISE(ABORT, 'failure'); END")
	if e = Change(s, "changed", "", false, nil, "local"); e == nil {
		t.Fatal("expected transaction failure")
	}
	var after Admin
	s.Setting("admin", &after)
	if after != a {
		t.Fatal("partial update")
	}
	var count int
	s.DB.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&count)
	if count != 1 {
		t.Fatal("partial session deletion")
	}
}
