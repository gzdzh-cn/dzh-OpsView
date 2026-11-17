//go:build linux

package executor

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

func fileDescriptorPath(f *os.File, fallback string) (string, error) {
	p, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", f.Fd()))
	if err != nil {
		return "", err
	}
	if strings.HasSuffix(p, " (deleted)") {
		return "", errors.New("目录已被移除，请刷新")
	}
	return p, nil
}
