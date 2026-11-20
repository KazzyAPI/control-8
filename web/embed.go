// Package web embeds the built SPA (UI-005/006). web/dist/index.html is a committed stub (D-58).
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the bundle rooted at dist/.
func Dist() fs.FS { f, _ := fs.Sub(dist, "dist"); return f }
