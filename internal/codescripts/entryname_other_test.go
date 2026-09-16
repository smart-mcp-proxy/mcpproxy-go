//go:build !darwin && !windows

package codescripts

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveScoped_OnAFoldingDirectory (Spec 105 FR-012, codex r3 #1 and
// r4 #1): Linux has no single-entry call that reports an entry's stored
// spelling, so on a case-folding mount (ext4 casefold, vfat, a bind mount from
// a case-insensitive host) the scoped probe for `backdoor.js` finds
// `backdoor.JS` — a file the listing and the administrator's Resolve reject.
// Round 3 refused every candidate on such a mount, which also refused a
// correctly named `daily.js` to agent tokens while the administrator ran it
// (round 4). The contract now: the fold is proven with one constant-cost probe
// (no listing on the case-sensitive filesystems every native Linux volume
// is), and only where it is proven does entryName list the directory ONCE and
// match the exact on-disk basename — so an exact name runs for every caller,
// a folded spelling is still refused with the ordinary non-disclosing
// not-found, and a mount that folds case pays one listing per existing
// candidate (a retained, documented effect). The folding lookup is simulated
// through the package's lstat seam so the rule is pinned on the case-sensitive
// filesystems CI runs on; the same test against a real folding mount (TMPDIR
// on a Docker Desktop bind mount of an APFS directory) exercises the kernel's
// own fold.
func TestResolveScoped_OnAFoldingDirectory(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "backdoor.JS", "({pwned: true})")
	writeScript(t, dir, "exact.js", "({exact: true})")
	simulateCaseFoldingLstat(t)

	// On a case-sensitive filesystem this first case is refused even without
	// the listing (the real open of `backdoor.js` misses); it bites on a real
	// folding mount. The listing count is what the simulation pins here.
	t.Run("a folded spelling is not a stored script", func(t *testing.T) {
		readDirs, lstats := countDirectoryPrimitives(t)
		src, _, err := ResolveScoped(dir, "backdoor", "")
		var notFound *NotFoundError
		require.True(t, errors.As(err, &notFound), "want *NotFoundError, got %T: %v", err, err)
		assert.True(t, notFound.Undisclosed, "the refusal is the ordinary non-disclosing form")
		assert.NotContains(t, string(src), "pwned")
		assert.Equal(t, 1, *readDirs, "the proven fold is resolved by exactly one listing: the .js probe hit, the .ts probe missed")
		assert.LessOrEqual(t, *lstats, 4, "at most one extra probe per candidate: constant cost")

		// The administrator's directory read agrees: byte-for-byte, .JS is
		// not an extension of a stored script.
		_, _, err = Resolve(dir, "backdoor", "")
		require.True(t, errors.As(err, &notFound))
		assert.False(t, notFound.Undisclosed)
	})

	t.Run("an exactly spelled script on a folding mount runs for scoped callers and administrators alike", func(t *testing.T) {
		readDirs, lstats := countDirectoryPrimitives(t)
		src, lang, err := ResolveScoped(dir, "exact", "")
		require.NoError(t, err, "a correctly named script must not be refused to an agent token (codex r4 #1)")
		assert.Equal(t, "({exact: true})", string(src))
		assert.Equal(t, LanguageJavaScript, lang)
		assert.Equal(t, 1, *readDirs, "the proven fold costs one listing, and only the existing candidate pays it")
		assert.LessOrEqual(t, *lstats, 4)

		src, lang, err = Resolve(dir, "exact", "")
		require.NoError(t, err)
		assert.Equal(t, "({exact: true})", string(src))
		assert.Equal(t, LanguageJavaScript, lang)
	})

	t.Run("entryName reports the stored spelling from one listing", func(t *testing.T) {
		exact := filepath.Join(dir, "exact.js")
		info, err := lstat(exact)
		require.NoError(t, err)
		readDirs, _ := countDirectoryPrimitives(t)
		stored, err := entryName(exact, info)
		require.NoError(t, err)
		assert.Equal(t, "exact.js", stored)
		assert.Equal(t, 1, *readDirs)

		folded := filepath.Join(dir, "backdoor.js")
		info, err = lstat(folded)
		require.NoError(t, err, "the simulated fold finds backdoor.JS")
		stored, err = entryName(folded, info)
		require.NoError(t, err)
		assert.Equal(t, "backdoor.JS", stored, "the on-disk spelling, which probeCandidates then rejects as a fold")
		assert.Equal(t, 2, *readDirs)
	})

	t.Run("a listing the process cannot perform leaves the spelling unverifiable, fail closed", func(t *testing.T) {
		orig := readDir
		readDir = func(string) ([]os.DirEntry, error) {
			return nil, &os.PathError{Op: "open", Path: dir, Err: os.ErrPermission}
		}
		t.Cleanup(func() { readDir = orig })

		exact := filepath.Join(dir, "exact.js")
		info, err := lstat(exact)
		require.NoError(t, err)
		_, err = entryName(exact, info)
		assert.ErrorIs(t, err, errSpellingUnverifiable)

		_, _, err = ResolveScoped(dir, "exact", "")
		var notFound *NotFoundError
		require.True(t, errors.As(err, &notFound), "want *NotFoundError, got %T: %v", err, err)
		assert.True(t, notFound.Undisclosed, "an unverifiable spelling is the ordinary non-disclosing not-found, never the OS error")
	})
}

// TestEntryName_ExactOnACaseSensitiveLookup is the other half: where the
// lookup does not fold (every native Linux filesystem), an entry found by its
// exact name IS that name, no directory is listed, and the scoped resolver
// keeps its O(1) probe.
func TestEntryName_ExactOnACaseSensitiveLookup(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "exact.js", "1")
	path := dir + "/exact.js"
	info, err := os.Lstat(path)
	require.NoError(t, err)
	if folds, _ := foldsCase(path, info); folds {
		t.Skip("this temp directory folds case; that branch is pinned by TestResolveScoped_OnAFoldingDirectory")
	}
	readDirs, lstats := countDirectoryPrimitives(t)
	stored, err := entryName(path, info)
	require.NoError(t, err)
	assert.Equal(t, "exact.js", stored)
	assert.Equal(t, 0, *readDirs, "a case-sensitive lookup is verified by the probe alone, never by a listing")
	assert.Equal(t, 1, *lstats, "exactly the one fold probe")

	src, _, err := ResolveScoped(dir, "exact", "")
	require.NoError(t, err)
	assert.Equal(t, "1", string(src))
	assert.Equal(t, 0, *readDirs, "a scoped hit on a case-sensitive filesystem never reads the directory")
}
