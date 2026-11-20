package executor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"opsview/internal/catalog"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
)

// Build as a dedicated user and install into a private staging tree, never through root make.
func (e *Engine) installSource(ctx context.Context, r model.Resource, entry catalog.Entry, base string, log func(string)) error {
	if err := run(ctx, log, "", []string{"DEBIAN_FRONTEND=noninteractive"}, nil, "apt-get", "update"); err != nil {
		return err
	}
	deps := []string{"install", "-y", "--no-install-recommends", "build-essential", "ca-certificates", "xz-utils", "libpcre2-dev", "libssl-dev", "zlib1g-dev", "libreadline-dev", "libicu-dev", "pkg-config", "bison", "flex", "autoconf"}
	if entry.Software == "php" {
		deps = append(deps, "libxml2-dev", "libsqlite3-dev", "libcurl4-openssl-dev", "libonig-dev", "libzip-dev", "libpng-dev", "libjpeg-dev", "libfreetype6-dev", "libxslt1-dev", "libpq-dev", "perl", "libtool", "automake")
	}
	if err := run(ctx, log, "", []string{"DEBIAN_FRONTEND=noninteractive"}, nil, "apt-get", deps...); err != nil {
		return err
	}
	archive := filepath.Join(e.Root, "downloads", entry.ID+".archive")
	if err := download(ctx, entry.Source, entry.SHA256, archive, log); err != nil {
		return err
	}
	source := filepath.Join(base, "source")
	if err := extractArchive(ctx, archive, entry.Source, source, true); err != nil {
		return err
	}
	if entry.Software == "php" {
		if err := applyPHPCompatibility(source, entry.Version); err != nil {
			return err
		}
	}
	builder := "ovbuild"
	if err := ensureUser(ctx, builder, "/nonexistent", log); err != nil {
		return err
	}
	stage := filepath.Join(base, ".stage")
	if err := os.MkdirAll(stage, 0755); err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := run(ctx, log, "", nil, nil, "chown", "-R", builder+":"+builder, source, stage); err != nil {
		return err
	}
	buildEnv := []string{"CFLAGS=-O2", "CXXFLAGS=-O2"}
	dependencyPrefix := ""
	if entry.Software == "php" && legacyPHP(entry.Version) {
		var err error
		dependencyPrefix, err = e.buildLegacyPHPDependencies(ctx, base, builder, log)
		if err != nil {
			return err
		}
		buildEnv = []string{"CFLAGS=-O2 -std=gnu99 -Wno-error=implicit-int -Wno-error=int-conversion -Wno-error=implicit-function-declaration -Wno-error=incompatible-pointer-types", "CXXFLAGS=-O2 -std=c++11", "CPPFLAGS=-I" + dependencyPrefix + "/include", "LDFLAGS=-L" + dependencyPrefix + "/lib -Wl,--exclude-libs,ALL", "PKG_CONFIG_PATH=" + dependencyPrefix + "/lib/pkgconfig", "LIBS=-lstdc++ -ldl -lpthread"}
	}
	build := func(args ...string) error {
		return run(ctx, log, source, buildEnv, nil, "runuser", append([]string{"-u", builder, "--"}, args...)...)
	}
	runtimePath := filepath.Join(base, "runtime")
	log("以低权限构建用户编译；完整日志持续输出")
	switch entry.Software {
	case "nginx":
		args, err := e.nginxConfigureArgs(ctx, entry.Version, base, builder, log)
		if err != nil {
			return err
		}
		if err := build(args...); err != nil {
			return err
		}
	case "php":
		if err := build(phpConfigureArgs(entry, base, dependencyPrefix)...); err != nil {
			return err
		}
	case "redis":
		// Build only the core server/client target. New Redis top-level make also
		// builds optional modules with additional toolchains; these are not part
		// of this managed recipe and must never be loaded implicitly.
		if err := build("make", "-C", "src", "-j2", "BUILD_TLS=yes", "OPTIMIZATION=-O2"); err != nil {
			return err
		}
		if err := build("make", "-C", "src", "PREFIX="+stage+runtimePath, "BUILD_TLS=yes", "OPTIMIZATION=-O2", "install"); err != nil {
			return err
		}
	case "postgresql":
		if err := build("./configure", "--prefix="+runtimePath, "--with-openssl"); err != nil {
			return err
		}
	default:
		return errors.New("此软件不提供源码配方")
	}
	if entry.Software != "redis" {
		if err := build("make", "-j2"); err != nil {
			return err
		}
		stageVariable := "DESTDIR=" + stage
		if entry.Software == "php" {
			stageVariable = "INSTALL_ROOT=" + stage
		}
		if err := build("make", stageVariable, "install"); err != nil {
			return err
		}
	}
	staged := stage + runtimePath
	if entry.Software == "nginx" && nginxNeedsPCRE1(entry.Version) {
		license, err := os.ReadFile(filepath.Join(base, "pcre-license"))
		if err != nil {
			return err
		}
		if err = os.MkdirAll(filepath.Join(staged, "licenses"), 0755); err != nil {
			return err
		}
		if err = security.WriteAtomic(filepath.Join(staged, "licenses", "PCRE-LICENCE"), license, 0644); err != nil {
			return err
		}
		_ = os.Remove(filepath.Join(base, "pcre-license"))
	}
	if dependencyPrefix != "" {
		if err := os.Rename(dependencyPrefix, staged+"/compat"); err != nil {
			return err
		}
		for _, name := range []string{"php-config", "phpize"} {
			p := filepath.Join(staged, "bin", name)
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if err = security.WriteAtomic(p, []byte(strings.ReplaceAll(string(b), dependencyPrefix, runtimePath+"/compat")), 0755); err != nil {
				return err
			}
		}
		// pkg-config and legacy configure helpers are text; relocation is repeated
		// for the verified binary package at its final instance path.
		if err := relocatePHPDependencyHelpers(staged, dependencyPrefix, runtimePath+"/compat"); err != nil {
			return err
		}
	}
	if err := run(ctx, log, "", nil, nil, "chown", "-R", "root:root", staged); err != nil {
		return err
	}
	// runtime is an empty directory allocated by the installer, never an existing installation.
	if children, err := os.ReadDir(runtimePath); err != nil || len(children) > 0 {
		return errors.New("运行时目录不是空目录，保留现场以供核对")
	}
	if err := os.Remove(runtimePath); err != nil {
		return err
	}
	if err := os.Rename(staged, runtimePath); err != nil {
		return err
	}
	if r.Config.Bool("stageRuntime") && entry.Software == "nginx" {
		return prepareRuntimePermissions(base + "/runtime")
	}
	return e.configureNative(ctx, r, base, log)
}
func (e *Engine) configureNative(ctx context.Context, r model.Resource, base string, log func(string)) error {
	runtimePath := base + "/runtime"
	if err := prepareRuntimePermissions(runtimePath); err != nil {
		return err
	}
	user := "ov_" + r.ID[:min(12, len(r.ID))]
	sw := r.Config.String("software")
	if sw != "nginx" {
		if err := ensureUser(ctx, user, base, log); err != nil {
			return err
		}
		if err := run(ctx, log, "", nil, nil, "chown", "-R", user+":"+user, base+"/data", base+"/logs"); err != nil {
			return err
		}
	}
	switch sw {
	case "nginx":
		// Dedicated loopback status listener avoids exposing administrative metrics through sites.
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}
		r.Config["statusPort"] = listener.Addr().(*net.TCPAddr).Port
		listener.Close()
		if err := e.writeNginxBase(r, base); err != nil {
			return err
		}
		if err := e.save(r); err != nil {
			return err
		}
		return installUnit(ctx, r, fmt.Sprintf("Type=forking\nPIDFile=%s/logs/nginx.pid\nExecStart=%s/sbin/nginx -p %s/ -c %s/conf/nginx.conf\nExecReload=%s/sbin/nginx -p %s/ -c %s/conf/nginx.conf -s reload\nExecStop=%s/sbin/nginx -p %s/ -c %s/conf/nginx.conf -s quit\n", base, runtimePath, runtimePath, base, runtimePath, runtimePath, base, runtimePath, runtimePath, base), log)
	case "postgresql":
		data := filepath.Join(base, "data")
		if err := run(ctx, log, "", nil, nil, "chown", "-R", user+":"+user, data); err != nil {
			return err
		}
		pwfile := filepath.Join(base, "pg-password")
		if err := security.WriteAtomic(pwfile, []byte(r.Config.String("password")), 0600); err != nil {
			return err
		}
		defer os.Remove(pwfile)
		if err := run(ctx, log, "", nil, nil, "chown", user+":"+user, pwfile); err != nil {
			return err
		}
		if err := run(ctx, log, "", nil, nil, "runuser", "-u", user, "--", runtimePath+"/bin/initdb", "-D", data, "-U", "postgres", "--pwfile="+pwfile, "--auth=scram-sha-256"); err != nil {
			return err
		}
		conf := fmt.Sprintf("\nport = %d\nlisten_addresses = '127.0.0.1'\n", r.Config.Int("port"))
		f, err := os.OpenFile(data+"/postgresql.conf", os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, err = f.WriteString(conf)
		f.Close()
		if err != nil {
			return err
		}
		return installUnit(ctx, r, fmt.Sprintf("User=%s\nGroup=%s\nExecStart=%s/bin/postgres -D %s\n", user, user, runtimePath, data), log)
	case "redis":
		if len(r.Config.String("password")) < 12 {
			return errors.New("Redis 密码至少 12 位")
		}
		// Only a token-safe password is permitted here; credentials never become process arguments.
		if strings.ContainsAny(r.Config.String("password"), " \"'\\\r\n\x00\t") {
			return errors.New("Redis 密码不允许空白或引号")
		}
		conf := fmt.Sprintf("bind 127.0.0.1\nprotected-mode yes\nport %d\ndaemonize no\ndir %s/data\nlogfile %s/logs/redis.log\nrequirepass %s\nappendonly yes\nappendfsync everysec\nsave 900 1\nsave 300 10\nmaxmemory 256mb\nmaxmemory-policy noeviction\n", r.Config.Int("port"), base, base, r.Config.String("password"))
		if err := security.WriteAtomic(base+"/conf/redis.conf", []byte(conf), 0640); err != nil {
			return err
		}
		if err := run(ctx, log, "", nil, nil, "chown", "root:"+user, base+"/conf/redis.conf"); err != nil {
			return err
		}
		return installUnit(ctx, r, fmt.Sprintf("User=%s\nGroup=%s\nExecStart=%s/bin/redis-server %s/conf/redis.conf\n", user, user, runtimePath, base), log)
	case "php":
		if err := os.MkdirAll(base+"/conf/php.d", 0755); err != nil {
			return err
		}
		ini := "[PHP]\nshort_open_tag = Off\nmax_execution_time = 60\nmax_input_time = 60\nmemory_limit = 128M\npost_max_size = 32M\nupload_max_filesize = 16M\nmax_file_uploads = 20\nfile_uploads = On\ndisplay_errors = Off\nlog_errors = On\nerror_log = " + base + "/logs/php-error.log\ncgi.fix_pathinfo = 0\ndate.timezone = Asia/Shanghai\ndisable_functions = exec,passthru,shell_exec,system,proc_open,popen\n"
		extension, err := output(ctx, runtimePath+"/bin/php-config", "--extension-dir")
		if err != nil {
			return err
		}
		opcache := strings.TrimSpace(extension) + "/opcache.so"
		if err := security.WriteAtomic(base+"/conf/php.d/10-opcache.ini", []byte("zend_extension="+opcache+"\n"), 0644); err != nil {
			return err
		}
		if err := security.WriteAtomic(base+"/conf/php.ini", []byte(ini), 0644); err != nil {
			return err
		}
		fpm := fmt.Sprintf("[global]\npid = %s/logs/php-fpm.pid\nerror_log = %s/logs/fpm.log\n[www]\nuser = %s\ngroup = %s\nlisten = 127.0.0.1:%d\nlisten.allowed_clients = 127.0.0.1\npm = ondemand\npm.max_children = 8\npm.process_idle_timeout = 10s\npm.max_requests = 500\npm.status_path = /opsview-fpm-status\nrequest_slowlog_timeout = 5s\nslowlog = %s/logs/slow.log\n", base, base, user, user, r.Config.Int("port"), base)
		if err := security.WriteAtomic(base+"/conf/php-fpm.conf", []byte(fpm), 0644); err != nil {
			return err
		}
		return installUnit(ctx, r, fmt.Sprintf("Environment=PHP_INI_SCAN_DIR=%s/conf/php.d\nExecStart=%s/sbin/php-fpm --nodaemonize -y %s/conf/php-fpm.conf -c %s/conf/php.ini\nExecReload=/bin/kill -USR2 $MAINPID\n", base, runtimePath, base, base), log)
	}
	return errors.New("此安装包尚未提供初始化器")
}
