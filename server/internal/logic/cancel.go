package logic

import (
	"errors"
	"opsview/internal/dao"
	"strings"
)

// Installation cancellation is intentionally narrower than arbitrary task cancellation.
func (a *App) CancelInstall(id string) error {
	a.taskMu.Lock()
	defer a.taskMu.Unlock()
	var operation, state string
	if err := a.Store.DB.QueryRow("SELECT operation,state FROM tasks WHERE id=?", id).Scan(&operation, &state); err != nil {
		return err
	}
	if operation != "instance.install" && !strings.HasPrefix(operation, "files.") {
		return errors.New("此任务不支持取消")
	}
	if state == "queued" {
		result, err := a.Store.DB.Exec("UPDATE tasks SET state='cancelled',updated_at=? WHERE id=? AND state='queued'", dao.Now(), id)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n == 1 {
			a.Store.Event(id, "任务已取消")
			return nil
		}
	}
	if cancel := a.taskCancels[id]; cancel != nil {
		cancel()
		return nil
	}
	return errors.New("任务已完成或正在恢复现场，请刷新状态")
}
