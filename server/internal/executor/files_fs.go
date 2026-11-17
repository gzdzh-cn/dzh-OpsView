package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const fileTextLimit = 10 << 20
const fileUploadLimit int64 = 2 << 30
const fileWalkLimit = 100000

// A checked directory handle pins the parent while the final entry is opened
// without following symlinks. Explicit symlink navigation is resolved first.
type managedFile struct {
	root       *os.Root
	name, path string
}

func (f *managedFile) close() { _ = f.root.Close() }
func pathContains(parent, child string) bool {
	return child == parent || strings.HasPrefix(child, parent+"/") || parent == "/"
}
func (e *Engine) filePrivatePaths() []string {
	paths := []string{filepath.Join(e.Root, "state"), filepath.Join(e.Root, "executor.key"), filepath.Join(e.Root, "backup.key"), filepath.Join(e.Root, "file-manager"), filepath.Join(filepath.Dir(e.Root), "panel"), "/var/lib/opsview/panel", "/run/opsview", "/var/run/docker.sock", "/etc/opsview/trust", "/etc/opsview/trust.d"}
	if e.socketPath != "" {
		paths = append(paths, e.socketPath)
	}
	if e.TrustDir != "" {
		paths = append(paths, e.TrustDir)
	}
	for i, path := range paths {
		if real, err := filepath.EvalSymlinks(path); err == nil {
			paths[i] = real
		} else if real, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
			paths[i] = filepath.Join(real, filepath.Base(path))
		}
	}
	return paths
}
func (e *Engine) checkFilePath(path string, write bool) error { return e.checkFilePolicy(path, false) }
func (e *Engine) checkFilePolicy(path string, internal bool) error {
	return checkFilePolicyPaths(path, internal, e.filePrivatePaths())
}
func checkFilePolicyPaths(path string, internal bool, private []string) error {
	if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") {
		return errors.New("文件路径必须是有效绝对路径")
	}
	for _, prefix := range []string{"/proc", "/sys", "/dev", "/run"} {
		if pathContains(prefix, path) {
			return errors.New("运行时及设备目录不开放文件管理")
		}
	}
	for _, component := range strings.Split(filepath.Clean(path), "/") {
		if component == ".opsview-trash" || (!internal && ((strings.HasPrefix(component, ".opsview-upload-") || strings.HasPrefix(component, ".opsview-write-")) || strings.HasPrefix(component, ".opsview-copy-") || strings.HasPrefix(component, ".opsview-extract-"))) {
			return errors.New("受保护的回收站或临时文件")
		}
	}
	for _, protected := range private {
		if pathContains(protected, path) {
			return errors.New("此路径包含面板密钥、认证数据或受保护元数据")
		}
	}
	return nil
}
func (e *Engine) criticalFilePath(path string) bool {
	for _, p := range []string{"/", "/boot", "/etc", "/home", "/root", "/usr", "/usr/bin", "/usr/sbin", "/usr/lib", "/bin", "/sbin", "/lib", "/lib64", "/var", "/var/lib", "/opt", "/srv", "/tmp", "/mnt", "/media", e.Root} {
		if path == p {
			return true
		}
	}
	for _, p := range e.filePrivatePaths() {
		if pathContains(path, p) {
			return true
		}
	}
	return false
}
func (e *Engine) filePath(path string, follow bool) (*managedFile, error) {
	return e.checkedFilePath(path, follow, false)
}
func (e *Engine) checkedFilePath(path string, follow, internal bool) (*managedFile, error) {
	path = filepath.Clean(path)
	if err := e.checkFilePolicy(path, internal); err != nil {
		return nil, err
	}
	if follow {
		if real, err := filepath.EvalSymlinks(path); err == nil {
			path = real
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	name := filepath.Base(path)
	if path == "/" {
		name = "."
	}
	parent := filepath.Dir(path)
	canonical, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return nil, err
	}
	path = filepath.Join(canonical, name)
	if err = e.checkFilePolicy(path, internal); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(canonical)
	if err != nil {
		return nil, err
	}
	// Verify the pinned parent itself, including a symlink replacement racing OpenRoot.
	d, err := root.Open(".")
	if err != nil {
		root.Close()
		return nil, err
	}
	actual, err := fileDescriptorPath(d, canonical)
	d.Close()
	if err != nil {
		root.Close()
		return nil, err
	}
	if err = e.checkFilePolicy(filepath.Join(actual, name), internal); err != nil {
		root.Close()
		return nil, err
	}
	return &managedFile{root: root, name: name, path: filepath.Join(actual, name)}, nil
}
func (e *Engine) openFile(path string, follow bool) (*os.File, error) {
	f, err := e.filePath(path, follow)
	if err != nil {
		return nil, err
	}
	defer f.close()
	file, err := f.root.OpenFile(f.name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	actual, err := fileDescriptorPath(file, f.path)
	if err != nil {
		file.Close()
		return nil, err
	}
	if err = e.checkFilePath(actual, false); err != nil {
		file.Close()
		return nil, err
	}
	st, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !st.Mode().IsRegular() && !st.IsDir() {
		file.Close()
		return nil, errors.New("只允许普通文件和目录")
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && st.Mode().IsRegular() && sys.Nlink > 1 {
		file.Close()
		return nil, errors.New("硬链接文件仅展示属性，请在服务器核对后操作")
	}
	if e.privateFileIdentity(st) {
		file.Close()
		return nil, errors.New("此文件指向面板受保护数据")
	}
	return file, nil
}

// Also protect aliases through bind mounts. Path checks alone do not identify
// an ordinary file mounted from inside a private metadata directory.
func (e *Engine) privateFileIdentity(st os.FileInfo) bool {
	for _, private := range e.filePrivatePaths() {
		found := false
		_ = filepath.WalkDir(private, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return fs.SkipDir
			}
			info, err := entry.Info()
			if err == nil && os.SameFile(st, info) {
				found = true
				return fs.SkipAll
			}
			return nil
		})
		if found {
			return true
		}
	}
	return false
}
func fileRevision(st os.FileInfo) string {
	uid, gid, _ := fileOwner(st)
	return fmt.Sprintf("%s-%d-%d-%o-%d-%d-%d", fileIdentity(st), st.Size(), st.ModTime().UnixNano(), st.Mode(), fileChangeStamp(st), uid, gid)
}
func fileOwner(st os.FileInfo) (int, int, uint64) {
	if v, ok := st.Sys().(*syscall.Stat_t); ok {
		return int(v.Uid), int(v.Gid), uint64(v.Dev)
	}
	return -1, -1, 0
}
func fileInfo(path string, st os.FileInfo) model.Config {
	uid, gid, _ := fileOwner(st)
	owner, group := strconv.Itoa(uid), strconv.Itoa(gid)
	if u, err := user.LookupId(owner); err == nil {
		owner = u.Username
	}
	if g, err := user.LookupGroupId(group); err == nil {
		group = g.Name
	}
	item := model.Config{"name": st.Name(), "path": path, "directory": st.IsDir(), "symlink": st.Mode()&os.ModeSymlink != 0, "size": st.Size(), "mode": fmt.Sprintf("%04o", st.Mode().Perm()), "owner": owner, "group": group, "uid": uid, "gid": gid, "modifiedAt": st.ModTime().UTC(), "revision": fileRevision(st), "special": !st.Mode().IsRegular() && !st.IsDir() && st.Mode()&os.ModeSymlink == 0}
	if st.Mode()&os.ModeSymlink != 0 {
		target, _ := os.Readlink(path)
		item["target"] = target
		if resolved, err := os.Stat(path); err == nil {
			item["targetDirectory"] = resolved.IsDir()
		}
	}
	return item
}
func (e *Engine) fileMetaDir() string { return filepath.Join(e.Root, "file-manager") }
func (e *Engine) readFileMeta(name string, target any) error {
	b, err := os.ReadFile(filepath.Join(e.fileMetaDir(), name+".json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, target)
}
func (e *Engine) writeFileMeta(name string, value any) error {
	if err := os.MkdirAll(e.fileMetaDir(), 0700); err != nil {
		return err
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return security.WriteAtomic(filepath.Join(e.fileMetaDir(), name+".json"), b, 0600)
}
func (e *Engine) listFiles(ctx context.Context, p model.Config) (model.Config, error) {
	path := p.String("path")
	if path == "" {
		path = "/"
	}
	dir, err := e.openFile(path, true)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	actual, err := fileDescriptorPath(dir, path)
	if err != nil {
		return nil, err
	}
	if err = e.checkFilePath(actual, false); err != nil {
		return nil, err
	}
	visibility, err := e.fileVisibility(ctx)
	if err != nil {
		return nil, err
	}
	entries := []model.Config{}
	notes := map[string]string{}
	_ = e.readFileMeta("notes", &notes)
	for {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		batch, er := dir.ReadDir(256)
		for _, entry := range batch {
			if !p.Bool("hidden") && strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			if filter := strings.ToLower(p.String("filter")); filter != "" && !strings.Contains(strings.ToLower(entry.Name()), filter) {
				continue
			}
			st, er := entry.Info()
			if er != nil {
				continue
			}
			full := filepath.Join(actual, entry.Name())
			if !visibility.visible(full, st) {
				continue
			}
			item := fileInfo(full, st)
			item["note"] = notes[full]
			item["protected"] = false
			item["browsable"] = st.IsDir()
			entries = append(entries, item)
		}
		if len(entries) > fileWalkLimit {
			return nil, errors.New("目录超过 100000 项，请按名称筛选后读取")
		}
		if er == io.EOF {
			break
		}
		if er != nil {
			return nil, er
		}
	}
	order := p.String("sort")
	desc := p.Bool("descending")
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Bool("directory") != b.Bool("directory") {
			return a.Bool("directory")
		}
		var less bool
		switch order {
		case "size":
			less = a["size"].(int64) < b["size"].(int64)
		case "modifiedAt":
			less = a["modifiedAt"].(time.Time).Before(b["modifiedAt"].(time.Time))
		default:
			less = strings.ToLower(a.String("name")) < strings.ToLower(b.String("name"))
		}
		if desc {
			return !less && fmt.Sprint(a[order]) != fmt.Sprint(b[order])
		}
		return less
	})
	total := len(entries)
	page := p.Int("page")
	if page < 1 {
		page = 1
	}
	limit := p.Int("pageSize")
	if limit < 1 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	start := min((page-1)*limit, total)
	end := min(start+limit, total)
	return model.Config{"items": entries[start:end], "total": total, "path": actual, "root": "/", "page": page, "pageSize": limit, "readOnly": false}, nil
}
func (e *Engine) readFileText(p model.Config) (model.Config, error) {
	f, err := e.openFile(p.String("path"), true)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > fileTextLimit {
		return nil, errors.New("文本编辑仅支持不超过 10 MiB 的普通文件，请使用下载")
	}
	b, err := io.ReadAll(io.LimitReader(f, fileTextLimit+1))
	if err != nil {
		return nil, err
	}
	if len(b) > fileTextLimit || !utf8.Valid(b) || strings.IndexByte(string(b), 0) >= 0 {
		return nil, errors.New("不是可编辑的 UTF-8 文本，请下载查看")
	}
	return model.Config{"text": string(b), "revision": fileRevision(st), "size": st.Size()}, nil
}
func (e *Engine) writeFileText(p model.Config) (model.Config, error) {
	text := p.String("text")
	if len(text) > fileTextLimit || !utf8.ValidString(text) {
		return nil, errors.New("文本无效或超过 10 MiB")
	}
	target, err := e.filePath(p.String("path"), true)
	if err != nil {
		return nil, err
	}
	defer target.close()
	mode := os.FileMode(0644)
	uid, gid := -1, -1
	if old, er := e.openFile(target.path, false); er == nil {
		st, er := old.Stat()
		old.Close()
		if er != nil {
			return nil, er
		}
		if !st.Mode().IsRegular() {
			return nil, errors.New("只能编辑普通文件")
		}
		if p.String("revision") != fileRevision(st) {
			return nil, &model.RevisionConflict{Revision: fileRevision(st)}
		}
		mode = st.Mode().Perm()
		uid, gid, _ = fileOwner(st)
	} else if !os.IsNotExist(er) {
		return nil, er
	} else if p.String("revision") != "" {
		return nil, &model.RevisionConflict{Revision: "deleted"}
	}
	tmp := ".opsview-write-" + security.ID()
	out, err := target.root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer target.root.Remove(tmp)
	defer out.Close()
	if err = ensureFileSpace(out, int64(len(text))); err != nil {
		return nil, err
	}
	if err = out.Chown(uid, gid); err != nil {
		return nil, err
	}
	if err = out.Chmod(mode); err != nil {
		return nil, err
	}
	if _, err = io.WriteString(out, text); err != nil {
		return nil, err
	}
	if err = out.Sync(); err != nil {
		return nil, err
	}
	if err = out.Close(); err != nil {
		return nil, err
	}
	// Recheck both revision and entry type immediately before promotion.
	if st, er := target.root.Lstat(target.name); er == nil {
		if st.Mode()&os.ModeSymlink != 0 || p.String("revision") != fileRevision(st) {
			return nil, &model.RevisionConflict{Revision: fileRevision(st)}
		}
	} else if !os.IsNotExist(er) {
		return nil, er
	} else if p.String("revision") != "" {
		return nil, &model.RevisionConflict{Revision: "deleted"}
	}
	if p.String("revision") != "" {
		if err = e.retainTextRevision(target.path, p.String("revision")); err != nil {
			return nil, err
		}
	}
	if err = target.root.Rename(tmp, target.name); err != nil {
		return nil, err
	}
	st, err := target.root.Stat(target.name)
	if err != nil {
		return nil, err
	}
	return model.Config{"revision": fileRevision(st)}, nil
}
func stringPaths(p model.Config) []string {
	var paths []string
	switch v := p["paths"].(type) {
	case []string:
		paths = v
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				paths = append(paths, s)
			}
		}
	}
	if len(paths) == 0 && p.String("path") != "" {
		paths = []string{p.String("path")}
	}
	return paths
}

// Walk each directory via a pinned descriptor. Never follow links or descend
// into a different device. Every path, including children, is policy checked.
func (e *Engine) walkFiles(ctx context.Context, path string, visit func(string, os.FileInfo) error) error {
	visibility, err := e.fileVisibility(ctx)
	if err != nil {
		return err
	}
	start, err := e.filePath(path, false)
	if err != nil {
		return err
	}
	st, err := start.root.Lstat(start.name)
	start.close()
	if err != nil {
		return err
	}
	_, _, device := fileOwner(st)
	count := 0
	var walk func(string) error
	walk = func(path string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		count++
		if count > fileWalkLimit {
			return errors.New("递归操作超过 100000 项")
		}
		f, err := e.filePath(path, false)
		if err != nil {
			return err
		}
		defer f.close()
		st, err := f.root.Lstat(f.name)
		if err != nil {
			return err
		}
		if !visibility.visible(f.path, st) {
			return nil
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if !st.Mode().IsRegular() && !st.IsDir() {
			return nil
		}
		_, _, dev := fileOwner(st)
		if dev != device {
			return fs.SkipDir
		}
		if err = visit(f.path, st); err != nil {
			return err
		}
		if !st.IsDir() {
			return nil
		}
		dir, err := e.openFile(f.path, false)
		if err != nil {
			return err
		}
		defer dir.Close()
		for {
			batch, er := dir.ReadDir(128)
			for _, item := range batch {
				child := filepath.Join(f.path, item.Name())
				if e.checkFilePath(child, false) != nil {
					continue
				}
				if err = walk(child); err != nil && !errors.Is(err, fs.SkipDir) {
					return err
				}
			}
			if er == io.EOF {
				break
			}
			if er != nil {
				return er
			}
		}
		return nil
	}
	return walk(path)
}
func (e *Engine) fileSearch(ctx context.Context, p model.Config, log func(string)) (model.Config, error) {
	path := p.String("path")
	query := strings.ToLower(p.String("query"))
	if query == "" {
		return nil, errors.New("请输入搜索内容")
	}
	results := []model.Config{}
	var bytes int64
	err := e.walkFiles(ctx, path, func(path string, st os.FileInfo) error {
		match := strings.Contains(strings.ToLower(st.Name()), query)
		if p.Bool("content") {
			match = false
			if st.Mode().IsRegular() && st.Size() <= fileTextLimit {
				f, er := e.openFile(path, false)
				if er != nil {
					return nil
				}
				b, er := io.ReadAll(io.LimitReader(f, fileTextLimit+1))
				f.Close()
				bytes += int64(len(b))
				if bytes > 256<<20 {
					return errors.New("内容搜索已达到 256 MiB 扫描上限，请缩小目录")
				}
				match = er == nil && utf8.Valid(b) && strings.Contains(strings.ToLower(string(b)), query)
			}
		}
		if match {
			results = append(results, fileInfo(path, st))
			if len(results) > 1000 {
				return errors.New("搜索超过 1000 条，请缩小范围")
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	log(fmt.Sprintf("搜索完成：%d 项", len(results)))
	return model.Config{"items": results, "total": len(results)}, nil
}
func fileHash(f *os.File) (string, error) {
	if _, err := f.Seek(0, 0); err != nil {
		return "", err
	}
	h := sha256.New()
	_, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), err
}

// Used by database access configuration inside its already validated os.Root.
func writeRoot(root *os.Root, name string, data []byte, mode os.FileMode) error {
	tmp := ".opsview-" + security.ID()
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return root.Rename(tmp, name)
}
