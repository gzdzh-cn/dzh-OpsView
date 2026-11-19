package maintenance

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"opsview/internal/runtimeconfig"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"time"
)

func (r *Runner) panel(args []string) error {
	if len(args) != 1 || (args[0] != "on" && args[0] != "off") {
		return errors.New("用法：ov panel on|off；off 仅关闭网页入口，计划和告警继续运行")
	}
	out, err := r.metadata("panel-entry", map[string]any{"enabled": args[0] == "on"})
	if err == nil {
		fmt.Fprint(r.Out, string(out))
	}
	return err
}
func (r *Runner) security(args []string) error {
	if len(args) == 0 {
		return errors.New("用法：ov security status|recover [--yes]")
	}
	if args[0] == "status" && len(args) == 1 {
		out, err := r.metadata("security-status", nil)
		if err == nil {
			fmt.Fprint(r.Out, string(out))
		}
		return err
	}
	if args[0] != "recover" || len(args) > 2 || len(args) == 2 && args[1] != "--yes" {
		return errors.New("用法：ov security status|recover [--yes]")
	}
	if err := r.confirm("恢复网页入口，关闭 BasicAuth、客户端证书、地区和 UA 限制、未认证隐藏、API，并撤销 API/临时授权与所有会话。管理员密码、TOTP、Passkey 及运行地址保留；入口和 IP 另用 ov recover-access --all 恢复。", len(args) == 2); err != nil {
		return err
	}
	out, err := r.metadata("security-recover", nil)
	if err == nil {
		fmt.Fprint(r.Out, string(out))
	}
	return err
}
func (r *Runner) restore(args []string) error {
	var file string
	switchData, yes := false, false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--file":
			i++
			if i >= len(args) {
				return errors.New("缺少备份文件")
			}
			file = args[i]
		case "--switch":
			switchData = true
		case "--yes":
			yes = true
		default:
			return errors.New("用法：ov restore --file <.ovp> [--switch --yes]；默认只恢复到新目录")
		}
	}
	if !filepath.IsAbs(file) || filepath.Ext(file) != ".ovp" {
		return errors.New("请使用 .ovp 备份的绝对路径")
	}
	st, err := os.Lstat(file)
	if err != nil || !st.Mode().IsRegular() {
		return errors.New("备份必须是普通文件")
	}
	parent, err := os.MkdirTemp("/var/lib/opsview", "restore-verified-*")
	if err != nil {
		return err
	}
	u, err := user.Lookup("opsview")
	if err != nil {
		return err
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	if err = os.Chown(parent, uid, gid); err != nil {
		return err
	}
	// The verified restore subprocess runs as opsview; stage root-owned uploads explicitly.
	staged := filepath.Join(parent, "input.ovp")
	in, err := os.Open(file)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(staged, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(out, io.LimitReader(in, (2<<30)+1))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		return errors.Join(copyErr, closeErr)
	}
	if n > 2<<30 {
		return errors.New("归档超过 2 GiB")
	}
	if err = os.Chown(staged, uid, gid); err != nil {
		return err
	}
	defer os.Remove(staged)
	dest := filepath.Join(parent, "panel")
	cmd := exec.Command(r.Executable, "restore-config", "--data", "/var/lib/opsview/panel", "--file", staged, "--output", dest)
	cmd.Stdout = r.Out
	cmd.Stderr = r.Err
	if err = cmd.Run(); err != nil {
		return err
	}
	fmt.Fprintln(r.Out, "已验证恢复目录："+dest+"。API、临时授权已失效；计划暂停，核对后启用。")
	if !switchData {
		fmt.Fprintln(r.Out, "核对后可执行同一命令附加 --switch 完成本机切换；原目录保留供回退。")
		return nil
	}
	if err = r.confirm("停止面板并切换已验证的数据目录及备份运行配置？失败恢复当前目录。", yes); err != nil {
		return err
	}
	cfg, err := runtimeconfig.Load(r.configPath())
	if err != nil {
		return err
	}
	next := cfg
	if b, readErr := os.ReadFile(filepath.Join(dest, "panel.json")); readErr == nil {
		if err = json.Unmarshal(b, &next); err != nil {
			return err
		}
		if err = next.Validate(); err != nil {
			return err
		}
	}
	if err = r.exec("systemctl", "stop", panelUnit); err != nil {
		return err
	}
	previous := "/var/lib/opsview/panel-before-restore-" + time.Now().UTC().Format("20060102T150405.000000000")
	if err = os.Rename("/var/lib/opsview/panel", previous); err != nil {
		_ = r.exec("systemctl", "start", panelUnit)
		return err
	}
	rollback := func(cause error) error {
		_ = r.exec("systemctl", "stop", panelUnit)
		_ = os.Rename("/var/lib/opsview/panel", dest)
		er := os.Rename(previous, "/var/lib/opsview/panel")
		if er == nil {
			er = runtimeconfig.Save(r.configPath(), cfg)
		}
		start := r.exec("systemctl", "start", panelUnit)
		return errors.Join(cause, er, start)
	}
	if err = os.Rename(dest, "/var/lib/opsview/panel"); err != nil {
		_ = os.Rename(previous, "/var/lib/opsview/panel")
		_ = r.exec("systemctl", "start", panelUnit)
		return err
	}
	if err = runtimeconfig.Save(r.configPath(), next); err != nil {
		return rollback(err)
	}
	if err = r.exec("systemctl", "start", panelUnit); err != nil {
		return rollback(err)
	}
	if err = r.health(next); err != nil {
		return rollback(err)
	}
	fmt.Fprintln(r.Out, "恢复切换成功。原数据保留："+previous+"。旧二进制回退必须使用配套的升级前元数据快照。")
	return nil
}
