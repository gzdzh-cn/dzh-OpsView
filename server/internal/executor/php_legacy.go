package executor

import (
	"context"
	"fmt"
	"opsview/internal/catalog"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
)

func legacyPHP(version string) bool {
	return strings.HasPrefix(version, "7.") || strings.HasPrefix(version, "8.0.")
}

type phpBuildDependency struct {
	name, source, digest string
	subdir               string
}

var legacyPHPDependencies = []phpBuildDependency{
	{"openssl", "https://github.com/openssl/openssl/releases/download/OpenSSL_1_1_1w/openssl-1.1.1w.tar.gz", "cf3098950cb4d853ad95c0841f1f9c6d3dc102dccfcacd521d93925208b76ac8", ""},
	{"libxml2", "https://download.gnome.org/sources/libxml2/2.9/libxml2-2.9.14.tar.xz", "60d74a257d1ccec0475e749cba2f21559e48139efba6ff28224357c7c798dfee", ""},
	{"icu", "https://github.com/unicode-org/icu/releases/download/release-60-3/icu4c-60_3-src.tgz", "476287b17db6e0b7da230dce4b58e8e5669b1510847f82cab3647920f1374390", "source"},
	{"curl", "https://curl.se/download/curl-7.88.1.tar.xz", "1dae31b2a7c1fe269de99c0c31bb488346aab3459b5ffca909d6938249ae415f", ""},
}

// Build legacy ABI dependencies privately with a fixed digest. No replacement
// system libraries or global LD_LIBRARY_PATH; static PIC libraries travel with
// the runtime so extension build helpers remain usable after relocation.
func (e *Engine) buildLegacyPHPDependencies(ctx context.Context, base, builder string, log func(string)) (string, error) {
	root := filepath.Join(base, ".php-deps")
	prefix := filepath.Join(root, "prefix")
	if err := os.MkdirAll(prefix, 0755); err != nil {
		return "", err
	}
	if err := run(ctx, log, "", nil, nil, "chown", "-R", builder+":"+builder, root); err != nil {
		return "", err
	}
	for _, dep := range legacyPHPDependencies {
		archive := filepath.Join(e.Root, "downloads", "php-dependency-"+dep.name+".archive")
		if err := download(ctx, dep.source, dep.digest, archive, log); err != nil {
			return "", err
		}
		source := filepath.Join(root, dep.name)
		if err := extractArchive(ctx, archive, dep.source, source, true); err != nil {
			return "", err
		}
		if err := run(ctx, log, "", nil, nil, "chown", "-R", builder+":"+builder, source); err != nil {
			return "", err
		}
		work := filepath.Join(source, dep.subdir)
		build := func(args ...string) error {
			return run(ctx, log, work, []string{"CFLAGS=-O2 -fPIC", "CXXFLAGS=-O2 -fPIC -std=c++11", "PKG_CONFIG_PATH=" + prefix + "/lib/pkgconfig"}, nil, "runuser", append([]string{"-u", builder, "--"}, args...)...)
		}
		log("编译旧 PHP 隔离依赖：" + dep.name)
		var args []string
		switch dep.name {
		case "openssl":
			args = []string{"./config", "--prefix=" + prefix, "--openssldir=/etc/ssl", "--libdir=lib", "no-shared", "-fPIC"}
		case "libxml2":
			args = []string{"./configure", "--prefix=" + prefix, "--disable-shared", "--enable-static", "--with-pic", "--without-python", "--without-lzma", "--without-zlib", "--disable-maintainer-mode"}
		case "icu":
			args = []string{"./configure", "--prefix=" + prefix, "--libdir=" + prefix + "/lib", "--disable-shared", "--enable-static", "--with-data-packaging=static", "--disable-tests", "--disable-samples"}
		case "curl":
			args = []string{"./configure", "--prefix=" + prefix, "--disable-shared", "--enable-static", "--with-pic", "--with-openssl=" + prefix, "--with-ca-bundle=/etc/ssl/certs/ca-certificates.crt", "--with-ca-path=/etc/ssl/certs", "--without-libpsl", "--without-libidn2", "--without-librtmp", "--without-zstd", "--without-brotli", "--without-nghttp2", "--without-libssh2", "--disable-ldap", "--disable-ldaps"}
		}
		if err := build(args...); err != nil {
			return "", fmt.Errorf("%s: %w", dep.name, err)
		}
		if err := build("make", "-j2"); err != nil {
			return "", err
		}
		target := "install"
		if dep.name == "openssl" {
			target = "install_sw"
		}
		if err := build("make", target); err != nil {
			return "", err
		}
		for _, name := range []string{"LICENSE", "COPYING", "LICENSE.txt", "license.html"} {
			b, err := os.ReadFile(filepath.Join(source, name))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return "", err
			}
			dir := filepath.Join(prefix, "licenses", dep.name)
			if err = os.MkdirAll(dir, 0755); err != nil {
				return "", err
			}
			if err = security.WriteAtomic(filepath.Join(dir, name), b, 0644); err != nil {
				return "", err
			}
		}
	}
	// PHP 7.0/7.2 expect the retired freetype-config helper. Translate its
	// supported read-only switches to the installed system pkg-config metadata.
	if err := security.WriteAtomic(filepath.Join(prefix, "bin", "freetype-config"), []byte(legacyFreetypeConfig), 0755); err != nil {
		return "", err
	}
	return prefix, nil
}

func phpConfigureArgs(entry catalog.Entry, base, dependencyPrefix string) []string {
	args := []string{"./configure", "--prefix=" + base + "/runtime", "--with-config-file-path=" + base + "/conf", "--with-config-file-scan-dir=" + base + "/conf/php.d", "--enable-fpm", "--disable-cgi", "--disable-phpdbg", "--with-fpm-user=www-data", "--with-fpm-group=www-data", "--enable-opcache", "--enable-mbstring", "--enable-bcmath", "--enable-exif", "--enable-intl", "--with-zlib", "--with-mysqli=mysqlnd", "--with-pdo-mysql=mysqlnd", "--with-pgsql", "--with-pdo-pgsql"}
	if !legacyPHP(entry.Version) {
		return append(args, "--disable-opcache-jit", "--with-curl", "--with-openssl", "--with-zip", "--enable-gd", "--with-jpeg", "--with-freetype")
	}
	args = append(args, "--with-openssl="+dependencyPrefix, "--with-curl="+dependencyPrefix)
	if strings.HasPrefix(entry.Version, "7.0.") || strings.HasPrefix(entry.Version, "7.2.") {
		args = append(args, "--with-libxml-dir="+dependencyPrefix, "--with-icu-dir="+dependencyPrefix, "--enable-zip", "--with-gd", "--with-jpeg-dir=/usr", "--with-freetype-dir="+dependencyPrefix)
	} else {
		args = append(args, "--with-zip", "--enable-gd", "--with-jpeg", "--with-freetype")
	}
	return args
}

func relocatePHPDependencyHelpers(runtimePath, from, to string) error {
	for _, rel := range []string{"compat/lib/pkgconfig", "compat/bin", "compat/lib"} {
		dir := filepath.Join(runtimePath, rel)
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			p := filepath.Join(dir, entry.Name())
			if !strings.HasSuffix(p, ".pc") && !strings.HasSuffix(p, "-config") && !strings.HasSuffix(p, ".la") {
				continue
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			st, err := entry.Info()
			if err != nil {
				return err
			}
			if err = os.WriteFile(p, []byte(strings.ReplaceAll(string(b), from, to)), st.Mode().Perm()); err != nil {
				return err
			}
		}
	}
	return nil
}

const legacyFreetypeConfig = `#!/bin/sh
case "$1" in
 --cflags|--libs) exec pkg-config "$1" freetype2 ;;
 --version|--ftversion) exec pkg-config --modversion freetype2 ;;
 --prefix|--exec-prefix) printf '%s\n' /usr ;;
 *) exit 1 ;;
esac
`
