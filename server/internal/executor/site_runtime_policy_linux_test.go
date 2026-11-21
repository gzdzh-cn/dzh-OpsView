//go:build linux

package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"opsview/internal/hostfirewall"
	"opsview/internal/logic"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"os/user"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestLinuxSiteRuntimePolicy(t *testing.T) {
	if os.Getenv("OPSVIEW_RUNTIME_SMOKE") != "true" || os.Getenv("GITHUB_ACTIONS") != "true" {
		t.Skip("disposable opted-in Linux systemd host required")
	}
	oldMask := syscall.Umask(0027)
	defer syscall.Umask(oldMask)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	base := "/var/lib/opsview-runtime-" + security.ID()
	e := &Engine{Root: base}
	if err := e.Initialize(); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	other := "/www/wwwroot/other-" + security.ID()
	os.MkdirAll(other, 0755)
	defer os.RemoveAll(other)
	os.WriteFile(other+"/secret", []byte("other-site"), 0644)
	for _, mode := range []string{"isolated", "root", "www-data", "existing", "node"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "node" && os.Getenv("OPSVIEW_NODE_RUNTIME_SMOKE") != "true" {
				t.Skip("optional installed Node runtime check")
			}
			id := security.ID()
			dir := "/www/wwwroot/runtime-" + id
			os.MkdirAll(dir, 0755)
			defer os.RemoveAll(dir)
			runUser := mode
			if mode == "node" {
				runUser = "isolated"
			}
			if mode == "existing" {
				runUser = "ovt_" + id[:12]
				group := "ovg_" + id[:12]
				if _, err := output(ctx, "groupadd", "--", group); err != nil {
					t.Fatal(err)
				}
				defer output(context.Background(), "groupdel", "--", group)
				if _, err := output(ctx, "useradd", "--no-create-home", "--gid", group, "--shell", "/usr/sbin/nologin", "--", runUser); err != nil {
					t.Fatal(err)
				}
				defer output(context.Background(), "userdel", "--", runUser)
			}
			port := unusedSessionPort(t)
			script := fmt.Sprintf("import os,json,http.server\npaths=%q\nr={}\nfor p in paths.split('|'):\n try:\n  open(p,'rb').read(1);r[p]=True\n except OSError:r[p]=False\nr['uid']=os.getuid()\nopen('checks.json','w').write(json.dumps(r))\nhttp.server.HTTPServer(('127.0.0.1',%d),http.server.SimpleHTTPRequestHandler).serve_forever()\n", other+"/secret|"+base+"/executor.key|/etc/shadow|/proc/1/root/etc/passwd", port)
			os.WriteFile(dir+"/server.py", []byte(script), 0755)
			r := model.Resource{ID: id, Kind: "site", Name: "runtime-test", Config: model.Config{"type": "managed", "user": runUser, "port": port, "executable": "/usr/bin/python3", "arguments": "[\"server.py\"]", "environment": "{}", "rootDirectory": dir, "documentRoot": dir, "runtimeIsolation": true, "autoStart": false}}
			if mode == "node" {
				paths, _ := json.Marshal([]string{other + "/secret", base + "/executor.key", "/etc/shadow", "/proc/1/root/etc/passwd"})
				js := fmt.Sprintf("const fs=require('fs'),r={};for(const p of %s){try{fs.readFileSync(p);r[p]=true}catch(e){r[p]=false}}r.uid=process.getuid();fs.writeFileSync('checks.json',JSON.stringify(r));require('http').createServer((q,s)=>s.end('ok')).listen(%d,'127.0.0.1');", paths, port)
				os.WriteFile(dir+"/server.js", []byte(js), 0755)
				r.Config["executable"] = "/usr/bin/node"
				r.Config["arguments"] = `["server.js"]`
			}
			_, fingerprint, err := e.inspectSitePermissions(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			r.Config["permissionPreview"] = fingerprint
			if mode == "root" {
				r.Config["siteRootApproval"] = logic.SiteRootDigest(r)
			}
			unit := serviceName(managedSiteProcess(r))
			defer cleanupSiteUnit(t, unit)
			restore, err := e.setupManagedSite(ctx, r, func(s string) { t.Log(s) })
			if err != nil {
				t.Fatal(err)
			}
			if err = e.commitSitePermissions(id); err != nil {
				t.Fatal(err)
			}
			if err = e.save(r); err != nil {
				t.Fatal(err)
			}
			if err = e.commitSiteRuntime(id); err != nil {
				t.Fatal(err)
			}
			if mode == "isolated" {
				registry, er := e.readSiteAccounts()
				if er != nil {
					t.Fatal(er)
				}
				account := registry.Accounts[r.Config.String("runtimeUser")]
				account.Phase = "creating"
				if er = e.writeSiteAccounts(registry); er != nil {
					t.Fatal(er)
				}
				resumed, er := e.ensureSiteAccount(ctx, r, func(s string) { t.Log(s) })
				if er != nil || resumed.Uid != account.UID || resumed.Gid != account.GID {
					t.Fatal("interrupted account creation changed identity", er)
				}
			}
			var checks map[string]any
			b, err := os.ReadFile(dir + "/checks.json")
			if err != nil {
				t.Fatal(err)
			}
			json.Unmarshal(b, &checks)
			for p, ok := range checks {
				if p != "uid" && ok != false {
					t.Fatalf("sandbox leaked %s: %v", p, ok)
				}
			}
			if mode == "root" && checks["uid"] != float64(0) {
				t.Fatal(checks)
			}
			st, _ := os.Stat(dir + "/server.py")
			if st.Mode().Perm()&0100 == 0 {
				t.Fatal("execute permission lost")
			}
			if _, err = output(ctx, "runuser", "-u", "www-data", "--", "test", "-r", dir+"/server.py"); err != nil {
				t.Fatal("Nginx cannot read prepared files", err)
			}
			// Recover an interrupted runtime update before the site commit. The
			// executor must restore the previous service, not accept the new process.
			candidate := r
			candidate.Config = cloneConfig(r.Config)
			candidate.Config["environment"] = `{"RUNTIME_REVISION":"uncommitted"}`
			delete(candidate.Config, "permissionPreview")
			if mode == "root" {
				candidate.Config["siteRootApproval"] = logic.SiteRootDigest(candidate)
			}
			if _, err = e.setupManagedSite(ctx, candidate, func(s string) { t.Log(s) }); err != nil {
				t.Fatal(err)
			}
			if err = e.recoverSiteRuntime(id); err != nil {
				t.Fatal("interrupted update recovery", err)
			}
			previous, err := e.load(managedSiteProcess(r).ID)
			if err != nil || previous.Config.String("environment") == candidate.Config.String("environment") {
				t.Fatal("runtime update was not rolled back", err)
			}
			// External symlink and hardlink adoption is refused, not chmod'd.
			os.Symlink(other+"/secret", dir+"/alias")
			if _, _, err = e.inspectSitePermissions(ctx, r); err == nil {
				t.Fatal("external alias accepted")
			}
			os.Remove(dir + "/alias")
			os.Link(dir+"/server.py", dir+"/hardlink")
			if _, _, err = e.inspectSitePermissions(ctx, r); err == nil {
				t.Fatal("hardlink accepted")
			}
			os.Remove(dir + "/hardlink")
			if err = systemctl(ctx, func(s string) { t.Log(s) }, "stop", unit); err != nil {
				t.Fatal(err)
			}
			cleanupSiteUnit(t, unit)
			e.forget(managedSiteProcess(r).ID)
			e.forget(r.ID)
			if mode == "isolated" || mode == "node" {
				name := r.Config.String("runtimeUser")
				if _, err = user.Lookup(name); err != nil {
					t.Fatal(err)
				}
				if err = e.retainSiteAccount(r, dir); err != nil {
					t.Fatal(err)
				}
				item, er := e.trashFile(dir)
				if er != nil {
					t.Fatal(er)
				}
				if item.Account != name {
					t.Fatal("trash account association missing")
				}
				if _, er = e.fileTrash(model.Config{"action": "restore", "id": item.ID}); er != nil {
					t.Fatal(er)
				}
				if _, er = user.Lookup(name); er != nil {
					t.Fatal("restored owner removed", er)
				}
				item, er = e.trashFile(dir)
				if er != nil {
					t.Fatal(er)
				}
				if _, er = e.fileTrash(model.Config{"action": "purge", "id": item.ID, "confirmed": true}); er != nil {
					t.Fatal(er)
				}
				if _, er = user.Lookup(name); er == nil {
					v, _ := e.readSiteAccounts()
					t.Fatal("unreferenced account retained", v.Accounts[name])
				}
			} else {
				if _, err = user.Lookup(runUser); err != nil {
					t.Fatal("shared/root user removed", err)
				}
			}
			_ = restore
		})
	}
}

func TestLinuxSitePermissionRollback(t *testing.T) {
	if os.Getenv("OPSVIEW_RUNTIME_SMOKE") != "true" || os.Getenv("GITHUB_ACTIONS") != "true" {
		t.Skip("disposable opted-in host required")
	}
	e := &Engine{Root: t.TempDir()}
	os.MkdirAll(e.Root+"/site-users", 0700)
	dir := "/www/wwwroot/rollback-" + security.ID()
	os.MkdirAll(dir, 0755)
	defer os.RemoveAll(dir)
	os.WriteFile(dir+"/entry", []byte("preserve"), 0751)
	r := model.Resource{ID: security.ID(), Config: model.Config{"rootDirectory": dir, "documentRoot": dir}}
	ctx := context.Background()
	before, fp, err := e.inspectSitePermissions(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	r.Config["permissionPreview"] = fp
	if err = e.applySitePermissions(ctx, r, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	if err = e.restoreSitePermissions(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	after, _, err := e.inspectSitePermissions(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range before.Entries {
		got := after.Entries[i]
		if got.UID != v.UID || got.GID != v.GID || got.Mode != v.Mode || got.ACL != v.ACL {
			t.Fatalf("permissions not restored for %s", v.Path)
		}
	}
	if _, err = output(ctx, "mount", "-t", "tmpfs", "tmpfs", dir+"/mounted"); err == nil {
		t.Fatal("unexpected preexisting mount target")
	}
	os.Mkdir(dir+"/mounted", 0755)
	if _, err = output(ctx, "mount", "-t", "tmpfs", "tmpfs", dir+"/mounted"); err != nil {
		t.Fatal(err)
	}
	defer output(ctx, "umount", dir+"/mounted")
	if _, _, err = e.inspectSitePermissions(ctx, r); err == nil {
		t.Fatal("mount boundary accepted")
	}
}

func TestLinuxSitePortOwnership(t *testing.T) {
	if os.Getenv("OPSVIEW_SITE_FIREWALL_SMOKE") != "true" || os.Getenv("GITHUB_ACTIONS") != "true" {
		t.Skip("disposable opted-in firewall required")
	}
	e := &Engine{Root: t.TempDir()}
	ctx := context.Background()
	s, err := e.hostFirewall().Status(ctx)
	if err != nil || !s.Installed || !s.Compatible {
		t.Fatal(s, err)
	}
	port := unusedSessionPort(t)
	a := model.Resource{ID: security.ID(), Config: model.Config{"releasePort": true, "port": port}}
	b := model.Resource{ID: security.ID(), Config: cloneConfig(a.Config)}
	if err = e.syncSitePort(ctx, a, false); err != nil {
		t.Fatal(err)
	}
	defer e.syncSitePort(ctx, a, true)
	if err = e.syncSitePort(ctx, b, false); err != nil {
		t.Fatal(err)
	}
	if err = e.syncSitePort(ctx, a, true); err != nil {
		t.Fatal(err)
	}
	rows, _ := e.sitePortRecords()
	if len(rows) == 0 {
		t.Fatal("shared rule removed")
	}
	if err = e.syncSitePort(ctx, b, true); err != nil {
		t.Fatal(err)
	}
	rows, _ = e.sitePortRecords()
	if len(rows) != 0 {
		t.Fatal("unreferenced rules retained", rows)
	}
	external := hostfirewall.Rule{Kind: "port", Protocol: "tcp", Ports: fmt.Sprint(port), Source: "any", Destination: "any", Family: "ipv4", Direction: "in", Action: "allow", Note: "external-test-rule", Permanent: true, Runtime: s.Enabled}
	if s.Backend == "firewalld" {
		external.Zone = firewallDefaultZone(ctx, e.hostFirewall(), s.Backend)
	}
	if err = e.hostFirewall().Change(ctx, s.Backend, external, false); err != nil {
		t.Fatal(err)
	}
	defer e.hostFirewall().Change(ctx, s.Backend, external, true)
	if err = e.syncSitePort(ctx, a, false); err != nil {
		t.Fatal(err)
	}
	if err = e.syncSitePort(ctx, a, true); err != nil {
		t.Fatal(err)
	}
	current, err := e.hostFirewall().Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, rule := range current.Rules {
		if rule.Key() == external.Key() {
			found = true
		}
	}
	if !found {
		t.Fatal("external rule was deleted")
	}
}

func TestSiteAccountMetadataRejectsUnknownVersion(t *testing.T) {
	e := &Engine{Root: t.TempDir()}
	os.MkdirAll(filepath.Join(e.Root, "site-users"), 0700)
	os.WriteFile(filepath.Join(e.Root, "site-users", "accounts.json"), []byte(`{"version":99,"accounts":{}}`), 0600)
	if _, err := e.readSiteAccounts(); err == nil {
		t.Fatal("unknown registry accepted")
	}
}
