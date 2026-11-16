//go:build linux || darwin

package executor

import (
	"os"
	"path/filepath"
	"syscall"
)

// Preserve uid/gid before promotion so no connection observes a root-owned replacement.
func writeOwnedAtomic(path string, b []byte, mode os.FileMode) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	s, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return os.ErrInvalid
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".opsview-write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(mode); err != nil {
		return err
	}
	if err = f.Chown(int(s.Uid), int(s.Gid)); err != nil {
		return err
	}
	if _, err = f.Write(b); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
