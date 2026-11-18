package maintenance

import (
	"bytes"
	"context"
	"opsview/internal/model"
	"testing"
)

type firewallCLIExecutor struct{ calls []string }

func (e *firewallCLIExecutor) Execute(_ context.Context, op model.Operation, _ func(string)) (model.Config, error) {
	e.calls = append(e.calls, op.Name)
	if op.Name == "system.firewall-admin-status" {
		return model.Config{"pending": model.Config{"mode": true}}, nil
	}
	return model.Config{"rolledBack": true}, nil
}
func TestFirewallCLIRollbackUsesActiveTransaction(t *testing.T) {
	var out bytes.Buffer
	e := &firewallCLIExecutor{}
	r := Runner{Executor: e, Out: &out, Err: &out}
	if err := r.firewall([]string{"status"}); err != nil {
		t.Fatal(err)
	}
	if err := r.firewall([]string{"rollback", "--yes"}); err != nil {
		t.Fatal(err)
	}
	if e.calls[len(e.calls)-1] != "system.firewall-rollback" {
		t.Fatal(e.calls)
	}
	if err := r.firewall([]string{"rollback", "--unexpected"}); err == nil {
		t.Fatal("unknown parameter accepted")
	}
}
