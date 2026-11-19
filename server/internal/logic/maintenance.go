package logic

import (
	"context"
	"opsview/internal/model"
	"opsview/internal/preferences"
	"time"
)

func (a *App) startMaintenance(ctx context.Context) {
	// Rollback runs independently of network probes and notification delivery.
	a.wg.Add(3)
	go func() {
		defer a.wg.Done()
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		seconds := 0
		evaluating := make(chan struct{}, 1)
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				a.rollbackPreferences()
				seconds++
				if seconds%60 == 0 {
					a.pruneScheduleLogs()
					select {
					case evaluating <- struct{}{}:
						a.wg.Add(1)
						go func() { defer a.wg.Done(); defer func() { <-evaluating }(); a.evaluateAlerts(ctx, false) }()
					default:
					}
				}
				if seconds%3600 == 0 {
					a.refreshLoginRegions(ctx)
				}
			}
		}
	}()
	go func() {
		defer a.wg.Done()
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-a.Store.AlertWake:
				a.evaluateAlerts(ctx, true)
			case <-tick.C:
				a.evaluateAlerts(ctx, true)
			}
		}
	}()
	go func() {
		defer a.wg.Done()
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				a.deliverNotifications(ctx)
			}
		}
	}()
}
func (a *App) rollbackPreferences() { _ = a.Store.RollbackPreferences() }
func (a *App) pruneScheduleLogs() {
	schedules, _ := a.Store.Resources("schedule")
	for _, s := range schedules {
		days := s.Config.Int("retentionDays")
		if days == 0 {
			days = 30
		}
		_ = a.Store.PruneScheduleHistory(s.ID, time.Now().Add(-time.Duration(days)*24*time.Hour).UTC().Format(time.RFC3339))
	}
}
func (a *App) refreshLoginRegions(ctx context.Context) {
	p := model.Config{}
	_ = a.Store.Setting("panel", &p)
	if len(stringSlice(preferences.Merge(p)["loginCountries"])) == 0 {
		return
	}
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	data, err := a.Executor.Execute(probe, model.Operation{Name: "system.panel-region-status"}, func(string) {})
	if err != nil {
		return
	}
	updated, _ := time.Parse(time.RFC3339, data.String("updatedAt"))
	if time.Since(updated) < 7*24*time.Hour {
		return
	}
	_, _ = a.Store.Enqueue(model.Operation{Name: "system.panel-region-update", Resource: model.Resource{ID: "panel-region-data", Kind: "system", Name: "面板地区地址数据"}, Params: model.Config{}})
}
