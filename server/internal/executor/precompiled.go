package executor

import (
	"context"
	"errors"
	"opsview/internal/catalog"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
)

func hostSystem() string {
	values := map[string]string{}
	for _, line := range strings.Split(readText("/etc/os-release"), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			values[k] = strings.Trim(v, "\"'")
		}
	}
	return values["ID"] + ":" + values["VERSION_ID"]
}

// Only trusted catalog entries can introduce artifacts; imported entries require an Ed25519 signature.
func (e *Engine) installPrecompiled(ctx context.Context, r model.Resource, entry catalog.Entry, base string, log func(string)) error {
	var artifact catalog.BinaryArtifact
	for _, a := range entry.Binaries {
		if a.System == hostSystem() {
			artifact = a
			break
		}
	}
	if artifact.Source == "" {
		return errors.New("此版本没有经过当前系统验证的极速安装包，请选择编译安装")
	}
	if err := catalog.Validate(entry); err != nil {
		return err
	}
	if err := run(ctx, log, "", []string{"DEBIAN_FRONTEND=noninteractive"}, nil, "apt-get", "update"); err != nil {
		return err
	}
	deps := []string{"install", "-y", "--no-install-recommends", "ca-certificates"}
	deps = append(deps, artifact.RuntimePackages...)
	if entry.Software == "postgresql" {
		deps = append(deps, "patchelf")
	}
	if err := run(ctx, log, "", []string{"DEBIAN_FRONTEND=noninteractive"}, nil, "apt-get", deps...); err != nil {
		return err
	}
	archive := filepath.Join(e.Root, "downloads", entry.ID+"-"+strings.ReplaceAll(artifact.System, ":", "-")+".tar.gz")
	if err := download(ctx, artifact.Source, artifact.SHA256, archive, log); err != nil {
		return err
	}
	runtimePath := filepath.Join(base, "runtime")
	children, err := os.ReadDir(runtimePath)
	if err != nil {
		return err
	}
	if len(children) > 0 {
		return errors.New("运行时目录非空，请先核对保留的数据")
	}
	if err = extractArchive(ctx, archive, artifact.Source, runtimePath, false); err != nil {
		return err
	}
	if entry.Software == "postgresql" {
		// Upstream tools contain the build prefix in RUNPATH. Resolve libpq
		// from this instance after relocating the verified package.
		if err = filepath.WalkDir(filepath.Join(runtimePath, "bin"), func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() || d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if _, er := output(ctx, "patchelf", "--print-rpath", path); er != nil {
				return nil
			}
			return run(ctx, log, "", nil, nil, "patchelf", "--set-rpath", "$ORIGIN/../lib", path)
		}); err != nil {
			return err
		}
	}
	if entry.Software == "php" {
		if err = relocatePHPDependencyHelpers(runtimePath, artifact.BuildPrefix, runtimePath); err != nil {
			return err
		}
		// The binaries use explicit ini/FPM paths. Relocate the two upstream build helper scripts for PECL.
		for _, name := range []string{"php-config", "phpize"} {
			path := filepath.Join(runtimePath, "bin", name)
			if err = regularManagedFile(path, runtimePath); err != nil {
				return err
			}
			b, er := os.ReadFile(path)
			if er != nil {
				return er
			}
			if er = security.WriteAtomic(path, []byte(strings.ReplaceAll(string(b), artifact.BuildPrefix, runtimePath)), 0755); er != nil {
				return er
			}
		}
	}
	executable := map[string]string{"nginx": "sbin/nginx", "php": "bin/php", "redis": "bin/redis-server", "postgresql": "bin/postgres"}[entry.Software]
	if executable == "" {
		return errors.New("此软件没有预编译初始化器")
	}
	args := []string{"--version"}
	if entry.Software == "nginx" {
		args = []string{"-v"}
	}
	if err = run(ctx, log, "", nil, nil, filepath.Join(runtimePath, executable), args...); err != nil {
		return errors.New("预编译包不能在当前系统运行，已保留诊断现场")
	}
	if r.Config.Bool("stageRuntime") && entry.Software == "nginx" {
		return prepareRuntimePermissions(base + "/runtime")
	}
	return e.configureNative(ctx, r, base, log)
}
