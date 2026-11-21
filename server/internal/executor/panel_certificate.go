package executor

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/challenge/http01"
	"net"
	"opsview/internal/logic"
	"opsview/internal/model"
	"opsview/internal/runtimeconfig"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"time"
)

func (e *Engine) panelCertificate(ctx context.Context, p model.Config, log func(string)) (model.Config, error) {
	e.siteMu.Lock()
	defer e.siteMu.Unlock()
	if p.Bool("renew") {
		b, err := os.ReadFile(filepath.Join(e.Root, "panel-acme", "renewal.enc"))
		if err != nil {
			return nil, errors.New("没有自动续期记录，请先执行 ov ssl issue")
		}
		b, err = e.Vault.Decrypt(string(b))
		if err != nil {
			return nil, err
		}
		var saved model.Config
		if err = json.Unmarshal(b, &saved); err != nil {
			return nil, err
		}
		for _, key := range []string{"host", "email", "acceptTerms"} {
			if _, ok := p[key]; !ok {
				p[key] = saved[key]
			}
		}
	}
	host := p.String("host")
	if !runtimeconfig.ValidHost(host) {
		return nil, errors.New("证书主机须为域名或公网 IPv4")
	}
	if !p.Bool("acceptTerms") {
		return nil, errors.New("申请前须明确同意 ACME 服务条款")
	}
	base := filepath.Join(e.Root, "panel-acme")
	if err := os.MkdirAll(base+"/cert", 0700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(base+"/acme/.well-known/acme-challenge", 0755); err != nil {
		return nil, err
	}
	var provider challenge.Provider
	for _, site := range e.states() {
		if site.Kind != "site" {
			continue
		}
		for _, domain := range logic.SiteDomainNames(site.Config) {
			if domain == host {
				if inst, err := e.load(site.Config.String("instanceId")); err == nil && inst.Config.Int("port") == 80 && inst.Status == "ready" {
					provider = webroot{filepath.Join(e.Root, "sites", site.ID, "acme", ".well-known", "acme-challenge")}
				}
			}
		}
	}
	var cleanup func()
	if provider == nil {
		for _, inst := range e.states() {
			if inst.Kind == "instance" && inst.Config.String("software") == "nginx" && inst.Config.Int("port") == 80 && inst.Status == "ready" {
				path := filepath.Join(e.Root, "instances", inst.ID, "conf", "sites", "opsview-panel-acme.conf")
				text := "server { listen 80; server_name " + host + "; location ^~ /.well-known/acme-challenge/ { root " + base + "/acme; } location / { return 404; } }\n"
				if err := security.WriteAtomic(path, []byte(text), 0644); err != nil {
					return nil, err
				}
				cleanup = func() { _ = os.Remove(path); _ = e.nginx(context.Background(), inst, func(string) {}, "-s", "reload") }
				if err := e.nginx(ctx, inst, func(string) {}, "-t"); err != nil {
					cleanup()
					return nil, err
				}
				if err := e.nginx(ctx, inst, func(string) {}, "-s", "reload"); err != nil {
					cleanup()
					return nil, err
				}
				provider = webroot{base + "/acme/.well-known/acme-challenge"}
				break
			}
		}
	}
	if cleanup != nil {
		defer cleanup()
	}
	if provider == nil {
		listener, err := net.Listen("tcp", ":80")
		if err != nil {
			return nil, errors.New("80 端口已被非受管服务占用，不能自动申请证书")
		}
		listener.Close()
		provider = http01.NewProviderServer("", "80")
	}
	r := model.Resource{Config: model.Config{"domains": host, "email": p.String("email"), "acceptACMETerms": true}}
	log("通过 HTTP-01 验证申请证书，必须从公网访问 TCP 80")
	if err := e.issueCertificateProvider(ctx, r, base, provider); err != nil {
		return nil, err
	}
	crt, err := os.ReadFile(base + "/cert/fullchain.pem")
	if err != nil {
		return nil, err
	}
	key, err := os.ReadFile(base + "/cert/key.pem")
	if err != nil {
		return nil, err
	}
	pair, err := tls.X509KeyPair(crt, key)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, err
	}
	if err = leaf.VerifyHostname(host); err != nil {
		return nil, err
	}
	if time.Now().Before(leaf.NotBefore) || time.Now().After(leaf.NotAfter) {
		return nil, errors.New("签发证书不在有效期内")
	}
	panelPath := e.panelCertificatePath()
	old, err := os.ReadFile(panelPath)
	if err != nil {
		return nil, err
	}
	combined := append(append(crt, '\n'), key...)
	if err = writeOwnedAtomic(panelPath, combined, 0600); err != nil {
		return nil, err
	}
	metadata := model.Config{"host": host, "email": p.String("email"), "acceptTerms": true, "expires": leaf.NotAfter.UTC().Format(time.RFC3339), "notBefore": leaf.NotBefore.UTC().Format(time.RFC3339)}
	b, _ := json.Marshal(metadata)
	if err = security.WriteAtomic(base+"/renewal.enc", []byte(e.Vault.Encrypt(b)), 0600); err != nil {
		_ = writeOwnedAtomic(panelPath, old, 0600)
		return nil, err
	}
	return model.Config{"expires": leaf.NotAfter.UTC().Format(time.RFC3339), "host": host}, nil
}
func (e *Engine) watchCertificates(ctx context.Context) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			cfg, err := runtimeconfig.Load(e.panelConfigPath())
			if err != nil || cfg.Scheme() != "https" {
				continue
			}
			b, err := os.ReadFile(filepath.Join(e.Root, "panel-acme", "renewal.enc"))
			if err != nil {
				continue
			}
			b, err = e.Vault.Decrypt(string(b))
			if err != nil {
				continue
			}
			var p model.Config
			if json.Unmarshal(b, &p) != nil {
				continue
			}
			expires, err := time.Parse(time.RFC3339, p.String("expires"))
			if err != nil {
				continue
			}
			start, _ := time.Parse(time.RFC3339, p.String("notBefore"))
			threshold := expires.Sub(start) / 3
			if threshold > 30*24*time.Hour {
				threshold = 30 * 24 * time.Hour
			}
			if time.Until(expires) < threshold {
				_, _ = e.panelCertificate(ctx, p, func(string) {})
			}
		}
	}
}

func (e *Engine) panelCertificatePath() string {
	if e.PanelCertificatePath != "" {
		return e.PanelCertificatePath
	}
	return "/var/lib/opsview/panel/panel.pem"
}
func (e *Engine) panelCertificateInfo() model.Config {
	b, err := os.ReadFile(e.panelCertificatePath())
	if err != nil {
		return model.Config{"unavailable": "尚无可读取的证书"}
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return model.Config{"unavailable": "证书格式无效"}
	}
	crt, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return model.Config{"unavailable": "证书解析失败"}
	}
	_, renewalErr := os.Stat(filepath.Join(e.Root, "panel-acme", "renewal.enc"))
	return model.Config{"expires": crt.NotAfter.UTC().Format(time.RFC3339), "notBefore": crt.NotBefore.UTC().Format(time.RFC3339), "issuer": crt.Issuer.CommonName, "domains": crt.DNSNames, "selfSigned": crt.CheckSignatureFrom(crt) == nil, "autoRenew": renewalErr == nil}
}
func (e *Engine) uploadPanelCertificate(p model.Config) (model.Config, error) {
	e.siteMu.Lock()
	defer e.siteMu.Unlock()
	cert, key := []byte(p.String("certificate")), []byte(p.String("privateKey"))
	pair, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return nil, errors.New("证书和私钥不匹配")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, err
	}
	if time.Now().Before(leaf.NotBefore) || time.Now().After(leaf.NotAfter) {
		return nil, errors.New("证书不在有效期内")
	}
	cfg, err := runtimeconfig.Load(e.panelConfigPath())
	if err != nil {
		return nil, err
	}
	host := cfg.AllowedHost
	if host == "" {
		host = cfg.PublicHost
	}
	if host != "" {
		if err = leaf.VerifyHostname(host); err != nil {
			return nil, errors.New("证书未覆盖面板访问主机名")
		}
	}
	path := e.panelCertificatePath()
	old, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err = writeOwnedAtomic(path, append(append(cert, '\n'), key...), 0600); err != nil {
		return nil, err
	}
	renewal := filepath.Join(e.Root, "panel-acme", "renewal.enc")
	if err = os.Remove(renewal); err != nil && !os.IsNotExist(err) {
		_ = writeOwnedAtomic(path, old, 0600)
		return nil, errors.New("无法停止旧 ACME 续期，已恢复原证书")
	}
	return model.Config{"expires": leaf.NotAfter.UTC().Format(time.RFC3339), "autoRenew": false}, nil
}
