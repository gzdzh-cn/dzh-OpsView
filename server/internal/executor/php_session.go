package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

func (e *Engine) phpSession(ctx context.Context, r model.Resource, p model.Config, log func(string)) (model.Config, error) {
	e.siteMu.Lock()
	defer e.siteMu.Unlock()
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
		handler := phpIniValue(string(old), "session.save_handler", "files")
		namespace, isolated := phpSessionNamespace(string(old), handler, sessions, subject.ID)
		return model.Config{"handler": handler, "revision": revision, "expiredFiles": expired, "directory": sessions, "instanceId": subject.Config.String("sessionRedisInstanceId"), "memcached": inspectMemcachedSession(string(old)), "namespace": namespace, "isolated": isolated}, nil
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
		return nil, &model.RevisionConflict{Revision: revision}
	}
	if !p.Bool("confirmed") {
		return nil, errors.New("切换可能使现有会话失效，需要确认")
	}
	active, activeErr := output(ctx, "systemctl", "is-active", unit)
	if activeErr != nil || strings.TrimSpace(active) != "active" {
		return nil, errors.New("对应 FPM 服务未运行，请先启动")
	}
	log("1/4 检查存储连接与会话扩展")
	handler := p.String("handler")
	savePath := sessions
	settings := map[string]string{}
	switch handler {
	case "files":
		if _, err = safePath(base, "sessions"); err != nil {
			return nil, err
		}
		if err = os.MkdirAll(sessions, 0700); err != nil {
			return nil, err
		}
		if err = os.Chmod(sessions, 0700); err != nil {
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
		savePath = fmt.Sprintf("tcp://127.0.0.1:%d?auth=%s&prefix=%s&timeout=2&read_timeout=2", inst.Config.Int("port"), url.QueryEscape(inst.Config.String("password")), url.QueryEscape("ov."+subject.ID+"."))
	case "memcached":
		settings, err = memcachedSessionSettings(p, string(old), subject.ID)
		if err != nil {
			return nil, err
		}
		savePath = settings["session.save_path"]
	default:
		return nil, errors.New("会话处理器无效")
	}
	settings["session.save_handler"] = handler
	settings["session.save_path"] = savePath
	// Credentials from an inactive handler must not linger in the running document.
	if handler != "memcached" {
		settings["memcached.sess_sasl_username"] = ""
		settings["memcached.sess_sasl_password"] = ""
	}
	next := patchSessionIni(string(old), settings)
	candidate, err := os.CreateTemp(base+"/conf", ".session-*.ini")
	if err != nil {
		return nil, err
	}
	defer os.Remove(candidate.Name())
	_, writeErr := candidate.WriteString(next)
	closeErr := candidate.Close()
	if err = errors.Join(writeErr, closeErr); err != nil {
		return nil, err
	}
	if err = e.verifyPHPSession(ctx, r, candidate.Name(), settings); err != nil {
		return nil, err
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if latest := security.Hash(string(current)); latest != revision {
		return nil, &model.RevisionConflict{Revision: latest}
	}
	log("2/4 保存加密配置历史")
	history := base + "/history/main/" + revision + ".enc"
	if err = security.WriteAtomic(history, []byte(e.Vault.Encrypt(old)), 0600); err != nil {
		return nil, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	address := phpStatusAddress(r)
	if subject.Kind == "site" {
		address = filepath.Join(base, "run", "php.sock")
	}
	restore := func(cause error) (model.Config, error) {
		recovery, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		writeErr := writeOwnedAtomic(path, old, st.Mode().Perm())
		reloadErr := systemctl(recovery, func(string) {}, "reload", unit)
		healthErr := waitFPM(recovery, address)
		if writeErr != nil || reloadErr != nil || healthErr != nil {
			return nil, errors.New("会话配置应用失败，回退未完成，请检查 FPM 服务及配置历史")
		}
		return nil, fmt.Errorf("会话配置应用失败，已恢复原文件：%w", cause)
	}
	log("3/4 应用并重载所选 FPM 服务")
	if err = writeOwnedAtomic(path, []byte(next), 0600); err != nil {
		return nil, err
	}
	if err = systemctl(ctx, log, "reload", unit); err != nil {
		return restore(errors.New("FPM 重载失败"))
	}
	log("4/4 检查 FPM 与会话读写")
	if err = e.verifyFPMSession(ctx, address, base, settings); err != nil {
		return restore(err)
	}
	if handler == "redis" {
		subject.Config["sessionRedisInstanceId"] = p.String("instanceId")
	} else {
		delete(subject.Config, "sessionRedisInstanceId")
	}
	if handler == "memcached" {
		subject.Config["sessionMemcachedPassword"] = settings["memcached.sess_sasl_password"]
	} else {
		delete(subject.Config, "sessionMemcachedPassword")
	}
	if err = e.save(subject); err != nil {
		return restore(errors.New("会话依赖登记失败"))
	}
	return model.Config{"handler": handler, "configurationRevision": security.Hash(next), "linkedResources": []model.Resource{subject}}, nil
}

func phpSessionNamespace(text, handler, directory, id string) (string, bool) {
	value := phpIniValue(text, "session.save_path", "")
	if handler == "files" {
		return value, value == directory
	}
	if handler == "memcached" {
		value = phpIniValue(text, "memcached.sess_prefix", "")
	}
	if handler == "redis" {
		value = ""
		if u, err := url.Parse(phpIniValue(text, "session.save_path", "")); err == nil {
			value = u.Query().Get("prefix")
		}
	}
	return value, value == "ov."+id+"."
}

func inspectMemcachedSession(text string) model.Config {
	port := 11211
	path := phpIniValue(text, "session.save_path", "")
	if strings.HasPrefix(path, "127.0.0.1:") {
		if n, err := strconv.Atoi(strings.TrimPrefix(path, "127.0.0.1:")); err == nil && n > 0 && n <= 65535 {
			port = n
		}
	}
	username := phpIniValue(text, "memcached.sess_sasl_username", "")
	password := ""
	if phpIniValue(text, "memcached.sess_sasl_password", "") != "" {
		password = "••••••••"
	}
	return model.Config{"port": port, "saslEnabled": username != "", "saslUsername": username, "saslPassword": password}
}

func memcachedSessionSettings(p model.Config, old, id string) (map[string]string, error) {
	port, err := strconv.Atoi(fmt.Sprint(p["memcachedPort"]))
	if err != nil || port < 1 || port > 65535 {
		return nil, errors.New("memcached 端口须为 1–65535，仅连接本机 127.0.0.1")
	}
	user, password := "", ""
	if p.Bool("saslEnabled") {
		user, password = p.String("saslUsername"), p.String("saslPassword")
		if password == "••••••••" {
			password = phpIniValue(old, "memcached.sess_sasl_password", "")
		}
		for _, value := range []string{user, password} {
			if value == "" || len(value) > 256 || strings.ContainsAny(value, "\"\\") || strings.Contains(value, "${") || strings.IndexFunc(value, unicode.IsControl) >= 0 {
				return nil, errors.New("SASL 用户名和密码须为 1–256 字节，不能包含双引号、反斜杠、控制字符或 ${")
			}
		}
	}
	return map[string]string{"session.save_path": "127.0.0.1:" + strconv.Itoa(port), "memcached.sess_binary_protocol": "1", "memcached.sess_sasl_username": user, "memcached.sess_sasl_password": password, "memcached.sess_prefix": "ov." + id + ".", "memcached.sess_persistent": "0", "memcached.sess_connect_timeout": "2000", "memcached.sess_lock_retries": "3"}, nil
}

func patchSessionIni(text string, values map[string]string) string {
	// A final section sets the effective values while retaining unrelated comments.
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
		text = regexp.MustCompile(`(?m)^[\t ]*`+regexp.QuoteMeta(key)+`[\t ]*=[^\r\n]*`).ReplaceAllString(text, "")
	}
	sort.Strings(keys)
	text += "\n[Session]\n"
	for _, key := range keys {
		text += key + " = \"" + values[key] + "\"\n"
	}
	return text
}

// Only fixed PHP code is executed. Secrets travel through the private candidate and
// stdin; warnings/output never enter task logs, argv or the process environment.
func (e *Engine) verifyPHPSession(ctx context.Context, r model.Resource, path string, expected map[string]string) error {
	probe, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	input, _ := json.Marshal(model.Config{"values": expected, "token": security.ID()})
	err := run(probe, func(string) {}, "", nil, strings.NewReader(string(input)), filepath.Join(e.Root, "instances", r.ID, "runtime", "bin", "php"), "-c", path, "-d", "log_errors=0", "-d", "display_errors=0", "-r", sessionProbeCode)
	if err != nil {
		return errors.New("会话读写验证失败，请检查对应扩展、存储连接、SASL 支持和凭据；运行配置未通过验证")
	}
	return nil
}

const sessionProbeCode = `error_reporting(0); ini_set('log_errors','0'); ini_set('display_errors','0'); $input=json_decode(stream_get_contents(STDIN), true); $expected=$input['values'];
$fail=function($reason) { echo json_encode(['verified'=>false,'reason'=>$reason]); exit(1); };
foreach ($expected as $key=>$value) { $actual=(string)ini_get($key); if ($value==='0' && $actual==='') continue; if ($actual!==(string)$value) $fail('configuration'); }
$handler=ini_get('session.save_handler');
if ($handler!=='files' && !extension_loaded($handler)) $fail('extension');
if ($handler==='memcached' && ini_get('memcached.sess_sasl_username')!=='' && (!defined('Memcached::HAVE_SASL') || !Memcached::HAVE_SASL)) $fail('sasl');
ini_set('session.use_cookies','0'); ini_set('session.use_strict_mode','0'); session_cache_limiter('');
$id='ovprobe'.bin2hex(random_bytes(16)); $value=bin2hex(random_bytes(24)); session_id($id);
if (!session_start()) $fail('session-start'); $_SESSION['opsview_probe']=$value;
if (!session_write_close() || !session_start()) $fail('session-write');
$ok=isset($_SESSION['opsview_probe']) && hash_equals($value, $_SESSION['opsview_probe']);
if (!session_destroy() || !$ok) $fail('session-read'); echo json_encode(['verified'=>true,'token'=>$input['token']]);`

func (e *Engine) verifyFPMSession(ctx context.Context, address, base string, expected map[string]string) error {
	dir, err := safePath(base, "run")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	probe, err := os.CreateTemp(dir, ".session-probe-*.php")
	if err != nil {
		return err
	}
	defer os.Remove(probe.Name())
	// No expected values or credentials are written to this short-lived, fixed
	// script outside the public root. They travel over the private FPM channel.
	code := "<?php " + strings.Replace(sessionProbeCode, "stream_get_contents(STDIN)", "file_get_contents('php://input')", 1)
	_, writeErr := probe.WriteString(code)
	modeErr := probe.Chmod(0644)
	closeErr := probe.Close()
	if err = errors.Join(writeErr, modeErr, closeErr); err != nil {
		return err
	}
	token := security.ID()
	input, _ := json.Marshal(model.Config{"values": expected, "token": token})
	check, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	reason := "请检查服务及目录访问权限"
	for {
		body, err := fpmRequest(check, address, map[string]string{"REQUEST_METHOD": "POST", "SCRIPT_NAME": "/opsview-session-probe.php", "SCRIPT_FILENAME": probe.Name(), "REQUEST_URI": "/opsview-session-probe.php", "CONTENT_TYPE": "application/json", "CONTENT_LENGTH": strconv.Itoa(len(input))}, input)
		var result model.Config
		if err == nil && json.Unmarshal(body, &result) == nil && result.Bool("verified") && result.String("token") == token {
			return nil
		}
		if notice := map[string]string{"configuration": "有效配置尚未更新", "extension": "会话扩展未加载", "sasl": "扩展缺少 SASL 支持", "session-start": "无法建立会话", "session-write": "无法写入会话", "session-read": "无法读取或清理会话"}[result.String("reason")]; notice != "" {
			reason = notice
		}
		select {
		case <-check.Done():
			return fmt.Errorf("FPM 新配置或会话读写验证失败：%s", reason)
		case <-time.After(250 * time.Millisecond):
		}
	}
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
	switch phpIniValue(text, "session.save_handler", "files") {
	case "files":
		set("session.save_path", base+"/sessions")
	case "memcached":
		set("memcached.sess_prefix", "ov."+filepath.Base(base)+".")
	case "redis":
		if u, err := url.Parse(phpIniValue(text, "session.save_path", "")); err == nil && u.Scheme == "tcp" {
			query := u.Query()
			query.Set("prefix", "ov."+filepath.Base(base)+".")
			u.RawQuery = query.Encode()
			set("session.save_path", u.String())
		}
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
