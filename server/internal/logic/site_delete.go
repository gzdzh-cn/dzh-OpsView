package logic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"opsview/internal/model"
	"opsview/internal/scheduling"
	"opsview/internal/security"
	"path/filepath"
	"sort"
	"strings"
)

// All snapshots are resolved from registered resources. Callers never provide paths.
func (a *App) SiteDeletePlan(ctx context.Context, items []model.SiteDeleteItem) (model.Config, error) {
	if len(items) == 0 || len(items) > 100 {
		return nil, errors.New("请选择 1–100 个站点")
	}
	all, err := a.Store.Resources("")
	if err != nil {
		return nil, err
	}
	byID := map[string]model.Resource{}
	for _, r := range all {
		byID[r.ID] = r
	}
	seen := map[string]bool{}
	details := []model.Config{}
	locks := map[string]bool{}
	snapshots := []any{}
	for _, item := range items {
		site, ok := byID[item.ID]
		if !ok || site.Kind != "site" || seen[item.ID] {
			return nil, errors.New("站点不存在或重复选择")
		}
		seen[item.ID] = true
		if site.Revision != item.Revision {
			return nil, errors.New("站点已修改，请重新预览")
		}
		inspection, er := a.Executor.Execute(ctx, model.Operation{Name: "site.delete-preview", Resource: site, Params: model.Config{}}, func(string) {})
		if er != nil {
			return nil, fmt.Errorf("%s：%w", site.Name, er)
		}
		root := inspection.String("directory")
		dirReason := inspection.String("directoryReason")
		dbReason := ""
		dbName := ""
		dbID := site.Config.String("databaseId")
		if site.Config.String("type") == "proxy" {
			dirReason = "反向代理不删除上游目录"
		}
		db, dbOK := byID[dbID]
		if !dbOK || db.Kind != "database" {
			dbReason = "未关联受管数据库"
		} else {
			dbName = db.Name
			locks[dbID] = true
			locks[db.Config.String("instanceId")] = true
			snapshots = append(snapshots, db)
		}
		scheduleNames := []string{}
		scheduleIDs := []string{}
		for _, other := range all {
			if other.ID == site.ID {
				continue
			}
			if other.Kind == "site" {
				if dbID != "" && other.Config.String("databaseId") == dbID {
					dbReason = "数据库仍被其他站点引用"
				}
				otherRoot := other.Config.String("documentRoot")
				if otherRoot == "" {
					otherRoot = other.Config.String("rootDirectory")
				}
				if root != "" && otherRoot != "" && (sitePathContains(root, otherRoot) || sitePathContains(otherRoot, root)) {
					dirReason = "目录与其他站点相同或交叠"
				}
			}
			if other.Kind == "schedule" {
				for _, id := range scheduling.TargetIDs(other.Config) {
					if id == site.ID || (item.DeleteDatabase && id == dbID) {
						scheduleNames = append(scheduleNames, other.Name)
						scheduleIDs = append(scheduleIDs, other.ID)
						locks[other.ID] = true
						snapshots = append(snapshots, other)
						break
					}
				}
			}
		}
		if item.DeleteDirectory && dirReason != "" {
			return nil, fmt.Errorf("%s：%s", site.Name, dirReason)
		}
		if item.DeleteDatabase && dbReason != "" {
			return nil, fmt.Errorf("%s：%s", site.Name, dbReason)
		}
		services := []string{}
		if site.Config.String("type") == "php" {
			services = append(services, "站点独立 FPM")
		}
		if site.Config.String("type") == "managed" {
			services = append(services, "站点托管应用")
		}
		details = append(details, model.Config{"id": site.ID, "name": site.Name, "domains": site.Config.String("domains"), "revision": site.Revision, "directory": root, "directoryReason": dirReason, "database": dbName, "databaseReason": dbReason, "schedules": scheduleNames, "scheduleIds": scheduleIDs, "services": services, "inspection": inspection})
		locks[site.ID] = true
		locks[site.Config.String("instanceId")] = true
		locks[site.Config.String("phpInstanceId")] = true
		snapshots = append(snapshots, site, inspection)
	}
	keys := []string{}
	for id := range locks {
		if id != "" {
			keys = append(keys, id)
		}
	}
	sort.Strings(keys)
	raw, _ := json.Marshal([]any{items, snapshots})
	return model.Config{"items": details, "fingerprint": security.Hash(string(raw)), "lockIds": keys}, nil
}
func sitePathContains(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	return parent == child || strings.HasPrefix(child, parent+"/")
}

func decodeSiteDeleteItems(v any) ([]model.SiteDeleteItem, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var items []model.SiteDeleteItem
	err = json.Unmarshal(raw, &items)
	return items, err
}

// QueueSiteDeletion derives all locks again, including the shared entry and plans.
func (a *App) QueueSiteDeletion(ctx context.Context, items []model.SiteDeleteItem, fingerprint string) (model.Task, error) {
	plan, err := a.SiteDeletePlan(ctx, items)
	if err != nil {
		return model.Task{}, err
	}
	if fingerprint != "" && plan.String("fingerprint") != fingerprint {
		return model.Task{}, errors.New("关联资源或文件已变化，请重新预览")
	}
	return a.Store.Enqueue(model.Operation{Name: "sites.delete", Resource: model.Resource{ID: "site-delete-" + security.ID(), Kind: "system", Name: "网站删除"}, Params: model.Config{"items": items, "fingerprint": plan.String("fingerprint"), "lockIds": plan["lockIds"]}})
}

func (a *App) deleteSites(ctx context.Context, op model.Operation, log func(string)) (model.Config, error) {
	items, err := decodeSiteDeleteItems(op.Params["items"])
	if err != nil {
		return nil, err
	}
	plan, err := a.SiteDeletePlan(ctx, items)
	if err != nil {
		return nil, err
	}
	if plan.String("fingerprint") != op.Params.String("fingerprint") {
		return nil, errors.New("执行前发现资源变化，未删除，请重新预览")
	}
	details := plan["items"].([]model.Config)
	results := []model.Config{}
	failed := 0
	for n, item := range items {
		if err = ctx.Err(); err != nil {
			break
		}
		site, er := a.Store.Resource(item.ID)
		if er != nil {
			err = er
			break
		}
		row := model.Config{"id": site.ID, "name": site.Name, "state": "running"}
		results = append(results, row)
		save := func() error {
			return a.Store.SaveTaskResult(op.Params.String("resultKey"), model.Config{"items": results, "failed": failed})
		}
		if err = save(); err != nil {
			break
		}
		log(fmt.Sprintf("%d/%d 处理站点 %s", n+1, len(items), site.Name))
		er = func() error {
			for _, id := range details[n]["scheduleIds"].([]string) {
				schedule, e := a.Store.Resource(id)
				if e != nil {
					return e
				}
				if schedule.Config.Bool("enabled") {
					schedule.Config["enabled"] = false
					if e = a.Store.Update(&schedule); e != nil {
						return e
					}
				}
			}
			a.ReloadSchedules()
			params := model.Config{"deleteDirectory": item.DeleteDirectory, "deleteDatabase": item.DeleteDatabase, "deleteLogs": item.DeleteLogs}
			data, e := a.Executor.Execute(ctx, model.Operation{Name: "site.delete", Resource: site, Params: params}, log)
			if data != nil {
				if rs, ok := data["resources"]; ok {
					raw, _ := json.Marshal(rs)
					var linked []model.Resource
					if json.Unmarshal(raw, &linked) == nil {
						if persist := a.Store.ApplyDatabaseResources(linked); persist != nil {
							return fmt.Errorf("现场已变更，关联记录保存失败：%w", persist)
						}
					}
				}
				row["steps"] = data["steps"]
				row["inspection"] = data["inspection"]
			}
			if e != nil {
				return e
			}
			// Backups remain usable after the target is removed: preserve a display snapshot.
			backups, e := a.Store.Resources("backup")
			if e != nil {
				return e
			}
			for _, backup := range backups {
				if backup.Config.String("targetId") == site.ID {
					backup.Config["retainedTargetName"] = site.Name
					if e = a.Store.Update(&backup); e != nil {
						return e
					}
				}
			}
			if e = a.Store.Set("deleted-site:"+site.ID, model.Resource{ID: site.ID, Kind: "site", Name: site.Name, Config: model.Config{}}); e != nil {
				return e
			}
			return a.Store.Delete(site.ID)
		}()
		if er != nil {
			failed++
			row["state"] = "failed"
			row["error"] = er.Error()
			log("删除未完成：" + er.Error())
		} else {
			row["state"] = "succeeded"
			log("站点删除完成；未勾选数据及已有备份保留")
		}
		if err = save(); err != nil {
			break
		}
	}
	succeeded := 0
	for _, row := range results {
		if row.String("state") == "succeeded" {
			succeeded++
		}
	}
	result := model.Config{"items": results, "failed": failed, "succeeded": succeeded}
	if err != nil {
		return result, err
	}
	if failed > 0 {
		return result, fmt.Errorf("%d 个站点删除未完成，请查看逐项结果", failed)
	}
	return result, nil
}
