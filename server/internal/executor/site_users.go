package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"opsview/internal/logic"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

type siteAccount struct {
	Name     string   `json:"name"`
	UID      string   `json:"uid"`
	GID      string   `json:"gid"`
	Group    string   `json:"group"`
	Home     string   `json:"home"`
	Site     string   `json:"site"`
	Phase    string   `json:"phase"`
	Retained []string `json:"retained,omitempty"`
	Reason   string   `json:"reason,omitempty"`
}
type siteAccountRegistry struct {
	Version  int                     `json:"version"`
	Accounts map[string]*siteAccount `json:"accounts"`
}

func (e *Engine) readSiteAccounts() (siteAccountRegistry, error) {
	v := siteAccountRegistry{Version: 1, Accounts: map[string]*siteAccount{}}
	b, err := os.ReadFile(filepath.Join(e.Root, "site-users", "accounts.json"))
	if os.IsNotExist(err) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	if err = json.Unmarshal(b, &v); err != nil {
		return v, err
	}
	if v.Version != 1 || v.Accounts == nil {
		return v, errors.New("站点账号记录版本不兼容")
	}
	for name, a := range v.Accounts {
		if a == nil || !logic.Name.MatchString(a.Site) || a.Name != name || name != "ovs_"+a.Site[:min(12, len(a.Site))] || a.Group != name || !filepath.IsAbs(a.Home) || a.Home == "/" {
			return v, errors.New("站点账号归属记录无效")
		}
		uid, ue := strconv.Atoi(a.UID)
		gid, ge := strconv.Atoi(a.GID)
		if ue != nil || ge != nil || uid < 100 || gid < 100 {
			return v, errors.New("站点账号 UID/GID 记录无效")
		}
	}
	return v, nil
}
func (e *Engine) writeSiteAccounts(v siteAccountRegistry) error {
	dir := filepath.Join(e.Root, "site-users")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return security.WriteAtomic(filepath.Join(dir, "accounts.json"), b, 0600)
}
func siteAccountAllowed(u *user.User) bool {
	uid, err := strconv.Atoi(u.Uid)
	return err == nil && ((u.Username == "root" && uid == 0) || (uid > 0 && (u.Username == "www-data" || (uid >= 1000 && uid < 65534)))) && !strings.HasPrefix(u.Username, "opsview") && u.Username != "ovbuild"
}
func (e *Engine) siteRuntimeUsers() (model.Config, error) {
	b, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return nil, err
	}
	rows := []model.Config{}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(line, ":")
		if len(f) != 7 {
			continue
		}
		u, er := user.Lookup(f[0])
		if er != nil || !siteAccountAllowed(u) {
			continue
		}
		g, er := user.LookupGroupId(u.Gid)
		if er != nil {
			continue
		}
		rows = append(rows, model.Config{"name": u.Username, "uid": u.Uid, "gid": u.Gid, "group": g.Name, "source": "existing", "shared": true})
	}
	return model.Config{"users": rows, "default": "isolated", "notice": "独立用户与运行限制分别生效；root 保留目录限制，已有账号不会随站点删除"}, nil
}
func (e *Engine) ensureSiteAccount(ctx context.Context, r model.Resource, log func(string)) (*user.User, error) {
	name := r.Config.String("user")
	if name != "" && name != "isolated" {
		u, err := user.Lookup(name)
		if err != nil || !siteAccountAllowed(u) {
			return nil, errors.New("运行用户不存在或不允许用于项目")
		}
		return u, nil
	}
	name = "ovs_" + r.ID[:min(12, len(r.ID))]
	e.siteUsersMu.Lock()
	defer e.siteUsersMu.Unlock()
	v, err := e.readSiteAccounts()
	if err != nil {
		return nil, err
	}
	if a := v.Accounts[name]; a != nil {
		if a.Site != r.ID || a.Home != r.Config.String("documentRoot") {
			return nil, errors.New("独立用户记录与站点不匹配")
		}
		if a.Phase == "creating" && a.UID != "" && a.GID != "" {
			return e.finishSiteAccount(ctx, v, a, log)
		}
		u, er := user.Lookup(name)
		if er != nil || a.Phase != "active" || !siteAccountIdentity(u, a) {
			return nil, errors.New("独立用户创建未完成或身份已变更，请核对账号记录")
		}
		return u, nil
	}
	if _, er := user.Lookup(name); er == nil {
		// A legacy process can be remediated without claiming ownership of an
		// account whose creation wasn't recorded by OpsView.
		old, loadErr := e.load("web-" + r.ID)
		u, lookupErr := user.Lookup(name)
		if loadErr == nil && lookupErr == nil && old.Kind == "site-process" && old.Config.String("user") == name && old.Config.String("workingDirectory") == r.Config.String("documentRoot") && u.HomeDir == r.Config.String("documentRoot") && r.Config.String("permissionPreview") != "" {
			return u, nil
		}
		return nil, errors.New("同名历史用户未经归属核验，禁止自动接管")
	}
	if _, er := user.LookupGroup(name); er == nil {
		return nil, errors.New("同名用户组已存在，禁止自动接管")
	}
	id := ""
	for n := 999; n >= 100; n-- {
		candidate := strconv.Itoa(n)
		_, ue := user.LookupId(candidate)
		_, ge := user.LookupGroupId(candidate)
		_, unknownUser := ue.(user.UnknownUserIdError)
		_, unknownGroup := ge.(user.UnknownGroupIdError)
		if unknownUser && unknownGroup {
			id = candidate
			break
		}
	}
	if id == "" {
		return nil, errors.New("没有可用的独立系统账号 UID/GID")
	}
	a := &siteAccount{Name: name, Group: name, UID: id, GID: id, Home: r.Config.String("documentRoot"), Site: r.ID, Phase: "creating"}
	v.Accounts[name] = a
	if err = e.writeSiteAccounts(v); err != nil {
		return nil, err
	}
	return e.finishSiteAccount(ctx, v, a, log)
}
func (e *Engine) retainSiteAccount(r model.Resource, path string) error {
	e.siteUsersMu.Lock()
	defer e.siteUsersMu.Unlock()
	v, err := e.readSiteAccounts()
	if err != nil {
		return err
	}
	for _, a := range v.Accounts {
		if a.Site != r.ID {
			continue
		}
		for _, p := range a.Retained {
			if p == path {
				return nil
			}
		}
		a.Retained = append(a.Retained, path)
	}
	return e.writeSiteAccounts(v)
}

// Called after trash metadata was durably updated. Ambiguous dependencies retain
// the account. Never userdel -r and never infer ownership from its name alone.
func (e *Engine) cleanupSiteAccounts(ctx context.Context) error {
	e.siteUsersMu.Lock()
	defer e.siteUsersMu.Unlock()
	v, err := e.readSiteAccounts()
	if err != nil {
		return err
	}
	for _, a := range v.Accounts {
		if a.Phase == "deleted" || a.Phase == "creating" {
			continue
		}
		a.Reason = ""
		if err = e.siteAccountDependency(ctx, a); err != nil {
			a.Reason = err.Error()
			continue
		}
		u, er := user.Lookup(a.Name)
		if er != nil {
			if _, ok := er.(user.UnknownUserError); !ok {
				a.Reason = er.Error()
				continue
			}
		} else {
			if !siteAccountIdentity(u, a) {
				a.Reason = "账号身份或主目录已被外部修改"
				continue
			}
			// userdel may itself remove the private group on some distributions.
			if er = run(ctx, func(string) {}, "", nil, nil, "userdel", "--", a.Name); er != nil {
				a.Reason = er.Error()
				continue
			}
		}
		if g, er := user.LookupGroup(a.Group); er == nil {
			if g.Gid != a.GID {
				a.Reason = "专属组身份已改变"
				continue
			}
			if er = run(ctx, func(string) {}, "", nil, nil, "groupdel", "--", a.Group); er != nil {
				a.Reason = er.Error()
				continue
			}
		}
		a.Phase = "deleted"
	}
	return e.writeSiteAccounts(v)
}
func (e *Engine) accountForPath(path string) (string, error) {
	e.siteUsersMu.Lock()
	defer e.siteUsersMu.Unlock()
	v, err := e.readSiteAccounts()
	if err != nil {
		return "", err
	}
	for _, a := range v.Accounts {
		if a.Phase == "active" {
			for _, p := range append([]string{a.Home}, a.Retained...) {
				if pathContains(p, path) {
					return a.Name, nil
				}
			}
		}
	}
	return "", nil
}
func (e *Engine) retainRestoredAccount(name, path string) error {
	e.siteUsersMu.Lock()
	defer e.siteUsersMu.Unlock()
	v, err := e.readSiteAccounts()
	if err != nil {
		return err
	}
	if a := v.Accounts[name]; a != nil {
		u, er := user.Lookup(name)
		if er != nil || a.Phase != "active" || !siteAccountIdentity(u, a) {
			return errors.New("回收项的原运行用户身份已变化，恢复已停止，请先核对账号")
		}
		for _, p := range a.Retained {
			if p == path {
				return nil
			}
		}
		a.Retained = append(a.Retained, path)
	}
	return e.writeSiteAccounts(v)
}
func (e *Engine) siteAccountDependency(ctx context.Context, a *siteAccount) error {
	for _, r := range e.states() {
		if (r.Kind == "site" && r.ID == a.Site) || r.Config.String("user") == a.Name {
			return errors.New("仍有关联站点或进程记录")
		}
	}
	index, err := e.trashIndex()
	if err != nil {
		return err
	}
	for _, t := range index {
		if t.Account == a.Name {
			return errors.New("仍有回收站文件")
		}
	}
	for _, p := range a.Retained {
		if _, er := os.Lstat(p); er == nil {
			return errors.New("仍有保留或恢复后的文件")
		} else if !os.IsNotExist(er) {
			return er
		}
	}
	// Independent NSS and system configuration checks; failure is conservative.
	passwd, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(passwd), "\n") {
		f := strings.Split(line, ":")
		if len(f) > 3 && f[0] != a.Name && f[3] == a.GID {
			return errors.New("其他用户仍使用专属组")
		}
	}
	group, err := os.ReadFile("/etc/group")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(group), "\n") {
		f := strings.Split(line, ":")
		if len(f) == 4 && f[2] == a.GID && f[3] != "" {
			return errors.New("专属组仍有成员")
		}
	}
	proc, err := os.ReadDir("/proc")
	if err != nil {
		return err
	}
	for _, p := range proc {
		if _, er := strconv.Atoi(p.Name()); er != nil {
			continue
		}
		b, er := os.ReadFile("/proc/" + p.Name() + "/status")
		if os.IsNotExist(er) {
			continue
		}
		if er != nil {
			return er
		}
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "Uid:") {
				for _, uid := range strings.Fields(line)[1:] {
					if uid == a.UID {
						return errors.New("账号仍有运行进程")
					}
				}
			}
		}
	}
	for _, dir := range []string{"/etc/systemd/system", "/run/systemd/system", "/usr/lib/systemd/system", "/etc/cron.d", "/var/spool/cron"} {
		err = filepath.WalkDir(dir, func(p string, d os.DirEntry, er error) error {
			if os.IsNotExist(er) {
				return nil
			}
			if er != nil {
				return er
			}
			if d.IsDir() {
				return nil
			}
			if strings.Contains(filepath.Base(p), a.Name) {
				return errors.New("账号仍有服务或计划配置")
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			b, er := os.ReadFile(p)
			if er != nil {
				return er
			}
			for _, line := range strings.Split(string(b), "\n") {
				s := strings.TrimSpace(line)
				if s == "User="+a.Name || s == "User="+a.UID || s == "Group="+a.Group || s == "Group="+a.GID {
					return fmt.Errorf("账号仍被配置引用：%s", p)
				}
				if !strings.Contains(p, "systemd") {
					for _, f := range strings.Fields(s) {
						if f == a.Name {
							return errors.New("账号仍被计划引用")
						}
					}
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return scanSiteAccountFiles(ctx, a.UID, a.GID)
}

// GNU find performs the same no-follow ownership scan without a Go allocation
// and stat round trip per entry. Large build/runtime trees must not routinely
// exhaust the cleanup budget. No shell, no writes, and any incomplete scan keeps
// the account. Do not use -xdev: data on separately mounted disks also counts.
func scanSiteAccountFiles(ctx context.Context, uid, gid string) error {
	cmd := exec.CommandContext(ctx, "find", "-P", "/", "-ignore_readdir_race", "(", "-path", "/proc", "-o", "-path", "/sys", "-o", "-path", "/dev", "-o", "-path", "/run", ")", "-prune", "-o", "(", "-uid", uid, "-o", "-gid", gid, ")", "-print", "-quit")
	b, err := cmd.Output()
	if ctx.Err() != nil {
		return errors.New("文件归属检查超时，账号保留待重试")
	}
	if err != nil {
		return fmt.Errorf("文件归属检查未完整完成，账号保留待重试：%w", err)
	}
	if p := strings.TrimSpace(string(b)); p != "" {
		return fmt.Errorf("仍有账号或专属组所有的文件：%s", p)
	}
	return nil
}

func siteAccountIdentity(u *user.User, a *siteAccount) bool {
	if u.Username != a.Name || u.Uid != a.UID || u.Gid != a.GID || u.HomeDir != a.Home {
		return false
	}
	passwd, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return false
	}
	found := false
	for _, line := range strings.Split(string(passwd), "\n") {
		f := strings.Split(line, ":")
		if len(f) == 7 && f[0] == a.Name {
			found = f[6] == "/usr/sbin/nologin"
		}
	}
	if !found {
		return false
	}
	groups, err := u.GroupIds()
	if err != nil {
		return false
	}
	for _, gid := range groups {
		if gid != a.GID {
			return false
		}
	}
	return true
}
func (e *Engine) finishSiteAccount(ctx context.Context, v siteAccountRegistry, a *siteAccount, log func(string)) (*user.User, error) {
	g, err := user.LookupGroup(a.Group)
	if _, unknown := err.(user.UnknownGroupError); unknown {
		if _, er := user.LookupGroupId(a.GID); er == nil {
			return nil, errors.New("预留组 GID 被占用")
		}
		if err = run(ctx, log, "", nil, nil, "groupadd", "--system", "--gid", a.GID, "--", a.Group); err != nil {
			return nil, err
		}
		g, err = user.LookupGroup(a.Group)
	}
	if err != nil || g.Gid != a.GID {
		return nil, errors.New("专属组身份已变化")
	}
	u, err := user.Lookup(a.Name)
	if _, unknown := err.(user.UnknownUserError); unknown {
		if _, er := user.LookupId(a.UID); er == nil {
			return nil, errors.New("预留 UID 被占用")
		}
		if err = run(ctx, log, "", nil, nil, "useradd", "--system", "--uid", a.UID, "--gid", a.Group, "--no-user-group", "--no-create-home", "--home-dir", a.Home, "--shell", "/usr/sbin/nologin", "--", a.Name); err != nil {
			return nil, err
		}
		u, err = user.Lookup(a.Name)
	}
	if err != nil || !siteAccountIdentity(u, a) {
		return nil, errors.New("独立用户身份核验失败")
	}
	a.Phase = "active"
	if err = e.writeSiteAccounts(v); err != nil {
		return nil, err
	}
	return u, nil
}
