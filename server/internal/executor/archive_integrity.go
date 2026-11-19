package executor

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
)

func archiveExcluded(name string, patterns []string) bool {
	for _, p := range patterns {
		match, _ := filepath.Match(p, name)
		base, _ := filepath.Match(p, filepath.Base(name))
		if match || base {
			return true
		}
	}
	return false
}
func preflightArchive(ctx context.Context, root *os.Root, excludes []string, allow func(string) bool) (int64, error) {
	var size int64
	count := 0
	err := fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		if archiveExcluded(name, excludes) || allow != nil && !allow(name) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		st, err := d.Info()
		if err != nil {
			return err
		}
		if !st.IsDir() && !st.Mode().IsRegular() {
			return nil
		}
		count++
		if count > 1000000 {
			return errors.New("归档条目超过上限")
		}
		if st.Mode().IsRegular() {
			if st.Size() > fileArchiveLimit-size {
				return errors.New("归档超过 8 GiB 限制")
			}
			size += st.Size()
		}
		return nil
	})
	return size, err
}
func (e *Engine) decryptTrustedArchive(path string) (string, error) {
	key := filepath.Join(e.Root, "backup.key")
	if _, err := os.Stat(key); err != nil {
		return "", errors.New("恢复或完整性检查需要原执行器 backup.key")
	}
	v, err := security.OpenVault(key)
	if err != nil {
		return "", err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "verify-*.plain")
	if err != nil {
		return "", err
	}
	temp.Close()
	os.Remove(temp.Name())
	if err = decryptBackup(v, path, temp.Name()); err != nil {
		os.Remove(temp.Name())
		return "", err
	}
	return temp.Name(), nil
}
func verifyArchiveTree(ctx context.Context, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	count := 0
	var size int64
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		h, er := tr.Next()
		if er == io.EOF {
			break
		}
		if er != nil {
			return er
		}
		count++
		if count > 1000000 {
			return errors.New("归档条目超过上限")
		}
		if _, er = archiveName(h.Name); er != nil {
			return er
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir {
			return errors.New("归档包含链接或特殊文件")
		}
		if h.Size < 0 || h.Size > fileArchiveLimit-size {
			return errors.New("归档大小无效")
		}
		size += h.Size
		if _, er = io.Copy(io.Discard, &contextReader{ctx: ctx, r: tr}); er != nil {
			return er
		}
	}
	_, err = io.Copy(io.Discard, &contextReader{ctx: ctx, r: gz})
	return err
}
func (e *Engine) archiveStorage(record model.Resource, m model.Config) (model.Config, bool, error) {
	if id := m.String("remoteStorageId"); id != "" {
		if !regexpArchiveID(id) {
			return nil, false, errors.New("存储标识无效")
		}
		stored, err := e.load(id)
		if err != nil || stored.Kind != "backup-storage" {
			return nil, false, errors.New("备份原存储配置缺失")
		}
		return stored.Config, true, nil
	}
	if record.Config.String("destination") == "s3" {
		return record.Config, true, nil
	}
	return nil, false, nil
}
func (e *Engine) ensureArchiveLocal(ctx context.Context, record model.Resource, m model.Config, path string) error {
	if st, err := os.Lstat(path); err == nil {
		if !st.Mode().IsRegular() {
			return errors.New("备份不是普通文件")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	cfg, remote, err := e.archiveStorage(record, m)
	if err != nil {
		return err
	}
	if !remote {
		return os.ErrNotExist
	}
	return downloadS3(ctx, cfg, path, record.ID+"/"+m.String("file"), m)
}
