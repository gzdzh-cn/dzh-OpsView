package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

func databaseSoftware(r model.Resource) bool {
	switch r.Config.String("software") {
	case "mysql", "postgresql", "redis":
		return r.Kind == "instance"
	}
	return false
}

func properties(b []byte) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			out[key] = value
		}
	}
	return out
}

func (u *uninstaller) unitState(ctx context.Context, name string) (map[string]string, error) {
	b, err := u.run(ctx, "systemctl", "show", name, "--property=LoadState,ActiveState,SubState,MainPID,Result,ExecMainCode,ExecMainStatus,ExecStart,EnvironmentFiles,FragmentPath,DropInPaths", "--no-pager")
	if err != nil {
		return nil, fmt.Errorf("无法核对服务状态：%s", name)
	}
	return properties(b), nil
}

func (u *uninstaller) units(ctx context.Context, p *uninstallPlan, resources map[string]model.Resource) error {
	expected := map[string]string{
		panelUnit:                       "ExecStart=/opt/opsview/bin/opsview serve ",
		executorUnit:                    "ExecStart=/opt/opsview/bin/opsview-executor ",
		"opsview-firewall-sets.service": "ExecStart=/usr/sbin/ipset restore -exist -file /etc/opsview/firewall-ipsets.restore",
	}
	dbs := map[string]bool{}
	markers := map[string]string{}
	for _, r := range resources {
		if r.Kind == "instance" || r.Kind == "app" || r.Kind == "site-process" {
			if r.Config.String("mode") == "docker" {
				continue
			}
			unit := "opsview-" + r.Kind + "-" + r.ID + ".service"
			expected[unit] = "Description=OpsView " + r.Kind + " " + r.ID + "\n"
			if r.Kind == "instance" {
				markers[unit] = "ExecStart=" + uninstallManaged + "/instances/" + r.ID + "/runtime/"
			} else {
				markers[unit] = "EnvironmentFile=" + uninstallManaged + "/state/" + r.ID + ".env\n"
			}
			dbs[unit] = databaseSoftware(r)
		}
		if r.Kind == "site" {
			expected["opsview-site-php-"+r.ID+".service"] = "Description=OpsView site-php " + r.ID + "\n"
			markers["opsview-site-php-"+r.ID+".service"] = "ExecStart=" + uninstallManaged + "/instances/" + r.Config.String("phpInstanceId") + "/runtime/"
		}
	}
	entries, err := u.fs.entries("/etc/systemd/system")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".service") || entry.IsDir() {
			continue
		}
		match, known := expected[entry.Name()]
		if !known && entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := "/etc/systemd/system/" + entry.Name()
		b, err := u.fs.read(path)
		if err != nil {
			return fmt.Errorf("服务文件类型或路径异常：%s", path)
		}
		if !known {
			if strings.Contains(string(b), uninstallManaged+"/") || strings.Contains(string(b), "/opt/opsview/bin/") {
				return fmt.Errorf("发现未登记的受管服务，请先核对：%s", entry.Name())
			}
			if strings.HasPrefix(entry.Name(), "opsview-") {
				p.Warnings = append(p.Warnings, "保留无法确认归属的服务："+entry.Name())
			}
			continue
		}
		if !strings.Contains(string(b), match) || !strings.Contains(string(b), "Description=OpsView ") {
			return fmt.Errorf("服务内容与安装登记不匹配：%s", entry.Name())
		}
		if marker := markers[entry.Name()]; marker != "" && !strings.Contains(string(b), marker) {
			return fmt.Errorf("服务启动程序或配置不属于登记资源：%s", entry.Name())
		}
		node, err := u.fs.capture(path, true)
		if err != nil {
			return err
		}
		state, err := u.unitState(ctx, entry.Name())
		if err != nil {
			return err
		}
		if state["FragmentPath"] != path || state["DropInPaths"] != "" {
			return fmt.Errorf("服务存在自定义覆盖或来源变化，请先核对：%s", entry.Name())
		}
		p.Units = append(p.Units, uninstallUnit{Name: entry.Name(), File: node, Database: dbs[entry.Name()]})
	}
	// A prefix alone never authorizes stopping a transient script/worker unit.
	list, err := u.run(ctx, "systemctl", "list-units", "--all", "--type=service", "--no-legend", "--plain", "--no-pager")
	if err != nil {
		return errors.New("无法核对后台服务，请确认 systemd 可用")
	}
	for _, line := range strings.Split(string(list), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || (!strings.HasPrefix(fields[0], "opsview-job-") && !strings.HasPrefix(fields[0], "run-")) {
			continue
		}
		name := fields[0]
		if !regexp.MustCompile(`^[A-Za-z0-9_.-]+\.service$`).MatchString(name) {
			continue
		}
		state, err := u.unitState(ctx, name)
		if err != nil {
			return err
		}
		owned := strings.Contains(state["EnvironmentFiles"], uninstallManaged+"/jobs/environment-") || strings.Contains(state["ExecStart"], "/opt/opsview/bin/opsview-plugin-worker ") || strings.Contains(state["ExecStart"], uninstallManaged+"/plugins/")
		if owned {
			p.Units = append(p.Units, uninstallUnit{Name: name, Transient: true, File: uninstallNode{Hash: security.Hash(state["ExecStart"] + state["EnvironmentFiles"])}})
		}
	}
	// Stop panel/executor before scripts and sites, and databases last.
	priority := func(s uninstallUnit) int {
		if s.Name == panelUnit {
			return 0
		}
		if s.Name == executorUnit {
			return 1
		}
		if s.Transient {
			return 2
		}
		if s.Database {
			return 4
		}
		return 3
	}
	sort.Slice(p.Units, func(i, j int) bool {
		a, b := priority(p.Units[i]), priority(p.Units[j])
		if a != b {
			return a < b
		}
		return p.Units[i].Name < p.Units[j].Name
	})
	return nil
}

func (u *uninstaller) verifyUnit(ctx context.Context, unit uninstallUnit) (map[string]string, error) {
	if !unit.Transient {
		if err := u.fs.verify(unit.File, false); err != nil {
			return nil, err
		}
	}
	state, err := u.unitState(ctx, unit.Name)
	if err != nil {
		return nil, err
	}
	if unit.Transient {
		if state["LoadState"] != "not-found" && security.Hash(state["ExecStart"]+state["EnvironmentFiles"]) != unit.File.Hash {
			return nil, fmt.Errorf("后台服务身份已变化：%s", unit.Name)
		}
	} else if state["LoadState"] != "not-found" && (state["DropInPaths"] != "" || state["FragmentPath"] != unit.File.Path) {
		return nil, fmt.Errorf("服务来源已变化：%s", unit.Name)
	}
	return state, nil
}

func stopped(state map[string]string) bool {
	return (state["LoadState"] == "not-found" || state["ActiveState"] == "inactive" || state["ActiveState"] == "failed") && (state["MainPID"] == "0" || state["MainPID"] == "")
}

func (u *uninstaller) stopUnit(ctx context.Context, unit uninstallUnit) error {
	state, err := u.verifyUnit(ctx, unit)
	if err != nil {
		return err
	}
	if stopped(state) {
		return nil
	}
	if unit.Database {
		// systemctl stop may escalate to SIGKILL on TimeoutStopSec. Signal the
		// database explicitly and abort on timeout instead of forcing shutdown.
		if _, err = u.run(ctx, "systemctl", "kill", "--kill-whom=main", "--signal=SIGTERM", unit.Name); err != nil {
			return fmt.Errorf("数据库无法正常停止：%s", unit.Name)
		}
		deadline := time.Now().Add(u.stopTimeout)
		for {
			state, err = u.unitState(ctx, unit.Name)
			if err != nil {
				return err
			}
			if stopped(state) {
				if state["Result"] != "success" || (state["ExecMainCode"] == "1" && state["ExecMainStatus"] != "0") {
					return fmt.Errorf("数据库非正常退出，数据与程序均保留：%s", unit.Name)
				}
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("数据库停止超时，未强制终止：%s", unit.Name)
			}
			if err := u.pause(ctx); err != nil {
				return err
			}
		}
	}
	if _, err = u.run(ctx, "systemctl", "stop", unit.Name); err != nil {
		return fmt.Errorf("停止服务失败：%s", unit.Name)
	}
	state, err = u.unitState(ctx, unit.Name)
	if err != nil || !stopped(state) {
		return fmt.Errorf("服务仍在运行：%s", unit.Name)
	}
	return nil
}

type uninstallContainer struct {
	ID, Created string
	Labels      map[string]string
	Database    bool
	StopSignal  string
}

type uninstallDockerInfo struct {
	ID      string `json:"Id"`
	Created string
	Config  struct {
		Labels     map[string]string
		StopSignal string
	}
	State struct {
		Running   bool
		ExitCode  int
		OOMKilled bool
	}
	Mounts []struct{ Type, Source, Destination string }
}

func ownershipLabels(labels map[string]string) map[string]string {
	out := map[string]string{}
	for _, key := range []string{"opsview.managed", "opsview.resource", "com.docker.compose.project", "com.docker.compose.service"} {
		if value, ok := labels[key]; ok {
			out[key] = value
		}
	}
	return out
}

// StopSignal is optional in Docker's inspect response; index handles its absence.
const uninstallDockerFormat = `{"Id":{{json .Id}},"Created":{{json .Created}},"Config":{"Labels":{{json .Config.Labels}},"StopSignal":{{json (index .Config "StopSignal")}}},"State":{"Running":{{json .State.Running}},"ExitCode":{{json .State.ExitCode}},"OOMKilled":{{json .State.OOMKilled}}},"Mounts":{{json .Mounts}}}`

func (u *uninstaller) dockerInspect(ctx context.Context, id string) (uninstallDockerInfo, error) {
	var row uninstallDockerInfo
	// Exclude environment variables, healthcheck output and command arguments.
	b, err := u.run(ctx, "docker", "inspect", "--type=container", "--format", uninstallDockerFormat, id)
	if err != nil {
		return uninstallDockerInfo{}, errors.New("无法读取容器状态")
	}
	if err := json.Unmarshal(b, &row); err != nil || row.ID != id || row.Created == "" {
		return uninstallDockerInfo{}, errors.New("容器状态无效")
	}
	return row, nil
}

func (u *uninstaller) containers(ctx context.Context, p *uninstallPlan, resources map[string]model.Resource) error {
	b, err := u.run(ctx, "docker", "ps", "--all", "--quiet", "--no-trunc")
	if err != nil {
		for _, r := range resources {
			if r.Config.String("mode") == "docker" || r.Kind == "compose" || (r.Kind == "plugin" && r.Config.String("type") == "application") {
				return errors.New("有受管容器但 Docker 不可用，请先启动 Docker 后卸载")
			}
		}
		p.Warnings = append(p.Warnings, "Docker 不可用，未执行容器清理；系统 Docker 引擎保留")
		return nil
	}
	for _, id := range strings.Fields(string(b)) {
		if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(id) {
			return errors.New("Docker 返回无效容器标识")
		}
		row, err := u.dockerInspect(ctx, id)
		if err != nil {
			return err
		}
		labels := row.Config.Labels
		resource, direct := resources[labels["opsview.resource"]]
		owned := labels["opsview.managed"] == "true" && direct && resource.Config.String("mode") == "docker"
		project := strings.TrimPrefix(labels["com.docker.compose.project"], "opsview-")
		if r, ok := resources[project]; ok && (r.Kind == "compose" || r.Kind == "plugin") && labels["com.docker.compose.project"] == "opsview-"+r.ID {
			owned = true
		}
		if !owned {
			if labels["opsview.managed"] == "true" || strings.HasPrefix(labels["com.docker.compose.project"], "opsview-") {
				return fmt.Errorf("发现未登记的 OpsView 容器，请先核对：%s", id[:12])
			}
			continue
		}
		for _, mount := range row.Mounts {
			if mount.Type != "bind" && mount.Type != "volume" {
				continue
			}
			// Standard Nginx configuration/log mounts are disposable; website
			// roots beneath sites/ are protected individually by their records.
			if mount.Type == "bind" && (mount.Source == uninstallManaged+"/sites" || (strings.HasPrefix(mount.Source, uninstallManaged+"/instances/") && (strings.HasSuffix(mount.Source, "/conf") || strings.HasSuffix(mount.Source, "/logs")))) {
				continue
			}
			if err := u.keep(p, mount.Source); err != nil {
				return err
			}
		}
		signal := strings.TrimPrefix(row.Config.StopSignal, "SIG")
		if signal == "" {
			signal = "TERM"
		}
		if signal != "TERM" && signal != "INT" && signal != "QUIT" {
			return fmt.Errorf("容器停止信号需先核对：%s", id[:12])
		}
		if databaseSoftware(resource) && signal == "QUIT" {
			return fmt.Errorf("数据库容器停止信号不能保证正常关闭：%s", id[:12])
		}
		p.Containers = append(p.Containers, uninstallContainer{ID: row.ID, Created: row.Created, Labels: ownershipLabels(labels), Database: databaseSoftware(resource), StopSignal: signal})
	}
	return nil
}

func (u *uninstaller) stopContainer(ctx context.Context, c uninstallContainer) error {
	return u.processContainer(ctx, c, false)
}

func (u *uninstaller) removeContainer(ctx context.Context, c uninstallContainer) error {
	return u.processContainer(ctx, c, true)
}

func (u *uninstaller) processContainer(ctx context.Context, c uninstallContainer, remove bool) error {
	// An explicit successful listing distinguishes absence from daemon failure.
	b, err := u.run(ctx, "docker", "ps", "--all", "--quiet", "--no-trunc", "--filter", "id="+c.ID)
	if err != nil {
		return errors.New("Docker 不可用，保留卸载进度")
	}
	if strings.TrimSpace(string(b)) == "" {
		return nil
	}
	row, err := u.dockerInspect(ctx, c.ID)
	if err != nil {
		return err
	}
	a, _ := json.Marshal(c.Labels)
	d, _ := json.Marshal(ownershipLabels(row.Config.Labels))
	if row.ID != c.ID || row.Created != c.Created || string(a) != string(d) {
		return errors.New("容器归属已变化，未删除")
	}
	if row.State.Running {
		signal := c.StopSignal
		if signal == "" {
			signal = "TERM"
		}
		_, err = u.run(ctx, "docker", "kill", "--signal="+signal, c.ID)
		if err != nil {
			return errors.New("容器停止失败")
		}
		deadline := time.Now().Add(u.stopTimeout)
		for {
			row, err = u.dockerInspect(ctx, c.ID)
			if err != nil {
				return err
			}
			if !row.State.Running {
				break
			}
			if time.Now().After(deadline) {
				return errors.New("容器正常停止超时，未强制删除")
			}
			if err := u.pause(ctx); err != nil {
				return err
			}
		}
		if c.Database && (row.State.ExitCode != 0 || row.State.OOMKilled) {
			return errors.New("数据库容器非正常退出，未删除")
		}
	}
	if remove {
		if _, err = u.run(ctx, "docker", "rm", c.ID); err != nil {
			return errors.New("容器删除失败（卷和数据保留）")
		}
	}
	return nil
}
