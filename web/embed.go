//go:build !dev

package web

import (
	"embed"
	"io/fs"
)

//go:embed all:frontend/dist
var embeddedFiles embed.FS

// GetDistFS returns the embedded frontend files
func GetDistFS() fs.FS {
	distFS, err := fs.Sub(embeddedFiles, "frontend/dist")
	if err != nil {
		panic("failed to get embedded frontend files: " + err.Error())
	}
	return distFS
}

// IsEmbedded returns true if the frontend is embedded
func IsEmbedded() bool {
	return true
}