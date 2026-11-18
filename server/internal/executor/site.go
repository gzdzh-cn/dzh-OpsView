package executor

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
	"net"
	"net/http"
	"opsview/internal/logic"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func (e *Engine) writeNginxBase(r model.Resource, base string) error {
	if err := os.MkdirAll(filepath.Join(base, "conf", "sites"), 0755); err != nil {
		return err
	}
	user := "user www-data;\n"
	if r.Config.String("mode") == "docker" {
		user = "user nginx;\n"
	}
	conf := fmt.Sprintf("%sworker_processes auto;\npid %s/logs/nginx.pid;\nerror_log %s/logs/error.log warn;\nevents { worker_connections 1024; }\nhttp {\n default_type application/octet-stream;\n types { text/html html; text/css css; application/javascript js; application/json json; image/png png; image/jpeg jpg jpeg; image/svg+xml svg; }\n sendfile on;\n server_tokens off;\n access_log %s/logs/access.log;\n include %s/conf/sites/*.conf;\n server { listen %d default_server; server_name _; return 404; }\n}\n", user, base, base, base, base, r.Config.Int("port"))
	if port := r.Config.Int("statusPort"); port > 0 {
		at := strings.LastIndex(conf, "}")
		conf = conf[:at] + " include " + base + "/conf/status.conf;\n" + conf[at:]
		if err := security.WriteAtomic(base+"/conf/status.conf", []byte(nginxStatusServer(port, "abcdef")), 0644); err != nil {
			return err
		}
	}
	return security.WriteAtomic(base+"/conf/nginx.conf", []byte(conf), 0644)
}
func (e *Engine) nginx(ctx context.Context, inst model.Resource, log func(string), args ...string) error {
	base := filepath.Join(e.Root, "instances", inst.ID)
	if inst.Config.String("mode") == "docker" {
		all := []string{"exec", "opsview-" + inst.ID, "nginx", "-c", base + "/conf/nginx.conf"}
		all = append(all, args...)
		return run(ctx, log, "", nil, nil, "docker", all...)
	}
	all := []string{"-p", base + "/runtime/", "-c", base + "/conf/nginx.conf"}
	return run(ctx, log, "", nil, nil, base+"/runtime/sbin/nginx", append(all, args...)...)
}
func (e *Engine) site(ctx context.Context, op model.Operation, log func(string)) (result model.Config, retErr error) {
	e.siteMu.Lock()
	defer e.siteMu.Unlock()
	r := op.Resource
	inst, err := e.load(r.Config.String("instanceId"))
	if err != nil {
		return nil, err
	}
	if inst.Config.String("software") != "nginx" {
		return nil, errors.New("关联实例必须为 Nginx")
	}
	base := filepath.Join(e.Root, "sites", r.ID)
	confpath := filepath.Join(e.Root, "instances", inst.ID, "conf", "sites", r.ID+".conf")
	action := strings.TrimPrefix(op.Name, "site.")
	if action == "security" {
		return e.siteSecurity(r)
	}
	if action == "traffic" {
		return e.siteTraffic(r)
	}
	if stored, er := e.load(r.ID); er == nil {
		r.Config["documentRoot"] = siteDocumentRoot(stored, base)
	}
	if action == "resume" || action == "stop" {
		stored, er := e.load(r.ID)
		if er != nil {
			return nil, er
		}
		r = stored
		if action == "resume" {
			if siteExpired(r, time.Now()) {
				return nil, errors.New("请先延长到期时间")
			}
			r.Config["expirySuspended"] = false
			r.Config["disabled"] = false
		} else {
			r.Config["disabled"] = true
		}
		action = "apply"
	}
	if r.Config.String("type") == "app" && r.Config.String("upstream") == "" {
		app, er := e.load(r.Config.String("appId"))
		if er != nil || app.Kind != "app" {
			return nil, errors.New("关联应用不存在")
		}
		r.Config["upstream"] = "http://127.0.0.1:" + strconv.Itoa(app.Config.Int("port"))
	}

	if action == "configuration" {
		current, err := os.ReadFile(confpath)
		if err != nil {
			return nil, err
		}
		stored, err := e.load(r.ID)
		if err != nil {
			return nil, err
		}
		paths, _ := filepath.Glob(filepath.Join(filepath.Dir(confpath), "history", r.ID+".conf.*"))
		history := []model.Config{}
		for i := len(paths) - 1; i >= 0 && len(history) < 10; i-- {
			b, err := os.ReadFile(paths[i])
			if err == nil {
				history = append(history, model.Config{"revision": filepath.Base(paths[i]), "content": string(b)})
			}
		}
		return model.Config{"current": string(current), "managed": stored.Config.String("renderedConfig"), "drift": stored.Config.String("configHash") != security.Hash(string(current)), "history": history}, nil
	}
	if action == "process-logs" {
		stored, er := e.load(r.ID)
		if er != nil {
			return nil, er
		}
		if stored.Config.String("type") != "managed" {
			return nil, errors.New("此站点未托管应用进程")
		}
		proc := managedSiteProcess(stored)
		text, er := output(ctx, "journalctl", "-u", serviceName(proc), "-n", "200", "--no-pager")
		return model.Config{"content": text}, er
	}
	if action == "logs" {
		text, err := tail(filepath.Join(e.Root, "sites", r.ID, "logs", "access.json"), 200)
		return model.Config{"content": text}, err
	}
	if action == "delete" {
		stored, err := e.load(r.ID)
		if err != nil {
			return nil, err
		}
		old, err := os.ReadFile(confpath)
		if err != nil {
			return nil, err
		}
		if err = os.Remove(confpath); err != nil {
			return nil, err
		}
		if err = e.nginx(ctx, inst, log, "-t"); err == nil {
			err = e.nginx(ctx, inst, log, "-s", "reload")
		}
		if err != nil {
			_ = security.WriteAtomic(confpath, old, 0644)
			_ = e.nginx(ctx, inst, log, "-s", "reload")
			return nil, err
		}
		_ = stored
		e.stopSiteProcesses(ctx, stored)
		if stored.Config.String("type") == "managed" {
			proc := managedSiteProcess(stored)
			if _, err = e.app(ctx, model.Operation{Name: "app.delete", Resource: proc, Params: model.Config{}}, log); err != nil {
				return nil, err
			}
		}
		_ = os.Remove(filepath.Join(e.Root, "state", r.ID+".document-root"))
		log("站点配置已移除，站点文件仍保留")
		return model.Config{}, e.forget(r.ID)
	}
	if action != "apply" && action != "renew" && action != "certificate" {
		return nil, errors.New("网站操作无效")
	}

	if inst.Config.String("mode") == "docker" && r.Config.String("rootDirectory") != "" {
		return nil, errors.New("历史容器 Nginx 未挂载 /www/wwwroot，请使用原生 Nginx 创建新站点")
	}
	if err = e.ensureNginxObservability(inst); err != nil {
		return nil, err
	}
	for _, other := range e.states() {
		if other.Kind != "site" || other.ID == r.ID || other.Config.String("instanceId") != inst.ID {
			continue
		}
		for _, domain := range strings.Fields(r.Config.String("domains")) {
			for _, existing := range strings.Fields(other.Config.String("domains")) {
				if strings.EqualFold(domain, existing) {
					return nil, errors.New("域名已被同一入口中的另一个站点绑定")
				}
			}
		}
	}
	if err = os.MkdirAll(base+"/logs", 0755); err != nil {
		return nil, err
	}
	if err = os.Chmod(base, 0711); err != nil {
		return nil, err
	}
	if err = e.prepareSiteRoot(&r, base); err != nil {
		return nil, err
	}
	if err = prepareSiteRunningDirectory(siteDocumentRoot(r, base), r.Config.String("runningDirectory")); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(base+"/acme/.well-known/acme-challenge", 0755); err != nil {
		return nil, err
	}
	for _, p := range []string{"acme", "acme/.well-known", "acme/.well-known/acme-challenge"} {
		if err = os.Chmod(filepath.Join(base, p), 0755); err != nil {
			return nil, err
		}
	}
	if err = os.MkdirAll(base+"/cert", 0700); err != nil {
		return nil, err
	}
	if _, err = os.Stat(siteDocumentRoot(r, base) + "/index.html"); os.IsNotExist(err) {
		_ = security.WriteAtomic(siteDocumentRoot(r, base)+"/index.html", []byte("<!doctype html><html lang=zh-CN><meta charset=utf-8><title>站点已就绪</title><h1>站点已就绪</h1><p>由 OpsView 管理。</p></html>"), 0644)
	}
	old, readErr := os.ReadFile(confpath)
	if readErr != nil && !os.IsNotExist(readErr) {
		return nil, readErr
	}

	previousConfigPath := ""
	if stored, err := e.load(r.ID); err == nil {
		r.Config["phpPaused"] = stored.Config.Bool("phpPaused")
		if op.Name != "site.resume" && stored.Config.Bool("expirySuspended") {
			r.Config["expirySuspended"] = true
		}
		driftContent := old
		if stored.Config.String("instanceId") != inst.ID {
			previous, err := e.load(stored.Config.String("instanceId"))
			if err != nil {
				return nil, err
			}
			if previous.Status != "stopped" {
				return nil, errors.New("切换站点入口前请先停止原 Nginx 实例")
			}
			previousConfigPath = filepath.Join(e.Root, "instances", previous.ID, "conf", "sites", r.ID+".conf")
			driftContent, err = os.ReadFile(previousConfigPath)
			if err != nil {
				return nil, err
			}
		}
		if expected := stored.Config.String("configHash"); expected != "" && expected != security.Hash(string(driftContent)) && !op.Params.Bool("acceptDrift") {
			return nil, errors.New("检测到外部配置修改，请检查实际配置并确认接受差异后重试")
		}
		if stored.Config.Bool("tls") {
			r.Config["tls"] = true
		}
	}

	if action == "certificate" || action == "renew" {
		oldCert, certErr := os.ReadFile(base + "/cert/fullchain.pem")
		oldKey, keyErr := os.ReadFile(base + "/cert/key.pem")
		if certErr != nil && !os.IsNotExist(certErr) {
			return nil, certErr
		}
		if keyErr != nil && !os.IsNotExist(keyErr) {
			return nil, keyErr
		}
		defer func() {
			if retErr == nil {
				return
			}
			if certErr == nil {
				_ = security.WriteAtomic(base+"/cert/fullchain.pem", oldCert, 0644)
			} else {
				_ = os.Remove(base + "/cert/fullchain.pem")
			}
			if keyErr == nil {
				_ = security.WriteAtomic(base+"/cert/key.pem", oldKey, 0600)
			} else {
				_ = os.Remove(base + "/cert/key.pem")
			}
			if len(old) > 0 {
				_ = security.WriteAtomic(confpath, old, 0644)
			} else {
				_ = os.Remove(confpath)
			}
			recovery, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_ = e.nginx(recovery, inst, log, "-s", "reload")
		}()
	}
	if action == "certificate" {
		crt := []byte(op.Params.String("certificate"))
		key := []byte(op.Params.String("privateKey"))
		pair, err := tls.X509KeyPair(crt, key)
		if err != nil {
			return nil, errors.New("证书与私钥不匹配")
		}
		leaf, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return nil, err
		}
		for _, d := range strings.Fields(r.Config.String("domains")) {
			if err = leaf.VerifyHostname(d); err != nil {
				return nil, errors.New("证书未覆盖站点域名")
			}
		}
		if err = security.WriteAtomic(base+"/cert/fullchain.pem", crt, 0644); err != nil {
			return nil, err
		}
		if err = security.WriteAtomic(base+"/cert/key.pem", key, 0600); err != nil {
			return nil, err
		}
		r.Config["tls"] = true
		r.Config["certificateProvider"] = "uploaded"
		r.Config["certificateExpires"] = leaf.NotAfter.UTC().Format(time.RFC3339)
	}
	// HTTP validation is served by Nginx; certificate issuance never binds the panel port.
	if action == "renew" {
		if inst.Config.Int("port") != 80 {
			return nil, errors.New("ACME HTTP-01 需要站点入口监听 80 端口")
		}
		r.Config["probeToken"] = security.ID()
		candidate := e.renderSite(r, inst, base)
		if err = e.applyNginx(ctx, inst, confpath, []byte(candidate), old, log); err != nil {
			return nil, err
		}
		if err = e.issueCertificate(ctx, r, base); err != nil {
			_ = security.WriteAtomic(confpath, old, 0644)
			_ = e.nginx(ctx, inst, log, "-s", "reload")
			return nil, err
		}
		r.Config["tls"] = true
		r.Config["certificateProvider"] = "acme"
	}
	if r.Config.String("type") == "php" {
		restore, snapshotErr := e.snapshotSitePHP(ctx, r, base)
		if snapshotErr != nil {
			return nil, snapshotErr
		}
		defer func() {
			if retErr != nil {
				if rollbackErr := restore(); rollbackErr != nil {
					retErr = fmt.Errorf("网站应用失败且 PHP 回退未完成，请检查服务日志：%w", retErr)
				}
			}
		}()
		if err = e.setupSitePHP(ctx, r, base, log); err != nil {
			return nil, err
		}
	}
	if r.Config.String("type") == "managed" && !siteUnavailable(r) && (action == "apply" || action == "certificate") {
		restore, processErr := e.setupManagedSite(ctx, r, log)
		if processErr != nil {
			return nil, processErr
		}
		defer func() {
			if retErr != nil {
				if restoreErr := restore(); restoreErr != nil {
					retErr = fmt.Errorf("网站应用失败且进程回退未完成：%w", retErr)
				}
			}
		}()
	}
	r.Config["probeToken"] = security.ID()
	candidate := []byte(e.renderSite(r, inst, base))
	if err = e.applyNginx(ctx, inst, confpath, candidate, old, log); err != nil {
		return nil, err
	}

	if previousConfigPath != "" {
		original, err := os.ReadFile(previousConfigPath)
		if err != nil {
			return nil, err
		}
		history := filepath.Join(filepath.Dir(previousConfigPath), "history")
		if err = os.MkdirAll(history, 0700); err != nil {
			return nil, err
		}
		if err = security.WriteAtomic(filepath.Join(history, r.ID+".conf."+strconv.FormatInt(time.Now().UnixNano(), 10)), original, 0600); err != nil {
			return nil, err
		}
		if err = os.Remove(previousConfigPath); err != nil {
			return nil, err
		}
	}
	r.Config["renderedConfig"] = string(candidate)
	r.Config["configHash"] = security.Hash(string(candidate))
	r.Config["documentRoot"] = siteDocumentRoot(r, base)
	r.Config["runningRoot"] = siteRunningRoot(r, base)
	r.Status = "ready"
	if siteUnavailable(r) {
		r.Status = "stopped"
		if siteExpired(r, time.Now()) {
			r.Status = "expired"
		}
	}
	if r.Config.Bool("tls") {
		b, _ := os.ReadFile(base + "/cert/fullchain.pem")
		if block, _ := pem.Decode(b); block != nil {
			if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
				r.Config["certificateExpires"] = cert.NotAfter.UTC().Format(time.RFC3339)
				r.Config["certificateNotBefore"] = cert.NotBefore.UTC().Format(time.RFC3339)
			}
		}
	}
	if err = e.save(r); err != nil {
		return nil, err
	}
	if siteUnavailable(r) {
		e.stopSiteProcesses(ctx, r)
	}
	return model.Config{"configHash": r.Config["configHash"], "documentRoot": siteDocumentRoot(r, base), "runningRoot": r.Config["runningRoot"], "tls": r.Config.Bool("tls"), "certificateExpires": r.Config["certificateExpires"], "certificateNotBefore": r.Config["certificateNotBefore"], "certificateProvider": r.Config["certificateProvider"], "expirySuspended": r.Config["expirySuspended"], "disabled": r.Config["disabled"], "runtimeStatus": r.Status}, nil
}
func (e *Engine) renderSite(r, inst model.Resource, base string) string {
	domains := strings.Join(strings.Fields(r.Config.String("domains")), " ")
	conf := fmt.Sprintf("server {\n listen %d;\n server_name %s;\n location ^~ /.well-known/acme-challenge/ { root %s/acme; }\n", inst.Config.Int("port"), domains, base)

	conf += " access_log " + base + "/logs/access.json opsview_json;\n error_log " + base + "/logs/error.log warn;\n"
	conf += r.Config.String("advanced") + "\n"
	conf += siteSecurityConfig(r)
	if token := r.Config.String("probeToken"); token != "" {
		conf += fmt.Sprintf(" location = /.opsview-health-%s { add_header X-OpsView-Revision %s always; return 204; }\n", token, token)
	}
	if r.Config.Bool("tls") {
		conf += fmt.Sprintf(" listen 443 ssl;\n ssl_certificate %s/cert/fullchain.pem;\n ssl_certificate_key %s/cert/key.pem;\n ssl_protocols TLSv1.2 TLSv1.3;\n", base, base)

	}

	redirect := ""
	if r.Config.Bool("tls") && r.Config.Bool("redirectHTTPS") {
		redirect = "  if ($scheme = http) { return 301 https://$host$request_uri; }\n"
	}
	if siteUnavailable(r) {
		return conf + " location / { return 403; }\n}\n"
	}
	if r.Config.String("type") == "php" {
		conf += " root " + siteRunningRoot(r, base) + ";\n index " + siteIndex(r) + ";\n location / { " + redirect + siteTryFiles(r) + " }\n location ~ \\.php$ { " + redirect + "try_files $uri =404; fastcgi_pass unix:" + base + "/run/php.sock; fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name; include " + filepath.Join(e.Root, "instances", inst.ID, "runtime", "conf", "fastcgi_params") + "; }\n"
		return conf + "}\n"
	}
	if r.Config.String("type") == "proxy" || r.Config.String("type") == "app" || r.Config.String("type") == "managed" {
		if routes, _ := logic.SiteProxyRoutes(r.Config); len(routes) > 0 {
			upstreams, locations := renderProxyRoutes(r, redirect)
			return upstreams + conf + locations + "}\n"
		}
		conf += " location / {\n" + redirect + "  proxy_pass " + siteUpstream(r) + ";\n  proxy_http_version 1.1;\n  proxy_set_header Host " + siteProxyHost(r) + ";\n  proxy_set_header Upgrade $http_upgrade;\n  proxy_set_header Connection $opsview_connection;\n  proxy_set_header X-Real-IP $remote_addr;\n  proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n  proxy_set_header X-Forwarded-Proto $scheme;\n }\n"
	} else {
		conf += " root " + siteRunningRoot(r, base) + ";\n index " + siteIndex(r) + ";\n location / {\n" + redirect + " " + siteTryFiles(r) + " }\n"
	}
	return conf + "}\n"
}
func (e *Engine) applyNginx(ctx context.Context, inst model.Resource, path string, next, old []byte, log func(string)) error {
	if len(old) > 0 {
		history := filepath.Join(filepath.Dir(path), "history")
		if err := os.MkdirAll(history, 0700); err != nil {
			return err
		}
		if err := security.WriteAtomic(filepath.Join(history, filepath.Base(path)+"."+strconv.FormatInt(time.Now().UnixNano(), 10)), old, 0600); err != nil {
			return err
		}
	}
	if err := security.WriteAtomic(path, next, 0644); err != nil {
		return err
	}
	rollback := func() {
		if len(old) == 0 {
			_ = os.Remove(path)
		} else {
			_ = security.WriteAtomic(path, old, 0644)
		}
		_ = e.nginx(ctx, inst, log, "-s", "reload")
	}
	if err := e.nginx(ctx, inst, log, "-t"); err != nil {
		rollback()
		return err
	}
	if err := e.nginx(ctx, inst, log, "-s", "reload"); err != nil {
		rollback()
		return err
	}
	if err := probeNginx(ctx, inst, next); err != nil {
		rollback()
		return err
	}
	return nil
}

// Probe the generated configuration revision, so old workers cannot hide a failed reload.
func probeNginx(ctx context.Context, inst model.Resource, config []byte) error {
	marker := regexp.MustCompile(`location = /\.opsview-health-([a-f0-9]+)`).FindSubmatch(config)
	domains := regexp.MustCompile(`server_name ([^;]+);`).FindSubmatch(config)
	if len(marker) < 2 || len(domains) < 2 {
		return errors.New("候选配置缺少健康检查标记")
	}
	domain := strings.Fields(string(domains[1]))[0]
	port, scheme := inst.Config.Int("port"), "http"
	if strings.Contains(string(config), "listen 443 ssl;") {
		port, scheme = 443, "https"
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	deadline, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for {
		request, err := http.NewRequestWithContext(deadline, "GET", scheme+"://"+net.JoinHostPort(domain, strconv.Itoa(port))+"/.opsview-health-"+string(marker[1]), nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == 204 && response.Header.Get("X-OpsView-Revision") == string(marker[1]) {
				return nil
			}
		}
		select {
		case <-deadline.Done():
			return errors.New("Nginx 新配置健康检查失败，已尝试回退")
		case <-time.After(250 * time.Millisecond):
		}
	}
}

type acmeUser struct {
	Email        string
	Registration *registration.Resource
	Key          crypto.PrivateKey
}

func (a *acmeUser) GetEmail() string                        { return a.Email }
func (a *acmeUser) GetRegistration() *registration.Resource { return a.Registration }
func (a *acmeUser) GetPrivateKey() crypto.PrivateKey        { return a.Key }

type webroot struct{ root string }

func (w webroot) Present(domain, token, keyAuth string) error {
	if strings.ContainsAny(token, "/\\.") {
		return errors.New("ACME token 无效")
	}
	return security.WriteAtomic(filepath.Join(w.root, token), []byte(keyAuth), 0644)
}
func (w webroot) CleanUp(domain, token, keyAuth string) error {
	return os.Remove(filepath.Join(w.root, token))
}
func (e *Engine) issueCertificate(ctx context.Context, r model.Resource, base string) error {
	return e.issueCertificateProvider(ctx, r, base, webroot{base + "/acme/.well-known/acme-challenge"})
}
func (e *Engine) issueCertificateProvider(ctx context.Context, r model.Resource, base string, provider challenge.Provider) error {
	email := r.Config.String("email")
	if !strings.Contains(email, "@") || strings.ContainsAny(email, "\r\n") {
		return errors.New("请在站点配置填写有效的 ACME 联系邮箱")
	}
	if !r.Config.Bool("acceptACMETerms") {
		return errors.New("申请证书前须同意 ACME 服务条款")
	}
	keyPath := filepath.Join(e.Root, "acme-account.key")
	var key crypto.PrivateKey
	b, err := os.ReadFile(keyPath)
	if err == nil {
		block, _ := pem.Decode(b)
		if block == nil {
			return errors.New("ACME 账号密钥损坏")
		}
		key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return err
		}
	} else if os.IsNotExist(err) {
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return err
		}
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return err
		}
		if err = security.WriteAtomic(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
			return err
		}
	} else {
		return err
	}
	user := &acmeUser{Email: email, Key: key}
	regpath := filepath.Join(e.Root, "acme-registration.json")
	if b, err = os.ReadFile(regpath); err == nil {
		_ = json.Unmarshal(b, &user.Registration)
	}
	cfg := lego.NewConfig(user)
	cfg.Certificate.KeyType = certcrypto.EC256
	cfg.HTTPClient = &http.Client{Timeout: 45 * time.Second}
	client, err := lego.NewClient(cfg)
	if err != nil {
		return err
	}
	if err = client.Challenge.SetHTTP01Provider(provider); err != nil {
		return err
	}
	if user.Registration == nil {
		user.Registration, err = client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return err
		}
		b, _ = json.Marshal(user.Registration)
		if err = security.WriteAtomic(regpath, b, 0600); err != nil {
			return err
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	profile := ""
	for _, host := range strings.Fields(r.Config.String("domains")) {
		if net.ParseIP(host) != nil {
			profile = "shortlived"
		}
	}
	result, err := client.Certificate.Obtain(certificate.ObtainRequest{Domains: strings.Fields(r.Config.String("domains")), Bundle: true, Profile: profile})
	if err != nil {
		return err
	}
	if err = security.WriteAtomic(base+"/cert/fullchain.pem", result.Certificate, 0644); err != nil {
		return err
	}
	return security.WriteAtomic(base+"/cert/key.pem", result.PrivateKey, 0600)
}
