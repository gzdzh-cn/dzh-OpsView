package executor

import (
	"context"
	"errors"
	"fmt"
	"github.com/shirou/gopsutil/v4/disk"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func (e *Engine) moveMySQLStorage(ctx context.Context, r model.Resource, p model.Config, log func(string)) (model.Config, error) {
	if r.Config.String("software") != "mysql" || r.Config.String("mode") != "native" {
		return nil, errors.New("仅支持原生 MySQL")
	}
	if !p.Bool("maintenanceConfirmed") {
		return nil, errors.New("请先停止关联应用写入并确认维护窗口")
	}
	base := filepath.Join(e.Root, "instances", r.ID)
	path, err := e.softwarePath(r, "main")
	if err != nil {
		return nil, err
	}
	old, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if p.String("baseRevision") != security.Hash(string(old)) {
		return nil, errors.New("配置已变化，请刷新")
	}
	oldDir := r.Config.String("dataDirectory")
	if oldDir == "" {
		oldDir = base + "/data"
	}
	rel, err := filepath.Rel(base, oldDir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil, errors.New("原数据目录不在受管范围")
	}
	var size uint64
	err = filepath.WalkDir(oldDir, func(path string, d os.DirEntry, er error) error {
		if er != nil {
			return er
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("数据目录含符号链接，需先人工核对")
		}
		if !d.IsDir() {
			st, er := d.Info()
			if er != nil {
				return er
			}
			if !st.Mode().IsRegular() && st.Mode()&os.ModeSocket == 0 {
				return errors.New("数据目录包含特殊文件")
			}
			size += uint64(st.Size())
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	usage, err := disk.Usage(base)
	if err != nil {
		return nil, err
	}
	if usage.Free < size*2+1<<30 {
		return nil, errors.New("可用磁盘不足以同时保留旧目录、迁移副本和备份")
	}
	log("1/5 生成所有数据库的逻辑备份")
	backupDir := filepath.Join(e.Root, "backups", "instance-"+r.ID)
	if err = os.MkdirAll(backupDir, 0700); err != nil {
		return nil, err
	}
	plain, err := os.CreateTemp(backupDir, ".migration-*.sql")
	if err != nil {
		return nil, err
	}
	defer os.Remove(plain.Name())
	cmd := exec.CommandContext(ctx, base+"/runtime/bin/mysqldump", "--protocol=TCP", "-h127.0.0.1", fmt.Sprintf("-P%d", r.Config.Int("port")), "-uroot", "--all-databases", "--single-transaction", "--routines", "--events")
	cmd.Env = append(os.Environ(), "MYSQL_PWD="+r.Config.String("password"))
	cmd.Stdout = plain
	isolateProcess(cmd)
	if err = cmd.Run(); err != nil {
		plain.Close()
		return nil, errors.New("迁移前备份失败")
	}
	plain.Close()
	backupPath := filepath.Join(backupDir, security.ID()+".ovb")
	if err = security.EncryptFile(e.Vault, plain.Name(), backupPath); err != nil {
		return nil, err
	}
	log("2/5 停止实例")
	if err = systemctl(ctx, log, "stop", serviceName(r)); err != nil {
		return nil, err
	}
	stopped := true
	defer func() {
		if stopped {
			_ = systemctl(context.Background(), func(string) {}, "start", serviceName(r))
		}
	}()
	nextDir := filepath.Join(base, "data-"+security.ID()[:12])
	log("3/5 复制数据并保留原权限")
	if err = run(ctx, log, "", nil, nil, "cp", "-a", "--", oldDir, nextDir); err != nil {
		return nil, err
	}
	// A socket left by an unclean shutdown is never promoted into the new directory.
	_ = os.Remove(filepath.Join(nextDir, "mysql.sock"))
	_ = os.Remove(filepath.Join(nextDir, "mysql.sock.lock"))
	next := strings.Replace(string(old), "datadir="+oldDir, "datadir="+nextDir, 1)
	if next == string(old) {
		return nil, errors.New("未找到受管的数据目录指令")
	}
	log("4/5 切换配置并启动")
	if err = writeOwnedAtomic(path, []byte(next), 0644); err != nil {
		return nil, err
	}
	rollback := func(cause error) (model.Config, error) {
		_ = writeOwnedAtomic(path, old, 0644)
		_ = systemctl(context.Background(), func(string) {}, "restart", serviceName(r))
		return nil, fmt.Errorf("迁移失败，已恢复旧目录，副本保留供核对：%w", cause)
	}
	if err = systemctl(ctx, log, "start", serviceName(r)); err != nil {
		return rollback(err)
	}
	if err = e.softwareHealth(ctx, r); err != nil {
		return rollback(err)
	}
	log("5/5 验证 MySQL 可查询")
	if err = e.sql(ctx, r, "", strings.NewReader("SELECT 1;"), func(string) {}); err != nil {
		return rollback(err)
	}
	r.Config["dataDirectory"] = nextDir
	r.Config["previousDataDirectory"] = oldDir
	r.Config["storageBackup"] = backupPath
	if err = e.save(r); err != nil {
		return rollback(err)
	}
	stopped = false
	return model.Config{"dataDirectory": nextDir, "previousDataDirectory": oldDir, "storageBackup": backupPath}, nil
}
