package software

import (
	"strings"
	"testing"
)

func TestNginxPreservesContextsAndProtectedDirectives(t *testing.T) {
	old := "# keep this\nuser www-data;\nworker_processes auto;\nevents { worker_connections 1024; }\nhttp { # context\n include /managed/sites/*.conf;\n server { listen 80; gzip off; }\n}\n"
	next, err := Patch("nginx", "1.30.6", "main", old, map[string]string{"gzip": "on", "worker_connections": "2048", "client_max_body_size": "32m"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(next, "server { listen 80; gzip off; }") || !strings.Contains(next, "# keep this") {
		t.Fatal("unrelated custom config lost")
	}
	if err = ValidateEdit("nginx", "1.30.6", "main", old, next); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(next, "www-data", "root", 1), strings.Replace(next, "/managed/sites/*.conf", "/etc/*.conf", 1), strings.Replace(next, "http {", "http { server {}", 1), strings.Replace(next, "gzip on;", "gzip on; load_module /tmp/evil.so;", 1)} {
		if ValidateEdit("nginx", "1.30.6", "main", old, bad) == nil {
			t.Fatal("accepted privileged structural edit")
		}
	}
}
func TestINICommentsScopesAndValidation(t *testing.T) {
	old := "; header\n[PHP]\n  memory_limit = 128M ; keep note\n[other]\nmemory_limit = 4M\n"
	next, err := Patch("php", "8.4.27", "main", old, map[string]string{"memory_limit": "256M"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(next, "  memory_limit = 256M ; keep note") || !strings.Contains(next, "[other]\nmemory_limit = 4M") {
		t.Fatal(next)
	}
	if err = ValidateEdit("php", "8.4.27", "main", old, next); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"1M\nextension=/tmp/evil.so", "; shutdown", "1M;extension=/tmp/evil.so"} {
		if _, err = Patch("php", "8.4.27", "main", old, map[string]string{"memory_limit": value}); err == nil {
			t.Fatal("accepted injected config")
		}
	}
}
func TestRedisRetainsCredentialsAndRejectsDuplicateOverrides(t *testing.T) {
	old := "bind 127.0.0.1\nrequirepass a-super-secret\nmaxmemory 128mb\n"
	next, err := Patch("redis", "8.10.3", "main", old, map[string]string{"maxmemory": "256mb"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(next, "requirepass a-super-secret") {
		t.Fatal("credential lost")
	}
	if err = ValidateEdit("redis", "8.10.3", "main", old, next); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{next + "maxmemory 0\n", strings.Replace(next, "127.0.0.1", "0.0.0.0", 1), next + "loadmodule /tmp/bad.so\n"} {
		if ValidateEdit("redis", "8.10.3", "main", old, bad) == nil {
			t.Fatal("accepted protected edit")
		}
	}
}
func TestVersionFieldsAndFPMRelations(t *testing.T) {
	for _, f := range Fields("mysql", "8.4.13", "main") {
		if f.Key == "expire_logs_days" {
			t.Fatal("obsolete MySQL parameter")
		}
	}
	if ValidateValues(Fields("php", "8.4.27", "fpm"), map[string]string{"pm": "dynamic", "pm.max_children": "8", "pm.start_servers": "1", "pm.min_spare_servers": "4", "pm.max_spare_servers": "2"}) == nil {
		t.Fatal("invalid pool accepted")
	}
}
