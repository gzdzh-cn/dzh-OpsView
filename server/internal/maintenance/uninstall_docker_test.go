package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"opsview/internal/model"
	"strings"
	"testing"
)

func TestUninstallDockerOwnershipAndVolumes(t *testing.T) {
	u, root, _ := uninstallFixture(t)
	id := strings.Repeat("a", 64)
	other := strings.Repeat("b", 64)
	data := uninstallManaged + "/instances/db/data"
	uninstallWrite(t, root, data+"/ibdata1", "keep")
	rows := map[string]uninstallDockerInfo{
		id: {ID: id, Created: "time"}, other: {ID: other, Created: "time"},
	}
	owned := rows[id]
	owned.Config.Labels = map[string]string{"opsview.managed": "true", "opsview.resource": "db"}
	owned.Mounts = append(owned.Mounts, struct{ Type, Source, Destination string }{"bind", data, "/var/lib/mysql"}, struct{ Type, Source, Destination string }{"volume", "/var/lib/docker/volumes/data/_data", "/other"})
	owned.State.Running = true
	rows[id] = owned
	calls := []string{}
	u.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		switch args[0] {
		case "ps":
			if len(args) > 4 {
				return []byte(id + "\n"), nil
			}
			return []byte(id + "\n" + other + "\n"), nil
		case "inspect":
			row := rows[args[len(args)-1]]
			return json.Marshal(row)
		case "kill":
			row := rows[id]
			row.State.Running = false
			rows[id] = row
			return nil, nil
		case "rm":
			return nil, nil
		}
		return nil, errors.New("unexpected command")
	}
	p := &uninstallPlan{}
	resources := map[string]model.Resource{"db": {ID: "db", Kind: "instance", Config: model.Config{"mode": "docker", "software": "mysql"}}}
	if err := u.containers(context.Background(), p, resources); err != nil {
		t.Fatal(err)
	}
	if len(p.Containers) != 1 || !protectedPath(data, p.Keep) {
		t.Fatal("incorrect ownership/data protection")
	}
	if err := u.removeContainer(context.Background(), p.Containers[0]); err != nil {
		t.Fatal(err)
	}
	for _, call := range calls {
		if strings.Contains(call, "docker rm") && call != "docker rm "+id {
			t.Fatal("removed volume or unrelated container")
		}
		if strings.Contains(call, "prune") || strings.Contains(call, "--force") || strings.Contains(call, "--volumes") {
			t.Fatal("unsafe docker cleanup")
		}
	}
	row := rows[id]
	row.Config.Labels = map[string]string{"other": "owner"}
	rows[id] = row
	if err := u.removeContainer(context.Background(), p.Containers[0]); err == nil {
		t.Fatal("accepted ownership drift")
	}
}

func TestUninstallDockerUnknownOwnerAndFailure(t *testing.T) {
	u, _, _ := uninstallFixture(t)
	id := strings.Repeat("a", 64)
	u.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if args[0] == "ps" {
			return []byte(id), nil
		}
		row := uninstallDockerInfo{ID: id, Created: "time"}
		row.Config.Labels = map[string]string{"opsview.managed": "true", "opsview.resource": "missing"}
		return json.Marshal(row)
	}
	if err := u.containers(context.Background(), &uninstallPlan{}, nil); err == nil {
		t.Fatal("unknown container owner accepted")
	}
	u.run = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("socket down") }
	resources := map[string]model.Resource{"db": {ID: "db", Kind: "instance", Config: model.Config{"mode": "docker"}}}
	if err := u.containers(context.Background(), &uninstallPlan{}, resources); err == nil {
		t.Fatal("daemon failure treated as no containers")
	}
	if err := u.removeContainer(context.Background(), uninstallContainer{ID: id}); err == nil {
		t.Fatal("daemon failure treated as successful removal")
	}
}
