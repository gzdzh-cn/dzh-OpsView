package executor

import (
	"context"
	"errors"
	"net"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"os/user"
	"strconv"
	"strings"
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
	if err := e.recoverSiteRuntime(r.ID); err != nil {
		return nil, err
	}
	proc := managedSiteProcess(r)
	old, err := e.load(proc.ID)
	var account *user.User
	if err == nil && !r.Config.Bool("runtimeIsolation") && old.Config.String("user") == proc.Config.String("user") {
		account, err = user.Lookup(proc.Config.String("user"))
	} else {
		account, err = e.ensureSiteAccount(ctx, r, log)
	}
	if err != nil {
		return nil, err
	}
	group, err := user.LookupGroupId(account.Gid)
	if err != nil {
		return nil, err
	}
	r.Config["runtimeUser"], r.Config["runtimeGroup"] = account.Username, group.Name
	r.Config["runtimeUID"], r.Config["runtimeGID"] = account.Uid, account.Gid
	if err != nil || old.Config.Int("port") != proc.Config.Int("port") {
		listener, er := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(proc.Config.Int("port"))))
		if er != nil {
			return nil, errors.New("应用端口已被占用")
		}
		listener.Close()
	}
	unit := serviceName(proc)
	paths := e.siteRuntimePaths(r.ID)
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
	txn := siteRuntimeTransaction{Version: 1, Site: r.ID, Token: security.ID(), Phase: "applying", Files: contents, Active: strings.TrimSpace(active) == "active", Enabled: strings.TrimSpace(enabled) == "enabled"}
	if err = e.writeRuntimeTransaction(txn); err != nil {
		return nil, err
	}
	r.Config["runtimeTransaction"] = txn.Token
	restore := func() error { return e.restoreRuntimeTransaction(txn) }
	if r.Config.Bool("runtimeIsolation") && (!old.Config.Bool("runtimeIsolationApplied") || old.Config.String("user") != proc.Config.String("user") || r.Config.String("permissionPreview") != "") {
		if txn.Active {
			if err = systemctl(ctx, log, "stop", unit); err != nil {
				return nil, errors.Join(err, restore())
			}
		}
		uid, _ := strconv.Atoi(account.Uid)
		gid, _ := strconv.Atoi(account.Gid)
		if err = e.applySitePermissions(ctx, r, uid, gid); err != nil {
			if rollback := restore(); rollback != nil {
				return nil, errors.Join(err, rollback)
			}
			return nil, err
		}
		r.Config["permissionRevision"] = txn.Token
	}
	proc.Config["runtimeIsolationApplied"] = r.Config.Bool("runtimeIsolation")
	r.Config["runtimeIsolationApplied"] = r.Config.Bool("runtimeIsolation")
	if _, err = e.siteSandbox(ctx, proc); err != nil {
		_ = restore()
		return nil, err
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
