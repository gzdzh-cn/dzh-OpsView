package dao

import (
	"opsview/internal/model"
	"strings"
	"sync"
	"testing"
)

func TestSingleNginxReservationConcurrentVersions(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, v := range []string{"1.30.6", "1.28.3"} {
		wg.Add(1)
		go func(v string) {
			defer wg.Done()
			r := model.Resource{Kind: "instance", Name: "nginx-" + v, Config: model.Config{"software": "nginx", "version": v, "mode": "native"}}
			results <- s.Create(&r)
		}(v)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("accepted %d nginx versions", success)
	}
}
func TestSiteProvisionAtomicAndEncrypted(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	site := model.Resource{Kind: "site", Name: "example.test", Config: model.Config{"instanceId": "nginx", "type": "static"}}
	db := model.Resource{Kind: "database", Name: "site_example", Config: model.Config{"instanceId": "mysql", "username": "site_example", "password": "secret-db-12345"}}
	task, err := s.CreateSiteProvision(&site, &db, model.Config{"privateKey": "private-key-test"})
	if err != nil {
		t.Fatal(err)
	}
	if site.Config.String("databaseId") != db.ID || task.ResourceID != site.ID {
		t.Fatal("relation lost")
	}
	var payload, request string
	s.DB.QueryRow(`SELECT payload FROM tasks WHERE id=?`, task.ID).Scan(&payload)
	s.DB.QueryRow(`SELECT request FROM site_provisions WHERE site_id=?`, site.ID).Scan(&request)
	if strings.Contains(payload, "secret-db") || strings.Contains(request, "private-key-test") {
		t.Fatal("unencrypted payload")
	}
	saved, err := s.SiteProvisionRequest(site.ID)
	if err != nil || saved.String("privateKey") != "private-key-test" {
		t.Fatal(saved, err)
	}
	second := model.Resource{Kind: "site", Name: "other.test", Config: model.Config{"instanceId": "nginx", "type": "static"}}
	db2 := db
	db2.ID = ""
	if _, err = s.CreateSiteProvision(&second, &db2); err == nil {
		t.Fatal("duplicate database accepted")
	}
	rows, _ := s.Resources("site")
	if len(rows) != 1 {
		t.Fatal("partial site committed")
	}
	if err = s.Finish(task.ID, "failed", "site apply failed"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Enqueue(model.Operation{Name: "site.provision", Resource: site, Params: saved}); err != nil {
		t.Fatal("retry not queued", err)
	}
}
func TestSiteCandidateRevisionAndCategories(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	if err = s.ChangeSiteCategory("", "业务", false); err != nil {
		t.Fatal(err)
	}
	r := model.Resource{Kind: "site", Name: "site", Config: model.Config{"category": "业务", "type": "static"}}
	if err = s.Create(&r); err != nil {
		t.Fatal(err)
	}
	if err = s.ChangeSiteCategory("业务", "", true); err != nil {
		t.Fatal(err)
	}
	latest, _ := s.Resource(r.ID)
	if latest.Config.String("category") != "默认分类" || latest.Revision <= r.Revision {
		t.Fatal(latest)
	}
	if _, err = s.Enqueue(model.Operation{Name: "site.apply", Resource: r}); err == nil {
		t.Fatal("stale candidate accepted")
	}
}

func TestSiteProvisionConcurrentRequestAndRetryRollback(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	var wg sync.WaitGroup
	tasks := make(chan model.Task, 2)
	failures := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			site := model.Resource{Kind: "site", Name: "same.test", Config: model.Config{"instanceId": "nginx", "type": "static"}}
			task, er := s.CreateSiteProvision(&site, nil, model.Config{"requestId": "same-request"})
			tasks <- task
			failures <- er
		}()
	}
	wg.Wait()
	close(tasks)
	close(failures)
	for er := range failures {
		if er != nil {
			t.Fatal(er)
		}
	}
	var first model.Task
	for task := range tasks {
		if first.ID == "" {
			first = task
		}
		if first.ID != task.ID {
			t.Fatal("duplicate task", task)
		}
	}
	sites, _ := s.Resources("site")
	if len(sites) != 1 {
		t.Fatal("duplicate resource", len(sites))
	}
	before := sites[0]
	candidate := before
	candidate.Name = "corrected.test"
	if _, err = s.ResumeSiteProvision(candidate, true, model.Config{"requestId": "same-request"}); err == nil {
		t.Fatal("running retry accepted")
	}
	unchanged, _ := s.Resource(before.ID)
	if unchanged.Revision != before.Revision || unchanged.Name != before.Name {
		t.Fatal("failed enqueue changed draft", unchanged)
	}
	if err = s.Finish(first.ID, "failed", "fixture"); err != nil {
		t.Fatal(err)
	}
	resumed, err := s.ResumeSiteProvision(candidate, true, model.Config{"requestId": "same-request"})
	if err != nil {
		t.Fatal(err)
	}
	pointer, err := s.SiteProvisionByRequest("same-request")
	if err != nil || pointer.String("taskId") != resumed.ID || pointer.Int("revision") != before.Revision+1 {
		t.Fatal(pointer, err)
	}
}
