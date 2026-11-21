package executor

import (
	"context"
	"encoding/json"
	"errors"
	"opsview/internal/logic"
	"opsview/internal/model"
	"opsview/internal/security"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type siteRuntimeTransaction struct {
	Version int               `json:"version"`
	Site    string            `json:"site"`
	Token   string            `json:"token"`
	Phase   string            `json:"phase"`
	Files   map[string][]byte `json:"files"`
	Active  bool              `json:"active"`
	Enabled bool              `json:"enabled"`
}

func (e *Engine) siteRuntimePaths(id string) []string {
	proc := model.Resource{ID: "web-" + id, Kind: "site-process"}
	return []string{filepath.Join("/etc/systemd/system", serviceName(proc)), filepath.Join(e.Root, "state", proc.ID+".env"), filepath.Join(e.Root, "state", proc.ID+".json")}
}
func (e *Engine) runtimeTransactionPath(id string) string {
	return filepath.Join(e.Root, "site-users", "transaction-"+id+".json")
}
func (e *Engine) writeRuntimeTransaction(t siteRuntimeTransaction) error {
	if err := os.MkdirAll(filepath.Dir(e.runtimeTransactionPath(t.Site)), 0700); err != nil {
		return err
	}
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return security.WriteAtomic(e.runtimeTransactionPath(t.Site), b, 0600)
}
func (e *Engine) readRuntimeTransaction(id string) (siteRuntimeTransaction, error) {
	var t siteRuntimeTransaction
	b, err := os.ReadFile(e.runtimeTransactionPath(id))
	if err != nil {
		return t, err
	}
	if err = json.Unmarshal(b, &t); err != nil {
		return t, err
	}
	if t.Version != 1 || t.Site != id || !logic.Name.MatchString(id) || t.Token == "" {
		return t, errors.New("运行配置事务记录无效")
	}
	allowed := map[string]bool{}
	for _, p := range e.siteRuntimePaths(id) {
		allowed[p] = true
	}
	for p := range t.Files {
		if !allowed[p] {
			return t, errors.New("运行配置恢复路径无效")
		}
	}
	return t, nil
}
func (e *Engine) restoreRuntimeTransaction(t siteRuntimeTransaction) error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	unit := serviceName(model.Resource{ID: "web-" + t.Site, Kind: "site-process"})
	// A stopped service cannot race recursive permission rollback.
	if state, err := output(ctx, "systemctl", "show", unit, "--property=LoadState", "--value"); err == nil && strings.TrimSpace(state) != "not-found" {
		if err = systemctl(ctx, func(string) {}, "stop", unit); err != nil {
			return err
		}
		_ = systemctl(ctx, func(string) {}, "disable", unit)
	}
	if err := e.restoreSitePermissions(ctx, t.Site); err != nil {
		return err
	}
	for _, p := range e.siteRuntimePaths(t.Site) {
		if b, ok := t.Files[p]; ok {
			mode := os.FileMode(0600)
			if strings.HasSuffix(p, ".service") {
				mode = 0644
			}
			if err := security.WriteAtomic(p, b, mode); err != nil {
				return err
			}
		} else if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := run(ctx, func(string) {}, "", nil, nil, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if t.Enabled {
		if err := systemctl(ctx, func(string) {}, "enable", unit); err != nil {
			return err
		}
	}
	if t.Active {
		if err := systemctl(ctx, func(string) {}, "start", unit); err != nil {
			return err
		}
	}
	t.Phase = "restored"
	return e.writeRuntimeTransaction(t)
}
func (e *Engine) commitSiteRuntime(id string) error {
	if err := e.commitSitePermissions(id); err != nil {
		return err
	}
	t, err := e.readRuntimeTransaction(id)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	t.Phase = "committed"
	return e.writeRuntimeTransaction(t)
}
func (e *Engine) recoverSiteRuntime(id string) error {
	t, err := e.readRuntimeTransaction(id)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if t.Phase == "committed" || t.Phase == "restored" {
		return nil
	}
	if t.Phase != "applying" {
		return errors.New("运行配置事务阶段无效")
	}
	if site, er := e.load(id); er == nil && site.Config.String("runtimeTransaction") == t.Token {
		return e.commitSiteRuntime(id)
	}
	return e.restoreRuntimeTransaction(t)
}
func (e *Engine) recoverSiteRuntimes() error {
	paths, err := filepath.Glob(filepath.Join(e.Root, "site-users", "transaction-*.json"))
	if err != nil {
		return err
	}
	for _, p := range paths {
		id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(p), "transaction-"), ".json")
		if err = e.recoverSiteRuntime(id); err != nil {
			return err
		}
	}
	return nil
}
