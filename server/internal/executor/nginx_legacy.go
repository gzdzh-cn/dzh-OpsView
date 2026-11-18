package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const nginxPCRESource = "https://downloads.sourceforge.net/project/pcre/pcre/8.45/pcre-8.45.tar.gz"
const nginxPCRESHA256 = "4e6ce03e0336e8b4a3d6c2b70b1c5e18590a5673a98186da90d4f33c23defc09"

func nginxNeedsPCRE1(version string) bool {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return false
	}
	minor, _ := strconv.Atoi(parts[1])
	patch, _ := strconv.Atoi(parts[2])
	return parts[0] == "1" && (minor < 21 || minor == 21 && patch < 5)
}

// Old Nginx requires PCRE1. Compile the fixed source statically inside the
// instance rather than installing a retired global library on modern systems.
func (e *Engine) nginxConfigureArgs(ctx context.Context, version, base, builder string, log func(string)) ([]string, error) {
	args := []string{"./configure", "--prefix=" + base + "/runtime", "--with-http_ssl_module", "--with-http_v2_module", "--with-http_stub_status_module", "--with-threads", "--with-cc-opt=-Wno-error=deprecated-declarations"}
	if !nginxNeedsPCRE1(version) {
		return args, nil
	}
	archive := filepath.Join(e.Root, "downloads", "nginx-pcre-8.45.tar.gz")
	if err := downloadPinnedMirrors(ctx, []string{"https://netix.dl.sourceforge.net/project/pcre/pcre/8.45/pcre-8.45.tar.gz", "https://ftp.exim.org/pub/pcre/pcre-8.45.tar.gz", nginxPCRESource}, nginxPCRESHA256, archive, log); err != nil {
		return nil, err
	}
	source := filepath.Join(base, ".nginx-pcre")
	if err := extractArchive(ctx, archive, nginxPCRESource, source, true); err != nil {
		return nil, err
	}
	if err := run(ctx, log, "", nil, nil, "chown", "-R", builder+":"+builder, source); err != nil {
		return nil, err
	}
	// Keep the dependency's license with the distributed runtime.
	license, err := os.ReadFile(filepath.Join(source, "LICENCE"))
	if err != nil {
		return nil, fmt.Errorf("PCRE license: %w", err)
	}
	if err = os.WriteFile(filepath.Join(base, "pcre-license"), license, 0644); err != nil {
		return nil, err
	}
	log("使用实例隔离的 PCRE 8.45 静态依赖")
	return append(args, "--with-pcre="+source, "--with-pcre-jit"), nil
}
