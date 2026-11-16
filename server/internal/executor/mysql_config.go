package executor

import (
	"context"
	"errors"
	"fmt"
	"opsview/internal/model"
	"opsview/internal/security"
	"opsview/internal/software"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SET GLOBAL never consumes SQL fragments from the browser. Names come from the
// versioned field definitions and values are normalized to numeric literals.
func (e *Engine) applyMySQLConfig(ctx context.Context, r model.Resource, path, old, next string, log func(string)) (model.Config, error) {
	before, after := software.Values("mysql", r.Config.String("version"), "main", old), software.Values("mysql", r.Config.String("version"), "main", next)
	keys := []string{}
	values := map[string]string{}
	for _, field := range software.Fields("mysql", r.Config.String("version"), "main") {
		if before[field.Key] == after[field.Key] {
			continue
		}
		if field.Apply != "dynamic" {
			return nil, fmt.Errorf("%s 需要重启，不能动态应用", field.Label)
		}
		v, err := mysqlNumericValue(after[field.Key])
		if err != nil {
			return nil, err
		}
		values[field.Key] = v
		keys = append(keys, field.Key)
	}
	sort.Strings(keys)
	actual := map[string]string{}
	for _, key := range keys {
		v, err := e.mysqlGlobalValue(ctx, r, key)
		if err != nil {
			return nil, err
		}
		actual[key] = v
	}
	applied := []string{}
	rollback := func(cause error) (model.Config, error) {
		recovery, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		failed := false
		for i := len(applied) - 1; i >= 0; i-- {
			key := applied[i]
			if e.sql(recovery, r, "", strings.NewReader("SET GLOBAL "+key+"="+actual[key]+";"), func(string) {}) != nil {
				failed = true
			}
		}
		if st, err := os.Stat(path); err != nil || writeOwnedAtomic(path, []byte(old), st.Mode().Perm()) != nil {
			failed = true
		}
		if failed {
			return nil, fmt.Errorf("MySQL 动态配置失败，回退未完成，请检查当前状态：%w", cause)
		}
		return nil, fmt.Errorf("MySQL 动态配置失败，已恢复原配置和运行值：%w", cause)
	}
	log("3/4 使用白名单 SET GLOBAL 应用动态参数；会话级默认值对新连接生效")
	for _, key := range keys {
		if err := e.sql(ctx, r, "", strings.NewReader("SET GLOBAL "+key+"="+values[key]+";"), func(string) {}); err != nil {
			return rollback(errors.New("MySQL 拒绝参数 " + key))
		}
		applied = append(applied, key)
		// Re-read the actual value: MySQL may round buffer sizes to allocation units.
		if _, err := e.mysqlGlobalValue(ctx, r, key); err != nil {
			return rollback(err)
		}
	}
	st, err := os.Stat(path)
	if err != nil {
		return rollback(err)
	}
	if err = writeOwnedAtomic(path, []byte(next), st.Mode().Perm()); err != nil {
		return rollback(err)
	}
	log("4/4 验证 MySQL 协议并持久化受管配置")
	if err = e.sql(ctx, r, "", strings.NewReader("SELECT 1;"), func(string) {}); err != nil {
		return rollback(errors.New("MySQL 协议验证失败"))
	}
	r.Config["configurationRevision"] = security.Hash(next)
	if err = e.save(r); err != nil {
		return rollback(err)
	}
	return model.Config{"configurationRevision": security.Hash(next), "runtimeStatus": r.Status}, nil
}

func (e *Engine) mysqlGlobalValue(ctx context.Context, r model.Resource, key string) (string, error) {
	var value strings.Builder
	if err := e.sql(ctx, r, "", strings.NewReader("SELECT @@GLOBAL."+key+";"), func(s string) { value.WriteString(s) }); err != nil {
		return "", errors.New("无法读取 MySQL 当前参数 " + key)
	}
	v, err := mysqlNumericValue(strings.TrimSpace(value.String()))
	if err != nil {
		return "", errors.New("MySQL 当前参数格式不可识别：" + key)
	}
	return v, nil
}
func mysqlNumericValue(value string) (string, error) {
	switch strings.ToUpper(value) {
	case "ON":
		return "1", nil
	case "OFF":
		return "0", nil
	}
	if regexp.MustCompile(`^[0-9]{1,16}(\.[0-9]{1,6})?$`).MatchString(value) {
		return value, nil
	}
	if len(value) > 1 {
		factor := map[byte]uint64{'K': 1 << 10, 'M': 1 << 20, 'G': 1 << 30}[strings.ToUpper(value)[len(value)-1]]
		if factor > 0 {
			n, err := strconv.ParseUint(value[:len(value)-1], 10, 64)
			if err == nil && n <= ((1<<63)-1)/factor {
				return strconv.FormatUint(n*factor, 10), nil
			}
		}
	}
	return "", errors.New("MySQL 动态参数必须为安全的数值或大小")
}
