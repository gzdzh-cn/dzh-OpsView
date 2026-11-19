package executor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"opsview/internal/hostfirewall"
	"opsview/internal/logic"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Engine struct {
	Root                  string
	PanelConfigPath       string
	PanelCertificatePath  string
	TrustDir              string
	SocketGID             int
	socketPath            string
	Vault                 *security.Vault
	fileMu                sync.Mutex
	mu                    sync.Mutex
	siteMu                sync.Mutex
	runtimeMu             sync.Mutex
	firewallMu            sync.Mutex
	firewallClient        *hostfirewall.Client
	firewallConfigRoot    string
	countryTransport      http.RoundTripper
	sshMu                 sync.Mutex
	sshConfigPath         string // overridden only by executor tests
	cancel                context.CancelFunc
	allowUnverifiedNative bool // only set by disposable native acceptance tests
}

func (e *Engine) Initialize() error {
	if runtime.GOOS != "linux" {
		return errors.New("系统执行器仅支持 Linux；macOS 请使用面板开发模式")
	}
	if os.Geteuid() != 0 {
		return errors.New("执行器必须以 root 运行")
	}
	if err := os.MkdirAll(e.Root, 0711); err != nil {
		return err
	}
	if err := os.Chmod(e.Root, 0711); err != nil {
		return err
	}
	for _, p := range []string{"state", "instances", "sites", "apps", "compose", "backups", "plugins", "downloads", "firewall"} {
		mode := os.FileMode(0700)
		if p == "instances" || p == "sites" || p == "apps" || p == "plugins" {
			mode = 0711
		}
		if err := os.MkdirAll(filepath.Join(e.Root, p), mode); err != nil {
			return err
		}
		if err := os.Chmod(filepath.Join(e.Root, p), mode); err != nil {
			return err
		}
	}
	var err error
	e.Vault, err = security.OpenVault(filepath.Join(e.Root, "executor.key"))
	if err != nil {
		return err
	}
	return nil
}
func (e *Engine) Serve(ctx context.Context, socket string) error {
	e.socketPath = socket
	if err := e.Initialize(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0750); err != nil {
		return err
	}
	if err := os.Chown(filepath.Dir(socket), 0, e.SocketGID); err != nil {
		return err
	}
	if st, err := os.Lstat(socket); err == nil {
		if st.Mode()&os.ModeSocket == 0 {
			return errors.New("socket 路径已被普通文件占用")
		}
		_ = os.Remove(socket)
	}
	l, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer l.Close()
	if err = os.Chown(socket, 0, e.SocketGID); err != nil {
		return err
	}
	if err = os.Chmod(socket, 0660); err != nil {
		return err
	}
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(e.handle)}
	runCtx, cancel := context.WithCancel(ctx)
	e.cancel = cancel
	defer cancel()
	go e.watchFirewall(runCtx)
	go e.watchSSH(runCtx)
	go e.watchSites(runCtx)
	go e.watchPanelRuntime(runCtx)
	go e.watchCertificates(runCtx)
	go e.expireFileTrash(runCtx)
	go func() { <-ctx.Done(); _ = srv.Close() }()
	err = srv.Serve(l)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
func (e *Engine) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/panel-s3" {
		e.handlePanelS3Upload(w, r)
		return
	}
	if r.URL.Path == "/archive-upload" {
		e.handleArchiveUpload(w, r)
		return
	}
	if r.URL.Path == "/file-transfer" {
		e.handleFileTransfer(w, r)
		return
	}
	if r.Method == "POST" && r.URL.Path == "/download" {
		e.handleDownload(w, r)
		return
	}
	if r.Method != "POST" || r.URL.Path != "/execute" {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	var op model.Operation
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(&op); err != nil {
		http.Error(w, "invalid operation", 400)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	enc := json.NewEncoder(w)
	emit := func(s string) {
		_ = enc.Encode(model.Result{Log: s})
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	data, err := e.Execute(r.Context(), op, emit)
	if err != nil {
		result := model.Result{Error: err.Error(), Data: data}
		var conflict *model.RevisionConflict
		if errors.As(err, &conflict) {
			result.Code = "revision_conflict"
			result.Revision = conflict.Revision
		}
		_ = enc.Encode(result)
		return
	}
	if data == nil {
		data = model.Config{}
	}
	_ = enc.Encode(model.Result{Data: data})
}
func (e *Engine) Execute(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	if op.Params == nil {
		op.Params = model.Config{}
	}
	if op.Name == "database.delete" {
		op.Name = "dbadmin.recycle"
	}
	if op.Resource.ID != "" && !logic.Name.MatchString(op.Resource.ID) {
		return nil, errors.New("资源标识无效")
	}
	storedDatabase := strings.HasPrefix(op.Name, "database.") && op.Name != "database.create"
	if storedDatabase {
		stored, err := e.load(op.Resource.ID)
		if err != nil || stored.Kind != "database" || stored.Status == "recycled" {
			return nil, errors.New("受管数据库不可用")
		}
		op.Resource = stored
	}
	if !storedDatabase && !strings.HasPrefix(op.Name, "schedule.") && !strings.HasPrefix(op.Name, "archive.") && !strings.HasPrefix(op.Name, "system.") && !strings.HasPrefix(op.Name, "docker.") && !strings.HasPrefix(op.Name, "files.") && !strings.HasPrefix(op.Name, "catalog.") && !strings.HasPrefix(op.Name, "plugin.") && !strings.HasPrefix(op.Name, "dbadmin.") && !strings.HasPrefix(op.Name, "redisdata.") {
		if err := logic.Validate(op.Resource); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch {
	case strings.HasPrefix(op.Name, "schedule."):
		return e.scheduleExecute(ctx, op, log)
	case strings.HasPrefix(op.Name, "archive."):
		return e.archiveOperation(ctx, op, log)
	case strings.HasPrefix(op.Name, "dbadmin."):
		return e.databaseAdmin(ctx, op, log)
	case strings.HasPrefix(op.Name, "redisdata."):
		return e.redisData(ctx, op)
	case strings.HasPrefix(op.Name, "instance."):
		action := strings.TrimPrefix(op.Name, "instance.")
		if action != "install" && action != "start" && action != "stop" && action != "restart" && action != "uninstall" {
			return e.softwareOperation(ctx, op, log)
		}
		return e.instance(ctx, op, log)
	case strings.HasPrefix(op.Name, "site."):
		return e.site(ctx, op, log)
	case strings.HasPrefix(op.Name, "database."):
		return e.database(ctx, op, log)
	case strings.HasPrefix(op.Name, "app."):
		return nil, errors.New("应用进程功能已下线，已有服务与数据保留")
	case strings.HasPrefix(op.Name, "compose."):
		return e.compose(ctx, op, log)
	case strings.HasPrefix(op.Name, "firewall."):
		return e.firewall(ctx, op, log)
	case strings.HasPrefix(op.Name, "files."):
		return e.files(ctx, op, log)
	case strings.HasPrefix(op.Name, "backup."):
		return e.backup(ctx, op, log)
	case strings.HasPrefix(op.Name, "docker."):
		return e.docker(ctx, op, log)
	case strings.HasPrefix(op.Name, "system."):
		return e.system(ctx, op, log)
	case strings.HasPrefix(op.Name, "plugin."):
		if op.Name == "plugin.repository" {
			return e.repository(ctx, op)
		}
		return e.plugin(ctx, op, log)
	case op.Name == "catalog.import":
		return e.importCatalog(op)
	}
	return nil, errors.New("执行器不支持此操作")
}
func (e *Engine) save(r model.Resource) error {
	if !logic.Name.MatchString(r.ID) {
		return errors.New("资源标识无效")
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if e.Vault == nil {
		return errors.New("执行器密钥尚未初始化")
	}
	return security.WriteAtomic(filepath.Join(e.Root, "state", r.ID+".json"), []byte(e.Vault.Encrypt(b)), 0600)
}
func (e *Engine) load(id string) (model.Resource, error) {
	var r model.Resource
	if !logic.Name.MatchString(id) {
		return r, errors.New("资源标识无效")
	}
	b, err := os.ReadFile(filepath.Join(e.Root, "state", id+".json"))
	if err != nil {
		return r, fmt.Errorf("执行器未登记此资源，请先安装或应用配置：%w", err)
	}
	if e.Vault == nil {
		return r, errors.New("执行器密钥尚未初始化")
	}
	b, err = e.Vault.Decrypt(string(b))
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(b, &r)
	return r, err
}
func (e *Engine) forget(id string) error {
	return os.Remove(filepath.Join(e.Root, "state", id+".json"))
}
func (e *Engine) states() []model.Resource {
	files, _ := filepath.Glob(filepath.Join(e.Root, "state", "*.json"))
	out := []model.Resource{}
	for _, p := range files {
		r, err := e.load(strings.TrimSuffix(filepath.Base(p), ".json"))
		if err == nil {
			out = append(out, r)
		}
	}
	return out
}
func run(ctx context.Context, log func(string), dir string, env []string, input io.Reader, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	isolateProcess(cmd)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	if (filepath.Base(name) == "php" || filepath.Base(name) == "php-fpm" || filepath.Base(name) == "phpize" || filepath.Base(name) == "php-config") && strings.Contains(name, "/runtime/") {
		base, _, _ := strings.Cut(name, "/runtime/")
		cmd.Env = append(cmd.Env, "PHP_INI_SCAN_DIR="+base+"/conf/php.d")
	}
	cmd.Stdin = input
	writer := &logWriter{fn: log}
	cmd.Stdout = writer
	cmd.Stderr = writer
	err := cmd.Run()
	writer.Flush()
	if err != nil {
		return fmt.Errorf("%s 执行失败：%w", filepath.Base(name), err)
	}
	return nil
}

type logWriter struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	fn    func(string)
	total int
}

func (w *logWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	if w.total > 2<<20 {
		return n, nil
	}
	if w.total+len(p) > 2<<20 {
		w.total = 2<<20 + 1
		w.fn("[OpsView 日志已截断] 输出超过 2 MiB；执行继续，退出状态仍会记录")
		return n, nil
	}
	w.total += n
	for _, b := range p {
		if b == '\n' {
			w.fn(w.buf.String())
			w.buf.Reset()
		} else if w.buf.Len() < 8192 {
			w.buf.WriteByte(b)
		}
	}
	return n, nil
}
func (w *logWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf.Len() > 0 {
		w.fn(w.buf.String())
		w.buf.Reset()
	}
}
func output(ctx context.Context, name string, args ...string) (string, error) {
	var b bytes.Buffer
	err := run(ctx, func(s string) { b.WriteString(s); b.WriteByte('\n') }, "", nil, nil, name, args...)
	return b.String(), err
}
func safePath(root, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", errors.New("仅允许相对路径")
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("路径越界")
	}
	p := root
	parts := strings.Split(clean, string(filepath.Separator))
	if parts[0] == "." {
		parts = nil
	}
	if st, err := os.Lstat(root); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("目录不能为符号链接")
	}
	for _, s := range parts {
		p = filepath.Join(p, s)
		st, err := os.Lstat(p)
		if err == nil && st.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("不允许通过符号链接访问文件")
		}
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}
	return p, nil
}
func serviceName(r model.Resource) string { return "opsview-" + r.Kind + "-" + r.ID + ".service" }
func systemctl(ctx context.Context, log func(string), action, name string) error {
	if !logic.Name.MatchString(strings.TrimSuffix(name, ".service")) {
		return errors.New("服务名无效")
	}
	return run(ctx, log, "", nil, nil, "systemctl", action, name)
}
func tail(path string, n int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, _ := f.Stat()
	if st.Size() > 256<<10 {
		_, _ = f.Seek(-(256 << 10), io.SeekEnd)
	}
	scanner := bufio.NewScanner(f)
	lines := []string{}
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return strings.Join(lines, "\n"), scanner.Err()
}

func (e *Engine) panelConfigPath() string {
	if e.PanelConfigPath != "" {
		return e.PanelConfigPath
	}
	return "/etc/opsview/panel.json"
}
