package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"opsview/internal/model"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func sqlLiteral(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, "\\", "\\\\"), "'", "''") + "'"
}
func pgLiteral(s string) string { return "E" + sqlLiteral(s) }
func (e *Engine) database(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	r := op.Resource
	inst, err := e.load(r.Config.String("instanceId"))
	if err != nil {
		return nil, err
	}
	software := inst.Config.String("software")
	if software != "mysql" && software != "postgresql" {
		return nil, errors.New("关联实例不是数据库")
	}
	action := strings.TrimPrefix(op.Name, "database.")
	username := r.Config.String("username")
	source := r.Config.String("source")
	if source == "" {
		source = "127.0.0.1"
		r.Config["source"] = source
	}
	if software == "mysql" && strings.Contains(source, ":") && strings.Contains(source, "/") {
		return nil, errors.New("MySQL 来源限制请使用单个 IPv6 地址，IPv4 可使用 CIDR")
	}
	remote := !isLoopbackSource(source)
	if (action == "create" || action == "access") && remote {
		if !r.Config.Bool("allowRemote") {
			return nil, errors.New("远程来源需要明确开启远程访问")
		}
		fw := databaseFirewall(r, inst)
		if err = e.firewallCompatible(ctx, fw, log); err != nil {
			return nil, err
		}
	}
	sqlText := ""
	switch action {
	case "create":
		if !validDBPassword(r.Config.String("password")) {
			return nil, errors.New("新数据库需要有效密码")
		}
		encoding := r.Config.String("encoding")
		if encoding == "" {
			if software == "mysql" {
				encoding = "utf8mb4"
			} else {
				encoding = "UTF8"
			}
		}
		collation := r.Config.String("collation")
		if !logicSQLName(encoding) || (collation != "" && !logicSQLName(collation)) {
			return nil, errors.New("编码或排序规则无效")
		}
		r.Config["encoding"] = encoding
		r.Config["owner"] = username
		r.Config["managedAccount"] = true
		if _, err = e.load(r.ID); err == nil {
			return nil, errors.New("数据库已登记，禁止重复创建")
		}
		if err = e.databaseVacant(ctx, inst, r); err != nil {
			return nil, err
		}
		if software == "mysql" {
			sqlText = fmt.Sprintf("CREATE DATABASE `%s` CHARACTER SET utf8mb4;\nCREATE USER %s@%s IDENTIFIED BY %s;\nGRANT ALL PRIVILEGES ON `%s`.* TO %s@%s;", r.Name, sqlLiteral(username), sqlLiteral(mysqlAccountHost(source)), sqlLiteral(r.Config.String("password")), r.Name, sqlLiteral(username), sqlLiteral(mysqlAccountHost(source)))
			sqlText = strings.Replace(sqlText, "CHARACTER SET utf8mb4", "CHARACTER SET "+encoding, 1)
			if collation != "" {
				sqlText = strings.Replace(sqlText, "CHARACTER SET "+encoding+";", "CHARACTER SET "+encoding+" COLLATE "+collation+";", 1)
			}
		} else {
			sqlText = fmt.Sprintf("CREATE ROLE \"%s\" LOGIN PASSWORD %s;\nCREATE DATABASE \"%s\" OWNER \"%s\";", username, pgLiteral(r.Config.String("password")), r.Name, username)
			sqlText = strings.TrimSuffix(sqlText, ";") + " ENCODING " + pgLiteral(encoding) + " TEMPLATE template0;"
		}

	case "access", "delete":
		return nil, errors.New("请使用数据库专用访问或回收操作")
	case "import":
		if _, err = e.load(r.ID); err != nil {
			return nil, err
		}
		path, err := safePath(filepath.Join(e.Root, "instances", inst.ID, "imports"), op.Params.String("filename"))
		if err != nil {
			return nil, err
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		err = e.sql(ctx, inst, r.Name, file, func(string) {})
		if err != nil {
			return nil, errors.New("SQL 导入失败，请检查格式、版本及实际数据；查询内容不写入任务日志")
		}
		return model.Config{}, nil
	case "migrate":
		return e.migrateDatabase(ctx, op, inst, log)
	default:
		return nil, errors.New("数据库操作无效")
	}
	if err = e.dbRun(ctx, inst, "", sqlText); err != nil {
		return nil, err
	}
	if action == "delete" {
		if _, err := e.firewall(ctx, model.Operation{Name: "firewall.delete", Resource: databaseFirewall(r, inst), Params: model.Config{}}, log); err != nil {
			log("关联防火墙规则可能已回退或不存在，请在安全页面核对")
		}

		if err = e.forget(r.ID); err != nil {
			return nil, err
		}
		if software == "postgresql" {
			if err = e.configurePGAccess(ctx, inst, log); err != nil {
				return nil, err
			}
		}
		return model.Config{}, nil
	}
	r.Status = "ready"
	if err = e.save(r); err != nil {
		return nil, err
	}
	if software == "postgresql" {
		if err = e.configurePGAccess(ctx, inst, log); err != nil {
			return nil, err
		}
	}
	result := model.Config{"engine": software, "encoding": r.Config["encoding"], "collation": r.Config["collation"], "owner": r.Config["owner"], "managedAccount": r.Config["managedAccount"]}
	if remote {
		fw := databaseFirewall(r, inst)
		data, err := e.firewall(ctx, model.Operation{Name: "firewall.apply", Resource: fw, Params: model.Config{}}, log)
		if err != nil {
			return nil, err
		}
		for k, v := range data {
			fw.Config[k] = v
		}
		fw.Status = "ready"
		result["linkedFirewall"] = fw
		inst, err = e.load(inst.ID)
		if err != nil {
			return nil, err
		}
		if err = e.exposeDatabase(ctx, inst, log); err != nil {
			_, _ = e.firewall(ctx, model.Operation{Name: "firewall.delete", Resource: fw, Params: model.Config{}}, log)
			return nil, err
		}
		log("远程端口已限源放行，请在安全页面 120 秒内确认规则")
	}
	return result, nil
}
func (e *Engine) sql(ctx context.Context, inst model.Resource, db string, input io.Reader, log func(string)) error {
	software := inst.Config.String("software")
	port := strconv.Itoa(inst.Config.Int("port"))
	pass := inst.Config.String("password")
	binary := "mysql"
	args := []string{"--protocol=TCP", "-h127.0.0.1", "-P" + port, "-uroot", "--batch", "--skip-column-names", "--raw"}
	env := []string{"MYSQL_PWD=" + pass}
	if software == "postgresql" {
		binary = "psql"
		args = []string{"-h", "127.0.0.1", "-p", port, "-U", "postgres", "-v", "ON_ERROR_STOP=1", "-t", "-A", "-d", "postgres"}
		env = []string{"PGPASSWORD=" + pass}
		if db != "" {
			args[len(args)-1] = db
		}
	} else if db != "" {
		args = append(args, "--", db)
	}
	if inst.Config.String("mode") == "docker" {
		execArgs := []string{"exec", "-i", "-e", strings.SplitN(env[0], "=", 2)[0], "opsview-" + inst.ID, binary}
		return run(ctx, log, "", env, input, "docker", append(execArgs, args...)...)
	}
	path := filepath.Join(e.Root, "instances", inst.ID, "runtime", "bin", binary)
	return run(ctx, log, "", env, input, path, args...)
}
func isLoopbackSource(source string) bool {
	if ip := net.ParseIP(source); ip != nil {
		return ip.IsLoopback()
	}
	_, n, err := net.ParseCIDR(source)
	if err != nil {
		return false
	}
	ones, bits := n.Mask.Size()
	if bits == 32 {
		return ones >= 8 && n.IP[0] == 127
	}
	return bits == 128 && ones == 128 && n.IP.IsLoopback()
}
func databaseFirewall(r, inst model.Resource) model.Resource {
	return model.Resource{ID: "db-" + r.ID, Kind: "firewall", Name: "db-" + r.Name, Config: model.Config{"protocol": "tcp", "ports": strconv.Itoa(inst.Config.Int("port")), "source": r.Config.String("source"), "action": "allow", "scope": "host", "databaseInstanceId": inst.ID, "note": "数据库 " + r.Name + " 的关联规则"}}
}
func (e *Engine) exposeDatabase(ctx context.Context, inst model.Resource, log func(string)) error {
	return e.setDatabaseBind(ctx, inst, "*", log)
}
func (e *Engine) setDatabaseBind(ctx context.Context, inst model.Resource, bind string, log func(string)) error {
	if inst.Config.String("bind") == bind {
		return nil
	}
	old := inst
	old.Config = mapClone(inst.Config)
	inst.Config = mapClone(inst.Config)
	inst.Config["bind"] = bind
	base := filepath.Join(e.Root, "instances", inst.ID)
	var rollback func()
	if inst.Config.String("mode") == "docker" {
		entry, ok := e.catalogEntry(inst.Config.String("catalogId"))
		if !ok {
			return errors.New("实例安装配方不存在")
		}
		if _, err := dockerRequest(ctx, "DELETE", "/containers/opsview-"+inst.ID+"?force=true", nil); err != nil {
			return err
		}
		rollback = func() {
			recovery, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			_, _ = dockerRequest(recovery, "DELETE", "/containers/opsview-"+inst.ID+"?force=true", nil)
			_ = e.installContainer(recovery, old, entry, base, log)
		}
		if err := e.installContainer(ctx, inst, entry, base, log); err != nil {
			rollback()
			return err
		}
	} else {
		rel := "conf/my.cnf"
		if inst.Config.String("software") == "postgresql" {
			rel = "data/postgresql.conf"
		}
		data, err := os.ReadFile(filepath.Join(base, rel))
		if err != nil {
			return err
		}
		oldBind := old.Config.String("bind")
		if oldBind == "" {
			oldBind = "127.0.0.1"
		}
		next := strings.ReplaceAll(string(data), "bind-address="+oldBind, "bind-address="+bind)
		next = strings.ReplaceAll(next, "listen_addresses = '"+oldBind+"'", "listen_addresses = '"+bind+"'")
		if err = writeOwned(base, rel, []byte(next)); err != nil {
			return err
		}
		rollback = func() {
			recovery, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = writeOwned(base, rel, data)
			_ = systemctl(recovery, log, "restart", serviceName(old))
		}
		if err = systemctl(ctx, log, "restart", serviceName(inst)); err != nil {
			rollback()
			return err
		}
	}
	if err := waitPort(ctx, inst.Config.Int("port")); err != nil {
		rollback()
		return err
	}
	if err := e.save(inst); err != nil {
		rollback()
		return err
	}
	return nil
}

func mapClone(c model.Config) model.Config {
	out := model.Config{}
	for k, v := range c {
		out[k] = v
	}
	return out
}

// All supported MySQL versions accept IPv4 netmasks; CIDR prefix syntax is
// newer than MySQL 5.7. Keep user-facing CIDR and render the account host here.
func mysqlAccountHost(source string) string {
	if _, network, err := net.ParseCIDR(source); err == nil && network.IP.To4() != nil {
		return network.IP.String() + "/" + net.IP(network.Mask).String()
	}
	return source
}
