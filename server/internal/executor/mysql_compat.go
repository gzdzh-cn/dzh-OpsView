package executor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// The Linux amd64 upstream binary expects the original AIO SONAME. New t64
// distributions expose an ABI-compatible amd64 library under another name.
// Keep the compatibility alias inside this root-owned instance, never /usr/lib.
func prepareMySQLAIO(ctx context.Context, runtimePath string, log func(string)) error {
	if _, err := os.Stat("/usr/lib/x86_64-linux-gnu/libaio.so.1"); err == nil {
		return nil
	}
	library := "/usr/lib/x86_64-linux-gnu/libaio.so.1t64"
	st, err := os.Stat(library)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return errors.New("AIO 库不是普通文件")
	}
	dir := filepath.Join(runtimePath, "lib", "opsview-compat")
	if err = os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	if err = os.Symlink(library, filepath.Join(dir, "libaio.so.1")); err != nil {
		return err
	}
	if err = run(ctx, log, "", []string{"DEBIAN_FRONTEND=noninteractive"}, nil, "apt-get", "install", "-y", "--no-install-recommends", "patchelf"); err != nil {
		return err
	}
	return filepath.WalkDir(filepath.Join(runtimePath, "bin"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		needed, err := output(ctx, "patchelf", "--print-needed", path)
		if err != nil || !strings.Contains(needed, "libaio.so.1") {
			return nil
		}
		rel, err := filepath.Rel(filepath.Dir(path), dir)
		if err != nil {
			return err
		}
		return run(ctx, log, "", nil, nil, "patchelf", "--add-rpath", "$ORIGIN/"+rel, path)
	})
}
