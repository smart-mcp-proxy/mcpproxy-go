//go:build darwin || windows

package codescripts

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
)

// Warm is a no-op where storedSpellingsOf is a single-entry platform call:
// there is no index to build. The Linux/BSD counterpart lists the directory
// once, off the request path.
func Warm(string) error { return nil }

// storedSpellingsOf answers, for one scoped request, whether scriptsDir holds
// an entry spelled exactly `want`, by a fixed number of single-path calls and
// never a listing (Spec 105 FR-012). The default APFS/HFS+ and NTFS volumes
// are case-insensitive but case-PRESERVING, so the probe alone would accept
// `backdoor.JS` for `backdoor.js`; a hit is accepted only when the entry's
// stored spelling (entryName, one single-entry platform call) is
// byte-for-byte the requested one, exactly as List decides. The no-follow
// open remains the authoritative check.
//
// The second return is the shared signature's post-open recheck (round 8
// MUST-FIX on Linux/BSD, the lookup→open race): here every candidate is
// already re-verified directly, per call, against the CURRENT filesystem
// (there is no directory-generation index to fall behind), so there is
// nothing further to recheck after the open and this is always nil.
func storedSpellingsOf(scriptsDir string) (storedExactly func(want string) (bool, error), verifyUnchanged func() error, err error) {
	return func(want string) (bool, error) {
		path := filepath.Join(scriptsDir, want)
		if _, err := lstat(path); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return false, nil
			}
			return false, err
		}
		stored, err := entryName(path)
		switch {
		case err != nil:
			// The platform call failed: the Lstat verdict stays in force and
			// the no-follow open decides usability.
			return true, nil
		case stored != want && strings.EqualFold(stored, want):
			// The filesystem folded the case: the entry is spelled differently
			// and no discovery surface reports it under this name. Only a
			// case-only difference is a fold; any other answer (a hard link's
			// other name) leaves the Lstat verdict in force.
			return false, nil
		}
		return true, nil
	}, nil, nil
}
