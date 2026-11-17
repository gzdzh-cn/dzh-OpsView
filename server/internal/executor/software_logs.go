package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"opsview/internal/logic"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // minimal supported Linux images may not install a zoneinfo database
)

const softwareLogWindow = 1 << 20

type logQuery struct {
	since, until  time.Time
	level, search string
	limit         int
}

func parseLogQuery(p model.Config) (logQuery, error) {
	q := logQuery{level: p.String("level"), search: p.String("search"), limit: 500}
	if len(q.search) > 256 {
		return q, errors.New("搜索内容不能超过 256 字节")
	}
	if q.level != "" && !map[string]bool{"debug": true, "info": true, "notice": true, "warning": true, "error": true, "critical": true, "alert": true, "emergency": true}[q.level] {
		return q, errors.New("日志级别无效")
	}
	if s := p.String("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 2000 {
			return q, errors.New("日志条数必须为 1–2000")
		}
		q.limit = n
	}
	for name, dst := range map[string]*time.Time{"since": &q.since, "until": &q.until} {
		if s := p.String(name); s != "" {
			t, err := time.Parse(time.RFC3339Nano, s)
			if err != nil {
				return q, errors.New("日志时间必须包含时区（RFC3339）")
			}
			*dst = t
		}
	}
	if !q.since.IsZero() && !q.until.IsZero() && q.since.After(q.until) {
		return q, errors.New("开始时间不能晚于结束时间")
	}
	return q, nil
}

// Each log belongs to the executor's registered instance/site, never to a browser-supplied path.
func (e *Engine) softwareLogTarget(r model.Resource, p model.Config) (string, string, string, error) {
	kind := p.String("kind")
	if kind == "" {
		kind = "service"
	}
	unit, owner := serviceName(r), r.ID
	base := filepath.Join(e.Root, "instances", r.ID, "logs")
	if id := p.String("siteId"); id != "" {
		if !logic.Name.MatchString(id) {
			return "", "", "", errors.New("站点标识无效")
		}
		site, err := e.load(id)
		if err != nil || r.Config.String("software") != "php" || site.Kind != "site" || site.Config.String("phpInstanceId") != r.ID {
			return "", "", "", errors.New("站点不属于此 PHP 运行环境")
		}
		base, unit, owner = filepath.Join(e.Root, "sites", id, "logs"), phpSiteUnit(site), id
	}
	if kind == "service" {
		return "", unit, owner, nil
	}
	files := map[string]map[string]string{
		"nginx": {"error": "error.log"}, "mysql": {"error": "mysql.log", "slow": "slow.log"},
		"php": {"error": "php-error.log", "fpm": "fpm.log", "slow": "slow.log"}, "redis": {"error": "redis.log"},
	}
	name := files[r.Config.String("software")][kind]
	if name == "" {
		return "", "", "", errors.New("日志类型无效")
	}
	return filepath.Join(base, name), unit, owner, nil
}

var logArchiveName = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}\.[0-9]{9}Z-[a-zA-Z0-9_-]+\.log$`)

func (e *Engine) logArchiveDir(r model.Resource, owner, kind string) string {
	return filepath.Join(e.Root, "log-archives", r.ID, owner, kind)
}

// OpenRoot anchors both the lookup and the file descriptor; O_NOFOLLOW and SameFile
// also reject a final symlink/replacement even when a site user can write its log directory.
func (e *Engine) openSoftwareLog(path string, flags int) (*os.File, error) {
	rel, err := filepath.Rel(e.Root, path)
	if err != nil {
		return nil, err
	}
	if _, err := safePath(e.Root, rel); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	before, err := root.Lstat(filepath.Base(path))
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, errors.New("只允许访问普通日志文件")
	}
	f, err := root.OpenFile(filepath.Base(path), flags|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) || !after.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("日志在打开时发生变化，请重试")
	}
	if st, ok := after.Sys().(*syscall.Stat_t); !ok || st.Nlink != 1 {
		f.Close()
		return nil, errors.New("不允许访问硬链接日志")
	}
	return f, nil
}

func logRevision(st os.FileInfo) string {
	stat := st.Sys().(*syscall.Stat_t)
	return security.Hash(fmt.Sprintf("%d:%d:%d:%d", stat.Dev, stat.Ino, st.Size(), st.ModTime().UnixNano()))
}

func (e *Engine) softwareLogs(ctx context.Context, r model.Resource, p model.Config) (model.Config, error) {
	q, err := parseLogQuery(p)
	if err != nil {
		return nil, err
	}
	path, unit, owner, err := e.softwareLogTarget(r, p)
	if err != nil {
		return nil, err
	}
	if path == "" {
		if p.String("archive") != "" {
			return nil, errors.New("系统日志没有面板归档")
		}
		args := []string{"--unit", unit, "--lines", "2000", "--no-pager", "--output=json"}
		if !q.since.IsZero() {
			args = append(args, "--since", q.since.Format(time.RFC3339Nano))
		}
		if !q.until.IsZero() {
			args = append(args, "--until", q.until.Format(time.RFC3339Nano))
		}
		data, truncated, err := boundedJournal(ctx, args)
		if err != nil {
			return nil, errors.New("无法读取 systemd 日志")
		}
		return e.filteredSoftwareLog(r, journalLogText(data), q, truncated), nil
	}
	archives := e.softwareLogArchives(r, owner, p.String("kind"))
	archive := p.String("archive")
	if archive != "" {
		if !logArchiveName.MatchString(archive) {
			return nil, errors.New("归档标识无效")
		}
		path = filepath.Join(e.logArchiveDir(r, owner, p.String("kind")), archive)
	}
	f, err := e.openSoftwareLog(path, os.O_RDONLY)
	if os.IsNotExist(err) && archive == "" {
		return model.Config{"content": "", "notice": "尚未产生日志或该日志未启用", "archives": archives}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	start := max(int64(0), st.Size()-softwareLogWindow)
	b := make([]byte, st.Size()-start)
	n, err := f.ReadAt(b, start)
	if err != nil && err != io.EOF {
		return nil, err
	}
	content := string(b[:n])
	if start > 0 {
		_, content, _ = strings.Cut(content, "\n")
	}
	result := e.filteredSoftwareLog(r, content, q, start > 0)
	result["revision"], result["size"], result["archives"] = logRevision(st), st.Size(), archives
	result["archive"], result["canRotate"] = archive, archive == ""
	result["rotationNotice"] = "Nginx 支持在线重开日志；其他日志须先停止对应服务。归档保留原始内容，仅 root 可读；下载仅包含当前脱敏筛选结果。"
	return result, nil
}

func (e *Engine) softwareLogArchives(r model.Resource, owner, kind string) []model.Config {
	items := []model.Config{}
	dir := e.logArchiveDir(r, owner, kind)
	if _, err := safePath(e.Root, strings.TrimPrefix(dir, e.Root+string(filepath.Separator))); err != nil {
		return items
	}
	entries, _ := os.ReadDir(dir)
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if !logArchiveName.MatchString(entry.Name()) || !entry.Type().IsRegular() {
			continue
		}
		st, err := entry.Info()
		if err == nil {
			items = append(items, model.Config{"name": entry.Name(), "size": st.Size(), "createdAt": st.ModTime().Format(time.RFC3339)})
		}
		if len(items) == 100 {
			break
		}
	}
	return items
}

type softwareLogRecord struct {
	lines []string
	level string
	at    time.Time
}

var logDate = regexp.MustCompile(`^(?:# Time: )?(\d{4}[-/]\d{2}[-/]\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.[0-9]+)?(?:Z|[+-]\d{2}:\d{2})?)`)
var phpLogDate = regexp.MustCompile(`^\[(\d{2}-[A-Za-z]{3}-\d{4} \d{2}:\d{2}:\d{2}(?: [^\]]+)?)\]`)
var redisLogDate = regexp.MustCompile(`^\d+:[A-Za-z] (\d{2} [A-Za-z]{3} \d{4} \d{2}:\d{2}:\d{2}(?:\.[0-9]+)?) ([*#.-])`)
var logLevel = regexp.MustCompile(`(?i)\[(emerg|emergency|alert|crit|critical|error|warn|warning|notice|note|info|system|debug)\]|\bPHP (Warning|Notice|Fatal error|Parse error):|\]\s+(WARNING|ERROR|NOTICE|DEBUG):`)

func softwareLogHeader(line string) (time.Time, string, bool) {
	var at time.Time
	header := false
	if m := logDate.FindStringSubmatch(line); m != nil {
		header = true
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006/01/02 15:04:05", "2006-01-02 15:04:05.999999999"} {
			if t, err := time.ParseInLocation(layout, m[1], time.Local); err == nil {
				at = t
				break
			}
		}
	} else if m := phpLogDate.FindStringSubmatch(line); m != nil {
		header = true
		stamp, zone := m[1], ""
		if len(stamp) > 20 {
			zone, stamp = stamp[21:], stamp[:20]
		}
		loc := time.Local
		if zone != "" {
			if parsed, err := time.LoadLocation(zone); err == nil {
				loc = parsed
			} else if zone == "UTC" || zone == "GMT" {
				loc = time.UTC
			} else {
				return time.Time{}, "", true
			}
		}
		at, _ = time.ParseInLocation("02-Jan-2006 15:04:05", stamp, loc)
	} else if m := redisLogDate.FindStringSubmatch(line); m != nil {
		at, _ = time.ParseInLocation("02 Jan 2006 15:04:05.999999999", m[1], time.Local)
		return at, map[string]string{"*": "notice", "#": "warning", "-": "info", ".": "debug"}[m[2]], true
	}
	level := ""
	if m := logLevel.FindStringSubmatch(line); m != nil {
		for _, v := range m[1:] {
			if v != "" {
				level = strings.ToLower(v)
				break
			}
		}
		if v := map[string]string{"emerg": "emergency", "crit": "critical", "warn": "warning", "note": "notice", "system": "info", "fatal error": "critical", "parse error": "error"}[level]; v != "" {
			level = v
		}
	}
	return at, level, header
}

var logURLSecret = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^\s/@]+(?::[^\s/@]*)?@`)
var logQuerySecret = regexp.MustCompile(`(?i)([?&](?:auth|password|passwd|token|secret|access_key|api_key)=)[^&\s"']+`)

func (e *Engine) filteredSoftwareLog(r model.Resource, content string, q logQuery, truncated bool) model.Config {
	// Mask all registered credential values before searching, preventing secret-value searches.
	content = security.MaskLog(content, append(e.states(), r))
	content = logURLSecret.ReplaceAllString(content, "$1[已隐藏]@")
	content = logQuerySecret.ReplaceAllString(content, "$1[已隐藏]")
	records := []softwareLogRecord{}
	lines := []string{}
	if content != "" {
		lines = strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	}
	for _, line := range lines {
		at, level, header := softwareLogHeader(line)
		if header || len(records) == 0 {
			records = append(records, softwareLogRecord{[]string{line}, level, at})
		} else {
			records[len(records)-1].lines = append(records[len(records)-1].lines, line)
		}
	}
	matches := []string{}
	unknown := 0
	for _, rec := range records {
		if (!q.since.IsZero() || !q.until.IsZero()) && rec.at.IsZero() {
			unknown++
			continue
		}
		if !q.since.IsZero() && rec.at.Before(q.since) || !q.until.IsZero() && rec.at.After(q.until) || q.level != "" && rec.level != q.level {
			continue
		}
		text := strings.Join(rec.lines, "\n")
		if q.search != "" && !strings.Contains(strings.ToLower(text), strings.ToLower(q.search)) {
			continue
		}
		matches = append(matches, text)
	}
	limited := len(matches) > q.limit
	if limited {
		matches = matches[len(matches)-q.limit:]
	}
	return model.Config{"content": strings.Join(matches, "\n"), "count": len(matches), "windowTruncated": truncated, "limited": limited, "unknownTimeRecords": unknown, "timezone": time.Local.String() + " (" + time.Now().Format("-07:00 MST") + ")", "windowBytes": softwareLogWindow}
}

type logTailWriter struct {
	data      []byte
	truncated bool
}

func (w *logTailWriter) Write(p []byte) (int, error) {
	n := len(p)
	if n > softwareLogWindow {
		p = p[n-softwareLogWindow:]
		w.truncated = true
	}
	w.data = append(w.data, p...)
	if len(w.data) > softwareLogWindow {
		w.data = w.data[len(w.data)-softwareLogWindow:]
		w.truncated = true
	}
	return n, nil
}

func boundedJournal(ctx context.Context, args []string) (string, bool, error) {
	cmd := exec.CommandContext(ctx, "journalctl", args...)
	isolateProcess(cmd)
	w := &logTailWriter{}
	cmd.Stdout = w
	cmd.Stderr = io.Discard
	err := cmd.Run()
	text := string(w.data)
	if w.truncated {
		_, text, _ = strings.Cut(text, "\n")
	}
	return text, w.truncated, err
}

func journalLogText(data string) string {
	levels := []string{"emergency", "alert", "critical", "error", "warning", "notice", "info", "debug"}
	var out strings.Builder
	for _, line := range strings.Split(data, "\n") {
		var row struct {
			Message   string `json:"MESSAGE"`
			Priority  string `json:"PRIORITY"`
			Timestamp string `json:"__REALTIME_TIMESTAMP"`
		}
		if json.Unmarshal([]byte(line), &row) != nil {
			continue
		}
		us, er := strconv.ParseInt(row.Timestamp, 10, 64)
		priority, err := strconv.Atoi(row.Priority)
		if er != nil || err != nil || priority < 0 || priority >= len(levels) {
			continue
		}
		fmt.Fprintf(&out, "%s [%s] %s\n", time.UnixMicro(us).Format(time.RFC3339Nano), levels[priority], row.Message)
	}
	return out.String()
}

func (e *Engine) manageSoftwareLog(ctx context.Context, r model.Resource, p model.Config, action string, log func(string)) (model.Config, error) {
	if r.Config.String("software") == "php" {
		e.siteMu.Lock()
		defer e.siteMu.Unlock()
	}
	if !p.Bool("confirmed") {
		return nil, errors.New("请先确认日志操作的影响范围")
	}
	path, unit, owner, err := e.softwareLogTarget(r, p)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, errors.New("系统日志由 journald 管理，不允许在此清理")
	}
	dir := e.logArchiveDir(r, owner, p.String("kind"))
	if _, err := safePath(e.Root, strings.TrimPrefix(dir, e.Root+string(filepath.Separator))); err != nil {
		return nil, err
	}
	if action == "delete-log-archive" {
		name := p.String("archive")
		if !logArchiveName.MatchString(name) {
			return nil, errors.New("归档标识无效")
		}
		archive := filepath.Join(dir, name)
		f, err := e.openSoftwareLog(archive, os.O_RDONLY)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		st, statErr := f.Stat()
		if statErr != nil {
			return nil, statErr
		}
		if p.String("baseRevision") != logRevision(st) {
			return nil, errors.New("归档已变化，请重新读取")
		}
		if err = os.Remove(archive); err != nil {
			return nil, err
		}
		log("已删除选定日志归档，其他归档和当前日志未变更")
		return model.Config{}, nil
	}
	if p.String("archive") != "" {
		return nil, errors.New("归档不可再次轮转")
	}
	log("1/4 核对日志归属、修订和服务状态")
	state, statusErr := output(ctx, "systemctl", "is-active", unit)
	state = strings.TrimSpace(state)
	active := state == "active" && statusErr == nil
	if !active && state != "inactive" && state != "failed" {
		return nil, errors.New("无法确认服务已停止，请检查服务状态")
	}
	if active && r.Config.String("software") != "nginx" {
		return nil, errors.New("请先停止对应服务再归档清理，避免丢失正在写入的日志")
	}
	f, err := e.openSoftwareLog(path, os.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if p.String("baseRevision") != logRevision(st) {
		return nil, errors.New("日志已变化，请重新读取后确认")
	}
	if st.Size() == 0 {
		return nil, errors.New("日志为空，无需归档")
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	name := time.Now().UTC().Format("20060102T150405.000000000Z") + "-" + security.ID() + ".log"
	archive := filepath.Join(dir, name)
	log("2/4 保留原始日志并创建新日志文件")
	if err = os.Rename(path, archive); err != nil {
		return nil, errors.New("日志归档失败，源与归档目录须在同一文件系统")
	}
	archivedInfo, err := os.Lstat(archive)
	if err != nil || !os.SameFile(st, archivedInfo) {
		// A site user may replace a log name. Do not chmod/truncate the substituted inode.
		if _, er := os.Lstat(path); os.IsNotExist(er) {
			_ = os.Rename(archive, path)
		}
		return nil, errors.New("日志在归档时发生变化，请重新读取")
	}
	// Keep the inode for writers until Nginx reopens it. Never copytruncate.
	var newInfo os.FileInfo
	rollback := func(cause error) (model.Config, error) {
		var partialErr error
		if current, er := os.Lstat(path); er == nil {
			if newInfo == nil || !os.SameFile(newInfo, current) {
				return nil, errors.Join(cause, errors.New("日志路径已被外部程序替换；归档已保留，请手工核对"))
			}
			// Even a file that is empty now may receive a concurrent write before
			// workers reopen it. Always retain the replacement inode on rollback.
			partial := filepath.Join(dir, time.Now().UTC().Format("20060102T150405.000000000Z")+"-"+security.ID()+".log")
			if er = os.Rename(path, partial); er != nil {
				return nil, errors.Join(cause, er)
			}
			partialErr = os.Chmod(partial, 0600)
		}
		restoreErr := os.Rename(archive, path)
		if active && restoreErr == nil {
			recovery, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			restoreErr = e.reopenNginxLog(recovery, r)
		}
		return nil, errors.Join(cause, restoreErr, partialErr)
	}
	created, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, st.Mode().Perm())
	if err != nil {
		return rollback(err)
	}
	newInfo, err = created.Stat()
	if err != nil {
		created.Close()
		return rollback(err)
	}
	stat := st.Sys().(*syscall.Stat_t)
	err = created.Chmod(st.Mode().Perm())
	if err == nil {
		err = created.Chown(int(stat.Uid), int(stat.Gid))
	}
	err = errors.Join(err, created.Close())
	if err != nil {
		return rollback(err)
	}
	if active {
		log("3/4 请求 Nginx 重开并核对主进程及工作进程描述符")
		if err = e.reopenNginxLog(ctx, r); err != nil {
			return rollback(err)
		}
		if err = waitNginxLogReopen(ctx, unit, st, newInfo); err != nil {
			return rollback(err)
		}
	}
	log("4/4 同步归档并设置私有权限")
	// Archive directory is root-private. File mode follows source until reopen succeeds,
	// so rollback can preserve the original ownership and permissions.
	if err = f.Sync(); err != nil {
		return rollback(err)
	}
	if err = f.Chmod(0600); err != nil {
		return rollback(err)
	}
	log("日志已归档并建立新的日志文件；归档内容不写入任务日志")
	return model.Config{"archive": name}, nil
}

func (e *Engine) reopenNginxLog(ctx context.Context, r model.Resource) error {
	base := filepath.Join(e.Root, "instances", r.ID)
	_, err := output(ctx, base+"/runtime/sbin/nginx", "-c", base+"/conf/nginx.conf", "-s", "reopen")
	if err != nil {
		return errors.New("Nginx 重开日志失败，已尝试恢复原文件")
	}
	return nil
}

// A successful signal command only acknowledges delivery. Verify that the master
// has opened the replacement and its current children have released the old inode.
func waitNginxLogReopen(ctx context.Context, unit string, old, replacement os.FileInfo) error {
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pidText, err := output(wait, "systemctl", "show", "--property=MainPID", "--value", unit)
	pid, parseErr := strconv.Atoi(strings.TrimSpace(pidText))
	if err != nil || parseErr != nil || pid < 2 {
		return errors.New("无法确认 Nginx 主进程，日志重开未验证")
	}
	for {
		opened, released := nginxLogDescriptors(pid, old, replacement)
		if opened && released {
			return nil
		}
		select {
		case <-wait.Done():
			return errors.New("Nginx 未确认重开日志，正在恢复原文件")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func nginxLogDescriptors(pid int, old, replacement os.FileInfo) (bool, bool) {
	proc := filepath.Join("/proc", strconv.Itoa(pid))
	children, err := os.ReadFile(proc + "/task/" + strconv.Itoa(pid) + "/children")
	if err != nil {
		return false, false
	}
	pids := append([]string{strconv.Itoa(pid)}, strings.Fields(string(children))...)
	opened := false
	for _, id := range pids {
		entries, err := os.ReadDir("/proc/" + id + "/fd")
		if os.IsNotExist(err) && id != strconv.Itoa(pid) {
			continue
		}
		if err != nil {
			return false, false
		}
		for _, entry := range entries {
			st, err := os.Stat("/proc/" + id + "/fd/" + entry.Name())
			if err != nil {
				continue
			} // descriptors can close during enumeration
			if os.SameFile(st, old) {
				return false, false
			}
			if id == strconv.Itoa(pid) && os.SameFile(st, replacement) {
				opened = true
			}
		}
	}
	return opened, true
}
