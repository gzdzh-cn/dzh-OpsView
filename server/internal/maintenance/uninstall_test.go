package maintenance

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"opsview/internal/dao"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func uninstallFixture(t *testing.T) (*uninstaller, string, *[]string) {
	t.Helper()
	dir := t.TempDir()
	for _, p := range []string{"etc/systemd/system", "var/lib/opsview/managed/state", "opt/opsview/bin", "usr/local/bin"} {
		if err := os.MkdirAll(filepath.Join(dir, p), 0755); err != nil {
			t.Fatal(err)
		}
	}
	f, err := openUninstallFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.root.Close() })
	calls := []string{}
	u := &uninstaller{fs: f, out: io.Discard, stopTimeout: time.Millisecond, cleanup: func(context.Context) error { return nil }}
	u.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "docker" {
			return nil, errors.New("no docker")
		}
		if name == "systemctl" && len(args) >= 2 && args[0] == "show" {
			path := "/etc/systemd/system/" + args[1]
			if _, err := f.stat(path); errors.Is(err, os.ErrNotExist) {
				return []byte("LoadState=not-found\nActiveState=inactive\nMainPID=0\n"), nil
			}
			return []byte("LoadState=loaded\nActiveState=inactive\nMainPID=0\nResult=success\nFragmentPath=" + path + "\nDropInPaths=\n"), nil
		}
		return nil, nil
	}
	return u, dir, &calls
}

func uninstallWrite(t *testing.T, root, path, body string) {
	t.Helper()
	p := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0640); err != nil {
		t.Fatal(err)
	}
}

func uninstallRegister(t *testing.T, root string, resources ...model.Resource) {
	t.Helper()
	v, err := security.OpenVault(filepath.Join(root, uninstallManaged, "executor.key"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range resources {
		b, _ := json.Marshal(r)
		uninstallWrite(t, root, uninstallManaged+"/state/"+r.ID+".json", v.Encrypt(b))
	}
}

func TestUninstallConfirmation(t *testing.T) {
	for _, value := range []string{"y\n", "y\r\n", "Y\n", "yes\n", " y\n", "y \n", "\n", "n\n", "", "y", "\x03\n"} {
		var out bytes.Buffer
		got := uninstallConfirmed(bufio.NewReader(strings.NewReader(value)), &out)
		if got != (value == "y\n" || value == "y\r\n") {
			t.Fatalf("accepted %q = %v", value, got)
		}
		if strings.Count(out.String(), "确认卸载") != 1 {
			t.Fatal("confirmation must appear once")
		}
	}
	var out bytes.Buffer
	r := Runner{Out: &out}
	if err := r.uninstall([]string{"--yes"}); err == nil {
		t.Fatal("confirmation bypass")
	}
	if os.Geteuid() != 0 {
		if err := r.uninstall(nil); err == nil || !strings.Contains(err.Error(), "root") {
			t.Fatal("missing root check")
		}
	}
}

func TestUninstallRetainsDataAndSources(t *testing.T) {
	u, root, _ := uninstallFixture(t)
	r := model.Resource{ID: "mysql-test", Kind: "instance", Config: model.Config{"software": "mysql", "version": "8.4.13", "mode": "native", "dataDirectory": uninstallManaged + "/instances/mysql-test/data-new"}}
	site := model.Resource{ID: "site-test", Kind: "site", Config: model.Config{"documentRoot": "/www/wwwroot/example.test"}}
	uninstallRegister(t, root, r, site)
	base := uninstallManaged + "/instances/mysql-test"
	keep := map[string]string{
		base + "/source/main.c": "source", base + "/data/ibdata1": "old-data", base + "/data-new/ibdata1": "new-data",
		base + "/logs/binlogs/mysql-bin.000001": "recovery-log", "/www/wwwroot/example.test/index.php": "website",
		uninstallManaged + "/sites/site-test/www/old.php": "legacy-website",
		"/opt/opsview/bin/repo/.git/config":               "source-git", "/opt/opsview/bin/repo/main.go": "project-source",
	}
	for p, body := range keep {
		uninstallWrite(t, root, p, body)
	}
	remove := []string{base + "/runtime/bin/mysqld", base + "/logs/error.log", base + "/conf/my.cnf", uninstallManaged + "/sites/site-test/logs/access.log", "/var/lib/opsview/updates/old/bin/opsview", "/www/backup/opsview-panel/old.ovp", "/opt/opsview/bin/opsview", "/opt/opsview/bin/opsview-install.sh"}
	for _, p := range remove {
		uninstallWrite(t, root, p, "OVB1\ninstalled")
	}
	uninstallWrite(t, root, base+"/conf/my.cnf", "[mysqld]\ndatadir="+base+"/data-new\nlog_bin="+base+"/logs/binlogs/mysql-bin\n")
	if err := os.Symlink("/opt/opsview/bin/opsview", filepath.Join(root, "usr/local/bin/ov")); err != nil {
		t.Fatal(err)
	}
	p, err := u.build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := u.execute(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	for path, body := range keep {
		b, err := os.ReadFile(filepath.Join(root, path))
		if err != nil || string(b) != body {
			t.Fatalf("lost retained path %s: %v", path, err)
		}
		st, _ := os.Stat(filepath.Join(root, path))
		if st.Mode().Perm() != 0640 {
			t.Fatal("retained permissions changed")
		}
	}
	for _, path := range append(remove, "/usr/local/bin/ov", uninstallProgress) {
		if _, err := os.Lstat(filepath.Join(root, path)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("not removed: %s (%v)", path, err)
		}
	}
}

func TestUninstallPanelInventoryReadOnly(t *testing.T) {
	u, root, _ := uninstallFixture(t)
	dir := filepath.Join(root, "var/lib/opsview/panel")
	s, err := dao.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := model.Resource{ID: "pending-site", Kind: "site", Name: "pending", Config: model.Config{"rootDirectory": "/var/lib/opsview/updates/pending-source"}}
	if err := s.Create(&r); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("panel", model.Config{"backupDirectory": "/srv/backups"}); err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	uninstallWrite(t, root, "/var/lib/opsview/updates/pending-source/index.php", "keep")
	uninstallWrite(t, root, "/srv/backups/opsview-panel/old.ovp", "OVB1\nbackup")
	key, _ := os.ReadFile(filepath.Join(dir, "master.key"))
	before, _ := os.ReadFile(filepath.Join(dir, "opsview.db"))
	p, err := u.build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "opsview.db"))
	if !bytes.Equal(before, after) {
		t.Fatal("preflight changed database")
	}
	if !protectedPath(r.Config.String("rootDirectory"), p.Keep) {
		t.Fatal("pending site not protected")
	}
	var out bytes.Buffer
	u.out = &out
	u.show(p)
	if strings.Contains(out.String(), string(key)) {
		t.Fatal("key leaked")
	}
	if err := u.execute(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("panel metadata remains")
	}
}

func TestUninstallPreflightRejectsUncertainState(t *testing.T) {
	for _, kind := range []string{"bad-key", "bad-state", "pending", "unknown-service", "overridden-service", "mounted-tree", "linked-state"} {
		t.Run(kind, func(t *testing.T) {
			u, root, calls := uninstallFixture(t)
			switch kind {
			case "bad-key":
				uninstallWrite(t, root, uninstallManaged+"/state/resource.json", "enc:bad")
			case "bad-state":
				uninstallRegister(t, root, model.Resource{ID: "bad", Kind: "instance"})
				uninstallWrite(t, root, uninstallManaged+"/state/bad.json", "enc:bad")
			case "pending":
				uninstallWrite(t, root, uninstallManaged+"/ssh-pending.json", "{}")
			case "unknown-service":
				uninstallWrite(t, root, "/etc/systemd/system/opsview-instance-missing.service", "[Service]\nExecStart="+uninstallManaged+"/instances/missing/runtime/bin/redis-server\n")
			case "overridden-service":
				uninstallWrite(t, root, "/etc/systemd/system/opsview-panel.service", "Description=other\nExecStart=/opt/opsview/bin/opsview serve \n")
			case "mounted-tree":
				uninstallWrite(t, root, "/var/lib/opsview/updates/mounted/x", "x")
				u.fs.mounts["/var/lib/opsview/updates/mounted"] = true
			case "linked-state":
				os.Remove(filepath.Join(root, uninstallManaged, "state"))
				if err := os.Symlink("/tmp", filepath.Join(root, uninstallManaged, "state")); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := u.build(context.Background()); err == nil {
				t.Fatal("accepted uncertain state")
			}
			for _, call := range *calls {
				if strings.Contains(call, " stop ") || strings.Contains(call, " rm ") {
					t.Fatal("preflight mutated system")
				}
			}
			if _, err := os.Stat(filepath.Join(root, uninstallProgress)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("preflight wrote progress")
			}
		})
	}
}

func TestUninstallFileSafety(t *testing.T) {
	for _, kind := range []string{"symlink-leaf", "ancestor-replaced", "identity-replaced", "mount", "keep-child"} {
		t.Run(kind, func(t *testing.T) {
			u, root, _ := uninstallFixture(t)
			uninstallWrite(t, root, "/cleanup/tree/file", "remove")
			uninstallWrite(t, root, "/outside/file", "keep")
			path := "/cleanup/tree"
			if kind == "symlink-leaf" {
				os.RemoveAll(filepath.Join(root, path))
				os.Symlink("../../outside", filepath.Join(root, path))
			}
			n, err := u.fs.capture(path, false)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "ancestor-replaced":
				os.Rename(filepath.Join(root, "cleanup"), filepath.Join(root, "old-cleanup"))
				os.Symlink("outside", filepath.Join(root, "cleanup"))
			case "identity-replaced":
				os.Rename(filepath.Join(root, path), filepath.Join(root, path+"-old"))
				uninstallWrite(t, root, path+"/file", "keep-new")
			case "mount":
				u.fs.base = "/"
				u.fs.refreshMounts()
				u.fs.mounts[path] = true // inspect rejection is separately checked below.
			}
			if kind == "mount" {
				if err := u.fs.inspect(path, nil); err == nil {
					t.Fatal("crossed mount")
				}
				u.fs.base = root
				return
			}
			var keep []string
			if kind == "keep-child" {
				keep = []string{path + "/file"}
			}
			err = u.fs.remove(n, keep)
			if (kind == "ancestor-replaced" || kind == "identity-replaced") && err == nil {
				t.Fatal("accepted replacement")
			}
			if (kind == "symlink-leaf" || kind == "keep-child") && err != nil {
				t.Fatal(err)
			}
			if b, _ := os.ReadFile(filepath.Join(root, "outside/file")); string(b) != "keep" {
				t.Fatal("followed symlink")
			}
			if kind == "keep-child" {
				if _, err := os.Stat(filepath.Join(root, path, "file")); err != nil {
					t.Fatal("removed retained child")
				}
			}
		})
	}
}

func TestUninstallChangedUnitAndGracefulStop(t *testing.T) {
	for _, kind := range []string{"changed-unit", "timeout", "bad-exit", "success"} {
		t.Run(kind, func(t *testing.T) {
			u, root, calls := uninstallFixture(t)
			path := "/etc/systemd/system/opsview-instance-db.service"
			uninstallWrite(t, root, path, "Description=OpsView instance db\nExecStart=/database\n")
			n, _ := u.fs.capture(path, true)
			unit := uninstallUnit{Name: filepath.Base(path), File: n, Database: true}
			killed := false
			u.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
				*calls = append(*calls, name+" "+strings.Join(args, " "))
				if args[0] == "kill" {
					killed = true
					return nil, nil
				}
				if args[0] == "show" {
					state := "active"
					pid := "123"
					result := "success"
					status := "0"
					if killed && kind != "timeout" {
						state = "inactive"
						pid = "0"
					}
					if kind == "bad-exit" {
						result = "exit-code"
						status = "1"
					}
					return []byte(fmt.Sprintf("FragmentPath=%s\nActiveState=%s\nMainPID=%s\nResult=%s\nExecMainCode=1\nExecMainStatus=%s\n", path, state, pid, result, status)), nil
				}
				return nil, nil
			}
			if kind == "changed-unit" {
				uninstallWrite(t, root, path, "Description=Other\nExecStart=/other\n")
			}
			err := u.stopUnit(context.Background(), unit)
			if (kind == "success") != (err == nil) {
				t.Fatalf("unexpected result %v", err)
			}
			for _, call := range *calls {
				if strings.Contains(call, "SIGKILL") {
					t.Fatal("forced database termination")
				}
			}
		})
	}
}

func TestUninstallFailureAndResume(t *testing.T) {
	u, root, _ := uninstallFixture(t)
	uninstallWrite(t, root, "/opt/opsview/bin/opsview", "program")
	uninstallWrite(t, root, "/var/lib/opsview/updates/snapshot/bin/opsview", "old")
	uninstallWrite(t, root, "/var/log/opsview/install.log", "log")
	os.Symlink("/opt/opsview/bin/opsview", filepath.Join(root, "usr/local/bin/ov"))
	p, err := u.build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	u.cleanup = func(context.Context) error { return errors.New("simulated failure") }
	if err := u.execute(context.Background(), p); err == nil {
		t.Fatal("failure not reported")
	}
	if _, err := os.Stat(filepath.Join(root, "opt/opsview/bin/opsview")); err != nil {
		t.Fatal("retry program lost")
	}
	u.cleanup = func(context.Context) error { return nil }
	resumed, err := u.build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := u.execute(context.Background(), resumed); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "var/log/opsview/install.log")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("resume did not complete")
	}
}

func TestUninstallUnrelatedCommandSurvives(t *testing.T) {
	u, root, _ := uninstallFixture(t)
	uninstallWrite(t, root, "/usr/local/bin/ov", "unrelated")
	if err := os.Symlink("../../other", filepath.Join(root, "usr/local/bin/opsview")); err != nil {
		t.Fatal(err)
	}
	if err := u.removeLinks(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "usr/local/bin/ov")); string(b) != "unrelated" {
		t.Fatal("unrelated command removed")
	}
	if _, err := os.Lstat(filepath.Join(root, "usr/local/bin/opsview")); err != nil {
		t.Fatal("unrelated link removed")
	}
}

func TestUninstallProtectsLinkedTablespaces(t *testing.T) {
	u, root, _ := uninstallFixture(t)
	base := uninstallManaged + "/instances/db"
	r := model.Resource{ID: "db", Kind: "instance", Config: model.Config{"software": "postgresql", "mode": "native"}}
	uninstallRegister(t, root, r)
	uninstallWrite(t, root, base+"/runtime/external-tablespace/rows", "database tablespace")
	if err := os.MkdirAll(filepath.Join(root, base, "data/pg_tblspc"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../runtime/external-tablespace", filepath.Join(root, base, "data/pg_tblspc/123")); err != nil {
		t.Fatal(err)
	}
	p, err := u.build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !protectedPath(base+"/runtime/external-tablespace/rows", p.Keep) {
		t.Fatal("tablespace target not protected")
	}
	if err := u.execute(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, base, "runtime/external-tablespace/rows")); string(b) != "database tablespace" {
		t.Fatal("tablespace lost")
	}
}

func TestUninstallRetainsUnknownBackupAndRejectsNewResource(t *testing.T) {
	u, root, _ := uninstallFixture(t)
	uninstallWrite(t, root, "/www/backup/opsview-panel/20261121.ovp", "OVB1\nencrypted archive")
	uninstallWrite(t, root, "/www/backup/opsview-panel/manual.txt", "unknown file")
	p, err := u.build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !protectedPath("/www/backup/opsview-panel/manual.txt", p.Keep) {
		t.Fatal("unknown backup file not protected")
	}
	uninstallRegister(t, root, model.Resource{ID: "new", Kind: "instance", Config: model.Config{"software": "mysql"}})
	if err := u.execute(context.Background(), p); err == nil {
		t.Fatal("resource change during confirmation accepted")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "www/backup/opsview-panel/manual.txt")); string(b) != "unknown file" {
		t.Fatal("unknown file removed")
	}
}

func TestUninstallCancelledCleanup(t *testing.T) {
	u, root, _ := uninstallFixture(t)
	uninstallWrite(t, root, "/cleanup/file", "keep")
	n, err := u.fs.capture("/cleanup", false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	u.fs.ctx = ctx
	if err := u.fs.remove(n, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("cleanup ignored cancellation")
	}
	if _, err := os.Stat(filepath.Join(root, "cleanup/file")); err != nil {
		t.Fatal("cancelled cleanup deleted file")
	}
}

func TestUninstallRechecksRetainedLinksAfterConfirmation(t *testing.T) {
	u, root, _ := uninstallFixture(t)
	base := uninstallManaged + "/instances/db"
	uninstallRegister(t, root, model.Resource{ID: "db", Kind: "instance", Config: model.Config{"software": "postgresql", "mode": "native"}})
	uninstallWrite(t, root, base+"/runtime/tablespace/rows", "retained database")
	if err := os.MkdirAll(filepath.Join(root, base, "data/pg_tblspc"), 0700); err != nil {
		t.Fatal(err)
	}
	p, err := u.build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../runtime/tablespace", filepath.Join(root, base, "data/pg_tblspc/123")); err != nil {
		t.Fatal(err)
	}
	if err := u.execute(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, base, "runtime/tablespace/rows")); string(b) != "retained database" {
		t.Fatal("link created during confirmation lost its target")
	}
}

func TestUninstallRejectsMalformedProgress(t *testing.T) {
	for _, kind := range []string{"container", "service", "keep"} {
		t.Run(kind, func(t *testing.T) {
			u, root, _ := uninstallFixture(t)
			p := &uninstallPlan{Version: 1, Done: map[string]bool{"cleanup-started": true}}
			switch kind {
			case "container":
				p.Containers = []uninstallContainer{{ID: "short"}}
			case "service":
				p.Units = []uninstallUnit{{Name: "../../bad.service"}}
			case "keep":
				p.Keep = []string{"relative"}
			}
			if err := u.save(p); err != nil {
				t.Fatal(err)
			}
			if _, err := u.build(context.Background()); err == nil {
				t.Fatal("invalid progress accepted")
			}
			if _, err := os.Stat(filepath.Join(root, uninstallProgress)); err != nil {
				t.Fatal("invalid progress modified")
			}
		})
	}
}

func TestUninstallRejectsChangedDatabaseDataPath(t *testing.T) {
	u, root, _ := uninstallFixture(t)
	base := uninstallManaged + "/instances/db"
	uninstallRegister(t, root, model.Resource{ID: "db", Kind: "instance", Config: model.Config{"software": "mysql", "mode": "native"}})
	uninstallWrite(t, root, base+"/conf/my.cnf", "datadir="+base+"/data\n")
	uninstallWrite(t, root, base+"/runtime/program", "runtime")
	uninstallWrite(t, root, "/var/lib/opsview/updates/new-data/ibdata1", "new database data")
	p, err := u.build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	uninstallWrite(t, root, base+"/conf/my.cnf", "datadir=/var/lib/opsview/updates/new-data\n")
	if err := u.execute(context.Background(), p); err == nil {
		t.Fatal("changed data configuration accepted")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "var/lib/opsview/updates/new-data/ibdata1")); string(b) != "new database data" {
		t.Fatal("new data deleted")
	}
}

func TestUninstallRejectsRetainedPanelConfiguration(t *testing.T) {
	u, root, _ := uninstallFixture(t)
	uninstallRegister(t, root, model.Resource{ID: "site", Kind: "site", Config: model.Config{"documentRoot": "/www/wwwroot/site"}})
	uninstallWrite(t, root, "/etc/opsview/panel.json", "panel configuration")
	if err := os.MkdirAll(filepath.Join(root, "www/wwwroot/site"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc", filepath.Join(root, "www/wwwroot/site/config")); err != nil {
		t.Fatal(err)
	}
	if _, err := u.build(context.Background()); err == nil || !strings.Contains(err.Error(), "交叠") {
		t.Fatal("retention could silently leave panel configuration installed", err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "etc/opsview/panel.json")); string(b) != "panel configuration" {
		t.Fatal("overlap check modified files")
	}
}
