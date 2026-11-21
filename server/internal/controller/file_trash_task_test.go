package controller

import (
	"encoding/json"
	"opsview/internal/model"
	"testing"
)

func TestPermanentTrashCleanupRunsAsTask(t *testing.T) {
	for _, action := range []string{"purge", "empty"} {
		t.Run(action, func(t *testing.T) {
			c, h := testController(t)
			cookie, csrf := setupAndLogin(t, h)
			w := request(h, "POST", "/api/v1/files", model.Config{"scope": "server", "action": "trash", "trashAction": action, "id": "test-trash", "confirmed": true}, cookie, csrf, "")
			if w.Code != 202 {
				t.Fatal(w.Code, w.Body.String())
			}
			var task model.Task
			if err := json.Unmarshal(w.Body.Bytes(), &task); err != nil {
				t.Fatal(err)
			}
			stored, err := c.App.Store.Task(task.ID)
			if err != nil || stored.Operation != "files.trash" || stored.State != "queued" {
				t.Fatal("trash cleanup was not queued", stored, err)
			}
		})
	}
}
