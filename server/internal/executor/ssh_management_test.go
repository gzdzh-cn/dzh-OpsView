package executor

import (
	"context"
	"opsview/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSSHLoginsAndAccountMetadataAreStructuredAndRedacted(t *testing.T) {
	logs := parseSSHLogins("2026-11-19T11:00:00+0000 host sshd[1]: Accepted publickey for root from 2001:db8::1 port 555 ssh2\n2026-11-19T11:01:00+0000 host sshd[2]: Failed password for invalid user stranger from 192.0.2.1 port 600 ssh2\nnot a login")
	rows := logs["rows"].([]model.Config)
	if len(rows) != 2 || rows[0].String("source") != "2001:db8::1" || rows[1].String("username") != "stranger" || logs.Int("success") != 1 || logs.Int("failed") != 1 {
		t.Fatal(logs)
	}
	users := parseSSHLocalUsers([]byte("root:x:0:0::/root:/bin/bash\napp:x:1000:1000::/home/app:/bin/bash\nopsview:x:1001:1001::/var/lib/opsview:/usr/sbin/nologin\ndaemon:x:1:1::/:/usr/sbin/nologin\n"), []byte("root:$6$secret:1:0:9:7:::\napp:!:1:0:9:7:::\n"))
	if len(users) != 2 || !users[1].Bool("locked") || users[0].String("revision") == "" {
		t.Fatal(users)
	}
	for _, u := range users {
		for _, v := range u {
			if s, ok := v.(string); ok && strings.Contains(s, "secret") {
				t.Fatal("password hash leaked")
			}
		}
	}
	e := &Engine{}
	if _, err := e.readSSHLogins(context.Background(), "--follow"); err == nil {
		t.Fatal("unbounded log option accepted")
	}
}
func TestSSHServiceStopRequiresConfirmationAndPersistsRecovery(t *testing.T) {
	e, settings := securityFixture(t)
	settings.Ports = []int{sshBannerServer(t)}
	if err := os.WriteFile(e.sshPath(), buildSSHConfig(nil, settings), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := e.changeSSHService(ctx, model.Config{"operation": "stop"}); err == nil {
		t.Fatal("missing impact accepted")
	}
	if _, err := e.changeSSHService(ctx, model.Config{"operation": "shell", "confirmedImpact": true}); err == nil {
		t.Fatal("arbitrary operation")
	}
	if _, err := e.changeSSHService(ctx, model.Config{"operation": "stop", "confirmedImpact": true}); err != nil {
		t.Fatal(err)
	}
	p, err := e.loadSSHService()
	if err != nil || p.Deadline < time.Now().Add(110*time.Second).Unix() {
		t.Fatal(p, err)
	}
	st, _ := os.Stat(e.sshServicePath())
	if st.Mode().Perm() != 0600 {
		t.Fatal("recovery not private")
	}
	if err = e.confirmSSHService(model.Config{}); err == nil {
		t.Fatal("missing console confirmation")
	}
	if _, err = e.changeSSHService(ctx, model.Config{"operation": "restart", "confirmedImpact": true}); err == nil {
		t.Fatal("pending stop overwritten")
	}

	if err = e.restoreSSHService(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(e.Root, "ssh-service-pending.json")); !os.IsNotExist(err) {
		t.Fatal("recovery not cleared")
	}
}
