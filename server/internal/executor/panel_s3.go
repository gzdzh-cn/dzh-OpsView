package executor

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"io"
	"net/http"
	"net/url"
	"opsview/internal/logic"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
)

func (e *Engine) registerBackupStorage(r model.Resource) (model.Config, error) {
	if r.Kind != "backup" || r.Config.String("destination") != "s3" {
		return nil, errors.New("请选择已配置的 S3 备份存储")
	}
	if err := logic.Validate(r); err != nil {
		return nil, err
	}
	cfg := model.Config{}
	for _, k := range []string{"endpoint", "bucket", "region", "accessKey", "secretKey"} {
		cfg[k] = r.Config[k]
	}
	id := "backup-storage-" + r.ID
	raw, _ := json.Marshal(cfg)
	immutable := "backup-store-" + security.Hash(string(raw))[:32]
	cfg["immutableId"] = immutable
	if err := e.save(model.Resource{ID: immutable, Kind: "backup-storage", Name: r.Name, Config: cfg}); err != nil {
		return nil, err
	}
	return model.Config{"storageId": immutable}, e.save(model.Resource{ID: id, Kind: "backup-storage", Name: r.Name, Config: cfg})
}
func (e *Engine) handlePanelS3Upload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "PUT" && r.Method != "GET" {
		http.Error(w, "method", 405)
		return
	}
	id, name := r.URL.Query().Get("storageId"), r.URL.Query().Get("file")
	storage, err := e.load(id)
	if err != nil || storage.Kind != "backup-storage" || filepath.Base(name) != name || !strings.HasSuffix(name, ".ovp") {
		http.Error(w, "存储或文件标识无效", 400)
		return
	}
	if r.Method == "GET" {
		client, er := s3Client(r.Context(), storage.Config)
		if er != nil {
			http.Error(w, "S3 连接失败", 502)
			return
		}
		result, er := client.GetObject(r.Context(), &s3.GetObjectInput{Bucket: aws.String(storage.Config.String("bucket")), Key: aws.String("opsview-panel/" + name)})
		if er != nil {
			http.Error(w, "S3 读取失败", 502)
			return
		}
		defer result.Body.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		if _, er = io.Copy(w, io.LimitReader(&contextReader{ctx: r.Context(), r: result.Body}, (2<<30)+1)); er != nil {
			panic(http.ErrAbortHandler)
		}
		return
	}
	base := filepath.Join(e.Root, "backups", "panel-s3")
	if err = os.MkdirAll(base, 0700); err != nil {
		http.Error(w, "无法创建备份暂存目录", 500)
		return
	}
	temp, err := os.CreateTemp(base, "upload-*")
	if err != nil {
		http.Error(w, "无法暂存", 500)
		return
	}
	defer os.Remove(temp.Name())
	r.Body = http.MaxBytesReader(w, r.Body, 2<<30)
	_, err = io.Copy(temp, &contextReader{ctx: r.Context(), r: r.Body})
	err = errors.Join(err, temp.Close())
	if err != nil {
		http.Error(w, "备份传输中断或超过 2 GiB", 400)
		return
	}
	if err = uploadS3(r.Context(), storage.Config, temp.Name(), "opsview-panel/"+name); err != nil {
		http.Error(w, "S3 上传失败，本地备份及此前成功备份保留", 502)
		return
	}
	w.WriteHeader(201)
}
func (c *Client) UploadPanelBackup(ctx context.Context, id, name string, in io.Reader) error {
	req, err := http.NewRequestWithContext(ctx, "PUT", "http://executor/panel-s3?storageId="+url.QueryEscape(id)+"&file="+url.QueryEscape(name), in)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return errors.New("备份存储执行器不可用")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return errors.New(string(msg))
	}
	return nil
}

func (e *Engine) deletePanelS3(ctx context.Context, p model.Config) (model.Config, error) {
	storage, err := e.load(p.String("storageId"))
	name := p.String("file")
	if err != nil || storage.Kind != "backup-storage" || filepath.Base(name) != name || !strings.HasSuffix(name, ".ovp") {
		return nil, errors.New("备份存储标识无效")
	}
	if err = deleteS3(ctx, storage.Config, "opsview-panel/"+name); err != nil {
		return nil, errors.New("S3 删除失败，本地副本保留")
	}
	return model.Config{"deleted": true}, nil
}

func (c *Client) FetchPanelBackup(ctx context.Context, id, name string, out io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://executor/panel-s3?storageId="+url.QueryEscape(id)+"&file="+url.QueryEscape(name), nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return errors.New("S3 执行器不可用")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("S3 备份读取失败")
	}
	n, err := io.Copy(out, io.LimitReader(resp.Body, (2<<30)+1))
	if n > 2<<30 {
		return errors.New("S3 备份超过 2 GiB")
	}
	return err
}
