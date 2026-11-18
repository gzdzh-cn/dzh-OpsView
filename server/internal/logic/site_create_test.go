package logic

import (
	"opsview/internal/model"
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
