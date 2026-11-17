//go:build linux

package executor

import (
	"os"
	"syscall"
)

func fileChangeStamp(st os.FileInfo) int64 {
	if value, ok := st.Sys().(*syscall.Stat_t); ok {
		return value.Ctim.Sec*1e9 + value.Ctim.Nsec
	}
	return 0
}
