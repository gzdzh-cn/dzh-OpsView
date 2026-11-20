package executor

import (
	"archive/tar"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/shirou/gopsutil/v4/disk"
	"io"
	"net"
	"net/http"
	"net/url"
	"opsview/internal/catalog"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func (e *Engine) instance(ctx context.Context, op model.Operation, log func(string)) (result model.Config, retErr error) {
	action := strings.TrimPrefix(op.Name, "instance.")
	r := op.Resource
	if action == "install" {
		if r.Config.Bool("stageRuntime") {
			return nil, errors.New("禁止外部设置暂存安装参数")
		}
		if r.Status == "error" || r.Status == "uninstalled" || r.Config.Bool("retainedData") {
			return nil, errors.New("请先卸载并清理保留记录后，再新建实例安装")
		}
		if r.Config.String("mode") != "native" {
			return nil, errors.New("软件市场仅支持原生安装")
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		if previous, err := e.load(r.ID); err == nil {
			if previous.Config.Bool("installationIncomplete") {
				return nil, errors.New("此实例安装未完成；请先卸载服务（保留现场数据），再新建实例安装，禁止覆盖残留目录")
			}
			return nil, errors.New("实例已安装，请使用启动或重启操作")
		}
		entry, ok := e.catalogEntry(r.Config.String("catalogId"))
		if !ok {
			return nil, errors.New("执行器未信任此软件版本")
		}
		if runtime.GOARCH != "amd64" {
			return nil, errors.New("首版安装配方仅支持 Linux amd64")
		}
		if err := checkOS(); err != nil {
			return nil, err
		}
		r.Name = entry.Software + "-" + entry.Version
		if !e.allowUnverifiedNative {
			if reason := catalog.InstallationReason(entry, r.Config.String("mode"), r.Config.String("installMethod"), hostSystem()); reason != "" {
				return nil, errors.New(reason)
			}
		}
		if (entry.Software == "mysql" || entry.Software == "postgresql" || entry.Software == "redis") && (len(r.Config.String("password")) < 12 || strings.ContainsAny(r.Config.String("password"), "\x00\r\n")) {
			return nil, errors.New("数据库管理员密码至少 12 位且不得包含换行")
		}
		for _, other := range e.states() {
			if other.Kind == "instance" && other.Config.String("software") == entry.Software && other.Config.String("version") == entry.Version {
				return nil, errors.New("该软件的准确版本已存在")
			}
			if entry.Software != "nodejs" && other.Kind == "instance" && other.Config.Int("port") == r.Config.Int("port") && !(entry.Software == "nginx" && other.Config.String("software") == "nginx") {
				return nil, errors.New("端口已被其他实例使用")
			}
			if entry.Software == "nginx" && other.Kind == "instance" && other.Config.String("software") == "nginx" {
				return nil, errors.New("只能安装一个受管 Nginx，请从现有实例更换版本")
			}
		}

		usage, diskErr := disk.Usage(e.Root)
		if diskErr != nil {
			return nil, diskErr
		}
		required := uint64(2 << 30)
		if r.Config.String("mode") == "native" {
			required = 6 << 30
		}
		if usage.Free < required {
			return nil, fmt.Errorf("可用磁盘不足，安装至少需要 %d GiB", required>>30)
		}
		if err := catalog.Validate(entry); err != nil {
			return nil, err
		}
		var err error
		if !r.Config.Bool("deferStart") && entry.Software != "nodejs" {
			conn, er := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(r.Config.Int("port"))))
			if er != nil {
				return nil, errors.New("实例端口已被占用")
			}
			_ = conn.Close()
		}
		r.Config["software"] = entry.Software
		r.Config["version"] = entry.Version
		r.Config["catalogId"] = entry.ID
		r.Config["bind"] = "127.0.0.1"
		base := filepath.Join(e.Root, "instances", r.ID)
		if err = prepareInstanceLayout(base); err != nil {
			return nil, err
		}

		log("环境预检通过，开始安装 " + entry.Software + " " + entry.Version)
		r.Status = "installing"
		if err = e.save(r); err != nil {
			return nil, err
		}
		defer func() {
			if retErr != nil {
				r.Status = "error"
				r.Config["installationIncomplete"] = true
				_ = e.save(r)
				log("安装未完成，已保留现场；请卸载服务并保留数据，再新建实例重试")
			}
		}()
		if r.Config.String("mode") == "docker" {
			err = e.installContainer(ctx, r, entry, base, log)
		} else {
			err = e.installNative(ctx, r, entry, base, log)
		}
		if err != nil {
			return nil, err
		}
		if !r.Config.Bool("deferStart") && entry.Software != "nodejs" {
			if err = waitPort(ctx, r.Config.Int("port")); err != nil {
				return nil, err
			}
		}
		if persisted, er := e.load(r.ID); er == nil {
			if v, ok := persisted.Config["statusPort"]; ok {
				r.Config["statusPort"] = v
			}
		}
		r.Status = "ready"
		if r.Config.Bool("deferStart") {
			r.Status = "stopped"
			log("实例已安装并保持停止；可在设置中启动服务或切换入口")
		}
		if err = e.save(r); err != nil {
			return nil, err
		}
		if persisted, er := e.load(r.ID); er == nil {
			if v, ok := persisted.Config["statusPort"]; ok {
				r.Config["statusPort"] = v
			}
		}
		_ = e.save(r)
		result := model.Config{"software": entry.Software, "version": entry.Version, "bind": "127.0.0.1", "runtimeStatus": r.Status, "installedAt": time.Now().UTC().Format(time.RFC3339), "statusPort": r.Config["statusPort"], "installMethod": r.Config["installMethod"]}
		if entry.Software == "nodejs" {
			result["executablePath"] = base + "/runtime/bin/node"
			r.Config["executablePath"] = result["executablePath"]
			if err = e.save(r); err != nil {
				return nil, err
			}
		}
		return result, nil
	}
	var err error
	requested := r
	r, err = e.load(r.ID)
	if err != nil {
		if action == "uninstall" && errors.Is(err, os.ErrNotExist) {
			// Preflight failures have a reserved version but no executor state.
			// Release only the service lifecycle; retained files stay untouched.
			unit := filepath.Join("/etc/systemd/system", serviceName(requested))
			if _, unitErr := os.Lstat(unit); !os.IsNotExist(unitErr) {
				return nil, errors.New("存在未登记的服务文件，请先诊断现场后清理")
			}
			log("此安装尚未创建服务，保留现有文件；可在面板清理记录后重试")
			return model.Config{}, nil
		}
		return nil, err
	}
	if r.Config.String("software") == "nginx" {
		e.siteMu.Lock()
		defer e.siteMu.Unlock()
	}
	if action == "uninstall" {
		if r.Config.String("software") == "nodejs" {
			log("运行时文件保留，以免影响已部署的应用")
			return model.Config{}, e.forget(r.ID)
		}
		for _, other := range e.states() {
			if other.Config.String("instanceId") == r.ID || other.Config.String("phpInstanceId") == r.ID || other.Config.String("sessionRedisInstanceId") == r.ID {
				return nil, errors.New("执行器检测到关联资源，禁止卸载")
			}
		}
		if r.Config.String("mode") == "docker" {
			_, err = dockerRequest(ctx, "DELETE", "/containers/opsview-"+r.ID+"?force=true", nil)
			var apiError *dockerAPIError
			if errors.As(err, &apiError) && apiError.code == 404 {
				err = nil
			}
		} else {
			_ = systemctl(ctx, log, "stop", serviceName(r))
			_ = systemctl(ctx, log, "disable", serviceName(r))
			err = os.Remove(filepath.Join("/etc/systemd/system", serviceName(r)))
			if err == nil || os.IsNotExist(err) {
				err = run(ctx, log, "", nil, nil, "systemctl", "daemon-reload")
			}
		}
		if err != nil {
			return nil, err
		}
		log("已卸载服务，原始数据保留在 instances/" + r.ID)
		return model.Config{}, e.forget(r.ID)
	}
	if r.Config.String("software") == "php" && (action == "start" || action == "stop" || action == "restart") {
		return e.controlPHP(ctx, r, action, op.Params, log)
	}
	if action != "start" && action != "stop" && action != "restart" {
		return nil, errors.New("实例操作无效")
	}
	if r.Config.String("software") == "nodejs" {
		return nil, errors.New("Node.js 为运行时，不需要启动或停止服务")
	}
	if action != "stop" && r.Config.String("software") == "nginx" {
		for _, other := range e.states() {
			if other.ID != r.ID && other.Kind == "instance" && other.Config.String("software") == "nginx" && other.Status != "stopped" {
				return nil, errors.New("请先停止当前 Nginx 入口，再启动此版本")
			}
		}
	}
	if r.Config.String("mode") == "docker" {
		_, err = dockerRequest(ctx, "POST", "/containers/opsview-"+r.ID+"/"+action, nil)
	} else {

		if r.Config.String("software") == "nginx" {
			mode := "enable"
			if action == "stop" {
				mode = "disable"
			}
			if err = systemctl(ctx, log, mode, serviceName(r)); err != nil {
				return nil, err
			}
		}
		err = systemctl(ctx, log, action, serviceName(r))
	}
	if err == nil && action != "stop" {
		err = waitPort(ctx, r.Config.Int("port"))
	}
	if err == nil {
		r.Status = "ready"
		if action == "stop" {
			r.Status = "stopped"
		}
		err = e.save(r)
	}
	return model.Config{}, err
}
func checkOS() error {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return err
	}
	s := string(b)
	valid := (strings.Contains(s, "ID=ubuntu") && (strings.Contains(s, `VERSION_ID="22.04"`) || strings.Contains(s, `VERSION_ID="24.04"`))) || (strings.Contains(s, "ID=debian") && (strings.Contains(s, `VERSION_ID="12"`) || strings.Contains(s, `VERSION_ID="13"`)))
	if !valid {
		return errors.New("仅支持 Ubuntu 22.04/24.04、Debian 12/13")
	}
	return nil
}
func waitPort(ctx context.Context, port int) error {
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
		if err == nil {
			c.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return errors.New("服务健康检查超时，请检查实例日志；保留安装现场以供诊断")
}
func (e *Engine) installContainer(ctx context.Context, r model.Resource, entry catalog.Entry, base string, log func(string)) error {
	if entry.Image == "" {
		return errors.New("该版本没有已固定摘要的容器镜像")
	}
	if err := pullImage(ctx, entry.Image, log); err != nil {
		return err
	}
	port := r.Config.Int("port")
	env := []string{}
	binds := []string{}
	target := 0
	command := []string{}
	host := map[string]any{"RestartPolicy": map[string]any{"Name": "unless-stopped"}, "LogConfig": map[string]any{"Type": "json-file", "Config": map[string]string{"max-size": "10m", "max-file": "3"}}}
	switch entry.Software {
	case "nginx":
		if err := e.writeNginxBase(r, base); err != nil {
			return err
		}
		host["NetworkMode"] = "host"
		binds = []string{base + "/conf:" + base + "/conf:ro", base + "/logs:" + base + "/logs", e.Root + "/sites:" + e.Root + "/sites:ro"}
		command = []string{"nginx", "-c", base + "/conf/nginx.conf", "-g", "daemon off;"}
	case "mysql":
		if len(r.Config.String("password")) < 12 {
			return errors.New("数据库管理员密码至少 12 位")
		}
		target = 3306
		host["NetworkMode"] = "host"
		command = []string{"mysqld", "--port=" + strconv.Itoa(port), "--bind-address=" + r.Config.String("bind"), "--mysqlx=OFF"}
		env = []string{"MYSQL_ROOT_PASSWORD=" + r.Config.String("password")}
		binds = []string{base + "/data:/var/lib/mysql"}
	case "postgresql":
		if len(r.Config.String("password")) < 12 {
			return errors.New("数据库管理员密码至少 12 位")
		}
		target = 5432
		host["NetworkMode"] = "host"
		command = []string{"postgres", "-p", strconv.Itoa(port), "-c", "listen_addresses=" + r.Config.String("bind")}
		env = []string{"POSTGRES_PASSWORD=" + r.Config.String("password"), "PGDATA=/var/lib/postgresql/data/pgdata"}
		binds = []string{base + "/data:/var/lib/postgresql/data"}
	default:
		return errors.New("不支持的核心容器软件")
	}
	host["Binds"] = binds
	if target > 0 && host["NetworkMode"] != "host" {
		host["PortBindings"] = map[string]any{strconv.Itoa(target) + "/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": strconv.Itoa(port)}}}
	}
	body := map[string]any{"Image": entry.Image, "Env": env, "HostConfig": host, "Labels": map[string]string{"opsview.managed": "true", "opsview.resource": r.ID}}
	if len(command) > 0 {
		body["Cmd"] = command
	}
	if target > 0 {
		body["ExposedPorts"] = map[string]any{strconv.Itoa(target) + "/tcp": map[string]any{}}
	}
	if _, err := dockerRequest(ctx, "POST", "/containers/create?name=opsview-"+r.ID, body); err != nil {
		return err
	}
	if r.Config.Bool("deferStart") {
		return nil
	}
	_, err := dockerRequest(ctx, "POST", "/containers/opsview-"+r.ID+"/start", nil)
	return err
}
func (e *Engine) installNative(ctx context.Context, r model.Resource, entry catalog.Entry, base string, log func(string)) error {
	if entry.DisabledReason != "" {
		return errors.New(entry.DisabledReason)
	}
	if entry.Source == "" || len(entry.SHA256) != 64 {
		return errors.New("此版本缺少已验证的原生安装源")
	}
	if r.Config.String("installMethod") == "binary" && entry.Software != "mysql" && entry.Software != "nodejs" {
		return e.installPrecompiled(ctx, r, entry, base, log)
	}
	if entry.Software == "nginx" || entry.Software == "php" || entry.Software == "redis" || entry.Software == "postgresql" {
		return e.installSource(ctx, r, entry, base, log)
	}
	if entry.Software == "nodejs" {
		if err := run(ctx, log, "", []string{"DEBIAN_FRONTEND=noninteractive"}, nil, "apt-get", "update"); err != nil {
			return err
		}
		if err := run(ctx, log, "", []string{"DEBIAN_FRONTEND=noninteractive"}, nil, "apt-get", "install", "-y", "--no-install-recommends", "ca-certificates", "xz-utils", "libstdc++6", "libatomic1"); err != nil {
			return err
		}
		archive := filepath.Join(e.Root, "downloads", entry.ID+".archive")
		if err := download(ctx, entry.Source, entry.SHA256, archive, log); err != nil {
			return err
		}
		if err := extractArchive(ctx, archive, entry.Source, base+"/runtime", true); err != nil {
			return err
		}
		if err := prepareRuntimePermissions(base + "/runtime"); err != nil {
			return err
		}
		return run(ctx, log, "", nil, nil, base+"/runtime/bin/node", "--version")
	}
	if err := run(ctx, log, "", []string{"DEBIAN_FRONTEND=noninteractive"}, nil, "apt-get", "update"); err != nil {
		return err
	}
	aio := "libaio1t64"
	if strings.Contains(readText("/etc/os-release"), `VERSION_ID="22.04"`) || strings.Contains(readText("/etc/os-release"), `VERSION_ID="12"`) {
		aio = "libaio1"
	}
	deps := []string{"install", "-y", "--no-install-recommends", "ca-certificates", "xz-utils", aio, "libnuma1", "libncurses6", "libkeyutils1", "libsasl2-2"}
	if err := run(ctx, log, "", []string{"DEBIAN_FRONTEND=noninteractive"}, nil, "apt-get", deps...); err != nil {
		return err
	}
	archive := filepath.Join(e.Root, "downloads", entry.ID+".archive")
	if err := download(ctx, entry.Source, entry.SHA256, archive, log); err != nil {
		return err
	}
	source := filepath.Join(base, "source")
	if err := os.MkdirAll(source, 0755); err != nil {
		return err
	}
	if err := extractArchive(ctx, archive, entry.Source, source, true); err != nil {
		return err
	}
	runtimePath := filepath.Join(base, "runtime")
	user := "ov_" + r.ID[:min(12, len(r.ID))]
	if entry.Software != "nginx" {
		if err := ensureUser(ctx, user, base, log); err != nil {
			return err
		}
	}
	switch entry.Software {
	case "mysql":
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, item := range entries {
			if err = os.Rename(filepath.Join(source, item.Name()), filepath.Join(runtimePath, item.Name())); err != nil {
				return err
			}
		}
		if err = e.prepareMySQLLegacyClients(ctx, entry.Version, base, log); err != nil {
			return err
		}
		if err = prepareMySQLAIO(ctx, runtimePath, log); err != nil {
			return err
		}
		// Compatibility directories are created under the executor umask too.
		if err = prepareRuntimePermissions(runtimePath); err != nil {
			return err
		}
		data := filepath.Join(base, "data")
		if err = run(ctx, log, "", nil, nil, "chown", "-R", user+":"+user, data); err != nil {
			return err
		}
		conf := fmt.Sprintf("[mysqld]\nbasedir=%s\ndatadir=%s\nport=%d\nbind-address=127.0.0.1\nsocket=%s/mysql.sock\nloose-mysqlx=0\nslow-query-log-file=%s/logs/slow.log\nlog-error=%s/logs/mysql.log\n", runtimePath, data, r.Config.Int("port"), data, base, base)
		if err = security.WriteAtomic(base+"/conf/my.cnf", []byte(conf), 0644); err != nil {
			return err
		}
		if err = run(ctx, log, "", nil, nil, "chown", "-R", user+":"+user, base+"/logs"); err != nil {
			return err
		}
		if err = run(ctx, log, "", nil, nil, runtimePath+"/bin/mysqld", "--defaults-file="+base+"/conf/my.cnf", "--initialize-insecure", "--user="+user); err != nil {
			return err
		}
		if err = installUnit(ctx, r, fmt.Sprintf("User=%s\nGroup=%s\nExecStart=%s/bin/mysqld --defaults-file=%s/conf/my.cnf\n", user, user, runtimePath, base), log); err != nil {
			return err
		}
		if err = waitPort(ctx, r.Config.Int("port")); err != nil {
			return err
		}
		password := r.Config.String("password")
		if len(password) < 12 {
			return errors.New("数据库管理员密码至少 12 位")
		}
		sql := "ALTER USER 'root'@'localhost' IDENTIFIED BY " + sqlLiteral(password) + ";"
		return run(ctx, log, "", nil, strings.NewReader(sql), runtimePath+"/bin/mysql", "--protocol=TCP", "-h127.0.0.1", "-P"+strconv.Itoa(r.Config.Int("port")), "-uroot")
	}
	return errors.New("不支持的原生安装配方")
}
func readText(path string) string { b, _ := os.ReadFile(path); return string(b) }
func ensureUser(ctx context.Context, user, home string, log func(string)) error {
	if _, err := output(ctx, "id", "-u", user); err == nil {
		return nil
	}
	return run(ctx, log, "", nil, nil, "useradd", "--system", "--home-dir", home, "--shell", "/usr/sbin/nologin", user)
}
func serviceUnitContent(r model.Resource, body string) string {
	limits := ""
	if r.Kind == "site-process" {
		limits = "StartLimitIntervalSec=60\nStartLimitBurst=5\n"
	}
	return "[Unit]\n" + limits + "Description=OpsView " + r.Kind + " " + r.ID + "\nAfter=network.target\n[Service]\n" + body + "Restart=on-failure\nRestartSec=3\nTimeoutStartSec=180\nUMask=0027\n[Install]\nWantedBy=multi-user.target\n"
}
func installUnit(ctx context.Context, r model.Resource, body string, log func(string)) error {
	unit := serviceUnitContent(r, body)
	if err := security.WriteAtomic(filepath.Join("/etc/systemd/system", serviceName(r)), []byte(unit), 0644); err != nil {
		return err
	}
	if err := run(ctx, log, "", nil, nil, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if r.Config.Bool("deferStart") {
		if err := systemctl(ctx, log, "stop", serviceName(r)); err != nil {
			return err
		}
		return run(ctx, log, "", nil, nil, "systemctl", "disable", serviceName(r))
	}
	if err := run(ctx, log, "", nil, nil, "systemd-analyze", "verify", filepath.Join("/etc/systemd/system", serviceName(r))); err != nil {
		return err
	}
	if err := systemctl(ctx, log, "enable", serviceName(r)); err != nil {
		return err
	}
	return systemctl(ctx, log, "restart", serviceName(r))
}
func download(ctx context.Context, source, expected, path string, log func(string)) error {
	u, err := url.Parse(source)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return errors.New("安装源必须使用 HTTPS")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", source, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; OpsViewInstaller)")
	client := &http.Client{Timeout: 30 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if r.URL.Scheme != "https" || len(via) > 5 {
			return errors.New("不允许的下载重定向")
		}
		return nil
	}}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("下载失败：HTTP %d", res.StatusCode)
	}
	f, err := os.CreateTemp(filepath.Dir(path), "download-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	h := sha256.New()
	log("下载并校验安装包")
	n, err := io.Copy(io.MultiWriter(f, h, &downloadProgress{log: log, total: res.ContentLength, last: time.Now()}), io.LimitReader(res.Body, 2<<30+1))
	ce := f.Close()
	if err != nil {
		return err
	}
	if ce != nil {
		return ce
	}
	if n > 2<<30 {
		return errors.New("安装包超过 2 GiB")
	}
	if hex.EncodeToString(h.Sum(nil)) != expected {
		return errors.New("安装包 SHA-256 校验失败")
	}
	return os.Rename(tmp, path)
}
func extractArchive(ctx context.Context, path, source, dest string, strip bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var reader io.Reader = f
	var cmd *exec.Cmd
	if strings.Contains(source, ".tar.gz") || strings.Contains(source, ".tgz") {
		g, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer g.Close()
		reader = g
	} else if strings.Contains(source, ".tar.bz2") {
		reader = bzip2.NewReader(f)
	} else if strings.Contains(source, ".tar.xz") {
		cmd = exec.CommandContext(ctx, "xz", "-dc", path)
		out, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		if err = cmd.Start(); err != nil {
			return err
		}
		defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
		reader = out
	} else {
		return errors.New("不支持的归档格式")
	}
	tr := tar.NewReader(reader)
	var total int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := h.Name
		if strip {
			parts := strings.SplitN(name, "/", 2)
			if len(parts) < 2 || parts[1] == "" {
				continue
			}
			name = parts[1]
		}
		p, err := safePath(dest, name)
		if err != nil {
			return err
		}
		if h.Size < 0 || h.Size > 2<<30 || total+h.Size > 4<<30 {
			return errors.New("解压内容超过限制")
		}
		total += h.Size
		switch h.Typeflag {
		case tar.TypeDir:
			if err = os.MkdirAll(p, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err = os.MkdirAll(filepath.Dir(p), 0755); err != nil {
				return err
			}
			out, err := os.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(h.Mode)&0755)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, tr)
			ce := out.Close()
			if err != nil {
				return err
			}
			if ce != nil {
				return ce
			}
			if !h.ModTime.IsZero() {
				if err = os.Chtimes(p, h.ModTime, h.ModTime); err != nil {
					return err
				}
			}
		case tar.TypeLink:
			targetName := h.Linkname
			if filepath.IsAbs(targetName) {
				return errors.New("归档含绝对硬链接")
			}
			if strip {
				parts := strings.SplitN(targetName, "/", 2)
				if len(parts) != 2 {
					return errors.New("归档硬链接目标无效")
				}
				targetName = parts[1]
			}
			target, er := safePath(dest, targetName)
			if er != nil {
				return er
			}
			st, er := os.Lstat(target)
			if er != nil || !st.Mode().IsRegular() {
				return errors.New("归档硬链接只允许指向已解压的普通文件")
			}
			if er = os.MkdirAll(filepath.Dir(p), 0755); er != nil {
				return er
			}
			if er = os.Link(target, p); er != nil {
				return er
			}
		case tar.TypeSymlink:
			if filepath.IsAbs(h.Linkname) {
				return errors.New("归档含绝对符号链接")
			}
			target := filepath.Clean(filepath.Join(filepath.Dir(name), h.Linkname))
			if _, err = safePath(dest, target); err != nil {
				return err
			}
			if err = os.MkdirAll(filepath.Dir(p), 0755); err != nil {
				return err
			}
			if err = os.Symlink(h.Linkname, p); err != nil {
				return err
			}
		default:
			return errors.New("归档含不支持的特殊文件")
		}
	}
	return nil
}

type downloadProgress struct {
	log             func(string)
	total, received int64
	last            time.Time
}

func (p *downloadProgress) Write(b []byte) (int, error) {
	p.received += int64(len(b))
	if time.Since(p.last) >= 3*time.Second {
		p.last = time.Now()
		if p.total > 0 {
			p.log(fmt.Sprintf("下载 %d / %d KiB", p.received/1024, p.total/1024))
		} else {
			p.log(fmt.Sprintf("已下载 %d KiB", p.received/1024))
		}
	}
	return len(b), nil
}
