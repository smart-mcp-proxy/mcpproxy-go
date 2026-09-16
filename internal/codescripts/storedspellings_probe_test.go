//go:build darwin || windows

package codescripts

import (
	"errors"
	"os"
	"path/filepath"
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

	storedExactly, verifyUnchanged, err := storedSpellingsOf(dir)
	require.NoError(t, err)
	require.NotNil(t, verifyUnchanged, "darwin/Windows always supply the authoritative post-open check")
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

	_, verifyUnchanged, err := storedSpellingsOf(dir)
	require.NoError(t, err)
	require.NotNil(t, verifyUnchanged)

	f, err := openScriptFile(path)
	require.NoError(t, err)
	defer f.Close()

	// The race: case-rename the file the descriptor is already reading.
	// F_GETPATH / GetFinalPathNameByHandle on the open descriptor now report
	// the RENAMED spelling — proving the descriptor is no longer the exact
	// name that was requested and probed.
	require.NoError(t, os.Rename(path, filepath.Join(dir, "ALPHA.JS")))

	verifyErr := verifyUnchanged(f, "alpha.js")
	require.Error(t, verifyErr, "the opened descriptor's spelling no longer matches what was requested")
	assert.True(t, errors.Is(verifyErr, errSpellingUnproven))
}
