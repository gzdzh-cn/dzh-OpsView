package executor

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func dbQuote(s string, pg bool) string {
	if pg {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}
func dbJSONRows(e *Engine, ctx context.Context, inst model.Resource, db, q string) ([]model.Config, error) {
	var out strings.Builder
	err := e.sql(ctx, inst, db, strings.NewReader(q), func(s string) {
		if out.Len() < 8<<20 {
			out.WriteString(s)
			out.WriteByte('\n')
		}
	})
	if err != nil {
		return nil, errors.New("数据库查询失败，请检查实例连接与账号权限")
	}
	if out.Len() >= 8<<20 {
		return nil, errors.New("查询结果过大，请缩小范围")
	}
	rows := []model.Config{}
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row model.Config
		if json.Unmarshal([]byte(line), &row) != nil {
			return nil, errors.New("数据库返回格式无效")
		}
		rows = append(rows, row)
	}
	return rows, nil
}
func (e *Engine) databaseInventory(ctx context.Context, i model.Resource) ([]model.Config, error) {
	q := `SELECT JSON_OBJECT('name',s.SCHEMA_NAME,'encoding',s.DEFAULT_CHARACTER_SET_NAME,'collation',s.DEFAULT_COLLATION_NAME,'username',COALESCE(d.User,''),'source',COALESCE(d.Host,'')) FROM information_schema.SCHEMATA s LEFT JOIN mysql.db d ON d.Db=s.SCHEMA_NAME WHERE s.SCHEMA_NAME NOT IN ('mysql','sys','information_schema','performance_schema');`
	if i.Config.String("software") == "postgresql" {
		q = `SELECT row_to_json(x) FROM (SELECT d.datname AS name, pg_get_userbyid(d.datdba) AS username, pg_encoding_to_char(d.encoding) AS encoding, d.datcollate AS collation FROM pg_database d WHERE NOT d.datistemplate AND d.datname!='postgres') x;`
	}
	return dbJSONRows(e, ctx, i, "", q)
}
func (e *Engine) databaseTables(ctx context.Context, i, r model.Resource) ([]model.Config, error) {
	q := `SELECT JSON_OBJECT('name',TABLE_NAME,'schema',TABLE_SCHEMA,'engine',ENGINE,'encoding',TABLE_COLLATION,'rows',TABLE_ROWS,'bytes',DATA_LENGTH+INDEX_LENGTH,'note',TABLE_COMMENT) FROM information_schema.TABLES WHERE TABLE_TYPE='BASE TABLE' AND TABLE_SCHEMA=` + sqlLiteral(r.Name) + `;`
	if i.Config.String("software") == "postgresql" {
		q = `SELECT row_to_json(x) FROM (SELECT c.relname AS name,n.nspname AS schema,c.reltuples::bigint AS rows,pg_total_relation_size(c.oid) AS bytes,COALESCE(obj_description(c.oid),'') AS note,COALESCE((SELECT json_agg(indexdef) FROM pg_indexes WHERE schemaname=n.nspname AND tablename=c.relname),'[]') AS indexes FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relkind IN ('r','p') AND n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg_toast%') x;`
	}
	return dbJSONRows(e, ctx, i, r.Name, q)
}
func (e *Engine) dbRun(ctx context.Context, i model.Resource, db, q string) error {
	if err := e.sql(ctx, i, db, strings.NewReader(q), func(string) {}); err != nil {
		return errors.New("数据库变更失败，请检查连接、权限及实际状态；执行内容已隐藏")
	}
	return nil
}
func dbResult(rs ...model.Resource) model.Config { return model.Config{"resources": rs} }
func validDBPassword(s string) bool {
	return len(s) >= 12 && len(s) <= 128 && !strings.ContainsAny(s, "\x00\n\r")
}
func (e *Engine) databaseAdmin(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, err := e.load(op.Resource.ID)
	if err != nil {
		return nil, err
	}
	i := r
	if r.Kind == "database" {
		i, err = e.load(r.Config.String("instanceId"))
		if err != nil {
			return nil, err
		}
	}
	sw := i.Config.String("software")
	if i.Kind != "instance" || (sw != "mysql" && sw != "postgresql") {
		return nil, errors.New("请选择本机受管数据库实例")
	}
	pg := sw == "postgresql"
	p := op.Params
	a := strings.TrimPrefix(op.Name, "dbadmin.")
	if r.Kind == "database" && r.Status == "recycled" && a != "restore" && a != "purge" {
		return nil, errors.New("数据库已回收")
	}
	switch a {
	case "reconcile":
		rows, er := e.databaseInventory(ctx, i)
		if er != nil {
			return nil, er
		}
		present := map[string]bool{}
		for _, row := range rows {
			present[row.String("name")] = true
		}
		changed := []model.Resource{}
		deleted := []string{}
		for _, db := range e.states() {
			if db.Kind == "database-tombstone" && db.Config.String("sourceInstanceId") == i.ID {
				backupID := "recycle-" + db.ID
				if err := os.RemoveAll(filepath.Join(e.Root, "backups", backupID)); err != nil {
					return nil, err
				}
				_ = e.forget(backupID)
				deleted = append(deleted, db.ID, backupID)
				continue
			}
			if db.Kind != "database" || db.Config.String("instanceId") != i.ID {
				continue
			}
			if db.Config.String("recyclePhase") == "prepared" {
				if present[db.Name] {
					delete(db.Config, "recyclePhase")
				} else {
					db.Status = "recycled"
					db.Config["recyclePhase"] = "deleted"
				}
				if er = e.save(db); er != nil {
					return nil, er
				}
			}
			if db.Status != "recycled" {
				for _, actual := range rows {
					if actual.String("name") != db.Name {
						continue
					}
					db.Config["encoding"] = actual.String("encoding")
					db.Config["collation"] = actual.String("collation")
					if pg {
						db.Config["owner"] = actual.String("username")
					}
					if er = e.save(db); er != nil {
						return nil, er
					}
					break
				}
			}
			changed = append(changed, db)
			if b, er := e.load(db.Config.String("recycleBackupId")); er == nil {
				changed = append(changed, b)
			}
		}
		return model.Config{"resources": changed, "deletedIds": deleted}, nil
	case "capabilities":
		if err := e.dbRun(ctx, i, "", "SELECT 1;"); err != nil {
			return nil, err
		}
		return model.Config{"engine": sw, "version": i.Config.String("version"), "connected": true, "maintenance": map[bool][]string{true: {"analyze", "vacuum"}, false: {"check", "optimize", "repair-supported-engines"}}[pg], "restoreBranch": databaseBranch(i), "migrationVerified": (pg || strings.HasPrefix(i.Config.String("version"), "8.4."))}, nil
	case "inventory":
		rows, err := e.databaseInventory(ctx, i)
		return model.Config{"rows": rows}, err
	case "adopt":
		if r.Kind != "instance" {
			return nil, errors.New("请选择实例")
		}
		rows, err := e.databaseInventory(ctx, i)
		if err != nil {
			return nil, err
		}
		for _, v := range rows {
			if v.String("name") != p.String("name") || v.String("username") != p.String("username") || (!pg && v.String("source") != p.String("source")) {
				continue
			}
			for _, known := range e.states() {
				if known.Kind == "database" && known.Status != "recycled" && known.Name == v.String("name") && known.Config.String("instanceId") == i.ID {
					return dbResult(known), nil
				}
			}
			if v.String("username") == "" {
				return nil, errors.New("未发现库级账号关系，需先在数据库中配置专用账号")
			}
			db := model.Resource{ID: security.ID(), Kind: "database", Name: v.String("name"), Status: "ready", Config: model.Config{"instanceId": i.ID, "engine": sw, "username": v.String("username"), "owner": v.String("username"), "source": v.String("source"), "encoding": v.String("encoding"), "collation": v.String("collation"), "imported": true, "password": "", "managedAccount": false}}
			if db.Config.String("source") == "" {
				db.Config["source"] = "127.0.0.1"
			}
			if err = e.save(db); err != nil {
				return nil, err
			}
			return dbResult(db), nil
		}
		return nil, errors.New("同步预览已变化，请重新扫描")
	case "password":
		password := p.String("password")
		if !validDBPassword(password) {
			return nil, errors.New("密码需为 12–128 字节且不包含换行")
		}
		user := r.Config.String("username")
		host := r.Config.String("source")
		if r.Kind == "instance" {
			user = "root"
			host = "localhost"
			if !pg {
				accounts, er := dbJSONRows(e, ctx, i, "", `SELECT JSON_OBJECT('account',CURRENT_USER());`)
				if er != nil || len(accounts) != 1 {
					return nil, errors.New("无法确定当前管理账号")
				}
				_, host, _ = strings.Cut(accounts[0].String("account"), "@")
			}
			if pg {
				user = "postgres"
			}
		}
		q := "ALTER USER " + sqlLiteral(user) + "@" + sqlLiteral(mysqlAccountHost(host)) + " IDENTIFIED BY " + sqlLiteral(password) + ";"
		if pg {
			q = "ALTER ROLE " + dbQuote(user, true) + " PASSWORD " + pgLiteral(password) + ";"
		}
		if err = e.dbRun(ctx, i, "", q); err != nil {
			return nil, err
		}
		changed := []model.Resource{}
		if r.Kind == "instance" {
			i.Config["password"] = password
			if err = e.save(i); err != nil {
				return nil, errors.New("运行密码已修改，执行器凭据保存失败，请本机恢复")
			}
			changed = append(changed, i)
		}
		for _, db := range e.states() {
			if db.Kind == "database" && db.Config.String("instanceId") == i.ID && db.Config.String("username") == user && (pg || db.Config.String("source") == host) {
				db.Config["password"] = password
				if err = e.save(db); err != nil {
					return nil, err
				}
				changed = append(changed, db)
			}
		}
		// Keep the new credential even if the post-change probe fails; report this in metadata.
		probe := e.dbRun(ctx, i, "", "SELECT 1;")
		if probe != nil && r.Kind == "instance" {
			changed[0].Config["credentialHealth"] = "密码已修改，连接验证失败"
			changed[0].Status = "error"
			log("管理密码已修改且已保存新凭据，但连接验证失败，请检查实例；不要继续使用旧密码")
			_ = e.save(changed[0])
		}
		return model.Config{"resources": changed}, nil
	case "tables":
		if r.Kind != "database" {
			return nil, errors.New("请选择数据库")
		}
		rows, err := e.databaseTables(ctx, i, r)
		return model.Config{"rows": rows}, err
	case "structure", "maintain":
		if r.Kind != "database" {
			return nil, errors.New("请选择数据库")
		}
		rows, err := e.databaseTables(ctx, i, r)
		if err != nil {
			return nil, err
		}
		var table model.Config
		for _, t := range rows {
			if t.String("name") == p.String("table") && t.String("schema") == p.String("schema") {
				table = t
				break
			}
		}
		if table == nil {
			return nil, errors.New("数据表不存在")
		}
		target := dbQuote(table.String("name"), pg)
		if pg {
			target = dbQuote(table.String("schema"), true) + "." + target
		}
		if a == "structure" {
			return e.databaseStructure(ctx, i, r, table)
		}

		if !p.Bool("confirmed") {
			return nil, errors.New("请确认维护范围")
		}
		command := p.String("operation")
		allowed := map[string]string{"check": "CHECK TABLE ", "optimize": "OPTIMIZE TABLE ", "repair": "REPAIR TABLE "}
		if pg {
			allowed = map[string]string{"analyze": "ANALYZE ", "vacuum": "VACUUM "}
		}
		prefix, ok := allowed[command]
		if !ok {
			return nil, errors.New("不支持的维护操作")
		}
		if command == "repair" && table.String("engine") != "MyISAM" && table.String("engine") != "ARCHIVE" && table.String("engine") != "CSV" {
			return nil, errors.New("此存储引擎不支持 REPAIR TABLE")
		}
		log("执行已确认的数据表维护")
		if pg {
			return model.Config{}, e.dbRun(ctx, i, r.Name, prefix+target+";")
		}
		failed := false
		err = e.sql(ctx, i, r.Name, strings.NewReader(prefix+target+";"), func(line string) {
			columns := strings.Split(line, "\t")
			if len(columns) >= 4 && (columns[2] == "error" || columns[2] == "status" && columns[3] != "OK") {
				failed = true
			}
		})
		if err != nil || failed {
			return nil, errors.New("表维护未成功，请通过实例日志核对表状态")
		}
		return model.Config{}, nil
	case "permissions":
		if r.Kind != "database" {
			return nil, errors.New("请选择数据库")
		}
		if pg {
			rows, er := dbJSONRows(e, ctx, i, r.Name, `SELECT row_to_json(x) FROM (SELECT grantee,table_schema,table_name,privilege_type FROM information_schema.role_table_grants WHERE grantee=`+pgLiteral(r.Config.String("username"))+`) x;`)
			return model.Config{"rows": rows, "owner": r.Config.String("owner"), "source": r.Config.String("source")}, er
		}
		var text strings.Builder
		err = e.sql(ctx, i, "", strings.NewReader("SHOW GRANTS FOR "+sqlLiteral(r.Config.String("username"))+"@"+sqlLiteral(mysqlAccountHost(r.Config.String("source")))+";"), func(s string) { text.WriteString(s + "\n") })
		if err != nil {
			return nil, errors.New("读取授权失败")
		}
		return model.Config{"content": text.String(), "source": r.Config.String("source")}, nil
	case "access":
		return e.databaseAccessChange(ctx, i, r, p, log)
	case "grant":
		return e.databaseGrant(ctx, i, r, p)
	case "recycle":
		return e.recycleDatabase(ctx, i, r, p, log)
	case "restore":
		return e.restoreRecycledDatabase(ctx, i, r, p, log)
	case "purge":
		if r.Status != "recycled" || !p.Bool("confirmed") {
			return nil, errors.New("永久删除需要确认已回收记录")
		}
		b, err := e.load(r.Config.String("recycleBackupId"))
		if err != nil {
			return nil, err
		}
		if b.Config.String("targetId") != r.ID || !b.Config.Bool("recycleOnly") {
			return nil, errors.New("非回收站专属备份")
		}
		r.Kind = "database-tombstone"
		r.Status = "purged"
		r.Config = model.Config{"sourceInstanceId": i.ID}
		if err = e.save(r); err != nil {
			return nil, err
		}
		if err = os.RemoveAll(filepath.Join(e.Root, "backups", b.ID)); err != nil {
			return nil, err
		}
		if err = e.forget(b.ID); err != nil {
			return nil, err
		}
		// Keep a minimal encrypted tombstone so a crash before panel metadata
		// commit can be reconciled without repeating any database deletion.
		return model.Config{"deletedIds": []string{r.ID, b.ID}}, nil
	}
	return nil, errors.New("数据库管理操作不存在")
}

func (e *Engine) recycleDatabase(ctx context.Context, i, r model.Resource, p model.Config, log func(string)) (model.Config, error) {
	if r.Kind != "database" || r.Status == "recycled" || !p.Bool("maintenanceConfirmed") || !p.Bool("confirmDataDeletion") {
		return nil, errors.New("请确认已停止业务写入，并确认删除数据库")
	}
	// The encrypted prepared record is durable before the first destructive statement.
	b := model.Resource{ID: "recycle-" + r.ID, Kind: "backup", Name: "recycle-" + r.ID, Config: model.Config{"targetId": r.ID, "destination": "local", "retention": 1, "recycleOnly": true}}
	if r.Config.String("recyclePhase") == "prepared" {
		return nil, errors.New("上次回收中断；请检查实际数据库状态，禁止重复执行删除")
	}
	log("1/3 创建回收站专属加密备份")
	counts, err := e.databaseRowCounts(ctx, i, r)
	if err != nil {
		return nil, err
	}
	b.Config["expectedCounts"] = counts
	if i.Config.String("software") == "postgresql" {
		grants, er := dbJSONRows(e, ctx, i, r.Name, `SELECT row_to_json(x) FROM (SELECT grantee,table_schema,table_name,privilege_type FROM information_schema.role_table_grants WHERE table_schema NOT IN ('pg_catalog','information_schema')) x;`)
		if er != nil {
			return nil, er
		}
		r.Config["recycledGrants"] = grants
	} else {
		var grants strings.Builder
		er := e.sql(ctx, i, "", strings.NewReader("SHOW GRANTS FOR "+sqlLiteral(r.Config.String("username"))+"@"+sqlLiteral(mysqlAccountHost(r.Config.String("source")))+";"), func(line string) { grants.WriteString(line + "\n") })
		if er != nil {
			return nil, errors.New("保存原授权失败，未执行删除")
		}
		r.Config["recycledGrants"] = grants.String()
	}
	result, err := e.backupUnlocked(ctx, model.Operation{Name: "backup.backup", Resource: b, Params: model.Config{}}, log)
	if err != nil {
		return nil, err
	}
	_ = result
	b, err = e.load(b.ID)
	if err != nil {
		return nil, err
	}
	artifact := filepath.Join(e.Root, "backups", b.ID, b.Config.String("artifact"))
	sum, err := hashFile(artifact)
	if err != nil || sum != b.Config.String("sha256") {
		return nil, errors.New("回收备份校验失败")
	}
	r.Config["recycleBackupId"] = b.ID
	r.Config["recyclePhase"] = "prepared"
	r.Config["expiresAt"] = time.Now().UTC().Add(30 * 24 * time.Hour).Format(time.RFC3339)
	if err = e.save(r); err != nil {
		return nil, err
	}
	log("2/3 删除已备份数据库，保留共享账号")
	if err = e.dbRun(ctx, i, "", "DROP DATABASE "+dbQuote(r.Name, i.Config.String("software") == "postgresql")+";"); err != nil {
		return nil, err
	}
	// Retaining accounts is safer than guessing external role dependencies. Dedicated
	// accounts remain for explicit review; no DROP USER is issued during recycling.
	r.Status = "recycled"
	r.Config["recyclePhase"] = "deleted"
	if err = e.save(r); err != nil {
		return nil, err
	}
	log("3/3 已进入回收站，保留 30 天；账号保留供核对")
	return dbResult(r, b), nil
}
func (e *Engine) restoreRecycledDatabase(ctx context.Context, i, r model.Resource, p model.Config, log func(string)) (model.Config, error) {
	if r.Status != "recycled" {
		return nil, errors.New("仅恢复已回收数据库")
	}
	b, err := e.load(r.Config.String("recycleBackupId"))
	if err != nil {
		return nil, err
	}

	result, err := e.restore(ctx, model.Operation{Name: "backup.restore", Resource: b, Params: p}, r, log)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(result["linkedDatabase"])
	var restored model.Resource
	if json.Unmarshal(raw, &restored) != nil {
		return nil, errors.New("恢复记录无效")
	}
	restored.Status = "verifying"
	if err = e.save(restored); err != nil {
		return nil, err
	}
	expected := map[string]int64{}
	raw, _ = json.Marshal(b.Config["expectedCounts"])
	_ = json.Unmarshal(raw, &expected)
	target, er := e.load(restored.Config.String("instanceId"))
	if er != nil {
		return nil, er
	}
	actual, er := e.databaseRowCounts(ctx, target, restored)
	if er != nil {
		return nil, er
	}
	want, _ := json.Marshal(expected)
	got, _ := json.Marshal(actual)
	if string(want) != string(got) {
		restored.Status = "error"
		restored.Config["verificationError"] = "恢复后的表和行数校验不一致"
		_ = e.save(restored)
		return nil, errors.New("恢复后的表和行数校验不一致，新库保留供检查，源备份未删除")
	}
	restored.Status = "ready"
	delete(restored.Config, "recyclePhase")
	delete(restored.Config, "recycleBackupId")
	delete(restored.Config, "expiresAt")
	if err = e.save(restored); err != nil {
		return nil, err
	}
	return dbResult(restored), nil
}

func (e *Engine) databaseGrant(ctx context.Context, i, r model.Resource, p model.Config) (model.Config, error) {
	if r.Kind != "database" {
		return nil, errors.New("请选择数据库")
	}
	level := p.String("level")
	grants := map[string]string{"readonly": "SELECT", "readwrite": "SELECT, INSERT, UPDATE, DELETE", "admin": "ALL PRIVILEGES"}
	grant, ok := grants[level]
	if !ok {
		return nil, errors.New("权限类型无效")
	}
	source := p.String("source")
	if source == "" {
		source = r.Config.String("source")
	}
	if source != r.Config.String("source") {
		return nil, errors.New("请通过访问来源设置调整连接地址；权限变更不修改账号身份")
	}
	user := r.Config.String("username")
	if name := p.String("username"); name != "" {
		if !logicSQLName(name) {
			return nil, errors.New("账号名称格式无效")
		}
		user = name
	}
	pg := i.Config.String("software") == "postgresql"
	q := ""
	rollbackSQL := ""
	createdAccount := false
	if !pg {
		var grants strings.Builder
		if err := e.sql(ctx, i, "", strings.NewReader("SHOW GRANTS FOR "+sqlLiteral(user)+"@"+sqlLiteral(mysqlAccountHost(source))+";"), func(line string) { grants.WriteString(line + ";\n") }); err != nil {
			return nil, errors.New("无法读取原授权，拒绝修改")
		}
		rollbackSQL = grants.String()
	}
	if pg {
		owner := r.Config.String("owner")
		if owner == "" {
			owner = user
		}
		if owner == user && level != "admin" {
			return nil, errors.New("所有者拥有固有权限；请使用普通授权账号设置只读或读写权限")
		}
		schemas, err := dbJSONRows(e, ctx, i, r.Name, `SELECT row_to_json(x) FROM (SELECT nspname AS name FROM pg_namespace WHERE nspname NOT IN ('pg_catalog','information_schema') AND nspname NOT LIKE 'pg_%') x;`)
		if err != nil {
			return nil, err
		}
		u := dbQuote(user, true)
		q = "BEGIN; "
		if user != r.Config.String("username") {
			exists, er := e.scalar(ctx, i, "", "SELECT count(*) FROM pg_roles WHERE rolname="+pgLiteral(user)+";")
			if er != nil {
				return nil, er
			}
			if exists == 0 {
				if !validDBPassword(p.String("password")) {
					return nil, errors.New("创建普通授权账号需要 12–128 字节密码")
				}
				q += "CREATE ROLE " + u + " LOGIN PASSWORD " + pgLiteral(p.String("password")) + ";"
				createdAccount = true
			}
		}
		q += "GRANT CONNECT ON DATABASE " + dbQuote(r.Name, true) + " TO " + u + ";"
		for _, s := range schemas {
			schema := dbQuote(s.String("name"), true)
			q += "ALTER DEFAULT PRIVILEGES FOR ROLE " + dbQuote(owner, true) + " IN SCHEMA " + schema + " REVOKE ALL ON SEQUENCES FROM " + u + "; "
			q += "ALTER DEFAULT PRIVILEGES FOR ROLE " + dbQuote(owner, true) + " IN SCHEMA " + schema + " REVOKE ALL ON TABLES FROM " + u + "; REVOKE ALL ON ALL SEQUENCES IN SCHEMA " + schema + " FROM " + u + "; REVOKE CREATE ON SCHEMA " + schema + " FROM " + u + "; "
			if level != "readonly" {
				q += "GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA " + schema + " TO " + u + "; ALTER DEFAULT PRIVILEGES FOR ROLE " + dbQuote(owner, true) + " IN SCHEMA " + schema + " GRANT USAGE, SELECT ON SEQUENCES TO " + u + ";"
			}
			if level == "admin" {
				q += "GRANT CREATE ON SCHEMA " + schema + " TO " + u + ";"
			}
			q += "REVOKE ALL ON ALL TABLES IN SCHEMA " + schema + " FROM " + u + "; GRANT USAGE ON SCHEMA " + schema + " TO " + u + "; GRANT " + grant + " ON ALL TABLES IN SCHEMA " + schema + " TO " + u + "; ALTER DEFAULT PRIVILEGES FOR ROLE " + dbQuote(owner, true) + " IN SCHEMA " + schema + " GRANT " + grant + " ON TABLES TO " + u + ";"
		}
		q += "COMMIT;"
	} else {
		u := sqlLiteral(user) + "@" + sqlLiteral(mysqlAccountHost(source))
		q = "REVOKE ALL PRIVILEGES ON " + dbQuote(r.Name, false) + ".* FROM " + u + "; GRANT " + grant + " ON " + dbQuote(r.Name, false) + ".* TO " + u + ";"
	}
	original := r
	original.Config = mapClone(r.Config)
	preparedPG := false
	committed := false
	if pg {
		candidate := r
		candidate.Config = mapClone(r.Config)
		candidate.Config["username"] = user
		if user != r.Config.String("username") {
			candidate.Config["password"] = ""
			if createdAccount {
				candidate.Config["password"] = p.String("password")
			}
		}
		if err := e.save(candidate); err != nil {
			return nil, err
		}
		preparedPG = true
		defer func() {
			if preparedPG && !committed {
				_ = e.save(original)
				recovery, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				_ = e.configurePGAccess(recovery, i, func(string) {})
			}
		}()
		if err := e.configurePGAccess(ctx, i, func(string) {}); err != nil {
			return nil, err
		}
	}
	if err := e.dbRun(ctx, i, r.Name, q); err != nil {
		if rollbackSQL != "" {
			recovery, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if er := e.dbRun(recovery, i, r.Name, rollbackSQL); er != nil {
				return nil, errors.New("授权失败且旧授权恢复失败，请核对实际权限")
			}
		}
		return nil, err
	}
	if user != r.Config.String("username") {
		r.Config["username"] = user
		r.Config["password"] = ""
		if createdAccount {
			r.Config["password"] = p.String("password")
		}
		r.Config["managedAccount"] = createdAccount
	}
	r.Config["privilege"] = level
	if err := e.save(r); err != nil {
		return nil, err
	}
	committed = true
	return dbResult(r), nil
}

// Raw integer values in JSON may come from either decoded floats or query text.
func dbInt(c model.Config, k string) int {
	if s := c.String(k); s != "" {
		n, _ := strconv.Atoi(s)
		return n
	}
	return c.Int(k)
}

func (e *Engine) databaseAccessChange(ctx context.Context, i, r model.Resource, p model.Config, log func(string)) (model.Config, error) {
	if r.Kind != "database" {
		return nil, errors.New("请选择数据库")
	}
	source := p.String("source")
	if net.ParseIP(source) == nil {
		if _, _, err := net.ParseCIDR(source); err != nil {
			return nil, errors.New("来源必须为 IP/CIDR")
		}
	}
	pg := i.Config.String("software") == "postgresql"
	if !pg && strings.Contains(source, ":") && strings.Contains(source, "/") {
		return nil, errors.New("MySQL 的 IPv6 来源须为单个地址")
	}
	remote := !isLoopbackSource(source)
	if remote && !p.Bool("allowRemote") {
		return nil, errors.New("请确认远程访问")
	}
	next := r
	next.Config = mapClone(r.Config)
	next.Config["source"] = source
	next.Config["allowRemote"] = remote
	if remote {
		if err := e.firewallCompatible(ctx, databaseFirewall(next, i), log); err != nil {
			return nil, err
		}
	}
	changed := []model.Resource{}
	for _, db := range e.states() {
		if db.Kind == "database" && db.Status != "recycled" && db.Config.String("instanceId") == i.ID && db.Config.String("username") == r.Config.String("username") && (pg || db.Config.String("source") == r.Config.String("source")) {
			changed = append(changed, db)
		}
	}
	if len(changed) > 1 && !p.Bool("sharedConfirmed") {
		return nil, errors.New("此账号被多个数据库共享，请确认所有关联数据库的来源一起修改")
	}
	rename := func(from, to string) error {
		return e.dbRun(ctx, i, "", "RENAME USER "+sqlLiteral(r.Config.String("username"))+"@"+sqlLiteral(mysqlAccountHost(from))+" TO "+sqlLiteral(r.Config.String("username"))+"@"+sqlLiteral(mysqlAccountHost(to))+";")
	}
	if !pg && source != r.Config.String("source") {
		if err := rename(r.Config.String("source"), source); err != nil {
			return nil, err
		}
	}
	rollback := func(cause error) (model.Config, error) {
		recovery, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		ctx = recovery
		failed := false
		for _, db := range changed {
			if e.save(db) != nil {
				failed = true
			}
		}
		if pg {
			if e.configurePGAccess(recovery, i, func(string) {}) != nil {
				failed = true
			}
		} else if source != r.Config.String("source") {
			if rename(source, r.Config.String("source")) != nil {
				failed = true
			}
		}
		if actual, er := e.load(i.ID); er == nil && actual.Config.String("bind") != i.Config.String("bind") {
			bind := i.Config.String("bind")
			if bind == "" {
				bind = "127.0.0.1"
			}
			if e.setDatabaseBind(recovery, actual, bind, func(string) {}) != nil {
				failed = true
			}
		}
		if failed {
			return nil, errors.New("访问变更失败，部分回退未完成，请检查实际账号、监听地址和访问规则")
		}
		return nil, cause
	}

	updated := []model.Resource{}
	for _, db := range changed {
		db.Config = mapClone(db.Config)
		db.Config["source"] = source
		db.Config["allowRemote"] = remote
		if err := e.save(db); err != nil {
			return rollback(err)
		}
		updated = append(updated, db)
	}
	if pg {
		if err := e.configurePGAccess(ctx, i, log); err != nil {
			return rollback(err)
		}
	}
	if remote {
		if err := e.exposeDatabase(ctx, i, log); err != nil {
			return rollback(err)
		}
		fw := databaseFirewall(next, i)
		result, err := e.firewall(ctx, model.Operation{Name: "firewall.apply", Resource: fw, Params: model.Config{}}, log)
		if err != nil {
			return rollback(err)
		}
		for k, v := range result {
			fw.Config[k] = v
		}
		fw.Status = "ready"
		updated = append(updated, fw)
		log("访问来源已变更；请在系统安全页核对端口规则和云安全组")
	}
	return model.Config{"resources": updated}, nil
}

func (e *Engine) databaseRowCounts(ctx context.Context, i, r model.Resource) (map[string]int64, error) {
	tables, err := e.databaseTables(ctx, i, r)
	if err != nil {
		return nil, err
	}
	if len(tables) > 1000 {
		return nil, errors.New("恢复验证最多支持 1000 张表")
	}
	counts := map[string]int64{}
	pg := i.Config.String("software") == "postgresql"
	for _, table := range tables {
		name := dbQuote(table.String("name"), pg)
		key := table.String("name")
		if pg {
			name = dbQuote(table.String("schema"), true) + "." + name
			key = table.String("schema") + "." + key
		}
		n, er := e.scalar(ctx, i, r.Name, "SELECT count(*) FROM "+name+";")
		if er != nil {
			return nil, errors.New("无法验证数据库行数")
		}
		counts[key] = n
	}
	return counts, nil
}

// Structure export uses the matching instance utility, not a synthesized schema.
func (e *Engine) databaseStructure(ctx context.Context, i, db model.Resource, table model.Config) (model.Config, error) {
	binary := "mysqldump"
	args := []string{"--protocol=TCP", "-h127.0.0.1", "-P" + strconv.Itoa(i.Config.Int("port")), "-uroot", "--no-data", "--no-tablespaces", "--set-gtid-purged=OFF", "--", db.Name, table.String("name")}
	env := []string{"MYSQL_PWD=" + i.Config.String("password")}
	if i.Config.String("software") == "postgresql" {
		binary = "pg_dump"
		args = []string{"-h", "127.0.0.1", "-p", strconv.Itoa(i.Config.Int("port")), "-U", "postgres", "--schema-only", "--no-owner", "--no-privileges", "--table=" + dbQuote(table.String("schema"), true) + "." + dbQuote(table.String("name"), true), "--dbname=" + db.Name}
		env = []string{"PGPASSWORD=" + i.Config.String("password")}
	}
	path := filepath.Join(e.Root, "instances", i.ID, "runtime/bin", binary)
	if i.Config.String("mode") == "docker" {
		args = append([]string{"exec", "-e", strings.SplitN(env[0], "=", 2)[0], "opsview-" + i.ID, binary}, args...)
		path = "docker"
	}
	var out strings.Builder
	overflow := false
	err := run(ctx, func(line string) {
		if out.Len()+len(line) > 2<<20 {
			overflow = true
			return
		}
		out.WriteString(line + "\n")
	}, "", env, nil, path, args...)
	if err != nil || overflow {
		return nil, errors.New("表结构导出失败或超过 2 MiB，请使用数据库备份")
	}
	return model.Config{"content": out.String(), "indexes": table["indexes"]}, nil
}

func databaseBranch(i model.Resource) string {
	parts := strings.Split(i.Config.String("version"), ".")
	if i.Config.String("software") == "mysql" && len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return parts[0]
}
