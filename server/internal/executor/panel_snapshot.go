package executor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"opsview/internal/model"
	"os"
	"path/filepath"
	"strings"
)

// Snapshot only managed configuration trees. Database files, business data,
// environment files and executor/master keys are deliberately outside this set.
func (e *Engine) panelConfigurationSnapshot(ctx context.Context) (model.Config, error) {
	e.siteMu.Lock()
	defer e.siteMu.Unlock()
	files := map[string]string{}
	var total int64
	for _, kind := range []string{"instances", "sites"} {
		base := filepath.Join(e.Root, kind)
		root, err := os.OpenRoot(base)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			parts := strings.Split(filepath.ToSlash(name), "/")
			if d.IsDir() {
				if len(parts) > 1 && parts[1] != "conf" && kind == "instances" {
					return fs.SkipDir
				}
				if kind == "sites" && len(parts) > 1 && (parts[1] == "www" || parts[1] == "logs") {
					return fs.SkipDir
				}
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			ext := filepath.Ext(name)
			if ext != ".conf" && ext != ".ini" {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			total += info.Size()
			if info.Size() > 1<<20 || total > 32<<20 {
				return errors.New("受管配置快照超过限制")
			}
			data, err := root.ReadFile(name)
			if err != nil {
				return err
			}
			files[filepath.ToSlash(filepath.Join(kind, name))] = base64.StdEncoding.EncodeToString(data)
			return nil
		})
		root.Close()
		if err != nil {
			return nil, err
		}
	}
	result := model.Config{"formatVersion": 1, "files": files, "resources": e.states(), "restoreMode": "review-only"}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if len(encoded) > 48<<20 {
		return nil, errors.New("配置及资源快照超过 48 MiB 限制")
	}
	return result, nil
}
