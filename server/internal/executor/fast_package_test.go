package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"opsview/internal/catalog"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Runs twice in fresh, real-systemd target systems. No service stubs or
// source-build success alone can promote a package into the public catalog.
func TestFastPackageAcceptance(t *testing.T) {
	mode := os.Getenv("OPSVIEW_PACKAGE_MODE")
	if mode == "" {
		t.Skip("explicit disposable package pipeline only")
	}
	if os.Getenv("GITHUB_ACTIONS") != "true" || runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Fatal("requires disposable root Linux CI")
	}
	entry, ok := catalog.Find(os.Getenv("OPSVIEW_NATIVE_ENTRY"))
	if !ok {
		t.Fatal("unknown entry")
	}
	if active, err := output(context.Background(), "systemctl", "is-system-running"); err != nil && !strings.Contains(active, "degraded") {
		t.Fatal("real systemd is required", active, err)
	}
	// Match deploy/install.sh: the parent exists with explicit traversal rights.
	// MkdirAll under the executor umask must not create an inaccessible parent.
	if err := os.MkdirAll("/var/lib/opsview", 0711); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod("/var/lib/opsview", 0711); err != nil {
		t.Fatal(err)
	}
	previousMask := syscall.Umask(0027)
	defer syscall.Umask(previousMask)
	e := &Engine{Root: "/var/lib/opsview/fast-acceptance", allowUnverifiedNative: true}
	if err := e.Initialize(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Minute)
	defer cancel()
	outputDir := os.Getenv("OPSVIEW_NATIVE_OUTPUT")
	if outputDir == "" {
		t.Fatal("output directory missing")
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatal(err)
	}
	id := security.ID()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	method := "source"
	official := entry.Software == "mysql" || entry.Software == "nodejs"
	if official || mode == "verify" {
		method = "binary"
	}
	r := model.Resource{ID: id, Kind: "instance", Name: entry.Software + "-" + entry.Version, Config: model.Config{"catalogId": entry.ID, "software": entry.Software, "version": entry.Version, "mode": "native", "installMethod": method, "port": port, "password": "disposable-fast-package-test-only"}}
	base := filepath.Join(e.Root, "instances", id)
	if err = prepareInstanceLayout(base); err != nil {
		t.Fatal(err)
	}
	defer func() {
		{
			_ = systemctl(context.Background(), func(string) {}, "stop", serviceName(r))
			_ = systemctl(context.Background(), func(string) {}, "disable", serviceName(r))
			_ = os.Remove("/etc/systemd/system/" + serviceName(r))
			_ = run(context.Background(), func(string) {}, "", nil, nil, "systemctl", "daemon-reload")
		}
	}()
	log := func(s string) { t.Log(security.MaskLog(s, model.Operation{Resource: r})) }
	defer func() {
		if t.Failed() {
			detail, _ := output(context.Background(), "journalctl", "-u", serviceName(r), "-n", "20", "--no-pager")
			log(detail)
		}
	}()
	name := entry.ID + "-" + strings.ReplaceAll(hostSystem(), ":", "-") + ".tar.gz"
	reportPath := filepath.Join(outputDir, name+".json")
	var report model.Config
	if mode == "build" || official {
		if err = e.installNative(ctx, r, entry, base, log); err != nil {
			t.Fatal(err)
		}
	} else {
		b, er := os.ReadFile(reportPath)
		if er != nil {
			t.Fatal(er)
		}
		if er = json.Unmarshal(b, &report); er != nil {
			t.Fatal(er)
		}
		srv := httptest.NewTLSServer(http.FileServer(http.Dir(outputDir)))
		defer srv.Close()
		oldTransport := http.DefaultTransport
		http.DefaultTransport = srv.Client().Transport
		defer func() { http.DefaultTransport = oldTransport }()
		packages := []string{}
		raw, _ := json.Marshal(report["runtimePackages"])
		_ = json.Unmarshal(raw, &packages)
		entry.Binaries = []catalog.BinaryArtifact{{System: hostSystem(), Source: srv.URL + "/" + name, SHA256: report.String("sha256"), BuildPrefix: report.String("buildPrefix"), RuntimePackages: packages}}
		if err = e.installPrecompiled(ctx, r, entry, base, log); err != nil {
			t.Fatal(err)
		}
		if entry.Software == "php" {
			if err = run(ctx, log, "", nil, nil, base+"/runtime/bin/phpize", "--version"); err != nil {
				t.Fatal("PHP build helpers were not relocated", err)
			}
		}
	}
	// Validate real version and built-in extensions before promoting an artifact.
	if entry.Software == "php" {
		probe := fmt.Sprintf(`if(PHP_VERSION!==%q){fwrite(STDERR,PHP_VERSION);exit(1);}foreach(["fileinfo","mbstring","curl","zip","gd","intl","bcmath","exif","mysqli","pdo_mysql","pgsql","pdo_pgsql"] as $e){if(!extension_loaded($e)){fwrite(STDERR,$e);exit(2);}}if(MessageFormatter::formatMessage("en_US","{0,number,integer}",[1234])!=="1,234"){exit(3);}`, entry.Version)
		if err = run(ctx, log, "", nil, nil, base+"/runtime/bin/php", "-n", "-r", probe); err != nil {
			t.Fatal("PHP version/extensions", err)
		}
		if mode == "verify" && legacyPHP(entry.Version) {
			if err = run(ctx, log, "", nil, nil, base+"/runtime/compat/bin/curl-config", "--libs"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if stored, er := e.load(id); er == nil {
		r = stored
	}
	if entry.Software == "nodejs" {
		if err = run(ctx, log, "", nil, nil, base+"/runtime/bin/node", "-e", "if(1+1!==2)process.exit(1)"); err != nil {
			t.Fatal(err)
		}
		probe := fmt.Sprintf(`require('http').createServer((_,r)=>r.end('opsview-node-health')).listen(%d,'127.0.0.1');`, port)
		if err = security.WriteAtomic(base+"/node-probe.js", []byte(probe), 0644); err != nil {
			t.Fatal(err)
		}
		if err = installUnit(ctx, r, fmt.Sprintf("DynamicUser=yes\nExecStart=%s/runtime/bin/node %s/node-probe.js\n", base, base), log); err != nil {
			t.Fatal(err)
		}
		if err = waitPort(ctx, port); err != nil {
			t.Fatal(err)
		}
		response, er := http.Get(fmt.Sprintf("http://127.0.0.1:%d", port))
		if er != nil {
			t.Fatal(er)
		}
		data, er := io.ReadAll(response.Body)
		response.Body.Close()
		if er != nil || string(data) != "opsview-node-health" {
			t.Fatal(string(data), er)
		}
	} else {
		if err = waitPort(ctx, port); err != nil {
			t.Fatal(err)
		}
		if err = e.softwareHealth(ctx, r); err != nil {
			t.Fatal(err)
		}
		if entry.Software == "postgresql" {
			if err = e.sql(ctx, r, "", strings.NewReader("SELECT 1;"), log); err != nil {
				t.Fatal(err)
			}
		}
		if entry.Software == "nginx" {
			verifyNginxPackage(t, ctx, r, base, entry.Version, log)
			probe := r
			probe.Config = cloneConfig(r.Config)
			probe.Config["port"] = r.Config["statusPort"]
			if err = probeNginx(ctx, probe, []byte(nginxStatusServer(r.Config.Int("statusPort"), "abcdef"))); err != nil {
				t.Fatal(err)
			}
		}
	}
	if mode == "build" {
		packages, er := runtimePackages(ctx, base+"/runtime")
		if er != nil {
			t.Fatal(er)
		}
		report = model.Config{"catalogId": entry.ID, "software": entry.Software, "version": entry.Version, "system": hostSystem(), "buildPrefix": base + "/runtime", "recipeRevision": entry.RecipeRevision, "runtimePackages": packages, "protocolVerified": true, "systemdVerified": true, "runtimeVerified": true, "binaryVerified": false, "official": official}
		if !official {
			archive := filepath.Join(outputDir, name)
			if err = run(ctx, log, "", nil, nil, "tar", "--dereference", "--hard-dereference", "-czf", archive, "-C", base+"/runtime", "."); err != nil {
				t.Fatal(err)
			}
			// Published runtime archives contain no instance data or credentials.
			// The host upload action must read files created by root under umask 0027.
			if err = os.Chmod(archive, 0644); err != nil {
				t.Fatal(err)
			}
			digest, er := hashFile(archive)
			if er != nil {
				t.Fatal(er)
			}
			report["sha256"] = digest
		} else {
			report["sha256"] = entry.SHA256
			report["source"] = entry.Source
		}
	} else {
		if official {
			b, er := os.ReadFile(reportPath)
			if er != nil {
				t.Fatal(er)
			}
			if er = json.Unmarshal(b, &report); er != nil {
				t.Fatal(er)
			}
		}
		report["binaryVerified"] = true
		report["verifiedAt"] = time.Now().UTC().Format(time.RFC3339)
	}
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = security.WriteAtomic(reportPath, b, 0644); err != nil {
		t.Fatal(err)
	}
}
func runtimePackages(ctx context.Context, root string) ([]string, error) {
	packages := map[string]bool{"ca-certificates": true}
	library := regexp.MustCompile(`(?:=>\s*)?(/[^\s()]+)`)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		st, err := d.Info()
		if err != nil {
			return err
		}
		if st.Mode()&0111 == 0 && !strings.Contains(path, ".so") {
			return nil
		}
		out, er := output(ctx, "ldd", path)
		if er != nil {
			return nil
		}
		if strings.Contains(out, "not found") {
			return fmt.Errorf("unresolved library: %s: %s", path, out)
		}
		for _, match := range library.FindAllStringSubmatch(out, -1) {
			p := match[1]
			real, _ := filepath.EvalSymlinks(p)
			candidates := []string{p, real, strings.Replace(p, "/usr/lib/", "/lib/", 1), strings.Replace(p, "/lib/", "/usr/lib/", 1)}
			found := false
			for _, candidate := range candidates {
				owner, er := output(ctx, "dpkg-query", "--search", candidate)
				if er != nil {
					continue
				}
				owners := runtimePackageOwners(owner)
				if len(owners) == 0 {
					continue
				}
				for _, pkg := range owners {
					packages[pkg] = true
				}
				found = true
				break
			}
			if !found && !strings.HasPrefix(p, root+"/") {
				return fmt.Errorf("library %s has no runtime package", p)
			}
		}
		return nil
	})
	out := []string{}
	for pkg := range packages {
		out = append(out, pkg)
	}
	sort.Strings(out)
	return out, err
}

// dpkg-query can put diversion notices before the owning package on t64
// systems. Notices are diagnostic text, never apt package arguments.
func runtimePackageOwners(text string) []string {
	valid := regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]*(?::amd64)?$`)
	for _, line := range strings.Split(text, "\n") {
		prefix, _, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		owners := []string{}
		for _, name := range strings.Split(prefix, ", ") {
			if valid.MatchString(name) {
				owners = append(owners, name)
			}
		}
		if len(owners) > 0 {
			return owners
		}
	}
	return nil
}
func TestRuntimePackageOwnersIgnoreDiversions(t *testing.T) {
	out := "diversion by libssl3t64 from: /lib/libssl.so.3\ndiversion by libssl3t64 to: /lib/libssl.so.3.usr-is-merged\nlibssl3t64:amd64: /lib/libssl.so.3\n"
	names := runtimePackageOwners(out)
	if len(names) != 1 || names[0] != "libssl3t64:amd64" {
		t.Fatal(names)
	}
	if len(runtimePackageOwners("local diversion from: /lib/libc.so.6")) != 0 {
		t.Fatal("notice accepted as package")
	}
}
