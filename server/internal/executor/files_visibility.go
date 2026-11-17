package executor

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"opsview/internal/model"
	"os"
	"path/filepath"
)

// One snapshot per list/search/metadata request, never one tree walk per entry.
// This is a presentation filter; actual reads/writes still validate pinned handles.
type fileVisibility struct {
	private    []string
	identities map[string]bool
}

func (e *Engine) fileVisibility(ctx context.Context) (*fileVisibility, error) {
	v := &fileVisibility{private: e.filePrivatePaths(), identities: map[string]bool{}}
	for _, path := range v.private {
		err := filepath.WalkDir(path, func(path string, entry fs.DirEntry, err error) error {
			if er := ctx.Err(); er != nil {
				return er
			}
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			v.identities[fileIdentity(info)] = true
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return v, nil
}
func (v *fileVisibility) visible(path string, st os.FileInfo) bool {
	if checkFilePolicyPaths(filepath.Clean(path), false, v.private) != nil || v.identities[fileIdentity(st)] {
		return false
	}
	// Resolve aliases (including symlinked parents) before returning names/targets.
	actual, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	if checkFilePolicyPaths(actual, false, v.private) != nil {
		return false
	}
	target, err := os.Stat(actual)
	return err == nil && !v.identities[fileIdentity(target)] && (target.IsDir() || target.Mode().IsRegular())
}
func (e *Engine) fileMetadata(ctx context.Context, p model.Config) (model.Config, error) {
	v, err := e.fileVisibility(ctx)
	if err != nil {
		return nil, err
	}
	filter := func(paths []string) []string {
		out := []string{}
		seen := map[string]bool{}
		for _, path := range paths {
			if ctx.Err() != nil {
				break
			}
			if !filepath.IsAbs(path) || seen[path] {
				continue
			}
			st, err := os.Lstat(path)
			if err != nil || !v.visible(path, st) {
				continue
			}
			target, err := os.Stat(path)
			if err == nil && target.IsDir() {
				out = append(out, path)
				seen[path] = true
			}
		}
		return out
	}
	favorites := []string{}
	_ = e.readFileMeta("favorites", &favorites)
	recent := []string{}
	if raw, ok := p["recentPaths"]; ok {
		b, err := json.Marshal(raw)
		if err != nil || json.Unmarshal(b, &recent) != nil || len(recent) > 15 {
			return nil, errors.New("最近访问最多包含 15 个目录")
		}
	}
	shortcuts := []model.Config{}
	for _, shortcut := range []model.Config{{"name": "根目录", "path": "/"}, {"name": "站点目录", "path": filepath.Join(e.Root, "sites")}, {"name": "备份目录", "path": filepath.Join(e.Root, "backups")}} {
		if len(filter([]string{shortcut.String("path")})) != 0 {
			shortcuts = append(shortcuts, shortcut)
		}
	}
	result := model.Config{"favorites": filter(favorites), "recentPaths": filter(recent), "shortcuts": shortcuts, "uploadLimit": fileUploadLimit}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
