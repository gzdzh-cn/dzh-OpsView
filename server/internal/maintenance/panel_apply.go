package maintenance

import (
	"context"
	"fmt"
	"opsview/internal/executor"
	"opsview/internal/model"
	"opsview/internal/runtimeconfig"
	"time"
)

// All normal CLI access changes share the executor's durable 120-second transaction.
// Printing a link never confirms it; confirmation must arrive through the new listener.
func (r *Runner) applyPanelAccess(next runtimeconfig.Config, source string) error {
	client := r.Executor
	if client == nil {
		if err := r.exec("systemctl", "start", executorUnit); err != nil {
			return err
		}
		client = executor.NewClient("/run/opsview/executor.sock")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := client.Execute(ctx, model.Operation{Name: "system.panel-apply", Params: model.Config{"config": next, "source": source, "confirmedImpact": true}}, func(s string) { fmt.Fprintln(r.Out, s) })
	if err != nil {
		return err
	}
	fmt.Fprintf(r.Out, "\n访问设置已暂存。请等待约 3 秒后，在 120 秒内打开以下地址确认：\n%s#runtime-confirm=%s\n\n超时自动恢复原设置；旧防火墙规则保留。云安全组需单独放行。\n", result.String("url"), result.String("confirmToken"))
	return nil
}
