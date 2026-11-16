package public

import (
	"embed"
	"io/fs"
)

//go:embed dist
var embedded embed.FS

func Files() fs.FS {
	f, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	return f
}
