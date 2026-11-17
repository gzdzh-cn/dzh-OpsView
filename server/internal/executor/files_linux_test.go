//go:build linux

package executor

import (
	"context"
	"golang.org/x/sys/unix"
	"opsview/internal/model"
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxFileCrossFilesystemMoveRetainsRecoverableSource(t *testing.T) {
	if os.Getenv("OPSVIEW_FILES_ACCEPTANCE") != "true" || os.Getenv("GITHUB_ACTIONS") != "true" || os.Geteuid() != 0 {
		t.Skip("explicit disposable privileged CI")
	}
	e, dir := fileFixture(t)
	other, err := os.MkdirTemp(filepath.Dir(dir), "opsview-file-test-")
	if err != nil {
		t.Skip(err)
	}
	defer os.RemoveAll(other)
	if err = unix.Mount("tmpfs", other, "tmpfs", 0, "size=32m"); err != nil {
		t.Fatal(err)
	}
	defer unix.Unmount(other, 0)
	a, _ := os.Stat(dir)
	b, _ := os.Stat(other)
	_, _, ad := fileOwner(a)
	_, _, bd := fileOwner(b)
	if ad == bd {
		t.Skip("requires two filesystems")
	}
	source := filepath.Join(dir, "source.txt")
	_ = os.WriteFile(source, []byte("cross-device"), 0640)
	result, err := e.fileBatch(context.Background(), "move", model.Config{"paths": []string{source}, "target": other}, func(string) {})
	if err != nil {
		t.Fatal(result, err)
	}
	got, err := os.ReadFile(filepath.Join(other, "source.txt"))
	if err != nil || string(got) != "cross-device" {
		t.Fatal(string(got), err)
	}
	if _, err = os.Stat(source); !os.IsNotExist(err) {
		t.Fatal("source path not moved")
	}
	index, err := e.trashIndex()
	if err != nil || len(index) != 1 {
		t.Fatal("source backup not retained", index, err)
	}
	for _, entry := range index {
		data, err := os.ReadFile(entry.Stored)
		if err != nil || string(data) != "cross-device" {
			t.Fatal(err)
		}
	}
}
func TestLinuxProtectedBindMountAliases(t *testing.T) {
	if os.Getenv("OPSVIEW_FILES_ACCEPTANCE") != "true" || os.Getenv("GITHUB_ACTIONS") != "true" || os.Geteuid() != 0 {
		t.Skip("explicit disposable privileged CI")
	}
	e, dir := fileFixture(t)
	private := filepath.Join(e.Root, "state")
	_ = os.Mkdir(private, 0700)
	secret := filepath.Join(private, "resource.json")
	_ = os.WriteFile(secret, []byte("credential"), 0600)
	alias := filepath.Join(dir, "alias")
	_ = os.WriteFile(alias, nil, 0600)
	if err := unix.Mount(secret, alias, "", unix.MS_BIND, ""); err != nil {
		t.Fatal(err)
	}
	defer unix.Unmount(alias, 0)
	if _, err := e.readFileText(model.Config{"path": alias}); err == nil {
		t.Fatal("bind-mounted private inode exposed")
	}
	listing, err := e.listFiles(context.Background(), model.Config{"path": dir, "hidden": true})
	if err != nil || listing.Int("total") != 0 {
		t.Fatal("bind-mounted secret listed", listing, err)
	}
	search, err := e.fileSearch(context.Background(), model.Config{"path": dir, "query": "alias"}, func(string) {})
	if err != nil || search.Int("total") != 0 {
		t.Fatal("bind-mounted secret searchable", search, err)
	}
	if _, err := e.trashFile(alias); err == nil {
		t.Fatal("mountpoint was deleted")
	}
}
