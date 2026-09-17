//go:build darwin

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
// index or settle window on darwin — storedSpellingsOf proves the spelling
// directly, on the descriptor that is actually opened, rather than trusting
// a listed generation. Present so a caller outside this package (an
// internal/server fixture built for every platform) compiles and runs
// unchanged on darwin, where there is nothing to settle.
func SetIndexClockForTest(func() time.Time) (restore func()) { return func() {} }

// storedSpellingsOf answers, for one scoped request, whether scriptsDir holds
// an entry spelled exactly `want`, by a fixed number of single-path calls and
// never a listing (Spec 105 FR-012). The default APFS/HFS+ volume is
// case-insensitive but case-PRESERVING, so the probe alone would accept
// `backdoor.JS` for `backdoor.js`; a hit is accepted only when the entry's
// stored spelling (entryName, one single-entry platform call) is
// byte-for-byte the requested one, exactly as List decides.
//
// This is the CHEAP pre-open gate only — round 9 MUST-FIX: a case-rename or
// replacement landing between this probe and openScriptFile's own open can
// leave a different, case-folded file behind the same requested spelling for
// the descriptor's entire lifetime, and neither a no-follow open nor Stat
// tells a folded spelling from an exact one. The returned verifyUnchanged
// proves the spelling AUTHORITATIVELY, on the descriptor that will actually
// be read — see below. open and closeSession are always nil here (round 11:
// darwin is unchanged beyond this shared five-return signature — see
// storednames_other.go's Linux/BSD counterpart and
// storedspellings_probe_windows.go for the platforms that need them):
// openScriptFile's own O_SYMLINK-probed, O_NOFOLLOW no-follow open already
// resolves the path exactly once for the actual read, and there is no
// per-request resource to release.
//
// A platform-call failure here is not a match (round 9 MUST-FIX): earlier
// rounds let the Lstat verdict alone stand when entryName errored, which
// fails OPEN on a probe race or platform-call failure. The pre-open probe
// need not be perfectly precise — the post-open proof is authoritative and
// would still catch a wrongly admitted candidate — but there is no reason to
// admit one on a failure this function cannot itself explain.
func storedSpellingsOf(scriptsDir string) (storedExactly func(want string) (bool, error), open func(path string) (*os.File, error), verifyUnchanged func(f *os.File, want string) error, closeSession func(), err error) {
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
			// The filesystem folded the case: the entry is spelled differently
			// and no discovery surface reports it under this name. Only a
			// case-only difference is a fold; any other answer (a hard link's
			// other name) leaves the Lstat verdict in force.
			return false, nil
		}
		return true, nil
	}
	verifyUnchanged = func(f *os.File, want string) error {
		stored, err := openedEntryName(f)
		if err != nil || stored != want {
			// Any failure of the proof call, or any mismatch, refuses — the
			// pre-open probe already decided "true" and the caller is about
			// to read this descriptor, so an unprovable spelling gets no
			// benefit of the doubt (round 9 MUST-FIX).
			return errSpellingUnproven
		}
		return nil
	}
	return storedExactly, nil, verifyUnchanged, nil, nil
}
