package security

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVaultAuthenticationAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	v, err := OpenVault(path)
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("private database credentials")
	enc := v.Encrypt(secret)
	if bytes.Contains([]byte(enc), secret) {
		t.Fatal("plaintext leaked")
	}
	again, err := OpenVault(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := again.Decrypt(enc)
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatal("persistent key did not decrypt", err)
	}
	tampered := []byte(enc)
	tampered[len(tampered)-3] ^= 1
	if _, err = v.Decrypt(string(tampered)); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0600 {
		t.Fatal("key permissions", st.Mode())
	}
}
func TestTOTPRFCVectorAndWindow(t *testing.T) {
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	if got := TOTP(secret, time.Unix(59, 0)); got != "287082" {
		t.Fatal(got)
	}
	if !VerifyTOTP(secret, TOTP(secret, time.Now())) {
		t.Fatal("valid code rejected")
	}
	if VerifyTOTP(secret, "12345") || VerifyTOTP(secret, TOTP(secret, time.Now().Add(-5*time.Minute))) {
		t.Fatal("invalid code accepted")
	}
}
func TestRedactionNested(t *testing.T) {
	m := map[string]any{"password": "dont-log", "child": map[string]any{"secretKey": "hidden", "port": 3306}, "environment": "{password:bad}"}
	r := Redact(m)
	if r["password"] == "dont-log" || r["environment"] == m["environment"] || r["child"].(map[string]any)["secretKey"] == "hidden" {
		t.Fatal(r)
	}
	if m["password"] != "dont-log" {
		t.Fatal("redaction mutated original")
	}
}

func TestPreserveNestedMaskedSecrets(t *testing.T) {
	old := map[string]any{"values": map[string]any{"accessKey": "original-key", "secretKey": "original-secret", "port": 1234}}
	next := map[string]any{"values": map[string]any{"accessKey": "••••••••", "secretKey": "••••••••", "port": 5678}}
	PreserveMasked(next, old)
	values := next["values"].(map[string]any)
	if values["accessKey"] != "original-key" || values["secretKey"] != "original-secret" || values["port"] != 5678 {
		t.Fatal(values)
	}
}
