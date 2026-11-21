package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (e *Engine) deletionRecord(id string) (model.Resource, bool) {
	r, err := e.load("delete-" + id)
	return r, err == nil && r.Kind == "site-deletion"
}
func (e *Engine) deletionSite(r model.Resource) model.Resource {
	if stored, err := e.load(r.ID); err == nil && stored.Kind == "site" {
		return stored
	}
	if rec, ok := e.deletionRecord(r.ID); ok {
		raw, _ := json.Marshal(rec.Config["site"])
		var old model.Resource
		if json.Unmarshal(raw, &old) == nil && old.ID == r.ID {
			return old
		}
	}
	// Failed first deployments have a reservation, but no authoritative state yet.
	if claim, err := os.ReadFile(filepath.Join(e.Root, "state", r.ID+".document-root")); err == nil {
		r.Config = cloneConfig(r.Config)
		r.Config["documentRoot"] = string(claim)
	}
	return r
}
func (e *Engine) inspectSiteDeletion(r model.Resource) (model.Config, error) {
	r = e.deletionSite(r)
	base := filepath.Join(e.Root, "sites", r.ID)
	root := siteDocumentRoot(r, base)
	conf := filepath.Join(e.Root, "instances", r.Config.String("instanceId"), "conf", "sites", r.ID+".conf")
	reason := ""
	identity := "missing"
	var confHash string
	if raw, err := os.ReadFile(conf); err == nil {
		confHash = security.Hash(string(raw))
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if r.Config.String("type") == "proxy" {
		reason = "反向代理不删除上游目录"
	} else if err := e.validateDeletionPath(root); err != nil {
		reason = err.Error()
	} else if st, err := os.Lstat(root); err == nil {
		uid, gid, dev := fileOwner(st)
		identity = fmt.Sprintf("%d:%d:%d:%s:%s", uid, gid, dev, st.ModTime().UTC().Format(time.RFC3339Nano), fileRevision(st))
	} else if !os.IsNotExist(err) {
		reason = err.Error()
	}
	for _, other := range e.states() {
		if other.Kind != "site" || other.ID == r.ID {
			continue
		}
		otherRoot := siteDocumentRoot(other, filepath.Join(e.Root, "sites", other.ID))
		if pathContains(root, otherRoot) || pathContains(otherRoot, root) {
			reason = "目录与其他站点相同或交叠"
		}
	}
	phase := "未执行"
	recoveryOptions := model.Config{}
	if rec, ok := e.deletionRecord(r.ID); ok {
		phase = rec.Config.String("phase")
		for _, key := range []string{"deleteDirectory", "deleteDatabase", "deleteLogs"} {
			recoveryOptions[key] = rec.Config.Bool(key)
		}
	}
	if confHash == "" && phase == "未执行" {
		phase = "站点配置不存在，需核对并清理残留记录"
	}
	legacyUnits := model.Config{}
	if _, err := e.load(r.ID); err != nil {
		if _, recovering := e.deletionRecord(r.ID); !recovering {
			legacyUnits, err = e.inspectUnregisteredSite(r, conf, "/etc/systemd/system")
			if err != nil {
				return nil, err
			}
			if len(legacyUnits) > 0 {
				phase = "站点配置已不存在；已核验归属的独立 PHP-FPM 残留服务将清理"
			}
		}
	}
	userPolicy := "已有或共享用户不随站点删除"
	if r.Config.String("user") == "isolated" || r.Config.String("user") == "" {
		userPolicy = "保留独立用户；文件彻底清理后检查依赖再清理"
	}
	return model.Config{"directory": root, "directoryReason": reason, "identity": identity, "configHash": confHash, "legacyUnits": legacyUnits, "phase": phase, "recoveryOptions": recoveryOptions, "runtimeUser": r.Config.String("runtimeUser"), "userPolicy": userPolicy, "firewallPolicy": "仅清理本项目创建且无其他依赖的端口规则"}, nil
}

// Only an exact unit generated for this site and its registered PHP instance can
// be recovered without executor site state. A filename or Description alone is
// not ownership evidence. Unknown vhosts/processes, links and overrides stay put.
func (e *Engine) inspectUnregisteredSite(r model.Resource, conf, unitDir string) (model.Config, error) {
	units := model.Config{}
	if _, err := os.Lstat(conf); !os.IsNotExist(err) {
		return nil, fmt.Errorf("站点配置尚未登记：%s，请核对后恢复站点记录", conf)
	}
	for _, unit := range []string{phpSiteUnit(r), serviceName(managedSiteProcess(r))} {
		path := filepath.Join(unitDir, unit)
		st, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("无法读取站点残留服务 %s：%w", unit, err)
		}
		if !st.Mode().IsRegular() || unit != phpSiteUnit(r) {
			return nil, fmt.Errorf("无法确认残留服务归属：%s，请核对服务后恢复站点记录", path)
		}
		php, err := e.load(r.Config.String("phpInstanceId"))
		if err != nil || php.Kind != "instance" || php.Config.String("software") != "php" || php.Config.String("mode") != "native" {
			return nil, fmt.Errorf("残留服务 %s 的 PHP 实例关联失效，请先核对实例", unit)
		}
		service := r
		service.Kind = "site-php"
		data, err := os.ReadFile(path)
		if err != nil || string(data) != serviceUnitContent(service, e.sitePHPUnitBody(r, php.ID)) {
			return nil, fmt.Errorf("残留服务内容与站点不匹配：%s，请核对后恢复站点记录", path)
		}
		// Drop-ins may alter ExecStart or add other services even when the main
		// file matches. Do not automatically clean up such customized units.
		for _, dir := range []string{unitDir, "/run/systemd/system", "/usr/local/lib/systemd/system", "/usr/lib/systemd/system", "/lib/systemd/system"} {
			for _, name := range []string{unit + ".d", "opsview-site-php-.service.d", "opsview-site-.service.d", "opsview-.service.d", "service.d"} {
				if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
					return nil, fmt.Errorf("残留服务 %s 存在自定义覆盖 %s，请先核对", unit, filepath.Join(dir, name))
				}
			}
		}
		units[unit] = security.Hash(string(data))
	}
	return units, nil
}
func (e *Engine) validateDeletionPath(path string) error {
	f, err := e.filePath(path, false)
	if err != nil {
		// A nonexistent project is already clean, but its parent must be trusted.
		if os.IsNotExist(err) && !e.criticalFilePath(path) {
			return e.checkFilePath(path, true)
		}
		return err
	}
	defer f.close()
	if e.criticalFilePath(f.path) {
		return errors.New("受保护目录不能随站点删除")
	}
	st, err := f.root.Lstat(f.name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return errors.New("项目目录已变为链接或非目录，请先核对")
	}
	if hasNestedMount(f.path) {
		return errors.New("项目目录包含挂载点，请单独处理")
	}
	return nil
}
func (e *Engine) deleteSite(ctx context.Context, op model.Operation, log func(string)) (result model.Config, retErr error) {
	r := e.deletionSite(op.Resource)
	p := op.Params
	base := filepath.Join(e.Root, "sites", r.ID)
	inspection, err := e.inspectSiteDeletion(r)
	if err != nil {
		return nil, err
	}
	if p.Bool("deleteDirectory") && inspection.String("directoryReason") != "" {
		return nil, errors.New(inspection.String("directoryReason"))
	}
	rec, exists := e.deletionRecord(r.ID)
	if !exists {
		rec = model.Resource{ID: "delete-" + r.ID, Kind: "site-deletion", Name: r.Name, Config: model.Config{"site": r, "phase": "prepared", "legacyUnits": inspection["legacyUnits"], "deleteDirectory": p.Bool("deleteDirectory"), "deleteDatabase": p.Bool("deleteDatabase"), "deleteLogs": p.Bool("deleteLogs"), "startedAt": time.Now().UTC().Format(time.RFC3339Nano)}}
	}
	for _, key := range []string{"deleteDirectory", "deleteDatabase", "deleteLogs"} {
		if rec.Config.Bool(key) != p.Bool(key) {
			return nil, errors.New("删除已开始，必须沿用原选项完成现场核对")
		}
	}
	if err = e.save(rec); err != nil {
		return nil, err
	}
	result = model.Config{"steps": model.Config{"phase": rec.Config.String("phase")}, "inspection": inspection}
	save := func(phase string) error { rec.Config["phase"] = phase; return e.save(rec) }
	var db, inst, backup model.Resource
	if p.Bool("deleteDatabase") {
		db, err = e.load(r.Config.String("databaseId"))
		if err != nil {
			return result, errors.New("关联数据库未登记，请先同步核对")
		}
		if db.Kind != "database" {
			return result, errors.New("关联资源不是数据库")
		}
		for _, other := range e.states() {
			if other.Kind == "site" && other.ID != r.ID && other.Config.String("databaseId") == db.ID {
				return result, errors.New("数据库仍被其他站点引用")
			}
		}
		inst, err = e.load(db.Config.String("instanceId"))
		if err != nil {
			return result, err
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		if !rec.Config.Bool("databasePrepared") && db.Status != "recycled" {
			if (db.Config.String("recyclePhase") == "prepared" && db.Config.String("siteDeleteOwner") != r.ID) || (db.Config.String("siteDeleteOwner") != "" && db.Config.String("siteDeleteOwner") != r.ID) {
				return result, errors.New("数据库正在其他删除流程中")
			}
			db.Config["siteDeleteOwner"] = r.ID
			// Write ownership before preparation, allowing a post-crash check of the backup.
			if err = e.save(db); err != nil {
				return result, err
			}
			if db.Config.String("recyclePhase") != "prepared" {
				db, backup, err = e.prepareDatabaseRecycle(ctx, inst, db, log)
			} else {
				backup, err = e.load(db.Config.String("recycleBackupId"))
			}
			if err != nil {
				return result, fmt.Errorf("数据库备份未完成，站点保留：%w", err)
			}
			rec.Config["databasePrepared"] = true
			if err = save("database-prepared"); err != nil {
				return result, err
			}
		}
		if backup.ID == "" {
			backup, err = e.load(db.Config.String("recycleBackupId"))
			if err != nil {
				return result, err
			}
		}
		artifact := filepath.Join(e.Root, "backups", backup.ID, backup.Config.String("artifact"))
		sum, er := hashFile(artifact)
		if er != nil || sum != backup.Config.String("sha256") {
			return result, errors.New("回收备份校验失败，未删除站点")
		}
		result["resources"] = []model.Resource{db, backup}
	}
	for _, path := range []string{func() string {
		if p.Bool("deleteDirectory") {
			return inspection.String("directory")
		}
		return ""
	}(), func() string {
		if p.Bool("deleteLogs") {
			return base + "/logs"
		}
		return ""
	}()} {
		if path != "" {
			if er := e.validateDeletionPath(path); er != nil {
				return result, er
			}
			if _, er := os.Lstat(path); er == nil {
				if _, er = e.trashDirectory(path); er != nil {
					return result, er
				}
			}
		}
	}
	conf := filepath.Join(e.Root, "instances", r.Config.String("instanceId"), "conf", "sites", r.ID+".conf")
	if !rec.Config.Bool("configRemoved") {
		nginx, er := e.load(r.Config.String("instanceId"))
		if er != nil {
			return result, er
		}
		old, er := os.ReadFile(conf)
		if er != nil && !os.IsNotExist(er) {
			return result, er
		}
		if len(old) > 0 {
			rec.Config["oldConfig"] = string(old)
			if err = save("removing-config"); err != nil {
				return result, err
			}
			if err = os.Remove(conf); err != nil {
				return result, err
			}
		}
		if len(old) > 0 || rec.Config.String("oldConfig") != "" {
			if err = e.nginx(ctx, nginx, log, "-t"); err == nil {
				if active, probeErr := output(ctx, "systemctl", "is-active", serviceName(nginx)); probeErr == nil && strings.TrimSpace(active) == "active" {
					err = e.nginx(ctx, nginx, log, "-s", "reload")
				} else if strings.TrimSpace(active) != "inactive" && strings.TrimSpace(active) != "failed" && nginx.Status != "stopped" {
					err = e.nginx(ctx, nginx, log, "-s", "reload")
				}
			}
			if err != nil {
				if restore := rec.Config.String("oldConfig"); restore != "" {
					_ = security.WriteAtomic(conf, []byte(restore), 0644)
					_ = e.nginx(context.WithoutCancel(ctx), nginx, log, "-s", "reload")
				}
				return result, fmt.Errorf("Nginx 删除校验失败，已尝试恢复：%w", err)
			}
		}
		rec.Config["configRemoved"] = true
		if err = save("config-removed"); err != nil {
			return result, err
		}
	}
	// Once the public entry is removed, only this site's owned service units stop.
	if !rec.Config.Bool("servicesRemoved") {
		var legacyUnits map[string]string
		raw, _ := json.Marshal(rec.Config["legacyUnits"])
		_ = json.Unmarshal(raw, &legacyUnits)
		if len(legacyUnits) > 0 {
			current, er := e.inspectUnregisteredSite(r, conf, "/etc/systemd/system")
			if er != nil {
				return result, er
			}
			for unit, hash := range current {
				if legacyUnits[unit] != hash {
					return result, fmt.Errorf("残留服务 %s 在确认后发生变化，请核对", unit)
				}
			}
		}
		units := []string{phpSiteUnit(r)}
		changedUnits := false
		if r.Config.String("type") == "managed" {
			units = append(units, serviceName(managedSiteProcess(r)))
		}
		for _, unit := range units {
			path := filepath.Join("/etc/systemd/system", unit)
			if _, er := os.Stat(path); er == nil {
				changedUnits = true
				if er = systemctl(ctx, log, "stop", unit); er != nil {
					return result, er
				}
				if er = systemctl(ctx, log, "disable", unit); er != nil {
					return result, er
				}
				if er = os.Remove(path); er != nil {
					return result, er
				}
			}
		}
		if changedUnits {
			if err = run(ctx, log, "", nil, nil, "systemctl", "daemon-reload"); err != nil {
				return result, err
			}
		}
		if r.Config.String("type") == "managed" {
			if er := e.forget(managedSiteProcess(r).ID); er != nil && !os.IsNotExist(er) {
				return result, er
			}
		}
		rec.Config["servicesRemoved"] = true
		if err = save("services-removed"); err != nil {
			return result, err
		}
	}
	if p.Bool("deleteDatabase") && db.Status != "recycled" {
		data, er := e.finishDatabaseRecycle(ctx, inst, db, backup, log)
		if data != nil {
			result["resources"] = data["resources"]
		}
		if er != nil {
			return result, er
		}
		rec.Config["databaseRecycled"] = true
		if err = save("database-recycled"); err != nil {
			return result, err
		}
	}
	if r.Config.String("type") == "managed" {
		if err = e.syncSitePort(ctx, r, true); err != nil {
			return result, err
		}
	}
	e.fileMu.Lock()
	defer e.fileMu.Unlock()
	if err = e.retainSiteAccount(r, inspection.String("directory")); err != nil {
		return result, err
	}
	for _, entry := range []struct {
		key, path string
		enabled   bool
	}{{"directoryTrash", inspection.String("directory"), p.Bool("deleteDirectory")}, {"logsTrash", base + "/logs", p.Bool("deleteLogs")}} {
		if !entry.enabled || rec.Config.String(entry.key) != "" {
			continue
		}
		if _, er := os.Lstat(entry.path); os.IsNotExist(er) {
			index, er := e.trashIndex()
			if er != nil {
				return result, er
			}
			for _, v := range index {
				if v.Original == entry.path && v.DeletedAt.Format(time.RFC3339Nano) >= rec.Config.String("startedAt") {
					rec.Config[entry.key] = v.ID
				}
			}
			if rec.Config.String(entry.key) == "" {
				rec.Config[entry.key] = "already-absent"
			}
		} else if er != nil {
			return result, er
		} else {
			if er = e.validateDeletionPath(entry.path); er != nil {
				return result, er
			}
			item, er := e.trashFile(entry.path)
			if er != nil {
				return result, er
			}
			rec.Config[entry.key] = item.ID
		}
		if err = save("data-recycled"); err != nil {
			return result, err
		}
	}
	if err = e.forget(r.ID); err != nil && !os.IsNotExist(err) {
		return result, err
	}
	if err = os.Remove(filepath.Join(e.Root, "state", r.ID+".document-root")); err != nil && !os.IsNotExist(err) {
		return result, err
	}
	rec.Config["completed"] = true
	// Owned process credentials are no longer used after the unit is gone.
	if r.Config.String("type") == "managed" {
		if er := os.Remove(filepath.Join(e.Root, "state", managedSiteProcess(r).ID+".env")); er != nil && !os.IsNotExist(er) {
			return result, er
		}
	}
	delete(rec.Config, "oldConfig")
	if err = save("completed"); err != nil {
		return result, err
	}
	result["steps"] = model.Config{"phase": "completed", "directoryTrash": rec.Config.String("directoryTrash"), "logsTrash": rec.Config.String("logsTrash"), "databaseRecycled": rec.Config.Bool("databaseRecycled")}
	// Never return the encrypted recovery record's credentials or raw config.
	return result, nil
}
