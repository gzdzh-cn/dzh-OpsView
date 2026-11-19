// Package account is shared by HTTP authentication and local maintenance.
package account

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"golang.org/x/crypto/bcrypt"
	"opsview/internal/dao"
	"time"
)

type Admin struct {
	Username          string `json:"username"`
	PasswordHash      string `json:"passwordHash"`
	TOTP              string `json:"totp"`
	PendingTOTP       string `json:"pendingTotp"`
	PasswordChangedAt int64  `json:"passwordChangedAt,omitempty"`
}

func ValidUsername(s string) bool {
	if len(s) < 2 || len(s) > 40 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
func ValidPassword(s string) bool { return len(s) >= 12 && len(s) <= 72 }
func RandomCredentials() (string, string, error) {
	name := make([]byte, 6)
	password := make([]byte, 24)
	if _, err := rand.Read(name); err != nil {
		return "", "", err
	}
	if _, err := rand.Read(password); err != nil {
		return "", "", err
	}
	return "ov_" + hex.EncodeToString(name), base64.RawURLEncoding.EncodeToString(password), nil
}
func Initialize(s *dao.Store, name, password, ip string) error {
	if !ValidUsername(name) || !ValidPassword(password) {
		return errors.New("用户名格式无效，密码须为 12–72 字节")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	a := Admin{Username: name, PasswordHash: string(hash), PasswordChangedAt: time.Now().Unix()}
	return s.MutateSetting("admin", &a, true, "admin.setup", ip, func() error { return nil })
}

// Change retains two-factor configuration unless disabling it explicitly.
func Change(s *dao.Store, name, password string, resetTOTP bool, expected *Admin, ip string) error {
	if name != "" && !ValidUsername(name) {
		return errors.New("用户名须为 2–40 位字母、数字、横线或下划线")
	}
	var hash []byte
	var err error
	if password != "" {
		if !ValidPassword(password) {
			return errors.New("密码须为 12–72 字节")
		}
		hash, err = bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
	}
	action := "admin.username"
	if password != "" {
		action = "admin.password"
	}
	if resetTOTP {
		action = "admin.2fa.disable"
	}
	var a Admin
	return s.MutateSetting("admin", &a, false, action, ip, func() error {
		if expected != nil && a != *expected {
			return errors.New("账号已变更，请重新验证")
		}
		if name != "" {
			a.Username = name
		}
		if password != "" {
			a.PasswordHash = string(hash)
			a.PasswordChangedAt = time.Now().Unix()
		}
		if resetTOTP {
			a.TOTP = ""
			a.PendingTOTP = ""
		}
		return nil
	})
}
