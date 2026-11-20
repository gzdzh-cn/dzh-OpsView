package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"opsview/internal/catalog"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type nginxReplacement struct {
	Original model.Resource `json:"original"`
	Phase    string         `json:"phase"`
}

func (e *Engine) nginxRecovery(id string) string { return filepath.Join(e.Root, "nginx-recovery", id) }
func (e *Engine) saveNginxReplacement(id string, j nginxReplacement) error {
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	return security.WriteAtomic(filepath.Join(e.nginxRecovery(id), "journal"), []byte(e.Vault.Encrypt(b)), 0600)
}
func (e *Engine) readNginxReplacement(id string) (nginxReplacement, error) {
	var j nginxReplacement
	b, err := os.ReadFile(filepath.Join(e.nginxRecovery(id), "journal"))
	if err != nil {
		return j, err
	}
	raw, err := e.Vault.Decrypt(string(b))
	if err != nil {
		return j, err
	}
	err = json.Unmarshal(raw, &j)
	return j, err
}
func (e *Engine) restoreNginxReplacement(ctx context.Context, j nginxReplacement, log func(string)) error {
	r := j.Original
	dir := e.nginxRecovery(r.ID)
	base := filepath.Join(e.Root, "instances", r.ID)
	if _, err := os.Stat(filepath.Join(dir, "runtime")); err == nil {
		_ = systemctl(ctx, log, "stop", serviceName(r))
		failed := filepath.Join(dir, "failed-runtime")
		_ = os.RemoveAll(failed)
		if _, er := os.Stat(base + "/runtime"); er == nil {
			if er = os.Rename(base+"/runtime", failed); er != nil {
				return er
			}
		}
		if err = os.Rename(filepath.Join(dir, "runtime"), base+"/runtime"); err != nil {
			return err
		}
	}
	if err := e.save(r); err != nil {
		return err
	}
	if r.Status != "stopped" {
		if err := systemctl(ctx, log, "start", serviceName(r)); err != nil {
			return err
		}
		if err := e.softwareHealth(ctx, r); err != nil {
			return err
		}
	}
	return os.Remove(filepath.Join(dir, "journal"))
}
func (e *Engine) recoverNginxReplacements(ctx context.Context) error {
	dirs, err := os.ReadDir(filepath.Join(e.Root, "nginx-recovery"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		j, er := e.readNginxReplacement(d.Name())
		if os.IsNotExist(er) {
			continue
		}
		if er != nil {
			return er
		}
		if j.Original.ID != d.Name() {
			return errors.New("Nginx 恢复记录标识错误")
		}
		if j.Phase == "committed" {
			continue
		}
		if er = e.restoreNginxReplacement(ctx, j, func(string) {}); er != nil {
			return er
		}
	}
	return nil
}
func (e *Engine) replaceNginx(ctx context.Context, r model.Resource, p model.Config, log func(string)) (result model.Config, retErr error) {
	if r.Config.String("software") != "nginx" || r.Config.String("mode") != "native" {
		return nil, errors.New("仅支持受管原生 Nginx")
	}
	count := 0
	for _, i := range e.states() {
		if i.Kind == "instance" && i.Config.String("software") == "nginx" {
			count++
		}
	}
	if count != 1 {
		return nil, errors.New("存在历史多实例，请先合并清理后更换版本")
	}
	entry, ok := e.catalogEntry(p.String("catalogId"))
	if !ok || entry.Software != "nginx" || entry.Version == r.Config.String("version") {
		return nil, errors.New("请选择已验证的其他 Nginx 版本")
	}
	if !p.Bool("maintenanceConfirmed") {
		return nil, errors.New("请确认短暂维护窗口")
	}
	method := p.String("installMethod")
	if method == "" {
		method = "binary"
		if catalog.InstallationReason(entry, "native", method, hostSystem()) != "" {
			method = "source"
		}
	}
	if method != "binary" && method != "source" {
		return nil, errors.New("安装方式无效")
	}
	if !e.allowUnverifiedNative {
		if reason := catalog.InstallationReason(entry, "native", method, hostSystem()); reason != "" {
			return nil, errors.New(reason)
		}
	}
	if err := catalog.Validate(entry); err != nil {
		return nil, err
	}
	base := filepath.Join(e.Root, "instances", r.ID)
	dir := e.nginxRecovery(r.ID)
	if pending, er := e.readNginxReplacement(r.ID); er == nil && pending.Phase != "committed" {
		return nil, errors.New("存在未完成的版本替换，请先检查恢复状态")
	}
	// The previous completed fallback is replaced only when a new replacement is explicitly requested.
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	// Builder needs ancestor traversal, but cannot read recovery metadata or old binaries.
	buildBase := filepath.Join(e.Root, "instances", "nginx-stage-"+security.ID())
	if err := os.MkdirAll(buildBase, 0711); err != nil {
		return nil, err
	}
	defer os.RemoveAll(buildBase)
	for _, sub := range []string{"runtime", "data", "logs", "conf"} {
		if err := os.MkdirAll(filepath.Join(buildBase, sub), 0755); err != nil {
			return nil, err
		}
	}
	candidate := r
	candidate.Config = cloneConfig(r.Config)
	candidate.Config["version"] = entry.Version
	candidate.Config["catalogId"] = entry.ID
	candidate.Config["installMethod"] = method
	candidate.Config["stageRuntime"] = true
	log("1/4 在暂存目录准备目标运行包，原站点继续服务")
	var err error
	if method == "binary" {
		err = e.installPrecompiled(ctx, candidate, entry, buildBase, log)
	} else {
		err = e.installSource(ctx, candidate, entry, buildBase, log)
	}
	if err != nil {
		return nil, err
	}
	delete(candidate.Config, "stageRuntime")
	log("2/4 使用目标程序校验全部站点与模块")
	if _, err = output(ctx, buildBase+"/runtime/sbin/nginx", "-t", "-p", base+"/runtime/", "-c", base+"/conf/nginx.conf"); err != nil {
		return nil, errors.New("目标版本不支持现有站点或模块，原服务未改动")
	}
	active, activeErr := output(ctx, "systemctl", "is-active", serviceName(r))
	if strings.TrimSpace(active) != "active" {
		if activeErr != nil && strings.TrimSpace(active) != "inactive" && strings.TrimSpace(active) != "failed" {
			return nil, errors.New("无法确认 Nginx 运行状态")
		}
		r.Status = "stopped"
	} else {
		r.Status = "running"
	}
	candidate.Status = r.Status
	j := nginxReplacement{Original: r, Phase: "prepared"}
	if err = e.saveNginxReplacement(r.ID, j); err != nil {
		return nil, err
	}
	defer func() {
		if retErr != nil {
			recovery, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			if er := e.restoreNginxReplacement(recovery, j, log); er != nil {
				retErr = fmt.Errorf("版本替换失败且回退未完成：%v；%w", er, retErr)
			}
		}
	}()
	log("3/4 停止原入口并替换运行包")
	if err = systemctl(ctx, log, "stop", serviceName(r)); err != nil {
		return nil, err
	}
	j.Phase = "swapping"
	if err = e.saveNginxReplacement(r.ID, j); err != nil {
		return nil, err
	}
	if err = os.Rename(base+"/runtime", filepath.Join(dir, "runtime")); err != nil {
		return nil, err
	}
	if err = os.Rename(buildBase+"/runtime", base+"/runtime"); err != nil {
		return nil, err
	}
	if err = e.nginx(ctx, candidate, log, "-t"); err != nil {
		return nil, err
	}
	if r.Status != "stopped" {
		if err = systemctl(ctx, log, "start", serviceName(candidate)); err != nil {
			return nil, err
		}
		if err = e.softwareHealth(ctx, candidate); err != nil {
			return nil, err
		}
		for _, site := range e.states() {
			if site.Kind == "site" && site.Config.String("instanceId") == r.ID {
				b, er := os.ReadFile(filepath.Join(base, "conf", "sites", site.ID+".conf"))
				if er != nil {
					return nil, er
				}
				if er = probeNginx(ctx, candidate, b); er != nil {
					return nil, er
				}
			}
		}
	}
	log("4/4 验证完成，保留旧运行包用于恢复")
	candidate.Name = "nginx-" + entry.Version
	if err = e.save(candidate); err != nil {
		return nil, err
	}
	j.Phase = "committed"
	if err = e.saveNginxReplacement(r.ID, j); err != nil {
		return nil, err
	}
	return model.Config{"version": entry.Version, "catalogId": entry.ID, "installMethod": method, "runtimeStatus": r.Status, "replacementAt": time.Now().UTC().Format(time.RFC3339)}, nil
}

// Startup reconciliation reports only version identity, never arbitrary stored configuration.
func (e *Engine) nginxVersionIdentity(r model.Resource) (model.Config, error) {
	stored, err := e.load(r.ID)
	if err != nil {
		return nil, err
	}
	if stored.Kind != "instance" || stored.Config.String("software") != "nginx" {
		return nil, errors.New("不是 Nginx 实例")
	}
	return model.Config{"version": stored.Config.String("version"), "catalogId": stored.Config.String("catalogId"), "installMethod": stored.Config.String("installMethod")}, nil
}
