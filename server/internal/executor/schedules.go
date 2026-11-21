package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"opsview/internal/logic"
	"opsview/internal/model"
	"opsview/internal/outbound"
	"opsview/internal/scheduling"
	"opsview/internal/security"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func (e *Engine) scheduleExecute(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	if op.Name == "schedule.probe" {
		return e.scheduleProbe(ctx, op.Params)
	}
	if op.Name == "schedule.validate-directory" {
		path := op.Params.String("path")
		if err := e.checkFilePath(path, true); err != nil {
			return nil, err
		}
		if e.criticalFilePath(path) && filepath.Clean(path) != "/www/wwwroot" {
			return nil, errors.New("默认目录不能是关键系统目录")
		}
		if op.Params.Bool("panelStorage") {
			base, err := e.preparePanelBackupDirectory(path)
			return model.Config{"path": base}, err
		}
		// A pinned-root walk rejects links and writable parents before creating defaults.
		base, err := e.backupBase(model.Resource{ID: ".opsview-check-" + security.ID(), Config: model.Config{"storageDirectory": path}})
		if err != nil {
			return nil, err
		}
		if err = os.Remove(base); err != nil {
			return nil, err
		}
		return model.Config{"path": path}, nil
	}
	if op.Name == "schedule.capabilities" {
		users := []string{"opsview-task"}
		if data, err := os.ReadFile("/etc/passwd"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				f := strings.Split(line, ":")
				if len(f) >= 7 {
					uid, _ := strconv.Atoi(f[2])
					if (uid >= 1000 && uid < 65534) || f[0] == "root" {
						users = append(users, f[0])
					}
				}
			}
		}
		interpreters := []string{}
		for _, path := range []string{"/usr/bin/python3", "/usr/local/bin/python3"} {
			if _, err := os.Stat(path); err == nil {
				interpreters = append(interpreters, path)
			}
		}
		return model.Config{"types": scheduling.Types, "users": users, "interpreters": interpreters}, nil
	}
	c := op.Resource.Config
	if err := scheduling.Validate(c); err != nil {
		return nil, err
	}
	timeout := c.Int("timeoutSeconds")
	if timeout == 0 {
		timeout = 3600
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	switch c.String("operation") {
	case "shell", "python":
		return e.runScheduleScript(ctx, op, log)
	case "http":
		body := strings.NewReader(c.String("body"))
		req, err := http.NewRequestWithContext(ctx, c.String("method"), c.String("url"), body)
		if err != nil {
			return nil, errors.New("请求配置无效")
		}
		var headers map[string]string
		b, _ := json.Marshal(c["headers"])
		_ = json.Unmarshal(b, &headers)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		log("请求开始（地址参数、请求头及响应正文不写入日志）")
		resp, err := outbound.Client(time.Duration(timeout)*time.Second, c.Bool("allowPrivate")).Do(req)
		if err != nil {
			return nil, errors.New("HTTP 请求失败或超时，请检查目标和访问策略")
		}
		defer resp.Body.Close()
		n, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != c.Int("expectedStatus") {
			return model.Config{"status": resp.StatusCode}, fmt.Errorf("实际状态 %d，预期 %d", resp.StatusCode, c.Int("expectedStatus"))
		}
		return model.Config{"status": resp.StatusCode, "responseBytesRead": n}, nil
	case "directory":
		return e.directoryBackup(ctx, op, log)
	case "cleanup":
		return e.cleanupScheduledLogs(ctx, c, log)
	case "rotate":
		return e.rotateScheduledSiteLogs(ctx, op, log)
	case "time-sync":
		if _, err := exec.LookPath("timedatectl"); err != nil {
			return nil, errors.New("系统未提供 timedatectl")
		}
		err := run(ctx, log, "", nil, nil, "timedatectl", "set-ntp", "true")
		if err != nil {
			return nil, err
		}
		state, err := output(ctx, "timedatectl", "show", "--property=NTPSynchronized", "--value")
		if err != nil || strings.TrimSpace(state) != "yes" {
			return nil, errors.New("已启用时间同步，但尚未同步成功，请检查系统时间服务")
		}
		return model.Config{"synchronized": true}, nil
	}
	return nil, errors.New("任务类型不可由执行器执行")
}
func (e *Engine) runScheduleScript(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	c := op.Resource.Config
	if c.String("user") == "root" && c.String("rootApproval") != scheduling.Digest(c) {
		return nil, errors.New("缺少当前修订的 root 授权")
	}
	name := c.String("user")
	if name == "opsview-task" {
		if _, err := user.Lookup(name); err != nil {
			if err = run(ctx, log, "", nil, nil, "useradd", "--system", "--no-create-home", "--home-dir", "/nonexistent", "--shell", "/usr/sbin/nologin", name); err != nil {
				return nil, err
			}
		}
	}
	u, err := user.Lookup(name)
	if err != nil {
		return nil, errors.New("执行用户不存在")
	}
	uid, _ := strconv.Atoi(u.Uid)
	if name != "root" && name != "opsview-task" && (uid < 1000 || uid >= 65534) {
		return nil, errors.New("请选择普通用户或专用低权限用户；root 必须明确授权")
	}
	if uid == 0 && name != "root" {
		return nil, errors.New("UID 0 用户须使用 root 身份并重新验证")
	}

	if err := e.checkFilePath(c.String("workingDirectory"), false); err != nil {
		return nil, err
	}
	dir := filepath.Join(e.Root, "jobs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	env, err := os.CreateTemp(dir, "environment-")
	if err != nil {
		return nil, err
	}
	defer os.Remove(env.Name())
	_ = env.Chmod(0600)
	var values map[string]string
	raw, _ := json.Marshal(c["environment"])
	if c["environment"] != nil && json.Unmarshal(raw, &values) != nil {
		env.Close()
		return nil, errors.New("私密变量须为字符串键值对象")
	}
	for k, v := range values {
		if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(k) || strings.ContainsAny(v, "\x00\r\n") {
			env.Close()
			return nil, errors.New("环境变量名称或值无效")
		}
		quoted := strings.ReplaceAll(strings.ReplaceAll(v, "\\", "\\\\"), "\"", "\\\"")
		fmt.Fprintf(env, "%s=\"%s\"\n", k, quoted)
	}
	if err = env.Close(); err != nil {
		return nil, err
	}
	unit := "opsview-job-" + security.ID()
	args := []string{"--quiet", "--pipe", "--wait", "--collect", "--unit=" + unit, "--uid=" + name, "--working-directory=" + c.String("workingDirectory"), "--property=EnvironmentFile=" + env.Name(), "--property=RuntimeMaxSec=" + strconv.Itoa(c.Int("timeoutSeconds")), "--property=TimeoutStopSec=10s", "--property=KillMode=control-group", "--property=StandardOutput=inherit", "--property=StandardError=inherit", "--property=UMask=0077", "--property=NoNewPrivileges=yes", "--property=TasksMax=256"}
	if name != "root" {
		args = append(args, "--property=PrivateTmp=yes", "--property=ProtectSystem=full", "--property=ProtectKernelTunables=yes", "--property=ProtectControlGroups=yes")
	}
	var params []string
	raw, _ = json.Marshal(c["args"])
	_ = json.Unmarshal(raw, &params)
	if c.String("operation") == "shell" {
		args = append(args, "/bin/bash", "-s", "--")
	} else {
		if _, err := os.Stat(c.String("interpreter")); err != nil {
			return nil, errors.New("Python 解释器不存在")
		}
		args = append(args, c.String("interpreter"), "-")
	}
	args = append(args, params...)
	defer func() {
		stop, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		_ = exec.CommandContext(stop, "systemctl", "stop", unit+".service").Run()
	}()
	started := time.Now()
	log("以 " + name + " 执行脚本，输出进入本次任务日志")
	err = run(ctx, func(s string) { log(security.MaskLog(s, c)) }, "", nil, strings.NewReader(c.String("script")), "systemd-run", args...)
	result := model.Config{"durationMs": time.Since(started).Milliseconds(), "exitCode": 0}
	if ctx.Err() != nil {
		result["exitCode"] = -1
		result["interrupted"] = true
		return result, ctx.Err()
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			result["exitCode"] = exit.ExitCode()
		} else {
			result["exitCode"] = -1
		}
		return result, err
	}
	return result, nil
}
func (e *Engine) cleanupScheduledLogs(ctx context.Context, c model.Config, log func(string)) (model.Config, error) {
	root, err := e.filePath(c.String("path"), true)
	if err != nil {
		return nil, err
	}
	defer root.close()
	if e.criticalFilePath(root.path) {
		return nil, errors.New("不允许清理系统关键目录")
	}
	// Only explicitly selected log directories; only regular .log/.log.gz files, never symlink targets.
	if !strings.Contains(filepath.Base(root.path), "log") {
		return nil, errors.New("请选择名称包含 log 的日志目录")
	}
	directory, err := root.root.OpenRoot(root.name)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	cutoff := time.Now().Add(-time.Duration(c.Int("olderDays")) * 24 * time.Hour)
	count := 0
	err = filepath.WalkDir(root.path, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			if path != root.path {
				return filepath.SkipDir
			}
			return nil
		}
		if !(strings.HasSuffix(d.Name(), ".log") || strings.HasSuffix(d.Name(), ".log.gz")) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || !info.ModTime().Before(cutoff) {
			return nil
		}
		count++
		if c.Bool("preview") {
			return nil
		}
		return directory.Remove(d.Name())
	})
	log(fmt.Sprintf("符合条件的日志文件：%d", count))
	return model.Config{"count": count, "preview": c.Bool("preview")}, err
}
func (e *Engine) rotateScheduledSiteLogs(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	e.siteMu.Lock()
	defer e.siteMu.Unlock()
	var target model.Resource
	raw, _ := json.Marshal(op.Params["target"])
	if json.Unmarshal(raw, &target) != nil {
		return nil, errors.New("站点无效")
	}
	site, err := e.load(target.ID)
	if err != nil || site.Kind != "site" {
		return nil, errors.New("站点未部署")
	}
	nginx, err := e.load(site.Config.String("instanceId"))
	if err != nil {
		return nil, err
	}
	rotated := 0
	dir := filepath.Join(e.Root, "sites", site.ID, "logs")
	for _, name := range []string{"access.log", "error.log"} {
		path := filepath.Join(dir, name)
		f, err := e.openSoftwareLog(path, os.O_RDONLY)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		st, err := f.Stat()
		f.Close()
		if err != nil {
			return nil, err
		}
		if st.Size() == 0 || op.Resource.Config.Int("minBytes") > 0 && st.Size() < int64(op.Resource.Config.Int("minBytes")) {
			continue
		}
		archiveDir := filepath.Join(e.Root, "log-archives", nginx.ID, site.ID, name)
		if _, err = e.manageLogFile(ctx, nginx, model.Config{"confirmed": true, "baseRevision": logRevision(st)}, "rotate-log", log, path, serviceName(nginx), archiveDir); err != nil {
			return nil, err
		}
		rotated++
		if op.Resource.Config.Bool("compress") {
			if err = compressRotatedLogs(ctx, archiveDir); err != nil {
				return nil, err
			}
		}
		keep := op.Resource.Config.Int("keepLogs")
		if keep == 0 {
			keep = 30
		}
		files, _ := filepath.Glob(filepath.Join(archiveDir, "*"))
		for len(files) > keep {
			if err = os.Remove(files[0]); err != nil {
				return nil, err
			}
			files = files[1:]
		}
	}
	return model.Config{"rotated": rotated}, nil
}

func (e *Engine) scheduleProbe(ctx context.Context, p model.Config) (model.Config, error) {
	var target model.Resource
	b, _ := json.Marshal(p["target"])
	if json.Unmarshal(b, &target) != nil {
		return nil, errors.New("目标无效")
	}
	target, err := e.load(target.ID)
	if err != nil {
		return nil, err
	}
	if p.String("type") == "service" {
		state, err := output(ctx, "systemctl", "is-active", serviceName(target))
		return model.Config{"healthy": err == nil && strings.TrimSpace(state) == "active"}, nil
	}
	if target.Kind != "site" {
		return nil, errors.New("目标不是网站")
	}
	inst, err := e.load(target.Config.String("instanceId"))
	if err != nil {
		return nil, err
	}
	bindings, err := logic.SiteBindings(target.Config, inst.Config.Int("port"))
	if err != nil {
		return nil, err
	}
	domains := []string{}
	if len(bindings) > 0 {
		host := bindings[0].Host
		if strings.HasPrefix(host, "*.") {
			return nil, errors.New("可用性告警需要具体域名作为主域名")
		}
		port := bindings[0].Port
		if target.Config.Bool("tls") {
			port = 443
		}
		domains = append(domains, net.JoinHostPort(host, strconv.Itoa(port)))
	}
	if len(domains) == 0 {
		return nil, errors.New("网站没有域名")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", func() string {
		scheme := "http"
		if target.Config.Bool("tls") {
			scheme = "https"
		}
		return scheme + "://" + domains[0]
	}(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := outbound.Client(8*time.Second, false).Do(req)
	if err != nil {
		return model.Config{"healthy": false}, nil
	}
	defer resp.Body.Close()
	return model.Config{"healthy": resp.StatusCode >= 200 && resp.StatusCode < 400}, nil
}
