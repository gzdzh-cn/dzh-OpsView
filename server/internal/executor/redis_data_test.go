package executor

import (
	"bufio"
	"context"
	"opsview/internal/model"
	"strings"
	"testing"
)

func TestRedisProtocolBoundsAndBinary(t *testing.T) {
	for _, raw := range []string{"$999999999\r\n", "*999999999\r\n", "$-2\r\n", "-NOAUTH secret\r\n"} {
		budget := 1024
		if _, err := redisReply(bufio.NewReader(strings.NewReader(raw)), 0, &budget); err == nil {
			t.Fatal("unbounded reply accepted")
		}
	}
	budget := 1024
	v, err := redisReply(bufio.NewReader(strings.NewReader("$3\r\n\x00\xffa\r\n")), 0, &budget)
	if err != nil || v != "\x00\xffa" {
		t.Fatal(v, err)
	}
}
func TestDatabaseDeleteCannotBypassBackup(t *testing.T) {
	e, r, _ := softwareFixture(t)
	r.Config["software"] = "mysql"
	_ = e.save(r)
	db := model.Resource{ID: "legacy-db", Kind: "database", Name: "legacy", Config: model.Config{"instanceId": r.ID, "username": "legacy", "password": "valid-password"}}
	_ = e.save(db)
	_, err := e.Execute(context.Background(), model.Operation{Name: "database.delete", Resource: db, Params: model.Config{"confirmDataDeletion": true}}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "停止业务写入") {
		t.Fatal("unconfirmed destructive action", err)
	}
}
