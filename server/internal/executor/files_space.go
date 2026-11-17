package executor

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func ensureFileSpace(file *os.File, required int64) error {
	var st unix.Statfs_t
	if err := unix.Fstatfs(int(file.Fd()), &st); err != nil {
		return err
	}
	if required < 0 || uint64(required)+(16<<20) > uint64(st.Bavail)*uint64(st.Bsize) {
		return errors.New("目标磁盘空间不足，需额外保留 16 MiB")
	}
	return nil
}
func ensureRootSpace(root *os.Root, required int64) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return ensureFileSpace(dir, required)
}
