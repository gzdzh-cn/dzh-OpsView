package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"opsview/internal/model"
	"opsview/internal/security"
	"opsview/internal/software"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Redis dynamic settings are applied through a scalar whitelist. Runtime values and the managed
// document commit together; CONFIG REWRITE is intentionally replaced by an owned atomic write.
func (e *Engine) applyRedisConfig(ctx context.Context, r model.Resource, path, old, next string, log func(string)) (model.Config, error) {
	before := software.Values("redis", r.Config.String("version"), "main", old)
	after := software.Values("redis", r.Config.String("version"), "main", next)
	keys := []string{}
	for key, value := range after {
		if before[key] != value {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	actual := map[string]string{}
	for _, key := range keys {
		response, err := e.redisCommand(ctx, r, "CONFIG", "GET", key)
		if err != nil {
			return nil, err
		}
		_, value, ok := strings.Cut(strings.TrimSuffix(response, "\n"), "\n")
		if !ok {
			return nil, fmt.Errorf("版本不支持参数 %s", key)
		}
		actual[key] = value
	}
	applied := []string{}
	rollback := func(cause error) (model.Config, error) {
		recovery, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		failed := false
		for i := len(applied) - 1; i >= 0; i-- {
			key := applied[i]
			if _, err := e.redisCommand(recovery, r, "CONFIG", "SET", key, actual[key]); err != nil {
				failed = true
			}
		}
		if err := writeOwnedAtomic(path, []byte(old), 0640); err != nil {
			failed = true
		}
		if failed {
			return nil, fmt.Errorf("Redis 应用失败，运行配置回退未完成，请检查持久化状态：%w", cause)
		}
		return nil, fmt.Errorf("Redis 应用失败，已恢复原运行值和配置文件：%w", cause)
	}
	log("3/4 应用受控 Redis 动态参数")
	for _, key := range keys {
		if _, err := e.redisCommand(ctx, r, "CONFIG", "SET", key, after[key]); err != nil {
			return rollback(err)
		}
		applied = append(applied, key)
		if key == "appendonly" && after[key] == "yes" {
			deadline := time.Now().Add(90 * time.Second)
			for {
				text, err := e.redisCommand(ctx, r, "INFO", "persistence")
				if err != nil {
					return rollback(err)
				}
				info := parsePairs(text, ":")
				if info["aof_rewrite_in_progress"] == "0" && info["aof_last_bgrewrite_status"] == "ok" {
					break
				}
				if time.Now().After(deadline) {
					return rollback(errors.New("AOF 后台重写未完成"))
				}
				select {
				case <-ctx.Done():
					return rollback(ctx.Err())
				case <-time.After(time.Second):
				}
			}
		}
	}
	st, err := os.Stat(path)
	if err != nil {
		return rollback(err)
	}
	if err = writeOwnedAtomic(path, []byte(next), st.Mode().Perm()); err != nil {
		return rollback(err)
	}
	log("4/4 验证 Redis 协议及持久化状态")
	if _, err = e.redisCommand(ctx, r, "PING"); err != nil {
		return rollback(err)
	}
	r.Config["configurationRevision"] = security.Hash(next)
	if err = e.save(r); err != nil {
		return rollback(err)
	}
	return model.Config{"configurationRevision": security.Hash(next)}, nil
}

func (e *Engine) redisSlowLog(ctx context.Context, r model.Resource) (model.Config, error) {
	text, err := e.redisCommandFormat(ctx, r, "--json", "SLOWLOG", "GET", "100")
	if err != nil {
		return nil, err
	}
	entries, err := redactRedisSlowLog([]byte(text))
	if err != nil {
		return nil, err
	}
	lines := []string{"最近 100 条慢命令，按耗时排序；所有命令参数和客户端地址已隐藏。", "ID\t时间\t耗时（微秒）\t命令"}
	for _, entry := range entries {
		lines = append(lines, fmt.Sprintf("%v\t%s\t%v\t%s [参数已隐藏]", entry["id"], entry["time"], entry["durationMicros"], entry.String("command")))
	}
	return model.Config{"entries": entries, "content": strings.Join(lines, "\n")}, nil
}

func redactRedisSlowLog(raw []byte) ([]model.Config, error) {
	var records [][]json.RawMessage
	if len(raw) > 2<<20 || json.Unmarshal(raw, &records) != nil {
		return nil, errors.New("Redis 慢日志响应不可解析")
	}
	out := []model.Config{}
	for _, record := range records {
		if len(record) < 4 {
			continue
		}
		var id, at, duration int64
		var args []string
		if json.Unmarshal(record[0], &id) != nil || json.Unmarshal(record[1], &at) != nil || json.Unmarshal(record[2], &duration) != nil || json.Unmarshal(record[3], &args) != nil || len(args) == 0 {
			continue
		}
		command := strings.ToUpper(args[0])
		if len(command) > 64 || strings.IndexFunc(command, func(r rune) bool { return !(r >= 'A' && r <= 'Z' || r == '.' || r == '_' || r == '-') }) >= 0 {
			command = "[未知命令]"
		}
		out = append(out, model.Config{"id": strconv.FormatInt(id, 10), "time": time.Unix(at, 0).Format(time.RFC3339), "durationMicros": duration, "command": command})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["durationMicros"].(int64) > out[j]["durationMicros"].(int64) })
	return out, nil
}
