package executor

import (
	"context"
	"errors"
	"net"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func managedSiteProcess(r model.Resource) model.Resource {
	c := cloneConfig(r.Config)
	c["workingDirectory"] = r.Config.String("documentRoot")
	if c.String("user") == "" || c.String("user") == "isolated" {
		c["user"] = "ovs_" + r.ID[:min(12, len(r.ID))]
	}
	return model.Resource{ID: "web-" + r.ID, Kind: "site-process", Name: r.Name, Config: c}
}

// Capture the old unit and state before changing a process, so a later Nginx failure rolls both back.
func (e *Engine) setupManagedSite(ctx context.Context, r model.Resource, log func(string)) (func() error, error) {
	proc := managedSiteProcess(r)
	if r.Config.String("user") != "www-data" {
		if err := ensureUser(ctx, proc.Config.String("user"), r.Config.String("documentRoot"), log); err != nil {
			return nil, err
		}
	}
	old, err := e.load(proc.ID)
	if err != nil || old.Config.Int("port") != proc.Config.Int("port") {
		listener, er := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(proc.Config.Int("port"))))
		if er != nil {
			return nil, errors.New("应用端口已被占用")
		}
		listener.Close()
	}
	unit := serviceName(proc)
	paths := []string{filepath.Join("/etc/systemd/system", unit), filepath.Join(e.Root, "state", proc.ID+".env"), filepath.Join(e.Root, "state", proc.ID+".json")}
	contents := map[string][]byte{}
	for _, p := range paths {
		b, er := os.ReadFile(p)
		if er != nil && !os.IsNotExist(er) {
			return nil, er
		}
		if er == nil {
			contents[p] = b
		}
	}
	active, _ := output(ctx, "systemctl", "is-active", unit)
	enabled, _ := output(ctx, "systemctl", "is-enabled", unit)
	restore := func() error {
		recovery, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = systemctl(recovery, func(string) {}, "stop", unit)
		_ = systemctl(recovery, func(string) {}, "disable", unit)
		for _, p := range paths {
			if b, ok := contents[p]; ok {
				mode := os.FileMode(0600)
				if strings.HasSuffix(p, ".service") {
					mode = 0644
				}
				if er := security.WriteAtomic(p, b, mode); er != nil {
					return er
				}
			} else {
				_ = os.Remove(p)
			}
		}
		if er := run(recovery, func(string) {}, "", nil, nil, "systemctl", "daemon-reload"); er != nil {
			return er
		}
		if strings.TrimSpace(enabled) == "enabled" {
			if er := systemctl(recovery, func(string) {}, "enable", unit); er != nil {
				return er
			}
		}
		if strings.TrimSpace(active) == "active" {
			return systemctl(recovery, func(string) {}, "start", unit)
		}
		return nil
	}
	if _, err = e.app(ctx, model.Operation{Name: "app.apply", Resource: proc, Params: model.Config{}}, log); err != nil {
		rollback := restore()
		if rollback != nil {
			return nil, errors.New("进程配置失败且回退未完成，请检查服务日志")
		}
		return nil, err
	}
	if !r.Config.Bool("autoStart") {
		if err = systemctl(ctx, log, "disable", unit); err != nil {
			_ = restore()
			return nil, err
		}
	}
	return restore, nil
}
