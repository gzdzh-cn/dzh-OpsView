package executor

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type trashEntry struct {
	ID        string    `json:"id"`
	Original  string    `json:"original"`
	Stored    string    `json:"stored"`
	DeletedAt time.Time `json:"deletedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	Account   string    `json:"account,omitempty"`
	UID       int       `json:"uid,omitempty"`
	GID       int       `json:"gid,omitempty"`
}

func (e *Engine) trashIndex() (map[string]trashEntry, error) {
	v := map[string]trashEntry{}
	err := e.readFileMeta("trash", &v)
	return v, err
}
func (e *Engine) trashDirectory(path string) (string, error) {
	dir := filepath.Dir(path)
	st, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	_, _, device := fileOwner(st)
	for dir != "/" {
		parent := filepath.Dir(dir)
		st, err = os.Stat(parent)
		if err != nil {
			return "", err
		}
		_, _, dev := fileOwner(st)
		if dev != device {
			break
		}
		dir = parent
	}
	if managed, er := os.Stat(e.Root); er == nil {
		_, _, managedDevice := fileOwner(managed)
		if managedDevice == device {
			dir = e.Root
		}
	}
	target := filepath.Join(dir, ".opsview-trash")
	if err = os.Mkdir(target, 0700); err != nil && !os.IsExist(err) {
		return "", errors.New("当前文件系统无法建立回收站，删除已取消")
	}
	st, err = os.Lstat(target)
	if err != nil {
		return "", err
	}
	uid, _, _ := fileOwner(st)
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm() != 0700 || uid != os.Geteuid() {
		return "", errors.New("回收站目录不安全，删除已取消")
	}
	return target, nil
}
func (e *Engine) trashFile(path string) (trashEntry, error) {
	var item trashEntry
	f, err := e.filePath(path, false)
	if err != nil {
		return item, err
	}
	defer f.close()
	if e.criticalFilePath(f.path) {
		return item, errors.New("禁止删除、移动或整体改权系统关键目录")
	}
	if hasNestedMount(f.path) {
		return item, errors.New("路径包含挂载点，请先单独处理挂载目录")
	}
	st, err := f.root.Lstat(f.name)
	if err != nil {
		return item, err
	}
	dir, err := e.trashDirectory(f.path)
	if err != nil {
		return item, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return item, err
	}
	defer root.Close()
	id := security.ID()
	now := time.Now().UTC()
	item = trashEntry{ID: id, Original: f.path, Stored: filepath.Join(dir, id), DeletedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour)}
	item.UID, item.GID, _ = fileOwner(st)
	item.Account, err = e.accountForPath(f.path)
	if err != nil {
		return item, err
	}
	index, err := e.trashIndex()
	if err != nil {
		return item, err
	}
	index[id] = item
	if err = e.writeFileMeta("trash", index); err != nil {
		return item, err
	}
	if err = renameFileEntry(f.root, f.name, root, id, true); err != nil {
		delete(index, id)
		_ = e.writeFileMeta("trash", index)
		return item, fmt.Errorf("移动至回收站失败，原文件保留：%w", err)
	}
	return item, nil
}
func (e *Engine) fileTrash(p model.Config) (model.Config, error) {
	index, err := e.trashIndex()
	if err != nil {
		return nil, err
	}
	if p.String("action") == "list" || p.String("action") == "" {
		items := []trashEntry{}
		for _, item := range index {
			items = append(items, item)
		}
		return model.Config{"items": items}, nil
	}
	paths := []string{}
	if p.String("action") == "empty" {
		if !p.Bool("confirmed") {
			return nil, errors.New("永久清空需要明确确认")
		}
		for id, item := range index {
			paths = append(paths, item.Original)
			if err = removeTrashEntry(item); err != nil {
				return nil, err
			}
			delete(index, id)
		}
	} else {
		item, ok := index[p.String("id")]
		if !ok {
			return nil, errors.New("回收站记录不存在")
		}
		paths = append(paths, item.Original)
		switch p.String("action") {
		case "purge":
			if !p.Bool("confirmed") {
				return nil, errors.New("永久删除需要明确确认")
			}
			if err = removeTrashEntry(item); err != nil {
				return nil, err
			}
		case "restore":
			target := item.Original
			if p.String("target") != "" {
				target = p.String("target")
			}
			if target != item.Original {
				paths = append(paths, target)
			}
			f, er := e.filePath(target, false)
			if er != nil {
				return nil, er
			}
			defer f.close()
			if e.criticalFilePath(f.path) {
				return nil, errors.New("不能恢复覆盖关键目录")
			}
			if _, er = f.root.Lstat(f.name); !os.IsNotExist(er) {
				return nil, errors.New("恢复位置已存在，请选择新的完整路径")
			}
			if item.Account != "" {
				if er = e.retainRestoredAccount(item.Account, target); er != nil {
					return nil, er
				}
			}
			source, er := os.OpenRoot(filepath.Dir(item.Stored))
			if er != nil {
				return nil, er
			}
			defer source.Close()
			if er = renameFileEntry(source, item.ID, f.root, f.name, true); er != nil {
				return nil, er
			}
		default:
			return nil, errors.New("回收站操作无效")
		}
		delete(index, item.ID)
	}
	if err = e.writeFileMeta("trash", index); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = e.cleanupSiteAccounts(ctx); err != nil {
		return model.Config{"paths": paths, "accountCleanupPending": true}, err
	}
	return model.Config{"paths": paths}, nil
}
func removeTrashEntry(item trashEntry) error {
	if filepath.Base(filepath.Dir(item.Stored)) != ".opsview-trash" || filepath.Base(item.Stored) != item.ID {
		return errors.New("回收站路径无效")
	}
	root, err := os.OpenRoot(filepath.Dir(item.Stored))
	if err != nil {
		return err
	}
	defer root.Close()
	return root.RemoveAll(item.ID)
}
func (e *Engine) expireFileTrash(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.fileMu.Lock()
			index, err := e.trashIndex()
			if err == nil {
				for id, item := range index {
					if time.Now().After(item.ExpiresAt) {
						if removeTrashEntry(item) == nil {
							delete(index, id)
						}
					}
				}
				_ = e.writeFileMeta("trash", index)
			}
			e.cleanupExpiredUploads()
			cleanup, cancel := context.WithTimeout(ctx, 30*time.Second)
			_ = e.cleanupSiteAccounts(cleanup)
			cancel()
			e.fileMu.Unlock()
		}
	}
}
func (e *Engine) destination(path, policy string) (string, bool, error) {
	f, err := e.filePath(path, false)
	if err != nil {
		return "", false, err
	}
	defer f.close()
	if e.criticalFilePath(f.path) {
		return "", false, errors.New("禁止覆盖系统关键目录")
	}
	if _, err = f.root.Lstat(f.name); os.IsNotExist(err) {
		return f.path, false, nil
	} else if err != nil {
		return "", false, err
	}
	switch policy {
	case "overwrite":
		return f.path, false, nil
	case "keep":
		ext := filepath.Ext(f.path)
		base := strings.TrimSuffix(f.path, ext)
		for i := 1; i < 10000; i++ {
			candidate := fmt.Sprintf("%s (%d)%s", base, i, ext)
			if _, er := os.Lstat(candidate); os.IsNotExist(er) {
				return candidate, false, nil
			}
		}
		return "", false, errors.New("无法分配新文件名")
	default:
		return f.path, true, nil
	}
}
func (e *Engine) copyFileTree(ctx context.Context, source, target string, log func(string)) error {
	source = filepath.Clean(source)
	target = filepath.Clean(target)
	if pathContains(source, target) {
		return errors.New("不能复制到源目录内部")
	}
	count := 0
	type directoryMetadata struct {
		path     string
		mode     os.FileMode
		uid, gid int
	}
	directories := []directoryMetadata{}
	err := e.walkFiles(ctx, source, func(path string, st os.FileInfo) error {
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(target, rel)
		f, err := e.checkedFilePath(dst, false, true)
		if err != nil {
			return err
		}
		defer f.close()
		if st.IsDir() {
			if err = f.root.Mkdir(f.name, 0700); err != nil {
				return err
			}
			uid, gid, _ := fileOwner(st)
			directories = append(directories, directoryMetadata{dst, st.Mode().Perm(), uid, gid})
			return nil
		}
		in, err := e.openFile(path, false)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := f.root.OpenFile(f.name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		defer out.Close()
		if err = ensureFileSpace(out, st.Size()); err != nil {
			return err
		}
		if _, err = io.Copy(out, &contextReader{ctx: ctx, r: in}); err != nil {
			return err
		}
		if _, err = in.Seek(0, 0); err != nil {
			return err
		}
		before, err := fileHash(in)
		if err != nil {
			return err
		}
		if _, err = out.Seek(0, 0); err != nil {
			return err
		}
		after, err := fileHash(out)
		if err != nil {
			return err
		}
		current, err := in.Stat()
		if err != nil {
			return err
		}
		if before != after || fileRevision(current) != fileRevision(st) {
			return errors.New("复制期间源文件发生变化，源文件保留")
		}
		uid, gid, _ := fileOwner(st)
		if err = out.Chown(uid, gid); err != nil {
			return err
		}
		if err = out.Chmod(st.Mode().Perm()); err != nil {
			return err
		}
		if err = out.Sync(); err != nil {
			return err
		}
		count++
		if count%100 == 0 {
			log(fmt.Sprintf("已复制 %d 个文件", count))
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := len(directories) - 1; i >= 0; i-- {
		d := directories[i]
		f, err := e.checkedFilePath(d.path, false, true)
		if err != nil {
			return err
		}
		dir, err := f.root.OpenFile(f.name, os.O_RDONLY|fileNoFollow(), 0)
		f.close()
		if err != nil {
			return err
		}
		err = dir.Chown(d.uid, d.gid)
		if err == nil {
			err = dir.Chmod(d.mode)
		}
		dir.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}
func (e *Engine) fileBatch(ctx context.Context, action string, p model.Config, log func(string)) (model.Config, error) {
	paths := stringPaths(p)
	if len(paths) == 0 || len(paths) > 1000 {
		return nil, errors.New("请选择 1–1000 项")
	}
	results := []model.Config{}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		source, err := e.filePath(path, false)
		if err != nil {
			return nil, err
		}
		actual := source.path
		st, err := source.root.Lstat(source.name)
		source.close()
		if err != nil {
			return nil, err
		}
		if (action == "move" || action == "rename" || action == "delete" || action == "permissions") && e.criticalFilePath(actual) {
			return nil, errors.New("禁止整体操作系统关键目录")
		}
		if hasNestedMount(actual) && (action == "move" || action == "rename" || action == "permissions") {
			return nil, errors.New("路径包含挂载点，操作已取消")
		}
		switch action {
		case "delete":
			item, er := e.trashFile(actual)
			if er != nil {
				return nil, er
			}
			results = append(results, model.Config{"path": actual, "trashId": item.ID})
		case "copy", "move", "rename":
			target := p.String("target")
			if action != "rename" {
				target = filepath.Join(target, filepath.Base(actual))
			} else if len(paths) != 1 {
				return nil, errors.New("重命名一次只能选择一项")
			}
			if st.Mode()&os.ModeSymlink != 0 {
				return nil, errors.New("链接请进入实际目标后操作")
			}
			if pathContains(actual, filepath.Clean(target)) {
				return nil, errors.New("目标不能位于源路径内")
			}
			target, skip, er := e.destination(target, p.String("conflict"))
			if er != nil {
				return nil, er
			}
			if skip {
				results = append(results, model.Config{"path": actual, "skipped": true})
				continue
			}
			dst, er := e.filePath(target, false)
			if er != nil {
				return nil, er
			}
			if action == "move" || action == "rename" {
				src, er := e.filePath(actual, false)
				if er != nil {
					dst.close()
					return nil, er
				}
				er = e.publishFileEntry(src.root, src.name, dst, p.String("conflict"))
				src.close()
				dst.close()
				if er == nil {
					results = append(results, model.Config{"path": actual, "target": target})
					continue
				}
				if !errors.Is(er, syscallEXDEV()) {
					return nil, er
				}
			} else {
				dst.close()
			}
			// Copy to a private sibling and publish only after all content is verified.
			tmp := filepath.Join(filepath.Dir(target), ".opsview-copy-"+security.ID())
			if er = e.copyFileTree(ctx, actual, tmp, log); er != nil {
				removeFileTemporary(tmp)
				return nil, er
			}
			if er = e.promoteFile(tmp, target, p.String("conflict")); er != nil {
				removeFileTemporary(tmp)
				return nil, er
			}
			if action == "move" {
				if _, er = e.trashFile(actual); er != nil {
					return nil, fmt.Errorf("复制已完成，源文件保留：%w", er)
				}
			}
			results = append(results, model.Config{"path": actual, "target": target})
		case "permissions":
			if p.Int("permissionMask") < 0 || p.Int("permissionMask") > 0777 || p.Int("permissionValue") < 0 || p.Int("permissionValue") > 0777 {
				return nil, errors.New("权限范围无效")
			}
			mode, er := strconv.ParseUint(p.String("mode"), 8, 32)
			if p.String("mode") != "" && (er != nil || mode > 0777) {
				return nil, errors.New("权限必须在 0000–0777，不允许特殊提权位")
			}
			uid, gid := -1, -1
			if p.String("owner") != "" {
				u, er := user.Lookup(p.String("owner"))
				if er != nil {
					return nil, errors.New("系统用户不存在")
				}
				uid, _ = strconv.Atoi(u.Uid)
			}
			if p.String("group") != "" {
				g, er := user.LookupGroup(p.String("group"))
				if er != nil {
					return nil, errors.New("系统用户组不存在")
				}
				gid, _ = strconv.Atoi(g.Gid)
			}
			apply := func(path string, st os.FileInfo) error {
				if st.Mode()&os.ModeSymlink != 0 {
					return nil
				}
				f, er := e.openFile(path, false)
				if er != nil {
					return er
				}
				defer f.Close()
				if expected, ok := p["revisions"].(map[string]any); ok {
					if rev, yes := expected[path].(string); yes && rev != "" {
						actual, er := f.Stat()
						if er != nil {
							return er
						}
						if fileRevision(actual) != rev {
							return &model.RevisionConflict{Revision: fileRevision(actual)}
						}
					}
				}
				if uid >= 0 || gid >= 0 {
					if er = f.Chown(uid, gid); er != nil {
						return er
					}
				}
				if p.Int("permissionMask") != 0 {
					mask, value := p.Int("permissionMask"), p.Int("permissionValue")
					if mask < 0 || mask > 0777 || value < 0 || value > 0777 {
						return errors.New("权限范围无效")
					}
					current, er := f.Stat()
					if er != nil {
						return er
					}
					return f.Chmod((current.Mode().Perm() &^ os.FileMode(mask)) | os.FileMode(value&mask))
				}
				if p.String("mode") != "" {
					return f.Chmod(os.FileMode(mode))
				}
				return nil
			}
			if p.Bool("recursive") {
				if !p.Bool("confirmed") {
					return nil, errors.New("递归权限修改需要确认影响范围")
				}
				if er = e.walkFiles(ctx, actual, apply); er != nil {
					return nil, er
				}
			} else if er = apply(actual, st); er != nil {
				return nil, er
			}
			results = append(results, model.Config{"path": actual})
		default:
			return nil, errors.New("文件批量操作无效")
		}
	}
	log(fmt.Sprintf("操作完成：%d 项", len(results)))
	return model.Config{"items": results}, nil
}
func removeFileTemporary(path string) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err == nil {
		defer root.Close()
		_ = root.RemoveAll(filepath.Base(path))
	}
}

func (e *Engine) serverFiles(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	e.fileMu.Lock()
	defer e.fileMu.Unlock()
	p := op.Params
	action := strings.TrimPrefix(op.Name, "files.")
	switch action {
	case "identities":
		return fileIdentities()
	case "list":
		return e.listFiles(ctx, p)
	case "read":
		return e.readFileText(p)
	case "write":
		return e.writeFileText(p)
	case "metadata":
		return e.fileMetadata(ctx, p)
	case "favorite":
		f, err := e.openFile(p.String("path"), true)
		if err != nil {
			return nil, err
		}
		f.Close()
		favorites := []string{}
		_ = e.readFileMeta("favorites", &favorites)
		out := []string{}
		for _, path := range favorites {
			if path != p.String("path") {
				out = append(out, path)
			}
		}
		if !p.Bool("remove") {
			out = append(out, p.String("path"))
		}
		return model.Config{}, e.writeFileMeta("favorites", out)
	case "note":
		notes := map[string]string{}
		_ = e.readFileMeta("notes", &notes)
		if len(p.String("note")) > 1000 {
			return nil, errors.New("备注超过 1000 字符")
		}
		if f, err := e.filePath(p.String("path"), false); err != nil {
			return nil, err
		} else {
			f.close()
		}
		notes[p.String("path")] = p.String("note")
		return model.Config{}, e.writeFileMeta("notes", notes)
	case "mkdir":
		f, err := e.filePath(p.String("path"), false)
		if err != nil {
			return nil, err
		}
		defer f.close()
		return model.Config{}, f.root.Mkdir(f.name, 0755)
	case "stat":
		f, err := e.filePath(p.String("path"), false)
		if err != nil {
			return nil, err
		}
		defer f.close()
		st, err := f.root.Lstat(f.name)
		if err != nil {
			return nil, err
		}
		visibility, err := e.fileVisibility(ctx)
		if err != nil {
			return nil, err
		}
		if !visibility.visible(f.path, st) {
			return nil, errors.New("此路径不开放文件管理")
		}
		return fileInfo(f.path, st), nil
	case "search":
		return e.fileSearch(ctx, p, log)
	case "size":
		var bytes int64
		count := 0
		err := e.walkFiles(ctx, p.String("path"), func(_ string, st os.FileInfo) error {
			if st.Mode().IsRegular() {
				bytes += st.Size()
			}
			count++
			return nil
		})
		return model.Config{"size": bytes, "count": count}, err
	case "trash":
		return e.fileTrash(p)
	case "copy", "move", "rename", "permissions", "delete":
		return e.fileBatch(ctx, action, p, log)
	case "compress", "extract":
		return e.fileArchive(ctx, action, p, log)
	case "upload-create", "upload-status", "upload-finish", "upload-cancel":
		return e.fileUploadOperation(p, action)
	}
	return nil, errors.New("不支持的服务器文件操作")
}

// Legacy resource-relative requests keep their confinement and original encoding.
func (e *Engine) files(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	if op.Params.String("scope") == "server" {
		return e.serverFiles(ctx, op, log)
	}
	r := op.Resource
	folder := ""
	switch r.Kind {
	case "site":
		var err error
		folder, err = e.trustedSiteRoot(r)
		if err != nil {
			return nil, err
		}
	case "app":
		folder = filepath.Join(e.Root, "apps", r.ID)
	case "instance":
		folder = filepath.Join(e.Root, "instances", r.ID, "imports")
	default:
		return nil, errors.New("请选择受管资源")
	}
	if !logicFileID(r.ID) {
		return nil, errors.New("资源标识无效")
	}
	if err := os.MkdirAll(folder, 0755); err != nil {
		return nil, err
	}
	rel := filepath.Clean(op.Params.String("path"))
	if _, err := safePath(folder, rel); err != nil {
		return nil, err
	}
	p := cloneConfig(op.Params)
	p["path"] = filepath.Join(folder, rel)
	if strings.HasPrefix(filepath.Base(rel), ".env") {
		return nil, errors.New("环境文件请在服务器文件管理查看")
	}
	if op.Name == "files.write" {
		b, err := base64.StdEncoding.DecodeString(p.String("content"))
		if err != nil {
			return nil, err
		}
		p["text"] = string(b)
		if old, err := e.openFile(p.String("path"), false); err == nil {
			st, _ := old.Stat()
			old.Close()
			p["revision"] = fileRevision(st)
		}
	}
	if op.Name == "files.chmod" {
		op.Name = "files.permissions"
	}
	if st, err := os.Lstat(p.String("path")); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("旧接口不支持链接")
	}
	op.Params = p
	data, err := e.serverFiles(ctx, op, log)
	if err == nil && op.Name == "files.read" {
		data["content"] = base64.StdEncoding.EncodeToString([]byte(data.String("text")))
		delete(data, "text")
	}
	return data, err
}
func logicFileID(id string) bool {
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return id != "" && id != "." && id != ".."
}
