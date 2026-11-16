package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func (e *Engine) configurePGAccess(ctx context.Context, inst model.Resource, log func(string)) error {
	rules := "# Managed by OpsView\nlocal all all scram-sha-256\nhost all postgres 127.0.0.1/32 scram-sha-256\nhost all postgres ::1/128 scram-sha-256\n"
	for _, db := range e.states() {
		if db.Kind != "database" || db.Config.String("instanceId") != inst.ID {
			continue
		}
		source := db.Config.String("source")
		if source == "" {
			source = "127.0.0.1"
		}
		if !strings.Contains(source, "/") {
			if strings.Contains(source, ":") {
				source += "/128"
			} else {
				source += "/32"
			}
		}
		rules += fmt.Sprintf("host %s %s %s scram-sha-256\n", db.Name, db.Config.String("username"), source)
	}
	rules += "host all all 0.0.0.0/0 reject\nhost all all ::/0 reject\n"
	rel := "data/pg_hba.conf"
	if inst.Config.String("mode") == "docker" {
		rel = "data/pgdata/pg_hba.conf"
	}
	base := filepath.Join(e.Root, "instances", inst.ID)
	if err := writeOwned(base, rel, []byte(rules)); err != nil {
		return err
	}
	return e.sql(ctx, inst, "", strings.NewReader("SELECT pg_reload_conf();"), log)
}
func writeOwned(base, rel string, data []byte) error {
	root, err := os.OpenRoot(base)
	if err != nil {
		return err
	}
	defer root.Close()
	old, err := root.Stat(rel)
	if err != nil {
		return err
	}
	if err = writeRoot(root, rel, data, old.Mode().Perm()); err != nil {
		return err
	}
	file, err := root.Open(rel)
	if err != nil {
		return err
	}
	defer file.Close()
	if stat, ok := old.Sys().(*syscall.Stat_t); ok {
		return file.Chown(int(stat.Uid), int(stat.Gid))
	}
	return nil
}
func (e *Engine) scalar(ctx context.Context, inst model.Resource, db, sql string) (int64, error) {
	var value string
	err := e.sql(ctx, inst, db, strings.NewReader(sql), func(s string) { value += s + "\n" })
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(value), 10, 64)
}
func (e *Engine) databaseVacant(ctx context.Context, inst, r model.Resource) error {
	query := fmt.Sprintf("SELECT (SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name=%s)+(SELECT COUNT(*) FROM mysql.user WHERE User=%s);", sqlLiteral(r.Name), sqlLiteral(r.Config.String("username")))
	if inst.Config.String("software") == "postgresql" {
		query = fmt.Sprintf("SELECT (SELECT count(*) FROM pg_database WHERE datname=%s)+(SELECT count(*) FROM pg_roles WHERE rolname=%s);", pgLiteral(r.Name), pgLiteral(r.Config.String("username")))
	}
	count, err := e.scalar(ctx, inst, "", query)
	if err != nil {
		return err
	}
	if count != 0 {
		return errors.New("数据库或用户已存在，拒绝覆盖已有数据；请使用新的名称和用户名")
	}
	return nil
}
func (e *Engine) tableCounts(ctx context.Context, inst, db model.Resource) (map[string]int64, error) {
	query := fmt.Sprintf("SELECT COALESCE(JSON_ARRAYAGG(TABLE_NAME),JSON_ARRAY()) FROM information_schema.tables WHERE table_schema=%s AND TABLE_TYPE='BASE TABLE';", sqlLiteral(db.Name))
	if inst.Config.String("software") == "postgresql" {
		query = "SELECT COALESCE(json_agg(tablename),'[]'::json) FROM pg_tables WHERE schemaname='public';"
	}
	var result string
	if err := e.sql(ctx, inst, db.Name, strings.NewReader(query), func(s string) { result += s }); err != nil {
		return nil, err
	}
	var tables []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(result)), &tables); err != nil {
		return nil, err
	}
	if len(tables) > 1000 {
		return nil, errors.New("首版迁移验证最多支持 1000 张表")
	}
	counts := map[string]int64{}
	for _, table := range tables {
		quoted := "`" + strings.ReplaceAll(table, "`", "``") + "`"
		if inst.Config.String("software") == "postgresql" {
			quoted = "public.\"" + strings.ReplaceAll(table, "\"", "\"\"") + "\""
		}
		count, err := e.scalar(ctx, inst, db.Name, "SELECT count(*) FROM "+quoted+";")
		if err != nil {
			return nil, err
		}
		counts[table] = count
	}
	return counts, nil
}
func (e *Engine) migrateDatabase(ctx context.Context, op model.Operation, sourceInst model.Resource, log func(string)) (model.Config, error) {
	source, err := e.load(op.Resource.ID)
	if err != nil {
		return nil, err
	}
	if !op.Params.Bool("maintenanceConfirmed") {
		return nil, errors.New("迁移前请停止应用写入，并明确确认维护窗口")
	}
	targetInst, err := e.load(op.Params.String("targetInstanceId"))
	if err != nil {
		return nil, err
	}
	if targetInst.ID == sourceInst.ID || targetInst.Kind != "instance" || targetInst.Config.String("software") != sourceInst.Config.String("software") {
		return nil, errors.New("目标须为同一数据库引擎的另一个实例")
	}
	fromMajor, _ := strconv.Atoi(strings.Split(sourceInst.Config.String("version"), ".")[0])
	toMajor, _ := strconv.Atoi(strings.Split(targetInst.Config.String("version"), ".")[0])
	if toMajor < fromMajor {
		return nil, errors.New("禁止将数据库迁移到较低的大版本")
	}

	if sourceInst.Config.String("software") == "mysql" && (fromMajor != 8 || toMajor != 8 || !strings.HasPrefix(sourceInst.Config.String("version"), "8.4.") || !strings.HasPrefix(targetInst.Config.String("version"), "8.4.")) {
		return nil, errors.New("首版 MySQL 仅验证 8.4 LTS 内迁移，其他升级路径需另行验证")
	}
	if sourceInst.Config.String("software") == "postgresql" {
		count, err := e.scalar(ctx, sourceInst, source.Name, "SELECT count(*) FROM pg_tables WHERE schemaname NOT IN ('public','pg_catalog','information_schema');")
		if err != nil {
			return nil, err
		}
		if count != 0 {
			return nil, errors.New("首版自动迁移仅验证 public schema；其他 schema 请手动迁移并验证")
		}
	}
	before, err := e.tableCounts(ctx, sourceInst, source)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(filepath.Join(e.Root, "backups"), "migration-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	dump := filepath.Join(dir, "database.tar.gz")
	if err = e.dumpDatabase(ctx, sourceInst, source, dump, log); err != nil {
		return nil, err
	}
	target := source
	target.ID = source.ID + "-migration"
	target.Config = mapClone(source.Config)
	target.Config["instanceId"] = targetInst.ID
	target.Config["allowRemote"] = false
	target.Config["source"] = "127.0.0.1"
	log("在目标实例新建数据库；源数据库保持运行且不删除")
	created, err := e.database(ctx, model.Operation{Name: "database.create", Resource: target, Params: model.Config{}}, log)
	_ = created
	if err != nil {
		return nil, err
	}
	if targetInst.Config.String("software") == "mysql" {
		f, err := os.Open(dump)
		if err != nil {
			return nil, err
		}
		err = e.sql(ctx, targetInst, target.Name, f, log)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("目标导入失败，源库保持不变：%w", err)
		}
	} else {
		stage := filepath.Join(dir, "stage")
		if err = os.MkdirAll(stage, 0700); err != nil {
			return nil, err
		}
		if err = extractArchive(ctx, dump, "database.tar.gz", stage, false); err != nil {
			return nil, err
		}
		if err = e.pgRestore(ctx, targetInst, target, stage+"/database.dump", log); err != nil {
			return nil, fmt.Errorf("目标导入失败，源库保持不变：%w", err)
		}
	}
	after, err := e.tableCounts(ctx, targetInst, target)
	if err != nil {
		return nil, err
	}
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		return nil, errors.New("迁移验证未通过：表或行数不一致；源实例保留，请检查目标数据后重试")
	}
	snapshot := source
	snapshot.ID = source.ID + "-previous-" + security.ID()[:8]
	snapshot.Kind = "migration-source"
	if err = e.save(snapshot); err != nil {
		return nil, err
	}
	target.ID = source.ID
	target.Config["previousInstanceId"] = sourceInst.ID
	target.Config["migrationVerified"] = true
	target.Config["migrationSnapshotId"] = snapshot.ID
	if err = e.save(target); err != nil {
		return nil, err
	}
	_ = e.forget(source.ID + "-migration")
	log("迁移验证通过，已切换面板关联；请更新应用连接端口。原实例及源库保留。")
	return model.Config{"instanceId": targetInst.ID, "previousInstanceId": sourceInst.ID, "source": "127.0.0.1", "allowRemote": false, "migrationVerified": true, "migrationSnapshotId": snapshot.ID}, nil
}
func (e *Engine) pgRestore(ctx context.Context, inst, target model.Resource, path string, log func(string)) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	args := []string{"-h", "127.0.0.1", "-p", strconv.Itoa(inst.Config.Int("port")), "-U", "postgres", "--no-owner", "--no-acl", "--exit-on-error", "--role=" + target.Config.String("username"), "-d", target.Name}
	env := []string{"PGPASSWORD=" + inst.Config.String("password")}
	if inst.Config.String("mode") == "docker" {
		all := append([]string{"exec", "-i", "-e", env[0], "opsview-" + inst.ID, "pg_restore"}, args...)
		return run(ctx, log, "", nil, file, "docker", all...)
	}
	return run(ctx, log, "", env, file, filepath.Join(e.Root, "instances", inst.ID, "runtime/bin/pg_restore"), args...)
}
