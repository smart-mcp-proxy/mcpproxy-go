//go:build windows

package codescripts

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// Round 13 (round-10 findings 2, 3 and 4): Windows joined the same
// per-directory exact-spelling INDEX every unix platform uses
// (storednames_windows.go) instead of a per-request FindFirstFile probe, so
// these helpers now mirror storednames_other_test.go's (Linux/BSD/darwin)
// rather than being no-ops — there is real off-request-path work to
// quiesce and a real settle window to fake.

// quiesceIndexRebuilds waits for every rebuild goroutine the tests so far
// have left in flight.
func quiesceIndexRebuilds() {
	winForEachIndex(func(idx *winStoredNames) {
		idx.mu.Lock()
		building, landed := idx.building, idx.landed
		idx.mu.Unlock()
		if building {
			<-landed
		}
	})
}

// settleStoredNamesClock moves the index clock far past any directory the
// test writes, so an index taken now counts as settled and is trusted until
// the directory's generation moves. Restored on cleanup.
func settleStoredNamesClock(t *testing.T) {
	t.Helper()
	quiesceIndexRebuilds()
	orig := indexClock
	indexClock = func() time.Time { return orig().Add(time.Hour) }
	t.Cleanup(func() {
		quiesceIndexRebuilds()
		indexClock = orig
	})
}

// warmStoredNames builds the stored-name index of dir once, with the clock
// settled, so the shared tests that count a scoped resolution's directory
// reads start from a warm index — as the server does at construction.
func warmStoredNames(t *testing.T, dir string) {
	t.Helper()
	settleStoredNamesClock(t)
	require.NoError(t, Warm(dir))
}

// TestStoredSpellingsOf_PostOpenProofAcceptsAnUnchangedDescriptor is the
// positive control for the post-open proof: nothing raced the open, so the
// opened descriptor's own generation recheck and stored-basename proof both
// still agree with what was requested and probed.
func TestStoredSpellingsOf_PostOpenProofAcceptsAnUnchangedDescriptor(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "alpha.js", "1")
	warmStoredNames(t, dir)

	storedExactly, open, verifyUnchanged, closeSession, err := storedSpellingsOf(dir)
	require.NoError(t, err)
	require.NotNil(t, open, "round 13: Windows opens the winning candidate relative to the retained directory handle, exactly as unix does")
	require.NotNil(t, verifyUnchanged)
	if closeSession != nil {
		defer closeSession()
	}
	ok, err := storedExactly("alpha.js")
	require.NoError(t, err)
	require.True(t, ok)

	f, err := open(filepath.Join(dir, "alpha.js"))
	require.NoError(t, err)
	defer f.Close()

	assert.NoError(t, verifyUnchanged(f, "alpha.js"))
}

// TestStoredSpellingsOf_PostOpenProofCatchesARaceOnTheOpenedDescriptor:
// the pre-open probe (storedExactly) is only a cheap gate — the directory's
// generation (winFstatDirGeneration on the SAME retained handle) is what
// the post-open recheck actually trusts. A rename that lands between the
// probe and the open moves the directory's own LastWriteTime, so the
// recheck must refuse even though the open itself, relative to the
// retained handle, still succeeds against whatever the winning candidate's
// name resolves to at that moment.
func TestStoredSpellingsOf_PostOpenProofCatchesARaceOnTheOpenedDescriptor(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "alpha.js", "1")
	warmStoredNames(t, dir)

	_, open, verifyUnchanged, closeSession, err := storedSpellingsOf(dir)
	require.NoError(t, err)
	require.NotNil(t, open)
	require.NotNil(t, verifyUnchanged)
	if closeSession != nil {
		defer closeSession()
	}

	f, err := open(filepath.Join(dir, "alpha.js"))
	require.NoError(t, err)
	defer f.Close()

	// The race: another entry is written into the SAME directory between the
	// open and the recheck, moving the directory's own generation — exactly
	// the round-8 lookup→open race the shared generation recheck exists to
	// catch, here exercised through the Windows primitives.
	writeScript(t, dir, "beta.js", "2")

	verifyErr := verifyUnchanged(f, "alpha.js")
	require.Error(t, verifyErr, "the directory's own generation moved between the open and the recheck")
	assert.True(t, errors.Is(verifyErr, errIndexGenerationChanged))
}

// TestStoredSpellingsOf_DirectoryHandleIdentityMismatchIsAMiss (round 13,
// mirrors TestStoredNamesFor_IdentityMismatchIsAMiss and
// TestResolveScoped_DirectoryPathABA on unix): an index built from one
// directory must never authorize a request whose retained handle resolves
// to a DIFFERENT directory at the same path — the identity half of
// winDirGeneration (VolumeSerialNumber + FileIndexHigh/Low) is what this
// pins, directly at the winStoredNamesFor seam rather than through a real
// directory replacement (not reliably reproducible without an elevated
// symlink/junction on every CI runner).
func TestStoredSpellingsOf_DirectoryHandleIdentityMismatchIsAMiss(t *testing.T) {
	dirA := t.TempDir()
	writeScript(t, dirA, "alpha.js", "1")
	warmStoredNames(t, dirA)

	key := filepath.Clean(dirA)
	h, err := winOpenScopedDir(key)
	require.NoError(t, err)
	defer func() { _ = windows.CloseHandle(h) }()
	genA, err := winFstatDirGeneration(h)
	require.NoError(t, err)

	// A generation that shares dirA's LastWriteTime but claims a different
	// identity (as if the retained handle now resolved to a different
	// directory occupying the same path) must be refused exactly like a
	// stale generation — a miss, never an authorized hit.
	spoofed := genA
	spoofed.fileIndexLow++

	names, err := winStoredNamesFor(key, spoofed)
	require.NoError(t, err)
	assert.Nil(t, names, "an identity mismatch is refused exactly like a never-built index")
}
