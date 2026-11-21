package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"opsview/internal/logic"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

type sitePermissionEntry struct {
	Path       string      `json:"path"`
	Identity   string      `json:"identity"`
	Revision   string      `json:"revision"`
	UID        int         `json:"uid"`
	GID        int         `json:"gid"`
	Mode       os.FileMode `json:"mode"`
	ACL        string      `json:"acl"`
	AppliedACL string      `json:"appliedAcl,omitempty"`
}
type sitePermissionJournal struct {
	Version int                   `json:"version"`
	Root    string                `json:"root"`
	Entries []sitePermissionEntry `json:"entries"`
	UID     int                   `json:"uid"`
	GID     int                   `json:"gid"`
	Phase   string                `json:"phase"`
}

// Commands receive the pinned descriptor, not a path the application can swap.
func siteACL(ctx context.Context, f *os.File, input string) (string, error) {
	var cmd *exec.Cmd
	if input == "" {
		cmd = exec.CommandContext(ctx, "getfacl", "-cpn", "/proc/self/fd/3")
	} else {
		if st, err := f.Stat(); err != nil {
			return "", err
		} else if st.IsDir() && !strings.Contains(input, "default:") {
			clear := exec.CommandContext(ctx, "setfacl", "--remove-default", "/proc/self/fd/3")
			clear.ExtraFiles = []*os.File{f}
			if err = clear.Run(); err != nil {
				return "", fmt.Errorf("恢复默认 ACL 失败：%w", err)
			}
		}
		cmd = exec.CommandContext(ctx, "setfacl", "--set-file=-", "/proc/self/fd/3")
		cmd.Stdin = strings.NewReader(input)
	}
	cmd.ExtraFiles = []*os.File{f}
	b, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("文件系统 ACL 操作失败：%w", err)
	}
	lines := strings.FieldsFunc(string(b), func(r rune) bool { return r == '\n' || r == '\r' })
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n", nil
}
func openSitePermissionFile(root *os.Root, path, identity string) (*os.File, error) {
	f, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || (!st.IsDir() && !st.Mode().IsRegular()) || (identity != "" && fileIdentity(st) != identity) {
		f.Close()
		return nil, errors.New("项目文件身份已变化")
	}
	return f, nil
}
func (e *Engine) inspectSitePermissions(ctx context.Context, r model.Resource) (sitePermissionJournal, string, error) {
	var j sitePermissionJournal
	root := r.Config.String("documentRoot")
	if root == "" {
		root = r.Config.String("rootDirectory")
	}
	if err := logic.ValidateSiteRootAt(root, r.Config.String("siteBaseDirectory")); err != nil {
		return j, "", err
	}
	if _, err := safePath("/", strings.TrimPrefix(root, "/")); err != nil {
		return j, "", err
	}
	for _, other := range e.states() {
		if other.Kind == "site" && other.ID != r.ID {
			p := siteDocumentRoot(other, filepath.Join(e.Root, "sites", other.ID))
			if pathContains(root, p) || pathContains(p, root) {
				return j, "", errors.New("目录与其他站点交叠，不能整理权限")
			}
		}
	}
	j.Root = root
	scoped, err := os.OpenRoot(root)
	if os.IsNotExist(err) {
		return j, security.Hash(root + ":empty"), nil
	}
	if err != nil {
		return j, "", err
	}
	defer scoped.Close()
	st, err := scoped.Stat(".")
	if err != nil {
		return j, "", err
	}
	_, _, device := fileOwner(st)
	err = fs.WalkDir(scoped.FS(), ".", func(p string, d fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			return walkErr
		}
		if len(j.Entries) >= 100000 {
			return errors.New("目录超过十万项，请分批整理后重试")
		}
		st, er := scoped.Lstat(p)
		if er != nil {
			return er
		}
		_, _, dev := fileOwner(st)
		if dev != device {
			return errors.New("项目目录包含其他挂载点")
		}
		if st.Mode()&os.ModeSymlink != 0 {
			target, er := filepath.EvalSymlinks(filepath.Join(root, p))
			if er != nil || !pathContains(root, target) {
				return fmt.Errorf("链接指向项目外或目标不可用：%s", p)
			}
			return nil
		}
		if stat, ok := st.Sys().(*syscall.Stat_t); !ok || (st.Mode().IsRegular() && stat.Nlink > 1) {
			return fmt.Errorf("文件身份不明或存在硬链接：%s", p)
		}
		if st.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return fmt.Errorf("项目包含特殊权限：%s", p)
		}
		f, er := openSitePermissionFile(scoped, p, fileIdentity(st))
		if er != nil {
			return er
		}
		defer f.Close()
		acl, er := siteACL(ctx, f, "")
		if er != nil {
			return er
		}
		uid, gid, _ := fileOwner(st)
		j.Entries = append(j.Entries, sitePermissionEntry{Path: p, Identity: fileIdentity(st), Revision: fileRevision(st), UID: uid, GID: gid, Mode: st.Mode(), ACL: acl})
		return nil
	})
	if err != nil {
		return j, "", err
	}
	b, _ := json.Marshal(j)
	return j, security.Hash(string(b)), nil
}
func siteDesiredACL(entry sitePermissionEntry, nginxUID string) string {
	x := "-"
	if entry.Mode.IsDir() || entry.Mode.Perm()&0111 != 0 {
		x = "x"
	}
	read := "r--"
	if entry.Mode.IsDir() {
		read = "r-x"
	}
	acl := "user::rw" + x + "\nuser:" + nginxUID + ":" + read + "\ngroup::---\nmask::" + read + "\nother::---\n"
	if entry.Mode.IsDir() {
		acl += "default:user::rwx\ndefault:user:" + nginxUID + ":r-x\ndefault:group::---\ndefault:mask::r-x\ndefault:other::---\n"
	}
	return acl
}
func (e *Engine) sitePermissionPath(id string) string {
	return filepath.Join(e.Root, "site-users", "permissions-"+id+".json")
}
func (e *Engine) writePermissionJournal(id string, j sitePermissionJournal) error {
	if err := os.MkdirAll(filepath.Join(e.Root, "site-users"), 0700); err != nil {
		return err
	}
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	return security.WriteAtomic(e.sitePermissionPath(id), b, 0600)
}
func (e *Engine) restoreSitePermissions(ctx context.Context, id string) error {
	b, err := os.ReadFile(e.sitePermissionPath(id))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var j sitePermissionJournal
	if err = json.Unmarshal(b, &j); err != nil {
		return err
	}
	if j.Version != 1 {
		return errors.New("权限事务版本无效")
	}
	if j.Phase == "committed" || j.Phase == "restored" {
		return nil
	}
	scoped, err := os.OpenRoot(j.Root)
	if err != nil {
		return err
	}
	defer scoped.Close()
	var failures []error
	for i := len(j.Entries) - 1; i >= 0; i-- {
		v := j.Entries[i]
		f, er := openSitePermissionFile(scoped, v.Path, v.Identity)
		if er != nil {
			failures = append(failures, er)
			continue
		}
		st, er := f.Stat()
		if er == nil {
			uid, gid, _ := fileOwner(st)
			acl, readErr := siteACL(ctx, f, "")
			if uid == v.UID && gid == v.GID && acl == v.ACL {
				f.Close()
				continue
			}
			applied := strings.FieldsFunc(v.AppliedACL, func(r rune) bool { return r == '\n' || r == '\r' })
			sort.Strings(applied)
			if readErr != nil || ((uid != j.UID || gid != j.GID) && !(uid == v.UID && gid == v.GID)) || (acl != v.ACL && acl != strings.Join(applied, "\n")+"\n") {
				er = errors.New("权限被外部修改，保留现场")
			}
			if er == nil {
				er = f.Chown(v.UID, v.GID)
			}
			if er == nil {
				_, er = siteACL(ctx, f, v.ACL)
			}
			if er == nil {
				er = f.Chmod(v.Mode.Perm())
			}
		}
		f.Close()
		if er != nil {
			failures = append(failures, fmt.Errorf("%s: %w", v.Path, er))
		}
	}
	if err = errors.Join(failures...); err != nil {
		return err
	}
	j.Phase = "restored"
	return e.writePermissionJournal(id, j)
}
func (e *Engine) applySitePermissions(ctx context.Context, r model.Resource, uid, gid int) error {
	if _, err := exec.LookPath("setfacl"); err != nil {
		return errors.New("缺少 ACL 工具，请先安装 acl 软件包")
	}
	// A previous interrupted transaction must be resolved before another snapshot.
	if err := e.restoreSitePermissions(ctx, r.ID); err != nil {
		return fmt.Errorf("上次权限调整尚未恢复：%w", err)
	}
	j, fingerprint, err := e.inspectSitePermissions(ctx, r)
	if err != nil {
		return err
	}
	if (r.Config.Bool("adoptDirectory") || len(j.Entries) > 1) && r.Config.String("permissionPreview") != fingerprint {
		return errors.New("权限预览已失效，请重新检查并确认目录内容")
	}
	nginx, err := userLookupNginx()
	if err != nil {
		return err
	}
	j.Version = 1
	j.UID, j.GID, j.Phase = uid, gid, "applying"
	for i := range j.Entries {
		j.Entries[i].AppliedACL = siteDesiredACL(j.Entries[i], nginx)
	}
	if err = e.writePermissionJournal(r.ID, j); err != nil {
		return err
	}
	scoped, err := os.OpenRoot(j.Root)
	if err != nil {
		return err
	}
	defer scoped.Close()
	for _, v := range j.Entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		f, er := openSitePermissionFile(scoped, v.Path, v.Identity)
		if er != nil {
			return er
		}
		st, er := f.Stat()
		if er == nil && fileRevision(st) != v.Revision {
			er = errors.New("文件在预检后变化，请重新检查")
		}
		if er == nil {
			er = f.Chown(uid, gid)
		}
		if er == nil {
			_, er = siteACL(ctx, f, v.AppliedACL)
		}
		f.Close()
		if er != nil {
			return er
		}
	}
	return nil
}
func userLookupNginx() (string, error) {
	u, err := user.Lookup("www-data")
	if err != nil {
		return "", errors.New("Nginx www-data 用户不存在")
	}
	_, err = strconv.Atoi(u.Uid)
	return u.Uid, err
}
