package executor

import (
	"encoding/json"
	"opsview/internal/hostfirewall"
	"opsview/internal/model"
	"strings"
	"testing"
)

func TestSiteDeletionIdentityPreview(t *testing.T) {
	e := &Engine{Root: t.TempDir()}
	r := model.Resource{ID: "preview-site", Config: model.Config{"type": "managed", "user": "isolated"}}
	a := &siteAccount{Name: "ovs_preview-site", Group: "ovs_preview-site", Site: r.ID, UID: "998", GID: "998", Home: "/www/wwwroot/preview-site", Phase: "active"}
	if err := e.writeSiteAccounts(siteAccountRegistry{Version: 1, Accounts: map[string]*siteAccount{a.Name: a}}); err != nil {
		t.Fatal(err)
	}
	if err := e.saveSitePorts(map[string]*sitePortRule{
		"external": {Backend: "ufw", Rule: hostfirewall.Rule{Ports: "31001", Family: "ipv4"}, Sites: map[string]bool{r.ID: true}},
		"shared":   {Backend: "ufw", Rule: hostfirewall.Rule{Ports: "31002", Family: "ipv4"}, Owned: true, Sites: map[string]bool{r.ID: true, "another-site": true}},
		"owned":    {Backend: "ufw", Rule: hostfirewall.Rule{Ports: "31003", Family: "ipv6"}, Owned: true, Sites: map[string]bool{r.ID: true}},
	}); err != nil {
		t.Fatal(err)
	}
	var previous string
	for range 10 {
		inspection := model.Config{}
		if err := e.inspectSiteAccountDeletion(r, inspection); err != nil {
			t.Fatal(err)
		}
		if inspection.String("runtimeGroup") != a.Group || inspection.String("runtimeUID") != a.UID || !strings.Contains(inspection.String("userPolicy"), "保留独立用户") {
			t.Fatal(inspection)
		}
		rules := inspection["portRules"].([]model.Config)
		if len(rules) != 3 || !strings.Contains(rules[0].String("policy"), "外部规则") || !strings.Contains(rules[1].String("policy"), "重新核对") || !strings.Contains(rules[2].String("policy"), "其他项目") {
			t.Fatal(rules)
		}
		b, _ := json.Marshal(inspection)
		if previous != "" && previous != string(b) {
			t.Fatal("preview order changed without a configuration change")
		}
		previous = string(b)
	}
}
