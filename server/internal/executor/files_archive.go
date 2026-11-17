package executor

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
)

const fileArchiveLimit int64 = 8 << 30

func (e *Engine) promoteFile(from, to, policy string) error {
	a, err := os.OpenRoot(filepath.Dir(from))
	if err != nil {
		return err
	}
	defer a.Close()
	b, err := e.filePath(to, false)
	if err != nil {
		return err
	}
	defer b.close()
	return e.publishFileEntry(a, filepath.Base(from), b, policy)
}
func (e *Engine) fileArchive(ctx context.Context, action string, p model.Config, log func(string)) (model.Config, error) {
	if action == "compress" {
		return e.compressFiles(ctx, p, log)
	}
	return e.extractFiles(ctx, p, log)
}
func (e *Engine) compressFiles(ctx context.Context, p model.Config, log func(string)) (model.Config, error) {
	target, skip, err := e.destination(p.String("target"), p.String("conflict"))
	if err != nil {
		return nil, err
	}
	if skip {
		return model.Config{"skipped": true}, nil
	}
	paths := stringPaths(p)
	if len(paths) == 0 || len(paths) > 1000 {
		return nil, errors.New("请选择压缩内容")
	}
	for _, path := range paths {
		if pathContains(filepath.Clean(path), target) {
			return nil, errors.New("压缩包不能放在源目录内")
		}
	}
	f, err := e.filePath(target, false)
	if err != nil {
		return nil, err
	}
	defer f.close()
	tmp := ".opsview-copy-" + security.ID()
	out, err := f.root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer f.root.Remove(tmp)
	defer out.Close()
	var zw *zip.Writer
	var gz *gzip.Writer
	var tw *tar.Writer
	if strings.HasSuffix(strings.ToLower(target), ".zip") {
		zw = zip.NewWriter(out)
	} else if strings.HasSuffix(strings.ToLower(target), ".tar.gz") {
		gz = gzip.NewWriter(out)
		tw = tar.NewWriter(gz)
	} else {
		return nil, errors.New("只支持 ZIP 或 TAR.GZ")
	}
	count := 0
	var total int64
	for _, source := range paths {
		err = e.walkFiles(ctx, source, func(path string, st os.FileInfo) error {
			if er := ensureFileSpace(out, st.Size()); er != nil {
				return er
			}
			count++
			if count > 10000 {
				return errors.New("归档超过 10000 项")
			}
			total += st.Size()
			if total > fileArchiveLimit {
				return errors.New("归档超过 8 GiB")
			}
			rel, er := filepath.Rel(filepath.Dir(source), path)
			if er != nil {
				return er
			}
			name := filepath.ToSlash(rel)
			var writer io.Writer
			if zw != nil {
				h, er := zip.FileInfoHeader(st)
				if er != nil {
					return er
				}
				h.Name = name
				h.Method = zip.Deflate
				if st.IsDir() {
					h.Name += "/"
				}
				writer, er = zw.CreateHeader(h)
				if er != nil {
					return er
				}
			} else {
				h, er := tar.FileInfoHeader(st, "")
				if er != nil {
					return er
				}
				h.Name = name
				if er = tw.WriteHeader(h); er != nil {
					return er
				}
				writer = tw
			}
			if st.Mode().IsRegular() {
				in, er := e.openFile(path, false)
				if er != nil {
					return er
				}
				defer in.Close()
				_, er = io.Copy(writer, &contextReader{ctx: ctx, r: in})
				return er
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	if zw != nil {
		err = zw.Close()
	} else {
		err = tw.Close()
		if err == nil {
			err = gz.Close()
		}
	}
	if err != nil {
		return nil, err
	}
	if err = out.Sync(); err != nil {
		return nil, err
	}
	if err = out.Close(); err != nil {
		return nil, err
	}
	if err = e.publishFileEntry(f.root, tmp, f, p.String("conflict")); err != nil {
		return nil, err
	}
	log(fmt.Sprintf("压缩完成：%d 项", count))
	return model.Config{"path": target, "count": count}, nil
}
func archiveName(name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if name == "" || filepath.IsAbs(name) || clean == ".." || strings.HasPrefix(clean, "../") || strings.ContainsAny(name, "\x00\\") {
		return "", errors.New("归档含越界路径")
	}
	return clean, nil
}
func (e *Engine) extractFiles(ctx context.Context, p model.Config, log func(string)) (model.Config, error) {
	source, err := e.openFile(p.String("path"), true)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	st, err := source.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return nil, errors.New("请选择普通压缩文件")
	}
	target := p.String("target")
	if target == "" {
		return nil, errors.New("请选择解压目录")
	}
	dest, err := e.openFile(target, true)
	if err != nil {
		return nil, err
	}
	target, err = fileDescriptorPath(dest, target)
	dest.Close()
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(target)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	stageName := ".opsview-extract-" + security.ID()
	if err = root.Mkdir(stageName, 0700); err != nil {
		return nil, err
	}
	defer root.RemoveAll(stageName)
	stage, err := root.OpenRoot(stageName)
	if err != nil {
		return nil, err
	}
	defer stage.Close()
	type entry struct {
		name string
		size int64
		mode os.FileMode
		dir  bool
		open func() (io.ReadCloser, error)
	}
	entries := []entry{}
	var total int64
	validate := func(name string, size int64, mode os.FileMode, isDir bool) error {
		clean, er := archiveName(name)
		if er != nil {
			return er
		}
		if mode&os.ModeSymlink != 0 || (!mode.IsRegular() && !isDir) {
			return errors.New("归档含链接或特殊文件")
		}
		if er = e.checkFilePath(filepath.Join(target, clean), true); er != nil {
			return er
		}
		if e.criticalFilePath(filepath.Join(target, clean)) {
			return errors.New("归档试图覆盖关键路径")
		}
		if size < 0 || size > fileArchiveLimit-total {
			return errors.New("解压超过 8 GiB")
		}
		total += size
		if len(entries) >= 10000 {
			return errors.New("归档超过 10000 项")
		}
		return nil
	}
	lower := strings.ToLower(p.String("path"))
	if strings.HasSuffix(lower, ".zip") {
		zr, er := zip.NewReader(source, st.Size())
		if er != nil {
			return nil, er
		}
		for _, item := range zr.File {
			if item.UncompressedSize64 > uint64(fileArchiveLimit) {
				return nil, errors.New("解压文件过大")
			}
			if er = validate(item.Name, int64(item.UncompressedSize64), item.Mode(), item.FileInfo().IsDir()); er != nil {
				return nil, er
			}
			clean, _ := archiveName(item.Name)
			entries = append(entries, entry{name: clean, size: int64(item.UncompressedSize64), mode: item.Mode(), dir: item.FileInfo().IsDir(), open: item.Open})
		}
	} else if strings.HasSuffix(lower, ".tar.gz") {
		gz, er := gzip.NewReader(source)
		if er != nil {
			return nil, er
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			h, er := tr.Next()
			if er == io.EOF {
				break
			}
			if er != nil {
				return nil, er
			}
			if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir {
				return nil, errors.New("TAR 只允许普通文件和目录")
			}
			mode := os.FileMode(h.Mode) & 0777
			if h.Typeflag == tar.TypeDir {
				mode |= os.ModeDir
			}
			if er = validate(h.Name, h.Size, mode, h.Typeflag == tar.TypeDir); er != nil {
				return nil, er
			}
			clean, _ := archiveName(h.Name)
			entries = append(entries, entry{name: clean, size: h.Size, mode: mode, dir: h.Typeflag == tar.TypeDir})
			if er = ensureRootSpace(stage, h.Size); er != nil {
				return nil, er
			}
			if er = stageArchiveEntry(ctx, stage, clean, h.Typeflag == tar.TypeDir, h.Size, mode, tr); er != nil {
				return nil, er
			}
		}
	} else {
		return nil, errors.New("只支持 ZIP 或 TAR.GZ")
	}
	// Zip metadata has all been validated before extracting any entry.
	if strings.HasSuffix(lower, ".zip") {
		if err = ensureRootSpace(stage, total); err != nil {
			return nil, err
		}
	}
	for _, item := range entries {
		if item.open == nil {
			continue
		}
		in, er := item.open()
		if er != nil {
			return nil, er
		}
		er = stageArchiveEntry(ctx, stage, item.name, item.dir, item.size, item.mode, in)
		in.Close()
		if er != nil {
			return nil, er
		}
	}
	// Publish top-level members. A conflict never merges silently into an existing tree.
	children, err := stage.Open(".")
	if err != nil {
		return nil, err
	}
	items, err := children.ReadDir(-1)
	children.Close()
	if err != nil {
		return nil, err
	}
	published := []string{}
	for _, item := range items {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		dst, skip, er := e.destination(filepath.Join(target, item.Name()), p.String("conflict"))
		if er != nil {
			return nil, er
		}
		if skip {
			continue
		}
		f, er := e.filePath(dst, false)
		if er != nil {
			return nil, er
		}
		er = e.publishFileEntry(stage, item.Name(), f, p.String("conflict"))
		f.close()
		if er != nil {
			return nil, er
		}
		published = append(published, dst)
	}
	log(fmt.Sprintf("解压完成：%d 项", len(entries)))
	return model.Config{"items": published, "count": len(entries)}, nil
}
func stageArchiveEntry(ctx context.Context, root *os.Root, name string, dir bool, size int64, mode os.FileMode, in io.Reader) error {
	if dir {
		return root.MkdirAll(name, 0755)
	}
	if err := root.MkdirAll(filepath.Dir(name), 0755); err != nil {
		return err
	}
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(&contextReader{ctx: ctx, r: in}, size+1))
	if err != nil {
		return err
	}
	if n != size {
		return errors.New("解压大小与声明不一致")
	}
	return f.Chmod(mode.Perm())
}
