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
	"strings"
	"time"
)

func (e *Engine) phpSession(ctx context.Context, r model.Resource, p model.Config, log func(string)) (model.Config, error) {
	if r.Config.String("software") != "php" {
		return nil, errors.New("仅支持 PHP 会话管理")
	}
	base := filepath.Join(e.Root, "instances", r.ID)
	unit := serviceName(r)
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
		handler := "files"
		re := regexp.MustCompile(`(?m)^session\.save_handler\s*=\s*([a-z]+)`)
		if m := re.FindSubmatch(old); len(m) > 1 {
			handler = string(m[1])
		}
		entries, _ := os.ReadDir(sessions)
		expired := 0
		for _, item := range entries {
			if st, er := item.Info(); er == nil && st.Mode().IsRegular() && strings.HasPrefix(item.Name(), "sess_") && time.Since(st.ModTime()) > 24*time.Hour {
				expired++
			}
		}
		return model.Config{"handler": handler, "revision": revision, "expiredFiles": expired, "directory": sessions}, nil
	case "clean":
		if !p.Bool("confirmed") {
			return nil, errors.New("清理过期会话需要确认")
		}
		entries, er := os.ReadDir(sessions)
		if os.IsNotExist(er) {
			return model.Config{"removed": 0}, nil
		}
		if er != nil {
			return nil, er
		}
		count := 0
		for _, item := range entries {
			if st, er := item.Info(); er == nil && st.Mode().IsRegular() && strings.HasPrefix(item.Name(), "sess_") && time.Since(st.ModTime()) > 24*time.Hour {
				if os.Remove(filepath.Join(sessions, item.Name())) == nil {
					count++
				}
			}
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
	return model.Config{"handler": handler, "configurationRevision": security.Hash(next)}, nil
}
