//go:build !darwin && !windows

package codescripts

import (
	"io/fs"
	"path/filepath"
	"strings"
)

// entryName returns the name the filesystem stores for the directory entry at
// path, whose Lstat result is probed. Linux and the BSDs resolve names
// case-sensitively on their native filesystems, so an entry found by an
// exact-name Lstat IS that name — the common case, answered by the probe alone
// (foldsCase, one extra constant-cost Lstat) without touching the directory.
// But a case-folding mount (vfat, an ext4 casefold directory, a bind mount
// from a case-insensitive host) finds `backdoor.JS` for `backdoor.js` just as
// APFS and NTFS do, and unlike those it offers no single-entry "what is this
// spelled" call: F_GETPATH does not exist, and a readlink of /proc/self/fd/N
// echoes the spelling that was looked up, not the one on disk. There the only
// exact answer is the directory listing, so it is read ONCE, in that branch
// alone, and the stored basename is the one that matches the requested
// spelling — byte for byte when the exact name is stored, case-folded when it
// is not (which probeCandidates then refuses). A folding mount thus pays one
// listing per existing candidate; the refusal shape is unchanged (Spec 105
// FR-012, codex r4 #1). When neither the fold probe nor the listing can
// answer, the spelling is reported unverifiable and the scoped resolver fails
// closed.
func entryName(path string, probed fs.FileInfo) (string, error) {
	folds, err := foldsCase(path, probed)
	if err != nil {
		// The variant probe failed for a reason other than absence: nothing
		// proves the spelling, so the answer is the fail-closed one.
		return "", errSpellingUnverifiable
	}
	base := filepath.Base(path)
	if !folds {
		return base, nil
	}

	entries, err := readDir(filepath.Dir(path))
	if err != nil {
		// Searchable but not listable (or gone): the fold is proven and the
		// spelling cannot be read, so the scoped resolver fails closed.
		return "", errSpellingUnverifiable
	}
	stored := ""
	for _, e := range entries {
		name := e.Name()
		if name == base {
			return base, nil
		}
		if stored == "" && strings.EqualFold(name, base) {
			stored = name
		}
	}
	if stored == "" {
		// The probe found an entry the listing does not hold (removed in
		// between): nothing to verify against.
		return "", errSpellingUnverifiable
	}
	return stored, nil
}
