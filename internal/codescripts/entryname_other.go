//go:build !darwin && !windows

package codescripts

import (
	"io/fs"
	"path/filepath"
)

// entryName returns the name the filesystem stores for the directory entry at
// path, whose Lstat result is probed. Linux and the BSDs resolve names
// case-sensitively on their native filesystems, so an entry found by an
// exact-name Lstat IS that name — but a case-folding mount (vfat, an ext4
// casefold directory, a bind mount from a case-insensitive host) finds
// `backdoor.JS` for `backdoor.js` just as APFS and NTFS do, and unlike those it
// offers no single-entry "what is this spelled" call: F_GETPATH does not exist,
// and a readlink of /proc/self/fd/N echoes the spelling that was looked up,
// not the one on disk. The only exact answer is the directory listing the
// scoped resolver must not perform (Spec 105 FR-012), so the fold is proven
// with one constant-cost probe (foldsCase) and reported as unverifiable; the
// scoped resolver then refuses the candidate — fail closed.
func entryName(path string, probed fs.FileInfo) (string, error) {
	folds, err := foldsCase(path, probed)
	if err != nil {
		// The variant probe failed for a reason other than absence: nothing
		// proves the spelling, so the answer is the same fail-closed one.
		return "", errSpellingUnverifiable
	}
	if folds {
		return "", errSpellingUnverifiable
	}
	return filepath.Base(path), nil
}
