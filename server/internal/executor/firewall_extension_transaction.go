package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"opsview/internal/hostfirewall"
	"os"
)

type firewallExtensionTransaction struct {
	Backend string                 `json:"backend"`
	Active  bool                   `json:"active"`
	Before  firewallExtensionState `json:"before"`
	After   firewallExtensionState `json:"after"`
}

func extensionStateRevision(s firewallExtensionState) string {
	b, _ := json.Marshal(s)
	return hostfirewall.Hash(string(b))
}
func (e *Engine) commitFirewallExtensions(ctx context.Context, backend string, before, after firewallExtensionState, active bool, emit func(string)) error {
	tx := firewallExtensionTransaction{backend, active, before, after}
	if err := e.writeFirewallAdmin("feature-pending", tx); err != nil {
		return err
	}
	if err := e.applyFirewallExtensions(ctx, backend, before, after, active, emit); err != nil {
		if rb := e.recoverFirewallExtension(context.Background()); rb != nil {
			return fmt.Errorf("%v；扩展回退待重试：%w", err, rb)
		}
		return err
	}
	if err := e.writeFirewallAdmin("extensions", after); err != nil {
		return err
	}
	return os.Remove(e.firewallAdminPath("feature-pending"))
}
func (e *Engine) recoverFirewallExtension(ctx context.Context) error {
	var tx firewallExtensionTransaction
	if err := e.readFirewallAdmin("feature-pending", &tx); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := e.applyFirewallExtensions(ctx, tx.Backend, tx.After, tx.Before, tx.Active, func(string) {}); err != nil {
		return err
	}
	if err := e.writeFirewallAdmin("extensions", tx.Before); err != nil {
		return err
	}
	return os.Remove(e.firewallAdminPath("feature-pending"))
}
