package executor

import (
	"context"
	"encoding/json"
	"errors"
	"opsview/internal/logic"
	"opsview/internal/model"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func (e *Engine) siteRuntimePreview(ctx context.Context, r model.Resource) (model.Config, error) {
	e.siteMu.Lock()
	defer e.siteMu.Unlock()
	e.fileMu.Lock()
	defer e.fileMu.Unlock()
	j, hash, err := e.inspectSitePermissions(ctx, r)
	if err != nil {
		return nil, err
	}
	return model.Config{"fingerprint": hash, "path": j.Root, "entries": len(j.Entries), "changes": "递归调整所有者和用户组，限制其他用户访问，保留执行位，为 Nginx 配置读取 ACL；不跟随链接或跨越挂载点", "confirmedRequired": len(j.Entries) > 1}, nil
}
func (e *Engine) siteSandbox(ctx context.Context, r model.Resource) (string, error) {
	if !r.Config.Bool("runtimeIsolation") {
		return "", nil
	}
	if _, err := exec.LookPath("setfacl"); err != nil {
		return "", errors.New("运行隔离需要 acl 工具，请安装 acl 软件包")
	}
	out, err := output(ctx, "systemd", "--version")
	if err != nil {
		return "", errors.New("无法检测 systemd 运行隔离能力")
	}
	f := strings.Fields(out)
	if len(f) < 2 {
		return "", errors.New("systemd 版本信息无效")
	}
	v, _ := strconv.Atoi(f[1])
	if v < 249 {
		return "", errors.New("运行隔离需要 systemd 249 或更高版本")
	}
	if _, err = output(ctx, "unshare", "--mount", "--propagation", "private", "--", "/bin/true"); err != nil {
		return "", errors.New("系统禁止创建挂载命名空间，无法启用运行限制")
	}
	root := r.Config.String("workingDirectory")
	if root == "" || root == "/" {
		return "", errors.New("项目根目录无效")
	}
	// A minimal service root exposes only system runtime files and this project.
	// In particular /proc is absent: hiding it with InaccessiblePaths breaks
	// systemd's own namespace setup, while exposing it permits same-UID aliases.
	serviceRoot := filepath.Join(e.Root, "site-users", "roots", r.ID)
	if err := os.MkdirAll(serviceRoot, 0755); err != nil {
		return "", err
	}
	if err := os.Chmod(serviceRoot, 0755); err != nil {
		return "", err
	}
	for _, p := range []string{"dev", "proc", "run", "tmp", "var/tmp", "etc", "usr", "bin", "sbin", "lib", "lib64"} {
		if err := os.MkdirAll(filepath.Join(serviceRoot, p), 0755); err != nil {
			return "", err
		}
		if err := os.Chmod(filepath.Join(serviceRoot, p), 0755); err != nil {
			return "", err
		}
	}
	body := "RootDirectory=" + strings.ReplaceAll(serviceRoot, "%", "%%") + "\nMountAPIVFS=no\nPrivateDevices=true\nProtectKernelTunables=true\nProtectKernelModules=true\nProtectKernelLogs=true\nProtectControlGroups=true\nRestrictSUIDSGID=true\nLockPersonality=true\nRestrictNamespaces=true\nCapabilityBoundingSet=\nAmbientCapabilities=\nRestrictAddressFamilies=AF_UNIX AF_INET AF_INET6\nSystemCallFilter=~@mount @module @reboot @swap @debug\n"
	for _, p := range []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc/ld.so.cache", "/etc/ld.so.conf", "/etc/ld.so.conf.d", "/etc/resolv.conf", "/etc/hosts", "/etc/nsswitch.conf", "/etc/passwd", "/etc/group", "/etc/localtime", "/etc/ssl/certs", "/etc/alternatives"} {
		if _, er := os.Stat(p); er == nil {
			body += "BindReadOnlyPaths=" + unitQuote(p) + "\n"
		}
	}
	body += "BindPaths=" + unitQuote(root) + "\n"
	exe := r.Config.String("executable")
	realExe, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	if !pathContains(root, realExe) && !pathContains("/usr", realExe) && !pathContains("/lib", realExe) && !pathContains("/lib64", realExe) && !pathContains("/bin", realExe) && !pathContains("/sbin", realExe) {
		allowed := ""
		for _, s := range e.states() {
			if s.Kind == "instance" {
				runtime := filepath.Join(e.Root, "instances", s.ID, "runtime")
				if pathContains(runtime, realExe) {
					allowed = runtime
				}
			}
		}
		if allowed == "" {
			return "", errors.New("执行文件位于被隔离目录；请使用项目内程序、系统解释器或受管运行时")
		}
		body += "BindReadOnlyPaths=" + unitQuote(allowed) + "\n"
	}
	if r.Config.String("user") == "root" && r.Config.String("siteRootApproval") != logic.SiteRootDigest(model.Resource{ID: strings.TrimPrefix(r.ID, "web-"), Config: r.Config}) {
		return "", errors.New("root 项目缺少当前配置的管理员授权")
	}
	return body, nil
}

func (e *Engine) commitSitePermissions(id string) error {
	b, err := os.ReadFile(e.sitePermissionPath(id))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var j sitePermissionJournal
	if err = json.Unmarshal(b, &j); err != nil {
		return err
	}
	if j.Phase == "applying" {
		j.Phase = "committed"
		return e.writePermissionJournal(id, j)
	}
	return nil
}
