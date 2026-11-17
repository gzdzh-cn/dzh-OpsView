//go:build darwin

package executor

import (
	"os"
	"syscall"
)

func fileChangeStamp(st os.FileInfo) int64 {
	if value, ok := st.Sys().(*syscall.Stat_t); ok {
		return value.Ctimespec.Sec*1e9 + value.Ctimespec.Nsec
	}
	return 0
}
