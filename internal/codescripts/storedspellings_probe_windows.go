//go:build windows

package codescripts

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Warm is a no-op where storedSpellingsOf is a single-entry platform call:
// there is no index to build. The Linux/BSD counterpart lists the directory
// once, off the request path.
func Warm(string) error { return nil }

// SetIndexClockForTest is a no-op here: there is no directory-generation
// index or settle window on Windows — storedSpellingsOf proves the spelling
// directly, on the descriptor that is actually opened, rather than trusting
// a listed generation. Present so a caller outside this package (an
// internal/server fixture built for every platform) compiles and runs
// unchanged on Windows, where there is nothing to settle.
func SetIndexClockForTest(func() time.Time) (restore func()) { return func() {} }

// storedSpellingsOf answers, for one scoped request, whether scriptsDir holds
// an entry spelled exactly `want`, by a fixed number of single-path calls and
// never a listing (Spec 105 FR-012). NTFS is case-insensitive but
// case-PRESERVING, so the probe alone would accept `backdoor.JS` for
// `backdoor.js`; a hit is accepted only when the entry's stored spelling
// (entryName, one single-entry platform call) is byte-for-byte the requested
// one, exactly as List decides. This is the CHEAP pre-open gate only — the
// returned verifyUnchanged is what proves the winning candidate
// AUTHORITATIVELY, on the descriptor that is actually read.
//
// Round 11 MUST-FIX: a basename-only proof (round 9's openedEntryName) is
// satisfied by ANY identically named file reachable through a reparse point
// planted on the candidate itself, or on a symlinked ancestor directory,
// between the pre-open probe and openScriptFile's own open (which round 11
// also hardened — see open_windows.go — to never follow a reparse point at
// the final component, closing that half of the race; the ancestor half
// remains open to a plain basename check). The fix opens the scripts
// directory itself ONCE per request (dirFinalPath, FILE_FLAG_BACKUP_SEMANTICS)
// and compares the opened candidate's FULL normalized path
// (openedFinalPath) against that directory's own final path plus the exact
// basename — so the proof confirms both the name AND the parent, and a
// retargeted ancestor cannot make an outside file's basename satisfy it.
// closeSession releases the directory handle once the caller (resolve, in
// codescripts.go) is done with it. open stays nil: openScriptFile's own
// no-follow open (round 11 MUST-FIX above) is already authoritative about
// which entry it opens; there is no descriptor to bind it to beyond that.
func storedSpellingsOf(scriptsDir string) (storedExactly func(want string) (bool, error), open func(path string) (*os.File, error), verifyUnchanged func(f *os.File, want string) error, closeSession func(), err error) {
	dirPath, closeDir, err := dirFinalPath(scriptsDir)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	closeSession = closeDir
	baseline := strings.TrimRight(dirPath, `\`) + `\`

	storedExactly = func(want string) (bool, error) {
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
			return false, nil
		case stored != want && strings.EqualFold(stored, want):
			return false, nil
		}
		return true, nil
	}
	verifyUnchanged = func(f *os.File, want string) error {
		got, err := openedFinalPath(f)
		if err != nil || got != baseline+want {
			// Any failure of the proof call, a mismatched basename, or a
			// parent directory other than the one this request opened all
			// refuse alike (round 9 / round 11 MUST-FIX): the pre-open
			// probe already decided "true" and the caller is about to read
			// this descriptor, so an unprovable or misparented spelling
			// gets no benefit of the doubt.
			return errSpellingUnproven
		}
		return nil
	}
	return storedExactly, nil, verifyUnchanged, closeSession, nil
}
