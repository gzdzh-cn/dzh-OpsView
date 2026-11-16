package software

import (
	"fmt"
	"strconv"
	"strings"
)

type MySQLMemory struct {
	GlobalBytes           uint64 `json:"globalBytes"`
	PerConnectionBytes    uint64 `json:"perConnectionBytes"`
	ConnectionBudgetBytes uint64 `json:"connectionBudgetBytes"`
	EstimateBytes         uint64 `json:"estimateBytes"`
	Notice                string `json:"notice"`
}

func memoryBytes(text string) (uint64, error) {
	text = strings.ToUpper(strings.TrimSpace(text))
	factor := uint64(1)
	if len(text) > 0 {
		switch text[len(text)-1] {
		case 'K':
			factor = 1 << 10
		case 'M':
			factor = 1 << 20
		case 'G':
			factor = 1 << 30
		}
	}
	if factor != 1 {
		text = text[:len(text)-1]
	}
	n, err := strconv.ParseUint(text, 10, 64)
	if err != nil || n > (1<<50)/factor {
		return 0, fmt.Errorf("内存参数超出可估算范围")
	}
	return n * factor, nil
}

// This deliberately exposes a model, not a claim about actual memory or a hard upper bound.
func EstimateMySQLMemory(version string, values map[string]string) (MySQLMemory, error) {
	v := Values("mysql", version, "main", "")
	for k, x := range values {
		v[k] = x
	}
	if err := ValidateValues(Fields("mysql", version, "main"), v); err != nil {
		return MySQLMemory{}, err
	}
	out := MySQLMemory{Notice: "估算 = 全局缓存 + 最大连接数 × 每连接基础缓冲。缓冲按需分配；未计临时表、Performance Schema、引擎开销及操作系统缓存，不是内存上限。"}
	for _, k := range []string{"innodb_buffer_pool_size", "innodb_log_buffer_size", "key_buffer_size"} {
		n, err := memoryBytes(v[k])
		if err != nil {
			return out, err
		}
		out.GlobalBytes += n
	}
	for _, k := range []string{"sort_buffer_size", "read_buffer_size", "read_rnd_buffer_size", "join_buffer_size", "thread_stack", "binlog_cache_size"} {
		n, err := memoryBytes(v[k])
		if err != nil {
			return out, err
		}
		out.PerConnectionBytes += n
	}
	connections, err := strconv.ParseUint(v["max_connections"], 10, 32)
	if err != nil || connections < 1 || connections > 1000000 {
		return out, fmt.Errorf("连接数超出可估算范围")
	}
	if out.PerConnectionBytes > (1<<60)/connections {
		return out, fmt.Errorf("内存估算溢出")
	}
	out.ConnectionBudgetBytes = connections * out.PerConnectionBytes
	out.EstimateBytes = out.GlobalBytes + out.ConnectionBudgetBytes
	return out, nil
}

func MySQLMemoryPreset(budgetMB int) (map[string]string, error) {
	if budgetMB != 512 && budgetMB != 1024 && budgetMB != 2048 && budgetMB != 4096 && budgetMB != 8192 {
		return nil, fmt.Errorf("内存档位须为 512、1024、2048、4096 或 8192 MB")
	}
	connections := 50
	if budgetMB >= 2048 {
		connections = 100
	}
	if budgetMB >= 4096 {
		connections = 200
	}
	return map[string]string{"innodb_buffer_pool_size": fmt.Sprintf("%dM", budgetMB/2), "innodb_log_buffer_size": "16M", "key_buffer_size": "8M", "max_connections": strconv.Itoa(connections), "table_open_cache": strconv.Itoa(min(budgetMB, 4096)), "thread_cache_size": "16", "sort_buffer_size": "256K", "read_buffer_size": "128K", "read_rnd_buffer_size": "256K", "join_buffer_size": "256K", "thread_stack": "1M", "binlog_cache_size": "32K", "tmp_table_size": "16M"}, nil
}
