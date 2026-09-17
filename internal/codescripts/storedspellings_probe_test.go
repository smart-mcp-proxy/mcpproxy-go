//go:build darwin || windows

package codescripts

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// warmStoredNames is a no-op where storedSpellingsOf is a single-entry platform
// call (darwin F_GETPATH, Windows FindFirstFile) and there is no index to
// warm; the Linux/BSD counterpart builds the index once, off the request path.
func warmStoredNames(t *testing.T, _ string) {
	t.Helper()
}

// quiesceIndexRebuilds is a no-op here: nothing runs off the request path.
func quiesceIndexRebuilds() {}

// TestStoredSpellingsOf_PostOpenProofAcceptsAnUnchangedDescriptor is the
// positive control for the round 9 MUST-FIX post-open proof: nothing raced
// the open, so the opened descriptor's own stored spelling still matches
// exactly what was requested and probed.
func TestStoredSpellingsOf_PostOpenProofAcceptsAnUnchangedDescriptor(t *testing.T) {
	dir := t.TempDir()
	path := writeScript(t, dir, "alpha.js", "1")

	storedExactly, open, verifyUnchanged, closeSession, err := storedSpellingsOf(dir)
	require.NoError(t, err)
	require.Nil(t, open, "darwin/Windows never bind the open to a per-request descriptor (round 11): openScriptFile's own no-follow open is already authoritative")
	require.NotNil(t, verifyUnchanged, "darwin/Windows always supply the authoritative post-open check")
	if closeSession != nil {
		defer closeSession()
	}
	ok, err := storedExactly("alpha.js")
	require.NoError(t, err)
	require.True(t, ok)

	f, err := openScriptFile(path)
	require.NoError(t, err)
	defer f.Close()

	assert.NoError(t, verifyUnchanged(f, "alpha.js"))
}

// TestStoredSpellingsOf_PostOpenProofCatchesARaceOnTheOpenedDescriptor (round
// 9 MUST-FIX): the pre-open probe (storedExactly) is only a cheap gate — it
// can be satisfied and the open can still succeed against a file that a race
// has since case-renamed, because a no-follow open does not compare names,
// only symlink status, and the SAME descriptor keeps reading through a
// rename of its own directory entry. The authoritative check reads the
// OPENED descriptor's own stored spelling (F_GETPATH / GetFinalPathNameByHandle)
// and must refuse once it no longer matches what was requested, wherever in
// the descriptor's lifetime the rename lands.
func TestStoredSpellingsOf_PostOpenProofCatchesARaceOnTheOpenedDescriptor(t *testing.T) {
	dir := t.TempDir()
	path := writeScript(t, dir, "alpha.js", "1")

	_, _, verifyUnchanged, closeSession, err := storedSpellingsOf(dir)
	require.NoError(t, err)
	require.NotNil(t, verifyUnchanged)
	if closeSession != nil {
		defer closeSession()
	}

	// The race: the file is case-renamed between the pre-open probe and the
	// open. APFS and NTFS fold the requested spelling onto the renamed entry,
	// so the open succeeds — and F_GETPATH / GetFinalPathNameByHandle on the
	// opened descriptor report the RENAMED spelling, proving the descriptor
	// is not the exact name that was requested and probed.
	require.NoError(t, os.Rename(path, filepath.Join(dir, "ALPHA.JS")))

	f, err := openScriptFile(path)
	require.NoError(t, err, "a case-folding filesystem opens the renamed entry under the old spelling")
	defer f.Close()

	verifyErr := verifyUnchanged(f, "alpha.js")
	require.Error(t, verifyErr, "the opened descriptor's spelling no longer matches what was requested")
	assert.True(t, errors.Is(verifyErr, errSpellingUnproven))
}

// TestStoredSpellingsOf_PostOpenProofCatchesARenameAfterOpen is the same
// proof taken after the open: the descriptor is already reading the file when
// it is case-renamed. Windows refuses to rename a file another handle holds
// open (no FILE_SHARE_DELETE on the executed handle), so that half of the race
// cannot occur there; the test is darwin-only.
func TestStoredSpellingsOf_PostOpenProofCatchesARenameAfterOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows refuses to rename a file held open by another handle")
	}
	dir := t.TempDir()
	path := writeScript(t, dir, "alpha.js", "1")

	_, _, verifyUnchanged, closeSession, err := storedSpellingsOf(dir)
	require.NoError(t, err)
	require.NotNil(t, verifyUnchanged)
	if closeSession != nil {
		defer closeSession()
	}

	f, err := openScriptFile(path)
	require.NoError(t, err)
	defer f.Close()

	require.NoError(t, os.Rename(path, filepath.Join(dir, "ALPHA.JS")))

	verifyErr := verifyUnchanged(f, "alpha.js")
	require.Error(t, verifyErr, "the opened descriptor's spelling no longer matches what was requested")
	assert.True(t, errors.Is(verifyErr, errSpellingUnproven))
}
