package executor

import (
	"context"
	"fmt"
	"opsview/internal/model"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Runs only on the disposable Linux CI host, never against production services.
func TestLiveScheduleScripts(t *testing.T) {
	if os.Getenv("OPSVIEW_SCHEDULE_INTEGRATION") != "1" {
		t.Skip("requires disposable root Linux/systemd host")
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Fatal("root Linux required")
	}
	e := &Engine{Root: t.TempDir()}
	cfg := func(script string) model.Config {
		return model.Config{"operation": "shell", "cron": "* * * * *", "user": "opsview-task", "workingDirectory": "/tmp", "timeoutSeconds": 60, "script": script, "environment": model.Config{"VALUE": "hidden-fixture-54184"}}
	}
	for _, code := range []int{0, 7} {
		logs := []string{}
		result, err := e.scheduleExecute(context.Background(), model.Operation{Name: "schedule.execute", Resource: model.Resource{Config: cfg(fmt.Sprintf("echo \"$VALUE\"; exit %d", code))}}, func(s string) { logs = append(logs, s) })
		if (err != nil) != (code != 0) || result.Int("exitCode") != code {
			t.Fatalf("exit %d: %v %v", code, result, err)
		}
		if strings.Contains(strings.Join(logs, "\n"), "hidden-fixture-54184") {
			t.Fatal("secret leaked")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	pid := 0
	result, err := e.scheduleExecute(ctx, model.Operation{Name: "schedule.execute", Resource: model.Resource{Config: cfg("sleep 300 &\necho child=$!\nwait")}}, func(s string) {
		if strings.HasPrefix(s, "child=") {
			pid, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(s, "child=")))
		}
	})
	if err == nil || result.Int("exitCode") != -1 || pid <= 0 {
		t.Fatal(result, err, pid)
	}
	for i := 0; i < 30; i++ {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("cancel left a child process running", pid)
}
