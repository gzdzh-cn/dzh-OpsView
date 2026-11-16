package executor

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"opsview/internal/model"
	"opsview/internal/runtimeconfig"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runtimeFixture(t *testing.T) *Engine {
	t.Helper()
	e, _, _ := softwareFixture(t)
	e.PanelConfigPath = filepath.Join(e.Root, "panel.json")
	bin := t.TempDir()
	if err := os.WriteFile(bin+"/systemctl", []byte("#!/bin/sh\nif [ \"$1\" = show ]; then echo '/opt/opsview/opsview --config /etc/opsview/panel.json'; fi\n"), 0755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(bin+"/ufw", []byte("#!/bin/sh\necho 'Status: inactive'\n"), 0755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	return e
}
func TestRuntimeConfirmationAndExpiredRollback(t *testing.T) {
	e := runtimeFixture(t)
	ctx := context.Background()
	old := runtimeconfig.Default()
	if err := runtimeconfig.Save(e.PanelConfigPath, old); err != nil {
		t.Fatal(err)
	}
	next := old
	next.Protocol = "http"
	next.Entry = "test_entry"
	next.PublicHost = "panel.example.com"
	op := model.Operation{Name: "system.panel-apply", Params: model.Config{"config": next, "confirmedImpact": true}}
	result, err := e.panelRuntime(ctx, op, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(e.pendingPath())
	if strings.Contains(string(b), result.String("confirmToken")) {
		t.Fatal("plaintext confirmation token persisted")
	}
	if _, err = e.panelRuntime(ctx, op, func(string) {}); err == nil {
		t.Fatal("overwrote pending transaction")
	}
	confirm := model.Operation{Name: "system.panel-confirm", Params: model.Config{"token": "bad"}}
	if _, err = e.panelRuntime(ctx, confirm, func(string) {}); err == nil {
		t.Fatal("invalid token accepted")
	}
	confirm.Params["token"] = result["confirmToken"]
	if _, err = e.panelRuntime(ctx, confirm, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if _, err = e.panelRuntime(ctx, confirm, func(string) {}); err == nil {
		t.Fatal("token reused")
	}
	p := pendingPanel{Old: old, Next: next, Deadline: time.Now().Add(-time.Second).Unix(), TokenHash: security.Hash("expired")}
	if err = e.savePending(p); err != nil {
		t.Fatal(err)
	}
	e.checkPanelRuntime(ctx)
	actual, err := runtimeconfig.Load(e.PanelConfigPath)
	if err != nil || actual != old {
		t.Fatal("rollback did not restore old configuration", actual, err)
	}
	if _, err = os.Stat(e.pendingPath()); !os.IsNotExist(err) {
		t.Fatal("pending state retained after successful rollback")
	}
}

func TestRuntimeRejectsExpiredConfirmation(t *testing.T) {
	e := runtimeFixture(t)
	_ = e.savePending(pendingPanel{TokenHash: security.Hash("test"), Deadline: time.Now().Add(-time.Second).Unix()})
	if _, err := e.panelRuntime(context.Background(), model.Operation{Name: "system.panel-confirm", Params: model.Config{"token": "test"}}, func(string) {}); err == nil {
		t.Fatal("expired token accepted")
	}
}

func TestUploadedCertificateStopsRenewalAndPreservesPreviousOnMismatch(t *testing.T) {
	e := runtimeFixture(t)
	e.PanelCertificatePath = filepath.Join(e.Root, "panel.pem")
	cfg := runtimeconfig.Default()
	cfg.PublicHost = "panel.example.com"
	if err := runtimeconfig.Save(e.PanelConfigPath, cfg); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(e.PanelCertificatePath, []byte("previous"), 0600)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "panel.example.com"}, DNSNames: []string{"panel.example.com"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyBytes, _ := x509.MarshalECPrivateKey(key)
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	privateKey := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	renewal := filepath.Join(e.Root, "panel-acme", "renewal.enc")
	_ = os.MkdirAll(filepath.Dir(renewal), 0700)
	_ = os.WriteFile(renewal, []byte("old renewal"), 0600)
	if _, err = e.uploadPanelCertificate(model.Config{"certificate": string(cert), "privateKey": string(privateKey)}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(renewal); !os.IsNotExist(err) {
		t.Fatal("old renewal could overwrite uploaded certificate")
	}
	st, _ := os.Stat(e.PanelCertificatePath)
	if st.Mode().Perm() != 0600 {
		t.Fatal("private key permissions widened")
	}
	saved, _ := os.ReadFile(e.PanelCertificatePath)
	cfg.PublicHost = "another.example.com"
	_ = runtimeconfig.Save(e.PanelConfigPath, cfg)
	if _, err = e.uploadPanelCertificate(model.Config{"certificate": string(cert), "privateKey": string(privateKey)}); err == nil {
		t.Fatal("hostname mismatch accepted")
	}
	actual, _ := os.ReadFile(e.PanelCertificatePath)
	if string(actual) != string(saved) {
		t.Fatal("rejected certificate changed previous file")
	}
}
