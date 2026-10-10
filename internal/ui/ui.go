// Package ui serves the embedded tgDrive web interface.
package ui

import (
	"embed"
	"io/fs"
)

//go:embed web
var webFS embed.FS

// FS returns the embedded web assets rooted at the web/ directory.
func FS() fs.FS {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		// Unreachable: the web/ directory is embedded at build time.
		panic(err)
	}
	return sub
}
