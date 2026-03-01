package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// StaticFS returns the embedded web/dist filesystem rooted at "dist/".
func StaticFS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("web/dist not embedded: " + err.Error())
	}
	return sub
}
