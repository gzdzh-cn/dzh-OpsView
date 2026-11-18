package executor

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"opsview/internal/model"
	"opsview/internal/security"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// RESP transport keeps binary keys/values out of process arguments and task logs.
// Only this file's structured operations can construct commands.
type redisWire struct {
	net.Conn
	r *bufio.Reader
}

func redisReply(r *bufio.Reader, depth int, budget *int) (any, error) {
	if depth > 8 || *budget <= 0 {
		return nil, errors.New("Redis 响应超过限制")
	}
	bytes, err := r.ReadSlice('\n')
	line := string(bytes)
	if err != nil {
		return nil, err
	}
	*budget -= len(line)
	if len(line) < 3 || len(line) > 65536 {
		return nil, errors.New("Redis 响应无效")
	}
	body := line[1 : len(line)-2]
	switch line[0] {
	case '+':
		return body, nil
	case '-':
		return nil, errors.New("Redis 拒绝操作，请检查类型、权限和实例状态")
	case ':':
		return strconv.ParseInt(body, 10, 64)
	case '$':
		n, err := strconv.Atoi(body)
		if err != nil || n < -1 || n > *budget {
			return nil, errors.New("Redis 内容超过读取上限")
		}
		if n == -1 {
			return nil, nil
		}
		*budget -= n + 2
		b := make([]byte, n+2)
		_, err = io.ReadFull(r, b)
		if err != nil {
			return nil, err
		}
		return string(b[:n]), nil
	case '*':
		n, err := strconv.Atoi(body)
		if err != nil || n < -1 || n > 100000 || n > *budget {
			return nil, errors.New("Redis 集合超过读取上限")
		}
		if n == -1 {
			return nil, nil
		}
		a := make([]any, 0, n)
		for j := 0; j < n; j++ {
			v, err := redisReply(r, depth+1, budget)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		return a, nil
	}
	return nil, errors.New("Redis 协议无效")
}
func (c *redisWire) command(args ...string) (any, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
	}
	if _, err := io.WriteString(c.Conn, b.String()); err != nil {
		return nil, err
	}
	budget := 4 << 20
	return redisReply(c.r, 0, &budget)
}
func (e *Engine) redisConnect(ctx context.Context, i model.Resource, db int) (*redisWire, error) {
	if i.Kind != "instance" || i.Config.String("software") != "redis" || i.Config.Int("port") < 1 || i.Config.Int("port") > 65535 || db < 0 || db > 65535 {
		return nil, errors.New("Redis 实例或逻辑库无效")
	}
	conn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(i.Config.Int("port"))))
	if err != nil {
		return nil, errors.New("Redis 连接失败，请检查实例状态和本机发布端口")
	}
	deadline := time.Now().Add(10 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	c := &redisWire{Conn: conn, r: bufio.NewReaderSize(conn, 65536)}
	if password := i.Config.String("password"); password != "" {
		if _, err = c.command("AUTH", password); err != nil {
			conn.Close()
			return nil, err
		}
	}
	if _, err = c.command("SELECT", strconv.Itoa(db)); err != nil {
		conn.Close()
		return nil, err
	}
	return c, nil
}
func redisText(v any) string  { s, _ := v.(string); return s }
func redisArray(v any) []any  { a, _ := v.([]any); return a }
func redisNumber(v any) int64 { n, _ := v.(int64); return n }
func redisEncoded(v any) any {
	switch x := v.(type) {
	case string:
		return model.Config{"base64": base64.StdEncoding.EncodeToString([]byte(x)), "text": strings.ToValidUTF8(x, "�"), "binary": !utf8.ValidString(x)}
	case []any:
		a := []any{}
		for _, y := range x {
			a = append(a, redisEncoded(y))
		}
		return a
	default:
		return v
	}
}
func redisDecode(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) > 1<<20 {
		return "", errors.New("Base64 内容无效或超过 1 MiB")
	}
	return string(b), nil
}
func redisKeyInfo(c *redisWire, key string) (model.Config, error) {
	t, err := c.command("TYPE", key)
	if err != nil {
		return nil, err
	}
	ttl, err := c.command("PTTL", key)
	if err != nil {
		return nil, err
	}
	kind := redisText(t)
	lengthCmd := map[string]string{"string": "STRLEN", "hash": "HLEN", "list": "LLEN", "set": "SCARD", "zset": "ZCARD", "stream": "XLEN"}[kind]
	var length any = int64(0)
	if lengthCmd != "" {
		length, err = c.command(lengthCmd, key)
		if err != nil {
			return nil, err
		}
	}
	return model.Config{"key": base64.StdEncoding.EncodeToString([]byte(key)), "name": strings.ToValidUTF8(key, "�"), "binary": !utf8.ValidString(key), "type": kind, "ttlMillis": ttl, "length": length}, nil
}
func redisRevision(c *redisWire, key string) (string, error) {
	v, err := c.command("DUMP", key)
	if err != nil {
		return "", err
	}
	if v == nil {
		return "missing", nil
	}
	expiry, err := c.command("PEXPIRETIME", key)
	if err != nil {
		return "", err
	}
	return security.Hash(redisText(v) + "|" + strconv.FormatInt(redisNumber(expiry), 10)), nil
}
func (e *Engine) redisData(ctx context.Context, op model.Operation) (model.Config, error) {
	i, err := e.load(op.Resource.ID)
	if err != nil {
		return nil, err
	}
	p := op.Params
	db, err := redisDB(p)
	if err != nil {
		return nil, err
	}
	c, err := e.redisConnect(ctx, i, db)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	a := strings.TrimPrefix(op.Name, "redisdata.")
	switch a {
	case "databases":
		value, err := c.command("CONFIG", "GET", "databases")
		if err != nil {
			return nil, err
		}
		v := redisArray(value)
		if len(v) != 2 {
			return nil, errors.New("无法读取逻辑库数量")
		}
		n, _ := strconv.Atoi(redisText(v[1]))
		info, err := c.command("INFO", "keyspace")
		return model.Config{"count": n, "keyspace": redisText(info)}, err
	case "scan":
		cursor := p.String("cursor")
		if cursor == "" {
			cursor = "0"
		}
		if _, err = strconv.ParseUint(cursor, 10, 64); err != nil {
			return nil, errors.New("扫描游标无效")
		}
		pattern := p.String("pattern")
		if pattern == "" {
			pattern = "*"
		}
		if len(pattern) > 1024 {
			return nil, errors.New("搜索条件过长")
		}
		args := []string{"SCAN", cursor, "MATCH", pattern, "COUNT", "100"}
		if t := p.String("type"); t != "" {
			if !strings.Contains("|string|hash|list|set|zset|stream|", "|"+t+"|") {
				return nil, errors.New("类型无效")
			}
			args = append(args, "TYPE", t)
		}
		value, err := c.command(args...)
		if err != nil {
			return nil, err
		}
		v := redisArray(value)
		if len(v) != 2 {
			return nil, errors.New("扫描结果无效")
		}
		rows := []model.Config{}
		for _, key := range redisArray(v[1]) {
			item, err := redisKeyInfo(c, redisText(key))
			if err != nil {
				return nil, err
			}
			if item.String("type") != "none" {
				rows = append(rows, item)
			}
		}
		return model.Config{"cursor": v[0], "rows": rows}, nil
	}
	key, err := redisDecode(p.String("key"))
	if err != nil {
		return nil, err
	}
	if a == "read" {
		if _, err = c.command("WATCH", key); err != nil {
			return nil, err
		}
		info, err := redisKeyInfo(c, key)
		if err != nil {
			return nil, err
		}
		kind := info.String("type")
		cursor := p.String("cursor")
		if cursor == "" {
			cursor = "0"
		}
		offset, err := strconv.ParseUint(cursor, 10, 64)
		if err != nil || offset > 1<<40 {
			return nil, errors.New("游标无效")
		}
		var value any
		next := "0"
		switch kind {
		case "none":
			return nil, errors.New("键已删除或过期")
		case "string":
			chunk := uint64(64 << 10)
			if redisNumber(info["length"]) <= 1<<20 && offset == 0 {
				chunk = 1 << 20
			}
			value, err = c.command("GETRANGE", key, cursor, strconv.FormatUint(offset+chunk-1, 10))
			if int64(offset+chunk) < redisNumber(info["length"]) {
				next = strconv.FormatUint(offset+chunk, 10)
			}
			info["complete"] = offset == 0 && next == "0"
		case "hash", "set", "zset":
			cmd := map[string]string{"hash": "HSCAN", "set": "SSCAN", "zset": "ZSCAN"}[kind]
			var result any
			result, err = c.command(cmd, key, cursor, "COUNT", "100")
			v := redisArray(result)
			if len(v) == 2 {
				next = redisText(v[0])
				value = v[1]
			}
		case "list":
			value, err = c.command("LRANGE", key, cursor, strconv.FormatUint(offset+99, 10))
			if int64(offset+100) < redisNumber(info["length"]) {
				next = strconv.FormatUint(offset+100, 10)
			}
		case "stream":
			value, err = c.command("XRANGE", key, "-", "+", "COUNT", "100")
		default:
			value = "此类型仅展示元信息"
		}
		if err != nil {
			// An oversized bulk reply leaves unread bytes: close this connection;
			// never issue more RESP commands on a partially consumed response.
			info["editable"] = false
			info["notice"] = "此段包含大元素，请下载完整序列化内容"
			return info, nil
		}
		info["content"] = redisEncoded(value)
		info["cursor"] = next
		revision, revErr := redisRevision(c, key)
		if revErr != nil {
			info["editable"] = false
			info["notice"] = "键超过安全编辑范围，请下载完整序列化内容"
			return info, nil
		}
		info["editable"] = kind != "stream" && strings.Contains("|string|hash|list|set|zset|", "|"+kind+"|") && (kind != "string" || info.Bool("complete"))
		info["revision"] = revision
		if _, err = c.command("MULTI"); err != nil {
			return nil, err
		}
		if _, err = c.command("PING"); err != nil {
			return nil, err
		}
		snapshot, err := c.command("EXEC")
		if err != nil {
			return nil, err
		}
		if snapshot == nil {
			return nil, &model.RevisionConflict{Revision: "changed"}
		}
		return info, nil
	}
	if a != "write" {
		return nil, errors.New("Redis 操作不存在")
	}
	raw, _ := json.Marshal(p)
	if len(raw) > 1500000 {
		return nil, errors.New("单次编辑超过 1 MiB")
	}
	if _, err = c.command("WATCH", key); err != nil {
		return nil, err
	}
	revision, err := redisRevision(c, key)
	if err != nil {
		return nil, errors.New("键过大，暂不支持直接修改")
	}
	if revision != p.String("revision") {
		return nil, &model.RevisionConflict{Revision: revision}
	}
	info, err := redisKeyInfo(c, key)
	if err != nil {
		return nil, err
	}
	kind := info.String("type")
	operation := p.String("operation")
	commands := [][]string{}
	decode := func(k string) (string, error) { return redisDecode(p.String(k)) }
	switch operation {
	case "delete":
		if !p.Bool("confirmed") {
			return nil, errors.New("删除键需要确认")
		}
		commands = append(commands, []string{"UNLINK", key})
	case "rename":
		target, err := decode("target")
		if err != nil {
			return nil, err
		}
		commands = append(commands, []string{"RENAMENX", key, target})
	case "ttl":
		ttl, intErr := redisInteger(p, "ttl", -1, 2147483647)
		if intErr != nil {
			return nil, errors.New("TTL 无效")
		}
		if ttl == -1 {
			commands = append(commands, []string{"PERSIST", key})
		} else {
			commands = append(commands, []string{"EXPIRE", key, strconv.Itoa(ttl)})
		}
	case "set":
		want := p.String("type")
		if kind != "none" && kind != want {
			return nil, errors.New("类型已变化，请重新读取")
		}
		value, err := decode("value")
		if err != nil {
			return nil, err
		}
		field, err := decode("field")
		if err != nil {
			return nil, err
		}
		switch want {
		case "string":
			if redisNumber(info["length"]) > 1<<20 {
				return nil, errors.New("大字符串仅支持分段读取和下载，禁止以片段覆盖")
			}
			commands = append(commands, []string{"SET", key, value, "KEEPTTL"})
		case "hash":
			commands = append(commands, []string{"HSET", key, field, value})
		case "list":
			if p.String("index") == "" {
				commands = append(commands, []string{"RPUSH", key, value})
			} else {
				index, err := strconv.Atoi(p.String("index"))
				if err != nil || index < 0 {
					return nil, errors.New("列表下标无效")
				}
				commands = append(commands, []string{"LSET", key, strconv.Itoa(index), value})
			}
		case "set":
			commands = append(commands, []string{"SADD", key, value})
		case "zset":
			score := p.String("score")
			if n, err := strconv.ParseFloat(score, 64); err != nil || n != n {
				return nil, errors.New("分数无效")
			}
			commands = append(commands, []string{"ZADD", key, score, value})
		default:
			return nil, errors.New("该类型不支持编辑")
		}
	case "remove-member":
		value, err := decode("value")
		if err != nil {
			return nil, err
		}
		if kind == "list" {
			index, err := strconv.Atoi(p.String("index"))
			if err != nil || index < 0 || int64(index) >= redisNumber(info["length"]) {
				return nil, errors.New("列表下标无效")
			}
			commands = append(commands, []string{"EVAL", "redis.call('LSET',KEYS[1],ARGV[1],ARGV[2]); return redis.call('LREM',KEYS[1],1,ARGV[2])", "1", key, strconv.Itoa(index), "opsview-removal-" + security.ID()})
			break
		}
		cmd := map[string]string{"hash": "HDEL", "set": "SREM", "zset": "ZREM"}[kind]
		if cmd == "" {
			return nil, errors.New("该类型不支持成员删除")
		}
		commands = append(commands, []string{cmd, key, value})
	default:
		return nil, errors.New("不支持的键操作")
	}
	if _, err = c.command("MULTI"); err != nil {
		return nil, err
	}
	for _, args := range commands {
		if _, err = c.command(args...); err != nil {
			return nil, err
		}
	}
	result, err := c.command("EXEC")
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, &model.RevisionConflict{Revision: "changed"}
	}
	if operation == "rename" {
		values := redisArray(result)
		if len(values) != 1 || redisNumber(values[0]) != 1 {
			return nil, errors.New("目标键已存在，未覆盖")
		}
	}
	return model.Config{"ok": true}, nil
}

// DUMP returns one consistent, binary-safe serialization. Stream its RESP bulk
// body directly instead of materializing a large key in JSON or executor memory.
func (e *Engine) redisDownload(ctx context.Context, i model.Resource, p model.Config, w io.Writer) error {
	db, err := redisDB(p)
	if err != nil {
		return err
	}
	c, err := e.redisConnect(ctx, i, db)
	if err != nil {
		return err
	}
	defer c.Close()
	key, err := redisDecode(p.String("key"))
	if err != nil {
		return err
	}
	_ = c.SetDeadline(time.Now().Add(5 * time.Minute))
	if _, err = fmt.Fprintf(c.Conn, "*2\r\n$4\r\nDUMP\r\n$%d\r\n%s\r\n", len(key), key); err != nil {
		return err
	}
	header, err := c.r.ReadSlice('\n')
	line := string(header)
	if err != nil {
		return err
	}
	if len(line) < 3 || line[0] != '$' {
		return errors.New("Redis 拒绝下载")
	}
	n, err := strconv.ParseInt(strings.TrimSpace(line[1:]), 10, 64)
	if err != nil || n < 0 || n > 2<<30 {
		return errors.New("键不存在或超过 2 GiB 下载限制")
	}
	if response, ok := w.(http.ResponseWriter); ok {
		response.Header().Set("Content-Length", strconv.FormatInt(n, 10))
	}
	_, err = io.CopyN(w, c.r, n)
	return err
}

func redisInteger(p model.Config, key string, min, max int64) (int, error) {
	raw, ok := p[key]
	if !ok {
		return 0, errors.New("缺少整数参数: " + key)
	}
	n, err := strconv.ParseInt(fmt.Sprint(raw), 10, 64)
	if err != nil || n < min || n > max {
		return 0, errors.New("整数参数无效: " + key)
	}
	return int(n), nil
}
func redisDB(p model.Config) (int, error) {
	if _, ok := p["db"]; !ok {
		return 0, nil
	}
	return redisInteger(p, "db", 0, 65535)
}
