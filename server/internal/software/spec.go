// Package software describes the editable, version-aware surface of managed services.
// The executor and the UI consume the same definitions; privileged directives are never editable.
package software

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type Field struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Section string   `json:"section,omitempty"`
	Group   string   `json:"group"`
	Default string   `json:"default"`
	Pattern string   `json:"pattern"`
	Apply   string   `json:"apply"`
	Options []string `json:"options,omitempty"`
}

func field(key, label, section, group, value, pattern, apply string, options ...string) Field {
	return Field{key, label, section, group, value, pattern, apply, options}
}

const number = `^[0-9]{1,10}$`
const size = `^(?:0|[1-9][0-9]{0,8}[kKmMgG]?)$`
const duration = `^[0-9]{1,8}[smhd]?$`

func Fields(sw, version, file string) []Field {
	if file == "" {
		file = "main"
	}
	switch sw {
	case "nginx":
		return []Field{
			field("worker_processes", "工作进程", "", "performance", "auto", `^(auto|[1-9][0-9]{0,3})$`, "reload"),
			field("worker_connections", "每进程连接数", "events", "performance", "1024", number, "reload"),
			field("keepalive_timeout", "长连接超时", "http", "performance", "65", duration, "reload"),
			field("gzip", "Gzip 压缩", "http", "performance", "off", `^(on|off)$`, "reload", "on", "off"),
			field("gzip_min_length", "压缩最小长度", "http", "performance", "1024", size, "reload"),
			field("gzip_comp_level", "压缩等级", "http", "performance", "5", `^[1-9]$`, "reload"),
			field("client_max_body_size", "请求体上限", "http", "performance", "10m", size, "reload"),
			field("server_names_hash_bucket_size", "域名哈希桶", "http", "performance", "64", number, "reload"),
			field("client_header_buffer_size", "请求头缓冲", "http", "performance", "1k", size, "reload"),
			field("client_body_buffer_size", "请求体缓冲", "http", "performance", "16k", size, "reload"),
		}
	case "mysql":
		out := []Field{}
		for _, p := range [][3]string{{"max_connections", "最大连接", "151"}, {"table_open_cache", "表缓存", "2000"}, {"thread_cache_size", "线程缓存", "16"}, {"key_buffer_size", "MyISAM 缓冲", "8388608"}, {"tmp_table_size", "临时表上限", "16777216"}, {"innodb_buffer_pool_size", "InnoDB 缓冲池", "134217728"}, {"innodb_log_buffer_size", "日志缓冲", "16777216"}, {"sort_buffer_size", "排序缓冲", "262144"}, {"read_buffer_size", "读取缓冲", "131072"}, {"read_rnd_buffer_size", "随机读取缓冲", "262144"}, {"join_buffer_size", "连接缓冲", "262144"}, {"thread_stack", "线程栈", "262144"}, {"binlog_cache_size", "Binlog 缓冲", "32768"}} {
			apply := "dynamic"
			if p[0] == "thread_stack" || p[0] == "innodb_log_buffer_size" {
				apply = "restart"
			}
			out = append(out, field(p[0], p[1], "mysqld", "performance", p[2], size, apply))
		}
		out = append(out, field("slow_query_log", "慢查询日志", "mysqld", "slow", "0", `^[01]$`, "dynamic", "0", "1"), field("long_query_time", "慢查询阈值（秒）", "mysqld", "slow", "2", `^[0-9]{1,5}(\.[0-9]{1,3})?$`, "dynamic"))
		if strings.HasPrefix(version, "5.7") {
			out = append(out, field("expire_logs_days", "Binlog 保留天数", "mysqld", "binlog", "7", number, "dynamic"))
		} else {
			out = append(out, field("binlog_expire_logs_seconds", "Binlog 保留秒数", "mysqld", "binlog", "604800", number, "dynamic"))
		}
		return out
	case "php":
		if file == "fpm" {
			return []Field{
				field("pm", "进程模式", "www", "performance", "ondemand", `^(static|dynamic|ondemand)$`, "reload", "static", "dynamic", "ondemand"),
				field("pm.max_children", "最大子进程", "www", "performance", "8", `^[1-9][0-9]{0,3}$`, "reload"),
				field("pm.start_servers", "起始子进程", "www", "performance", "2", number, "reload"),
				field("pm.min_spare_servers", "最小空闲进程", "www", "performance", "1", number, "reload"),
				field("pm.max_spare_servers", "最大空闲进程", "www", "performance", "3", number, "reload"),
				field("pm.max_requests", "子进程请求上限", "www", "performance", "500", number, "reload"),
				field("pm.process_idle_timeout", "空闲进程超时", "www", "performance", "10s", duration, "reload"),
				field("request_terminate_timeout", "请求终止超时", "www", "timeout", "60s", duration, "reload"),
				field("request_slowlog_timeout", "慢请求阈值", "www", "slow", "5s", duration, "reload"),
			}
		}
		return []Field{
			field("short_open_tag", "短标签", "PHP", "general", "Off", `^(On|Off)$`, "reload", "On", "Off"),
			field("max_execution_time", "执行超时（秒）", "PHP", "timeout", "60", number, "reload"),
			field("max_input_time", "输入超时（秒）", "PHP", "timeout", "60", number, "reload"),
			field("memory_limit", "单请求内存限制", "PHP", "general", "128M", `^(-1|[1-9][0-9]{0,8}[KMG]?)$`, "reload"),
			field("post_max_size", "POST 上限", "PHP", "upload", "32M", size, "reload"),
			field("upload_max_filesize", "上传文件上限", "PHP", "upload", "16M", size, "reload"),
			field("max_file_uploads", "上传文件数量", "PHP", "upload", "20", number, "reload"),
			field("file_uploads", "允许文件上传", "PHP", "upload", "On", `^(On|Off)$`, "reload", "On", "Off"),
			field("default_socket_timeout", "Socket 超时", "PHP", "timeout", "60", number, "reload"),
			field("display_errors", "显示错误", "PHP", "general", "Off", `^(On|Off)$`, "reload", "On", "Off"),
			field("error_reporting", "错误报告", "PHP", "general", "E_ALL", `^[A-Z_0-9 |&~]{1,128}$`, "reload"),
			field("cgi.fix_pathinfo", "路径解析", "PHP", "general", "0", `^[01]$`, "reload", "0", "1"),
			field("date.timezone", "时区", "PHP", "general", "Asia/Shanghai", `^[A-Za-z_/+-]{1,64}$`, "reload"),
			field("disable_functions", "禁用函数", "PHP", "functions", "exec,passthru,shell_exec,system,proc_open,popen", `^[A-Za-z0-9_, ]{0,1000}$`, "reload"),
			field("opcache.enable", "OPcache", "opcache", "performance", "1", `^[01]$`, "reload", "0", "1"),
			field("opcache.memory_consumption", "OPcache 内存（MB）", "opcache", "performance", "128", number, "reload"),
			field("opcache.max_accelerated_files", "OPcache 文件数", "opcache", "performance", "10000", number, "reload"),
			field("opcache.validate_timestamps", "检测代码更新", "opcache", "performance", "1", `^[01]$`, "reload", "0", "1"),
		}
	case "redis":
		return []Field{
			field("maxmemory", "内存上限（字节/单位）", "", "performance", "256mb", `^[0-9]{1,12}(kb|mb|gb)?$`, "dynamic"),
			field("maxmemory-policy", "淘汰策略", "", "performance", "noeviction", `^(noeviction|allkeys-lru|volatile-lru|allkeys-random|volatile-random|volatile-ttl|allkeys-lfu|volatile-lfu)$`, "dynamic", "noeviction", "allkeys-lru", "volatile-lru", "allkeys-random", "volatile-random", "volatile-ttl", "allkeys-lfu", "volatile-lfu"),
			field("maxclients", "最大连接", "", "performance", "1000", number, "dynamic"),
			field("timeout", "客户端超时", "", "performance", "0", number, "dynamic"),
			field("tcp-keepalive", "TCP keepalive", "", "performance", "300", number, "dynamic"),
			field("appendonly", "AOF 持久化", "", "persistence", "yes", `^(yes|no)$`, "dynamic", "yes", "no"),
			field("appendfsync", "AOF 同步策略", "", "persistence", "everysec", `^(always|everysec|no)$`, "dynamic", "always", "everysec", "no"),
			field("auto-aof-rewrite-percentage", "AOF 重写比例", "", "persistence", "100", number, "dynamic"),
			field("auto-aof-rewrite-min-size", "AOF 重写最小大小", "", "persistence", "64mb", `^[0-9]{1,12}(kb|mb|gb)?$`, "dynamic"),
			field("slowlog-log-slower-than", "慢命令阈值（微秒）", "", "slow", "10000", number, "dynamic"),
			field("slowlog-max-len", "慢日志保留条数", "", "slow", "128", number, "dynamic"),
		}
	}
	return nil
}
func ValidateValues(fields []Field, values map[string]string) error {
	for k, v := range values {
		found := false
		for _, f := range fields {
			if f.Key == k {
				found = true
				if !regexp.MustCompile(f.Pattern).MatchString(v) {
					return fmt.Errorf("%s 的值无效", f.Label)
				}
			}
		}
		if !found {
			return fmt.Errorf("不允许修改参数 %s", k)
		}
	}
	get := func(k string) int { v, _ := strconv.Atoi(values[k]); return v }
	if values["pm"] == "dynamic" && (get("pm.min_spare_servers") > get("pm.max_spare_servers") || get("pm.max_spare_servers") > get("pm.max_children") || get("pm.start_servers") < get("pm.min_spare_servers") || get("pm.start_servers") > get("pm.max_spare_servers")) {
		return fmt.Errorf("FPM 动态进程数量关系无效")
	}
	return nil
}
