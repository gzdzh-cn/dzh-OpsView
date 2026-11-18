package executor

import (
	"errors"
	"os"
	"path/filepath"
)

// The executor runs with umask 0027. Public runtime binaries must remain
// traversable by the dedicated service user; data and logs stay private.
func prepareInstanceLayout(base string) error {
	for rel, mode := range map[string]os.FileMode{"": 0711, "runtime": 0755, "conf": 0755, "data": 0700, "logs": 0700} {
		p := filepath.Join(base, rel)
		if err := os.MkdirAll(p, mode); err != nil {
			return err
		}
		st, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return errors.New("实例目录必须为普通受管目录")
		}
		if err = os.Chmod(p, mode); err != nil {
			return err
		}
	}
	return nil
}

// Only runtime artifacts are public; never apply this to configuration or data.
func prepareRuntimePermissions(root string) error {
	return filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		st, err := d.Info()
		if err != nil {
			return err
		}
		mode := st.Mode().Perm()
		if d.IsDir() || mode&0111 != 0 {
			mode |= 0055
		} else if st.Mode().IsRegular() {
			mode |= 0044
		} else {
			return errors.New("运行时包含特殊文件")
		}
		return os.Chmod(p, mode)
	})
}
