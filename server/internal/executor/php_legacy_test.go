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

func TestPHPIntlPatchPreservesICUAPIAndRejectsUnexpectedSource(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "ext/intl/msgformat/msgformat_helpers.cpp")
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	source := `class MessageFormatAdapter {
 static const Formattable::Type* getArgTypeList(const MessageFormat& m, int32_t& count);
 static const MessagePattern getMessagePattern(MessageFormat* m);
};
const Formattable::Type* MessageFormatAdapter::getArgTypeList(const MessageFormat& m, int32_t& count) { return m.getArgTypeList(count); }
const MessagePattern MessageFormatAdapter::getMessagePattern(MessageFormat* m) { return m->msgPattern; }
MessageFormatAdapter::getArgTypeList(m, count);
MessageFormatAdapter::getMessagePattern(m);
`
	if err := os.WriteFile(p, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	header := filepath.Join(root, "main", "php_config.h.in")
	if err := os.MkdirAll(filepath.Dir(header), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(header, []byte("#if ZEND_BROKEN_SPRINTF\nint zend_sprintf(char *buffer, const char *format, ...);\n#endif\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := applyPHPCompatibility(root, "7.0.33"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"class MessageFormatAdapter", "m.getArgTypeList(count)", "MessageFormatAdapter::phpGetArgTypeList", "MessageFormatAdapter::phpGetMessagePattern"} {
		if !strings.Contains(string(b), text) {
			t.Fatal(text, string(b))
		}
	}
	if strings.Contains(string(b), "MessageFormatAdapter::getArgTypeList") {
		t.Fatal("colliding symbol retained")
	}
	h, err := os.ReadFile(header)
	if err != nil || !strings.Contains(string(h), "extern \"C\" {\n#endif\nint zend_sprintf") {
		t.Fatal("C++ linkage mismatch", string(h), err)
	}
	if err := applyPHPCompatibility(root, "7.2.34"); err == nil {
		t.Fatal("unexpected source silently patched")
	}
	if err := applyPHPCompatibility("/missing", "7.4.33"); err != nil {
		t.Fatal(err)
	}
}
