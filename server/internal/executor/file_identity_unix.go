//go:build linux || darwin

package executor

import (
	"fmt"
	"os"
	"syscall"
)

func fileIdentity(st os.FileInfo) string {
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("%d:%d", s.Dev, s.Ino)
	}
	return st.Name()
}
