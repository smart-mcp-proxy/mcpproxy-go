//go:build !darwin && !windows

package codescripts

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// simulateCaseFoldingLstat makes the package's lstat seam behave like a
// case-insensitive, case-preserving directory lookup (APFS, NTFS, ext4
// casefold, vfat): a path that does not exist as spelled resolves to the
// entry whose name matches it case-insensitively. The listing it consults is
// the simulation's own (os.ReadDir directly), invisible to the readDir seam.
// Installed BEFORE countDirectoryPrimitives when both are used, so the
// counters see the resolver's calls and not the simulation's.
func simulateCaseFoldingLstat(t *testing.T) {
	t.Helper()
	orig := lstat
	lstat = func(name string) (os.FileInfo, error) {
		info, err := orig(name)
		if err == nil || !errors.Is(err, fs.ErrNotExist) {
			return info, err
		}
		entries, readErr := os.ReadDir(filepath.Dir(name))
		if readErr != nil {
			return nil, err
		}
		for _, e := range entries {
			if strings.EqualFold(e.Name(), filepath.Base(name)) {
				return orig(filepath.Join(filepath.Dir(name), e.Name()))
			}
		}
		return nil, err
	}
	t.Cleanup(func() { lstat = orig })
}

// settleStoredNamesClock moves the index clock far past any directory the
// test writes, so an index taken now counts as settled (a coarse-timestamp
// write can no longer share the recorded stamp) and is trusted until the
// directory's generation moves. Restored on cleanup.
func settleStoredNamesClock(t *testing.T) {
	t.Helper()
	orig := indexClock
	indexClock = func() time.Time { return orig().Add(time.Hour) }
	t.Cleanup(func() { indexClock = orig })
}

// warmStoredNames takes the stored-name index of dir once, with the clock
// settled, so the shared tests that count a scoped resolution's directory
// reads start from a warm index: the one listing is paid per directory
// change (pinned below), never per request.
func warmStoredNames(t *testing.T, dir string) {
	t.Helper()
	settleStoredNamesClock(t)
	_, err := storedNamesFor(dir)
	require.NoError(t, err)
}

// latestStamp is the later of a directory generation's two timestamps.
func latestStamp(gen dirGeneration) time.Time {
	if gen.changeTime.After(gen.modTime) {
		return gen.changeTime
	}
	return gen.modTime
}

// TestResolveScoped_OnAFoldingDirectory (Spec 105 FR-012, codex r3 #1, r4 #1
// and r5 #1): Linux has no single-entry call that reports an entry's stored
// spelling, so on a case-folding mount (ext4 casefold, vfat, a bind mount
// from a case-insensitive host) a probe for `backdoor.js` finds `backdoor.JS`
// — a file the listing and the administrator's Resolve reject. Round 4
// settled the spelling by a listing paid when the probe hit, which made the
// PRESENCE of a differently cased entry cost O(directory) while absence cost
// O(1): a timing oracle on the stored names (round 5). The contract now: the
// scoped resolver answers from the directory's stored-name index, so a
// folded spelling is refused with the ordinary non-disclosing not-found, an
// exact name runs for every caller, and no request lists the directory while
// the index is current. The folding lookup is simulated through the lstat
// seam so the rule is pinned on the case-sensitive filesystems CI runs on;
// the same test on a real folding mount (TMPDIR and GOTMPDIR on a Docker
// Desktop bind mount of an APFS directory) exercises the kernel's own fold.
func TestResolveScoped_OnAFoldingDirectory(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "backdoor.JS", "({pwned: true})")
	writeScript(t, dir, "exact.js", "({exact: true})")
	simulateCaseFoldingLstat(t)
	warmStoredNames(t, dir)

	t.Run("a folded spelling is not a stored script, and settling it lists nothing", func(t *testing.T) {
		readDirs, lstats := countDirectoryPrimitives(t)
		src, _, err := ResolveScoped(dir, "backdoor", "")
		var notFound *NotFoundError
		require.True(t, errors.As(err, &notFound), "want *NotFoundError, got %T: %v", err, err)
		assert.True(t, notFound.Undisclosed, "the refusal is the ordinary non-disclosing form")
		assert.NotContains(t, string(src), "pwned")
		assert.Equal(t, 0, *readDirs, "a warm index answers the fold without a listing (codex r5 #1)")
		assert.Equal(t, 1, *lstats, "one directory Lstat validates the index; the candidate itself is never probed")

		// The administrator's directory read agrees: byte-for-byte, .JS is
		// not an extension of a stored script.
		_, _, err = Resolve(dir, "backdoor", "")
		require.True(t, errors.As(err, &notFound))
		assert.False(t, notFound.Undisclosed)
	})

	t.Run("an exactly spelled script runs for scoped callers and administrators alike", func(t *testing.T) {
		readDirs, lstats := countDirectoryPrimitives(t)
		src, lang, err := ResolveScoped(dir, "exact", "")
		require.NoError(t, err, "a correctly named script must not be refused to an agent token (codex r4 #1)")
		assert.Equal(t, "({exact: true})", string(src))
		assert.Equal(t, LanguageJavaScript, lang)
		assert.Equal(t, 0, *readDirs)
		assert.Equal(t, 2, *lstats, "the directory Lstat plus the one hit's own probe")

		src, lang, err = Resolve(dir, "exact", "")
		require.NoError(t, err)
		assert.Equal(t, "({exact: true})", string(src))
		assert.Equal(t, LanguageJavaScript, lang)
	})

	t.Run("an absent name and a present case-variant cost the same, cold and warm", func(t *testing.T) {
		cost := func(name string) (readDirs, lstats int) {
			storedNameIndexes.Delete(filepath.Clean(dir)) // cold
			rd, ls := countDirectoryPrimitives(t)
			_, _, err := ResolveScoped(dir, name, "")
			var notFound *NotFoundError
			require.True(t, errors.As(err, &notFound), "want *NotFoundError, got %T: %v", err, err)
			cold := *rd
			assert.Equal(t, 1, cold, "%s: a cold index is one listing, whatever the name", name)
			_, _, _ = ResolveScoped(dir, name, "")
			assert.Equal(t, 1, *rd, "%s: the second request finds the index warm", name)
			return cold, *ls
		}
		absentReadDirs, absentLstats := cost("missing")
		variantReadDirs, variantLstats := cost("backdoor")
		assert.Equal(t, absentReadDirs, variantReadDirs, "the listing count does not depend on the requested name")
		assert.Equal(t, absentLstats, variantLstats, "nor does the probe count")
	})

	t.Run("the index holds the stored spelling, so the fold is settled by an exact lookup", func(t *testing.T) {
		names, err := storedNamesFor(dir)
		require.NoError(t, err)
		assert.Contains(t, names, "backdoor.JS")
		assert.NotContains(t, names, "backdoor.js")
		assert.Contains(t, names, "exact.js")
	})
}

// TestStoredNames_ListedOncePerDirectoryGeneration pins the cost rule of the
// index: a directory that does not change is listed once, however many
// requests are answered from it and whatever they ask for; a change (an
// entry added) is one more listing, and the next requests are warm again.
func TestStoredNames_ListedOncePerDirectoryGeneration(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "alpha.js", "1")
	settleStoredNamesClock(t)
	readDirs, _ := countDirectoryPrimitives(t)

	requests := func(names ...string) {
		for i := 0; i < 20; i++ {
			_, _, _ = ResolveScoped(dir, names[i%len(names)], "")
		}
	}
	requests("alpha", "missing", "ALPHA")
	assert.Equal(t, 1, *readDirs, "an unchanged directory is listed exactly once")

	before, err := lstat(dir)
	require.NoError(t, err)
	// The write must land on a later stamp than the one recorded, whatever
	// the filesystem's timestamp granularity: past generationSettleTime is
	// the guarantee the index itself relies on.
	time.Sleep(time.Until(latestStamp(dirGenerationOf(before)).Add(generationSettleTime)))
	writeScript(t, dir, "beta.ts", "1")
	waitForGenerationChange(t, dir, dirGenerationOf(before))

	src, lang, err := ResolveScoped(dir, "beta", "")
	require.NoError(t, err, "a script added after the listing is found on the next request")
	assert.Equal(t, "1", string(src))
	assert.Equal(t, LanguageTypeScript, lang)
	assert.Equal(t, 2, *readDirs, "the change is one more listing")

	requests("alpha", "beta", "missing")
	assert.Equal(t, 2, *readDirs, "and the directory is warm again")

	require.NoError(t, os.Remove(filepath.Join(dir, "alpha.js")))
	_, _, err = ResolveScoped(dir, "alpha", "")
	var notFound *NotFoundError
	require.True(t, errors.As(err, &notFound), "a removed script is not found on the next request (%T: %v)", err, err)
	assert.True(t, notFound.Undisclosed)
}

// waitForGenerationChange confirms the directory's stamp moved with the
// write (it returns at once on every filesystem this test has met); a mount
// whose stamp never moves cannot pin the change count and is skipped.
func waitForGenerationChange(t *testing.T, dir string, was dirGeneration) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		info, err := lstat(dir)
		require.NoError(t, err)
		if !dirGenerationOf(info).equal(was) {
			return
		}
		if time.Now().After(deadline) {
			t.Skipf("%s: the directory's stamp did not move after a write", dir)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestStoredNames_RelistsUntilTheStampSettles pins the coarse-timestamp
// guard: an index taken within generationSettleTime of the directory's stamp
// is retaken on every request (a write in the same tick would not move the
// stamp) — for every name alike, the bound depends on the clock only — and
// once the stamp is old enough the next listing is the last.
func TestStoredNames_RelistsUntilTheStampSettles(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "alpha.js", "1")
	info, err := lstat(dir)
	require.NoError(t, err)
	stamp := latestStamp(dirGenerationOf(info))

	orig := indexClock
	t.Cleanup(func() { indexClock = orig })
	indexClock = func() time.Time { return stamp.Add(generationSettleTime / 2) }
	readDirs, _ := countDirectoryPrimitives(t)
	for i, name := range []string{"alpha", "missing", "alpha"} {
		_, _, _ = ResolveScoped(dir, name, "")
		assert.Equal(t, i+1, *readDirs, "within the settle window every request re-lists")
	}

	indexClock = func() time.Time { return stamp.Add(generationSettleTime) }
	_, _, _ = ResolveScoped(dir, "missing", "")
	assert.Equal(t, 4, *readDirs, "the first request past the window lists once more")
	_, _, _ = ResolveScoped(dir, "alpha", "")
	_, _, _ = ResolveScoped(dir, "missing", "")
	assert.Equal(t, 4, *readDirs, "and the settled index is trusted")
}

// TestStoredNames_UnlistableDirectoryRefusesScopedCallers: a scripts
// directory the process cannot list has no index, so the scoped resolver
// refuses — with the non-disclosing unreadable form, no path and no OS error
// — exactly where the administrator's directory read refuses (SC-005),
// rather than executing out of a directory the listing cannot vouch for.
func TestStoredNames_UnlistableDirectoryRefusesScopedCallers(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions are not enforced")
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced on Windows")
	}
	scriptsDir := filepath.Join(t.TempDir(), "scripts")
	writeScript(t, scriptsDir, "known.js", "1")
	require.NoError(t, os.Chmod(scriptsDir, 0o111))
	t.Cleanup(func() { _ = os.Chmod(scriptsDir, 0o755) })

	src, _, err := ResolveScoped(scriptsDir, "known", "")
	require.Nil(t, src)
	var invalid *InvalidError
	require.True(t, errors.As(err, &invalid), "want *InvalidError, got %T: %v", err, err)
	assert.True(t, invalid.Undisclosed)
	assert.Equal(t, ReasonUnreadable, invalid.Reason)
	assert.NotContains(t, err.Error(), scriptsDir)
	assert.NotContains(t, err.Error(), "permission denied")

	_, _, err = Resolve(scriptsDir, "known", "")
	require.True(t, errors.As(err, &invalid))
	assert.Equal(t, ReasonUnreadable, invalid.Reason, "the administrator is refused for the same reason")
}
