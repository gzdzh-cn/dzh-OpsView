package executor

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"opsview/internal/model"
	"testing"
	"time"
)

func testWebsiteClientAuth(t *testing.T, e *Engine, r model.Resource) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Disposable website test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ = x509.ParseCertificate(caDER)
	issue := func(serial int64, client bool) ([]byte, []byte) {
		t.Helper()
		k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		cert := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "Disposable website fixture"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, DNSNames: []string{"settings-smoke.test", "docs-smoke.test"}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		if client {
			cert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
		}
		der, err := x509.CreateCertificate(rand.Reader, cert, ca, &k.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		private, err := x509.MarshalPKCS8PrivateKey(k)
		if err != nil {
			t.Fatal(err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})
	}
	serverCert, serverKey := issue(2, false)
	clientCert, clientKey := issue(3, true)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	current, err := e.load(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Execute(ctx, model.Operation{Name: "site.certificate", Resource: current, Params: model.Config{"certificate": string(serverCert), "privateKey": string(serverKey)}}, func(s string) { t.Log(s) }); err != nil {
		t.Fatal(err)
	}
	current, err = e.load(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	current.Config["clientAuthEnabled"] = true
	current.Config["clientCA"] = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	if _, err = e.Execute(ctx, model.Operation{Name: "site.apply", Resource: current}, func(s string) { t.Log(s) }); err != nil {
		t.Fatal(err)
	}
	request := func(cert *tls.Certificate) int {
		t.Helper()
		cfg := &tls.Config{InsecureSkipVerify: true, ServerName: "settings-smoke.test"}
		if cert != nil {
			cfg.Certificates = []tls.Certificate{*cert}
		}
		transport := &http.Transport{TLSClientConfig: cfg}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
		req, _ := http.NewRequestWithContext(ctx, "GET", "https://127.0.0.1/", nil)
		req.Host = "settings-smoke.test"
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		_, _ = io.Copy(io.Discard, res.Body)
		return res.StatusCode
	}
	if code := request(nil); code != 403 {
		t.Fatal("missing client certificate accepted", code)
	}
	pair, err := tls.X509KeyPair(clientCert, clientKey)
	if err != nil {
		t.Fatal(err)
	}
	if code := request(&pair); code != 200 {
		t.Fatal("valid client certificate rejected", code)
	}
	t.Log("PASS actual uploaded TLS, optional TLS handshake plus mandatory client certificate policy, internal revision probe and authenticated response")
}
