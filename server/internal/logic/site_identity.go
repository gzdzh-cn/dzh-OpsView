package logic

import (
	"encoding/json"
	"opsview/internal/model"
	"opsview/internal/security"
)

// Bound to the site ID and security-relevant execution inputs, never accepted
// from a browser. Unrelated domain/note/certificate edits retain the approval.
func SiteRootDigest(r model.Resource) string {
	c := model.Config{"site": r.ID}
	for _, key := range []string{"user", "executable", "arguments", "environment", "rootDirectory", "runtimeIsolation"} {
		c[key] = r.Config[key]
	}
	b, _ := json.Marshal(c)
	return security.Hash(string(b))
}

func SitePrivateFields() []string {
	return []string{"siteRootApproval", "runtimeUser", "runtimeGroup", "runtimeUID", "runtimeGID", "runtimeIsolationApplied", "permissionRevision", "runtimeTransaction", "siteFirewallRules"}
}
