package executor

import (
	"context"
	"errors"
	"fmt"
	"opsview/internal/catalog"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
)

func (e *Engine) phpExtension(ctx context.Context, r model.Resource, p model.Config, mutate bool, log func(string)) (model.Config, error) {
	if r.Config.String("software") != "php" {
		return nil, errors.New("仅适用于 PHP 实例")
	}
	base := filepath.Join(e.Root, "instances", r.ID)
	text, err := output(ctx, base+"/runtime/bin/php", "-c", base+"/conf/php.ini", "-m")
	if err != nil {
		return nil, errors.New("无法读取 PHP 扩展")
	}
	if !mutate {
		names := []string{"Zend OPcache", "fileinfo", "mbstring", "curl", "zip", "gd", "intl", "bcmath", "exif", "mysqli", "pdo_mysql", "pgsql", "pdo_pgsql", "redis", "memcached", "apcu", "imagick", "xdebug"}
		list := []model.Config{}
		branch := strings.Join(strings.Split(r.Config.String("version"), ".")[:2], ".")
		for _, name := range names {
			loaded := false
			for _, line := range strings.Split(text, "\n") {
				if strings.EqualFold(strings.TrimSpace(line), name) {
					loaded = true
				}
			}
			item := model.Config{"name": name, "loaded": loaded, "status": map[bool]string{true: "已加载", false: "未加载"}[loaded], "editable": false, "reason": "由 PHP 构建配方内置"}
			for _, recipe := range catalog.Extensions() {
				if recipe.Name == name && branch >= recipe.MinPHP && branch <= recipe.MaxPHP {
					item["editable"] = true
					item["version"] = recipe.Version
					if _, er := os.Stat(base + "/runtime/opsview-extensions/" + name + "-" + recipe.Version + ".so"); er == nil {
						item["installed"] = true
						if !loaded {
							item["status"] = "已安装，未启用"
						}
					}
					item["reason"] = "官方 PECL 固定版本，修改会重载关联站点"
				}
			}
			list = append(list, item)
		}
		info, er := output(ctx, base+"/runtime/bin/php", "-n", "-r", `echo json_encode(["version"=>PHP_VERSION,"sapi"=>PHP_SAPI,"intSize"=>PHP_INT_SIZE,"threadSafe"=>PHP_ZTS]);`)
		if er != nil {
			info = "{}"
		}
		return model.Config{"extensions": list, "runtime": info}, nil
	}
	name, operation := p.String("name"), p.String("operation")
	if operation != "install" && operation != "enable" && operation != "disable" && operation != "uninstall" {
		return nil, errors.New("扩展操作无效")
	}
	var recipe catalog.Extension
	for _, entry := range catalog.Extensions() {
		if entry.Name == name {
			recipe = entry
			break
		}
	}
	if recipe.Name == "" {
		return nil, errors.New("此扩展由 PHP 构建配方管理，不能单独卸载")
	}
	branch := strings.Join(strings.Split(r.Config.String("version"), ".")[:2], ".")
	if branch < recipe.MinPHP || branch > recipe.MaxPHP {
		return nil, errors.New("没有匹配此 PHP 版本的扩展配方")
	}
	extensionDir := base + "/runtime/opsview-extensions"
	library := extensionDir + "/" + name + "-" + recipe.Version + ".so"
	iniPath := base + "/conf/php.d/opsview-" + name + ".ini"
	old, oldErr := os.ReadFile(iniPath)
	if operation == "install" {
		deps := map[string][]string{"memcached": {"libmemcached-dev", "zlib1g-dev", "libsasl2-dev"}, "imagick": {"libmagickwand-dev"}}
		if len(deps[name]) > 0 {
			args := append([]string{"install", "-y", "--no-install-recommends"}, deps[name]...)
			if err = run(ctx, log, "", []string{"DEBIAN_FRONTEND=noninteractive"}, nil, "apt-get", args...); err != nil {
				return nil, err
			}
		}
		archive := filepath.Join(e.Root, "downloads", "php-"+name+"-"+recipe.Version+".tgz")
		if err = download(ctx, recipe.Source, recipe.SHA256, archive, log); err != nil {
			return nil, err
		}
		stage, er := os.MkdirTemp(base, "extension-build-*")
		if er != nil {
			return nil, er
		}
		defer os.RemoveAll(stage)
		if err = extractArchive(ctx, archive, recipe.Source, stage, true); err != nil {
			return nil, err
		}
		if err = ensureUser(ctx, "ovbuild", "/nonexistent", log); err != nil {
			return nil, err
		}
		if err = run(ctx, log, "", nil, nil, "chown", "-R", "ovbuild:ovbuild", stage); err != nil {
			return nil, err
		}
		build := func(args ...string) error {
			return run(ctx, log, stage, nil, nil, "runuser", append([]string{"-u", "ovbuild", "--"}, args...)...)
		}
		if err = build(base + "/runtime/bin/phpize"); err != nil {
			return nil, err
		}
		if err = build("./configure", "--with-php-config="+base+"/runtime/bin/php-config"); err != nil {
			return nil, err
		}
		if err = build("make", "-j2"); err != nil {
			return nil, err
		}
		so := filepath.Join(stage, "modules", name+".so")
		if err = regularManagedFile(so, stage); err != nil {
			return nil, err
		}
		b, er := os.ReadFile(so)
		if er != nil {
			return nil, er
		}
		if err = os.MkdirAll(extensionDir, 0755); err != nil {
			return nil, err
		}
		if err = security.WriteAtomic(library, b, 0644); err != nil {
			return nil, err
		}
	}
	if operation == "install" || operation == "enable" {
		if _, err = os.Stat(library); err != nil {
			return nil, errors.New("扩展尚未安装")
		}
		directive := "extension"
		if name == "xdebug" {
			directive = "zend_extension"
		}
		if err = security.WriteAtomic(iniPath, []byte(directive+"="+library+"\n"), 0644); err != nil {
			return nil, err
		}
	} else {
		if err = os.Remove(iniPath); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	rollback := func(cause error) (model.Config, error) {
		if oldErr == nil {
			_ = security.WriteAtomic(iniPath, old, 0644)
		} else {
			_ = os.Remove(iniPath)
		}
		_ = e.reloadPHPServices(context.Background(), r, func(string) {})
		return nil, fmt.Errorf("扩展变更失败，已恢复启用状态：%w", cause)
	}
	expected := "false"
	if operation == "install" || operation == "enable" {
		expected = "true"
	}
	check := fmt.Sprintf("exit(extension_loaded(%q) === %s ? 0 : 1);", name, expected)
	if _, err = output(ctx, base+"/runtime/bin/php", "-c", base+"/conf/php.ini", "-r", check); err != nil {
		return rollback(err)
	}
	if err = e.reloadPHPServices(ctx, r, log); err != nil {
		return rollback(err)
	}
	if operation == "uninstall" {
		if err = os.Remove(library); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return model.Config{"lastExtensionChange": name + ":" + operation}, nil
}
func (e *Engine) reloadPHPServices(ctx context.Context, r model.Resource, log func(string)) error {
	if state, err := output(ctx, "systemctl", "is-active", serviceName(r)); err == nil && strings.TrimSpace(state) == "active" {
		if err := systemctl(ctx, log, "reload", serviceName(r)); err != nil {
			return err
		}
	}
	for _, site := range e.states() {
		if site.Config.String("phpInstanceId") == r.ID && !siteUnavailable(site) {
			if err := systemctl(ctx, log, "reload", phpSiteUnit(site)); err != nil {
				return err
			}
		}
	}
	return nil
}
