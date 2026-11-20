package executor

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Disposable containers only. No discovery of or connection to existing services.
func liveDatabase(t *testing.T, software, image string, modes ...string) (*Engine, model.Resource) {
	t.Helper()
	if os.Getenv("OPSVIEW_DATABASE_INTEGRATION") != "1" {
		t.Skip("set OPSVIEW_DATABASE_INTEGRATION=1 for disposable Docker integration")
	}
	e, _, _ := softwareFixture(t)
	id := "dbtest-" + security.ID()
	r := model.Resource{ID: id, Kind: "instance", Name: id, Status: "ready", Config: model.Config{"software": software, "version": "8.4.8", "mode": "native", "catalogId": "test", "password": "isolated-test-password-2026"}}
	if len(modes) > 0 {
		r.Config["mode"] = modes[0]
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	r.Config["port"] = port
	name := "opsview-" + id
	base := filepath.Join(e.Root, "instances", id)
	_ = os.MkdirAll(base+"/runtime/bin", 0755)
	_ = os.MkdirAll(base+"/data/pgdata", 0755)
	_ = os.MkdirAll(e.Root+"/backups", 0700)
	// The container reads this actual directory; atomic file replacements remain
	// visible through the directory mount. No test touches pre-existing services.
	hba := []byte("# external-rule-preserved\nlocal all all trust\nhost all all 127.0.0.1/32 scram-sha-256\n")
	_ = os.WriteFile(base+"/data/pg_hba.conf", hba, 0644)
	_ = os.WriteFile(base+"/data/pgdata/pg_hba.conf", hba, 0644)
	hbaPath := "/opsview-managed/pg_hba.conf"
	if r.Config.String("mode") == "docker" {
		hbaPath = "/opsview-managed/pgdata/pg_hba.conf"
	}
	args := []string{"run", "-d", "--name", name, "-p", fmt.Sprintf("127.0.0.1:%d:%d", port, port)}
	if software == "mysql" {
		args = append(args, "-e", "MYSQL_ROOT_PASSWORD="+r.Config.String("password"), image, "--port="+strconv.Itoa(port))
	} else if software == "postgresql" {
		r.Config["version"] = "18.0"
		args = append(args, "-v", base+"/data:/opsview-managed", "-e", "POSTGRES_PASSWORD="+r.Config.String("password"), image, "-p", strconv.Itoa(port), "-c", "hba_file="+hbaPath)
	} else {
		r.Config["version"] = "7.4"
		args = append(args, image, "redis-server", "--port", strconv.Itoa(port), "--requirepass", r.Config.String("password"))
	}
	if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
		t.Fatalf("start isolated %s: %v %s", software, err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-fv", name).Run() })
	for _, bin := range []string{"mysql", "mysqldump", "psql", "pg_dump", "pg_dumpall", "pg_restore"} {
		script := "#!/bin/sh\nexec docker exec -i -e MYSQL_PWD -e PGPASSWORD " + name + " " + bin + " \"$@\"\n"
		if err := os.WriteFile(base+"/runtime/bin/"+bin, []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err = e.save(r); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(100 * time.Second)
	for time.Now().Before(deadline) {
		if software == "redis" {
			conn, err := e.redisConnect(context.Background(), r, 0)
			if err == nil {
				conn.Close()
				return e, r
			}
		} else if e.dbRun(context.Background(), r, "", "SELECT 1;") == nil {
			return e, r
		}
		time.Sleep(time.Second)
	}
	t.Fatal("database startup timed out")
	return nil, r
}
func TestLiveDatabaseManagement(t *testing.T) {
	for _, tc := range []struct{ sw, image, mode string }{{"mysql", "mysql:8.4.8", "native"}, {"postgresql", "postgres:18.0", "native"}, {"mysql", "mysql:8.4.8", "docker"}, {"postgresql", "postgres:18.0", "docker"}, {"mysql", "mysql:5.7", "docker"}} {
		t.Run(tc.sw+"/"+tc.image+"/"+tc.mode, func(t *testing.T) {
			e, i := liveDatabase(t, tc.sw, tc.image, tc.mode)
			i.Config["mode"] = tc.mode
			if strings.Contains(tc.image, "5.7") {
				i.Config["version"] = "5.7.44"
			}
			_ = e.save(i)
			ctx := context.Background()
			r := model.Resource{ID: "business-" + security.ID(), Kind: "database", Name: "business", Config: model.Config{"instanceId": i.ID, "username": "business", "password": "business-password-test", "source": "127.0.0.1"}}
			if _, err := e.database(ctx, model.Operation{Name: "database.create", Resource: r, Params: model.Config{}}, func(string) {}); err != nil {
				t.Fatal("create", err)
			}
			r, _ = e.load(r.ID)
			q := "CREATE TABLE items(id INT PRIMARY KEY, title VARCHAR(100)); INSERT INTO items VALUES(1,'test');"
			if err := e.dbRun(ctx, i, r.Name, q); err != nil {
				t.Fatal(err)
			}
			call := func(a string, p model.Config) model.Config {
				t.Helper()
				data, err := e.databaseAdmin(ctx, model.Operation{Name: "dbadmin." + a, Resource: r, Params: p}, func(string) {})
				if err != nil {
					t.Fatal(a, err)
				}
				return data
			}
			tables := call("tables", nil)
			raw, _ := json.Marshal(tables["rows"])
			if !strings.Contains(string(raw), "items") {
				t.Fatal("table missing")
			}
			schema := r.Name
			if tc.sw == "postgresql" {
				schema = "public"
			}
			ddl := call("structure", model.Config{"table": "items", "schema": schema})
			if !strings.Contains(ddl.String("content"), "CREATE TABLE") {
				t.Fatal("missing DDL")
			}
			maintain := "check"
			if tc.sw == "postgresql" {
				maintain = "analyze"
			}
			call("maintain", model.Config{"table": "items", "schema": schema, "operation": maintain, "confirmed": true})
			if _, err := e.databaseAdmin(ctx, model.Operation{Name: "dbadmin.maintain", Resource: r, Params: model.Config{"table": "items;DROP DATABASE business", "schema": schema, "operation": "check", "confirmed": true}}, func(string) {}); err == nil {
				t.Fatal("unknown table accepted")
			}
			if tc.sw == "postgresql" {
				call("grant", model.Config{"level": "readonly", "username": "reader", "password": "reader-password-test"})
				r, _ = e.load(r.ID)
			} else {
				call("grant", model.Config{"level": "readonly"})
			}
			r, _ = e.load(r.ID)
			user := r.Config.String("username")
			// Check the actual business login, not just the administrator's grants.
			queryAs := func(query string) error {
				bin := "mysql"
				args := []string{"--protocol=TCP", "-h127.0.0.1", "-P" + strconv.Itoa(i.Config.Int("port")), "-u" + user, r.Name, "-e", query}
				env := "MYSQL_PWD=" + r.Config.String("password")
				if tc.sw == "postgresql" {
					bin = "psql"
					env = "PGPASSWORD=" + r.Config.String("password")
					args = []string{"-h", "127.0.0.1", "-p", strconv.Itoa(i.Config.Int("port")), "-U", user, "-d", r.Name, "-v", "ON_ERROR_STOP=1", "-c", query}
				}
				command := exec.Command("docker", append([]string{"exec", "-e", strings.SplitN(env, "=", 2)[0], "opsview-" + i.ID, bin}, args...)...)
				command.Env = append(os.Environ(), env)
				return command.Run()
			}
			if err := queryAs("SELECT * FROM items;"); err != nil {
				t.Fatal("readonly SELECT", err)
			}
			if err := queryAs("INSERT INTO items VALUES(2,'denied');"); err == nil {
				t.Fatal("readonly INSERT allowed")
			}
			call("grant", model.Config{"level": "readwrite"})
			if err := queryAs("INSERT INTO items VALUES(2,'allowed');"); err != nil {
				t.Fatal("readwrite INSERT", err)
			}
			if tc.sw == "postgresql" {
				if err := e.dbRun(ctx, i, r.Name, `CREATE SCHEMA custom; CREATE TABLE custom.other(id integer); INSERT INTO custom.other VALUES(1);`); err != nil {
					t.Fatal(err)
				}
				call("structure", model.Config{"table": "other", "schema": "custom"})
				before, _ := os.ReadFile(filepath.Join(e.Root, "instances", i.ID, "data/pg_hba.conf"))
				if !strings.Contains(string(before), "external-rule-preserved") {
					t.Fatal("external hba lost")
				}
			}
			call("password", model.Config{"password": "updated-business-test-password"})
			call("permissions", nil)
			inventoryBefore, err := e.databaseInventory(ctx, i)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range inventoryBefore {
				if entry.String("name") == r.Name && entry.String("username") == r.Config.String("owner") {
					if result, err := e.databaseAdmin(ctx, model.Operation{Name: "dbadmin.adopt", Resource: i, Params: entry}, func(string) {}); err != nil || result == nil {
						t.Fatal("idempotent adoption", err)
					}
					break
				}
			}
			if tc.mode == "native" {
				bin := "mysqldump"
				if tc.sw == "postgresql" {
					bin = "pg_dump"
				}
				path := filepath.Join(e.Root, "instances", i.ID, "runtime/bin", bin)
				if err := os.Rename(path, path+".disabled"); err != nil {
					t.Fatal(err)
				}
				_, err := e.databaseAdmin(ctx, model.Operation{Name: "dbadmin.recycle", Resource: r, Params: model.Config{"maintenanceConfirmed": true, "confirmDataDeletion": true}}, func(string) {})
				if err == nil {
					t.Fatal("backup failure allowed deletion")
				}
				_ = os.Rename(path+".disabled", path)
				if n, err := e.scalar(ctx, i, r.Name, "SELECT count(*) FROM items;"); err != nil || n != 2 {
					t.Fatal("backup failure lost database")
				}
			}
			call("recycle", model.Config{"maintenanceConfirmed": true, "confirmDataDeletion": true})
			r, _ = e.load(r.ID)
			if r.Status != "recycled" {
				t.Fatal("not recycled")
			}
			inventory, err := e.databaseInventory(ctx, i)
			if err != nil {
				t.Fatal(err)
			}
			for _, db := range inventory {
				if db.String("name") == r.Name {
					t.Fatal("database still exists")
				}
			}
			restored := call("restore", model.Config{"databaseName": "business_restored"})
			raw, _ = json.Marshal(restored["resources"])
			var rs []model.Resource
			_ = json.Unmarshal(raw, &rs)
			if len(rs) != 1 {
				t.Fatal("restore metadata")
			}
			count, err := e.scalar(ctx, i, "business_restored", "SELECT COUNT(*) FROM items;")
			if err != nil || count != 2 {
				t.Fatal("restored contents", count, err)
			}
			call("purge", model.Config{"confirmed": true})
			if tombstone, err := e.load(r.ID); err != nil || tombstone.Status != "purged" || tombstone.Config.String("password") != "" {
				t.Fatal("invalid purge tombstone", err)
			}
		})
	}
}
func TestLiveRedisManagement(t *testing.T) {
	e, i := liveDatabase(t, "redis", "redis:7")
	ctx := context.Background()
	enc := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	call := func(a string, p model.Config) model.Config {
		t.Helper()
		out, err := e.redisData(ctx, model.Operation{Name: "redisdata." + a, Resource: i, Params: p})
		if err != nil {
			t.Fatal(a, err)
		}
		return out
	}
	for _, kind := range []string{"string", "hash", "list", "set", "zset"} {
		key := enc("test:" + kind)
		call("write", model.Config{"key": key, "operation": "set", "revision": "missing", "type": kind, "value": enc("\x00binary\xff"), "field": enc("field"), "score": "2"})
		read := call("read", model.Config{"key": key})
		if read.String("type") != kind {
			t.Fatal(read)
		}
		call("write", model.Config{"key": key, "operation": "ttl", "ttl": 60, "revision": read.String("revision")})
		read = call("read", model.Config{"key": key})
		call("write", model.Config{"key": key, "operation": "set", "revision": read.String("revision"), "type": kind, "value": enc("updated"), "field": enc("field"), "score": "3"})
		read = call("read", model.Config{"key": key})
		if ttl := redisNumber(read["ttlMillis"]); ttl <= 0 || ttl > 60000 {
			t.Fatal("ttl lost", read)
		}
		if _, err := e.redisData(ctx, model.Operation{Name: "redisdata.write", Resource: i, Params: model.Config{"key": key, "operation": "delete", "confirmed": true, "revision": "stale"}}); err == nil {
			t.Fatal("stale edit accepted")
		}
	}
	page := call("scan", model.Config{"cursor": "0", "pattern": "test:*"})
	raw, _ := json.Marshal(page)
	if !strings.Contains(string(raw), "test:string") {
		t.Fatal("scan missing")
	}
	read := call("read", model.Config{"key": enc("test:string")})
	var dump strings.Builder
	if err := e.redisDownload(ctx, i, model.Config{"key": enc("test:string")}, &dump); err != nil || dump.Len() == 0 {
		t.Fatal("download", err)
	}
	call("write", model.Config{"key": enc("test:string"), "operation": "delete", "confirmed": true, "revision": read.String("revision")})
}

func TestLiveRedisLargeValuesAndTTLConflict(t *testing.T) {
	e, i := liveDatabase(t, "redis", "redis:7")
	ctx := context.Background()
	c, err := e.redisConnect(ctx, i, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	key := base64.StdEncoding.EncodeToString([]byte("large"))
	data := make([]byte, 5<<20)
	_, _ = rand.Read(data)
	if _, err = c.command("SET", "large", string(data)); err != nil {
		t.Fatal(err)
	}
	view, err := e.redisData(ctx, model.Operation{Name: "redisdata.read", Resource: i, Params: model.Config{"key": key}})
	if err != nil || view.Bool("editable") {
		t.Fatal("large read", err)
	}
	if _, err = e.redisData(ctx, model.Operation{Name: "redisdata.write", Resource: i, Params: model.Config{"db": "invalid", "key": key}}); err == nil {
		t.Fatal("invalid DB accepted")
	}
	if _, err = c.command("SET", "small", strings.Repeat("a", 100000)); err != nil {
		t.Fatal(err)
	}
	small := base64.StdEncoding.EncodeToString([]byte("small"))
	view, err = e.redisData(ctx, model.Operation{Name: "redisdata.read", Resource: i, Params: model.Config{"key": small}})
	if err != nil || !view.Bool("complete") || !view.Bool("editable") {
		t.Fatal("full editable read", err)
	}
	_, _ = c.command("EXPIRE", "small", "60")
	_, err = e.redisData(ctx, model.Operation{Name: "redisdata.write", Resource: i, Params: model.Config{"key": small, "operation": "delete", "confirmed": true, "revision": view.String("revision")}})
	if err == nil {
		t.Fatal("TTL change failed to invalidate revision")
	}
}

func TestLiveDatabaseSiteDeletion(t *testing.T) {
	for _, tc := range []struct{ sw, image string }{{"mysql", "mysql:8.4"}, {"postgresql", "postgres:18"}} {
		t.Run(tc.sw, func(t *testing.T) {
			e, i := liveDatabase(t, tc.sw, tc.image)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			db := model.Resource{ID: security.ID(), Kind: "database", Name: "site_deletion", Config: model.Config{"instanceId": i.ID, "username": "site_delete_user", "password": "site-delete-only-test-password", "source": "127.0.0.1"}}
			if _, err := e.database(ctx, model.Operation{Name: "database.create", Resource: db}, func(string) {}); err != nil {
				t.Fatal(err)
			}
			db, _ = e.load(db.ID)
			if err := e.dbRun(ctx, i, db.Name, "CREATE TABLE preserved(id integer); INSERT INTO preserved VALUES(7);"); err != nil {
				t.Fatal(err)
			}
			nginx := model.Resource{ID: security.ID(), Kind: "instance", Name: "site-delete-nginx", Config: model.Config{"software": "nginx"}}
			e.save(nginx)
			doc := filepath.Join(t.TempDir(), "project")
			os.MkdirAll(doc, 0755)
			os.WriteFile(doc+"/index.html", []byte("survives failed backup"), 0644)
			site := model.Resource{ID: security.ID(), Kind: "site", Name: "database-site.test", Config: model.Config{"type": "static", "domains": "database-site.test", "instanceId": nginx.ID, "databaseId": db.ID, "documentRoot": doc, "rootDirectory": doc, "siteBaseDirectory": filepath.Dir(doc)}}
			e.save(site)
			bin := "mysqldump"
			if tc.sw == "postgresql" {
				bin = "pg_dump"
			}
			path := filepath.Join(e.Root, "instances", i.ID, "runtime/bin", bin)
			os.Rename(path, path+".disabled")
			params := model.Config{"deleteDirectory": true, "deleteDatabase": true}
			if _, err := e.Execute(ctx, model.Operation{Name: "site.delete", Resource: site, Params: params}, func(string) {}); err == nil {
				t.Fatal("backup failure allowed site deletion")
			}
			if _, err := e.load(site.ID); err != nil {
				t.Fatal("site removed before backup")
			}
			if _, err := os.Stat(doc); err != nil {
				t.Fatal("directory removed before backup")
			}
			os.Rename(path+".disabled", path)
			if _, err := e.Execute(ctx, model.Operation{Name: "site.delete", Resource: site, Params: params}, func(string) {}); err != nil {
				t.Fatal(err)
			}
			db, _ = e.load(db.ID)
			if db.Status != "recycled" {
				t.Fatal("database not recycled")
			}
			restored, err := e.databaseAdmin(ctx, model.Operation{Name: "dbadmin.restore", Resource: db, Params: model.Config{"databaseName": "site_recovered"}}, func(string) {})
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(restored["resources"])
			var resources []model.Resource
			_ = json.Unmarshal(raw, &resources)
			if len(resources) != 1 {
				t.Fatal("missing restored DB")
			}
			if n, err := e.scalar(ctx, i, resources[0].Name, "SELECT count(*) FROM preserved;"); err != nil || n != 1 {
				t.Fatal("restored data", n, err)
			}
		})
	}
}
