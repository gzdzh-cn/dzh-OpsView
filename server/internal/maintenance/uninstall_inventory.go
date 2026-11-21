package maintenance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"opsview/internal/dao"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const uninstallManaged = "/var/lib/opsview/managed"
const uninstallProgress = "/var/lib/opsview/uninstall-progress.json"

var uninstallID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type uninstallUnit struct {
	Name      string
	File      uninstallNode
	Database  bool
	Transient bool
}

// No resource configurations or credentials are written to the progress file.
type uninstallPlan struct {
	Version          int
	Keep             []string
	Delete           []uninstallNode
	Check            []uninstallNode
	Units            []uninstallUnit
	Containers       []uninstallContainer
	Warnings         []string
	Done             map[string]bool
	ResourceRevision string
}

func (u *uninstaller) vault(path string) (*security.Vault, error) {
	b, err := u.fs.read(path)
	if err != nil {
		return nil, err
	}
	return security.VaultFromKey(b)
}

func (u *uninstaller) panelResources() ([]model.Resource, model.Config, []string, error) {
	const panel = "/var/lib/opsview/panel"
	n, err := u.fs.capture(panel+"/opsview.db", false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil, nil
	}
	if err != nil || !n.Mode.IsRegular() {
		return nil, nil, nil, fmt.Errorf("无法核对面板数据库：%w", err)
	}
	v, err := u.vault(panel + "/master.key")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("无法读取现有面板密钥：%w", err)
	}
	dsn := (&url.URL{Scheme: "file", Path: filepath.Join(u.fs.base, panel, "opsview.db"), RawQuery: "mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, nil, nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	s := &dao.Store{DB: db, Vault: v}
	resources, err := s.Resources("")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("无法核对面板资源，未执行卸载：%w", err)
	}
	prefs := model.Config{}
	if err = s.Setting("panel", &prefs); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil, err
	}
	var backups []string
	if err = s.Setting("panel-backup-directories", &backups); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil, err
	}
	if err = u.fs.verify(n, false); err != nil {
		return nil, nil, nil, err
	}
	return resources, prefs, backups, nil
}

func (u *uninstaller) resources(p *uninstallPlan) ([]model.Resource, error) {
	resources, prefs, backups, err := u.panelResources()
	if err != nil {
		return nil, err
	}
	backupDir := prefs.String("backupDirectory")
	if backupDir == "" {
		backupDir = "/www/backup"
	}
	backups = append(backups, filepath.Join(backupDir, "opsview-panel"))
	for _, path := range backups {
		// Only a dedicated panel-backup directory can be cleaned recursively.
		if !uninstallPath(path) || filepath.Base(path) != "opsview-panel" {
			return nil, fmt.Errorf("备份目录归属不明确，请先核对：%s", path)
		}
		u.candidates = append(u.candidates, path)
		if err := u.backupDirectory(p, path); err != nil {
			return nil, err
		}
	}
	entries, err := u.fs.entries(uninstallManaged + "/state")
	if errors.Is(err, os.ErrNotExist) {
		return resources, nil
	}
	if err != nil {
		return nil, err
	}
	var v *security.Vault
	for _, entry := range entries {
		path := filepath.Join(uninstallManaged, "state", entry.Name())
		if strings.HasSuffix(entry.Name(), ".document-root") {
			b, err := u.fs.read(path)
			if err != nil {
				return nil, err
			}
			if err = u.keep(p, string(b)); err != nil {
				return nil, err
			}
		}
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if v == nil {
			v, err = u.vault(uninstallManaged + "/executor.key")
			if err != nil {
				return nil, fmt.Errorf("无法读取现有执行器密钥：%w", err)
			}
		}
		b, err := u.fs.read(path)
		if err != nil {
			return nil, err
		}
		b, err = v.Decrypt(string(b))
		if err != nil {
			return nil, fmt.Errorf("执行器登记损坏，请先核对：%s", entry.Name())
		}
		var r model.Resource
		if err = json.Unmarshal(b, &r); err != nil || !uninstallID.MatchString(r.ID) || r.ID+".json" != entry.Name() {
			return nil, fmt.Errorf("执行器登记无效：%s", entry.Name())
		}
		resources = append(resources, r)
		n, err := u.fs.capture(path, true)
		if err != nil {
			return nil, err
		}
		p.Check = append(p.Check, n)
	}
	return resources, nil
}

// Resolve retained links without following them during deletion. Both the link
// and its real target are protected, even if the target lies in a cleanup tree.
func (u *uninstaller) keep(p *uninstallPlan, path string) error {
	if path == "" {
		return nil
	}
	if !uninstallPath(path) {
		return fmt.Errorf("保留路径无效：%q", path)
	}
	for _, critical := range []string{"/etc", "/usr", "/var", "/var/lib", "/var/lib/opsview", uninstallManaged, "/opt", "/opt/opsview/bin", "/www", "/run"} {
		if path == critical {
			return fmt.Errorf("数据保留范围过宽，需先核对：%s", path)
		}
	}
	p.Keep = append(p.Keep, path)
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	current := "/"
	links := 0
	for len(parts) > 0 {
		current = filepath.Join(current, parts[0])
		parts = parts[1:]
		st, err := u.fs.stat(current)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			links++
			if links > 40 {
				return fmt.Errorf("保留路径存在链接循环：%s", path)
			}
			d, err := u.fs.dir(filepath.Dir(current))
			if err != nil {
				return err
			}
			target, err := d.Readlink(filepath.Base(current))
			d.Close()
			if err != nil {
				return err
			}
			p.Keep = append(p.Keep, current)
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(current), target)
			}
			target = filepath.Clean(filepath.Join(append([]string{target}, parts...)...))
			if !uninstallPath(target) {
				return fmt.Errorf("保留链接目标无效：%s", path)
			}
			p.Keep = append(p.Keep, target)
			parts = strings.Split(strings.TrimPrefix(target, "/"), "/")
			current = "/"
		}
	}
	return nil
}

func (u *uninstaller) resourcePaths(p *uninstallPlan, resources []model.Resource) (map[string]model.Resource, error) {
	registered := map[string]model.Resource{}
	for _, r := range resources {
		if !uninstallID.MatchString(r.ID) {
			return nil, errors.New("资源标识无效，未执行卸载")
		}
		registered[r.ID] = r
		base := filepath.Join(uninstallManaged, "instances", r.ID)
		switch r.Kind {
		case "instance":
			if err := u.keep(p, base+"/source"); err != nil {
				return nil, err
			}
			for _, key := range []string{"dataDirectory", "previousDataDirectory"} {
				if err := u.keep(p, r.Config.String(key)); err != nil {
					return nil, err
				}
			}
			if err := u.keep(p, base+"/data"); err != nil {
				return nil, err
			}
		case "site":
			for _, path := range []string{r.Config.String("documentRoot"), r.Config.String("rootDirectory"), filepath.Join(uninstallManaged, "sites", r.ID, "www")} {
				if err := u.keep(p, path); err != nil {
					return nil, err
				}
			}
		case "app", "site-process":
			path := r.Config.String("workingDirectory")
			if path == "" && r.Kind == "app" {
				path = filepath.Join(uninstallManaged, "apps", r.ID)
			}
			if err := u.keep(p, path); err != nil {
				return nil, err
			}
		case "backup":
			if dir := r.Config.String("storageDirectory"); dir != "" {
				if !uninstallPath(dir) || len(strings.Split(dir, "/")) < 3 {
					return nil, errors.New("备份存储路径无效")
				}
				path := filepath.Join(dir, r.ID)
				u.candidates = append(u.candidates, path)
				if err := u.backupDirectory(p, path); err != nil {
					return nil, err
				}
			}
		case "site-deletion":
			var site model.Resource
			raw, _ := json.Marshal(r.Config["site"])
			if json.Unmarshal(raw, &site) == nil {
				for _, key := range []string{"documentRoot", "rootDirectory"} {
					if err := u.keep(p, site.Config.String(key)); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	return registered, nil
}

func (u *uninstaller) softwarePaths(p *uninstallPlan, registered map[string]model.Resource) error {
	for _, category := range []string{"instances", "sites", "apps", "compose", "plugins"} {
		entries, err := u.fs.entries(uninstallManaged + "/" + category)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			path := filepath.Join(uninstallManaged, category, entry.Name())
			_, known := registered[entry.Name()]
			if category == "apps" || !known {
				if err := u.keep(p, path); err != nil {
					return err
				}
				p.Warnings = append(p.Warnings, "保留项目或未登记目录："+path)
				continue
			}
			if category == "instances" {
				children, err := u.fs.entries(path)
				if err != nil {
					return err
				}
				for _, child := range children {
					if child.Name() == "source" || child.Name() == "data" || strings.HasPrefix(child.Name(), "data-") {
						if err := u.keep(p, filepath.Join(path, child.Name())); err != nil {
							return err
						}
					}
				}
				if err := u.databaseFiles(p, registered[entry.Name()], path); err != nil {
					return err
				}
			}
			u.candidates = append(u.candidates, path)
		}
	}
	return nil
}

// Custom database paths may appear in the saved engine configuration rather
// than panel metadata. Preserve recovery logs as well as data/tablespace paths.
func (u *uninstaller) databaseFiles(p *uninstallPlan, r model.Resource, base string) error {
	name := map[string]string{"mysql": "my.cnf", "redis": "redis.conf", "postgresql": "postgresql.conf"}[r.Config.String("software")]
	if name == "" {
		return nil
	}
	path := base + "/conf/" + name
	if r.Config.String("software") == "postgresql" {
		path = base + "/data/postgresql.conf"
	}
	b, err := u.fs.read(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	n, err := u.fs.capture(path, true)
	if err != nil {
		return err
	}
	p.Check = append(p.Check, n)
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "[") {
			continue
		}
		fields := strings.Fields(strings.ReplaceAll(line, "=", " "))
		if len(fields) < 2 {
			continue
		}
		key := strings.ReplaceAll(strings.ToLower(fields[0]), "_", "-")
		value := strings.Trim(fields[1], `"'`)
		data := false
		switch key {
		case "datadir", "dir", "data-directory", "innodb-data-home-dir", "innodb-log-group-home-dir", "innodb-undo-directory", "innodb-directories", "log-bin", "log-bin-index", "relay-log", "relay-log-index", "aria-log-dir-path":
			data = true
		case "include", "include-dir", "include-if-exists", "!include", "!includedir", "loadmodule", "early-plugin-load", "plugin-load-add":
			return fmt.Errorf("数据库有自定义外部配置或模块，需先核对数据依赖：%s", path)
		}
		if !data {
			continue
		}
		// Ambiguous quoted values or multiple directories must never result in
		// retaining only the first token and deleting the actual data directory.
		if len(fields) > 2 && !strings.HasPrefix(fields[2], "#") && !strings.HasPrefix(fields[2], ";") {
			return fmt.Errorf("数据库路径包含多个值或空格，请先核对：%s", path)
		}
		if !filepath.IsAbs(value) {
			// Relative log prefixes belong to the engine's preserved data directory.
			if strings.HasPrefix(key, "log-bin") || strings.HasPrefix(key, "relay-log") {
				continue
			}
			return fmt.Errorf("数据库数据路径无法明确解析：%s", path)
		}
		if strings.HasPrefix(key, "log-bin") || strings.HasPrefix(key, "relay-log") {
			value = filepath.Dir(value)
		}
		if err := u.keep(p, value); err != nil {
			return err
		}
	}
	return nil
}

func (u *uninstaller) discoverSources(p *uninstallPlan, path string) error {
	if protectedPath(path, p.Keep) {
		return nil
	}
	st, err := u.fs.stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !st.IsDir() {
		return err
	}
	if u.fs.mounts[path] {
		return fmt.Errorf("清理目录包含挂载点：%s", path)
	}
	entries, err := u.fs.entries(path)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == ".git" {
			return u.keep(p, path)
		}
	}
	for _, e := range entries {
		child := filepath.Join(path, e.Name())
		if e.IsDir() && (e.Name() == "source" || e.Name() == "sources") {
			if err := u.keep(p, child); err != nil {
				return err
			}
		} else if e.IsDir() {
			if err := u.discoverSources(p, child); err != nil {
				return err
			}
		}
	}
	return nil
}

func uniquePaths(paths []string) []string {
	sort.Strings(paths)
	out := []string{}
	for _, p := range paths {
		if len(out) == 0 || !containsPath(out[len(out)-1], p) {
			out = append(out, p)
		}
	}
	return out
}

func uninstallResourceRevision(resources []model.Resource) string {
	rows := make([]string, 0, len(resources))
	for _, r := range resources {
		b, _ := json.Marshal(r)
		rows = append(rows, string(b))
	}
	sort.Strings(rows)
	return security.Hash(strings.Join(rows, "\n"))
}

func (u *uninstaller) build(ctx context.Context) (*uninstallPlan, error) {
	if b, err := u.fs.read(uninstallProgress); err == nil {
		var p uninstallPlan
		if err := json.Unmarshal(b, &p); err != nil || p.Version != 1 || p.Done == nil {
			return nil, errors.New("卸载进度文件无效，请先核对现场")
		}
		st, err := u.fs.stat(uninstallProgress)
		if err != nil {
			return nil, err
		}
		if st.Mode().Perm()&0077 != 0 || nodeOwner(st) != uint32(os.Geteuid()) {
			return nil, errors.New("卸载进度文件权限无效")
		}
		for _, n := range p.Delete {
			if !uninstallPath(n.Path) || n.Path == uninstallProgress {
				return nil, errors.New("卸载进度路径无效")
			}
		}
		for _, path := range p.Keep {
			if !uninstallPath(path) {
				return nil, errors.New("卸载保留路径无效")
			}
		}
		for _, unit := range p.Units {
			if !regexp.MustCompile(`^[A-Za-z0-9_.-]+\.service$`).MatchString(unit.Name) || (!unit.Transient && !uninstallPath(unit.File.Path)) {
				return nil, errors.New("卸载服务记录无效")
			}
		}
		for _, c := range p.Containers {
			if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(c.ID) {
				return nil, errors.New("卸载容器记录无效")
			}
		}
		if p.Done["cleanup-started"] {
			return &p, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, path := range []string{"panel-pending.json", "ssh-pending.json", "firewall-mode.json", "firewall-admin/pending.json", "firewall-admin/feature-pending.json"} {
		if _, err := u.fs.stat(uninstallManaged + "/" + path); err == nil {
			return nil, fmt.Errorf("存在待确认或回退的安全变更，请先处理：%s", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	p := &uninstallPlan{Version: 1, Done: map[string]bool{}}
	if err := u.keep(p, "/www/wwwroot"); err != nil {
		return nil, err
	}
	resources, err := u.resources(p)
	if err != nil {
		return nil, err
	}
	p.ResourceRevision = uninstallResourceRevision(resources)
	registered, err := u.resourcePaths(p, resources)
	if err != nil {
		return nil, err
	}
	if err = u.softwarePaths(p, registered); err != nil {
		return nil, err
	}
	if err = u.units(ctx, p, registered); err != nil {
		return nil, err
	}
	if err = u.containers(ctx, p, registered); err != nil {
		return nil, err
	}
	if err = u.auxiliaryPaths(p); err != nil {
		return nil, err
	}
	if err = u.restorePaths(); err != nil {
		return nil, err
	}
	for _, name := range []string{"backups", "downloads", "catalog", "file-manager", "jobs", "client-auth", "panel-acme", "nginx-recovery", "firewall", "firewall-admin"} {
		u.candidates = append(u.candidates, uninstallManaged+"/"+name)
	}
	for _, name := range []string{"backup.key", "panel-acme.json", "ssh-service.json", "ssh-managed-users.json", "ssh-conflict.conf"} {
		u.candidates = append(u.candidates, uninstallManaged+"/"+name)
	}
	u.candidates = append(u.candidates, "/var/lib/opsview/updates", "/var/log/opsview", "/run/opsview")
	// Metadata and keys go last, so failed software cleanup remains diagnosable.
	u.candidates = append(u.candidates, uninstallManaged+"/state", uninstallManaged+"/executor.key", "/var/lib/opsview/panel", "/etc/opsview")
	for _, path := range u.candidates {
		if err := u.discoverSources(p, path); err != nil {
			return nil, err
		}
	}
	if err := u.discoverSources(p, "/opt/opsview"); err != nil {
		return nil, err
	}
	if err := u.scanKeptLinks(p); err != nil {
		return nil, err
	}
	p.Keep = uniquePaths(p.Keep)
	if err := u.checkRetainedOverlap(p); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, path := range u.candidates {
		if seen[path] || protectedPath(path, p.Keep) {
			continue
		}
		seen[path] = true
		n, err := u.fs.capture(path, false)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if err = u.fs.inspect(path, p.Keep); err != nil {
			return nil, err
		}
		p.Delete = append(p.Delete, n)
	}
	// Known release executables only. Unknown files and source trees survive.
	for _, name := range []string{"opsview-executor", "opsview-plugin-worker", "opsview-pack", "opsview-install.sh", "opsview"} {
		if n, err := u.fs.capture("/opt/opsview/bin/"+name, false); err == nil {
			if !n.Mode.IsRegular() {
				return nil, fmt.Errorf("安装程序类型异常：%s", n.Path)
			}
			if protectedPath(n.Path, p.Keep) {
				return nil, fmt.Errorf("程序路径与保留源码或业务数据交叠，请先调整：%s", n.Path)
			}
			p.Delete = append(p.Delete, n)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return p, nil
}

// Retention wins over deletion, but retaining an installed service or panel
// credentials cannot be reported as a complete uninstall. Resolve such
// conflicts before confirmation instead of silently skipping required items.
func (u *uninstaller) checkRetainedOverlap(p *uninstallPlan) error {
	required := []string{"/var/lib/opsview/panel", "/etc/opsview", uninstallManaged + "/state", uninstallManaged + "/executor.key", uninstallManaged + "/backup.key", "/usr/local/bin/ov", "/usr/local/bin/opsview"}
	for _, name := range []string{"opsview", "opsview-executor", "opsview-plugin-worker", "opsview-pack", "opsview-install.sh"} {
		required = append(required, "/opt/opsview/bin/"+name)
	}
	for _, unit := range p.Units {
		if !unit.Transient {
			required = append(required, unit.File.Path)
		}
	}
	for _, path := range required {
		if !protectedPath(path, p.Keep) {
			continue
		}
		if _, err := u.fs.stat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		return fmt.Errorf("保留数据与必须卸载的程序、配置或服务交叠，请先核对：%s", path)
	}
	return nil
}

func (u *uninstaller) scanKeptLinks(p *uninstallPlan) error {
	seenPaths := map[string]bool{}
	seenDirs := map[string]bool{}
	var scan func(string) error
	scan = func(path string) error {
		if seenPaths[path] {
			return nil
		}
		seenPaths[path] = true
		st, err := u.fs.stat(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return u.keep(p, path)
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return u.keep(p, path)
		}
		if !st.IsDir() || u.fs.mounts[path] {
			return nil
		}
		n := nodeFor(path, st)
		identity := fmt.Sprintf("%d:%d", n.Device, n.Inode)
		if seenDirs[identity] {
			return nil
		}
		seenDirs[identity] = true
		entries, err := u.fs.entries(path)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := scan(filepath.Join(path, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	for i := 0; i < len(p.Keep); i++ {
		if err := scan(p.Keep[i]); err != nil {
			return err
		}
	}
	return nil
}

func (u *uninstaller) auxiliaryPaths(p *uninstallPlan) error {
	for _, file := range []string{"before.rules", "before6.rules"} {
		path := "/etc/ufw/" + file
		n, err := u.fs.capture(path, true)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !n.Mode.IsRegular() {
			return fmt.Errorf("UFW 配置路径异常：%s", path)
		}
		p.Check = append(p.Check, n)
	}
	for _, family := range []string{"ipv4", "ipv6"} {
		path := "/etc/sysctl.d/91-opsview-forward-" + family + ".conf"
		b, err := u.fs.read(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		key := "net.ipv4.ip_forward"
		if family == "ipv6" {
			key = "net.ipv6.conf.all.forwarding"
		}
		if string(b) != key+" = 1\n" {
			return fmt.Errorf("转发配置已被外部修改：%s", path)
		}
		n, err := u.fs.capture(path, true)
		if err != nil {
			return err
		}
		p.Check = append(p.Check, n)
		u.candidates = append(u.candidates, path)
	}
	path := "/etc/fail2ban/jail.d/opsview-firewall.local"
	b, err := u.fs.read(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && line != "[opsview-sshd]" && line != "[opsview-nginx-http-auth]" {
			return errors.New("OpsView 防护文件包含其他 jail，需先核对")
		}
	}
	if !strings.Contains(string(b), "[opsview-sshd]") || !strings.Contains(string(b), "[opsview-nginx-http-auth]") {
		return errors.New("无法确认 Fail2ban 配置归属")
	}
	n, err := u.fs.capture(path, true)
	if err != nil {
		return err
	}
	p.Check = append(p.Check, n)
	return nil // The executor removes the file after stopping just these jails.
}

func (u *uninstaller) backupDirectory(p *uninstallPlan, path string) error {
	entries, err := u.fs.entries(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	owned := 0
	for _, entry := range entries {
		child := filepath.Join(path, entry.Name())
		known := false
		if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			name := strings.TrimSuffix(entry.Name(), ".failed")
			if strings.HasSuffix(name, ".ovp") || strings.HasSuffix(name, ".ovb") {
				header, err := u.fs.prefix(child, 5)
				known = err == nil && string(header) == "OVB1\n"
			} else if strings.HasSuffix(name, ".ovb.json") {
				raw, err := u.fs.read(child)
				var manifest model.Config
				if err == nil && json.Unmarshal(raw, &manifest) == nil && manifest.String("file") == strings.TrimSuffix(name, ".json") {
					header, err := u.fs.prefix(filepath.Join(path, manifest.String("file")), 5)
					known = err == nil && string(header) == "OVB1\n"
				}
			}
		}
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "metadata-") {
			st, err := u.fs.stat(child + "/opsview.db")
			known = err == nil && st.Mode().IsRegular()
		}
		if known {
			owned++
		} else {
			if err := u.keep(p, child); err != nil {
				return err
			}
			p.Warnings = append(p.Warnings, "备份目录中的未知项目保留："+child)
		}
	}
	if owned == 0 {
		return u.keep(p, path)
	}
	return nil
}

func (u *uninstaller) restorePaths() error {
	entries, err := u.fs.entries("/var/lib/opsview")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "restore-verified-") || !entry.IsDir() {
			continue
		}
		path := "/var/lib/opsview/" + entry.Name()
		st, err := u.fs.stat(path + "/input.ovp")
		if err == nil && st.Mode().IsRegular() {
			u.candidates = append(u.candidates, path)
		}
	}
	return nil
}
