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
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNginxPCRECompatibilityRange(t *testing.T) {
	for v, want := range map[string]bool{"1.18.0": true, "1.19.10": true, "1.20.2": true, "1.21.4": true, "1.21.5": false, "1.21.6": false, "1.22.1": false, "1.31.8": false} {
		if nginxNeedsPCRE1(v) != want {
			t.Errorf("%s", v)
		}
	}
}

// Run in both the build system and the independent binary target: verify the
// precise version, OpenSSL TLS, HTTP/2, and regex rewriting with the actual worker.
func verifyNginxPackage(t *testing.T, ctx context.Context, r model.Resource, base, version string, log func(string)) {
	t.Helper()
	out, err := output(ctx, base+"/runtime/sbin/nginx", "-v")
	if err != nil || !strings.Contains(out, "nginx/"+version) {
		t.Fatal("Nginx version", out, err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "native acceptance"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err = security.WriteAtomic(base+"/conf/probe.crt", cert, 0600); err != nil {
		t.Fatal(err)
	}
	if err = security.WriteAtomic(base+"/conf/probe.key", pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	conf := fmt.Sprintf("server { listen 127.0.0.1:%d ssl http2; server_name localhost; ssl_certificate %s/conf/probe.crt; ssl_certificate_key %s/conf/probe.key; ssl_protocols TLSv1.2 TLSv1.3; location /check/ { rewrite ^/check/([0-9]+)$ /result/$1 last; } location ~ ^/result/(?<value>[0-9]+)$ { return 200 \"opsview-$value\"; } }\n", port, base, base)
	path := filepath.Join(base, "conf/sites/native-acceptance.conf")
	if err = security.WriteAtomic(path, []byte(conf), 0644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	if err = run(ctx, log, "", nil, nil, base+"/runtime/sbin/nginx", "-p", base+"/runtime/", "-c", base+"/conf/nginx.conf", "-t"); err != nil {
		t.Fatal(err)
	}
	if err = systemctl(ctx, log, "reload", serviceName(r)); err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(cert)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, ForceAttemptHTTP2: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	var last error
	for i := 0; i < 30; i++ {
		response, er := client.Get(fmt.Sprintf("https://127.0.0.1:%d/check/42", port))
		if er == nil {
			body, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if readErr != nil || response.StatusCode != 200 || string(body) != "opsview-42" || response.ProtoMajor != 2 {
				t.Fatalf("Nginx TLS/regex/HTTP2: %s %s %s %v", response.Status, response.Proto, body, readErr)
			}
			return
		}
		last = er
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatal("Nginx HTTPS did not become healthy", last)
}
