package executor

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (e *Engine) directoryBackup(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	c := op.Resource.Config
	handle, err := e.filePath(c.String("path"), true)
	if err != nil {
		return nil, err
	}
	defer handle.close()
	if e.criticalFilePath(handle.path) {
		return nil, errors.New("不能备份系统关键目录或包含面板私密数据的父目录")
	}
	root, err := handle.root.OpenRoot(handle.name)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	base, err := e.backupBase(op.Resource)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(base, 0700); err != nil {
		return nil, err
	}
	if pathContains(handle.path, base) {
		return nil, errors.New("备份存储目录不能位于被备份目录内")
	}
	plain, err := os.CreateTemp(base, "snapshot-*.tar.gz")
	if err != nil {
		return nil, err
	}
	defer os.Remove(plain.Name())
	excludes := strings.Fields(c.String("excludes"))
	estimated, err := preflightArchive(ctx, root, excludes, func(name string) bool { return e.checkFilePath(filepath.Join(handle.path, name), false) == nil })
	if err != nil {
		plain.Close()
		return nil, err
	}
	if err = ensureFileSpace(plain, 3*estimated); err != nil {
		plain.Close()
		return nil, err
	}
	gz := gzip.NewWriter(plain)
	tw := tar.NewWriter(gz)
	var total int64
	count := 0
	err = fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		if archiveExcluded(path, excludes) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if err := e.checkFilePath(filepath.Join(handle.path, path), false); err != nil {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() && !info.IsDir() {
			return nil
		}
		if info.Mode().IsRegular() {
			if total+info.Size() > fileArchiveLimit {
				return errors.New("目录备份超过 8 GiB 限制")
			}
			if err = ensureFileSpace(plain, 2*info.Size()); err != nil {
				return err
			}
		}
		count++
		if count > 1000000 {
			return errors.New("归档条目超过上限")
		}
		h, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		h.Name = path
		if err = tw.WriteHeader(h); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			f, err := root.Open(path)
			if err != nil {
				return err
			}
			n, err := io.CopyN(tw, &contextReader{ctx: ctx, r: f}, info.Size())
			f.Close()
			total += n
			return err
		}
		return nil
	})
	err = errors.Join(err, tw.Close(), gz.Close(), plain.Close())
	if err != nil {
		return nil, err
	}
	vault, err := security.OpenVault(filepath.Join(e.Root, "backup.key"))
	if err != nil {
		return nil, err
	}
	name := time.Now().UTC().Format("20060102T150405.000000000") + ".ovb"
	path := filepath.Join(base, name)
	if err = encryptBackup(vault, plain.Name(), path); err != nil {
		return nil, err
	}

	digest, err := hashFile(path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	m := model.Config{"formatVersion": 2, "file": name, "sha256": digest, "size": st.Size(), "createdAt": time.Now().UTC().Format(time.RFC3339), "targetId": op.Resource.ID, "targetKind": "directory", "sourcePath": handle.path, "consistency": "online-file-copy", "uncompressedSize": total}
	b, _ := json.Marshal(m)
	if err = security.WriteAtomic(path+".json", b, 0600); err != nil {
		return nil, err
	}
	if id := c.String("storageId"); id != "" {
		storage, er := e.load("backup-storage-" + id)
		if er != nil || storage.Kind != "backup-storage" {
			return nil, errors.New("S3 存储未登记")
		}
		if er = uploadS3(ctx, storage.Config, path, op.Resource.ID+"/"+name); er != nil {
			return nil, errors.New("S3 备份上传失败，之前的成功备份保留")
		}
		if er = uploadS3(ctx, storage.Config, path+".json", op.Resource.ID+"/"+name+".json"); er != nil {
			return nil, errors.New("S3 清单上传失败")
		}
		m["remoteStorageId"] = storage.Config.String("immutableId")
	}
	record, loadErr := e.load(op.Resource.ID)
	if loadErr != nil {
		record = model.Resource{ID: op.Resource.ID, Kind: "directory-backup", Name: op.Resource.Name, Config: model.Config{"storageDirectory": c["storageDirectory"]}}
	}
	var history []model.Config
	raw, _ := json.Marshal(record.Config["history"])
	_ = json.Unmarshal(raw, &history)
	history = append(history, m)
	keep := c.Int("retention")
	if keep < 1 {
		keep = 7
	}
	// Persist the newly successful backup before pruning any older success.
	record.Config["history"] = history
	if err = e.save(record); err != nil {
		return nil, err
	}
	for len(history) > keep {
		cfg, remote, er := e.archiveStorage(record, history[0])
		if er != nil {
			return nil, er
		}
		if remote {
			key := record.ID + "/" + history[0].String("file")
			if er = deleteS3(ctx, cfg, key); er != nil {
				return nil, er
			}
			if er = deleteS3(ctx, cfg, key+".json"); er != nil {
				return nil, er
			}
		}
		old := filepath.Join(base, history[0].String("file"))
		if err = os.Remove(old); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		_ = os.Remove(old + ".json")
		history = history[1:]
	}
	record.Config["history"] = history
	if err = e.save(record); err != nil {
		return nil, err
	}
	log("目录加密备份完成；在线文件副本不保证应用事务一致性")
	return m, nil
}
func (e *Engine) archiveOperation(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if op.Name == "archive.panel-s3-delete" {
		return e.deletePanelS3(ctx, op.Params)
	}
	if op.Name == "archive.storage-register" {
		return e.registerBackupStorage(op.Resource)
	}
	if op.Name == "archive.panel-configs" {
		return e.panelConfigurationSnapshot(ctx)
	}
	if op.Name == "archive.list" {
		out := []model.Config{}
		for _, record := range e.states() {
			if record.Kind != "backup" && record.Kind != "directory-backup" {
				continue
			}
			base, err := e.backupBase(record)
			if err != nil {
				continue
			}
			var history []model.Config
			raw, _ := json.Marshal(record.Config["history"])
			_ = json.Unmarshal(raw, &history)
			for _, m := range history {
				if _, err = os.Stat(filepath.Join(base, m.String("file"))); err != nil && m.String("remoteStorageId") == "" && record.Config.String("destination") != "s3" {
					continue
				}
				m["backupId"] = record.ID
				m["storage"] = base
				out = append(out, m)
			}
		}
		return model.Config{"rows": out}, nil
	}
	id, name := op.Params.String("backupId"), op.Params.String("file")
	if !regexpArchiveID(id) || filepath.Base(name) != name || !strings.HasSuffix(name, ".ovb") {
		return nil, errors.New("归档标识无效")
	}
	record, err := e.load(id)
	if err != nil {
		return nil, err
	}
	base, err := e.backupBase(record)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(base, name)
	var history []model.Config
	raw, _ := json.Marshal(record.Config["history"])
	_ = json.Unmarshal(raw, &history)
	var m model.Config
	for _, entry := range history {
		if entry.String("file") == name {
			m = entry
			break
		}
	}
	if m == nil || len(m.String("sha256")) != 64 {
		return nil, errors.New("归档不在本机受信任历史中")
	}
	if op.Name != "archive.delete" {
		if err = e.ensureArchiveLocal(ctx, record, m, path); err != nil {
			return nil, err
		}
	}
	if op.Name == "archive.delete" {
		if record.Config.Bool("recycleOnly") {
			return nil, errors.New("回收备份只能通过回收站管理")
		}
		cfg, remote, er := e.archiveStorage(record, m)
		if er != nil {
			return nil, er
		}
		if remote {
			key := record.ID + "/" + name
			if er = deleteS3(ctx, cfg, key); er != nil {
				return nil, er
			}
			if er = deleteS3(ctx, cfg, key+".json"); er != nil {
				return nil, er
			}
		}
		tomb := path + ".deleting-" + security.ID()
		if err = os.Rename(path, tomb); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		filtered := []model.Config{}
		for _, entry := range history {
			if entry.String("file") != name {
				filtered = append(filtered, entry)
			}
		}
		record.Config["history"] = filtered
		if err = e.save(record); err != nil {
			_ = os.Rename(tomb, path)
			return nil, err
		}
		if err = os.Remove(tomb); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		_ = os.Remove(path + ".json")
		return model.Config{"deleted": true}, nil
	}
	digest, err := hashFile(path)
	if err != nil {
		return nil, err
	}
	if digest != m.String("sha256") {
		return nil, errors.New("备份摘要校验失败")
	}
	plainPath, err := e.decryptTrustedArchive(path)
	if err != nil {
		return nil, err
	}
	defer os.Remove(plainPath)
	if m.String("targetKind") == "directory" || m.String("targetKind") == "site" || m.String("targetKind") == "app" {
		if err = verifyArchiveTree(ctx, plainPath); err != nil {
			return nil, err
		}
	}
	m["verified"] = true
	if op.Name == "archive.verify" {
		return m, nil
	}
	if op.Name == "archive.preview" {
		m["requiresKey"] = "执行器 backup.key"
		m["restoreMode"] = "new-target"
		return m, nil
	}
	if op.Name == "archive.restore-directory" && m.String("targetKind") == "directory" {
		dest := op.Params.String("destination")
		if err = e.checkFilePath(dest, true); err != nil {
			return nil, err
		}
		if _, err = os.Lstat(dest); !os.IsNotExist(err) {
			return nil, errors.New("恢复目录必须不存在")
		}
		if err = e.restoreDirectoryArchive(ctx, plainPath, dest); err != nil {
			return nil, err
		}
		return model.Config{"restoredDirectory": dest}, nil
	}
	return nil, errors.New("该归档请通过原业务备份入口恢复至新目标")
}
func regexpArchiveID(s string) bool {
	if len(s) == 0 || len(s) > 100 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
