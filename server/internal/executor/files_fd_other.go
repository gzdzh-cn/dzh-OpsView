//go:build !linux

package executor

import (
	"errors"
	"os"
	"path/filepath"
)

func fileDescriptorPath(f *os.File, fallback string) (string, error) {
	p, err := filepath.EvalSymlinks(fallback)
	if err != nil {
		return "", err
	}
	a, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	b, err := f.Stat()
	if err != nil || !os.SameFile(a, b) {
		return "", errors.New("目录已变化，请重试")
	}
	return p, nil
}
