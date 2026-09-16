//go:build !darwin && !windows

package codescripts

import "path/filepath"

// entryName returns the name the filesystem stores for the directory entry at
// path. Linux and the BSDs resolve names case-sensitively on their native
// filesystems, so an entry found by an exact-name Lstat IS that name; there
// is no portable single-entry "what is this spelled" call to consult, and a
// listing is exactly what the resolver must not perform (Spec 105 FR-012).
// A case-folding mount (vfat, an ext4 casefold directory, a bind mount from
// a case-insensitive host) is outside what this probe can tell apart.
func entryName(path string) (string, error) {
	return filepath.Base(path), nil
}
