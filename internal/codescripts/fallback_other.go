//go:build !unix && !windows

package codescripts

import (
	"errors"
	"io/fs"
	"os"
)

// Round 13 SHOULD (finding 6): a build target that is neither `unix`
// (dirfd_other.go, storednames_other.go — the fd-bound Linux/BSD/darwin
// design) nor `windows` (open_windows.go, storedspellings_probe_windows.go)
// — plan9, js/wasm, wasip1, or any future GOOS this package has not been
// taught a directory-descriptor primitive for — has no platform primitive
// this package can trust to answer a scoped request, or even to open the
// administrator's own no-follow read (open_unix.go needs
// syscall.O_NOFOLLOW/ELOOP/EMLINK, none of which exist on plan9 or
// js/wasm). The concrete failure this closes: `GOOS=plan9 go build
// ./internal/codescripts` and `GOOS=js GOARCH=wasm go build
// ./internal/codescripts` failed outright before this file existed, because
// every one of storedSpellingsOf/Warm/SetIndexClockForTest/openScriptFile
// was defined only under tags that (before round 13) or now (after
// narrowing dirfd_other.go, storednames_other.go and open_unix.go to their
// correct, narrower tags) exclude these targets entirely.
//
// Rather than leave the package unable to compile there, it compiles and
// fails CLOSED and undisclosed: Warm is a no-op (there is no index to
// build, so nothing needs warming); storedSpellingsOf and openScriptFile
// both report the stored script as not found — wrapped so
// errors.Is(err, fs.ErrNotExist) is true — which resolve (codescripts.go)
// turns into the caller's ordinary NotFoundError, non-disclosing for a
// scoped caller exactly as SC-005 requires and, for an administrator,
// the same not-found form a genuinely empty or unreadable directory
// produces on every other platform. Neither function silently succeeds,
// silently discloses anything about what scriptsDir might hold, or panics;
// the package is simply unable to serve a stored script on a target with
// no directory-descriptor primitive of its own.
var errUnsupportedPlatform = errors.New("codescripts: stored scripts are not supported on this platform")

// Warm is a no-op: there is no index to build on a platform with no
// directory-descriptor primitive.
func Warm(string) error { return nil }

// SetIndexClockForTest itself is shared, no-build-tag code (indexclock.go):
// there is no settle window to fake on a platform with no index at all, but
// production never calls it here either way, so the shared (real) clock
// override is harmless to inherit rather than needing its own no-op.

// storedSpellingsOf always reports the requested name as not found —
// fail-closed, never fail-open — since this platform has no primitive this
// package trusts to answer whether scriptsDir holds an entry at all.
func storedSpellingsOf(string) (storedExactly func(want string) (bool, error), open func(path string) (*os.File, error), verifyUnchanged func(f *os.File, want string) error, closeSession func(), err error) {
	return nil, nil, nil, nil, errUnsupportedPlatformNotFound()
}

// openScriptFile always fails: there is no platform no-follow primitive
// here for the administrator's own read to rely on, so the safe answer is
// "not found" rather than an open that cannot promise it refused a symlink.
func openScriptFile(string) (*os.File, error) {
	return nil, errUnsupportedPlatformNotFound()
}

// errUnsupportedPlatformNotFound wraps errUnsupportedPlatform so
// errors.Is(err, fs.ErrNotExist) is true — resolve (codescripts.go) treats
// any fs.ErrNotExist from a candidates()/open call as an ordinary not-found,
// never as ReasonUnreadable, which is what keeps this fail-closed answer
// non-disclosing for a scoped caller.
func errUnsupportedPlatformNotFound() error {
	return &fs.PathError{Op: "open", Path: "", Err: errors.Join(errUnsupportedPlatform, fs.ErrNotExist)}
}
