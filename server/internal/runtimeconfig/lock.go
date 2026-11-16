//go:build linux || darwin

package runtimeconfig

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Serialize root CLI changes with the executor's reconnect/rollback watcher.
// Lock a separate inode because panel.json itself is replaced atomically.
func Lock(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = f.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, errors.New("其他进程正在修改面板访问设置，请稍后重试")
		}
		time.Sleep(25 * time.Millisecond)
	}
}
