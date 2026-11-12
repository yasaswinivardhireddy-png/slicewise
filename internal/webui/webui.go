// Package webui embeds the built web console.
//
// CI and the Dockerfile copy web/dist here before building the server. Without a build, dist holds
// only .gitkeep and the server shows a placeholder page.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the built console, or nil if it has not been built.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}
