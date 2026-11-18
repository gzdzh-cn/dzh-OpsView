package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"opsview/internal/executor"
	"opsview/internal/model"
	"time"
)

func (r *Runner) firewall(args []string) error {
	if len(args) == 0 || len(args) > 2 || (args[0] != "status" && args[0] != "rollback") {
		return errors.New("用法：ov firewall status|rollback [--yes]")
	}
	if len(args) == 2 && args[1] != "--yes" {
		return errors.New("未知参数")
	}
	if args[0] == "rollback" {
		if err := r.confirm("回退 OpsView 当前防火墙事务？", len(args) == 2); err != nil {
			return err
		}
	}
	client := r.Executor
	if client == nil {
		if err := r.exec("systemctl", "start", executorUnit); err != nil {
			return err
		}
		client = executor.NewClient("/run/opsview/executor.sock")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	status, err := client.Execute(ctx, model.Operation{Name: "system.firewall-admin-status"}, func(string) {})
	if err != nil {
		return err
	}
	if args[0] == "rollback" {
		name := "system.firewall-admin-rollback"
		pendingBytes, _ := json.Marshal(status["pending"])
		var pending map[string]any
		if json.Unmarshal(pendingBytes, &pending) == nil {
			if mode, _ := pending["mode"].(bool); mode {
				name = "system.firewall-rollback"
			}
			if feature, _ := pending["feature"].(bool); feature {
				name = "system.firewall-admin-rollback-feature"
			}
		}
		status, err = client.Execute(ctx, model.Operation{Name: name}, func(s string) { fmt.Fprintln(r.Out, s) })
		if err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(r.Out, string(b))
	return nil
}
