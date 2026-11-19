package executor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
)

func (e *Engine) handleArchiveUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "PUT" {
		http.Error(w, "method", 405)
		return
	}
	id, name := r.URL.Query().Get("backupId"), r.URL.Query().Get("file")
	if !regexpArchiveID(id) || filepath.Base(name) != name || !strings.HasSuffix(name, ".ovb") {
		http.Error(w, "归档标识无效", 400)
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	base, baseErr := e.registeredBackupBase(id)
	if baseErr != nil {
		http.Error(w, "受信任备份不存在", 409)
		return
	}
	if _, err := os.Lstat(filepath.Join(base, name)); !os.IsNotExist(err) {
		http.Error(w, "同名备份已存在", 409)
		return
	}
	stored, err := e.load(id)
	if err != nil {
		http.Error(w, "先恢复原备份元数据，缺少受信任备份历史", 409)
		return
	}
	var history []model.Config
	raw, _ := json.Marshal(stored.Config["history"])
	_ = json.Unmarshal(raw, &history)
	digest := ""
	for _, m := range history {
		if m.String("file") == name {
			digest = m.String("sha256")
		}
	}
	if digest == "" {
		http.Error(w, "归档不在本机受信任历史中", 409)
		return
	}
	if err = os.MkdirAll(base, 0700); err != nil {
		http.Error(w, "无法创建目录", 500)
		return
	}
	temp, err := os.CreateTemp(base, "upload-*")
	if err != nil {
		http.Error(w, "无法暂存", 500)
		return
	}
	defer os.Remove(temp.Name())
	_ = temp.Chmod(0600)
	r.Body = http.MaxBytesReader(w, r.Body, 2<<30)
	_, err = io.Copy(temp, r.Body)
	temp.Close()
	if err != nil {
		http.Error(w, "上传中断或超出大小限制", 400)
		return
	}
	got, err := hashFile(temp.Name())
	if err != nil || got != digest {
		http.Error(w, "备份摘要与本机历史不匹配", 400)
		return
	}
	if err = os.Link(temp.Name(), filepath.Join(base, name)); err != nil {
		http.Error(w, "发布冲突", 409)
		return
	}
	for _, m := range history {
		if m.String("file") == name {
			b, _ := json.Marshal(m)
			if err = security.WriteAtomic(filepath.Join(base, name)+".json", b, 0600); err != nil {
				http.Error(w, "清单写入失败", 500)
				return
			}
		}
	}
	w.WriteHeader(201)
}
func (c *Client) UploadArchive(ctx context.Context, id, name string, reader io.Reader) error {
	req, err := http.NewRequestWithContext(ctx, "PUT", "http://executor/archive-upload?backupId="+url.QueryEscape(id)+"&file="+url.QueryEscape(name), reader)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return errors.New("上传执行器不可用")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return errors.New(strings.TrimSpace(string(b)))
	}
	return nil
}
