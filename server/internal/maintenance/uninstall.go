package maintenance

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"opsview/internal/executor"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

var errUninstalled = errors.New("OpsView 已卸载")

type uninstaller struct {
	fs          *uninstallFS
	run         func(context.Context, string, ...string) ([]byte, error)
	cleanup     func(context.Context) error
	candidates  []string
	stopTimeout time.Duration
	out         io.Writer
}

func nodeOwner(st os.FileInfo) uint32 { return st.Sys().(*syscall.Stat_t).Uid }

func newUninstaller(out io.Writer) (*uninstaller, error) {
	f, err := openUninstallFS("/")
	if err != nil {
		return nil, err
	}
	u := &uninstaller{fs: f, stopTimeout: 90 * time.Second, out: out}
	u.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		callCtx, cancel := context.WithTimeout(ctx, 150*time.Second)
		defer cancel()
		cmd := exec.CommandContext(callCtx, name, args...)
		// Captured command output may contain Docker environment credentials.
		// Only contextual, credential-free errors are returned to the terminal.
		return cmd.Output()
	}
	u.cleanup = func(ctx context.Context) error {
		e := &executor.Engine{Root: uninstallManaged}
		return e.CleanupPanelUninstall(ctx)
	}
	return u, nil
}

func (u *uninstaller) pause(ctx context.Context) error {
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (u *uninstaller) save(p *uninstallPlan) error {
	d, err := u.fs.dir(filepath.Dir(uninstallProgress))
	if err != nil {
		return err
	}
	defer d.Close()
	name := ".opsview-uninstall-" + security.ID()
	f, err := d.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer d.Remove(name)
	err = json.NewEncoder(f).Encode(p)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return d.Rename(name, filepath.Base(uninstallProgress))
}

func (u *uninstaller) step(p *uninstallPlan, key, label string, fn func() error) error {
	if p.Done[key] {
		return nil
	}
	if err := fn(); err != nil {
		return fmt.Errorf("%s失败：%w", label, err)
	}
	p.Done[key] = true
	if err := u.save(p); err != nil {
		return fmt.Errorf("保存卸载进度失败：%w", err)
	}
	fmt.Fprintln(u.out, "已完成："+label)
	return nil
}

func (u *uninstaller) show(p *uninstallPlan) {
	fmt.Fprintln(u.out, "即将卸载 OpsView 及已登记的受管软件。网站、数据库和计划任务将停止。")
	fmt.Fprintln(u.out, "面板保存的密码、配置、密钥、证书、任务记录及本地备份将删除。")
	fmt.Fprintln(u.out, "删除范围（保留项优先；已完成步骤不会重复执行）：")
	for _, unit := range p.Units {
		fmt.Fprintln(u.out, "  服务："+unit.Name)
	}
	for _, c := range p.Containers {
		fmt.Fprintln(u.out, "  容器："+c.ID[:12]+"（保留卷）")
	}
	for _, n := range p.Delete {
		fmt.Fprintln(u.out, "  "+n.Path)
	}
	fmt.Fprintln(u.out, "  命令入口：仅清理仍指向 /opt/opsview/bin/opsview 的 ov、opsview 链接")
	fmt.Fprintln(u.out, "保留源码、网站文件和数据库数据（原路径、权限及所有权不变）：")
	for _, path := range p.Keep {
		fmt.Fprintln(u.out, "  "+path)
	}
	fmt.Fprintln(u.out, "保留系统账号、系统依赖、Docker 引擎及非 OpsView 容器；不删除远端备份。")
	for _, warning := range p.Warnings {
		fmt.Fprintln(u.out, "提示："+warning)
	}
}

func uninstallConfirmed(reader *bufio.Reader, out io.Writer) bool {
	fmt.Fprint(out, "确认卸载 OpsView？输入 y 继续，其他输入取消：")
	line, err := reader.ReadString('\n')
	return err == nil && (line == "y\n" || line == "y\r\n")
}

func (r *Runner) uninstall(args []string) error {
	if len(args) != 0 {
		return errors.New("用法：ov uninstall（必须输入 y 确认，不支持 --yes）")
	}
	if os.Geteuid() != 0 {
		return errors.New("ov 卸载命令需要 root，请使用 sudo ov uninstall")
	}
	u, err := newUninstaller(r.Out)
	if err != nil {
		return err
	}
	defer u.fs.root.Close()
	p, err := u.build(context.Background())
	if err != nil {
		return fmt.Errorf("卸载预检未通过，未执行卸载：%w", err)
	}
	u.show(p)
	if !uninstallConfirmed(r.reader, r.Out) {
		fmt.Fprintln(r.Out, "已取消卸载")
		return nil
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := u.execute(ctx, p); err != nil {
		return err
	}
	return errUninstalled
}

func (u *uninstaller) execute(ctx context.Context, p *uninstallPlan) error {
	u.fs.ctx = ctx
	fail := func(err error) error {
		if p.Done["links"] {
			_ = u.restoreLink()
			p.Done["links"] = false
			_ = u.save(p)
		}
		fmt.Fprintln(u.out, "卸载未完成；保留数据及继续处理所需程序。进度："+uninstallProgress)
		fmt.Fprintln(u.out, "已完成步骤：")
		completed := []string{}
		for key, done := range p.Done {
			if done {
				completed = append(completed, key)
			}
		}
		sort.Strings(completed)
		for _, key := range completed {
			fmt.Fprintln(u.out, "  "+key)
		}
		fmt.Fprintln(u.out, "未完成项：")
		for _, unit := range p.Units {
			if !p.Done["stop:"+unit.Name] {
				fmt.Fprintln(u.out, "  停止服务："+unit.Name)
			}
			if !p.Done["unit:"+unit.Name] {
				fmt.Fprintln(u.out, "  移除服务："+unit.Name)
			}
		}
		for _, c := range p.Containers {
			if !p.Done["container:"+c.ID] {
				fmt.Fprintln(u.out, "  移除容器："+c.ID[:12]+"（保留卷）")
			}
		}
		for _, n := range p.Delete {
			if !p.Done["file:"+n.Path] {
				fmt.Fprintln(u.out, "  清理："+n.Path)
			}
		}
		if !p.Done["auxiliary"] {
			fmt.Fprintln(u.out, "  移除 OpsView 辅助防护配置")
		}
		if !p.Done["links"] {
			fmt.Fprintln(u.out, "  清理 OpsView 命令链接")
		}
		fmt.Fprintln(u.out, "修复错误后重新运行 ov uninstall 核对并继续；未完成项仍保留在卸载清单中。")
		return err
	}
	if !p.Done["cleanup-started"] {
		for _, n := range p.Check {
			if err := u.fs.verify(n, false); err != nil {
				return err
			}
		}
		for _, n := range p.Delete {
			if err := u.fs.verify(n, false); err != nil {
				return err
			}
		}
		fresh := &uninstallPlan{}
		resources, err := u.resources(fresh)
		if err != nil {
			return fail(err)
		}
		if uninstallResourceRevision(resources) != p.ResourceRevision {
			return fail(errors.New("确认期间资源关系发生变化，请重新运行 ov uninstall 核对"))
		}
	}
	if err := u.save(p); err != nil {
		return err
	}
	// Stop applications (including container applications) before databases so
	// their open connections cannot prevent a database's graceful shutdown.
	for _, databasePhase := range []bool{false, true} {
		for _, unit := range p.Units {
			if unit.Database != databasePhase {
				continue
			}
			if p.Done["unit:"+unit.Name] {
				state, err := u.unitState(ctx, unit.Name)
				if err != nil || !stopped(state) {
					return fail(fmt.Errorf("已移除的服务再次运行：%s", unit.Name))
				}
				continue
			}
			if !unit.Transient && p.Done["cleanup-started"] {
				if _, err := u.fs.stat(unit.File.Path); errors.Is(err, os.ErrNotExist) {
					state, err := u.unitState(ctx, unit.Name)
					if err != nil || !stopped(state) {
						return fail(fmt.Errorf("已移除的服务再次出现或仍在运行：%s", unit.Name))
					}
					continue
				}
			}
			if err := u.stopUnit(ctx, unit); err != nil {
				return fail(err)
			}
			p.Done["stop:"+unit.Name] = true
			if err := u.save(p); err != nil {
				return fail(err)
			}
			fmt.Fprintln(u.out, "已停止："+unit.Name)
		}
		for _, c := range p.Containers {
			if c.Database != databasePhase {
				continue
			}
			if err := u.stopContainer(ctx, c); err != nil {
				return fail(err)
			}
			p.Done["stop-container:"+c.ID] = true
			if err := u.save(p); err != nil {
				return fail(err)
			}
			fmt.Fprintln(u.out, "已停止容器："+c.ID[:12])
		}
	}
	if !p.Done["cleanup-started"] {
		// Watchers or a deployment that completed during confirmation can have
		// changed resource relationships. Do not destroy their programs/data.
		for _, n := range p.Check {
			if err := u.fs.verify(n, false); err != nil {
				return fail(err)
			}
		}
		fresh := &uninstallPlan{}
		resources, err := u.resources(fresh)
		if err != nil {
			return fail(err)
		}
		if uninstallResourceRevision(resources) != p.ResourceRevision {
			return fail(errors.New("停止服务期间资源关系发生变化，请重新运行 ov uninstall 核对"))
		}
		registered := map[string]model.Resource{}
		for _, r := range resources {
			registered[r.ID] = r
		}
		if err := u.units(ctx, fresh, registered); err != nil {
			return fail(err)
		}
		known := map[string]bool{}
		for _, unit := range p.Units {
			known[unit.Name] = true
		}
		for _, unit := range fresh.Units {
			if !known[unit.Name] {
				return fail(fmt.Errorf("确认期间出现新后台服务，请重新核对：%s", unit.Name))
			}
		}
		if err := u.containers(ctx, fresh, registered); err != nil {
			return fail(err)
		}
		knownContainers := map[string]bool{}
		for _, c := range p.Containers {
			knownContainers[c.ID] = true
		}
		for _, c := range fresh.Containers {
			if !knownContainers[c.ID] {
				return fail(fmt.Errorf("确认期间出现新受管容器，请重新核对：%s", c.ID[:12]))
			}
		}
	}
	// Recheck links and mount boundaries after writers have stopped, including
	// resumed attempts. New links in retained data must not expose their target
	// to a cleanup list built before confirmation.
	if err := u.fs.refreshMounts(); err != nil {
		return fail(err)
	}
	if err := u.scanKeptLinks(p); err != nil {
		return fail(err)
	}
	p.Keep = uniquePaths(p.Keep)
	if err := u.checkRetainedOverlap(p); err != nil {
		return fail(err)
	}
	for _, n := range p.Delete {
		if strings.HasPrefix(n.Path, "/opt/opsview/bin/") && protectedPath(n.Path, p.Keep) {
			return fail(fmt.Errorf("程序路径与新发现的保留数据交叠：%s", n.Path))
		}
		if _, err := u.fs.stat(n.Path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return fail(err)
		}
		if err := u.fs.inspect(n.Path, p.Keep); err != nil {
			return fail(err)
		}
	}
	p.Done["cleanup-started"] = true
	if err := u.save(p); err != nil {
		return fail(err)
	}
	if err := u.step(p, "auxiliary", "移除 OpsView 辅助防护配置", func() error { return u.cleanup(ctx) }); err != nil {
		return fail(err)
	}
	for _, c := range p.Containers {
		if err := u.step(p, "container:"+c.ID, "移除受管容器 "+c.ID[:12], func() error { return u.removeContainer(ctx, c) }); err != nil {
			return fail(err)
		}
	}
	removeUnit := func(unit uninstallUnit) error {
		return u.step(p, "unit:"+unit.Name, "移除服务 "+unit.Name, func() error {
			if unit.Transient {
				return nil
			}
			// A previous attempt may have removed the file before saving progress.
			if _, err := u.fs.stat(unit.File.Path); errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if _, err := u.verifyUnit(ctx, unit); err != nil {
				return err
			}
			if _, err := u.run(ctx, "systemctl", "disable", unit.Name); err != nil {
				return errors.New("取消服务自启动失败")
			}
			return u.fs.remove(unit.File, p.Keep)
		})
	}
	for _, unit := range p.Units {
		if unit.Name != panelUnit && unit.Name != executorUnit {
			if err := removeUnit(unit); err != nil {
				return fail(err)
			}
		}
	}
	for _, n := range p.Delete {
		if strings.HasPrefix(n.Path, "/opt/opsview/bin/") {
			continue
		}
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if err := u.step(p, "file:"+n.Path, "清理 "+n.Path, func() error { return u.fs.remove(n, p.Keep) }); err != nil {
			return fail(err)
		}
	}
	for _, unit := range p.Units {
		if unit.Name == panelUnit || unit.Name == executorUnit {
			if err := removeUnit(unit); err != nil {
				return fail(err)
			}
		}
	}
	if _, err := u.run(ctx, "systemctl", "daemon-reload"); err != nil {
		return fail(errors.New("重新加载服务配置失败"))
	}
	for _, n := range p.Delete {
		if !strings.HasPrefix(n.Path, "/opt/opsview/bin/") || filepath.Base(n.Path) == "opsview" {
			continue
		}
		if err := u.step(p, "file:"+n.Path, "清理 "+n.Path, func() error { return u.fs.remove(n, p.Keep) }); err != nil {
			return fail(err)
		}
	}
	if err := u.step(p, "links", "清理 OpsView 命令链接", u.removeLinks); err != nil {
		return fail(err)
	}
	// The executable and ov link survive every failure before the final phase.
	// Recreate the link on a finalization failure so the documented retry works.
	for _, n := range p.Delete {
		if n.Path == "/opt/opsview/bin/opsview" {
			if err := u.fs.verify(n, true); err != nil {
				return fail(err)
			}
		}
	}
	progress, err := u.fs.capture(uninstallProgress, false)
	if err != nil {
		return fail(err)
	}
	if err = u.fs.remove(progress, nil); err != nil {
		return fail(err)
	}
	for _, n := range p.Delete {
		if n.Path == "/opt/opsview/bin/opsview" {
			if err := u.fs.remove(n, p.Keep); err != nil {
				_ = u.save(p)
				_ = u.restoreLink()
				return fail(err)
			}
		}
	}
	u.pruneEmpty()
	fmt.Fprintln(u.out, "OpsView 已卸载。源码、网站文件和数据库数据已保留；重新安装不会自动恢复原面板配置。")
	return nil
}

func (u *uninstaller) removeLinks() error {
	for _, path := range []string{"/usr/local/bin/ov", "/usr/local/bin/opsview"} {
		d, err := u.fs.dir(filepath.Dir(path))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		name := filepath.Base(path)
		st, err := d.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			d.Close()
			continue
		}
		if err != nil {
			d.Close()
			return err
		}
		if st.Mode()&os.ModeSymlink == 0 {
			d.Close()
			continue
		}
		target, err := d.Readlink(name)
		if err != nil {
			d.Close()
			return err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		if filepath.Clean(target) == "/opt/opsview/bin/opsview" {
			err = d.Remove(name)
		}
		d.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func (u *uninstaller) restoreLink() error {
	d, err := u.fs.dir("/usr/local/bin")
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Symlink("/opt/opsview/bin/opsview", "ov")
}

func (u *uninstaller) pruneEmpty() {
	for _, path := range []string{"/opt/opsview/bin", "/opt/opsview", uninstallManaged + "/instances", uninstallManaged + "/sites", uninstallManaged + "/plugins", uninstallManaged + "/compose", uninstallManaged, "/var/lib/opsview"} {
		d, err := u.fs.dir(filepath.Dir(path))
		if err == nil {
			_ = d.Remove(filepath.Base(path))
			d.Close()
		}
	}
}
