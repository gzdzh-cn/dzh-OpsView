package executor

import (
	"context"
	"errors"
	"fmt"
	"github.com/shirou/gopsutil/v4/disk"
	"io"
	"opsview/internal/model"
	"opsview/internal/security"
	"opsview/internal/software"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// --rdb obtains one complete replication snapshot, rather than copying a changing data file.
// The plaintext exists only in the root-only backup staging directory and is removed by backup().
func (e *Engine) dumpRedis(ctx context.Context, r model.Resource, path string, log func(string)) error {
	if r.Kind != "instance" || r.Config.String("software") != "redis" || r.Config.String("mode") != "native" {
		return errors.New("仅支持受管原生 Redis 快照备份")
	}
	info, err := e.redisCommand(ctx, r, "INFO", "memory")
	if err != nil {
		return err
	}
	memory, err := strconv.ParseUint(parsePairs(info, ":")["used_memory"], 10, 64)
	if err != nil {
		return errors.New("无法估算 Redis 备份空间")
	}
	usage, err := disk.Usage(filepath.Dir(path))
	if err != nil {
		return err
	}
	if memory > usage.Free/2 || usage.Free-memory*2 < 64<<20 {
		return errors.New("可用磁盘不足以保存并加密 Redis 快照")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	log("1/3 获取 Redis 一致性 RDB 快照，原实例继续运行")
	base := filepath.Join(e.Root, "instances", r.ID)
	cmd := exec.CommandContext(ctx, base+"/runtime/bin/redis-cli", "-h", "127.0.0.1", "-p", strconv.Itoa(r.Config.Int("port")), "--rdb", path)
	cmd.Env = append(os.Environ(), "REDISCLI_AUTH="+r.Config.String("password"))
	isolateProcess(cmd)
	if err = cmd.Run(); err != nil {
		return errors.New("Redis 快照传输失败，未生成可用备份")
	}
	log("2/3 使用当前版本检查 RDB 格式与校验和")
	if _, err = output(ctx, base+"/runtime/bin/redis-check-rdb", path); err != nil {
		return errors.New("RDB 完整性校验失败")
	}
	log("3/3 加密快照并登记备份摘要")
	return nil
}

func redisRestoreConfiguration(old, data string) (string, error) {
	seen := map[string]bool{}
	for _, line := range strings.Split(old, "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) == 0 || strings.HasPrefix(f[0], "#") {
			continue
		}
		switch f[0] {
		case "include", "loadmodule":
			return "", errors.New("恢复目标包含自定义 include 或模块，请使用新建的受管实例")
		case "dir", "dbfilename", "appendfilename", "appenddirname", "appendonly":
			if seen[f[0]] || len(f) != 2 {
				return "", errors.New("恢复目标的持久化路径或参数有歧义")
			}
			seen[f[0]] = true
			want := map[string]string{"dir": data, "dbfilename": "dump.rdb", "appendfilename": "appendonly.aof", "appenddirname": "appendonlydir"}[f[0]]
			if want != "" && strings.Trim(f[1], `"`) != want {
				return "", errors.New("恢复目标的持久化路径不属于标准受管目录")
			}
		}
	}
	if !seen["dir"] {
		return "", errors.New("恢复目标缺少受管数据目录")
	}
	return software.Patch("redis", "", "main", old, map[string]string{"appendonly": "no"})
}

func (e *Engine) restoreRedis(ctx context.Context, op model.Operation, source model.Resource, plain string, log func(string)) (model.Config, error) {
	if !op.Params.Bool("maintenanceConfirmed") {
		return nil, errors.New("请确认目标实例处于维护窗口且没有业务写入")
	}
	target, err := e.load(op.Params.String("targetInstanceId"))
	if err != nil || target.ID == source.ID || target.Kind != "instance" || target.Config.String("software") != "redis" || target.Config.String("mode") != "native" || target.Config.Bool("installationIncomplete") {
		return nil, errors.New("请选择源实例之外已安装的原生 Redis 空实例")
	}
	// A newly provisioned target must not already serve registered applications or Sessions.
	for _, r := range e.states() {
		if r.ID != op.Resource.ID && r.ID != target.ID && (r.Config.String("instanceId") == target.ID || r.Config.String("targetId") == target.ID || r.Config.String("sessionRedisInstanceId") == target.ID) {
			return nil, errors.New("目标实例已有关联资源，请新建独立 Redis 实例")
		}
	}
	info, err := e.redisCommand(ctx, target, "INFO", "keyspace")
	if err != nil {
		return nil, err
	}
	if len(parsePairs(info, ":")) != 0 {
		return nil, errors.New("恢复目标包含数据，禁止覆盖；请新建空实例")
	}
	base := filepath.Join(e.Root, "instances", target.ID)
	path, err := e.softwarePath(target, "main")
	if err != nil {
		return nil, err
	}
	old, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	data := base + "/data"
	dataInfo, err := os.Lstat(data)
	if err != nil || !dataInfo.IsDir() || dataInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("恢复目标数据目录不是受管的普通目录")
	}
	next, err := redisRestoreConfiguration(string(old), data)
	if err != nil {
		return nil, err
	}
	if err = regularManagedLimit(data+"/dump.rdb", data, 0); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	log("1/5 使用目标版本验证快照格式，源实例保持运行")
	if _, err = output(ctx, base+"/runtime/bin/redis-check-rdb", plain); err != nil {
		return nil, errors.New("目标 Redis 版本不支持此快照或快照损坏")
	}
	st, err := os.Stat(plain)
	if err != nil {
		return nil, err
	}
	usage, err := disk.Usage(base)
	if err != nil {
		return nil, err
	}
	if uint64(st.Size()) > usage.Free/2 || usage.Free-uint64(st.Size())*2 < 64<<20 {
		return nil, errors.New("恢复空间不足")
	}
	log("2/5 停止空目标实例，保留目标原持久化目录")
	if err = systemctl(ctx, log, "stop", serviceName(target)); err != nil {
		return nil, err
	}
	previous := base + "/data-before-restore-" + security.ID()
	if err = os.Rename(data, previous); err != nil {
		_ = systemctl(ctx, func(string) {}, "start", serviceName(target))
		return nil, err
	}
	rollback := func(cause error) (model.Config, error) {
		recovery, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		stopErr := systemctl(recovery, func(string) {}, "stop", serviceName(target))
		if stopErr != nil {
			return nil, fmt.Errorf("恢复失败且目标服务未能停止，旧目录保留在 %s：%w", previous, cause)
		}
		failed := base + "/data-failed-restore-" + security.ID()
		moveErr := os.Rename(data, failed)
		if os.IsNotExist(moveErr) {
			moveErr = nil
		}
		if moveErr != nil {
			return nil, fmt.Errorf("恢复失败，目录回退需人工处理：%w", cause)
		}
		dirErr := os.Rename(previous, data)
		fileErr := writeOwnedAtomic(path, old, 0640)
		startErr := systemctl(recovery, func(string) {}, "start", serviceName(target))
		if dirErr != nil || fileErr != nil || startErr != nil {
			return nil, fmt.Errorf("恢复失败，目标回退未完成，请检查旧目录 %s：%w", previous, cause)
		}
		return nil, fmt.Errorf("恢复失败，已恢复目标原目录与配置：%w", cause)
	}
	log("3/5 将快照放入新数据目录，以 RDB 模式启动验证")
	if err = os.Mkdir(data, 0700); err != nil {
		return rollback(err)
	}
	in, err := os.Open(plain)
	if err != nil {
		return rollback(err)
	}
	defer in.Close()
	if err = copyRedisSnapshot(in, data+"/dump.rdb"); err != nil {
		return rollback(err)
	}
	user := "ov_" + target.ID[:min(12, len(target.ID))]
	if err = run(ctx, func(string) {}, "", nil, nil, "chown", "-R", user+":"+user, data); err != nil {
		return rollback(err)
	}
	if err = writeOwnedAtomic(path, []byte(next), 0640); err != nil {
		return rollback(err)
	}
	if err = systemctl(ctx, log, "start", serviceName(target)); err != nil {
		return rollback(err)
	}
	if err = e.waitRedisReady(ctx, target); err != nil {
		return rollback(err)
	}
	log("4/5 按目标原策略恢复 AOF，并等待后台重写完成")
	if software.Values("redis", target.Config.String("version"), "main", string(old))["appendonly"] == "yes" {
		if _, err = e.applyRedisConfig(ctx, target, path, next, string(old), log); err != nil {
			return rollback(err)
		}
	} else if err = writeOwnedAtomic(path, old, 0640); err != nil {
		return rollback(err)
	}
	log("5/5 验证新实例可用，源实例及原持久化文件均保留")
	if err = e.waitRedisReady(ctx, target); err != nil {
		return rollback(err)
	}
	target.Status = "ready"
	target.Config["restoreSourceId"] = source.ID
	target.Config["restoredAt"] = time.Now().UTC().Format(time.RFC3339)
	target.Config["previousDataDirectory"] = previous
	target.Config["configurationRevision"] = security.Hash(string(old))
	if err = e.save(target); err != nil {
		return rollback(err)
	}
	return model.Config{"restoredInstanceId": target.ID, "linkedResources": []model.Resource{target}}, nil
}

func copyRedisSnapshot(in io.Reader, path string) error {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	return out.Close()
}

func (e *Engine) waitRedisReady(ctx context.Context, r model.Resource) error {
	deadline, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	for {
		if reply, err := e.redisCommand(deadline, r, "PING"); err == nil && strings.TrimSpace(reply) == "PONG" {
			return nil
		}
		select {
		case <-deadline.Done():
			return errors.New("Redis 未能完成加载并响应 PING")
		case <-time.After(time.Second):
		}
	}
}
