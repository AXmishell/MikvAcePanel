// Package webui embeds the compiled Vue single-page application.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var assets embed.FS

// FS returns the SPA files rooted at dist.
func FS() fs.FS {
	sub, err := fs.Sub(assets, "dist")
	if err != nil {
		return assets
	}
	return sub
}
