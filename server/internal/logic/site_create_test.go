package logic

import (
	"context"
	"errors"
	"opsview/internal/dao"
	"opsview/internal/model"
	"strings"
	"testing"
)

func TestSiteRootAndProcessValidation(t *testing.T) {
	if got := DefaultSiteRoot("Example.com\nwww.example.com"); got != "/www/wwwroot/example.com" {
		t.Fatal(got)
	}
	for _, p := range []string{"/etc", "/www/wwwroot", "/www/wwwroot/../etc", "/www/wwwroot/a/b", "/www/wwwroot/.secret", "/www/wwwroot/a;include"} {
		if ValidateSiteRoot(p) == nil {
			t.Fatal("unsafe root accepted", p)
		}
	}
	c := model.Config{"type": "managed", "rootDirectory": "/www/wwwroot/app.example.com", "domains": "app.example.com", "port": 3000, "executable": "/usr/bin/node", "user": "isolated", "arguments": `["server.js","$(touch /tmp/not-a-shell)"]`, "environment": `{"NODE_ENV":"production"}`, "healthURL": "http://127.0.0.1:3000/health"}
	if err := ValidateSiteCreate(c); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []struct {
		k string
		v any
	}{{"user", "root"}, {"executable", "/usr/bin/node\nUser=root"}, {"arguments", `["abc\nExecStart=/bin/sh"]`}, {"healthURL", "http://169.254.169.254/latest"}, {"port", 80}, {"proxyHost", "$host; include /etc/passwd"}} {
		before := c[bad.k]
		c[bad.k] = bad.v
		if ValidateSiteCreate(c) == nil {
			t.Fatal("invalid process accepted", bad.k)
		}
		c[bad.k] = before
	}
}

type provisionExecutor struct {
	calls    []string
	siteFail bool
}

func (e *provisionExecutor) Execute(_ context.Context, op model.Operation, log func(string)) (model.Config, error) {
	e.calls = append(e.calls, op.Name)
	if op.Name == "site.apply" && e.siteFail {
		return nil, errors.New("site validation failed")
	}
	return model.Config{}, nil
}
func TestProvisionRetryKeepsSuccessfulDatabase(t *testing.T) {
	s, err := dao.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	site := model.Resource{Kind: "site", Name: "test", Config: model.Config{"instanceId": "nginx", "type": "static"}}
	db := model.Resource{Kind: "database", Name: "test", Config: model.Config{"instanceId": "mysql", "username": "dedicated", "password": "secret123456"}}
	task, err := s.CreateSiteProvision(&site, &db)
	if err != nil {
		t.Fatal(err)
	}
	_ = task
	e := &provisionExecutor{siteFail: true}
	a := &App{Store: s, Executor: e}
	a.runNext(context.Background())
	saved, err := s.Resource(db.ID)
	if err != nil || saved.Status != "ready" {
		t.Fatal("successful db lost", saved, err)
	}
	current, _ := s.Resource(site.ID)
	if current.Status != "error" {
		t.Fatal(current.Status)
	}
	e.siteFail = false
	if _, err = s.Enqueue(model.Operation{Name: "site.provision", Resource: current}); err != nil {
		t.Fatal(err)
	}
	a.runNext(context.Background())
	if strings.Join(e.calls, ",") != "database.create,site.apply,site.apply" {
		t.Fatal("database created again", e.calls)
	}
}

func TestConfiguredSiteDirectory(t *testing.T) {
	if err := ValidateSiteRootAt("/srv/projects/example.test", "/srv/projects"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/etc/sites", "/var/lib/opsview/sites", "/srv/unsafe;sites", "/srv/{sites}"} {
		if ValidateSiteRootAt(p+"/example.test", p) == nil {
			t.Fatal("unsafe base", p)
		}
	}
	if ValidateSiteRootAt("/srv/projects/a/b", "/srv/projects") == nil {
		t.Fatal("nested project escaped base")
	}
}
