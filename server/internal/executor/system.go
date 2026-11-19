package executor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"opsview/internal/logic"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func (e *Engine) system(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	if strings.HasPrefix(op.Name, "system.ssh-") {
		return e.sshOperation(ctx, op, log)
	}
	if strings.HasPrefix(op.Name, "system.firewall-admin-") {
		return e.firewallAdmin(ctx, op, log)
	}
	if strings.HasPrefix(op.Name, "system.firewall-") {
		return e.firewallSystem(ctx, op, log)
	}
	if strings.HasPrefix(op.Name, "system.panel-region-") {
		return e.panelRegions(ctx, op, log)
	}
	if op.Name == "system.panel-client-certificate" {
		return e.issueClientCertificate(ctx)
	}
	if op.Name == "system.panel-certificate-upload" {
		return e.uploadPanelCertificate(op.Params)
	}
	if op.Name == "system.panel-certificate" {
		return e.panelCertificate(ctx, op.Params, log)
	}
	if strings.HasPrefix(op.Name, "system.panel-") {
		return e.panelRuntime(ctx, op, log)
	}
	switch op.Name {
	case "system.inspect":
		ports, _ := output(ctx, "ss", "-tulnp")
		firewall, fwerr := output(ctx, "ufw", "status", "verbose")
		if fwerr != nil {
			firewall = "UFW 不可用或尚未安装"
		}
		dockerRules, _ := output(ctx, "iptables", "-S", "DOCKER-USER")
		services, _ := output(ctx, "systemctl", "list-units", "--type=service", "--all", "--no-pager", "--plain")
		existing := []model.Config{}
		for _, s := range []string{"nginx", "mysql", "postgresql", "docker", "node"} {
			path, err := exec.LookPath(s)
			if err == nil {
				existing = append(existing, model.Config{"name": s, "path": path, "managed": false})
			}
		}
		return model.Config{"portItems": parseListeningPorts(ports), "serviceItems": parseSystemServices(services), "osRelease": readText("/etc/os-release"), "ports": ports, "firewall": firewall, "dockerRules": dockerRules, "services": services, "existing": existing, "externalRestrictions": "云厂商安全组需要在云控制台单独检查"}, nil
	case "system.logs":
		unit := op.Params.String("unit")
		if !logic.Name.MatchString(strings.TrimSuffix(unit, ".service")) {
			return nil, errors.New("服务名无效")
		}
		text, err := output(ctx, "journalctl", "-u", unit, "-n", "200", "--no-pager")
		return model.Config{"content": text}, err
	case "system.service":
		unit := op.Params.String("unit")
		if !strings.HasPrefix(unit, "opsview-") {
			return nil, errors.New("只允许控制 OpsView 管理的服务")
		}
		action := op.Params.String("action")
		if strings.HasPrefix(unit, "opsview-app-") {
			return nil, errors.New("应用进程功能已下线，旧服务保留")
		}
		if action != "start" && action != "stop" && action != "restart" {
			return nil, errors.New("服务操作无效")
		}
		return model.Config{}, systemctl(ctx, log, action, unit)
	case "system.reconcile":
		out := []model.Resource{}
		for _, r := range e.states() {
			r.Config = security.Redact(r.Config)
			out = append(out, r)
		}
		return model.Config{"resources": out}, nil
	case "system.start-docker":
		err := systemctl(ctx, log, "start", "docker.service")
		dockerAPI.invalidate()
		return model.Config{}, err
	case "system.install-docker":
		e.mu.Lock()
		defer e.mu.Unlock()
		if err := checkOS(); err != nil {
			return nil, err
		}
		if _, err := exec.LookPath("docker"); err == nil {
			return nil, errors.New("检测到已有 Docker 安装，仅提供状态检查，不自动替换")
		}
		if err := run(ctx, log, "", []string{"DEBIAN_FRONTEND=noninteractive"}, nil, "apt-get", "update"); err != nil {
			return nil, err
		}
		if err := run(ctx, log, "", []string{"DEBIAN_FRONTEND=noninteractive"}, nil, "apt-get", "install", "-y", "ca-certificates", "curl"); err != nil {
			return nil, err
		}
		release := parseRelease(readText("/etc/os-release"))
		osID := release["ID"]
		codename := release["VERSION_CODENAME"]
		if osID != "ubuntu" && osID != "debian" || !logic.Name.MatchString(codename) {
			return nil, errors.New("系统发行标识无效")
		}
		keyURL := "https://download.docker.com/linux/" + osID + "/gpg"
		req, err := http.NewRequestWithContext(ctx, "GET", keyURL, nil)
		if err != nil {
			return nil, err
		}
		res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return nil, fmt.Errorf("Docker 签名密钥下载失败：%d", res.StatusCode)
		}
		keyPath := "/etc/apt/keyrings/opsview-docker.asc"
		if err = os.MkdirAll(filepath.Dir(keyPath), 0755); err != nil {
			return nil, err
		}
		file, err := os.OpenFile(keyPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
		if err != nil {
			return nil, err
		}
		err = security.Copy(file, res.Body, 64<<10)
		file.Close()
		if err != nil {
			return nil, err
		}
		source := fmt.Sprintf("Types: deb\nURIs: https://download.docker.com/linux/%s\nSuites: %s\nComponents: stable\nArchitectures: amd64\nSigned-By: %s\n", osID, codename, keyPath)
		if err = security.WriteAtomic("/etc/apt/sources.list.d/opsview-docker.sources", []byte(source), 0644); err != nil {
			return nil, err
		}
		if err = run(ctx, log, "", nil, nil, "apt-get", "update"); err != nil {
			return nil, err
		}
		if err = run(ctx, log, "", []string{"DEBIAN_FRONTEND=noninteractive"}, nil, "apt-get", "install", "-y", "docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-compose-plugin"); err != nil {
			return nil, err
		}
		if err = run(ctx, log, "", nil, nil, "systemctl", "enable", "--now", "docker"); err != nil {
			return nil, err
		}
		dockerAPI.invalidate()
		return model.Config{}, nil
	}
	return nil, errors.New("系统操作无效")
}
func parseRelease(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		p := strings.SplitN(line, "=", 2)
		if len(p) == 2 {
			out[p[0]] = strings.Trim(p[1], "\"'")
		}
	}
	return out
}
func healthCheck(ctx context.Context, address string) error {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil {
		return errors.New("健康检查仅支持 http://127.0.0.1:<port>/path")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 400 {
		return fmt.Errorf("应用健康检查返回 HTTP %d", res.StatusCode)
	}
	return nil
}
