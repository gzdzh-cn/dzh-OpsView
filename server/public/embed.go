package public

import (
	"embed"
	"io/fs"
)

// Include Vite's underscore-prefixed shared chunks in the standalone binary.
//
//go:embed all:dist
var embedded embed.FS

func Files() fs.FS {
	f, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	return f
}
