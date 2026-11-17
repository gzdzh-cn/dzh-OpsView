package maintenance

import (
	"bytes"
	"opsview/internal/executor"
	"opsview/internal/runtimeconfig"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSSLMenuOff(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "panel.json")
	old := runtimeconfig.Default()
	old.PublicHost = "panel.example.com"
	if err := runtimeconfig.Save(config, old); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
case "$*" in
 *ExecStart*) echo '/opt/opsview/bin/opsview serve --config /etc/opsview/panel.json';;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	in, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	var out bytes.Buffer
	r := Runner{In: in, Out: &out, Err: &out, ConfigPath: config, Executor: &executor.Engine{Root: dir, PanelConfigPath: config}}
	args := strings.Fields(Commands[14])
	if len(args) != 2 || args[0] != "ssl" || args[1] != "off" {
		t.Fatal("menu 15 must disable SSL")
	}
	// Selecting the menu action must request confirmation, never fall back to status.
	if err = r.ssl(args[1:]); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatal("SSL menu bypassed confirmation", err)
	}
	unchanged, err := runtimeconfig.Load(config)
	if err != nil || unchanged != old {
		t.Fatal("unconfirmed SSL change modified configuration", err)
	}
	if err = r.ssl(append(args[1:], "--yes")); err != nil {
		t.Fatal(err)
	}
	next, err := runtimeconfig.Load(config)
	if err != nil || next.Scheme() != "http" || next.Port != old.Port || next.PublicHost != old.PublicHost {
		t.Fatal("SSL off did not preserve other access settings", err)
	}
	if _, err = os.Stat(filepath.Join(dir, "panel-pending.json")); err != nil {
		t.Fatal("SSL off must keep automatic rollback until reconnected", err)
	}
	if !strings.Contains(out.String(), "http://panel.example.com:18790#runtime-confirm=") {
		t.Fatal("missing HTTP reconnect link")
	}
	out.Reset()
	if err = r.ssl([]string{"off", "--yes"}); err != nil || !strings.Contains(out.String(), "登录地址：http://panel.example.com:18790") {
		t.Fatal("repeated SSL off must display current login address", err)
	}
}
