package scheduling

import (
	"encoding/json"
	"errors"
	"github.com/robfig/cron/v3"
	"net/url"
	"opsview/internal/model"
	"opsview/internal/security"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var Types = map[string]string{"backup": "网站 / 数据库备份", "renew": "证书续期", "shell": "Shell 脚本", "python": "Python 脚本", "http": "HTTP 请求", "directory": "目录备份", "panel": "面板备份", "rotate": "网站日志切割", "cleanup": "日志清理", "site-state": "网站启停", "time-sync": "时间同步"}

func Cron(c model.Config) (cron.Schedule, error) {
	expr := c.String("cron")
	if len(strings.Fields(expr)) != 5 {
		return nil, errors.New("Cron 使用五段格式，最短周期一分钟")
	}
	if zone := c.String("timezone"); zone != "" {
		if _, err := time.LoadLocation(zone); err != nil {
			return nil, errors.New("时区无效")
		}
		expr = "CRON_TZ=" + zone + " " + expr
	}
	return cron.ParseStandard(expr)
}
func Digest(c model.Config) string {
	v := model.Config{}
	for k, x := range c {
		if k != "rootApproval" && k != "enabled" && k != "pinned" && k != "category" {
			v[k] = x
		}
	}
	b, _ := json.Marshal(v)
	return security.Hash(string(b))
}
func Validate(c model.Config) error {
	if _, ok := Types[c.String("operation")]; !ok {
		return errors.New("不支持的计划任务类型")
	}
	if _, err := Cron(c); err != nil {
		return err
	}
	if c.Int("retentionDays") != 0 && (c.Int("retentionDays") < 1 || c.Int("retentionDays") > 365) {
		return errors.New("日志保留天数须为 1–365")
	}
	if c["targetIds"] != nil {
		var ids []string
		raw, _ := json.Marshal(c["targetIds"])
		if json.Unmarshal(raw, &ids) != nil || len(ids) > 100 {
			return errors.New("备份目标须为不超过 100 条的标识列表")
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if id == "" || seen[id] {
				return errors.New("备份目标为空或重复")
			}
			seen[id] = true
		}
	}
	switch c.String("operation") {
	case "backup", "renew", "rotate", "site-state":
		if c.String("operation") == "rotate" && (c.Int("keepLogs") < 0 || c.Int("keepLogs") > 3650 || c.Int("minBytes") < 0) {
			return errors.New("日志保留或轮转大小无效")
		}
		if c.String("operation") == "backup" && len(TargetIDs(c)) > 0 {
			break
		}
		if c.String("targetId") == "" {
			return errors.New("请选择任务目标")
		}
	case "shell", "python":
		if len(c.String("script")) == 0 || len(c.String("script")) > 1<<20 {
			return errors.New("脚本内容须为 1 B–1 MiB")
		}
		if !regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`).MatchString(c.String("user")) {
			return errors.New("执行用户无效")
		}
		if !filepath.IsAbs(c.String("workingDirectory")) {
			return errors.New("工作目录须为绝对路径")
		}
		if c.Int("timeoutSeconds") < 60 || c.Int("timeoutSeconds") > 86400 {
			return errors.New("脚本超时须为 60–86400 秒")
		}
		if c.String("operation") == "python" && !regexp.MustCompile(`^/usr/(local/)?bin/python3([.][0-9]+)?$`).MatchString(c.String("interpreter")) {
			return errors.New("请选择系统 Python 3 解释器")
		}
		var args []string
		b, _ := json.Marshal(c["args"])
		if c["args"] != nil && json.Unmarshal(b, &args) != nil {
			return errors.New("脚本参数须为字符串数组")
		}
		var environment map[string]string
		b, _ = json.Marshal(c["environment"])
		if c["environment"] != nil && json.Unmarshal(b, &environment) != nil {
			return errors.New("私密变量须为字符串键值对象")
		}
		for k, v := range environment {
			if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(k) || strings.ContainsAny(v, "\x00\r\n") {
				return errors.New("环境变量名称或值无效")
			}
		}
	case "http":
		u, err := url.Parse(c.String("url"))
		if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return errors.New("HTTP URL 无效，不允许 URL 内嵌凭据")
		}
		var headers map[string]string
		b, _ := json.Marshal(c["headers"])
		if c["headers"] != nil && json.Unmarshal(b, &headers) != nil {
			return errors.New("请求头须为字符串键值对象")
		}
		for k, v := range headers {
			if !regexp.MustCompile("^[!#$%&'*+.^_`|~a-zA-Z0-9-]+$").MatchString(k) || strings.ContainsAny(v, "\x00\r\n") {
				return errors.New("请求头无效")
			}
		}
		if len(c.String("body")) > 1<<20 {
			return errors.New("HTTP 请求体不能超过 1 MiB")
		}
		if c.String("method") != "GET" && c.String("method") != "POST" {
			return errors.New("仅支持 GET/POST")
		}
		if c.Int("timeoutSeconds") < 1 || c.Int("timeoutSeconds") > 300 {
			return errors.New("请求超时须为 1–300 秒")
		}
		if c.Int("expectedStatus") < 100 || c.Int("expectedStatus") > 599 {
			return errors.New("预期状态码无效")
		}
	case "directory", "cleanup":
		if c.Int("retention") < 0 || c.Int("retention") > 100 {
			return errors.New("备份保留须为 1–100 份")
		}
		if !filepath.IsAbs(c.String("path")) {
			return errors.New("目录须为绝对路径")
		}
		if c.String("operation") == "cleanup" && (c.Int("olderDays") < 1 || c.Int("olderDays") > 3650) {
			return errors.New("清理保留天数须大于零")
		}
	}
	if c.String("operation") == "site-state" && c.String("state") != "start" && c.String("state") != "stop" {
		return errors.New("网站操作须为启动或停止")
	}
	return nil
}
func Public(c model.Config) model.Config {
	v := model.Config{}
	for k, x := range security.Redact(c) {
		if k != "script" && k != "rootApproval" && k != "environment" && k != "headers" && k != "body" {
			v[k] = x
		}
	}
	return v
}

func TargetIDs(c model.Config) []string {
	var ids []string
	raw, _ := json.Marshal(c["targetIds"])
	_ = json.Unmarshal(raw, &ids)
	if len(ids) == 0 && c.String("targetId") != "" {
		ids = []string{c.String("targetId")}
	}
	return ids
}
