//go:build !darwin && !windows

package codescripts

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveScoped_FailsClosedOnAFoldingDirectory (Spec 105 FR-012, codex r3
// #1): Linux has no single-entry call that reports an entry's stored spelling,
// so on a case-folding mount (ext4 casefold, vfat, a bind mount from a
// case-insensitive host) the scoped probe for `backdoor.js` finds
// `backdoor.JS` — a file the listing and the administrator's Resolve reject —
// and, before this fix, executed it. The fold is now proven with one extra
// constant-cost probe and the candidate refused: a scoped caller cannot
// execute what no discovery surface reports, and the refusal is the ordinary
// non-disclosing not-found. The folding lookup is simulated through the
// package's lstat seam so the rule is pinned on the case-sensitive filesystems
// CI runs on; the same test against a real folding mount (TMPDIR on a Docker
// Desktop bind mount of an APFS directory) exercises the kernel's own fold.
func TestResolveScoped_FailsClosedOnAFoldingDirectory(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "backdoor.JS", "({pwned: true})")
	writeScript(t, dir, "exact.js", "({exact: true})")
	simulateCaseFoldingLstat(t)

	// On a case-sensitive filesystem this first case is refused even without
	// the fold proof (the real open of `backdoor.js` misses); it bites on a
	// real folding mount. The second case is the one the simulation pins on
	// every host: neutering the Linux entryName fails it.
	t.Run("folded spelling is not a stored script", func(t *testing.T) {
		readDirs, lstats := countDirectoryPrimitives(t)
		src, _, err := ResolveScoped(dir, "backdoor", "")
		var notFound *NotFoundError
		require.True(t, errors.As(err, &notFound), "want *NotFoundError, got %T: %v", err, err)
		assert.True(t, notFound.Undisclosed, "the refusal is the ordinary non-disclosing form")
		assert.NotContains(t, string(src), "pwned")
		assert.Equal(t, 0, *readDirs, "proving the fold must not list the directory")
		assert.LessOrEqual(t, *lstats, 4, "at most one extra probe per candidate: constant cost")

		// The administrator's directory read agrees: byte-for-byte, .JS is
		// not an extension of a stored script.
		_, _, err = Resolve(dir, "backdoor", "")
		require.True(t, errors.As(err, &notFound))
		assert.False(t, notFound.Undisclosed)
	})

	t.Run("an exactly spelled script on a folding mount is refused to scoped callers, fail closed", func(t *testing.T) {
		// Without a stored-spelling call the probe cannot tell this case from
		// the one above, so it must refuse both; the administrator, whose
		// candidates come from the directory read, still runs it.
		_, _, err := ResolveScoped(dir, "exact", "")
		var notFound *NotFoundError
		require.True(t, errors.As(err, &notFound), "want *NotFoundError, got %T: %v", err, err)
		assert.True(t, notFound.Undisclosed)

		src, lang, err := Resolve(dir, "exact", "")
		require.NoError(t, err)
		assert.Equal(t, "({exact: true})", string(src))
		assert.Equal(t, LanguageJavaScript, lang)
	})

	t.Run("entryName reports the fold as unverifiable", func(t *testing.T) {
		path := dir + "/exact.js"
		info, err := lstat(path)
		require.NoError(t, err)
		_, err = entryName(path, info)
		assert.ErrorIs(t, err, errSpellingUnverifiable)
	})
}

// TestEntryName_ExactOnACaseSensitiveLookup is the other half: where the
// lookup does not fold (every native Linux filesystem), an entry found by its
// exact name IS that name and the scoped resolver keeps working.
func TestEntryName_ExactOnACaseSensitiveLookup(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "exact.js", "1")
	path := dir + "/exact.js"
	info, err := os.Lstat(path)
	require.NoError(t, err)
	if folds, _ := foldsCase(path, info); folds {
		t.Skip("this temp directory folds case; the fail-closed rule is pinned by TestResolveScoped_FailsClosedOnAFoldingDirectory")
	}
	stored, err := entryName(path, info)
	require.NoError(t, err)
	assert.Equal(t, "exact.js", stored)

	src, _, err := ResolveScoped(dir, "exact", "")
	require.NoError(t, err)
	assert.Equal(t, "1", string(src))
}
