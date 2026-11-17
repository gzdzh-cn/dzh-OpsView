package executor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const fileChunkLimit = 8 << 20

type uploadSession struct {
	ID        string    `json:"id"`
	Path      string    `json:"path"`
	Temporary string    `json:"temporary"`
	Size      int64     `json:"size"`
	Offset    int64     `json:"offset"`
	Revision  string    `json:"revision"`
	Conflict  string    `json:"conflict"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func (e *Engine) uploadSession(id string) (uploadSession, error) {
	var s uploadSession
	if !logicFileID(id) {
		return s, errors.New("上传标识无效")
	}
	if err := e.readFileMeta("upload-"+id, &s); err != nil {
		return s, err
	}
	if s.ID != id || time.Now().After(s.ExpiresAt) {
		return s, errors.New("上传会话已过期或不存在")
	}
	if filepath.Dir(s.Temporary) != filepath.Dir(s.Path) || filepath.Base(s.Temporary) != ".opsview-upload-"+id {
		return s, errors.New("上传会话路径无效")
	}
	return s, nil
}
func (e *Engine) fileUploadOperation(p model.Config, action string) (model.Config, error) {
	if action == "upload-create" {
		var size int64
		switch n := p["size"].(type) {
		case float64:
			size = int64(n)
		case int64:
			size = n
		case int:
			size = int64(n)
		}
		if size < 0 || size > fileUploadLimit {
			return nil, errors.New("上传文件不得超过 2 GiB")
		}
		f, err := e.filePath(p.String("path"), false)
		if err != nil {
			return nil, err
		}
		defer f.close()
		if e.criticalFilePath(f.path) {
			return nil, errors.New("禁止覆盖关键目录")
		}
		revision := ""
		if st, er := f.root.Lstat(f.name); er == nil {
			if !st.Mode().IsRegular() {
				return nil, errors.New("上传目标不是普通文件")
			}
			if p.String("conflict") != "overwrite" && p.String("conflict") != "keep" {
				return nil, errors.New("文件已存在，请选择覆盖或保留两份")
			}
			revision = fileRevision(st)
		} else if !os.IsNotExist(er) {
			return nil, er
		}
		if err = ensureRootSpace(f.root, size); err != nil {
			return nil, err
		}
		id := security.ID()
		tmp := ".opsview-upload-" + id
		file, err := f.root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, err
		}
		file.Close()
		s := uploadSession{ID: id, Path: f.path, Temporary: filepath.Join(filepath.Dir(f.path), tmp), Size: size, Revision: revision, Conflict: p.String("conflict"), ExpiresAt: time.Now().Add(24 * time.Hour)}
		if err = e.writeFileMeta("upload-"+id, s); err != nil {
			f.root.Remove(tmp)
			return nil, err
		}
		return model.Config{"id": id, "offset": 0, "chunkSize": fileChunkLimit, "size": size}, nil
	}
	s, err := e.uploadSession(p.String("id"))
	if err != nil {
		return nil, err
	}
	if action == "upload-status" {
		return model.Config{"id": s.ID, "offset": s.Offset, "size": s.Size, "path": s.Path}, nil
	}
	target, err := e.filePath(s.Path, false)
	if err != nil {
		return nil, err
	}
	defer target.close()
	if action == "upload-cancel" {
		if err = target.root.Remove(filepath.Base(s.Temporary)); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		return model.Config{"path": s.Path}, os.Remove(filepath.Join(e.fileMetaDir(), "upload-"+s.ID+".json"))
	}
	if action != "upload-finish" {
		return nil, errors.New("上传操作无效")
	}
	if s.Offset != s.Size {
		return nil, errors.New("上传尚未完成")
	}
	file, err := target.root.OpenFile(filepath.Base(s.Temporary), os.O_RDONLY|fileNoFollow(), 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	st, err := file.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() != s.Size {
		return nil, errors.New("上传文件大小不匹配")
	}
	digest, err := fileHash(file)
	if err != nil {
		return nil, err
	}
	if expected := p.String("sha256"); expected != "" && expected != digest {
		return nil, errors.New("上传摘要校验失败")
	}
	mode := os.FileMode(0644)
	uid, gid := -1, -1
	if old, er := e.openFile(s.Path, false); er == nil {
		st, er := old.Stat()
		old.Close()
		if er != nil {
			return nil, er
		}
		if fileRevision(st) != s.Revision {
			return nil, &model.RevisionConflict{Revision: fileRevision(st)}
		}
		mode = st.Mode().Perm()
		uid, gid, _ = fileOwner(st)
	} else if !os.IsNotExist(er) {
		return nil, er
	} else if s.Revision != "" {
		return nil, &model.RevisionConflict{Revision: "deleted"}
	}
	final, skip, err := e.destination(s.Path, s.Conflict)
	if err != nil {
		return nil, err
	}
	if skip {
		return nil, errors.New("目标文件已存在，上传临时文件保留")
	}
	// Preserve metadata on overwrite; keep-both creates an ordinary new file.
	if s.Conflict == "keep" {
		mode = 0644
		uid, gid = -1, -1
	}
	if err = file.Chown(uid, gid); err != nil {
		return nil, err
	}
	if err = file.Chmod(mode); err != nil {
		return nil, err
	}
	dst, err := e.filePath(final, false)
	if err != nil {
		return nil, err
	}
	defer dst.close()
	if err = e.publishFileEntry(target.root, filepath.Base(s.Temporary), dst, s.Conflict); err != nil {
		return nil, err
	}
	_ = os.Remove(filepath.Join(e.fileMetaDir(), "upload-"+s.ID+".json"))
	return model.Config{"path": final, "sha256": digest, "size": s.Size}, nil
}
func (e *Engine) handleFileTransfer(w http.ResponseWriter, r *http.Request) {
	e.fileMu.Lock()
	defer e.fileMu.Unlock()
	if r.Method == "GET" || r.Method == "HEAD" {
		f, err := e.openFile(r.URL.Query().Get("path"), true)
		if err != nil {
			fileHTTPError(w, err)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil || !st.Mode().IsRegular() {
			http.Error(w, "只允许下载普通文件", 400)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": st.Name()}))
		// Release the file-manager mutation lock before streaming; the open descriptor
		// pins the selected file even if a later rename occurs.
		e.fileMu.Unlock()
		http.ServeContent(w, r, st.Name(), st.ModTime(), f)
		e.fileMu.Lock()
		return
	}
	if r.Method != "PUT" {
		http.Error(w, "method", 405)
		return
	}
	s, err := e.uploadSession(r.URL.Query().Get("id"))
	if err != nil {
		fileHTTPError(w, err)
		return
	}
	offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || offset != s.Offset {
		http.Error(w, "上传偏移不匹配，请读取会话进度", 409)
		return
	}
	if r.ContentLength < 0 || r.ContentLength > fileChunkLimit || r.ContentLength > s.Size-s.Offset {
		http.Error(w, "分块大小无效", 400)
		return
	}
	target, err := e.filePath(s.Path, false)
	if err != nil {
		fileHTTPError(w, err)
		return
	}
	defer target.close()
	f, err := target.root.OpenFile(filepath.Base(s.Temporary), os.O_WRONLY|fileNoFollow(), 0)
	if err != nil {
		fileHTTPError(w, err)
		return
	}
	defer f.Close()
	if err = f.Truncate(s.Offset); err != nil {
		fileHTTPError(w, err)
		return
	}
	if _, err = f.Seek(s.Offset, 0); err != nil {
		fileHTTPError(w, err)
		return
	}
	n, err := io.Copy(f, io.LimitReader(r.Body, r.ContentLength+1))
	if err != nil || n != r.ContentLength {
		_ = f.Truncate(s.Offset)
		http.Error(w, "上传分块中断，可从已确认偏移重试", 400)
		return
	}
	if err = f.Sync(); err != nil {
		fileHTTPError(w, err)
		return
	}
	s.Offset += n
	s.ExpiresAt = time.Now().Add(24 * time.Hour)
	if err = e.writeFileMeta("upload-"+s.ID, s); err != nil {
		fileHTTPError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(model.Config{"offset": s.Offset})
}
func fileHTTPError(w http.ResponseWriter, err error) {
	code := 400
	if os.IsNotExist(err) {
		code = 404
	}
	http.Error(w, err.Error(), code)
}
func (c *Client) FileTransfer(ctx context.Context, original *http.Request, w http.ResponseWriter) error {
	req, err := http.NewRequestWithContext(ctx, original.Method, "http://executor/file-transfer?"+original.URL.RawQuery, original.Body)
	if err != nil {
		return err
	}
	req.ContentLength = original.ContentLength
	for _, name := range []string{"Range", "If-Range", "If-Modified-Since"} {
		if h := original.Header.Get(name); h != "" {
			req.Header.Set(name, h)
		}
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return errors.New("文件传输执行器连接失败")
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		message, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(res.StatusCode)
		_ = json.NewEncoder(w).Encode(model.Config{"error": strings.TrimSpace(string(message))})
		return nil
	}
	for _, name := range []string{"Content-Type", "Content-Length", "Content-Disposition", "Last-Modified", "Accept-Ranges", "Content-Range", "ETag"} {
		if v := res.Header.Get(name); v != "" {
			w.Header().Set(name, v)
		}
	}
	w.WriteHeader(res.StatusCode)
	// Headers are already committed. A transfer interruption must not append a
	// JSON error to the downloaded bytes; Content-Length exposes truncation.
	_, _ = io.Copy(w, res.Body)
	return nil
}

// Called by the hourly metadata sweeper. Only validated session-owned temporary
// names are removed; an expired upload never deletes its destination.
func (e *Engine) cleanupExpiredUploads() {
	entries, err := os.ReadDir(e.fileMetaDir())
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "upload-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		var s uploadSession
		if e.readFileMeta(strings.TrimSuffix(name, ".json"), &s) != nil || s.ID == "" || time.Now().Before(s.ExpiresAt) {
			continue
		}
		if !logicFileID(s.ID) || filepath.Base(s.Temporary) != ".opsview-upload-"+s.ID || filepath.Dir(s.Temporary) != filepath.Dir(s.Path) {
			continue
		}
		target, err := e.filePath(s.Path, false)
		if err != nil {
			continue
		}
		err = target.root.Remove(filepath.Base(s.Temporary))
		target.close()
		if err == nil || os.IsNotExist(err) {
			_ = os.Remove(filepath.Join(e.fileMetaDir(), name))
		}
	}
}
