package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gopkg.in/yaml.v3"
	"opsview/internal/logic"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func (e *Engine) app(ctx context.Context, op model.Operation, log func(string)) (result model.Config, retErr error) {
	r := op.Resource
	action := strings.TrimPrefix(op.Name, "app.")
	if action == "apply" || action == "start" || action == "restart" {
		linked, available := false, false
		for _, site := range e.states() {
			if site.Kind == "site" && site.Config.String("appId") == r.ID {
				linked = true
				available = available || !siteUnavailable(site)
			}
		}
		if linked && !available {
			return nil, errors.New("关联网站已停止或到期，请先恢复网站")
		}
	}
	base := filepath.Join(e.Root, "apps", r.ID)
	if r.Kind == "site-process" {
		base = r.Config.String("workingDirectory")
		if err := logic.ValidateSiteRoot(base); err != nil {
			return nil, err
		}
		if _, err := safePath("/", strings.TrimPrefix(base, "/")); err != nil {
			return nil, err
		}
	}
	if action == "apply" {
		u, err := user.Lookup(r.Config.String("user"))
		if err != nil || u.Uid == "0" || u.Username == "opsview" {
			return nil, errors.New("运行用户不存在或为 root/面板账户")
		}
		if err = os.MkdirAll(base, 0755); err != nil {
			return nil, err
		}
		exe := r.Config.String("executable")
		st, err := os.Stat(exe)
		if err != nil || !st.Mode().IsRegular() || st.Mode()&0111 == 0 {
			return nil, errors.New("启动程序不存在或不可执行，请先上传构建产物或安装运行时")
		}
		args := []string{}
		if s := r.Config.String("arguments"); s != "" {
			if err = json.Unmarshal([]byte(s), &args); err != nil {
				return nil, errors.New("启动参数须为 JSON 字符串数组，例如 [\"server.js\"]")
			}
		}
		parts := []string{unitQuote(exe)}
		for _, s := range args {
			if strings.ContainsAny(s, "\x00\r\n") {
				return nil, errors.New("参数包含无效字符")
			}
			parts = append(parts, unitQuote(s))
		}
		env := map[string]string{}
		if s := r.Config.String("environment"); s != "" {
			if err = json.Unmarshal([]byte(s), &env); err != nil {
				return nil, errors.New("环境变量须为 JSON 对象")
			}
		}
		var b strings.Builder
		keyPattern := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
		for k, v := range env {
			if !keyPattern.MatchString(k) || strings.ContainsAny(v, "\x00\r\n") {
				return nil, errors.New("环境变量无效")
			}
			b.WriteString(k + "=" + strconv.Quote(v) + "\n")
		}
		envPath := filepath.Join(e.Root, "state", r.ID+".env")
		unitPath := filepath.Join("/etc/systemd/system", serviceName(r))
		oldEnv, envErr := os.ReadFile(envPath)
		oldUnit, unitErr := os.ReadFile(unitPath)
		if envErr != nil && !os.IsNotExist(envErr) {
			return nil, envErr
		}
		if unitErr != nil && !os.IsNotExist(unitErr) {
			return nil, unitErr
		}
		defer func() {
			if retErr == nil {
				return
			}
			rollbackCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if envErr == nil {
				_ = security.WriteAtomic(envPath, oldEnv, 0600)
			} else {
				_ = os.Remove(envPath)
			}
			if unitErr == nil {
				_ = security.WriteAtomic(unitPath, oldUnit, 0644)
				_ = run(rollbackCtx, log, "", nil, nil, "systemctl", "daemon-reload")
				_ = systemctl(rollbackCtx, log, "restart", serviceName(r))
				log("应用更新失败，已尝试恢复上一版服务配置")
			} else {
				_ = systemctl(rollbackCtx, log, "stop", serviceName(r))
				_ = systemctl(rollbackCtx, log, "disable", serviceName(r))
				_ = os.Remove(unitPath)
				_ = run(rollbackCtx, log, "", nil, nil, "systemctl", "daemon-reload")
			}
		}()

		if err = security.WriteAtomic(envPath, []byte(b.String()), 0600); err != nil {
			return nil, err
		}
		body := fmt.Sprintf("User=%s\nWorkingDirectory=%s\nEnvironmentFile=%s\nExecStart=%s\nNoNewPrivileges=true\nPrivateTmp=true\nProtectSystem=strict\nProtectHome=true\nReadWritePaths=%s\n", r.Config.String("user"), base, envPath, strings.Join(parts, " "), base)
		if err = run(ctx, log, "", nil, nil, "chown", "--no-dereference", r.Config.String("user"), base); err != nil {
			return nil, err
		}
		if err = installUnit(ctx, r, body, log); err != nil {
			return nil, err
		}
		if r.Config.Int("port") > 0 {
			if err = waitPort(ctx, r.Config.Int("port")); err != nil {
				return nil, err
			}
		}
		if health := r.Config.String("healthURL"); health != "" {
			if err = healthCheck(ctx, health); err != nil {
				return nil, err
			}
		}
		r.Config["workingDirectory"] = base
		r.Status = "ready"
		return model.Config{"workingDirectory": base}, e.save(r)
	}
	stored, err := e.load(r.ID)
	if err != nil {
		return nil, err
	}
	r = stored
	if action == "logs" {
		s, err := output(ctx, "journalctl", "-u", serviceName(r), "-n", "200", "--no-pager")
		return model.Config{"content": s}, err
	}
	if action == "delete" {
		if err = systemctl(ctx, log, "stop", serviceName(r)); err != nil {
			return nil, err
		}
		_ = systemctl(ctx, log, "disable", serviceName(r))
		if err = os.Remove(filepath.Join("/etc/systemd/system", serviceName(r))); err != nil {
			return nil, err
		}
		if err = run(ctx, log, "", nil, nil, "systemctl", "daemon-reload"); err != nil {
			return nil, err
		}
		log("应用已移除，工作目录保留")
		return model.Config{}, e.forget(r.ID)
	}
	if action != "start" && action != "stop" && action != "restart" {
		return nil, errors.New("应用操作无效")
	}
	return model.Config{}, systemctl(ctx, log, action, serviceName(r))
}
func unitQuote(s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	s = strings.ReplaceAll(s, "$", "$$")
	return strconv.Quote(s)
}
func (e *Engine) compose(ctx context.Context, op model.Operation, log func(string)) (result model.Config, retErr error) {
	r := op.Resource
	action := strings.TrimPrefix(op.Name, "compose.")
	dir := filepath.Join(e.Root, "compose", r.ID)
	if action == "apply" {
		text := r.Config.String("yaml")
		if err := validateCompose(text, dir); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}

		old, readErr := os.ReadFile(dir + "/compose.yaml")
		if readErr != nil && !os.IsNotExist(readErr) {
			return nil, readErr
		}
		defer func() {
			if retErr == nil {
				return
			}
			if readErr == nil {
				_ = security.WriteAtomic(dir+"/compose.yaml", old, 0600)
				recovery, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				_ = run(recovery, log, dir, nil, nil, "docker", "compose", "-p", "opsview-"+r.ID, "up", "-d", "--remove-orphans")
				log("Compose 更新失败，已尝试恢复原项目配置")
			} else {
				_ = os.Remove(dir + "/compose.yaml")
			}
		}()
		if err := security.WriteAtomic(dir+"/compose.yaml", []byte(text), 0600); err != nil {
			return nil, err
		}
		if err := run(ctx, log, dir, nil, nil, "docker", "compose", "-p", "opsview-"+r.ID, "config", "--quiet"); err != nil {
			return nil, err
		}
		if err := run(ctx, log, dir, nil, nil, "docker", "compose", "-p", "opsview-"+r.ID, "up", "-d", "--remove-orphans"); err != nil {
			return nil, err
		}
		r.Status = "ready"
		return model.Config{}, e.save(r)
	}
	if _, err := e.load(r.ID); err != nil {
		return nil, err
	}
	args := []string{"compose", "-p", "opsview-" + r.ID}
	switch action {
	case "start":
		args = append(args, "start")
	case "stop":
		args = append(args, "stop")
	case "delete":
		args = append(args, "down")
	case "logs":
		text, err := output(ctx, "docker", "compose", "--project-directory", dir, "-p", "opsview-"+r.ID, "logs", "--tail", "200", "--no-color")
		return model.Config{"content": text}, err
	default:
		return nil, errors.New("Compose 操作无效")
	}
	if err := run(ctx, log, dir, nil, nil, "docker", args...); err != nil {
		return nil, err
	}
	if action == "delete" {
		log("项目已停止并移除容器，卷和目录保留")
		return model.Config{}, e.forget(r.ID)
	}
	return model.Config{}, nil
}
func validateCompose(text, dir string) error {
	if len(text) > 1<<20 || strings.Contains(text, "${") || strings.Contains(text, "$$") {
		return errors.New("Compose 过大或包含环境变量插值；请使用明确配置")
	}
	var root map[string]any
	d := yaml.NewDecoder(strings.NewReader(text))
	if err := d.Decode(&root); err != nil {
		return err
	}
	allowedRoot := map[string]bool{"name": true, "services": true, "volumes": true, "networks": true}
	for k := range root {
		if !allowedRoot[k] {
			return fmt.Errorf("不支持 Compose 字段 %s", k)
		}
	}
	services, ok := root["services"].(map[string]any)
	if !ok || len(services) == 0 {
		return errors.New("Compose 必须包含 services")
	}
	allowed := map[string]bool{"image": true, "command": true, "entrypoint": true, "environment": true, "ports": true, "volumes": true, "restart": true, "healthcheck": true, "depends_on": true, "networks": true, "labels": true, "user": true, "working_dir": true, "read_only": true, "tmpfs": true, "mem_limit": true, "cpus": true, "logging": true, "security_opt": true, "cap_drop": true}
	for name, v := range services {
		if !logic.Name.MatchString(name) {
			return errors.New("Compose 服务名无效")
		}
		s, ok := v.(map[string]any)
		if !ok {
			return errors.New("Compose 服务无效")
		}
		for k := range s {
			if !allowed[k] {
				return fmt.Errorf("禁止 Compose 服务字段 %s", k)
			}
		}
		image, _ := s["image"].(string)
		if !strings.Contains(image, "@sha256:") || len(strings.Split(image, "@sha256:")[1]) != 64 {
			return errors.New("所有 Compose 镜像必须固定 SHA-256 摘要")
		}
		if opts, ok := s["security_opt"].([]any); ok {
			for _, v := range opts {
				if v != "no-new-privileges:true" {
					return errors.New("仅允许 no-new-privileges 安全选项")
				}
			}
		}
		if vols, ok := s["volumes"].([]any); ok {
			for _, v := range vols {
				vol, ok := v.(string)
				if !ok {
					return errors.New("首版卷挂载使用短格式")
				}
				parts := strings.Split(vol, ":")
				if len(parts) < 2 {
					return errors.New("请使用明确命名卷或项目内目录")
				}
				src := parts[0]
				if strings.HasPrefix(src, "/") || strings.HasPrefix(src, ".") {
					if filepath.IsAbs(src) {
						rel, err := filepath.Rel(dir, src)
						if err != nil {
							return err
						}
						if _, err = safePath(dir, rel); err != nil {
							return err
						}
					} else {
						if _, err := safePath(dir, src); err != nil {
							return err
						}
					}
				} else if !logic.Name.MatchString(src) {
					return errors.New("卷名无效")
				}
			}
		}
	}
	for _, kind := range []string{"volumes", "networks"} {
		if defs, ok := root[kind].(map[string]any); ok {
			for name, val := range defs {
				if !logic.Name.MatchString(name) {
					return errors.New("卷或网络名无效")
				}
				if m, ok := val.(map[string]any); ok {
					for k := range m {
						if k != "labels" {
							return fmt.Errorf("禁止 %s 配置 %s（只允许项目内默认资源）", kind, k)
						}
					}
				}
			}
		}
	}
	return nil
}
