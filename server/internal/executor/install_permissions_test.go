package executor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstanceRuntimeTraversableAndDataPrivate(t *testing.T) {
	base := filepath.Join(t.TempDir(), "instance")
	if err := prepareInstanceLayout(base); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "runtime", "bin")
	if err := os.MkdirAll(dir, 0750); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "server")
	if err := os.WriteFile(exe, []byte("binary"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := prepareRuntimePermissions(filepath.Join(base, "runtime")); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]os.FileMode{base: 0711, filepath.Join(base, "data"): 0700, filepath.Join(base, "logs"): 0700, dir: 0755, exe: 0755} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != want {
			t.Fatal(p, st.Mode().Perm(), want)
		}
	}
}
