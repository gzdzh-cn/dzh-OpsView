package maintenance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type uninstallNode struct {
	Path   string
	Device uint64
	Inode  uint64
	Mode   os.FileMode
	Hash   string `json:",omitempty"`
}

// All traversal is relative to pinned directory descriptors. os.Root alone is
// insufficient when its root is /: an intermediate link could point elsewhere
// inside /. Never descend into links, including links installed during a race.
type uninstallFS struct {
	root   *os.Root
	base   string
	mounts map[string]bool
	ctx    context.Context
}

func openUninstallFS(base string) (*uninstallFS, error) {
	if base == "" {
		base = "/"
	}
	r, err := os.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	f := &uninstallFS{root: r, base: base}
	return f, f.refreshMounts()
}

func uninstallPath(p string) bool {
	return filepath.IsAbs(p) && filepath.Clean(p) == p && p != "/" && !strings.ContainsAny(p, "\x00\r\n")
}

func containsPath(parent, child string) bool {
	return child == parent || strings.HasPrefix(child, parent+"/")
}

func nodeFor(path string, st os.FileInfo) uninstallNode {
	s := st.Sys().(*syscall.Stat_t)
	return uninstallNode{Path: path, Device: uint64(s.Dev), Inode: uint64(s.Ino), Mode: st.Mode()}
}

func (n uninstallNode) matches(st os.FileInfo) bool {
	m := nodeFor(n.Path, st)
	return n.Device == m.Device && n.Inode == m.Inode && n.Mode.Type() == m.Mode.Type()
}

func (f *uninstallFS) dir(path string) (*os.Root, error) {
	if path != "/" && !uninstallPath(path) {
		return nil, fmt.Errorf("非法路径：%q", path)
	}
	current, err := f.root.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	for _, part := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' }) {
		st, err := current.Lstat(part)
		if err != nil {
			current.Close()
			return nil, err
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			current.Close()
			return nil, fmt.Errorf("路径包含链接或非目录：%s", path)
		}
		next, err := current.OpenRoot(part)
		current.Close()
		if err != nil {
			return nil, err
		}
		actual, err := next.Stat(".")
		if err != nil || !os.SameFile(st, actual) {
			next.Close()
			return nil, fmt.Errorf("目录身份发生变化：%s", path)
		}
		current = next
	}
	return current, nil
}

func (f *uninstallFS) stat(path string) (os.FileInfo, error) {
	d, err := f.dir(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return d.Lstat(filepath.Base(path))
}

func (f *uninstallFS) read(path string) ([]byte, error) {
	d, err := f.dir(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer d.Close()
	file, err := d.OpenFile(filepath.Base(path), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	st, err := file.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > 32<<20 {
		return nil, fmt.Errorf("非普通文件或文件过大：%s", path)
	}
	return io.ReadAll(io.LimitReader(file, (32<<20)+1))
}

func (f *uninstallFS) prefix(path string, size int) ([]byte, error) {
	d, err := f.dir(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer d.Close()
	file, err := d.OpenFile(filepath.Base(path), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	st, err := file.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return nil, errors.New("归档不是普通文件")
	}
	return io.ReadAll(io.LimitReader(file, int64(size)))
}

func (f *uninstallFS) entries(path string) ([]os.DirEntry, error) {
	d, err := f.dir(path)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	file, err := d.Open(".")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return file.ReadDir(-1)
}

func (f *uninstallFS) capture(path string, hash bool) (uninstallNode, error) {
	st, err := f.stat(path)
	if err != nil {
		return uninstallNode{}, err
	}
	n := nodeFor(path, st)
	if hash && st.Mode().IsRegular() {
		b, err := f.read(path)
		if err != nil {
			return n, err
		}
		h := sha256.Sum256(b)
		n.Hash = hex.EncodeToString(h[:])
	}
	return n, nil
}

func (f *uninstallFS) verify(n uninstallNode, missingOK bool) error {
	st, err := f.stat(n.Path)
	if errors.Is(err, os.ErrNotExist) && missingOK {
		return nil
	}
	if err != nil {
		return err
	}
	if !n.matches(st) {
		return fmt.Errorf("文件身份发生变化，未删除：%s", n.Path)
	}
	if n.Hash != "" {
		actual, err := f.capture(n.Path, true)
		if err != nil {
			return err
		}
		if n.Hash != actual.Hash {
			return fmt.Errorf("文件内容发生变化，请重新核对：%s", n.Path)
		}
	}
	return nil
}

func (f *uninstallFS) refreshMounts() error {
	f.mounts = map[string]bool{}
	if f.base != "/" { // Test filesystems never inspect or modify host mounts.
		return nil
	}
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		p := fields[4]
		for _, escaped := range []string{`\040`, `\011`, `\012`, `\134`} {
			n, _ := strconv.ParseUint(escaped[1:], 8, 8)
			p = strings.ReplaceAll(p, escaped, string(rune(n)))
		}
		if p != "/" {
			f.mounts[p] = true
		}
	}
	return nil
}

func protectedPath(path string, keep []string) bool {
	for _, p := range keep {
		if containsPath(p, path) {
			return true
		}
	}
	return false
}

// Inspect every cleanup tree before confirmation, including mount boundaries.
func (f *uninstallFS) inspect(path string, keep []string) error {
	if protectedPath(path, keep) {
		return nil
	}
	if f.mounts[path] {
		return fmt.Errorf("清理范围包含挂载点，请先核对：%s", path)
	}
	st, err := f.stat(path)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return nil
	}
	entries, err := f.entries(path)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := f.inspect(filepath.Join(path, e.Name()), keep); err != nil {
			return err
		}
	}
	return nil
}

func (f *uninstallFS) remove(n uninstallNode, keep []string) error {
	if err := f.verify(n, true); err != nil {
		return err
	}
	if err := f.refreshMounts(); err != nil {
		return err
	}
	d, err := f.dir(filepath.Dir(n.Path))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer d.Close()
	return f.removeAt(d, filepath.Base(n.Path), n.Path, &n, keep)
}

func (f *uninstallFS) removeAt(parent *os.Root, name, path string, expected *uninstallNode, keep []string) error {
	if f.ctx != nil {
		if err := f.ctx.Err(); err != nil {
			return err
		}
	}
	if protectedPath(path, keep) {
		return nil
	}
	if f.mounts[path] {
		return fmt.Errorf("拒绝跨挂载点清理：%s", path)
	}
	st, err := parent.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if expected != nil && !expected.matches(st) {
		return fmt.Errorf("路径身份发生变化：%s", path)
	}
	if st.IsDir() {
		child, err := parent.OpenRoot(name)
		if err != nil {
			return err
		}
		defer child.Close()
		actual, err := child.Stat(".")
		if err != nil || !os.SameFile(st, actual) {
			return fmt.Errorf("目录身份发生变化：%s", path)
		}
		file, err := child.Open(".")
		if err != nil {
			return err
		}
		entries, err := file.ReadDir(-1)
		file.Close()
		if err != nil {
			return err
		}
		for _, entry := range entries {
			info, err := child.Lstat(entry.Name())
			if err != nil {
				return err
			}
			node := nodeFor(filepath.Join(path, entry.Name()), info)
			if err := f.removeAt(child, entry.Name(), node.Path, &node, keep); err != nil {
				return err
			}
		}
		for _, p := range keep {
			if containsPath(path, p) {
				return nil // Keep the ancestors of retained data in place.
			}
		}
	}
	actual, err := parent.Lstat(name)
	if err != nil || !os.SameFile(st, actual) {
		return fmt.Errorf("删除前路径身份发生变化：%s", path)
	}
	return parent.Remove(name)
}
