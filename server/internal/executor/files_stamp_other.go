//go:build !linux && !darwin

package executor

import "os"

func fileChangeStamp(st os.FileInfo) int64 { return 0 }
