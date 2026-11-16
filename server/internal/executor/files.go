package executor

import (
	"archive/zip"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (e *Engine) files(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	r := op.Resource
	folder := ""
	switch r.Kind {
	case "site":
		folder = filepath.Join(e.Root, "sites", r.ID, "www")
	case "app":
		folder = filepath.Join(e.Root, "apps", r.ID)
	case "instance":
		folder = filepath.Join(e.Root, "instances", r.ID, "imports")
	default:
		return nil, errors.New("文件管理只支持网站、应用和数据库导入目录")
	}
	if err := os.MkdirAll(folder, 0755); err != nil {
		return nil, err
	}
	rel := filepath.Clean(op.Params.String("path"))
	if _, err := safePath(folder, rel); err != nil {
		return nil, err
	}
	if filepath.Base(rel) == ".env" {
		return nil, errors.New("环境文件请通过应用配置管理")
	}
	root, err := os.OpenRoot(folder)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	switch op.Name {
	case "files.list":
		f, err := root.Open(rel)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		entries, err := f.ReadDir(-1)
		if err != nil {
			return nil, err
		}
		items := []model.Config{}
		for _, item := range entries {
			if strings.HasPrefix(item.Name(), ".env") {
				continue
			}
			info, err := item.Info()
			if err != nil {
				continue
			}
			items = append(items, model.Config{"name": item.Name(), "directory": item.IsDir(), "symlink": info.Mode()&os.ModeSymlink != 0, "size": info.Size(), "mode": fmt.Sprintf("%04o", info.Mode().Perm()), "modifiedAt": info.ModTime().UTC()})
		}
		return model.Config{"items": items, "root": folder}, nil
	case "files.read":
		f, err := root.Open(rel)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return nil, err
		}
		if !st.Mode().IsRegular() || st.Size() > 10<<20 {
			return nil, errors.New("只允许读取不超过 10 MiB 的普通文件")
		}
		b, err := io.ReadAll(io.LimitReader(f, 10<<20+1))
		if err != nil {
			return nil, err
		}
		if len(b) > 10<<20 {
			return nil, errors.New("文件超过读取限制")
		}
		return model.Config{"content": base64.StdEncoding.EncodeToString(b), "size": len(b)}, nil
	case "files.write":
		if rel == "." {
			return nil, errors.New("禁止写入根目录")
		}
		b, err := base64.StdEncoding.DecodeString(op.Params.String("content"))
		if err != nil || len(b) > 10<<20 {
			return nil, errors.New("文件编码无效或超过 10 MiB")
		}
		return model.Config{}, writeRoot(root, rel, b, 0644)
	case "files.mkdir":
		return model.Config{}, root.MkdirAll(rel, 0755)
	case "files.chmod":
		mode, err := strconv.ParseUint(op.Params.String("mode"), 8, 32)
		if err != nil || mode > 0775 {
			return nil, errors.New("权限只允许 0000–0775，禁止 SUID/SGID")
		}
		f, err := root.Open(rel)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return model.Config{}, f.Chmod(os.FileMode(mode))
	case "files.delete":
		if rel == "." {
			return nil, errors.New("禁止删除资源根目录")
		}
		return model.Config{}, root.RemoveAll(rel)
	case "files.extract":
		if !strings.HasSuffix(strings.ToLower(rel), ".zip") {
			return nil, errors.New("上传解压仅支持 ZIP")
		}
		f, err := root.Open(rel)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return nil, err
		}
		zr, err := zip.NewReader(f, st.Size())
		if err != nil {
			return nil, err
		}
		if len(zr.File) > 10000 {
			return nil, errors.New("归档文件数量超过限制")
		}
		var total uint64
		for _, item := range zr.File {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			if item.UncompressedSize64 > 512<<20 || total+item.UncompressedSize64 > 512<<20 || item.Mode()&os.ModeSymlink != 0 {
				return nil, errors.New("归档超过 512 MiB 或含符号链接")
			}
			total += item.UncompressedSize64
			target := filepath.Join(filepath.Dir(rel), item.Name)
			if filepath.IsAbs(item.Name) || strings.HasPrefix(filepath.Clean(item.Name), "../") || filepath.Clean(item.Name) == ".." {
				return nil, errors.New("归档路径越界")
			}
			if item.FileInfo().IsDir() {
				if err = root.MkdirAll(target, 0755); err != nil {
					return nil, err
				}
				continue
			}
			if err = root.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return nil, err
			}
			src, err := item.Open()
			if err != nil {
				return nil, err
			}
			dst, err := root.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
			if err != nil {
				src.Close()
				return nil, err
			}
			n, err := io.Copy(dst, io.LimitReader(src, int64(item.UncompressedSize64)+1))
			src.Close()
			ce := dst.Close()
			if err != nil {
				return nil, err
			}
			if ce != nil {
				return nil, ce
			}
			if uint64(n) != item.UncompressedSize64 {
				return nil, errors.New("ZIP 声明大小与实际内容不一致")
			}
		}
		log("ZIP 已解压到当前目录")
		return model.Config{}, nil
	}
	return nil, errors.New("文件操作无效")
}
func writeRoot(root *os.Root, name string, data []byte, mode os.FileMode) error {
	if err := root.MkdirAll(filepath.Dir(name), 0755); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(name), ".opsview-"+security.ID())
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err != nil {
		return err
	}
	if ce != nil {
		return ce
	}
	return root.Rename(tmp, name)
}
