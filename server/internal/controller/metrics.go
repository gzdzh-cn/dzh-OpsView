package controller

import (
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
	"net/http"
	"runtime"
	"time"
)

func (c *Controller) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cpuUsage, _ := cpu.PercentWithContext(ctx, 0, false)
	memory, _ := mem.VirtualMemoryWithContext(ctx)
	storage, _ := disk.UsageWithContext(ctx, "/")
	info, _ := host.InfoWithContext(ctx)
	network, _ := gnet.IOCountersWithContext(ctx, false)
	resources, _ := c.App.Store.Resources("")
	counts := map[string]int{}
	for _, v := range resources {
		counts[v.Kind]++
	}
	tasks, _ := c.App.Store.Tasks()
	failed := 0
	running := 0
	for _, t := range tasks {
		if t.State == "failed" || t.State == "interrupted" {
			failed++
		}
		if t.State == "queued" || t.State == "running" {
			running++
		}
	}
	usage := 0.0
	if len(cpuUsage) > 0 {
		usage = cpuUsage[0]
	}
	send(w, 200, map[string]any{"cpu": usage, "cpuCores": runtime.NumCPU(), "memory": memory, "disk": storage, "host": info, "network": network, "counts": counts, "runningTasks": running, "failedTasks": failed, "timestamp": time.Now().UTC().Format(time.RFC3339), "development": c.App.Development})
}
