package executor

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"time"
)

func (e *Engine) issueClientCertificate(ctx context.Context) (model.Config, error) {
	e.runtimeMu.Lock()
	defer e.runtimeMu.Unlock()
	base := filepath.Join(e.Root, "client-auth")
	if err := os.MkdirAll(base, 0700); err != nil {
		return nil, err
	}
	caPath := filepath.Join(base, "ca.pem")
	keyPath := filepath.Join(base, "ca.key")
	var ca *x509.Certificate
	var caKey *rsa.PrivateKey
	if b, err := os.ReadFile(caPath); err == nil {
		block, _ := pem.Decode(b)
		if block == nil {
			return nil, errors.New("CA 证书损坏")
		}
		ca, err = x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		key, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, err
		}
		kb, _ := pem.Decode(key)
		if kb == nil {
			return nil, errors.New("CA 私钥损坏")
		}
		caKey, err = x509.ParsePKCS1PrivateKey(kb.Bytes)
		if err != nil {
			return nil, err
		}
	} else if os.IsNotExist(err) {
		caKey, err = rsa.GenerateKey(rand.Reader, 3072)
		if err != nil {
			return nil, err
		}
		serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		ca = &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "OpsView client CA"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().AddDate(5, 0, 0), KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign, IsCA: true, BasicConstraintsValid: true}
		der, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
		if err != nil {
			return nil, err
		}
		ca, err = x509.ParseCertificate(der)
		if err != nil {
			return nil, err
		}
		if err = security.WriteAtomic(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(caKey)}), 0600); err != nil {
			return nil, err
		}
		if err = security.WriteAtomic(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "OpsView administrator device"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	pub, _ := os.ReadFile(caPath)
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	priv := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return model.Config{"ca": string(pub), "certificate": string(cert), "privateKey": string(priv), "expires": tmpl.NotAfter.Format(time.RFC3339)}, ctx.Err()
}
