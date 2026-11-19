package executor

import (
	"errors"
	"opsview/internal/model"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func (e *Engine) backupBase(r model.Resource) (string, error) {
	dir := r.Config.String("storageDirectory")
	if dir == "" {
		return filepath.Join(e.Root, "backups", r.ID), nil
	}
	if err := e.checkFilePath(dir, true); err != nil {
		return "", err
	}
	if e.criticalFilePath(dir) {
		return "", errors.New("备份存储不能是系统关键目录")
	}
	dir = filepath.Clean(dir)
	for _, protected := range []string{"/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/opt/opsview"} {
		if pathContains(protected, dir) {
			return "", errors.New("备份存储不能位于系统程序或配置目录")
		}
	}
	// Create through a pinned root and reject symlink components, including pre-existing leaf directories.
	root, err := os.OpenRoot("/")
	if err != nil {
		return "", err
	}
	defer root.Close()
	current := ""
	for _, part := range strings.Split(strings.TrimPrefix(dir, "/"), "/") {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		if err = root.Mkdir(current, 0755); err != nil && !os.IsExist(err) {
			return "", err
		}
		st, er := root.Lstat(current)
		if er != nil {
			return "", er
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("备份存储路径不能包含链接")
		}
		if st.Mode().Perm()&0022 != 0 && st.Mode()&os.ModeSticky == 0 {
			return "", errors.New("备份存储父目录不能允许其他用户写入")
		}
	}
	h, err := e.filePath(dir, true)
	if err != nil {
		return "", err
	}
	defer h.close()
	base, err := h.root.OpenRoot(h.name)
	if err != nil {
		return "", err
	}
	defer base.Close()
	if err = base.Mkdir(r.ID, 0700); err != nil && !os.IsExist(err) {
		return "", err
	}
	st, err := base.Lstat(r.ID)
	if err != nil {
		return "", err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0077 != 0 {
		return "", errors.New("备份实例目录须为私有普通目录")
	}
	return filepath.Join(h.path, r.ID), nil
}
func (e *Engine) preparePanelBackupDirectory(dir string) (string, error) {
	base, err := e.backupBase(model.Resource{ID: "opsview-panel", Config: model.Config{"storageDirectory": dir}})
	if err != nil {
		return "", err
	}
	h, err := e.filePath(base, false)
	if err != nil {
		return "", err
	}
	defer h.close()
	f, err := h.root.OpenFile(h.name, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.IsDir() {
		return "", errors.New("面板备份路径不是普通目录")
	}
	u, err := user.Lookup("opsview")
	if err != nil {
		return "", err
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	stat := st.Sys().(*syscall.Stat_t)
	if stat.Uid != 0 && int(stat.Uid) != uid {
		return "", errors.New("已有备份目录属于其他用户，请选择新目录")
	}
	if err = f.Chown(uid, gid); err != nil {
		return "", err
	}
	if err = f.Chmod(0700); err != nil {
		return "", err
	}
	return base, nil
}
func (e *Engine) registeredBackupBase(id string) (string, error) {
	r, err := e.load(id)
	if err != nil {
		return "", err
	}
	return e.backupBase(r)
}
