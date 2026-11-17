package executor

import (
	"opsview/internal/model"
	"strings"
)

func parseListeningPorts(text string) []model.Config {
	rows := []model.Config{}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] == "Netid" {
			continue
		}
		process := ""
		if len(fields) > 6 {
			process = strings.Join(fields[6:], " ")
		}
		rows = append(rows, model.Config{"protocol": fields[0], "state": fields[1], "local": fields[4], "peer": func() string {
			if len(fields) > 5 {
				return fields[5]
			}
			return ""
		}(), "process": process})
	}
	return rows
}
func parseSystemServices(text string) []model.Config {
	rows := []model.Config{}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "● "))
		if len(fields) < 4 || !strings.HasSuffix(fields[0], ".service") {
			continue
		}
		description := ""
		if len(fields) > 4 {
			description = strings.Join(fields[4:], " ")
		}
		rows = append(rows, model.Config{"unit": fields[0], "load": fields[1], "active": fields[2], "state": fields[3], "description": description, "managed": strings.HasPrefix(fields[0], "opsview-")})
	}
	return rows
}
