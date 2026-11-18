package executor

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"opsview/internal/catalog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPHPVersionSpecificBuildFlags(t *testing.T) {
	for _, v := range []string{"7.0.33", "7.2.34", "7.4.33", "8.0.30", "8.2.35", "8.4.27"} {
		args := strings.Join(phpConfigureArgs(catalog.Entry{Version: v}, "/instance", "/private"), " ")
		if legacyPHP(v) {
			if !strings.Contains(args, "--with-openssl=/private") || strings.Contains(args, "--disable-opcache-jit") {
				t.Fatal(v, args)
			}
		} else if !strings.Contains(args, "--disable-opcache-jit") {
			t.Fatal(v, args)
		}
		if strings.HasPrefix(v, "7.0.") || strings.HasPrefix(v, "7.2.") {
			if !strings.Contains(args, "--enable-zip") || !strings.Contains(args, "--with-icu-dir=/private") {
				t.Fatal(v, args)
			}
		} else if !strings.Contains(args, "--with-zip") {
			t.Fatal(v, args)
		}
	}
}
func TestPHPPrivateHelpersRelocateWithoutRewritingBinaries(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"compat/bin/curl-config", "compat/lib/pkgconfig/icu-uc.pc", "compat/lib/libcurl.la", "compat/bin/curl"} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("/old/runtime/compat/lib"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := relocatePHPDependencyHelpers(root, "/old/runtime", "/new/runtime"); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"compat/bin/curl-config", "compat/lib/pkgconfig/icu-uc.pc", "compat/lib/libcurl.la", "compat/bin/curl"} {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		want := "/new/runtime/compat/lib"
		if rel == "compat/bin/curl" {
			want = "/old/runtime/compat/lib"
		}
		if string(b) != want {
			t.Fatal(rel, string(b))
		}
	}
}

func TestVerifiedSourceHardLinksRemainInsideExtractionRoot(t *testing.T) {
	for _, target := range []string{"src/original", "src/../../outside", "/etc/passwd"} {
		t.Run(strings.ReplaceAll(target, "/", "_"), func(t *testing.T) {
			root := t.TempDir()
			archive := filepath.Join(root, "source.tar.gz")
			f, err := os.Create(archive)
			if err != nil {
				t.Fatal(err)
			}
			gz := gzip.NewWriter(f)
			tw := tar.NewWriter(gz)
			b := []byte("source content")
			if err = tw.WriteHeader(&tar.Header{Name: "src/original", Mode: 0644, Size: int64(len(b)), Typeflag: tar.TypeReg}); err != nil {
				t.Fatal(err)
			}
			tw.Write(b)
			tw.WriteHeader(&tar.Header{Name: "src/linked", Typeflag: tar.TypeLink, Linkname: target})
			tw.Close()
			gz.Close()
			f.Close()
			out := filepath.Join(root, "out")
			err = extractArchive(context.Background(), archive, "source.tar.gz", out, true)
			if target != "src/original" {
				if err == nil {
					t.Fatal("escaped link accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(out, "linked"))
			if err != nil || string(got) != string(b) {
				t.Fatal(err, string(got))
			}
		})
	}
}
