package controller

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"opsview/internal/model"
	"opsview/internal/runtimeconfig"
	"opsview/internal/security"
	"path/filepath"
	"time"
)

func (c *Controller) panelCertificate(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Certificate string `json:"certificate"`
		PrivateKey  string `json:"privateKey"`
	}
	if !decode(w, r, &b) {
		return
	}
	pair, e := tls.X509KeyPair([]byte(b.Certificate), []byte(b.PrivateKey))
	if e != nil {
		fail(w, 400, "证书和私钥不匹配或 PEM 格式错误")
		return
	}
	cert, e := x509.ParseCertificate(pair.Certificate[0])
	if e != nil || time.Now().Before(cert.NotBefore) || time.Now().After(cert.NotAfter) {
		fail(w, 400, "证书无效或已过期")
		return
	}
	if !c.App.Development {
		data, err := c.App.Executor.Execute(r.Context(), model.Operation{Name: "system.panel-certificate-upload", Params: model.Config{"certificate": b.Certificate, "privateKey": b.PrivateKey}}, func(string) {})
		if err != nil {
			fail(w, 422, err.Error())
			return
		}
		c.App.Store.Audit("settings.certificate", "", clientIP(r))
		send(w, 200, data)
		return
	}
	pemBytes := []byte(b.Certificate + "\n" + b.PrivateKey + "\n")
	if e = security.WriteAtomic(filepath.Join(c.DataDir, "panel.pem"), pemBytes, 0600); e != nil {
		fail(w, 500, "保存证书失败")
		return
	}
	c.App.Store.Audit("settings.certificate", "", clientIP(r))
	send(w, 200, map[string]any{"expires": cert.NotAfter, "development": c.App.Development})
}

func (c *Controller) issuePanelCertificate(w http.ResponseWriter, r *http.Request) {
	var p model.Config
	if !decode(w, r, &p) {
		return
	}
	if !p.Bool("acceptTerms") {
		fail(w, 400, "请明确同意 ACME 服务条款")
		return
	}
	if !runtimeconfig.ValidHost(p.String("host")) {
		fail(w, 400, "域名或公网 IP 无效")
		return
	}
	task, err := c.App.Store.Enqueue(model.Operation{Name: "system.panel-certificate", Params: p})
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	c.App.Store.Audit("settings.certificate.issue", "", clientIP(r))
	send(w, 202, task)
}
