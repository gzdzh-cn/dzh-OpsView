package logic

import (
	"opsview/internal/model"
	"testing"
)

func TestSiteOptionsRejectInjectionAndAmbiguousRouting(t *testing.T) {
	for _, c := range []model.Config{
		{"runningDirectory": "../etc"}, {"runningDirectory": "/etc"}, {"runningDirectory": "a/../b"}, {"runningDirectory": "public;root /etc"}, {"defaultDocuments": "index.html;"}, {"defaultDocuments": "../key"}, {"rewriteTemplate": "include /etc"},
		{"type": "proxy", "proxyRoutes": []ProxyRoute{{Path: "/", Targets: []string{"http://127.0.0.1:3000;"}}}},
		{"type": "proxy", "proxyRoutes": []ProxyRoute{{Path: "/", Targets: []string{"http://localhost:80/secret"}}}},
		{"type": "proxy", "proxyRoutes": []ProxyRoute{{Path: "/api/", Targets: []string{"http://localhost:80"}}}},
		{"type": "proxy", "proxyRoutes": []ProxyRoute{{Path: "/", Targets: []string{"http://localhost:80"}, Host: "$http_x_header"}}},
		{"type": "proxy", "proxyRoutes": []ProxyRoute{{Path: "/", Targets: []string{"http://localhost:80", "https://localhost:443"}}}},
		{"type": "proxy", "proxyRoutes": []ProxyRoute{{Path: "/", Targets: []string{"http://localhost:80"}, ReadTimeout: 3601}}},
	} {
		if ValidateSiteOptions(c) == nil {
			t.Fatal("accepted unsafe options", c)
		}
	}
	c := model.Config{"type": "proxy", "proxyRoutes": []ProxyRoute{{Path: "/", Targets: []string{"http://127.0.0.1:3000", "http://[::1]:3001"}, Websocket: true}, {Path: "/api/", Targets: []string{"https://api.example.com"}, Host: "api.example.com"}}}
	routes, err := SiteProxyRoutes(c)
	if err != nil || routes[0].Host != "$host" || routes[0].ReadTimeout != 60 {
		t.Fatal(routes, err)
	}
	if err = ValidateSiteOptions(model.Config{"type": "php", "runningDirectory": "public", "defaultDocuments": "index.php home.html", "rewriteTemplate": "laravel"}); err != nil {
		t.Fatal(err)
	}
}
