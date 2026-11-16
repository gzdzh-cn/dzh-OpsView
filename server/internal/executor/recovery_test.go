package executor

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestNginxProbeRequiresNewWorkerRevision(t *testing.T) {
	var current atomic.Bool
	token := "123abc"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if current.Load() && r.URL.Path == "/.opsview-health-"+token {
			w.Header().Set("X-OpsView-Revision", token)
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	_, portText, _ := net.SplitHostPort(server.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	inst := model.Resource{Config: model.Config{"port": port}}
	config := []byte(fmt.Sprintf("server { server_name example.test; location = /.opsview-health-%s { return 204; } }", token))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	if err := probeNginx(ctx, inst, config); err == nil {
		t.Fatal("old worker accepted")
	}
	cancel()
	current.Store(true)
	if err := probeNginx(context.Background(), inst, config); err != nil {
		t.Fatal(err)
	}
}
func TestRevokedPluginCanStillBeUninstalled(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "state"), 0700)
	vault, err := security.OpenVault(filepath.Join(root, "executor.key"))
	if err != nil {
		t.Fatal(err)
	}
	engine := &Engine{Root: root, Vault: vault, TrustDir: filepath.Join(root, "untrusted")}
	resource := model.Resource{ID: "plugin123456789", Kind: "plugin", Name: "custom-worker", Config: model.Config{"pluginId": "custom-worker", "builtin": false}}
	if err = engine.save(resource); err != nil {
		t.Fatal(err)
	}
	if _, err = engine.plugin(context.Background(), model.Operation{Name: "plugin.uninstall", Resource: resource}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if _, err = engine.load(resource.ID); err == nil {
		t.Fatal("plugin remains registered")
	}
}
func TestArchiveRejectsExternalSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	_ = os.WriteFile(outside, []byte("sensitive"), 0600)
	_ = os.Symlink(outside, filepath.Join(dir, "linked"))
	if err := archiveDirectory(dir, filepath.Join(t.TempDir(), "backup.tar.gz")); err == nil {
		t.Fatal("archived external symlink")
	}
}
