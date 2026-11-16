package executor

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func (e *Engine) phpSession(ctx context.Context, r model.Resource, p model.Config, log func(string)) (model.Config, error) {
	if r.Config.String("software") != "php" {
		return nil, errors.New("仅支持 PHP 会话管理")
	}
	base := filepath.Join(e.Root, "instances", r.ID)
	unit := serviceName(r)
	subject := r
	if id := p.String("siteId"); id != "" {
		site, err := e.load(id)
		if err != nil || site.Kind != "site" || site.Config.String("phpInstanceId") != r.ID {
			return nil, errors.New("站点不属于此 PHP 环境")
		}
		if (siteUnavailable(site) || site.Config.Bool("phpPaused")) && p.String("operation") == "save" {
			return nil, errors.New("站点已停止，请先恢复")
		}
		base = filepath.Join(e.Root, "sites", site.ID)
		unit = phpSiteUnit(site)
		subject = site
	}
	if r.Config.Bool("servicesPaused") && p.String("operation") == "save" {
		return nil, errors.New("PHP 服务已暂停，请先启动")
	}
	path := base + "/conf/php.ini"
	if err := regularManagedFile(path, base+"/conf"); err != nil {
		return nil, err
	}
	old, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	revision := security.Hash(string(old))
	sessions := base + "/sessions"
	switch p.String("operation") {
	case "", "inspect":
		expired, er := phpSessionFiles(base, false)
		if er != nil {
			return nil, er
		}
		return model.Config{"handler": phpIniValue(string(old), "session.save_handler", "files"), "revision": revision, "expiredFiles": expired, "directory": sessions, "instanceId": subject.Config.String("sessionRedisInstanceId")}, nil
	case "clean":
		if !p.Bool("confirmed") {
			return nil, errors.New("清理过期会话需要确认")
		}
		count, er := phpSessionFiles(base, true)
		if er != nil {
			return nil, er
		}
		return model.Config{"removed": count}, nil
	case "save":
	default:
		return nil, errors.New("会话操作无效")
	}
	if p.String("baseRevision") != revision {
		return nil, errors.New("配置已变更，请刷新")
	}
	if !p.Bool("confirmed") {
		return nil, errors.New("切换可能使现有会话失效，需要确认")
	}
	handler := p.String("handler")
	savePath := sessions
	switch handler {
	case "files":
		if _, err = safePath(base, "sessions"); err != nil {
			return nil, err
		}
		if err = os.MkdirAll(sessions, 0700); err != nil {
			return nil, err
		}
		user := "ov_" + r.ID[:min(12, len(r.ID))]
		if id := p.String("siteId"); id != "" {
			user = "ovs_" + id[:min(12, len(id))]
		}
		if err = run(ctx, func(string) {}, "", nil, nil, "chown", user+":"+user, sessions); err != nil {
			return nil, err
		}
	case "redis":
		inst, er := e.load(p.String("instanceId"))
		if er != nil || inst.Config.String("software") != "redis" || inst.Config.String("mode") != "native" {
			return nil, errors.New("请选择受管原生 Redis 实例")
		}
		if _, er = e.redisCommand(ctx, inst, "PING"); er != nil {
			return nil, er
		}
		savePath = fmt.Sprintf("tcp://127.0.0.1:%d?auth=%s", inst.Config.Int("port"), url.QueryEscape(inst.Config.String("password")))
	case "memcached":
		return nil, errors.New("尚无经过验证的受管 memcached 实例，请先安装兼容应用配方")
	default:
		return nil, errors.New("会话处理器无效")
	}
	runtime := filepath.Join(e.Root, "instances", r.ID, "runtime")
	if handler != "files" {
		script := fmt.Sprintf(`exit(extension_loaded(%q)?0:1);`, handler)
		if _, err = output(ctx, runtime+"/bin/php", "-c", path, "-r", script); err != nil {
			return nil, errors.New("请先安装并启用对应会话扩展")
		}
	}
	re := regexp.MustCompile(`(?m)^\s*session\.(?:save_handler|save_path)\s*=[^\r\n]*`)
	next := re.ReplaceAllString(string(old), "") + "\n[Session]\nsession.save_handler = " + handler + "\nsession.save_path = \"" + savePath + "\"\n"
	history := base + "/history/main/" + revision + ".enc"
	if err = security.WriteAtomic(history, []byte(e.Vault.Encrypt(old)), 0600); err != nil {
		return nil, err
	}
	st, _ := os.Stat(path)
	if err = writeOwnedAtomic(path, []byte(next), 0600); err != nil {
		return nil, err
	}
	if err = systemctl(ctx, log, "reload", unit); err != nil {
		_ = writeOwnedAtomic(path, old, st.Mode().Perm())
		_ = systemctl(context.Background(), func(string) {}, "reload", unit)
		return nil, errors.New("会话配置应用失败，已恢复原文件")
	}
	if handler == "redis" {
		subject.Config["sessionRedisInstanceId"] = p.String("instanceId")
	} else {
		delete(subject.Config, "sessionRedisInstanceId")
	}
	if err = e.save(subject); err != nil {
		_ = writeOwnedAtomic(path, old, st.Mode().Perm())
		_ = systemctl(context.Background(), func(string) {}, "reload", unit)
		return nil, errors.New("会话依赖登记失败，已恢复原配置")
	}
	return model.Config{"handler": handler, "configurationRevision": security.Hash(next), "linkedResources": []model.Resource{subject}}, nil
}

// Parse only scalar managed ini values; commented assignments do not affect the result.
func phpIniValue(text, key, fallback string) string {
	re := regexp.MustCompile(`(?m)^[\t ]*` + regexp.QuoteMeta(key) + `[\t ]*=[\t ]*([^\r\n]*)`)
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		value := strings.TrimSpace(m[1])
		if strings.HasPrefix(value, `"`) || strings.HasPrefix(value, `'`) {
			if end := strings.IndexByte(value[1:], value[0]); end >= 0 {
				fallback = value[1 : end+1]
			}
		} else {
			value, _, _ = strings.Cut(value, ";")
			fallback = strings.TrimSpace(value)
		}
	}
	return fallback
}

func sitePHPIni(text, base string) string {
	set := func(key, value string) {
		re := regexp.MustCompile(`(?m)^[\t ]*` + regexp.QuoteMeta(key) + `[\t ]*=[^\r\n]*`)
		line := key + " = " + strconv.Quote(value)
		if re.MatchString(text) {
			text = re.ReplaceAllStringFunc(text, func(string) string { return line })
		} else {
			text += "\n[PHP]\n" + line + "\n"
		}
	}
	set("error_log", base+"/logs/php-error.log")
	if phpIniValue(text, "session.save_handler", "files") == "files" {
		set("session.save_path", base+"/sessions")
	}
	return text
}

// Anchor cleanup to an opened directory so renames cannot redirect privileged deletion.
func phpSessionFiles(base string, remove bool) (int, error) {
	path, err := safePath(base, "sessions")
	if err != nil {
		return 0, err
	}
	root, err := os.OpenRoot(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return 0, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, item := range entries {
		st, er := root.Lstat(item.Name())
		if er != nil || !st.Mode().IsRegular() || !strings.HasPrefix(item.Name(), "sess_") || time.Since(st.ModTime()) <= 24*time.Hour {
			continue
		}
		if remove {
			if er = root.Remove(item.Name()); er != nil {
				return count, er
			}
		}
		count++
	}
	return count, nil
}
