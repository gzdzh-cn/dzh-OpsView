package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/shirou/gopsutil/v4/mem"
	"io"
	"net"
	"net/http"
	"opsview/internal/model"
	"opsview/internal/security"
	"opsview/internal/software"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (e *Engine) softwarePath(r model.Resource, file string) (string, error) {
	base := filepath.Join(e.Root, "instances", r.ID, "conf")
	files := map[string]map[string]string{"nginx": {"main": "nginx.conf"}, "mysql": {"main": "my.cnf"}, "php": {"main": "php.ini", "fpm": "php-fpm.conf"}, "redis": {"main": "redis.conf"}}
	name := files[r.Config.String("software")][file]
	if name == "" {
		return "", errors.New("不支持的配置文件")
	}
	path := filepath.Join(base, name)
	if err := regularManagedFile(path, base); err != nil {
		return "", err
	}
	return path, nil
}
func regularManagedFile(path, base string) error { return regularManagedLimit(path, base, 1<<20) }
func regularManagedLimit(path, base string, limit int64) error {
	rel, err := filepath.Rel(base, path)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return errors.New("配置文件越界")
	}
	// Reject symlink ancestors as well as the final file; root owns the configuration tree.
	for p := path; ; p = filepath.Dir(p) {
		st, er := os.Lstat(p)
		if er != nil {
			return er
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return errors.New("受管文件不允许符号链接")
		}
		if p == base {
			break
		}
	}
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || (limit > 0 && st.Size() > limit) {
		return errors.New("配置文件类型或大小无效")
	}
	return nil
}

var secretConfigLine = regexp.MustCompile(`(?im)^[ \t]*(?:requirepass|masterauth|user\s+default|session\.save_path|memcached\.sess_sasl_(?:username|password)|password)\b[^\r\n]*`)

func maskConfig(text string) string {
	return secretConfigLine.ReplaceAllStringFunc(text, func(line string) string {
		f := strings.FieldsFunc(line, func(r rune) bool { return r == ' ' || r == '\t' || r == '=' })
		if len(f) == 0 {
			return line
		}
		return f[0] + " [OpsView:凭据已隐藏]"
	})
}
func unmaskConfig(candidate, old string) string {
	for _, line := range secretConfigLine.FindAllString(old, -1) {
		candidate = strings.ReplaceAll(candidate, maskConfig(line), line)
	}
	return candidate
}

func (e *Engine) softwareOperation(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, err := e.load(op.Resource.ID)
	if err != nil {
		return nil, err
	}
	if r.Kind != "instance" {
		return nil, errors.New("仅限软件实例")
	}
	action := strings.TrimPrefix(op.Name, "instance.")
	sw := r.Config.String("software")
	if sw == "nginx" {
		e.siteMu.Lock()
		defer e.siteMu.Unlock()
	}
	if action == "switch-version" {
		return e.switchNginx(ctx, r, op.Params, log)
	}
	version := r.Config.String("version")
	if action == "inspect" {
		return e.inspectSoftware(ctx, r), nil
	}
	if r.Config.String("mode") == "docker" {
		return nil, errors.New("此设置仅适用于原生实例；已有容器请在 Docker 页面管理")
	}
	if action == "rotate-log" || action == "delete-log-archive" {
		return e.manageSoftwareLog(ctx, r, op.Params, action, log)
	}
	if action == "logs" {
		if sw == "redis" && op.Params.String("kind") == "slow" {
			return e.redisSlowLog(ctx, r)
		}
		return e.softwareLogs(ctx, r, op.Params)
	}
	if action == "metrics" {
		return e.softwareMetrics(ctx, r)
	}
	if action == "memory-budget" {
		if sw != "mysql" {
			return nil, errors.New("内存档位仅适用于 MySQL")
		}
		path, er := e.softwarePath(r, "main")
		if er != nil {
			return nil, er
		}
		b, er := os.ReadFile(path)
		if er != nil {
			return nil, er
		}
		values := software.Values("mysql", version, "main", string(b))
		if budget := op.Params.Int("budgetMB"); budget != 0 {
			preset, er := software.MySQLMemoryPreset(budget)
			if er != nil {
				return nil, er
			}
			for k, v := range preset {
				values[k] = v
			}
		}
		if raw, ok := op.Params["values"]; ok {
			b, _ := json.Marshal(raw)
			input := map[string]string{}
			if json.Unmarshal(b, &input) != nil {
				return nil, errors.New("内存参数必须为字符串")
			}
			if er := software.ValidateValues(software.Fields(sw, version, "main"), input); er != nil {
				return nil, er
			}
			for k, v := range input {
				values[k] = v
			}
		}
		estimate, er := software.EstimateMySQLMemory(version, values)
		if er != nil {
			return nil, er
		}
		result := model.Config{"values": values, "estimate": estimate}
		if m, er := mem.VirtualMemory(); er == nil {
			result["hostMemoryBytes"] = m.Total
			result["availableBytes"] = m.Available
		}
		return result, nil
	}
	if action == "guardian" {
		return e.softwareGuardian(ctx, r, op.Params, log)
	}
	if action == "reload" {
		if sw != "nginx" && sw != "php" {
			return nil, errors.New("此软件不支持整体配置重载，请应用并重启")
		}
		if sw == "php" {
			return e.controlPHP(ctx, r, "reload", op.Params, log)
		}
		if err = e.validateSoftware(ctx, r, "main", "", log); err != nil {
			return nil, err
		}
		return model.Config{}, systemctl(ctx, log, "reload", serviceName(r))
	}
	if action == "extensions" || action == "extension" {
		return e.phpExtension(ctx, r, op.Params, action == "extension", log)
	}
	if action == "session" {
		return e.phpSession(ctx, r, op.Params, log)
	}
	if action == "snapshot" {
		return e.redisSnapshot(ctx, r, log)
	}
	if action == "binlogs" || action == "purge-binlogs" {
		return e.mysqlBinlogs(ctx, r, op.Params, action == "purge-binlogs", log)
	}
	if action == "move-storage" {
		return e.moveMySQLStorage(ctx, r, op.Params, log)
	}
	if action == "port" {
		return e.softwarePort(ctx, r, op.Params, log)
	}
	file := op.Params.String("file")
	if file == "" {
		file = "main"
	}
	scopeSite := model.Resource{}
	unit := serviceName(r)
	if id := op.Params.String("siteId"); id != "" {
		if sw != "php" {
			return nil, errors.New("仅 PHP 支持站点配置范围")
		}
		scopeSite, err = e.load(id)
		if err != nil || scopeSite.Kind != "site" || scopeSite.Config.String("phpInstanceId") != r.ID {
			return nil, errors.New("站点不属于此 PHP 运行环境")
		}
		unit = phpSiteUnit(scopeSite)
	}
	path, err := e.softwarePath(r, file)
	if err != nil {
		return nil, err
	}
	if scopeSite.ID != "" {
		name := "php.ini"
		if file == "fpm" {
			name = "php-fpm.conf"
		}
		base := filepath.Join(e.Root, "sites", scopeSite.ID, "conf")
		path = filepath.Join(base, name)
		if err = regularManagedFile(path, base); err != nil {
			return nil, err
		}
	}
	oldBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	old := string(oldBytes)
	hash := security.Hash(old)
	historyDir := filepath.Join(e.Root, "instances", r.ID, "history", file)
	if scopeSite.ID != "" {
		historyDir = filepath.Join(e.Root, "sites", scopeSite.ID, "history", file)
	}
	if action == "configuration" {
		content := old
		if rev := op.Params.String("revision"); rev != "" {
			if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(rev) {
				return nil, errors.New("历史版本无效")
			}
			b, er := os.ReadFile(filepath.Join(historyDir, rev+".enc"))
			if er != nil {
				return nil, er
			}
			b, er = e.Vault.Decrypt(string(b))
			if er != nil {
				return nil, er
			}
			content = string(b)
		}
		entries, _ := os.ReadDir(historyDir)
		history := []model.Config{}
		for _, item := range entries {
			if strings.HasSuffix(item.Name(), ".enc") && item.Name() != "draft.enc" {
				info, er := item.Info()
				if er == nil {
					history = append(history, model.Config{"revision": strings.TrimSuffix(item.Name(), ".enc"), "createdAt": info.ModTime().UTC().Format(time.RFC3339)})
				}
			}
		}
		sort.Slice(history, func(i, j int) bool { return history[i].String("createdAt") > history[j].String("createdAt") })
		if len(history) > 30 {
			history = history[:30]
		}
		draft := ""
		if b, er := os.ReadFile(filepath.Join(historyDir, "draft.enc")); er == nil {
			if b, er = e.Vault.Decrypt(string(b)); er == nil {
				draft = maskConfig(string(b))
			}
		}
		return model.Config{"file": file, "revision": hash, "content": maskConfig(content), "draft": draft, "values": software.Values(sw, version, file, content), "fields": software.Fields(sw, version, file), "history": history}, nil
	}
	if action != "configure" && action != "validate" {
		return nil, errors.New("不支持的软件设置操作")
	}
	if op.Params.String("baseRevision") != hash {
		return nil, &model.RevisionConflict{Revision: hash}
	}
	next := unmaskConfig(op.Params.String("content"), old)
	if values, ok := op.Params["values"]; ok {
		raw, _ := json.Marshal(values)
		v := map[string]string{}
		if err = json.Unmarshal(raw, &v); err != nil {
			return nil, errors.New("参数必须是字符串键值对")
		}
		next, err = software.Patch(sw, version, file, old, v)
		if err != nil {
			return nil, err
		}
	}
	if err = software.ValidateEdit(sw, version, file, old, next); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(historyDir, 0700); err != nil {
		return nil, err
	}
	if action == "configure" && op.Params.String("apply") == "draft" {
		return model.Config{"draftSaved": true}, security.WriteAtomic(filepath.Join(historyDir, "draft.enc"), []byte(e.Vault.Encrypt([]byte(next))), 0600)
	}
	// Validate a private candidate before replacing the authoritative document.
	candidate, err := os.CreateTemp(filepath.Dir(path), ".candidate-*")
	if err != nil {
		return nil, err
	}
	candidatePath := candidate.Name()
	defer os.Remove(candidatePath)
	if _, err = candidate.WriteString(next); err != nil {
		candidate.Close()
		return nil, err
	}
	candidate.Close()
	log("1/4 校验候选配置")
	if err = e.validateSoftware(ctx, r, file, candidatePath, log); err != nil {
		return nil, err
	}
	if action == "validate" {
		return model.Config{"valid": true, "revision": hash}, nil
	}
	mode := op.Params.String("apply")
	if mode != "reload" && mode != "restart" && !((sw == "redis" || sw == "mysql") && mode == "dynamic") {
		return nil, errors.New("请选择重载或重启；草稿不会应用到运行服务")
	}
	if sw == "php" && (r.Config.Bool("servicesPaused") || scopeSite.Config.Bool("phpPaused")) {
		return nil, errors.New("PHP 服务已主动停止，请先启动服务，或仅保存草稿")
	}
	if scopeSite.ID != "" && siteUnavailable(scopeSite) {
		return nil, errors.New("站点已停止或到期，请先恢复访问，或仅保存草稿")
	}
	if mode == "reload" && sw != "nginx" && sw != "php" {
		return nil, errors.New("此版本配置需要重启")
	}
	if sw == "redis" {
		before := software.Values(sw, version, file, old)
		after := software.Values(sw, version, file, next)
		if before["appendonly"] == "yes" && after["appendonly"] == "no" && !op.Params.Bool("confirmPersistenceChange") {
			return nil, errors.New("关闭 AOF 可能降低数据保障，需要单独确认")
		}
	}
	log("2/4 保存加密历史")
	if err = security.WriteAtomic(filepath.Join(historyDir, hash+".enc"), []byte(e.Vault.Encrypt(oldBytes)), 0600); err != nil {
		return nil, err
	}
	st, _ := os.Stat(path)
	if sw == "redis" {
		return e.applyRedisConfig(ctx, r, path, old, next, log)
	}
	if sw == "mysql" && mode == "dynamic" {
		return e.applyMySQLConfig(ctx, r, path, old, next, log)
	}
	var statusOld, statusNext []byte
	statusPath := filepath.Join(e.Root, "instances", r.ID, "conf", "status.conf")
	if sw == "nginx" && r.Config.Int("statusPort") > 0 {
		statusOld, err = os.ReadFile(statusPath)
		if err != nil {
			return nil, err
		}
		statusNext = []byte(nginxStatusServer(r.Config.Int("statusPort"), security.ID()))
		if err = security.WriteAtomic(statusPath, statusNext, 0644); err != nil {
			return nil, err
		}
	}
	log("3/4 应用配置")
	if err = writeOwnedAtomic(path, []byte(next), st.Mode().Perm()); err != nil {
		if len(statusOld) > 0 {
			_ = security.WriteAtomic(statusPath, statusOld, 0644)
		}
		return nil, err
	}
	restore := func(cause error) (model.Config, error) {
		recovery, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if len(statusOld) > 0 {
			_ = security.WriteAtomic(statusPath, statusOld, 0644)
		}
		writeErr := writeOwnedAtomic(path, oldBytes, st.Mode().Perm())
		restartErr := systemctl(recovery, func(string) {}, mode, unit)
		if writeErr != nil || restartErr != nil {
			return nil, fmt.Errorf("应用失败且回退未完成，请检查服务：%w", cause)
		}
		return nil, fmt.Errorf("应用失败，已恢复原配置：%w", cause)
	}
	if err = systemctl(ctx, log, mode, unit); err != nil {
		return restore(err)
	}
	log("4/4 检查服务状态")
	if scopeSite.ID != "" {
		_, err = fpmStatus(ctx, filepath.Join(e.Root, "sites", scopeSite.ID, "run", "php.sock"))
	} else {
		err = e.softwareHealth(ctx, r)
	}
	if err != nil {
		return restore(err)
	}
	if len(statusNext) > 0 {
		probe := r
		probe.Config = cloneConfig(r.Config)
		probe.Config["port"] = r.Config["statusPort"]
		if err = probeNginx(ctx, probe, statusNext); err != nil {
			return restore(err)
		}
	}
	r.Config["configurationRevision"] = security.Hash(next)
	if err = e.save(r); err != nil {
		return restore(err)
	}
	_ = os.Remove(filepath.Join(historyDir, "draft.enc"))
	return model.Config{"configurationRevision": security.Hash(next), "runtimeStatus": r.Status}, nil
}
func (e *Engine) inspectSoftware(ctx context.Context, r model.Resource) model.Config {
	sw := r.Config.String("software")
	associated := []model.Config{}
	for _, s := range e.states() {
		if s.ID != r.ID && (s.Config.String("instanceId") == r.ID || s.Config.String("phpInstanceId") == r.ID || s.Config.String("sessionRedisInstanceId") == r.ID || s.Config.String("targetId") == r.ID) {
			associated = append(associated, model.Config{"id": s.ID, "name": s.Name, "kind": s.Kind})
		}
	}
	state := "unavailable"
	enabled := false
	detail := ""
	if r.Config.String("mode") == "native" {
		var err error
		detail, err = output(ctx, "systemctl", "show", serviceName(r), "--property=ActiveState,SubState,MainPID,MemoryCurrent,CPUUsageNSec,UnitFileState")
		if err == nil {
			for _, l := range strings.Split(detail, "\n") {
				if strings.HasPrefix(l, "ActiveState=") {
					state = strings.TrimPrefix(l, "ActiveState=")
				}
				if l == "UnitFileState=enabled" {
					enabled = true
				}
			}
		}
	}
	supported := sw == "nginx" || sw == "mysql" || sw == "php" || sw == "redis"
	files := []string{"main"}
	if sw == "php" {
		files = append(files, "fpm")
	}
	return model.Config{"software": sw, "version": r.Config["version"], "state": state, "enabled": enabled, "detail": detail, "associated": associated, "files": files, "editable": supported && r.Config.String("mode") == "native", "reload": sw == "nginx" || sw == "php", "guardian": r.Config["guardian"], "port": r.Config["port"], "baseDirectory": filepath.Join(e.Root, "instances", r.ID), "notice": "仅展示实际采集结果；配置应用失败会恢复原配置"}
}
func (e *Engine) validateSoftware(ctx context.Context, r model.Resource, file, candidate string, log func(string)) error {
	base := filepath.Join(e.Root, "instances", r.ID)
	if candidate == "" {
		candidate, _ = e.softwarePath(r, file)
	}
	var err error
	switch r.Config.String("software") {
	case "nginx":
		_, err = output(ctx, base+"/runtime/sbin/nginx", "-p", base+"/runtime/", "-t", "-c", candidate)
	case "mysql":
		args := []string{"--defaults-file=" + candidate, "--validate-config"}
		if strings.HasPrefix(r.Config.String("version"), "5.7") {
			args = []string{"--defaults-file=" + candidate, "--verbose", "--help"}
		}
		_, err = output(ctx, base+"/runtime/bin/mysqld", args...)
	case "php":
		if file == "fpm" {
			_, err = output(ctx, base+"/runtime/sbin/php-fpm", "-t", "-y", candidate, "-c", base+"/conf/php.ini")
		} else {
			_, err = output(ctx, base+"/runtime/bin/php", "-c", candidate, "-r", "exit(0);")
		}
	case "redis": // Redis has no config-test option. Restrict all changes to validated scalars; startup is checked with rollback.
	default:
		return errors.New("此软件没有配置校验器")
	}
	if err != nil {
		return errors.New("软件拒绝候选配置；原配置未替换，请检查参数及版本兼容性")
	}
	return nil
}
func (e *Engine) softwareHealth(ctx context.Context, r model.Resource) error {
	active, err := output(ctx, "systemctl", "is-active", serviceName(r))
	if err != nil || strings.TrimSpace(active) != "active" {
		return errors.New("服务未运行")
	}
	switch r.Config.String("software") {
	case "php":
		_, err := fpmStatus(ctx, phpStatusAddress(r))
		return err
	case "redis":
		_, err := e.redisCommand(ctx, r, "PING")
		return err
	case "mysql":
		return e.sql(ctx, r, "", strings.NewReader("SELECT 1;"), func(string) {})
	}
	return waitPort(ctx, r.Config.Int("port"))
}
func (e *Engine) softwareGuardian(ctx context.Context, r model.Resource, p model.Config, log func(string)) (model.Config, error) {
	seconds, attempts := p.Int("restartSeconds"), p.Int("attempts")
	if seconds < 1 || seconds > 300 || attempts < 1 || attempts > 20 {
		return nil, errors.New("重试间隔为 1–300 秒，次数为 1–20")
	}
	oom := p.Int("oomScore")
	if oom < -500 || oom > 500 {
		return nil, errors.New("OOM 优先级必须在 -500–500")
	}
	memory := p.Int("memoryMB")
	if memory != 0 && memory < 128 {
		return nil, errors.New("内存上限至少 128 MB，0 表示不限制")
	}
	restart := "no"
	if p.Bool("enabled") {
		restart = "on-failure"
	}
	body := fmt.Sprintf("[Unit]\nStartLimitIntervalSec=300\nStartLimitBurst=%d\n[Service]\nRestart=%s\nRestartSec=%d\nOOMScoreAdjust=%d\n", attempts, restart, seconds, oom)
	if memory > 0 {
		body += fmt.Sprintf("MemoryHigh=%dM\nMemoryMax=%dM\n", memory*9/10, memory)
	} else {
		body += "MemoryHigh=infinity\nMemoryMax=infinity\n"
	}
	path := filepath.Join("/etc/systemd/system", serviceName(r)+".d", "opsview-guardian.conf")
	old, readErr := os.ReadFile(path)
	if err := security.WriteAtomic(path, []byte(body), 0644); err != nil {
		return nil, err
	}
	err := run(ctx, log, "", nil, nil, "systemctl", "daemon-reload")
	if err == nil {
		boot := "disable"
		if p.Bool("autostart") {
			boot = "enable"
		}
		err = systemctl(ctx, log, boot, serviceName(r))
	}
	if err != nil {
		if readErr == nil {
			_ = writeOwnedAtomic(path, old, 0644)
		} else {
			_ = os.Remove(path)
		}
		return nil, err
	}
	r.Config["guardian"] = p
	if err = e.save(r); err != nil {
		return nil, err
	}
	return model.Config{"guardian": p}, nil
}
func (e *Engine) softwareMetrics(ctx context.Context, r model.Resource) (model.Config, error) {
	data := e.inspectSoftware(ctx, r)
	switch r.Config.String("software") {
	case "mysql":
		var buf strings.Builder
		err := e.sql(ctx, r, "", strings.NewReader("SHOW GLOBAL STATUS;"), func(s string) { buf.WriteString(s + "\n") })
		if err != nil {
			data["unavailable"] = "无法读取 MySQL 状态"
		} else {
			data["metrics"] = parsePairs(buf.String(), "\t")
		}
	case "redis":
		text, err := e.redisCommand(ctx, r, "INFO", "all")
		if err != nil {
			data["unavailable"] = "无法读取 Redis 状态"
		} else {
			data["metrics"] = parsePairs(text, ":")
		}
	case "nginx":
		port := r.Config.Int("statusPort")
		if port == 0 {
			data["unavailable"] = "此旧实例未配置本机状态端口"
			break
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://127.0.0.1:%d/opsview-status", port), nil)
		res, err := (&http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
		if err != nil {
			data["unavailable"] = "Nginx 状态接口不可用"
			break
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		data["content"] = string(b)
	case "php":
		status, er := fpmStatus(ctx, phpStatusAddress(r))
		if er != nil {
			data["unavailable"] = "FPM 状态暂不可用"
		} else {
			data["metrics"] = status
		}
		sites := []model.Config{}
		for _, site := range e.states() {
			if site.Config.String("phpInstanceId") == r.ID {
				status, er := fpmStatus(ctx, filepath.Join(e.Root, "sites", site.ID, "run", "php.sock"))
				item := model.Config{"site": site.Name, "status": status}
				if er != nil {
					item["unavailable"] = true
				}
				sites = append(sites, item)
			}
		}
		data["sites"] = sites
	}
	return data, nil
}
func parsePairs(text, sep string) map[string]string {
	out := map[string]string{}
	for _, l := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), sep)
		if ok && !strings.HasPrefix(k, "#") {
			out[k] = strings.TrimSpace(v)
		}
	}
	return out
}
func (e *Engine) redisCommand(ctx context.Context, r model.Resource, args ...string) (string, error) {
	return e.redisCommandFormat(ctx, r, "--raw", args...)
}
func (e *Engine) redisCommandFormat(ctx context.Context, r model.Resource, format string, args ...string) (string, error) {
	base := filepath.Join(e.Root, "instances", r.ID)
	cmd := exec.CommandContext(ctx, base+"/runtime/bin/redis-cli", append([]string{format, "-h", "127.0.0.1", "-p", strconv.Itoa(r.Config.Int("port"))}, args...)...)
	cmd.Env = append(os.Environ(), "REDISCLI_AUTH="+r.Config.String("password"))
	b, err := cmd.CombinedOutput()
	if err != nil {
		return "", errors.New("Redis 请求失败")
	}
	text := string(b)
	for _, code := range []string{"ERR ", "NOAUTH ", "WRONGPASS ", "NOPERM ", "LOADING ", "BUSY ", "MISCONF ", "READONLY ", "MASTERDOWN ", "NOREPLICAS "} {
		if strings.HasPrefix(text, code) {
			return "", errors.New("Redis 拒绝请求或正在加载，请检查服务状态与授权")
		}
	}
	return text, nil
}
func (e *Engine) redisSnapshot(ctx context.Context, r model.Resource, log func(string)) (model.Config, error) {
	if r.Config.String("software") != "redis" {
		return nil, errors.New("仅支持 Redis")
	}
	text, err := e.redisCommand(ctx, r, "BGSAVE")
	if err == nil {
		log("已请求后台快照，请在持久化状态中核对完成结果")
	}
	return model.Config{"snapshotRequested": true, "message": text}, err
}
func (e *Engine) mysqlBinlogs(ctx context.Context, r model.Resource, p model.Config, purge bool, log func(string)) (model.Config, error) {
	if r.Config.String("software") != "mysql" {
		return nil, errors.New("仅支持 MySQL")
	}
	var buf strings.Builder
	collect := func(s string) { buf.WriteString(s + "\n") }
	if !purge {
		err := e.sql(ctx, r, "", strings.NewReader("SHOW BINARY LOGS;"), collect)
		return model.Config{"content": buf.String()}, err
	}
	// Refuse automated purges without an authoritative replication/backup inventory.
	return nil, errors.New("尚无完整的复制及备份依赖登记，已阻止自动清理；请使用数据库官方流程核对后清理")
}
func (e *Engine) softwarePort(ctx context.Context, r model.Resource, p model.Config, log func(string)) (model.Config, error) {
	sw := r.Config.String("software")
	if sw != "mysql" && sw != "redis" {
		return nil, errors.New("此入口只支持 MySQL / Redis 端口")
	}
	port := p.Int("port")
	if port < 1 || port > 65535 {
		return nil, errors.New("端口无效")
	}
	if port == r.Config.Int("port") {
		return model.Config{"port": port}, nil
	}
	for _, x := range e.states() {
		if x.Config.String("sessionRedisInstanceId") == r.ID {
			return nil, errors.New("Redis 被 PHP Session 使用，请先切换会话存储后再修改端口")
		}
		if x.Kind == "database" && x.Config.String("instanceId") == r.ID && x.Config.Bool("allowRemote") {
			return nil, errors.New("有关联远程数据库，请先调整访问规则以免连接中断")
		}
	}
	conn, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, errors.New("新端口已被占用")
	}
	conn.Close()
	path, err := e.softwarePath(r, "main")
	if err != nil {
		return nil, err
	}
	old, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if p.String("baseRevision") != security.Hash(string(old)) {
		return nil, errors.New("配置已变化，请重新载入")
	}
	re := regexp.MustCompile(`(?m)^port\s*[= ]\s*[0-9]+\s*$`)
	sep := "="
	if sw == "redis" {
		sep = " "
	}
	if len(re.FindAll(old, -1)) != 1 {
		return nil, errors.New("未找到唯一受管端口指令")
	}
	next := re.ReplaceAllString(string(old), "port"+sep+strconv.Itoa(port))
	st, er := os.Stat(path)
	if er != nil {
		return nil, er
	}
	if err = writeOwnedAtomic(path, []byte(next), st.Mode().Perm()); err != nil {
		return nil, err
	}
	original := r
	r.Config = cloneConfig(r.Config)
	r.Config["port"] = port
	if err = systemctl(ctx, log, "restart", serviceName(r)); err == nil {
		err = e.softwareHealth(ctx, r)
	}
	if err == nil {
		err = e.save(r)
	}
	if err != nil {
		_ = writeOwnedAtomic(path, old, st.Mode().Perm())
		_ = systemctl(context.Background(), log, "restart", serviceName(original))
		return nil, fmt.Errorf("端口修改失败，已恢复配置：%w", err)
	}
	return model.Config{"port": port}, nil
}
func cloneConfig(c model.Config) model.Config {
	b, _ := json.Marshal(c)
	var out model.Config
	_ = json.Unmarshal(b, &out)
	return out
}
