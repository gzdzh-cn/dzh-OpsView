package maintenance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"opsview/internal/model"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// This exercises real database processes, systemd, a private Docker daemon and
// the installed CLI binary. It cannot run against a user's installed server.
func TestLinuxPanelUninstall(t *testing.T) {
	if os.Getenv("OPSVIEW_UNINSTALL_LINUX") != "1" {
		t.Skip("disposable systemd/Docker acceptance only")
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Fatal("Linux root required")
	}
	for _, marker := range []string{"/opsview-disposable-uninstall", "/.dockerenv", "/run/systemd/system", "/tests/bin/opsview"} {
		if _, err := os.Stat(marker); err != nil {
			t.Fatal("disposable host marker missing")
		}
	}
	for _, path := range []string{"/opt/opsview", "/var/lib/opsview"} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("disposable host must have no OpsView install")
		}
	}
	command := func(name string, args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		b, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
		if err != nil {
			if filepath.Base(name) == "mysqld" {
				// This is the freshly initialized, password-free disposable fixture.
				// Never inspect panel credentials or production database logs.
				t.Logf("disposable mysql initialization: %s", b)
				if detail, readErr := os.ReadFile(uninstallManaged + "/instances/mysql-check/logs/mysql.log"); readErr == nil {
					t.Logf("disposable mysql fixture log: %s", detail)
				}
			}
			if name == "docker" || name == "systemctl" || name == "python3" {
				// These commands only touch the disposable fixture and never
				// receive generated administrator credentials.
				t.Logf("disposable host diagnostic: %s", b)
			}
			t.Fatalf("acceptance command %s failed (%v); output withheld", filepath.Base(name), err)
		}
		return b
	}
	for _, args := range [][]string{{"groupadd", "--system", "opsview"}, {"useradd", "--system", "--gid", "opsview", "--home-dir", "/var/lib/opsview/panel", "--shell", "/usr/sbin/nologin", "opsview"}, {"useradd", "--system", "--no-create-home", "--shell", "/usr/sbin/nologin", "ovdb"}} {
		command(args[0], args[1:]...)
	}
	for _, path := range []string{"/opt/opsview/bin", "/etc/opsview", "/var/lib/opsview/panel", uninstallManaged + "/state", "/usr/local/bin", "/www/wwwroot/example.test"} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	command("chown", "opsview:opsview", "/var/lib/opsview/panel")
	command("chmod", "0711", "/var/lib/opsview", uninstallManaged)
	for _, binary := range []string{"opsview", "opsview-executor", "opsview-plugin-worker", "opsview-pack", "opsview-install.sh"} {
		command("cp", "/tests/bin/"+binary, "/opt/opsview/bin/"+binary)
		command("chmod", "0755", "/opt/opsview/bin/"+binary)
	}
	for _, unit := range []string{panelUnit, executorUnit} {
		command("cp", "/tests/deploy/"+unit, "/etc/systemd/system/"+unit)
	}
	for _, name := range []string{"ov", "opsview"} {
		if err := os.Symlink("/opt/opsview/bin/opsview", "/usr/local/bin/"+name); err != nil {
			t.Fatal(err)
		}
	}
	command("/opt/opsview/bin/opsview", "runtime-config", "init", "--new-install")
	command("/opt/opsview/bin/opsview", "admin-init") // Newly generated test credentials remain in memory only.
	command("systemctl", "daemon-reload")
	command("systemctl", "enable", "--now", executorUnit, panelUnit)
	for i := 0; i < 60; i++ {
		if err := HealthForTest(); err == nil {
			break
		}
		if i == 59 {
			t.Fatal("panel did not start")
		}
		time.Sleep(200 * time.Millisecond)
	}
	// Nested overlay mounts are unsupported on some Docker Desktop hosts.
	// A private classic vfs daemon exercises the same container lifecycle API.
	uninstallWrite(t, "/", "/etc/docker/daemon.json", `{"storage-driver":"vfs","features":{"containerd-snapshotter":false}}`)
	command("systemctl", "restart", "docker.service")
	if strings.TrimSpace(string(command("docker", "info", "--format", "{{.Driver}}"))) != "vfs" {
		t.Fatal("disposable Docker daemon must use vfs")
	}
	command("docker", "load", "--input", "/tests/redis.tar")
	resources := []model.Resource{}
	addDB := func(id, software, binary, body string) string {
		t.Helper()
		base := uninstallManaged + "/instances/" + id
		for _, sub := range []string{"runtime/bin", "conf", "data", "logs", "source"} {
			if err := os.MkdirAll(base+"/"+sub, 0755); err != nil {
				t.Fatal(err)
			}
		}
		command("chown", "-R", "ovdb:ovdb", base+"/data", base+"/logs")
		command("chmod", "0700", base+"/data")
		if err := os.Symlink(binary, base+"/runtime/bin/"+filepath.Base(binary)); err != nil {
			t.Fatal(err)
		}
		uninstallWrite(t, "/", base+"/source/main.c", "original compile source")
		r := model.Resource{ID: id, Kind: "instance", Name: id, Status: "ready", Config: model.Config{"software": software, "mode": "native", "version": "acceptance"}}
		resources = append(resources, r)
		unit := "opsview-instance-" + id + ".service"
		uninstallWrite(t, "/", "/etc/systemd/system/"+unit, "[Unit]\nDescription=OpsView instance "+id+"\n[Service]\nUser=ovdb\nGroup=ovdb\n"+body+"\nRestart=on-failure\n[Install]\nWantedBy=multi-user.target\n")
		return base
	}
	mysqlBase := uninstallManaged + "/instances/mysql-check"
	// Use a fixture executable outside the distribution path: host AppArmor
	// profiles for /usr/sbin/mysqld can attach inside privileged CI containers.
	// Keep vendor resources explicit and preserve this external test executable
	// for the retained-data restart after uninstall.
	const mysqlExecutable = "/opt/opsview-uninstall-fixture/mysqld"
	command("install", "-D", "-m", "0755", "/usr/sbin/mysqld", mysqlExecutable)
	addDB("mysql-check", "mysql", mysqlExecutable, "ExecStart="+mysqlBase+"/runtime/bin/mysqld --no-defaults --basedir=/usr --datadir="+mysqlBase+"/data --socket="+mysqlBase+"/data/mysql.sock --port=13316 --bind-address=127.0.0.1 --mysqlx=OFF --log-error="+mysqlBase+"/logs/mysql.log")
	command(mysqlExecutable, "--no-defaults", "--basedir=/usr", "--initialize-insecure", "--user=ovdb", "--datadir="+mysqlBase+"/data", "--log-error="+mysqlBase+"/logs/mysql.log")
	pgBase := uninstallManaged + "/instances/pg-check"
	addDB("pg-check", "postgresql", "/usr/lib/postgresql/16/bin/postgres", "ExecStart="+pgBase+"/runtime/bin/postgres -D "+pgBase+"/data -h 127.0.0.1 -p 13432 -k "+pgBase+"/data")
	command("runuser", "-u", "ovdb", "--", "/usr/lib/postgresql/16/bin/initdb", "-D", pgBase+"/data", "-U", "postgres", "--auth=trust")
	redisBase := uninstallManaged + "/instances/redis-check"
	addDB("redis-check", "redis", "/usr/bin/redis-server", "ExecStart="+redisBase+"/runtime/bin/redis-server "+redisBase+"/conf/redis.conf")
	uninstallWrite(t, "/", redisBase+"/conf/redis.conf", "bind 127.0.0.1\nport 13679\ndir "+redisBase+"/data\nappendonly yes\nsave 60 1\n")
	command("chown", "ovdb:ovdb", redisBase+"/conf/redis.conf")
	command("systemctl", "daemon-reload")
	for _, id := range []string{"mysql-check", "pg-check", "redis-check"} {
		command("systemctl", "enable", "--now", "opsview-instance-"+id+".service")
	}
	for i := 0; i < 120; i++ {
		if exec.Command("/usr/bin/mysqladmin", "--no-defaults", "--socket="+mysqlBase+"/data/mysql.sock", "-uroot", "ping").Run() == nil {
			break
		}
		if i == 119 {
			t.Fatal("mysql not ready")
		}
		time.Sleep(200 * time.Millisecond)
	}
	command("mysql", "--no-defaults", "--socket="+mysqlBase+"/data/mysql.sock", "-uroot", "-e", "CREATE DATABASE retained; CREATE TABLE retained.probe (value INT); INSERT INTO retained.probe VALUES (42)")
	command("psql", "-h", "127.0.0.1", "-p", "13432", "-U", "postgres", "-c", "CREATE TABLE probe (value INT); INSERT INTO probe VALUES (42)")
	command("redis-cli", "-p", "13679", "set", "retained", "42")
	command("redis-cli", "-p", "13679", "save")
	resources = append(resources, model.Resource{ID: "site-check", Kind: "site", Config: model.Config{"documentRoot": "/www/wwwroot/example.test"}}, model.Resource{ID: "container-check", Kind: "instance", Config: model.Config{"software": "redis", "mode": "docker"}}, model.Resource{ID: "app-check", Kind: "site-process", Config: model.Config{"workingDirectory": "/www/wwwroot/example.test"}})
	uninstallRegister(t, "/", resources...)
	uninstallWrite(t, "/", "/www/wwwroot/example.test/index.php", "<?php echo 'retained';")
	uninstallWrite(t, "/", "/www/wwwroot/example.test/process.sh", "#!/bin/sh\nexec sleep 3600\n")
	uninstallWrite(t, "/", uninstallManaged+"/state/app-check.env", "OPSVIEW_TEST=1\n")
	uninstallWrite(t, "/", "/etc/systemd/system/opsview-site-process-app-check.service", "[Unit]\nDescription=OpsView site-process app-check\n[Service]\nEnvironmentFile="+uninstallManaged+"/state/app-check.env\nExecStart=/bin/sh /www/wwwroot/example.test/process.sh\n")
	command("systemctl", "daemon-reload")
	command("systemctl", "start", "opsview-site-process-app-check.service")
	uninstallWrite(t, "/", "/etc/ssh/sshd_config", "Port 22\n# independent SSH settings\n")
	command("iptables", "-A", "INPUT", "-p", "tcp", "--dport", "25432", "-j", "ACCEPT")
	identities := map[string]string{}
	for _, path := range []string{mysqlBase + "/data", pgBase + "/data", redisBase + "/data", "/www/wwwroot/example.test/index.php", "/www/wwwroot/example.test/process.sh"} {
		identities[path] = string(command("stat", "--format=%u:%g:%a", path))
	}
	uninstallWrite(t, "/", "/www/backup/opsview-panel/old.ovp", "OVB1\nremove this backup")
	uninstallWrite(t, "/", "/var/lib/opsview/updates/old/bin/opsview", "old binary")
	uninstallWrite(t, "/", "/var/log/opsview/install.log", "install log")
	command("docker", "volume", "create", "retained-database")
	id := strings.TrimSpace(string(command("docker", "run", "-d", "--name", "owned", "--label", "opsview.managed=true", "--label", "opsview.resource=container-check", "-v", "retained-database:/data", "redis:7", "redis-server", "--appendonly", "yes")))
	other := strings.TrimSpace(string(command("docker", "run", "-d", "--name", "unrelated", "redis:7")))
	command("docker", "inspect", "--type=container", "--format", uninstallDockerFormat, id)
	for i := 0; i < 50; i++ {
		if exec.Command("docker", "exec", id, "redis-cli", "ping").Run() == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	command("docker", "exec", id, "redis-cli", "set", "retained", "42")
	command("docker", "exec", id, "redis-cli", "save")
	for _, answer := range []string{"Y\n", "n\n", "\n", ""} {
		cmd := exec.Command("ov", "uninstall")
		cmd.Stdin = strings.NewReader(answer)
		if output, err := cmd.CombinedOutput(); err != nil || !bytes.Contains(output, []byte("已取消卸载")) {
			t.Fatalf("cancel failed: %v; %s", err, output)
		}
		command("systemctl", "is-active", panelUnit)
	}
	lock, err := os.OpenFile("/run/lock/opsview-install.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("ov", "uninstall")
	cmd.Stdin = strings.NewReader("y\n")
	if output, err := cmd.CombinedOutput(); err == nil || !bytes.Contains(output, []byte("另一个 OpsView")) {
		t.Fatal("maintenance lock bypass")
	}
	syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	lock.Close()
	// A live connection holds PostgreSQL's smart shutdown open long enough to
	// interrupt deterministically before any program/data cleanup begins.
	interrupted := `import os,signal,subprocess
hold=subprocess.Popen(['psql','-h','127.0.0.1','-p','13432','-U','postgres','-c','SELECT pg_sleep(120)'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
try:
 p=subprocess.Popen(['ov','uninstall'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True)
 p.stdin.write('y\n');p.stdin.flush();seen=False
 for line in p.stdout:
  if '已停止：opsview-panel.service' in line:
   seen=True;p.send_signal(signal.SIGINT);break
 p.communicate(timeout=30)
 assert seen and p.returncode!=0,'interrupted uninstall must fail'
 assert os.path.isfile('/opt/opsview/bin/opsview') and os.path.isfile('/var/lib/opsview/uninstall-progress.json'),'retry state lost'
finally:
 if hold.poll() is None:hold.send_signal(signal.SIGINT)
 hold.wait(timeout=10)
print('Interrupted uninstall retained retry program and progress')
`
	command("python3", "-c", interrupted)
	// Real PTY: select menu 18, confirm once, and assert successful uninstall
	// exits without redrawing a menu that can no longer execute any command.
	pty := `import os,pty,select,subprocess,time
m,s=pty.openpty();p=subprocess.Popen(['ov'],stdin=s,stdout=s,stderr=s);os.close(s);out=b''
def read_until(text):
 global out
 end=time.monotonic()+180
 while text.encode() not in out:
  assert time.monotonic()<end,'PTY timeout'
  if select.select([m],[],[],1)[0]:out+=os.read(m,65536)
read_until('请输入编号：');assert '18. 卸载 OpsView' in out.decode();os.write(m,b'18\n');read_until('其他输入取消：');os.write(m,b'y\n')
while p.poll() is None:
 if select.select([m],[],[],1)[0]:
  try:out+=os.read(m,65536)
  except OSError:break
assert p.wait(timeout=10)==0,out.decode();assert out.decode().count('终端管理')==1
print('PTY uninstall succeeded')
`
	command("python3", "-c", pty)
	for _, path := range []string{"/opt/opsview/bin/opsview", "/usr/local/bin/ov", "/usr/local/bin/opsview", "/var/lib/opsview/panel", "/etc/opsview", "/www/backup/opsview-panel", "/var/lib/opsview/updates", "/var/log/opsview", uninstallProgress, "/etc/systemd/system/opsview-site-process-app-check.service", "/etc/systemd/system/" + panelUnit, "/etc/systemd/system/" + executorUnit} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("installed path remains: %s", path)
		}
	}
	for _, base := range []string{mysqlBase, pgBase, redisBase} {
		if _, err := os.Stat(base + "/runtime"); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("software runtime remains")
		}
		b, err := os.ReadFile(base + "/source/main.c")
		if err != nil || string(b) != "original compile source" {
			t.Fatal("source lost")
		}
		st, err := os.Stat(base + "/data")
		if err != nil || st.Mode().Perm() != 0700 {
			t.Fatal("data/permissions changed")
		}
	}
	if b, _ := os.ReadFile("/www/wwwroot/example.test/index.php"); string(b) != "<?php echo 'retained';" {
		t.Fatal("website lost")
	}
	for path, before := range identities {
		if after := string(command("stat", "--format=%u:%g:%a", path)); after != before {
			t.Fatalf("retained ownership or permissions changed: %s", path)
		}
	}
	if b, _ := os.ReadFile("/etc/ssh/sshd_config"); string(b) != "Port 22\n# independent SSH settings\n" {
		t.Fatal("external SSH settings changed")
	}
	command("iptables", "-C", "INPUT", "-p", "tcp", "--dport", "25432", "-j", "ACCEPT")
	if state := strings.TrimSpace(string(command("docker", "inspect", "--format", "{{.State.Running}}", other))); state != "true" {
		t.Fatal("external container stopped")
	}
	command("docker", "volume", "inspect", "retained-database")
	if exec.Command("docker", "inspect", id).Run() == nil {
		t.Fatal("owned container remains")
	}
	// Recreate only test service units using the unchanged system binaries and
	// original data directories. Successful queries prove the retained data can
	// be used after all OpsView configuration and encryption keys are gone.
	for _, entry := range []struct{ name, exec string }{
		{"mysql", mysqlExecutable + " --no-defaults --basedir=/usr --datadir=" + mysqlBase + "/data --socket=" + mysqlBase + "/data/mysql.sock --port=13316 --bind-address=127.0.0.1 --mysqlx=OFF --log-error=" + mysqlBase + "/data/restore.log"},
		{"pg", "/usr/lib/postgresql/16/bin/postgres -D " + pgBase + "/data -h 127.0.0.1 -p 13432 -k " + pgBase + "/data"},
		{"redis", "/usr/bin/redis-server --bind 127.0.0.1 --port 13679 --dir " + redisBase + "/data --appendonly yes"},
	} {
		uninstallWrite(t, "/", "/etc/systemd/system/restore-"+entry.name+".service", "[Service]\nUser=ovdb\nExecStart="+entry.exec+"\n")
	}
	command("systemctl", "daemon-reload")
	command("systemctl", "start", "restore-mysql", "restore-pg", "restore-redis")
	for i := 0; i < 120; i++ {
		if exec.Command("mysqladmin", "--no-defaults", "--socket="+mysqlBase+"/data/mysql.sock", "-uroot", "ping").Run() == nil {
			break
		}
		if i == 119 {
			t.Fatal("retained mysql failed to restart")
		}
		time.Sleep(200 * time.Millisecond)
	}
	for name, output := range map[string][]byte{
		"mysql":      command("mysql", "--no-defaults", "--socket="+mysqlBase+"/data/mysql.sock", "-uroot", "-Nse", "SELECT value FROM retained.probe"),
		"postgresql": command("psql", "-h", "127.0.0.1", "-p", "13432", "-U", "postgres", "-Atc", "SELECT value FROM probe"),
		"redis":      command("redis-cli", "-p", "13679", "get", "retained"),
	} {
		if strings.TrimSpace(string(output)) != "42" {
			t.Fatalf("%s data recovery failed", name)
		}
	}
	command("docker", "run", "--rm", "--network=none", "-v", "retained-database:/data:ro", "redis:7", "redis-check-rdb", "/data/dump.rdb")
	// No passwords, keys or data values are reported by the test.
	t.Log("real CLI/systemd uninstall passed; MySQL/PostgreSQL/Redis data restarted; owned container removed; volume/source/site/external container retained")
}

func HealthForTest() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "curl", "--silent", "--fail", "--insecure", "https://127.0.0.1:18790/api/v1/health").Output()
	if err != nil {
		return err
	}
	var result map[string]any
	if json.Unmarshal(b, &result) != nil || result["status"] != "ok" {
		return fmt.Errorf("unhealthy")
	}
	return nil
}
