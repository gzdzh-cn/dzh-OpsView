package model

import "encoding/json"

type Config map[string]any

func (c Config) String(k string) string { v, _ := c[k].(string); return v }
func (c Config) Int(k string) int {
	switch v := c[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	}
	return 0
}
func (c Config) Bool(k string) bool { v, _ := c[k].(bool); return v }

type Resource struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Config    Config `json:"config"`
	Revision  int    `json:"revision"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}
type Task struct {
	ScheduleID string `json:"scheduleId,omitempty"`
	ID         string `json:"id"`
	ResourceID string `json:"resourceId"`
	Operation  string `json:"operation"`
	State      string `json:"state"`
	Error      string `json:"error,omitempty"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}
type Event struct {
	Sequence  int64  `json:"sequence"`
	TaskID    string `json:"taskId"`
	Message   string `json:"message"`
	CreatedAt string `json:"createdAt"`
}
type Operation struct {
	Name     string   `json:"name"`
	Resource Resource `json:"resource"`
	Params   Config   `json:"params,omitempty"`
}
type Result struct {
	Done     bool   `json:"done,omitempty"`
	Code     string `json:"code,omitempty"`
	Revision string `json:"revision,omitempty"`
	Data     Config `json:"data"`
	Error    string `json:"error,omitempty"`
	Log      string `json:"log,omitempty"`
}
type Audit struct {
	ID         int64  `json:"id"`
	Action     string `json:"action"`
	ResourceID string `json:"resourceId"`
	IP         string `json:"ip"`
	CreatedAt  string `json:"createdAt"`
}

type DuplicateVersion struct{ ResourceID string }

func (e *DuplicateVersion) Error() string {
	return "该软件的准确版本已存在，请管理已有实例或清理保留记录"
}
