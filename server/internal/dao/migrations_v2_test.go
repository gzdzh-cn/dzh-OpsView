package dao

import (
	"errors"
	"fmt"
	"opsview/internal/model"
	"sync"
	"testing"
)

func TestExactVersionOccupancySerializesConcurrentCreates(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := model.Resource{Kind: "instance", Name: fmt.Sprintf("candidate-%d", i), Config: model.Config{"software": "redis", "version": "8.10.3"}}
			results <- s.Create(&r)
		}(i)
	}
	wg.Wait()
	close(results)
	success, duplicate := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else {
			var d *model.DuplicateVersion
			if !errors.As(err, &d) || d.ResourceID == "" {
				t.Fatal(err)
			}
			duplicate++
		}
	}
	if success != 1 || duplicate != 11 {
		t.Fatal(success, duplicate)
	}
	resources, _ := s.Resources("instance")
	r := resources[0]
	r.Status = "uninstalled"
	r.Config["retainedData"] = true
	if err = s.Update(&r); err != nil {
		t.Fatal(err)
	}
	next := model.Resource{Kind: "instance", Name: "another", Config: model.Config{"software": "redis", "version": "8.10.3"}}
	if err = s.Create(&next); err == nil {
		t.Fatal("retained version was released")
	}
	if err = s.Delete(r.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Create(&next); err != nil {
		t.Fatal("explicit record cleanup did not release version", err)
	}
}
func TestLegacyApplicationsBecomeExternalAndQueuedMutationsCancelled(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	app := model.Resource{Kind: "app", Name: "running-app", Config: model.Config{"port": 3000}}
	if err = s.Create(&app); err != nil {
		t.Fatal(err)
	}
	site := model.Resource{Kind: "site", Name: "legacy", Config: model.Config{"type": "app", "appId": app.ID}}
	if err = s.Create(&site); err != nil {
		t.Fatal(err)
	}
	queued, err := s.Enqueue(model.Operation{Name: "app.stop", Resource: app})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.DB.Exec(`PRAGMA user_version=1`)
	s.DB.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	site, err = s.Resource(site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !site.Config.Bool("externalApplication") || site.Config.String("upstream") != "http://127.0.0.1:3000" || site.Config.String("appId") != "" || site.Config.String("legacyAppId") != app.ID {
		t.Fatal(site)
	}
	if _, err = s.Resource(app.ID); err != nil {
		t.Fatal("historical app removed", err)
	}
	task, err := s.Task(queued.ID)
	if err != nil || task.State != "cancelled" {
		t.Fatal(task, err)
	}
	audits, _ := s.Audits()
	found := false
	for _, a := range audits {
		if a.Action == "app.retired:queued-task-cancelled" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing cancellation audit")
	}
}

func TestHistoricalDuplicateVersionsRemainOccupiedAfterRepresentativeDeletion(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"legacy-a", "legacy-b"} {
		_, err = s.DB.Exec(`INSERT INTO resources VALUES(?,'instance',?,'ready',?,1,?,?)`, id, id, s.encode(model.Config{"software": "nginx", "version": "1.30.6"}), Now(), Now())
		if err != nil {
			t.Fatal(err)
		}
	}
	s.DB.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	var selected string
	if err = s.DB.QueryRow(`SELECT resource_id FROM software_versions WHERE software='nginx' AND version='1.30.6'`).Scan(&selected); err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(selected); err != nil {
		t.Fatal(err)
	}
	next := model.Resource{Kind: "instance", Name: "new", Config: model.Config{"software": "nginx", "version": "1.30.6"}}
	var duplicate *model.DuplicateVersion
	if err = s.Create(&next); !errors.As(err, &duplicate) || duplicate.ResourceID == selected {
		t.Fatal("remaining historical instance did not retain occupancy", err)
	}
}
