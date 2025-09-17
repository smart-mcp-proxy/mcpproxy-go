//go:build dev

package web

import (
	"io/fs"
	"os"
)

// GetDistFS returns the development frontend files from disk
func GetDistFS() fs.FS {
	return os.DirFS("frontend/dist")
}

// IsEmbedded returns false in development mode
func IsEmbedded() bool {
	return false
}