package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func ID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func Hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

type Vault struct{ aead cipher.AEAD }

func OpenVault(path string) (*Vault, error) {
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		b = make([]byte, 32)
		if _, e = rand.Read(b); e != nil {
			return nil, e
		}
		if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			return nil, e
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, err
		}
		_, e = f.Write(b)
		f.Close()
	}
	if e != nil {
		return nil, e
	}
	return VaultFromKey(b)
}

// VaultFromKey opens an existing key without creating or modifying any files.
// Maintenance preflight uses this instead of OpenVault, which initializes missing keys.
func VaultFromKey(b []byte) (*Vault, error) {
	if len(b) != 32 {
		return nil, errors.New("invalid encryption key")
	}
	block, e := aes.NewCipher(b)
	if e != nil {
		return nil, e
	}
	a, e := cipher.NewGCM(block)
	return &Vault{a}, e
}
func (v *Vault) Encrypt(b []byte) string {
	n := make([]byte, v.aead.NonceSize())
	if _, e := rand.Read(n); e != nil {
		panic(e)
	}
	return "enc:" + base64.StdEncoding.EncodeToString(v.aead.Seal(n, n, b, nil))
}
func (v *Vault) Decrypt(s string) ([]byte, error) {
	b, e := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, "enc:"))
	if e != nil {
		return nil, e
	}
	n := v.aead.NonceSize()
	if len(b) < n {
		return nil, errors.New("invalid ciphertext")
	}
	return v.aead.Open(nil, b[:n], b[n:], nil)
}
func Sensitive(k string) bool {
	k = strings.ToLower(k)
	for _, p := range []string{"password", "passwd", "secret", "token", "privatekey", "accesskey", "apikey", "api_key", "environment"} {
		if strings.Contains(k, p) {
			return true
		}
	}
	return false
}
func MaskLog(message string, context any) string {
	b, _ := json.Marshal(context)
	var value any
	_ = json.Unmarshal(b, &value)
	var walk func(any, bool)
	walk = func(v any, secret bool) {
		switch x := v.(type) {
		case map[string]any:
			for k, v := range x {
				walk(v, secret || Sensitive(k) || k == "environment" || k == "headers" || k == "body")
			}
		case []any:
			for _, v := range x {
				walk(v, secret)
			}
		case string:
			if secret && x != "" {
				message = strings.ReplaceAll(message, x, "[已隐藏]")
				var nested any
				if json.Unmarshal([]byte(x), &nested) == nil {
					walk(nested, true)
				}
			}
		}
	}
	walk(value, false)
	return message
}
func Redact(m map[string]any) map[string]any {
	b, _ := json.Marshal(m)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	for k, v := range out {
		if Sensitive(k) && v != nil && v != "" {
			out[k] = "••••••••"
		} else if child, ok := v.(map[string]any); ok {
			out[k] = Redact(child)
		}
	}
	return out
}
func NewTOTP() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
}
func TOTP(secret string, t time.Time) string {
	b, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(t.Unix()/30))
	h := hmac.New(sha1.New, b)
	_, _ = h.Write(n[:])
	s := h.Sum(nil)
	i := s[len(s)-1] & 15
	num := binary.BigEndian.Uint32(s[i:i+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", num%1000000)
}
func VerifyTOTP(secret, code string) bool {
	if len(code) != 6 {
		return false
	}
	for _, delta := range []int{-1, 0, 1} {
		if hmac.Equal([]byte(TOTP(secret, time.Now().Add(time.Duration(delta)*30*time.Second))), []byte(code)) {
			return true
		}
	}
	return false
}
func WriteAtomic(path string, data []byte, mode os.FileMode) error {
	if e := os.MkdirAll(filepath.Dir(path), 0750); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".opsview-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(data)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(tmp, path)
}
func Copy(dst io.Writer, src io.Reader, max int64) error {
	n, e := io.Copy(dst, io.LimitReader(src, max+1))
	if e != nil {
		return e
	}
	if n > max {
		return errors.New("file exceeds size limit")
	}
	return nil
}

// PreserveMasked keeps existing secrets when the client submits redacted form values.
func PreserveMasked(next, previous map[string]any) {
	for key, value := range next {
		if Sensitive(key) && value == "••••••••" {
			if secret, ok := previous[key]; ok {
				next[key] = secret
			}
			continue
		}
		child, ok := value.(map[string]any)
		if !ok {
			continue
		}
		old, _ := previous[key].(map[string]any)
		PreserveMasked(child, old)
	}
}
