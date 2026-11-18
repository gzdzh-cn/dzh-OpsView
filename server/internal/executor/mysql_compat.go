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

// Build the required ncurses ABI instead of falsely aliasing libncurses.so.6
// to ABI 5. Only the legacy upstream clients need it; keep it instance-local.
func (e *Engine) prepareMySQLLegacyClients(ctx context.Context, version, base string, log func(string)) error {
	if !strings.HasPrefix(version, "5.7.") && !strings.HasPrefix(version, "8.0.") {
		return nil
	}
	const sourceURL = "https://invisible-island.net/archives/ncurses/ncurses-6.5.tar.gz"
	const digest = "136d91bc269a9a5785e5f9e980bc76ab57428f604ce3e5a5a90cebc767971cc6"
	if err := run(ctx, log, "", []string{"DEBIAN_FRONTEND=noninteractive"}, nil, "apt-get", "install", "-y", "--no-install-recommends", "build-essential", "patchelf"); err != nil {
		return err
	}
	archive := filepath.Join(e.Root, "downloads", "ncurses-6.5.archive")
	if err := download(ctx, sourceURL, digest, archive, log); err != nil {
		return err
	}
	work := filepath.Join(base, ".ncurses-build")
	if err := extractArchive(ctx, archive, sourceURL, work, true); err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if err := ensureUser(ctx, "ovbuild", "/nonexistent", log); err != nil {
		return err
	}
	if err := run(ctx, log, "", nil, nil, "chown", "-R", "ovbuild:ovbuild", work); err != nil {
		return err
	}
	build := func(args ...string) error {
		return run(ctx, log, work, nil, nil, "runuser", append([]string{"-u", "ovbuild", "--"}, args...)...)
	}
	prefix := filepath.Join(work, "output")
	if err := build("./configure", "--prefix="+prefix, "--with-shared", "--without-normal", "--without-debug", "--without-cxx", "--without-cxx-binding", "--with-termlib", "--with-abi-version=5", "--enable-overwrite", "--disable-ldconfig"); err != nil {
		return err
	}
	if err := build("make", "-j2"); err != nil {
		return err
	}
	if err := build("make", "install.libs"); err != nil {
		return err
	}
	target := filepath.Join(base, "runtime", "lib", "opsview-compat")
	if err := os.MkdirAll(target, 0755); err != nil {
		return err
	}
	if err := run(ctx, log, "", nil, nil, "cp", "-a", prefix+"/lib/.", target); err != nil {
		return err
	}
	if err := run(ctx, log, "", nil, nil, "chown", "-R", "root:root", target); err != nil {
		return err
	}
	if err := filepath.WalkDir(target, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if _, err = output(ctx, "patchelf", "--print-rpath", p); err != nil {
			return nil
		}
		return run(ctx, log, "", nil, nil, "patchelf", "--set-rpath", "$ORIGIN", p)
	}); err != nil {
		return err
	}
	return filepath.WalkDir(filepath.Join(base, "runtime", "bin"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		needed, err := output(ctx, "patchelf", "--print-needed", p)
		if err != nil || (!strings.Contains(needed, "libncurses.so.5") && !strings.Contains(needed, "libtinfo.so.5")) {
			return nil
		}
		return run(ctx, log, "", nil, nil, "patchelf", "--add-rpath", "$ORIGIN/../lib/opsview-compat", p)
	})
}
