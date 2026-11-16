package executor

import (
	"context"
	"errors"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Keep the per-site FPM documents and unit recoverable until Nginx has accepted
// the new website. Credentials stay in memory and are never logged.
func (e *Engine) snapshotSitePHP(ctx context.Context, site model.Resource, base string) (func() error, error) {
	unit := phpSiteUnit(site)
	paths := []string{base + "/conf/php.ini", base + "/conf/php-fpm.conf", filepath.Join("/etc/systemd/system", unit)}
	undo, err := snapshotManagedFiles(paths)
	if err != nil {
		return nil, err
	}
	active, _ := output(ctx, "systemctl", "is-active", unit)
	enabled, _ := output(ctx, "systemctl", "is-enabled", unit)
	return func() error {
		recovery, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		writeErr := undo()
		reloadErr := run(recovery, func(string) {}, "", nil, nil, "systemctl", "daemon-reload")
		boot := "disable"
		if strings.TrimSpace(enabled) == "enabled" {
			boot = "enable"
		}
		// A newly created unit is removed by undo; stopping an unknown unit is
		// harmless and must not turn a successful file rollback into an error.
		if _, err := os.Stat(paths[2]); os.IsNotExist(err) {
			_ = systemctl(recovery, func(string) {}, "stop", unit)
			return errors.Join(writeErr, reloadErr)
		}
		bootErr := systemctl(recovery, func(string) {}, boot, unit)
		action := "stop"
		if strings.TrimSpace(active) == "active" {
			action = "restart"
		}
		serviceErr := systemctl(recovery, func(string) {}, action, unit)
		return errors.Join(writeErr, reloadErr, bootErr, serviceErr)
	}, nil
}

func snapshotManagedFiles(paths []string) (func() error, error) {
	type snapshot struct {
		path   string
		data   []byte
		mode   os.FileMode
		exists bool
	}
	files := []snapshot{}
	for _, path := range paths {
		if _, err := safePath(filepath.Dir(path), filepath.Base(path)); err != nil {
			return nil, err
		}
		st, err := os.Stat(path)
		if os.IsNotExist(err) {
			files = append(files, snapshot{path: path})
			continue
		}
		if err != nil {
			return nil, err
		}
		if !st.Mode().IsRegular() || st.Size() > 1<<20 {
			return nil, errors.New("受管配置文件类型或大小无效")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		files = append(files, snapshot{path, data, st.Mode().Perm(), true})
	}
	return func() error {
		var failures []error
		for _, f := range files {
			var err error
			if f.exists {
				if _, er := os.Stat(f.path); er == nil {
					err = writeOwnedAtomic(f.path, f.data, f.mode)
				} else {
					err = security.WriteAtomic(f.path, f.data, f.mode)
				}
			} else {
				err = os.Remove(f.path)
				if os.IsNotExist(err) {
					err = nil
				}
			}
			failures = append(failures, err)
		}
		return errors.Join(failures...)
	}, nil
}
