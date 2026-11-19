package logic

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"opsview/internal/dao"
	"opsview/internal/model"
	"opsview/internal/runtimeconfig"
	"opsview/internal/security"
	"opsview/internal/version"
	"os"
	"path/filepath"
	"time"
)

func (a *App) BackupPanel(ctx context.Context) (model.Config, error) {
	folder := a.PanelBackupDirectory()
	if a.PanelDir == "" {
		return nil, errors.New("未设置面板数据目录")
	}
	if a.Executor != nil && !a.Development && folder != filepath.Join(a.PanelDir, "backups") {
		if _, err := a.Executor.Execute(ctx, model.Operation{Name: "schedule.validate-directory", Params: model.Config{"path": filepath.Dir(folder), "panelStorage": true}}, func(string) {}); err != nil {
			return nil, err
		}
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
	defer archive.Close()
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
	if a.Executor != nil && !a.Development {
		snapshot, er := a.Executor.Execute(ctx, model.Operation{Name: "archive.panel-configs"}, func(string) {})
		if er != nil {
			return nil, er
		}
		raw, er := json.Marshal(snapshot)
		if er != nil {
			return nil, er
		}
		if er = security.WriteAtomic(filepath.Join(stage, "managed-configs.json"), raw, 0600); er != nil {
			return nil, er
		}
		if er = add("managed-configs.json", filepath.Join(stage, "managed-configs.json")); er != nil {
			return nil, er
		}
	}
	err = add("opsview.db", stage+"/opsview.db")
	if err == nil {
		if _, statErr := os.Stat(filepath.Join(a.PanelDir, "panel.pem")); statErr == nil {
			err = add("panel.pem", filepath.Join(a.PanelDir, "panel.pem"))
		}
	}
	if err == nil {
		configPath := a.PanelConfigPath
		if configPath == "" {
			configPath = runtimeconfig.Path
		}
		if _, statErr := os.Stat(configPath); statErr == nil {
			err = add("panel.json", configPath)
		} else if !os.IsNotExist(statErr) {
			err = statErr
		}
	}
	if err == nil {
		sums := map[string]string{}
		for _, name := range []string{"opsview.db", "managed-configs.json", "panel.pem", "panel.json"} {
			path := filepath.Join(stage, name)
			if name == "panel.pem" {
				path = filepath.Join(a.PanelDir, name)
			}
			if name == "panel.json" {
				path = a.PanelConfigPath
				if path == "" {
					path = runtimeconfig.Path
				}
			}
			data, er := os.ReadFile(path)
			if os.IsNotExist(er) {
				continue
			}
			if er != nil {
				err = er
				break
			}
			sum := sha256.Sum256(data)
			sums[name] = hex.EncodeToString(sum[:])
		}
		if err == nil {
			raw, er := json.Marshal(model.Config{"formatVersion": 2, "opsviewVersion": version.Current, "schemaVersion": 5, "sha256": sums, "keyRequirement": "original master.key; executor backup.key kept separately", "managedRestore": "review-only"})
			if er == nil {
				er = tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0600, Size: int64(len(raw)), Typeflag: tar.TypeReg})
			}
			if er == nil {
				_, er = tw.Write(raw)
			}
			err = er
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
	registeredSuccess := false
	defer func() {
		if !registeredSuccess {
			_ = os.Rename(filepath.Join(folder, name), filepath.Join(folder, name)+".failed")
		}
	}()
	var preference model.Config
	_ = a.Store.Setting("panel", &preference)
	var remoteHistory map[string]model.Config
	_ = a.Store.Setting("panel-s3-history", &remoteHistory)
	if remoteHistory == nil {
		remoteHistory = map[string]model.Config{}
	}
	if id := preference.String("backupStorageId"); id != "" {
		storage, er := a.BackupStorage(ctx, id)
		if er != nil {
			return nil, er
		}
		transfer, ok := a.Executor.(interface {
			UploadPanelBackup(context.Context, string, string, io.Reader) error
		})
		if !ok {
			return nil, errors.New("S3 流式传输不可用")
		}
		file, er := os.Open(filepath.Join(folder, name))
		if er != nil {
			return nil, er
		}
		er = transfer.UploadPanelBackup(ctx, storage, name, file)
		file.Close()
		if er != nil {
			_ = os.Rename(filepath.Join(folder, name), filepath.Join(folder, name)+".failed")
			return nil, er
		}
		path := filepath.Join(folder, name)
		info, er := os.Stat(path)
		if er != nil {
			return nil, er
		}
		digestFile, er := os.Open(path)
		if er != nil {
			return nil, er
		}
		sum := sha256.New()
		_, er = io.Copy(sum, digestFile)
		digestFile.Close()
		if er != nil {
			return nil, er
		}
		remoteHistory[name] = model.Config{"name": name, "size": info.Size(), "sha256": hex.EncodeToString(sum.Sum(nil)), "storageId": storage, "localDirectory": folder, "createdAt": info.ModTime().UTC().Format(time.RFC3339)}
		if er = a.Store.Set("panel-s3-history", remoteHistory); er != nil {
			return nil, er
		}
	}
	// Register storage before retention can remove an earlier successful backup.
	var registered []string
	_ = a.Store.Setting("panel-backup-directories", &registered)
	registered = append(registered, folder)
	if err = a.Store.Set("panel-backup-directories", registered); err != nil {
		return nil, err
	}
	registeredSuccess = true
	files, _ := filepath.Glob(folder + "/*.ovp")
	retention := 7
	var prefs model.Config
	if a.Store.Setting("panel", &prefs) == nil && prefs.Int("backupRetention") >= 1 && prefs.Int("backupRetention") <= 30 {
		retention = prefs.Int("backupRetention")
	}
	for len(files) > retention {
		old := filepath.Base(files[0])
		if remoteHistory[old] != nil {
			if _, er := a.Executor.Execute(ctx, model.Operation{Name: "archive.panel-s3-delete", Params: model.Config{"storageId": remoteHistory[old]["storageId"], "file": old}}, func(string) {}); er != nil {
				return nil, er
			}
			delete(remoteHistory, old)
			if er := a.Store.Set("panel-s3-history", remoteHistory); er != nil {
				return nil, er
			}
		}
		if err = os.Remove(files[0]); err != nil {
			return nil, err
		}
		files = files[1:]
	}
	var dirs []string
	_ = a.Store.Setting("panel-backup-directories", &dirs)
	found := false
	for _, d := range dirs {
		if d == folder {
			found = true
		}
	}
	if !found {
		dirs = append(dirs, folder)
		if err = a.Store.Set("panel-backup-directories", dirs); err != nil {
			return nil, err
		}
	}
	return model.Config{"metadataBackup": name, "path": filepath.Join(folder, name), "downloadPath": "/api/v1/settings/backup/" + name}, nil
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
		if (header.Name != "opsview.db" && header.Name != "panel.pem" && header.Name != "panel.json" && header.Name != "manifest.json" && header.Name != "managed-configs.json") || header.Typeflag != tar.TypeReg || header.Size > 1<<30 {
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
	if data, er := os.ReadFile(filepath.Join(stage, "manifest.json")); er == nil {
		var m struct {
			Format int               `json:"formatVersion"`
			Schema int               `json:"schemaVersion"`
			Hashes map[string]string `json:"sha256"`
		}
		if json.Unmarshal(data, &m) != nil || m.Format != 2 || m.Schema > 5 || len(m.Hashes) == 0 {
			return errors.New("备份清单版本不支持")
		}
		for name, want := range m.Hashes {
			if filepath.Base(name) != name {
				return errors.New("清单路径无效")
			}
			data, er := os.ReadFile(filepath.Join(stage, name))
			if er != nil {
				return er
			}
			sum := sha256.Sum256(data)
			if hex.EncodeToString(sum[:]) != want {
				return errors.New("备份清单摘要不匹配")
			}
		}
	}
	_ = os.Remove(archive)
	if err = security.WriteAtomic(filepath.Join(stage, "master.key"), key, 0600); err != nil {
		return err
	}
	if _, e := os.Stat(filepath.Join(stage, "panel.json")); e == nil {
		if _, e = runtimeconfig.Load(filepath.Join(stage, "panel.json")); e != nil {
			return e
		}
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
	if err == nil {
		_, err = store.DB.Exec(`DELETE FROM security_tokens;DELETE FROM settings WHERE key='preference-pending' OR key LIKE 'alert-state:%';UPDATE tasks SET state='interrupted',error='面板恢复，未完成任务不自动重放' WHERE state IN ('running','queued')`)
	}
	if err == nil {
		rows, e := store.Resources("schedule")
		err = e
		for _, row := range rows {
			if err != nil {
				break
			}
			row.Config["enabled"] = false
			delete(row.Config, "rootApproval")
			err = store.Update(&row)
		}
	}
	if err == nil {
		p := model.Config{}
		rev, e := store.SettingRevision("panel", &p)
		if e != nil {
			err = e
		} else {
			_, _, err = store.PatchSetting("panel", rev, model.Config{"apiEnabled": false}, "restore.invalidate-credentials", "local")
		}
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

func (a *App) PanelBackupDirectory() string {
	p := model.Config{}
	_ = a.Store.Setting("panel", &p)
	if dir := p.String("backupDirectory"); dir != "" {
		return filepath.Join(dir, "opsview-panel")
	}
	if a.Root != "" && !a.Development {
		return "/www/backup/opsview-panel"
	}
	return filepath.Join(a.PanelDir, "backups")
}
func (a *App) PanelBackupDirectories() []string {
	dirs := []string{filepath.Join(a.PanelDir, "backups"), a.PanelBackupDirectory()}
	var history []string
	_ = a.Store.Setting("panel-backup-directories", &history)
	dirs = append(dirs, history...)
	seen := map[string]bool{}
	out := []string{}
	for _, dir := range dirs {
		if !seen[dir] {
			seen[dir] = true
			out = append(out, dir)
		}
	}
	return out
}
func (a *App) PanelBackupFile(name string) string {
	for _, dir := range a.PanelBackupDirectories() {
		path := filepath.Join(dir, name)
		if st, err := os.Lstat(path); err == nil && st.Mode().IsRegular() {
			return path
		}
	}
	return filepath.Join(a.PanelBackupDirectory(), name)
}

func (a *App) PanelArchive(ctx context.Context, p model.Config, verb string) (model.Config, error) {
	name := p.String("file")
	if filepath.Base(name) != name || filepath.Ext(name) != ".ovp" {
		return nil, errors.New("备份文件名无效")
	}
	path := a.PanelBackupFile(name)
	if verb == "delete" {
		var remote map[string]model.Config
		_ = a.Store.Setting("panel-s3-history", &remote)
		if m := remote[name]; m != nil {
			if _, err := a.Executor.Execute(ctx, model.Operation{Name: "archive.panel-s3-delete", Params: model.Config{"storageId": m["storageId"], "file": name}}, func(string) {}); err != nil {
				return nil, err
			}
			delete(remote, name)
			if err := a.Store.Set("panel-s3-history", remote); err != nil {
				return nil, err
			}
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		return model.Config{"deleted": true}, nil
	}
	path, err := a.EnsurePanelBackup(ctx, name)
	if err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(a.PanelDir, "verify-*")
	if err != nil {
		return nil, err
	}
	os.Remove(stage)
	defer os.RemoveAll(stage)
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = RestorePanel(a.PanelDir, path, stage); err != nil {
		return nil, err
	}
	return model.Config{"verified": true, "restoreMode": "new-directory", "requiresKey": "原 master.key；业务归档另需 backup.key", "command": "ov restore --file " + path, "versionCheck": "passed", "schedules": "恢复后全部暂停", "tokens": "API 和临时授权失效"}, nil
}

// Download from a versioned registered S3 location only when the local copy is absent.
func (a *App) EnsurePanelBackup(ctx context.Context, name string) (string, error) {
	if filepath.Base(name) != name || filepath.Ext(name) != ".ovp" {
		return "", errors.New("备份名无效")
	}
	path := a.PanelBackupFile(name)
	if st, err := os.Lstat(path); err == nil && st.Mode().IsRegular() {
		return path, nil
	}
	var history map[string]model.Config
	_ = a.Store.Setting("panel-s3-history", &history)
	m := history[name]
	if m == nil {
		return "", os.ErrNotExist
	}
	transfer, ok := a.Executor.(interface {
		FetchPanelBackup(context.Context, string, string, io.Writer) error
	})
	if !ok {
		return "", errors.New("S3 流式传输不可用")
	}
	dir := filepath.Join(a.PanelDir, "backups")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	out, err := os.CreateTemp(dir, "s3-restore-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(out.Name())
	sum := sha256.New()
	count := &countingBackupWriter{w: io.MultiWriter(out, sum)}
	err = transfer.FetchPanelBackup(ctx, m.String("storageId"), name, count)
	err = errors.Join(err, out.Close())
	if err != nil {
		return "", err
	}
	if count.n != int64(m.Int("size")) || hex.EncodeToString(sum.Sum(nil)) != m.String("sha256") {
		return "", errors.New("S3 面板备份摘要不匹配")
	}
	path = filepath.Join(dir, name)
	if err = os.Link(out.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}

type countingBackupWriter struct {
	w io.Writer
	n int64
}

func (c *countingBackupWriter) Write(p []byte) (int, error) {
	n, e := c.w.Write(p)
	c.n += int64(n)
	return n, e
}
