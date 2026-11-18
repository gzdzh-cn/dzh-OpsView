package executor

import (
	"context"
	"opsview/internal/hostfirewall"
	"opsview/internal/model"
	"os"
	"time"
)

// The panel scheduler enqueues due updates so downloads and failures appear in
// the task center. The executor remains the authority for applying each change.
func (e *Engine) firewallCountryStatus() (model.Config, error) {
	var state firewallExtensionState
	if err := e.readFirewallAdmin("extensions", &state); err != nil {
		if os.IsNotExist(err) {
			return model.Config{"due": false}, nil
		}
		return nil, err
	}
	due := false
	for _, r := range state.Rules {
		if r.Kind != "region" {
			continue
		}
		t, err := time.Parse(time.RFC3339, r.DataUpdatedAt)
		if err != nil || time.Since(t) >= 7*24*time.Hour {
			due = true
		}
	}
	return model.Config{"due": due}, nil
}

func (e *Engine) updateCountryRules(ctx context.Context, state firewallExtensionState, s hostfirewall.Status, force bool, emit func(string)) (model.Config, error) {
	seen := map[string]bool{}
	for _, r := range state.Rules {
		if r.Kind != "region" {
			continue
		}
		for _, cc := range r.Countries {
			key := r.Family + cc
			if seen[key] {
				continue
			}
			seen[key] = true
			if err := e.prepareCountry(ctx, cc, r.Family, force, emit); err != nil {
				return nil, err
			}
			// IPdeny asks clients to avoid aggressive polling; one request per second.
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
	next := state
	next.Rules = append([]hostfirewall.Rule{}, state.Rules...)
	for i := range next.Rules {
		r := &next.Rules[i]
		if r.Kind != "region" {
			continue
		}
		r.Sets = map[string]string{}
		for _, cc := range r.Countries {
			name, err := e.versionCountry(cc, r.Family)
			if err != nil {
				return nil, err
			}
			r.Sets[cc] = name
		}
		r.DataUpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return model.Config{}, e.commitFirewallExtensions(ctx, s.Backend, state, next, s.Enabled, emit)
}
