package executor

import (
	"os/user"
	"testing"
)

func TestSiteRuntimeUserUIDZeroAliases(t *testing.T) {
	for _, u := range []*user.User{{Username: "www-data", Uid: "0"}, {Username: "admin", Uid: "0"}, {Username: "opsview", Uid: "1001"}, {Username: "ovbuild", Uid: "1002"}, {Username: "daemon", Uid: "1"}} {
		if siteAccountAllowed(u) {
			t.Fatalf("unexpected allowed identity: %s", u.Username)
		}
	}
	for _, u := range []*user.User{{Username: "root", Uid: "0"}, {Username: "www-data", Uid: "33"}, {Username: "deploy", Uid: "1001"}} {
		if !siteAccountAllowed(u) {
			t.Fatalf("valid identity rejected: %s", u.Username)
		}
	}
}

func TestSitePortUFWIPv6Config(t *testing.T) {
	for _, s := range []string{"IPV6=yes", "IPV6 = \"yes\" # enabled", "IPV6='yes'\n"} {
		if !ufwIPv6Enabled(s) {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"#IPV6=yes", "IPV6=no", ""} {
		if ufwIPv6Enabled(s) {
			t.Fatal(s)
		}
	}
}
