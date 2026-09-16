//go:build !darwin && !windows

package codescripts

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	quiesceIndexRebuilds()
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
	t.Cleanup(func() {
		quiesceIndexRebuilds()
		lstat = orig
	})
}

// quiesceIndexRebuilds waits for every rebuild goroutine the tests so far
// have left in flight. The package's seams (readDir, lstat, indexClock,
// spawnIndexRebuild) are process-wide, so a helper that installs or restores
// one must first let any rebuild still reading them land.
func quiesceIndexRebuilds() {
	storedNameIndexes.Range(func(_, v any) bool {
		idx := v.(*storedNames)
		idx.mu.Lock()
		building, landed := idx.building, idx.landed
		idx.mu.Unlock()
		if building {
			<-landed
		}
		return true
	})
}

// settleStoredNamesClock moves the index clock far past any directory the
// test writes, so an index taken now counts as settled (a coarse-timestamp
// write can no longer share the recorded stamp) and is trusted until the
// directory's generation moves. Restored on cleanup.
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
// reads start from a warm index — as the server does at construction. The
// one listing is paid off the request path (pinned below), never per request.
func warmStoredNames(t *testing.T, dir string) {
	t.Helper()
	settleStoredNamesClock(t)
	require.NoError(t, Warm(dir))
}

// heldRebuilds is the test's grip on the rebuild goroutines: while installed,
// a request that schedules a rebuild hands it here instead of spawning it, so
// what the request does on its OWN goroutine is exactly what the counters
// see, and the rebuild lands only when the test says so.
type heldRebuilds struct {
	mu   sync.Mutex
	held []func()
}

// holdIndexRebuilds installs the grip for the test's duration; whatever is
// still held at cleanup is landed so no index is left claimed.
func holdIndexRebuilds(t *testing.T) *heldRebuilds {
	t.Helper()
	h := &heldRebuilds{}
	quiesceIndexRebuilds()
	orig := spawnIndexRebuild
	spawnIndexRebuild = func(rebuild func()) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.held = append(h.held, rebuild)
	}
	t.Cleanup(func() {
		spawnIndexRebuild = orig
		h.land()
	})
	return h
}

// land runs every held rebuild on the test goroutine and reports how many
// there were — how many the requests since the last land scheduled.
func (h *heldRebuilds) land() int {
	h.mu.Lock()
	held := h.held
	h.held = nil
	h.mu.Unlock()
	for _, rebuild := range held {
		rebuild()
	}
	return len(held)
}

// waitForIndexRebuild blocks until the rebuild goroutine of dir, if one is in
// flight, has landed: the seam a test waits on instead of sleeping.
func waitForIndexRebuild(t *testing.T, dir string) {
	t.Helper()
	idx := storedNamesIndex(filepath.Clean(dir))
	idx.mu.Lock()
	building, landed := idx.building, idx.landed
	idx.mu.Unlock()
	if !building {
		return
	}
	select {
	case <-landed:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s: the index rebuild did not land", dir)
	}
}

// requireScopedNotFound asserts the ordinary non-disclosing not-found refusal.
func requireScopedNotFound(t *testing.T, err error) {
	t.Helper()
	var notFound *NotFoundError
	require.True(t, errors.As(err, &notFound), "want *NotFoundError, got %T: %v", err, err)
	assert.True(t, notFound.Undisclosed, "the refusal is the ordinary non-disclosing form")
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
// exact name runs for every caller, and no request lists the directory —
// warm or cold. The folding lookup is simulated through the lstat seam so
// the rule is pinned on the case-sensitive filesystems CI runs on; the same
// test on a real folding mount (TMPDIR and GOTMPDIR on a Docker Desktop bind
// mount of an APFS directory) exercises the kernel's own fold.
func TestResolveScoped_OnAFoldingDirectory(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "backdoor.JS", "({pwned: true})")
	writeScript(t, dir, "exact.js", "({exact: true})")
	simulateCaseFoldingLstat(t)
	warmStoredNames(t, dir)

	t.Run("a folded spelling is not a stored script, and settling it lists nothing", func(t *testing.T) {
		readDirs, lstats := countDirectoryPrimitives(t)
		src, _, err := ResolveScoped(dir, "backdoor", "")
		requireScopedNotFound(t, err)
		assert.NotContains(t, string(src), "pwned")
		assert.Equal(t, 0, *readDirs, "a warm index answers the fold without a listing (codex r5 #1)")
		assert.Equal(t, 1, *lstats, "one directory Lstat validates the index; the candidate itself is never probed")

		// The administrator's directory read agrees: byte-for-byte, .JS is
		// not an extension of a stored script.
		var notFound *NotFoundError
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
		assert.Equal(t, 3, *lstats, "the directory Lstat, the hit's own probe, and the post-open generation recheck (round 8 MUST-FIX)")

		src, lang, err = Resolve(dir, "exact", "")
		require.NoError(t, err)
		assert.Equal(t, "({exact: true})", string(src))
		assert.Equal(t, LanguageJavaScript, lang)
	})

	t.Run("an absent name and a present case-variant cost the same, cold and warm", func(t *testing.T) {
		held := holdIndexRebuilds(t)
		cost := func(name string) (readDirs, lstats int) {
			storedNameIndexes.Delete(filepath.Clean(dir)) // cold
			rd, ls := countDirectoryPrimitives(t)
			_, _, err := ResolveScoped(dir, name, "")
			requireScopedNotFound(t, err)
			assert.Equal(t, 0, *rd, "%s: a cold request lists nothing itself (codex r6 #1)", name)
			assert.Equal(t, 1, held.land(), "%s: it schedules the one rebuild", name)
			assert.Equal(t, 1, *rd, "%s: which is the one listing, off the request path", name)
			cold := *ls
			_, _, err = ResolveScoped(dir, name, "")
			requireScopedNotFound(t, err)
			assert.Equal(t, 1, *rd, "%s: the second request finds the index warm", name)
			assert.Equal(t, 0, held.land(), "%s: and schedules nothing", name)
			return *rd, cold
		}
		absentReadDirs, absentLstats := cost("missing")
		variantReadDirs, variantLstats := cost("backdoor")
		assert.Equal(t, absentReadDirs, variantReadDirs, "the listing count does not depend on the requested name")
		assert.Equal(t, absentLstats, variantLstats, "nor does the probe count")
	})

	t.Run("the index holds the stored spelling, so the fold is settled by an exact lookup", func(t *testing.T) {
		names, _, err := storedNamesFor(dir)
		require.NoError(t, err)
		assert.Contains(t, names, "backdoor.JS")
		assert.NotContains(t, names, "backdoor.js")
		assert.Contains(t, names, "exact.js")
	})
}

// TestResolveScoped_StaleIndexRefusesARenamedEntry (Spec 105 FR-012, codex r7
// #1 / round 8 MUST-FIX): earlier rounds scheduled a rebuild when a request
// found the index behind the directory's generation, but still answered
// from the index as it stood before — a stale index that once listed an
// entry under an EARLIER spelling stayed good enough to authorize it. On a
// case-folding mount that executes the wrong file: warm the index with
// `report.js`, then rename it to `REPORT.JS` (a real rename, so the
// directory's generation genuinely moves); the stale index still contains
// `report.js`, and that entry's own Lstat — simulated through the lstat seam
// so the fold is exercised on the case-sensitive filesystems CI runs on —
// folds onto the renamed file and succeeds, which round 7 trusted as a hit.
// The fix: the index answers ONLY for the generation it was built against,
// so a request landing while the rebuild is merely scheduled is refused
// exactly like a never-built index, without ever probing the candidate the
// stale index used to hold.
func TestResolveScoped_StaleIndexRefusesARenamedEntry(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "report.js", "({pwned: true})")
	simulateCaseFoldingLstat(t)
	warmStoredNames(t, dir)
	held := holdIndexRebuilds(t)

	outliveStamp(t, dir)
	before, err := lstat(dir)
	require.NoError(t, err)
	require.NoError(t, os.Rename(filepath.Join(dir, "report.js"), filepath.Join(dir, "REPORT.JS")))
	waitForGenerationChange(t, dir, dirGenerationOf(before))

	readDirs, lstats := countDirectoryPrimitives(t)
	src, _, err := ResolveScoped(dir, "report", "")
	requireScopedNotFound(t, err)
	assert.Nil(t, src, "the stale index must never authorize the renamed file, whatever its own Lstat folds onto")
	assert.Equal(t, 0, *readDirs, "the refusal lists nothing (it is fail-closed on the generation mismatch alone)")
	assert.Equal(t, 1, *lstats, "one directory Lstat decides staleness; the stale index's candidate is never probed")
	assert.Equal(t, 1, held.land(), "the rename moved the generation: one rebuild is scheduled")
	assert.Equal(t, 1, *readDirs, "which is the one listing, off the request path")

	// The rebuild has landed: the index now holds REPORT.JS, not report.js.
	// The old spelling is still refused — never executed — for the same
	// reason the administrator's byte-for-byte decision refuses it too.
	src, _, err = ResolveScoped(dir, "report", "")
	requireScopedNotFound(t, err)
	assert.Nil(t, src)

	var notFound *NotFoundError
	_, _, err = Resolve(dir, "report", "")
	require.True(t, errors.As(err, &notFound))
	assert.False(t, notFound.Undisclosed)
}

// TestResolveScoped_GenerationChangeBetweenLookupAndOpenRefuses (round 8
// MUST-FIX, the lookup→open race): an index hit is re-probed by the
// candidate's own Lstat, but neither that nor a successful no-follow open
// proves the file just opened is the one the index vouched for — a write
// landing between the probe and the open can leave a DIFFERENT file
// occupying the exact name for the descriptor's entire lifetime, and a
// no-follow open does not compare names, only symlink status. The directory
// generation is read once more after the open and must still equal the one
// read before the lookup; a mismatch closes the descriptor and refuses. The
// race is simulated deterministically through the lstat seam: the
// directory's SECOND Lstat this request performs (the post-open recheck) is
// where a real race could land at an arbitrary point, so that is where the
// swap happens here.
func TestResolveScoped_GenerationChangeBetweenLookupAndOpenRefuses(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "alpha.js", "({original: true})")
	warmStoredNames(t, dir)

	orig := lstat
	seenDirLstats := 0
	t.Cleanup(func() { lstat = orig })
	lstat = func(name string) (os.FileInfo, error) {
		if name == dir {
			seenDirLstats++
			if seenDirLstats == 2 {
				// Races the open: a write lands after the index vouched for
				// the candidate but before the descriptor is trusted.
				require.NoError(t, os.Remove(filepath.Join(dir, "alpha.js")))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "alpha.js"), []byte("({swapped: true})"), 0o644))
				// A same-name remove-then-recreate can land on the exact
				// same coarse directory timestamp as the original write (a
				// container filesystem observed to do this even at
				// nanosecond "resolution"): force the generation forward so
				// it is unambiguously the write's, not the clock's
				// granularity, that the recheck must catch.
				require.NoError(t, os.Chtimes(dir, time.Now(), time.Now().Add(time.Second)))
			}
		}
		return orig(name)
	}

	src, _, err := ResolveScoped(dir, "alpha", "")
	requireScopedNotFound(t, err)
	assert.Nil(t, src, "a file swapped in during the open's own window must never be read, original or swapped content alike")
	assert.Equal(t, 2, seenDirLstats, "the directory Lstat before the lookup and the recheck after the open")
}

// TestResolveScoped_ColdRequestCostIsIndependentOfDirectorySize (codex r6
// #1): the FIRST scoped request against a directory — before any index
// exists — must cost the same for an empty directory and for one holding ten
// thousand scripts. It lists nothing on its own goroutine, performs the same
// one directory Lstat, schedules the one rebuild and is refused fail-closed;
// the listing happens when the rebuild lands, and the next request is
// answered from it.
func TestResolveScoped_ColdRequestCostIsIndependentOfDirectorySize(t *testing.T) {
	empty := t.TempDir()
	crowded := t.TempDir()
	for i := 0; i < 10_000; i++ {
		writeScript(t, crowded, fmt.Sprintf("script-%05d.js", i), "1")
	}
	settleStoredNamesClock(t)
	held := holdIndexRebuilds(t)

	probe := func(dir string) (readDirs, lstats int) {
		storedNameIndexes.Delete(filepath.Clean(dir)) // cold: never warmed
		rd, ls := countDirectoryPrimitives(t)
		_, _, err := ResolveScoped(dir, "script-00042", "")
		requireScopedNotFound(t, err)
		assert.Equal(t, 0, *rd, "%s: a cold request must not list on its own goroutine", dir)
		lstats = *ls
		assert.Equal(t, 1, held.land(), "%s: the cold request schedules exactly one rebuild", dir)
		return *rd, lstats
	}

	emptyReadDirs, emptyLstats := probe(empty)
	crowdedReadDirs, crowdedLstats := probe(crowded)
	assert.Equal(t, 1, emptyReadDirs, "the rebuild is the one listing")
	assert.Equal(t, 1, crowdedReadDirs, "ten thousand entries are listed once, off the request path")
	assert.Equal(t, emptyLstats, crowdedLstats, "the number of Lstats is independent of the directory's contents")
	assert.Equal(t, 1, emptyLstats, "the request's own directory Lstat")

	// Landed: the script that was refused a moment ago now runs, with no
	// listing on the request goroutine and none scheduled.
	rd, ls := countDirectoryPrimitives(t)
	src, _, err := ResolveScoped(crowded, "script-00042", "")
	require.NoError(t, err, "after the rebuild lands the same request executes")
	assert.Equal(t, "1", string(src))
	assert.Equal(t, 0, *rd)
	assert.Equal(t, 3, *ls, "the directory Lstat, the hit's own probe, and the post-open generation recheck (round 8 MUST-FIX)")
	assert.Equal(t, 0, held.land())
}

// TestStoredNames_GenerationChangeRebuildsOffTheRequestPath pins the cost
// rule of the index: an unchanged directory is never listed again, however
// many requests are answered from it and whatever they ask for; a change (an
// entry added) is one asynchronous listing that no request performs — the
// request that notices it is refused fail-closed and the next one sees the
// new script.
func TestStoredNames_GenerationChangeRebuildsOffTheRequestPath(t *testing.T) {
	t.Run("held: the request lists nothing and the landed rebuild serves the next", func(t *testing.T) {
		dir := t.TempDir()
		writeScript(t, dir, "alpha.js", "1")
		warmStoredNames(t, dir)
		held := holdIndexRebuilds(t)
		readDirs, lstats := countDirectoryPrimitives(t)

		for i, name := range []string{"alpha", "missing", "ALPHA"} {
			for j := 0; j < 20; j++ {
				_, _, _ = ResolveScoped(dir, name, "")
			}
			assert.Equal(t, 0, *readDirs, "%d: an unchanged directory is never listed", i)
			assert.Equal(t, 0, held.land(), "%d: nor is a rebuild scheduled", i)
		}

		outliveStamp(t, dir)
		before, err := lstat(dir)
		require.NoError(t, err)
		writeScript(t, dir, "beta.ts", "1")
		waitForGenerationChange(t, dir, dirGenerationOf(before))

		*lstats = 0
		_, _, err = ResolveScoped(dir, "beta", "")
		requireScopedNotFound(t, err) // fail closed until the rebuild lands
		assert.Equal(t, 0, *readDirs, "the request that finds the generation moved lists nothing itself (codex r6 #1)")
		assert.Equal(t, 1, *lstats, "one directory Lstat, no candidate probe")
		assert.Equal(t, 1, held.land(), "it schedules the one rebuild")
		assert.Equal(t, 1, *readDirs, "which is the one listing")

		src, lang, err := ResolveScoped(dir, "beta", "")
		require.NoError(t, err, "the script added is found once the rebuild has landed")
		assert.Equal(t, "1", string(src))
		assert.Equal(t, LanguageTypeScript, lang)
		assert.Equal(t, 1, *readDirs)
		assert.Equal(t, 0, held.land(), "the directory is warm again")
	})

	t.Run("live: the rebuild goroutine lands and the next request sees the script", func(t *testing.T) {
		dir := t.TempDir()
		writeScript(t, dir, "alpha.js", "1")
		warmStoredNames(t, dir)

		outliveStamp(t, dir)
		before, err := lstat(dir)
		require.NoError(t, err)
		writeScript(t, dir, "beta.ts", "1")
		waitForGenerationChange(t, dir, dirGenerationOf(before))

		readDirs, _ := countDirectoryPrimitives(t)
		_, _, err = ResolveScoped(dir, "beta", "")
		requireScopedNotFound(t, err)
		waitForIndexRebuild(t, dir)
		assert.Equal(t, 1, *readDirs, "the rebuild is the one listing")

		src, _, err := ResolveScoped(dir, "beta", "")
		require.NoError(t, err, "a script added to the directory is callable after the rebuild lands")
		assert.Equal(t, "1", string(src))
		assert.Equal(t, 1, *readDirs)

		// The administrator's directory read never waited for anything.
		_, _, err = Resolve(dir, "beta", "")
		require.NoError(t, err)
	})
}

// outliveStamp sleeps until the directory's latest stamp is
// generationSettleTime old, so the next write lands on a later stamp whatever
// the filesystem's timestamp granularity (Linux stamps files with the coarse
// tick clock, so a write in the same tick as the index's listing would not
// move the generation — the guarantee the index itself relies on).
func outliveStamp(t *testing.T, dir string) {
	t.Helper()
	info, err := lstat(dir)
	require.NoError(t, err)
	time.Sleep(time.Until(dirGenerationOf(info).latest().Add(generationSettleTime)))
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

// TestStoredNames_RemovedScriptFailsClosedBeforeTheRebuildLands: a stale
// index — its rebuild scheduled by a generation change but not yet landed —
// is refused exactly as a never-built index is (round 8 MUST-FIX): a script
// removed after the last listing is refused at once, before the rebuild
// that will drop it from the index has even STARTED to run, and WITHOUT
// probing the candidate the stale index used to hold (earlier rounds still
// answered from that stale index and let the candidate's own Lstat, which
// happened to miss here, catch the removal — a stale index is refused on
// the generation mismatch alone now, so there is nothing left for a
// candidate probe to catch or miss).
func TestStoredNames_RemovedScriptFailsClosedBeforeTheRebuildLands(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "alpha.js", "1")
	warmStoredNames(t, dir)
	held := holdIndexRebuilds(t)

	outliveStamp(t, dir)
	before, err := lstat(dir)
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(dir, "alpha.js")))
	waitForGenerationChange(t, dir, dirGenerationOf(before))
	readDirs, lstats := countDirectoryPrimitives(t)
	_, _, err = ResolveScoped(dir, "alpha", "")
	requireScopedNotFound(t, err)
	assert.Equal(t, 0, *readDirs, "the refusal lists nothing")
	assert.Equal(t, 1, *lstats, "the directory Lstat alone decides staleness; the stale index's candidate is never probed (round 8 MUST-FIX)")

	assert.Equal(t, 1, held.land(), "the removal moved the generation: one rebuild")
	names, _, err := storedNamesFor(dir)
	require.NoError(t, err)
	assert.NotContains(t, names, "alpha.js")
	_, _, err = ResolveScoped(dir, "alpha", "")
	requireScopedNotFound(t, err)
}

// TestStoredNames_UnsettledIndexRefreshesAtMostOncePerWindow pins the
// coarse-timestamp guard: an index taken within generationSettleTime of the
// directory's stamp cannot rule out a write in the same tick, so requests
// keep scheduling a refresh — at most one per window, off the request path,
// for every name alike — and once a listing lands past the window the index
// is trusted until the stamp moves. The request's own cost never changes.
func TestStoredNames_UnsettledIndexRefreshesAtMostOncePerWindow(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "alpha.js", "1")
	info, err := lstat(dir)
	require.NoError(t, err)
	stamp := dirGenerationOf(info).latest()

	quiesceIndexRebuilds()
	orig := indexClock
	t.Cleanup(func() { indexClock = orig })
	indexClock = func() time.Time { return stamp.Add(generationSettleTime / 2) }
	held := holdIndexRebuilds(t)
	require.NoError(t, Warm(dir), "warmed inside the window: the index is not settled")
	readDirs, lstats := countDirectoryPrimitives(t)

	// requests issues scoped misses and pins each one's own cost: no listing,
	// one directory Lstat — the landed rebuilds' calls are counted between.
	requests := func(label string, names ...string) {
		for i, name := range names {
			rd, ls := *readDirs, *lstats
			_, _, err := ResolveScoped(dir, name, "")
			requireScopedNotFound(t, err)
			assert.Equal(t, rd, *readDirs, "%s %d: a request never lists", label, i)
			assert.Equal(t, ls+1, *lstats, "%s %d: one directory Lstat per request", label, i)
		}
	}

	requests("inside the window", "missing", "gamma", "missing")
	assert.Equal(t, 1, held.land(), "the unsettled index schedules ONE refresh per window, not one per request")
	assert.Equal(t, 1, *readDirs)
	requests("still inside", "missing", "gamma")
	assert.Equal(t, 0, held.land(), "the window is open until it elapses")

	// The refresh window elapsed but the stamp is still too young: one more.
	indexClock = func() time.Time { return stamp.Add(generationSettleTime/2 + generationSettleTime) }
	requests("next window", "missing")
	assert.Equal(t, 1, held.land(), "the next window schedules one more refresh")
	assert.Equal(t, 2, *readDirs)
	requests("settled", "missing", "gamma", "missing")
	assert.Equal(t, 0, held.land(), "the listing landed past the stamp's settle time: the index is trusted")
	assert.Equal(t, 2, *readDirs)
}

// TestStoredNames_RebuildAttemptsAreBounded (round 8 SHOULD): a directory
// whose generation moves on every observation — as another process
// continuously renaming an entry would leave it — must not keep a rebuild
// goroutine re-listing forever, and must not keep Warm blocked forever
// either. rebuild gives up after maxRebuildAttempts listings whatever the
// directory keeps doing next; what the last attempt installed simply goes
// stale against the directory's true current generation, and the next
// request's own check (the MUST-FIX rule above) refuses it rather than this
// loop spinning to prove something it never can.
func TestStoredNames_RebuildAttemptsAreBounded(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "alpha.js", "1")
	quiesceIndexRebuilds()

	origLstat, origReadDir := lstat, readDir
	var lstats, readDirs int
	t.Cleanup(func() { lstat, readDir = origLstat, origReadDir })
	lstat = func(name string) (os.FileInfo, error) {
		if name == dir {
			lstats++
			// Simulate another process continuously changing the directory:
			// its own generation moves on every observation, so rebuild's
			// list-then-recheck can never confirm stability.
			require.NoError(t, os.Chtimes(dir, time.Now(), time.Now().Add(time.Duration(lstats)*time.Second)))
		}
		return origLstat(name)
	}
	readDir = func(name string) ([]fs.DirEntry, error) {
		readDirs++
		return origReadDir(name)
	}

	done := make(chan error, 1)
	go func() { done <- Warm(dir) }()
	select {
	case err := <-done:
		require.NoError(t, err, "a continuously changing directory must not fail Warm outright")
	case <-time.After(10 * time.Second):
		t.Fatal("Warm did not return against a continuously changing directory (round 8 SHOULD)")
	}
	assert.Equal(t, maxRebuildAttempts, readDirs, "one rebuild lists at most maxRebuildAttempts times, however long the directory keeps changing")
}

// TestStoredNames_RebuildBackoffThrottlesReschedules (round 8 SHOULD): once
// a rebuild ends — landing cleanly or giving up after maxRebuildAttempts —
// the next one for the same directory may not start until rebuildBackoff has
// passed, whatever the request rate: without this, a directory that changes
// on every request would let scheduleRebuildLocked spawn a fresh rebuild the
// instant the bounded one above gives up, resuming the same unbounded
// listing cost one goroutine later. A request inside the backoff still
// costs one Lstat and answers fail-closed from whatever the index holds (or
// does not); only the new rebuild goroutine is withheld.
func TestStoredNames_RebuildBackoffThrottlesReschedules(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "alpha.js", "1")
	warmStoredNames(t, dir)

	quiesceIndexRebuilds()
	orig := indexClock
	t.Cleanup(func() { indexClock = orig })
	base := orig()
	indexClock = func() time.Time { return base }

	outliveStamp(t, dir)
	before, err := lstat(dir)
	require.NoError(t, err)
	writeScript(t, dir, "beta.ts", "1")
	waitForGenerationChange(t, dir, dirGenerationOf(before))

	held := holdIndexRebuilds(t)
	_, _, err = ResolveScoped(dir, "beta", "")
	requireScopedNotFound(t, err)
	assert.Equal(t, 1, held.land(), "the generation change schedules the first rebuild")
	// indexClock is still `base`: rebuild just set nextAttempt to
	// base+rebuildBackoff.

	outliveStamp(t, dir)
	before2, err := lstat(dir)
	require.NoError(t, err)
	writeScript(t, dir, "gamma.ts", "1")
	waitForGenerationChange(t, dir, dirGenerationOf(before2))

	_, _, err = ResolveScoped(dir, "gamma", "")
	requireScopedNotFound(t, err)
	assert.Equal(t, 0, held.land(), "a request inside the backoff window schedules nothing, though the generation moved again")

	indexClock = func() time.Time { return base.Add(rebuildBackoff) }
	_, _, err = ResolveScoped(dir, "gamma", "")
	requireScopedNotFound(t, err)
	assert.Equal(t, 1, held.land(), "past the backoff, the still-unresolved generation mismatch schedules again")
}

// TestStoredNames_WarmListsAfterAnInFlightRebuild: Warm is the server's
// promise that the index reflects the directory as it was when Warm was
// called, so a rebuild already in flight — which may have listed before the
// latest write — is waited for and then Warm lists again.
func TestStoredNames_WarmListsAfterAnInFlightRebuild(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "alpha.js", "1")
	settleStoredNamesClock(t)
	held := holdIndexRebuilds(t)
	readDirs, _ := countDirectoryPrimitives(t)

	_, _, err := ResolveScoped(dir, "alpha", "")
	requireScopedNotFound(t, err) // cold: the rebuild is scheduled and held
	writeScript(t, dir, "beta.ts", "1")

	warmed := make(chan error, 1)
	go func() { warmed <- Warm(dir) }()
	select {
	case err := <-warmed:
		t.Fatalf("Warm returned %v while the rebuild it must wait for was still held", err)
	case <-time.After(50 * time.Millisecond):
	}
	assert.Equal(t, 1, held.land(), "the held rebuild lands")
	require.NoError(t, <-warmed)
	assert.Equal(t, 2, *readDirs, "Warm listed again after the in-flight rebuild landed")

	src, _, err := ResolveScoped(dir, "beta", "")
	require.NoError(t, err, "the script written before Warm is in the index Warm returned")
	assert.Equal(t, "1", string(src))
	assert.Equal(t, 0, held.land())
}

// TestStoredNames_UnlistableDirectoryRefusesScopedCallers: a scripts
// directory the process cannot read is refused with the non-disclosing
// unreadable form — no path, no OS error — on the very first request, cold
// or warm, whatever the index holds: the request's own constant-cost open of
// the directory decides it, exactly where the administrator's directory read
// refuses (SC-005). (Answering not-found until a rebuild had recorded the
// error made the refusal shape depend on index state.)
func TestStoredNames_UnlistableDirectoryRefusesScopedCallers(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions are not enforced")
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

	err = Warm(scriptsDir)
	require.Error(t, err, "Warm reports the failure for the server's log")
	assert.True(t, errors.Is(err, fs.ErrPermission))
}
