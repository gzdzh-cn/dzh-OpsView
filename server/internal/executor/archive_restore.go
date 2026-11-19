package executor

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"opsview/internal/security"
	"os"
	"path/filepath"
)

func (e *Engine) restoreDirectoryArchive(ctx context.Context, archive, destination string) error {
	if e.criticalFilePath(filepath.Clean(destination)) {
		return errors.New("不能恢复到系统关键目录")
	}
	target, err := e.filePath(destination, false)
	if err != nil {
		return err
	}
	defer target.close()
	if _, err = target.root.Lstat(target.name); !os.IsNotExist(err) {
		return errors.New("恢复目录必须尚不存在")
	}
	temporary := ".opsview-extract-" + security.ID()
	if err = target.root.Mkdir(temporary, 0700); err != nil {
		return err
	}
	defer target.root.RemoveAll(temporary)
	stage, err := target.root.OpenRoot(temporary)
	if err != nil {
		return err
	}
	defer stage.Close()
	in, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer in.Close()
	gz, err := gzip.NewReader(in)
	if err != nil {
		return err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	count := 0
	var total int64
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		header, er := reader.Next()
		if er == io.EOF {
			break
		}
		if er != nil {
			return er
		}
		count++
		if count > 1000000 {
			return errors.New("归档超过条目上限")
		}
		name, er := archiveName(header.Name)
		if er != nil {
			return er
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir {
			return errors.New("归档仅允许普通文件和目录")
		}
		if header.Size < 0 || header.Size > fileArchiveLimit-total {
			return errors.New("归档超过 8 GiB 限制")
		}
		total += header.Size
		if er = ensureRootSpace(stage, header.Size); er != nil {
			return er
		}
		if er = stageArchiveEntry(ctx, stage, name, header.Typeflag == tar.TypeDir, header.Size, os.FileMode(header.Mode)&0777, reader); er != nil {
			return er
		}
	}
	if _, err = target.root.Lstat(target.name); !os.IsNotExist(err) {
		return errors.New("恢复期间目标被创建，拒绝覆盖")
	}
	return renameFileEntry(target.root, temporary, target.root, target.name, true)
}
