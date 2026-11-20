package executor

import (
	"opsview/internal/model"
	"os"
	"strconv"
	"strings"
)

// Read only public account metadata. Password/shadow records never enter this response.
func fileIdentities() (model.Config, error) {
	result := model.Config{}
	for _, kind := range []string{"passwd", "group"} {
		b, err := os.ReadFile("/etc/" + kind)
		if err != nil {
			return nil, err
		}
		rows := []model.Config{}
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Split(line, ":")
			if len(f) < 4 || f[0] == "" {
				continue
			}
			id, er := strconv.Atoi(f[2])
			if er != nil {
				continue
			}
			rows = append(rows, model.Config{"name": f[0], "id": id})
		}
		key := "users"
		if kind == "group" {
			key = "groups"
		}
		result[key] = rows
	}
	return result, nil
}
