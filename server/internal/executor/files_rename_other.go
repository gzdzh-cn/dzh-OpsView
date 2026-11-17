//go:build !linux

package executor

import (
	"os"
	"path/filepath"
	"syscall"
)

func renameFileEntry(a *os.Root, an string, b *os.Root, bn string, noReplace bool) error {
	if noReplace {
		if _, err := b.Lstat(bn); !os.IsNotExist(err) {
			return os.ErrExist
		}
	}
	return os.Rename(filepath.Join(a.Name(), an), filepath.Join(b.Name(), bn))
}
func syscallEXDEV() error             { return syscall.EXDEV }
func hasNestedMount(path string) bool { return false }

func fileNoFollow() int { return syscall.O_NOFOLLOW | syscall.O_NONBLOCK }
