package public

import (
	"bytes"
	"io/fs"
	"os"
	"testing"
)

// The standalone panel must serve the same complete build as the development
// file server, including Vite's underscore-prefixed shared Vue helpers.
func TestEmbeddedBuildContainsAllOutputFiles(t *testing.T) {
	built := os.DirFS("dist")
	embedded := Files()
	files := 0
	err := fs.WalkDir(built, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		files++
		want, err := fs.ReadFile(built, path)
		if err != nil {
			return err
		}
		got, err := fs.ReadFile(embedded, path)
		if err != nil {
			t.Errorf("built asset %q is missing from the panel binary: %v", path, err)
			return nil
		}
		if !bytes.Equal(got, want) {
			t.Errorf("embedded asset %q differs from the frontend build", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Fatal("no frontend build files were checked; run make assets first")
	}
}
