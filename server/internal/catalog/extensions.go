package catalog

import (
	_ "embed"
	"encoding/json"
)

//go:embed extensions.json
var extensionData []byte

type Extension struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Source  string `json:"source"`
	SHA256  string `json:"sha256"`
	MinPHP  string `json:"minPHP"`
	MaxPHP  string `json:"maxPHP"`
}

func Extensions() []Extension {
	var entries []Extension
	_ = json.Unmarshal(extensionData, &entries)
	return entries
}
