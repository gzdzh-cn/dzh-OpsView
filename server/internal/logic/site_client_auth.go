package logic

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"opsview/internal/model"
)

func ValidateSiteClientAuth(c model.Config) error {
	if !c.Bool("clientAuthEnabled") {
		return nil
	}
	if !c.Bool("tls") {
		return errors.New("双向认证需要先部署网站 HTTPS 证书")
	}
	data := bytes.TrimSpace([]byte(c.String("clientCA")))
	if len(data) == 0 || len(data) > 65536 {
		return errors.New("请提供最多 64 KiB 的客户端 CA 证书")
	}
	count := 0
	for len(data) > 0 {
		block, rest := pem.Decode(data)
		if block == nil {
			return errors.New("客户端 CA 必须为 PEM 证书，不包含私钥")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if block.Type != "CERTIFICATE" || err != nil || !cert.IsCA || cert.KeyUsage&x509.KeyUsageCertSign == 0 {
			return errors.New("仅接受具备证书签发用途的 CA 证书")
		}
		count++
		if count > 16 {
			return errors.New("客户端 CA 最多 16 个")
		}
		data = bytes.TrimSpace(rest)
	}
	return nil
}
