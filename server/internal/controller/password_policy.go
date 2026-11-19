package controller

import (
	"errors"
	"time"
	"unicode"
)

func (c *Controller) passwordPolicy(password string) error {
	p := c.panelPreferences()
	if len(password) < p.Int("passwordMinLength") || len(password) > 72 {
		return errors.New("密码不满足最小长度策略或超过 72 字节")
	}
	if p.Bool("passwordRequireMixed") {
		var upper, lower, digit, symbol bool
		for _, r := range password {
			switch {
			case unicode.IsUpper(r):
				upper = true
			case unicode.IsLower(r):
				lower = true
			case unicode.IsDigit(r):
				digit = true
			default:
				symbol = true
			}
		}
		if !upper || !lower || !digit || !symbol {
			return errors.New("密码须包含大写、小写字母、数字及符号")
		}
	}
	return nil
}
func (c *Controller) passwordExpired(a admin) bool {
	p := c.panelPreferences()
	days := p.Int("passwordExpiryDays")
	if days <= 0 {
		return false
	}
	since := a.PasswordChangedAt
	if since <= 0 {
		since = int64(p.Int("passwordPolicySince"))
	}
	return since > 0 && time.Now().Unix()-since >= int64(days)*86400
}
