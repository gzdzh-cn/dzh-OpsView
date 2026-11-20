package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"opsview/internal/model"
	"opsview/internal/security"
	"opsview/internal/software"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func siteExpired(r model.Resource, now time.Time) bool {
	value := r.Config.String("expiresAt")
	if value == "" {
		return false
	}
	deadline, err := time.Parse(time.RFC3339, value)
	return err == nil && !now.Before(deadline)
}
func siteUnavailable(r model.Resource) bool {
	return r.Config.Bool("disabled") || r.Config.Bool("expirySuspended") || siteExpired(r, time.Now())
}
func (e *Engine) setupSitePHP(ctx context.Context, r model.Resource, base string, log func(string)) error {
	php, err := e.load(r.Config.String("phpInstanceId"))
	if err != nil {
		return err
	}
	if php.Kind != "instance" || php.Config.String("software") != "php" || php.Config.String("mode") != "native" {
		return errors.New("请选择已安装的原生 PHP 实例")
	}
	user := "ovs_" + r.ID[:12]
	if err = ensureUser(ctx, user, base, log); err != nil {
		return err
	}
	for _, dir := range []string{"run", "logs", "conf", "sessions"} {
		if err = os.MkdirAll(base+"/"+dir, 0755); err != nil {
			return err
		}
	}
	if err = os.Chmod(base, 0711); err != nil {
		return err
	}
	if err = os.Chmod(base+"/run", 0755); err != nil {
		return err
	}
	if _, err = safePath(base, "sessions"); err != nil {
		return err
	}
	if err = os.Chmod(base+"/sessions", 0700); err != nil {
		return err
	}
	if err = run(ctx, log, "", nil, nil, "chown", "-R", user+":"+user, base+"/logs", base+"/sessions"); err != nil {
		return err
	}
	if root := r.Config.String("documentRoot"); root != "" {
		if _, er := e.load(r.ID); er != nil {
			if _, er = safePath("/", strings.TrimPrefix(root, "/")); er != nil {
				return er
			}
			if er = run(ctx, log, "", nil, nil, "chown", "--no-dereference", user+":"+user, root); er != nil {
				return er
			}
		}
	}
	// Existing site documents retain explicit overrides when domains, notes or certificates change.
	ini, err := os.ReadFile(base + "/conf/php.ini")
	if os.IsNotExist(err) {
		ini, err = os.ReadFile(filepath.Join(e.Root, "instances", php.ID, "conf", "php.ini"))
		if err == nil && phpIniValue(string(ini), "session.save_handler", "files") == "redis" {
			r.Config["sessionRedisInstanceId"] = php.Config.String("sessionRedisInstanceId")
		}
	}
	if err != nil {
		return err
	}
	// Register inherited credentials for log redaction without exposing the ini file.
	if phpIniValue(string(ini), "session.save_handler", "files") == "memcached" {
		r.Config["sessionMemcachedPassword"] = phpIniValue(string(ini), "memcached.sess_sasl_password", "")
	}
	if v, ok := r.Config["phpOverrides"]; ok {
		raw, _ := json.Marshal(v)
		values := map[string]string{}
		if json.Unmarshal(raw, &values) != nil {
			return errors.New("PHP 站点参数格式无效")
		}
		text, er := software.Patch("php", php.Config.String("version"), "main", string(ini), values)
		if er != nil {
			return er
		}
		ini = []byte(text)
	}
	ini = []byte(sitePHPIni(string(ini), base))
	if st, er := os.Stat(base + "/conf/php.ini"); er == nil {
		err = writeOwnedAtomic(base+"/conf/php.ini", ini, st.Mode().Perm())
	} else if os.IsNotExist(er) {
		err = security.WriteAtomic(base+"/conf/php.ini", ini, 0600)
	} else {
		err = er
	}
	if err != nil {
		return err
	}
	fpm := fmt.Sprintf("[global]\npid = %s/run/php.pid\nerror_log = %s/logs/fpm.log\n[www]\nuser = %s\ngroup = %s\nlisten = %s/run/php.sock\nlisten.owner = www-data\nlisten.group = www-data\nlisten.mode = 0660\npm = ondemand\npm.max_children = 8\npm.process_idle_timeout = 10s\npm.max_requests = 500\nrequest_terminate_timeout = 60s\nrequest_slowlog_timeout = 5s\nslowlog = %s/logs/slow.log\npm.status_path = /opsview-fpm-status\n", base, base, user, user, base, base)
	if _, statErr := os.Stat(base + "/conf/php-fpm.conf"); os.IsNotExist(statErr) {
		if err = security.WriteAtomic(base+"/conf/php-fpm.conf", []byte(fpm), 0644); err != nil {
			return err
		}
	} else if statErr != nil {
		return statErr
	}
	runtime := filepath.Join(e.Root, "instances", php.ID, "runtime")
	if _, err = output(ctx, runtime+"/sbin/php-fpm", "-t", "-y", base+"/conf/php-fpm.conf", "-c", base+"/conf/php.ini"); err != nil {
		return errors.New("站点 FPM 配置校验失败")
	}
	service := r
	service.Kind = "site-php"
	service.Config = cloneConfig(r.Config)
	service.Config["deferStart"] = siteUnavailable(r) || r.Config.Bool("phpPaused") || php.Config.Bool("servicesPaused")
	return installUnit(ctx, service, e.sitePHPUnitBody(r, php.ID), log)
}
func (e *Engine) sitePHPUnitBody(r model.Resource, phpID string) string {
	base := filepath.Join(e.Root, "sites", r.ID)
	instance := filepath.Join(e.Root, "instances", phpID)
	return fmt.Sprintf("Environment=PHP_INI_SCAN_DIR=%s/conf/php.d\nExecStart=%s/runtime/sbin/php-fpm --nodaemonize -y %s/conf/php-fpm.conf -c %s/conf/php.ini\nExecReload=/bin/kill -USR2 $MAINPID\n", instance, instance, base, base)
}
func phpSiteUnit(r model.Resource) string { r.Kind = "site-php"; return serviceName(r) }
func (e *Engine) watchSites(ctx context.Context) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	// Enforce expiry independently of the panel's scheduler or install queue.
	for {
		e.enforceSiteExpiry(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
func (e *Engine) enforceSiteExpiry(ctx context.Context) {
	e.siteMu.Lock()
	defer e.siteMu.Unlock()
	for _, r := range e.states() {
		if r.Kind != "site" {
			continue
		}
		_, _ = e.siteTraffic(r)
		if !siteExpired(r, time.Now()) && !r.Config.Bool("disabled") && !r.Config.Bool("expirySuspended") {
			continue
		}
		e.stopSiteProcesses(ctx, r)
		if !siteExpired(r, time.Now()) {
			continue
		}
		if r.Config.Bool("expirySuspended") {
			continue
		}
		r.Config["expirySuspended"] = true
		r.Config["probeToken"] = security.ID()
		inst, err := e.load(r.Config.String("instanceId"))
		if err != nil {
			continue
		}
		path := filepath.Join(e.Root, "instances", inst.ID, "conf", "sites", r.ID+".conf")
		old, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		base := filepath.Join(e.Root, "sites", r.ID)
		candidate := e.renderSite(r, inst, base)
		if e.applyNginx(ctx, inst, path, []byte(candidate), old, func(string) {}) == nil {
			r.Status = "expired"
			r.Config["configHash"] = security.Hash(candidate)
			r.Config["renderedConfig"] = candidate
			_ = e.save(r)
		}
	}
}

// Each cursor and its aggregate are committed in a single atomic file. Re-reading after a crash cannot double count.
type trafficCursor struct {
	FileID   string            `json:"fileId"`
	Size     int64             `json:"size"`
	Offset   int64             `json:"offset"`
	Hours    map[string]uint64 `json:"hours"`
	Requests map[string]uint64 `json:"requests"`
}

func (e *Engine) siteTraffic(r model.Resource) (model.Config, error) {
	base := filepath.Join(e.Root, "sites", r.ID)
	cursorPath := base + "/traffic.json"
	cur := trafficCursor{Hours: map[string]uint64{}, Requests: map[string]uint64{}}
	if b, err := os.ReadFile(cursorPath); err == nil {
		if err = json.Unmarshal(b, &cur); err != nil {
			return nil, err
		}
	}
	if cur.Hours == nil {
		cur.Hours = map[string]uint64{}
	}
	if cur.Requests == nil {
		cur.Requests = map[string]uint64{}
	}
	f, err := os.Open(base + "/logs/access.json")
	if os.IsNotExist(err) {
		return model.Config{"todayBytes": 0, "hours": cur.Hours, "requests": cur.Requests}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	id := fileIdentity(st)
	backlog := false
	if cur.FileID != "" && cur.FileID != id {
		// Drain the previous inode after rename before consuming the replacement log.
		paths, _ := filepath.Glob(base + "/logs/access.json.*")
		for _, path := range paths {
			rotated, er := os.Open(path)
			if er != nil {
				continue
			}
			info, er := rotated.Stat()
			if er == nil && fileIdentity(info) == cur.FileID {
				previousOffset := cur.Offset
				if er = consumeTraffic(rotated, &cur, 4<<20); er != nil {
					rotated.Close()
					return nil, er
				}
				// Keep the old inode cursor across polls until its complete lines
				// are drained, instead of dropping a large rotated tail.
				backlog = cur.Offset < info.Size() && cur.Offset > previousOffset
				cur.Size = info.Size()
			}
			rotated.Close()
		}
		if !backlog {
			cur.Offset = 0
		}
	}
	if !backlog {
		if st.Size() < cur.Offset || st.Size() < cur.Size {
			cur.Offset = 0
		}
		cur.FileID = id
		if err = consumeTraffic(f, &cur, 4<<20); err != nil {
			return nil, err
		}
		cur.Size = st.Size()
	}
	cutoff := time.Now().AddDate(0, 0, -31).Format("2006-01-02T15")
	today := time.Now().Format("2006-01-02")
	var total uint64
	for hour, v := range cur.Hours {
		if hour < cutoff {
			delete(cur.Hours, hour)
			delete(cur.Requests, hour)
		} else if strings.HasPrefix(hour, today) {
			total += v
		}
	}
	b, _ := json.Marshal(cur)
	if err = security.WriteAtomic(cursorPath, b, 0600); err != nil {
		return nil, err
	}
	return model.Config{"todayBytes": total, "hours": cur.Hours, "requests": cur.Requests, "timezone": time.Local.String(), "serverDate": time.Now().Format("2006-01-02"), "serverHour": time.Now().Format("2006-01-02T15"), "metric": "HTTP 响应字节"}, nil
}

const nginxObservability = `
 log_format opsview_json escape=json '{"time":"$time_iso8601","bytes":$body_bytes_sent}';
 map $http_upgrade $opsview_connection { default upgrade; '' close; }
`

func (e *Engine) ensureNginxObservability(inst model.Resource) error {
	path := filepath.Join(e.Root, "instances", inst.ID, "conf", "nginx.conf")
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if strings.Contains(string(b), "log_format opsview_json") {
		return nil
	}
	at := strings.Index(string(b), "http {")
	if at < 0 {
		return errors.New("Nginx 主配置缺少可识别的 http 块")
	}
	at += len("http {")
	next := string(b[:at]) + nginxObservability + string(b[at:])
	return security.WriteAtomic(path, []byte(next), 0644)
}

func consumeTraffic(f *os.File, cur *trafficCursor, limit int) error {
	_, err := f.Seek(cur.Offset, io.SeekStart)
	if err != nil {
		return err
	}
	buf := make([]byte, limit)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return err
	}
	chunk := string(buf[:n])
	end := strings.LastIndexByte(chunk, '\n')
	if end >= 0 {
		for _, line := range strings.Split(chunk[:end], "\n") {
			var item struct {
				Time  string `json:"time"`
				Bytes uint64 `json:"bytes"`
			}
			if json.Unmarshal([]byte(line), &item) != nil {
				continue
			}
			at, er := time.Parse(time.RFC3339, item.Time)
			if er != nil {
				continue
			}
			hour := at.In(time.Local).Format("2006-01-02T15")
			cur.Hours[hour] += item.Bytes
			cur.Requests[hour]++
		}
		cur.Offset += int64(end + 1)
	}
	return nil
}
func nginxStatusServer(port int, token string) string {
	return fmt.Sprintf("server { listen 127.0.0.1:%d; server_name 127.0.0.1; location = /opsview-status { stub_status; access_log off; } location = /.opsview-health-%s { add_header X-OpsView-Revision %s always; return 204; } location / { return 404; } }\n", port, token, token)
}
func (e *Engine) stopSiteProcesses(ctx context.Context, r model.Resource) {
	if r.Config.String("type") == "managed" {
		proc := managedSiteProcess(r)
		_ = systemctl(ctx, func(string) {}, "disable", serviceName(proc))
		_ = systemctl(ctx, func(string) {}, "stop", serviceName(proc))
	}
	if r.Config.String("type") == "php" {
		_ = systemctl(ctx, func(string) {}, "disable", phpSiteUnit(r))
		_ = systemctl(ctx, func(string) {}, "stop", phpSiteUnit(r))
	}
}
