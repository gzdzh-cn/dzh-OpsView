package logic

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"opsview/internal/dao"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"time"
)

func (a *App) BackupPanel(ctx context.Context) (model.Config, error) {
	folder := filepath.Join(a.PanelDir, "backups")
	if a.PanelDir == "" {
		return nil, errors.New("未设置面板数据目录")
	}
	if err := os.MkdirAll(folder, 0700); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(folder, "metadata-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	if err = a.Store.Snapshot(ctx, stage+"/opsview.db"); err != nil {
		return nil, err
	}
	archive, err := os.OpenFile(stage+"/panel.tar.gz", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	gz := gzip.NewWriter(archive)
	tw := tar.NewWriter(gz)
	add := func(name, path string) error {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		stat, err := file.Stat()
		if err != nil {
			return err
		}
		if err = tw.WriteHeader(&tar.Header{Name: name, Size: stat.Size(), Mode: 0600}); err != nil {
			return err
		}
		_, err = io.Copy(tw, file)
		return err
	}
	err = add("opsview.db", stage+"/opsview.db")
	if err == nil {
		if _, statErr := os.Stat(filepath.Join(a.PanelDir, "panel.pem")); statErr == nil {
			err = add("panel.pem", filepath.Join(a.PanelDir, "panel.pem"))
		}
	}
	if closeErr := tw.Close(); err == nil {
		err = closeErr
	}
	if closeErr := gz.Close(); err == nil {
		err = closeErr
	}
	if closeErr := archive.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	name := time.Now().UTC().Format("20060102T150405.000000000") + ".ovp"
	if err = security.EncryptFile(a.Store.Vault, stage+"/panel.tar.gz", filepath.Join(folder, name)); err != nil {
		return nil, err
	}
	files, _ := filepath.Glob(folder + "/*.ovp")
	for len(files) > 7 {
		if err = os.Remove(files[0]); err != nil {
			return nil, err
		}
		files = files[1:]
	}
	return model.Config{"metadataBackup": name, "downloadPath": "/api/v1/settings/backup/" + name}, nil
}

// RestorePanel always writes into a new directory. The caller switches services after validation.
func RestorePanel(data, artifact, destination string) error {
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return errors.New("恢复目标目录必须尚不存在")
	}
	key, err := os.ReadFile(filepath.Join(data, "master.key"))
	if err != nil {
		return err
	}
	vault, err := security.OpenVault(filepath.Join(data, "master.key"))
	if err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(destination), "panel-restore-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	archive := filepath.Join(stage, "data.tar.gz")
	if err = security.DecryptFile(vault, artifact, archive); err != nil {
		return err
	}
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	found := false
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if (header.Name != "opsview.db" && header.Name != "panel.pem") || header.Typeflag != tar.TypeReg || header.Size > 1<<30 {
			return errors.New("面板备份包含不允许的条目")
		}
		out, err := os.OpenFile(filepath.Join(stage, header.Name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, tr)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if header.Name == "opsview.db" {
			found = true
		}
	}
	if !found {
		return errors.New("面板备份缺少数据库")
	}
	_ = os.Remove(archive)
	if err = security.WriteAtomic(filepath.Join(stage, "master.key"), key, 0600); err != nil {
		return err
	}
	store, err := dao.Open(stage)
	if err != nil {
		return err
	}
	var integrity string
	err = store.DB.QueryRow("PRAGMA integrity_check").Scan(&integrity)
	if err == nil && integrity != "ok" {
		err = errors.New("面板数据库完整性校验失败")
	}
	if err == nil {
		_, err = store.DB.Exec("DELETE FROM sessions")
	}
	closeErr := store.DB.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(stage, destination)
}
