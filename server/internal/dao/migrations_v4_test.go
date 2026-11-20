package dao

import (
	"opsview/internal/model"
	"testing"
)

func TestFirewallObservationMigrationPreservesResourcesAndEncrypts(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := model.Resource{Kind: "firewall", Name: "existing-rule", Config: model.Config{"ports": "443", "protocol": "tcp", "action": "allow", "source": "any", "scope": "host"}}
	if err = s.Create(&r); err != nil {
		t.Fatal(err)
	}
	var payload string
	if err = s.DB.QueryRow("SELECT config FROM resources WHERE id=?", r.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("DROP TABLE firewall_observations;PRAGMA user_version=3"); err != nil {
		t.Fatal(err)
	}
	_ = s.DB.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	var schema int
	if err = s.DB.QueryRow("PRAGMA user_version").Scan(&schema); err != nil || schema != 6 {
		t.Fatal(schema, err)
	}
	var after string
	if err = s.DB.QueryRow("SELECT config FROM resources WHERE id=?", r.ID).Scan(&after); err != nil || after != payload {
		t.Fatal("resource rewritten", err)
	}
	status := model.Config{"backend": "ufw", "rules": []any{map[string]any{"id": "system-rule", "note": "private note", "ports": "443"}}}
	if err = s.ObserveFirewall(status); err != nil {
		t.Fatal(err)
	}
	if err = s.ObserveFirewall(status); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.DB.QueryRow("SELECT COUNT(*) FROM firewall_observations").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	var encrypted string
	if err = s.DB.QueryRow("SELECT payload FROM firewall_observations").Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if encrypted == "" || encrypted == "private note" {
		t.Fatal("observation not encrypted")
	}
}
