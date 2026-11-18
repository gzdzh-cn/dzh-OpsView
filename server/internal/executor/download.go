package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"opsview/internal/logic"
	"opsview/internal/model"
	"os"
	"path/filepath"
	"strings"
)

func (e *Engine) handleDownload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var op model.Operation
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&op) != nil {
		http.Error(w, "invalid download", 400)
		return
	}
	if !logic.Name.MatchString(op.Resource.ID) {
		http.Error(w, "invalid resource", 400)
		return
	}
	stored, err := e.load(op.Resource.ID)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	if op.Name == "redisdata.download" && stored.Kind == "instance" && stored.Config.String("software") == "redis" {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="redis-value.redis-dump"`)
		transfer := &downloadWriter{ResponseWriter: w}
		if err = e.redisDownload(r.Context(), stored, op.Params, transfer); err != nil {
			if transfer.started {
				panic(http.ErrAbortHandler)
			}
			w.Header().Del("Content-Length")
			http.Error(w, "Redis 下载失败", 422)
		}
		return
	}
	var path, name string
	if op.Name == "backup.download" && stored.Kind == "backup" {
		name = op.Params.String("artifact")
		if name == "" {
			name = stored.Config.String("artifact")
		}
		if filepath.Base(name) != name || !strings.HasSuffix(name, ".ovb") {
			http.Error(w, "invalid artifact", 400)
			return
		}
		path = filepath.Join(e.Root, "backups", stored.ID, name)
	} else if op.Name == "database.export" && stored.Kind == "database" {
		inst, err := e.load(stored.Config.String("instanceId"))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		dir, err := os.MkdirTemp(filepath.Join(e.Root, "downloads"), "export-*")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer os.RemoveAll(dir)
		path = filepath.Join(dir, "database.dump")
		if err = e.dumpDatabase(r.Context(), inst, stored, path, func(string) {}); err != nil {
			http.Error(w, "数据库导出失败，请检查实例状态及客户端版本", 500)
			return
		}
		name = stored.Name + ".sql"
		if inst.Config.String("software") == "postgresql" {
			name = stored.Name + ".tar.gz"
		}
	} else {
		http.Error(w, "unsupported download", 400)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() {
		http.Error(w, "invalid file", 400)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	http.ServeContent(w, r, name, stat.ModTime(), file)
}
func (c *Client) Download(ctx context.Context, op model.Operation, w http.ResponseWriter) error {
	b, err := json.Marshal(op)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "http://executor/download", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return errors.New("本机执行器不可用")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		message, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return fmt.Errorf("导出失败：%s", strings.TrimSpace(string(message)))
	}
	for _, h := range []string{"Content-Type", "Content-Length", "Content-Disposition", "Last-Modified"} {
		if value := res.Header.Get(h); value != "" {
			w.Header().Set(h, value)
		}
	}
	copied, err := io.Copy(w, res.Body)
	if err != nil && copied > 0 {
		panic(http.ErrAbortHandler)
	}
	if err != nil {
		w.Header().Del("Content-Length")
	}
	return err
}

type downloadWriter struct {
	http.ResponseWriter
	started bool
}

func (w *downloadWriter) Write(p []byte) (int, error) {
	w.started = true
	return w.ResponseWriter.Write(p)
}
