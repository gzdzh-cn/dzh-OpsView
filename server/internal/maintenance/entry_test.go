package maintenance

import (
	"bytes"
	"opsview/internal/runtimeconfig"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallEntranceAndUpdatePreservation(t *testing.T) {
	original := runtimeconfig.Default()
	a, err := initOptions(original, []string{"--new-install", "--public-host", "panel.example.com"}, true)
	if err != nil || len(a.Entry) != 32 || a.PublicHost != "panel.example.com" {
		t.Fatal("new installation missing entrance", err)
	}
	b, err := initOptions(original, []string{"--new-install"}, true)
	if err != nil || a.Entry == b.Entry {
		t.Fatal("entrances are not independent", err)
	}
	for _, c := range []runtimeconfig.Config{original, a} {
		for _, flags := range [][]string{nil, {"--new-install"}} {
			updated, err := initOptions(c, flags, false)
			if err != nil || updated.Entry != c.Entry {
				t.Fatal("initialization changed existing entrance", err)
			}
		}
	}
	if _, err = initOptions(original, []string{"--public-host"}, true); err == nil {
		t.Fatal("accepted incomplete flag")
	}
}

func TestEntranceRotationAndDisabledOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "panel.json")
	original := runtimeconfig.Default()
	original.Protocol = "http"
	original.PublicHost = "panel.example.com"
	if err := runtimeconfig.Save(path, original); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	r := Runner{Out: &output, ConfigPath: path}
	for i := 0; i < 2; i++ {
		if err := r.entry([]string{"on"}); err != nil {
			t.Fatal(err)
		}
		current, err := runtimeconfig.Load(path)
		if err != nil || current.Entry == "" || current.Entry == original.Entry || current.Protocol != original.Protocol || current.Port != original.Port {
			t.Fatal("rotation changed unrelated config", err)
		}
		if !strings.Contains(output.String(), current.URL(current.PublicHost)) {
			t.Fatal("missing terminal entrance URL")
		}
		original = current
		output.Reset()
	}
	if err := os.WriteFile(filepath.Join(dir, "panel-pending.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.entry([]string{"off"}); err == nil {
		t.Fatal("overrode pending transaction")
	}
	os.Remove(filepath.Join(dir, "panel-pending.json"))
	if err := r.entry([]string{"off"}); err != nil {
		t.Fatal(err)
	}
	if output.String() != "安全入口已关闭\n" {
		t.Fatal("off disclosed more than confirmation")
	}
	current, err := runtimeconfig.Load(path)
	if err != nil || current.Entry != "" {
		t.Fatal("off did not persist", err)
	}
	output.Reset()
	r.menuColumns()
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 10 || !strings.HasPrefix(lines[0], "0. 退出") || !strings.Contains(lines[0], "10. ") || !strings.Contains(lines[7], "17. 关闭安全入口") || !strings.Contains(lines[8], "18. 卸载 OpsView") {
		t.Fatal("invalid two-column ordering")
	}
}
