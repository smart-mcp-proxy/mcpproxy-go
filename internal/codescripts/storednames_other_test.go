//go:build unix

package codescripts

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// simulateCaseFoldingFstatat makes the package's fstatatEntry seam behave
// like a case-insensitive, case-preserving directory lookup (APFS, NTFS,
// ext4 casefold, vfat): a name that does not exist as spelled resolves to
// the entry whose name matches it case-insensitively. The listing it
// consults is the simulation's own (os.ReadDir directly, on dir, since
// fstatatEntry only receives a bare name relative to an already-open
// descriptor), invisible to the listScopedDir seam. Installed BEFORE
// countScopedDirPrimitives when both are used, so the counters see the
// resolver's own calls and not the simulation's.
func simulateCaseFoldingFstatat(t *testing.T, dir string) {
	t.Helper()
	quiesceIndexRebuilds()
	orig := fstatatEntry
	fstatatEntry = func(dirfd int, name string) error {
		err := orig(dirfd, name)
		if err == nil || !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			return err
		}
		for _, e := range entries {
			if strings.EqualFold(e.Name(), name) {
				return orig(dirfd, e.Name())
			}
		}
		return err
	}
	t.Cleanup(func() {
		quiesceIndexRebuilds()
		fstatatEntry = orig
	})
}

// quiesceIndexRebuilds waits for every rebuild goroutine the tests so far
// have left in flight. The package's seams (the dirfd_other.go primitives,
// listScopedDirOnce, indexClock, spawnIndexRebuild) are process-wide, so a
// helper that installs or restores one must first let any rebuild still
// reading them land.
func quiesceIndexRebuilds() {
	forEachIndex(func(idx *storedNames) {
		idx.mu.Lock()
		building, landed := idx.building, idx.landed
		idx.mu.Unlock()
		if building {
			<-landed
		}
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

// primCounts tallies the round 11 fd-bound primitives (dirfd_other.go) a
// scoped resolution or a rebuild goroutine performs: opens (openScopedDir),
// fstats (fstatDirGeneration — twice for a hit: before the lookup and after
// the open), fstatats (fstatatEntry — the candidate's own probe) and openats
// (openatEntry — the actual read). lists counts listScopedDir, the rebuild
// goroutine's own readdir. These replace the readDir/lstat counters
// countDirectoryPrimitives (codescripts_test.go) still uses for the
// administrator's unchanged, path-based decision.
type primCounts struct {
	opens, fstats, fstatats, openats, lists int
}

// countScopedDirPrimitives routes every round 11 fd-bound primitive through
// counters for the duration of the test, so a test can pin the EXACT
// bounded cost of one request — one open, two fstats, one fstatat, one
// openat for a hit; fewer for a miss, which never probes or opens a
// candidate — whatever the directory holds. Any rebuild still in flight
// lands first so its own calls do not pollute what the counted request
// itself performs.
func countScopedDirPrimitives(t *testing.T) *primCounts {
	t.Helper()
	c := &primCounts{}
	quiesceIndexRebuilds()
	origOpen, origFstat, origFstatat, origOpenat, origList := openScopedDir, fstatDirGeneration, fstatatEntry, openatEntry, listScopedDir
	openScopedDir = func(path string) (int, error) {
		c.opens++
		return origOpen(path)
	}
	fstatDirGeneration = func(fd int) (dirGeneration, error) {
		c.fstats++
		return origFstat(fd)
	}
	fstatatEntry = func(dirfd int, name string) error {
		c.fstatats++
		return origFstatat(dirfd, name)
	}
	openatEntry = func(dirfd int, name string) (*os.File, error) {
		c.openats++
		return origOpenat(dirfd, name)
	}
	listScopedDir = func(dirfd int, path string) ([]string, error) {
		c.lists++
		return origList(dirfd, path)
	}
	t.Cleanup(func() {
		quiesceIndexRebuilds()
		openScopedDir, fstatDirGeneration, fstatatEntry, openatEntry, listScopedDir = origOpen, origFstat, origFstatat, origOpenat, origList
	})
	return c
}

// lookupStoredNamesForTest is storedNamesFor for a self-contained,
// throwaway lookup: it opens the directory, reads its generation, calls
// storedNamesFor, and closes the descriptor itself. Production code never
// needs this — every real request already holds the session
// storedSpellingsOf opened for it — but the package's own tests, which only
// want to inspect what the index currently answers, do.
func lookupStoredNamesForTest(t *testing.T, dir string) map[string]struct{} {
	t.Helper()
	key := filepath.Clean(dir)
	fd, err := openScopedDir(key)
	require.NoError(t, err)
	defer func() { _ = unix.Close(fd) }()
	gen, err := fstatDirGeneration(fd)
	require.NoError(t, err)
	names, err := storedNamesFor(key, fd, gen)
	require.NoError(t, err)
	return names
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
// warm or cold. Since round 11 the index is validated and probed through a
// single retained directory descriptor per request (dirfd_other.go); a
// folded name is refused here purely because it is not a KEY in the index's
// exact-spelling set (built from the real on-disk names), so it never even
// reaches the candidate probe — the fold simulation matters for the
// STALE-index scenario below, where a name that WAS a key must still be
// refused.
func TestResolveScoped_OnAFoldingDirectory(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "backdoor.JS", "({pwned: true})")
	writeScript(t, dir, "exact.js", "({exact: true})")
	simulateCaseFoldingFstatat(t, dir)
	warmStoredNames(t, dir)

	t.Run("a folded spelling is not a stored script, and settling it lists nothing", func(t *testing.T) {
		c := countScopedDirPrimitives(t)
		src, _, err := ResolveScoped(dir, "backdoor", "")
		requireScopedNotFound(t, err)
		assert.NotContains(t, string(src), "pwned")
		assert.Equal(t, 0, c.lists, "a warm index answers the fold without a listing (codex r5 #1)")
		assert.Equal(t, 1, c.opens, "one directory open validates the index")
		assert.Equal(t, 1, c.fstats, "one fstat reads its generation; the candidate itself is never probed")
		assert.Equal(t, 0, c.fstatats, "\"backdoor.js\" is not a key of the index built from the real on-disk name")

		// The administrator's directory read agrees: byte-for-byte, .JS is
		// not an extension of a stored script.
		var notFound *NotFoundError
		_, _, err = Resolve(dir, "backdoor", "")
		require.True(t, errors.As(err, &notFound))
		assert.False(t, notFound.Undisclosed)
	})

	t.Run("an exactly spelled script runs for scoped callers and administrators alike", func(t *testing.T) {
		c := countScopedDirPrimitives(t)
		src, lang, err := ResolveScoped(dir, "exact", "")
		require.NoError(t, err, "a correctly named script must not be refused to an agent token (codex r4 #1)")
		assert.Equal(t, "({exact: true})", string(src))
		assert.Equal(t, LanguageJavaScript, lang)
		assert.Equal(t, 0, c.lists)
		assert.Equal(t, 1, c.opens, "the SINGLE directory descriptor this request opens (round 11 MUST-FIX)")
		assert.Equal(t, 2, c.fstats, "the generation read before the lookup and the post-open recheck after (round 8 MUST-FIX), both on that same descriptor")
		assert.Equal(t, 1, c.fstatats, "the hit's own candidate probe")
		assert.Equal(t, 1, c.openats, "the open, relative to the same descriptor")

		src, lang, err = Resolve(dir, "exact", "")
		require.NoError(t, err)
		assert.Equal(t, "({exact: true})", string(src))
		assert.Equal(t, LanguageJavaScript, lang)
	})

	t.Run("an absent name and a present case-variant cost the same, cold and warm", func(t *testing.T) {
		held := holdIndexRebuilds(t)
		cost := func(name string) (opens, fstats int) {
			forgetIndex(filepath.Clean(dir)) // cold
			c := countScopedDirPrimitives(t)
			_, _, err := ResolveScoped(dir, name, "")
			requireScopedNotFound(t, err)
			assert.Equal(t, 0, c.lists, "%s: a cold request lists nothing itself (codex r6 #1)", name)
			assert.Equal(t, 1, held.land(), "%s: it schedules the one rebuild", name)
			assert.Equal(t, 1, c.lists, "%s: which is the one listing, off the request path", name)
			cOpens, cFstats := c.opens, c.fstats
			_, _, err = ResolveScoped(dir, name, "")
			requireScopedNotFound(t, err)
			assert.Equal(t, 1, c.lists, "%s: the second request finds the index warm", name)
			assert.Equal(t, 0, held.land(), "%s: and schedules nothing", name)
			return c.opens - cOpens, c.fstats - cFstats
		}
		absentOpens, absentFstats := cost("missing")
		variantOpens, variantFstats := cost("backdoor")
		assert.Equal(t, absentOpens, variantOpens, "the open count does not depend on the requested name")
		assert.Equal(t, absentFstats, variantFstats, "nor does the fstat count")
	})

	t.Run("the index holds the stored spelling, so the fold is settled by an exact lookup", func(t *testing.T) {
		names := lookupStoredNamesForTest(t, dir)
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
// `report.js`, and that entry's own probe — simulated through the
// fstatatEntry seam so the fold is exercised on the case-sensitive
// filesystems CI runs on — folds onto the renamed file and succeeds, which
// round 7 trusted as a hit. The fix: the index answers ONLY for the
// generation it was built against, so a request landing while the rebuild
// is merely scheduled is refused exactly like a never-built index, without
// ever probing the candidate the stale index used to hold.
func TestResolveScoped_StaleIndexRefusesARenamedEntry(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "report.js", "({pwned: true})")
	simulateCaseFoldingFstatat(t, dir)
	warmStoredNames(t, dir)
	held := holdIndexRebuilds(t)

	outliveStamp(t, dir)
	before, err := lstat(dir)
	require.NoError(t, err)
	require.NoError(t, os.Rename(filepath.Join(dir, "report.js"), filepath.Join(dir, "REPORT.JS")))
	waitForGenerationChange(t, dir, dirGenerationOf(before))

	c := countScopedDirPrimitives(t)
	src, _, err := ResolveScoped(dir, "report", "")
	requireScopedNotFound(t, err)
	assert.Nil(t, src, "the stale index must never authorize the renamed file, whatever its own probe folds onto")
	assert.Equal(t, 0, c.lists, "the refusal lists nothing (it is fail-closed on the generation mismatch alone)")
	assert.Equal(t, 1, c.opens, "one directory open")
	assert.Equal(t, 1, c.fstats, "one fstat decides staleness; the stale index's candidate is never probed")
	assert.Equal(t, 0, c.fstatats)
	assert.Equal(t, 1, held.land(), "the rename moved the generation: one rebuild is scheduled")
	assert.Equal(t, 1, c.lists, "which is the one listing, off the request path")

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
// candidate's own no-follow stat, but neither that nor a successful
// no-follow open proves the file just opened is the one the index vouched
// for — a write landing between the probe and the open can leave a
// DIFFERENT file occupying the exact name for the descriptor's entire
// lifetime. The directory's generation (round 11: read from the SAME
// retained descriptor the whole request uses) is read once more after the
// open and must still equal the one read before the lookup; a mismatch
// closes the descriptor and refuses. The race is simulated at the seam
// where it actually lands in production: right after openatEntry succeeds
// and before the post-open recheck runs.
func TestResolveScoped_GenerationChangeBetweenLookupAndOpenRefuses(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "alpha.js", "({original: true})")
	warmStoredNames(t, dir)

	origOpenat := openatEntry
	var races int
	t.Cleanup(func() { openatEntry = origOpenat })
	openatEntry = func(dirfd int, name string) (*os.File, error) {
		f, err := origOpenat(dirfd, name)
		if err == nil && name == "alpha.js" && races == 0 {
			races++
			// Races the open: a write lands after the index vouched for the
			// candidate and the open succeeded, but before the descriptor is
			// trusted.
			require.NoError(t, os.Remove(filepath.Join(dir, "alpha.js")))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "alpha.js"), []byte("({swapped: true})"), 0o644))
			// A same-name remove-then-recreate can land on the exact same
			// coarse directory timestamp as the original write (a container
			// filesystem observed to do this even at nanosecond
			// "resolution"): force the generation forward so it is
			// unambiguously the write's, not the clock's granularity, that
			// the recheck must catch.
			require.NoError(t, os.Chtimes(dir, time.Now(), time.Now().Add(time.Second)))
		}
		return f, err
	}

	src, _, err := ResolveScoped(dir, "alpha", "")
	requireScopedNotFound(t, err)
	assert.Nil(t, src, "a file swapped in during the open's own window must never be read, original or swapped content alike")
	assert.Equal(t, 1, races, "the race must actually have run for this to prove anything")
}

// TestResolveScoped_ColdRequestCostIsIndependentOfDirectorySize (codex r6
// #1): the FIRST scoped request against a directory — before any index
// exists — must cost the same for an empty directory and for one holding ten
// thousand scripts. It lists nothing on its own goroutine, performs the same
// bounded number of directory primitives and is refused fail-closed; the
// listing happens when the rebuild lands, and the next request is answered
// from it.
func TestResolveScoped_ColdRequestCostIsIndependentOfDirectorySize(t *testing.T) {
	empty := t.TempDir()
	crowded := t.TempDir()
	for i := 0; i < 10_000; i++ {
		writeScript(t, crowded, fmt.Sprintf("script-%05d.js", i), "1")
	}
	settleStoredNamesClock(t)
	held := holdIndexRebuilds(t)

	probe := func(dir string) (opens, fstats int) {
		forgetIndex(filepath.Clean(dir)) // cold: never warmed
		c := countScopedDirPrimitives(t)
		_, _, err := ResolveScoped(dir, "script-00042", "")
		requireScopedNotFound(t, err)
		assert.Equal(t, 0, c.lists, "%s: a cold request must not list on its own goroutine", dir)
		// Snapshot before landing the rebuild: the rebuild's own listing
		// performs its own open/fstat calls through the SAME counters, which
		// must not be attributed to the request that merely scheduled it.
		opens, fstats = c.opens, c.fstats
		assert.Equal(t, 1, held.land(), "%s: the cold request schedules exactly one rebuild", dir)
		return opens, fstats
	}

	emptyOpens, emptyFstats := probe(empty)
	crowdedOpens, crowdedFstats := probe(crowded)
	assert.Equal(t, emptyOpens, crowdedOpens, "the number of directory opens is independent of the directory's contents")
	assert.Equal(t, emptyFstats, crowdedFstats, "nor does the fstat count depend on it")
	assert.Equal(t, 1, emptyOpens, "the request's own directory open")

	// Landed: the script that was refused a moment ago now runs, with no
	// listing on the request goroutine and none scheduled.
	c := countScopedDirPrimitives(t)
	src, _, err := ResolveScoped(crowded, "script-00042", "")
	require.NoError(t, err, "after the rebuild lands the same request executes")
	assert.Equal(t, "1", string(src))
	assert.Equal(t, 0, c.lists)
	assert.Equal(t, 1, c.opens, "the single retained descriptor (round 11 MUST-FIX)")
	assert.Equal(t, 2, c.fstats, "the generation read before the lookup and the post-open recheck (round 8 MUST-FIX)")
	assert.Equal(t, 1, c.fstatats, "the hit's own candidate probe")
	assert.Equal(t, 1, c.openats)
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
		c := countScopedDirPrimitives(t)

		for i, name := range []string{"alpha", "missing", "ALPHA"} {
			for j := 0; j < 20; j++ {
				_, _, _ = ResolveScoped(dir, name, "")
			}
			assert.Equal(t, 0, c.lists, "%d: an unchanged directory is never listed", i)
			assert.Equal(t, 0, held.land(), "%d: nor is a rebuild scheduled", i)
		}

		outliveStamp(t, dir)
		before, err := lstat(dir)
		require.NoError(t, err)
		writeScript(t, dir, "beta.ts", "1")
		waitForGenerationChange(t, dir, dirGenerationOf(before))

		c.opens, c.fstats = 0, 0
		_, _, err = ResolveScoped(dir, "beta", "")
		requireScopedNotFound(t, err) // fail closed until the rebuild lands
		assert.Equal(t, 0, c.lists, "the request that finds the generation moved lists nothing itself (codex r6 #1)")
		assert.Equal(t, 1, c.opens, "one directory open, no candidate probe")
		assert.Equal(t, 1, held.land(), "it schedules the one rebuild")
		assert.Equal(t, 1, c.lists, "which is the one listing")

		src, lang, err := ResolveScoped(dir, "beta", "")
		require.NoError(t, err, "the script added is found once the rebuild has landed")
		assert.Equal(t, "1", string(src))
		assert.Equal(t, LanguageTypeScript, lang)
		assert.Equal(t, 1, c.lists)
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

		c := countScopedDirPrimitives(t)
		_, _, err = ResolveScoped(dir, "beta", "")
		requireScopedNotFound(t, err)
		waitForIndexRebuild(t, dir)
		assert.Equal(t, 1, c.lists, "the rebuild is the one listing")

		src, _, err := ResolveScoped(dir, "beta", "")
		require.NoError(t, err, "a script added to the directory is callable after the rebuild lands")
		assert.Equal(t, "1", string(src))
		assert.Equal(t, 1, c.lists)

		// The administrator's directory read never waited for anything.
		_, _, err = Resolve(dir, "beta", "")
		require.NoError(t, err)
	})
}

// outliveStamp sleeps until the directory's latest stamp is
// generationSettleTime old, so the next write lands on a later stamp whatever
// the filesystem's timestamp granularity (Linux stamps files with the coarse
// tick clock, so a write in the same tick as the index's listing would not
// move the generation — the guarantee the index itself relies on). Uses the
// package's path-based dirGenerationOf/lstat reader (dirgeneration_ctim.go /
// dirgeneration_ctimespec.go), unchanged by round 11 — a convenience for
// test bookkeeping only, never on the request or rebuild path.
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
// probing the candidate the stale index used to hold.
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
	c := countScopedDirPrimitives(t)
	_, _, err = ResolveScoped(dir, "alpha", "")
	requireScopedNotFound(t, err)
	assert.Equal(t, 0, c.lists, "the refusal lists nothing")
	assert.Equal(t, 1, c.opens, "the directory open alone decides staleness; the stale index's candidate is never probed (round 8 MUST-FIX)")
	assert.Equal(t, 0, c.fstatats)

	assert.Equal(t, 1, held.land(), "the removal moved the generation: one rebuild")
	names := lookupStoredNamesForTest(t, dir)
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
	c := countScopedDirPrimitives(t)

	// requests issues scoped misses and pins each one's own cost: no listing,
	// one directory open — the landed rebuilds' calls are counted between.
	requests := func(label string, names ...string) {
		for i, name := range names {
			lists, opens := c.lists, c.opens
			_, _, err := ResolveScoped(dir, name, "")
			requireScopedNotFound(t, err)
			assert.Equal(t, lists, c.lists, "%s %d: a request never lists", label, i)
			assert.Equal(t, opens+1, c.opens, "%s %d: one directory open per request", label, i)
		}
	}

	// "alpha" is a genuinely stored script — its generation matches the
	// index's the whole time — yet it must be refused exactly like "missing"
	// and "gamma" until the index is SETTLED (round 9 MUST-FIX): a matching
	// generation alone cannot rule out a coarse-timestamp rename that landed
	// on the very stamp being trusted, so an unsettled index authorizes
	// nothing, hit or miss alike.
	requests("inside the window", "alpha", "missing", "gamma", "missing")
	assert.Equal(t, 1, held.land(), "the unsettled index schedules ONE refresh per window, not one per request")
	assert.Equal(t, 1, c.lists)
	requests("still inside", "alpha", "missing", "gamma")
	assert.Equal(t, 0, held.land(), "the window is open until it elapses")

	// The refresh window elapsed but the stamp is still too young: one more.
	indexClock = func() time.Time { return stamp.Add(generationSettleTime/2 + generationSettleTime) }
	requests("next window", "alpha", "missing")
	assert.Equal(t, 1, held.land(), "the next window schedules one more refresh")
	assert.Equal(t, 2, c.lists)
	requests("settled", "missing", "gamma", "missing")
	assert.Equal(t, 0, held.land(), "the listing landed past the stamp's settle time: the index is trusted")
	assert.Equal(t, 2, c.lists)

	// Only now — genuinely settled, not merely gen-matching — does the real
	// hit run (round 9 MUST-FIX).
	src, _, err := ResolveScoped(dir, "alpha", "")
	require.NoError(t, err, "once the index is settled, an exact hit runs")
	assert.Equal(t, "1", string(src))
}

// TestStoredNames_RebuildAttemptsAreBounded (round 8 SHOULD): a directory
// whose generation moves on every observation — as another process
// continuously renaming an entry would leave it — must not keep a rebuild
// goroutine re-listing forever, and must not keep Warm blocked forever
// either. rebuild gives up after maxRebuildAttempts listings whatever the
// directory keeps doing next; what the last attempt installed simply goes
// stale against the directory's true current generation, and the next
// request's own check (the MUST-FIX rule above) refuses it rather than this
// loop spinning to prove something it never can. The churn is simulated at
// fstatDirGeneration — called twice per listing attempt by listScopedDirOnce
// (round 11 MUST-FIX), once before the listing and once after — advancing
// the directory's own mtime on every call so the two never agree within one
// attempt.
func TestStoredNames_RebuildAttemptsAreBounded(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "alpha.js", "1")
	quiesceIndexRebuilds()

	origFstat, origList := fstatDirGeneration, listScopedDir
	var fstats, lists int
	t.Cleanup(func() { fstatDirGeneration, listScopedDir = origFstat, origList })
	fstatDirGeneration = func(fd int) (dirGeneration, error) {
		fstats++
		// Simulate another process continuously changing the directory: its
		// own generation moves on every observation, so the before/after
		// check listScopedDirOnce performs around the listing can never
		// confirm stability.
		require.NoError(t, os.Chtimes(dir, time.Now(), time.Now().Add(time.Duration(fstats)*time.Second)))
		return origFstat(fd)
	}
	listScopedDir = func(dirfd int, path string) ([]string, error) {
		lists++
		return origList(dirfd, path)
	}

	done := make(chan error, 1)
	go func() { done <- Warm(dir) }()
	select {
	case err := <-done:
		require.NoError(t, err, "a continuously changing directory must not fail Warm outright")
	case <-time.After(10 * time.Second):
		t.Fatal("Warm did not return against a continuously changing directory (round 8 SHOULD)")
	}
	assert.Equal(t, maxRebuildAttempts, lists, "one rebuild lists at most maxRebuildAttempts times, however long the directory keeps changing")
}

// TestStoredNames_RebuildBackoffThrottlesReschedules (round 8 SHOULD): once
// a rebuild ends — landing cleanly or giving up after maxRebuildAttempts —
// the next one for the same directory may not start until rebuildBackoff has
// passed, whatever the request rate: without this, a directory that changes
// on every request would let scheduleRebuildLocked spawn a fresh rebuild the
// instant the bounded one above gives up, resuming the same unbounded
// listing cost one goroutine later. A request inside the backoff still
// costs the same bounded directory primitives and answers fail-closed from
// whatever the index holds (or does not); only the new rebuild goroutine is
// withheld.
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

// TestStoredNames_RebuildSlotsBoundConcurrency (round 13 SHOULD, finding 5):
// no more than maxConcurrentRebuilds ASYNC rebuild goroutines may run at
// once, PROCESS-WIDE across every directory's index — a rebuild that cannot
// acquire a slot is SKIPPED outright, not queued behind one, so it never
// blocks the request that scheduled it and never itself piles up waiting.
// holdIndexRebuilds captures each scheduled rebuild's closure instead of
// running it, which — because scheduleRebuildLocked acquires its slot
// SYNCHRONOUSLY before handing the closure to spawnIndexRebuild — holds that
// slot consumed for exactly as long as the closure goes unlanded, without
// needing a real goroutine parked mid-listing.
func TestStoredNames_RebuildSlotsBoundConcurrency(t *testing.T) {
	settleStoredNamesClock(t)
	held := holdIndexRebuilds(t)

	busy := make([]string, maxConcurrentRebuilds)
	for i := range busy {
		dir := t.TempDir()
		writeScript(t, dir, "alpha.js", "1")
		busy[i] = dir
		_, _, err := ResolveScoped(dir, "alpha", "")
		requireScopedNotFound(t, err)
	}

	third := t.TempDir()
	writeScript(t, third, "alpha.js", "1")
	_, _, err := ResolveScoped(third, "alpha", "")
	requireScopedNotFound(t, err)

	thirdIdx := storedNamesIndex(filepath.Clean(third))
	thirdIdx.mu.Lock()
	building := thirdIdx.building
	thirdIdx.mu.Unlock()
	assert.False(t, building, "every rebuild slot is already held by another directory: this rebuild must be skipped, not queued")

	assert.Equal(t, maxConcurrentRebuilds, held.land(),
		"exactly the directories that could acquire a slot were scheduled — the third was skipped, not merely deferred")
	for _, dir := range busy {
		waitForIndexRebuild(t, dir)
	}

	// A slot is free again: the third directory's own next request finally
	// schedules its rebuild, and this time it can complete.
	_, _, err = ResolveScoped(third, "alpha", "")
	requireScopedNotFound(t, err) // the index has not landed yet, so this request still answers fail-closed
	assert.Equal(t, 1, held.land(), "the previously-skipped rebuild is scheduled now that a slot is free")
	waitForIndexRebuild(t, third)

	names := lookupStoredNamesForTest(t, third)
	assert.Contains(t, names, "alpha.js", "the rebuild that finally acquired a slot lands normally")
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
	c := countScopedDirPrimitives(t)

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
	assert.Equal(t, 2, c.lists, "Warm listed again after the in-flight rebuild landed")

	src, _, err := ResolveScoped(dir, "beta", "")
	require.NoError(t, err, "the script written before Warm is in the index Warm returned")
	assert.Equal(t, "1", string(src))
	assert.Equal(t, 0, held.land())
}

// TestStoredNames_WarmBlocksOnRebuildSlots (round 17 SHOULD, finding 1):
// Warm's own synchronous, population-sized listing must count against the
// SAME process-wide rebuildSlots bound the async path enforces
// (rebuildsemaphore.go) — otherwise two concurrent Warm calls for different
// directories (e.g. two active-config-path moves, each spawning its own
// async warmStoredScripts goroutine per mcp_code_execution.go) could run an
// unbounded number of listings alongside the async rebuilds the semaphore is
// meant to cap. The semaphore's capacity is temporarily reduced to 1 (a
// seam: rebuildSlots is swapped for the test and restored on cleanup) so a
// single held listing is enough to prove the second Warm call BLOCKS on the
// slot rather than racing ahead unbounded, and unblocks once the first
// Warm's slot is released — never deadlocking, since Warm never holds
// idx.mu while blocked acquiring the slot (see Warm's own comment).
func TestStoredNames_WarmBlocksOnRebuildSlots(t *testing.T) {
	quiesceIndexRebuilds()
	settleStoredNamesClock(t)
	origSlots := rebuildSlots
	rebuildSlots = make(chan struct{}, 1)
	t.Cleanup(func() { rebuildSlots = origSlots })

	dirA := t.TempDir()
	writeScript(t, dirA, "alpha.js", "1")
	dirB := t.TempDir()
	writeScript(t, dirB, "beta.js", "1")

	// Gate dirA's listing so the test controls exactly when its rebuild —
	// and with it, the sole rebuildSlots slot — completes.
	gate := make(chan struct{})
	entered := make(chan struct{}, 1)
	origList := listScopedDirOnce
	t.Cleanup(func() { listScopedDirOnce = origList })
	listScopedDirOnce = func(key string) (dirGeneration, dirGeneration, map[string]struct{}, error) {
		if key == filepath.Clean(dirA) {
			entered <- struct{}{}
			<-gate
		}
		return origList(key)
	}

	doneA := make(chan error, 1)
	go func() { doneA <- Warm(dirA) }()
	<-entered // dirA now holds the sole rebuildSlots slot, blocked mid-listing

	doneB := make(chan error, 1)
	go func() { doneB <- Warm(dirB) }()

	// dirB's Warm claims its OWN index's building flag immediately (it does
	// not contend with dirA on idx.mu — different indexes) but must block
	// acquiring the shared slot, so it must not return yet.
	select {
	case err := <-doneB:
		t.Fatalf("Warm(dirB) returned (err=%v) while the sole rebuildSlots slot was held by dirA's in-flight rebuild — the semaphore did not bound it", err)
	case <-time.After(100 * time.Millisecond):
	}
	idxB := storedNamesIndex(filepath.Clean(dirB))
	idxB.mu.Lock()
	buildingB := idxB.building
	idxB.mu.Unlock()
	assert.True(t, buildingB, "dirB's Warm has claimed its own index's building flag while waiting on the slot")

	close(gate) // release dirA's listing; its slot frees once its Warm returns
	require.NoError(t, <-doneA)
	require.NoError(t, <-doneB, "dirB's Warm proceeds once dirA's slot is released")

	namesB := lookupStoredNamesForTest(t, dirB)
	assert.Contains(t, namesB, "beta.js", "dirB's rebuild ran and landed once it finally acquired the slot")
}

// TestStoredNames_UnlistableDirectoryRefusesScopedCallers: a scripts
// directory the process cannot read is refused with the non-disclosing
// unreadable form — no path, no OS error — on the very first request, cold
// or warm, whatever the index holds: the request's own constant-cost open of
// the directory decides it, exactly where the administrator's directory read
// refuses (SC-005).
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

// TestDirGeneration_DeviceIsPartOfIdentity (round 9 MUST-FIX): an inode
// number is unique only WITHIN its device, so two directories on different
// devices can legitimately share an inode, size and both timestamps — a
// bind-mount swap from one filesystem to another is exactly this scenario.
// Without the device in the tuple such a swap would read as the SAME
// generation, letting a stale index vouch for a spelling never proven on the
// filesystem now actually mounted there. Pinned at the generation seam
// (dirGeneration.equal) rather than a real bind mount, which CI cannot set
// up portably.
func TestDirGeneration_DeviceIsPartOfIdentity(t *testing.T) {
	shared := dirGeneration{modTime: time.Unix(1, 0), changeTime: time.Unix(1, 0), size: 4096, ino: 42}
	onDeviceA := shared
	onDeviceA.dev = 1
	onDeviceB := shared
	onDeviceB.dev = 2

	assert.False(t, onDeviceA.equal(onDeviceB),
		"the same inode/size/timestamps on a different device must not compare equal")
	assert.True(t, onDeviceA.equal(onDeviceA), "a generation always equals itself")
}

// TestDirGenerationOf_ReadsTheDevice pins that the path-based platform
// reader actually populates dev from a real Lstat, not just that equal()
// considers it. TestDirFdGeneration_ReadsTheDevice below pins the same for
// the fd-based reader round 11 introduced.
func TestDirGenerationOf_ReadsTheDevice(t *testing.T) {
	dir := t.TempDir()
	info, err := lstat(dir)
	require.NoError(t, err)
	gen := dirGenerationOf(info)
	assert.NotZero(t, gen.dev, "a real directory's device must be read, not left at the zero value")
}

// TestDirFdGeneration_ReadsTheDevice (round 11 MUST-FIX): the fd-based
// generation reader every scoped request and rebuild actually uses
// (dirfd_other.go) must populate dev/ino identically to the path-based
// reader the tests and the administrator's bookkeeping use — both describe
// the SAME real directory here, so they must agree exactly.
func TestDirFdGeneration_ReadsTheDevice(t *testing.T) {
	dir := t.TempDir()
	info, err := lstat(dir)
	require.NoError(t, err)
	fromPath := dirGenerationOf(info)

	fd, err := defaultOpenScopedDir(dir)
	require.NoError(t, err)
	defer func() { _ = unix.Close(fd) }()
	fromFd, err := defaultFstatDirGeneration(fd)
	require.NoError(t, err)

	assert.NotZero(t, fromFd.dev)
	assert.Equal(t, fromPath.dev, fromFd.dev, "the same real directory's device must read the same whether reached by path or by descriptor")
	assert.Equal(t, fromPath.ino, fromFd.ino)
}

// TestStoredNamesFor_IdentityMismatchIsAMiss (round 11 MUST-FIX): even when
// a request's own directory descriptor happens to agree with the index's
// recorded generation on every OTHER field, a different device (the
// bind-mount-swap scenario round 9's dirGeneration.equal already refuses at
// the field-comparison level) must never authorize a hit, exercised here
// through the actual lookup function every request calls rather than only
// at the struct-equality level.
func TestStoredNamesFor_IdentityMismatchIsAMiss(t *testing.T) {
	held := holdIndexRebuilds(t)
	key := "codescripts-test-identity-mismatch-dir-does-not-exist"
	forgetIndex(key)
	t.Cleanup(func() { forgetIndex(key) })

	idx := storedNamesIndex(key)
	idx.mu.Lock()
	idx.names = map[string]struct{}{"report.js": {}}
	idx.gen = dirGeneration{modTime: time.Unix(1, 0), changeTime: time.Unix(1, 0), size: 4096, ino: 42, dev: 1}
	idx.settled = true
	idx.mu.Unlock()

	sameButDifferentDevice := dirGeneration{modTime: time.Unix(1, 0), changeTime: time.Unix(1, 0), size: 4096, ino: 42, dev: 2}
	names, err := storedNamesFor(key, -1, sameButDifferentDevice)
	require.NoError(t, err)
	assert.Nil(t, names, "an index built for one directory must never answer for a request whose descriptor resolved to a different one")
	held.land() // the mismatch schedules a (harmless, doomed-to-fail) rebuild of the bogus key; land it so nothing is left in flight
}

// TestResolveScoped_DirectoryPathABA (round 11 MUST-FIX, the directory-path
// ABA hole): every earlier round's scoped resolution re-resolved scriptsDir
// BY PATH at each step — reading the generation, probing a candidate,
// opening it, and rechecking the generation were four independent lookups
// of the same path, each of which a replaceable symlink, ancestor
// directory, or bind mount retargeted between two of them could answer
// differently. The fix binds the whole request to the ONE descriptor
// storedSpellingsOf opens: everything the returned closures still do — the
// candidate probe already ran before the retarget below, the open, and the
// post-open recheck — must be UNAFFECTED by retargeting the path after that
// call returns, because none of them ever resolve scriptsDir again. A real
// symlink retarget between the session's own open and the caller's
// subsequent calls to open()/verifyUnchanged() is exactly the window a
// naive (path-re-resolving) implementation would lose to, and exactly the
// window production code — resolve(), in codescripts.go — leaves between
// calling candidates() and later calling the open and verify closures it
// returned.
func TestResolveScoped_DirectoryPathABA(t *testing.T) {
	base := t.TempDir()
	dirA := filepath.Join(base, "a")
	dirB := filepath.Join(base, "b")
	require.NoError(t, os.Mkdir(dirA, 0o755))
	require.NoError(t, os.Mkdir(dirB, 0o755))
	writeScript(t, dirA, "report.js", "FROM-A")
	writeScript(t, dirB, "report.js", "FROM-B")

	link := filepath.Join(base, "scripts")
	require.NoError(t, os.Symlink(dirA, link))
	warmStoredNames(t, link)

	storedExactly, open, verifyUnchanged, closeSession, err := storedSpellingsOf(link)
	require.NoError(t, err)
	require.NotNil(t, open, "Linux/BSD always binds the open to the retained descriptor (round 11 MUST-FIX)")
	require.NotNil(t, verifyUnchanged)
	if closeSession != nil {
		defer closeSession()
	}

	ok, err := storedExactly("report.js")
	require.NoError(t, err)
	require.True(t, ok)

	// The window the fix closes: retarget the symlink AFTER the session
	// above already resolved it (dirA), before this request's remaining
	// steps run — exactly the gap between resolve() calling candidates()
	// and resolve() later calling the open and verify closures it got back.
	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Symlink(dirB, link))

	f, err := open(filepath.Join(link, "report.js"))
	require.NoError(t, err, "the open must succeed against the descriptor this request originally resolved")
	defer f.Close()
	data, err := io.ReadAll(f)
	require.NoError(t, err)
	assert.Equal(t, "FROM-A", string(data), "the open must read the directory this request originally resolved, never the retargeted one")

	assert.NoError(t, verifyUnchanged(f, "report.js"), "the retarget must not be visible to the post-open recheck either: it reads the SAME descriptor's generation, unaffected by what the path now points at")
}

// TestStoredNames_EvictionCancelsAnInFlightRebuild (round 11 SHOULD,
// cancellable rebuilds): a rebuild goroutine still mid-listing when its
// index is evicted (LRU) or pruned (Warm keeping only the active directory)
// must stop promptly rather than keep listing for a directory nobody will
// query through it any longer, and must install NOTHING — there is no
// reader left it could still be wrong for. The goroutine is parked inside
// listScopedDirOnce (via the openScopedDir seam) so the eviction genuinely
// races an in-flight rebuild rather than one that already finished; wg is
// the seam that proves the goroutine actually stopped, not merely that
// cancel was called.
func TestStoredNames_EvictionCancelsAnInFlightRebuild(t *testing.T) {
	quiesceIndexRebuilds()
	dir := t.TempDir()
	writeScript(t, dir, "alpha.js", "1")

	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	origOpen := openScopedDir
	t.Cleanup(func() { openScopedDir = origOpen })
	openScopedDir = func(path string) (int, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return origOpen(path)
	}

	key := filepath.Clean(dir)
	idx := storedNamesIndex(key)
	idx.mu.Lock()
	idx.beginRebuildLocked()
	idx.mu.Unlock()
	idx.wg.Add(1)
	go idx.rebuild(key, true, true)

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the rebuild goroutine never reached the directory-open seam")
	}

	// Evict it exactly as LRU eviction / Warm's own pruning of every other
	// directory would: cancel, then drop from the map.
	forgetIndex(key)

	close(release) // let the blocked open proceed; the listing itself succeeds

	done := make(chan struct{})
	go func() { idx.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the rebuild goroutine did not stop after its index was evicted")
	}

	idx.mu.Lock()
	names, buildErr, building := idx.names, idx.err, idx.building
	idx.mu.Unlock()
	assert.Nil(t, names, "a cancelled rebuild installs nothing")
	assert.NoError(t, buildErr, "nor does it install a failure")
	assert.False(t, building, "the single-flight slot is released so a future request can rebuild")
}

// TestStoredNames_WarmKeepsOnlyTheActiveDirectory (round 9 SHOULD): the
// server calls Warm whenever the active scripts directory changes, so Warm
// itself is where "only the active directory is warm" can be enforced —
// switching the active config path N times must leave exactly one index,
// not one per directory the process has ever served.
func TestStoredNames_WarmKeepsOnlyTheActiveDirectory(t *testing.T) {
	quiesceIndexRebuilds()
	settleStoredNamesClock(t)

	const n = 5
	dirs := make([]string, n)
	for i := range dirs {
		dirs[i] = t.TempDir()
		writeScript(t, dirs[i], "alpha.js", "1")
	}

	for _, d := range dirs {
		require.NoError(t, Warm(d))
	}

	storedIndexesMu.Lock()
	count := len(storedIndexes)
	_, activeIsWarm := storedIndexes[filepath.Clean(dirs[n-1])]
	storedIndexesMu.Unlock()

	assert.Equal(t, 1, count, "switching the active config path %d times must leave one index, not %d", n, n)
	assert.True(t, activeIsWarm, "the index left behind must be the one Warm was last called for")

	// The still-active directory keeps answering; the abandoned ones are
	// simply cold again (fail-closed until something warms or requests them
	// afresh) rather than lost or corrupted.
	src, _, err := ResolveScoped(dirs[n-1], "alpha", "")
	require.NoError(t, err)
	assert.Equal(t, "1", string(src))
}

// TestStoredNames_BareUseCapsAtLeastRecentlyUsed (round 9 SHOULD): a caller
// that never calls Warm (a scoped request against a directory the server
// never warmed) still must not grow storedIndexes without bound — the map
// caps at maxStoredNameIndexes, evicting the least-recently-used directory.
func TestStoredNames_BareUseCapsAtLeastRecentlyUsed(t *testing.T) {
	quiesceIndexRebuilds()
	storedIndexesMu.Lock()
	storedIndexes = map[string]*storedNames{}
	storedIndexesLRU = nil
	storedIndexesMu.Unlock()

	keys := make([]string, maxStoredNameIndexes+3)
	for i := range keys {
		keys[i] = fmt.Sprintf("bare-use-dir-%d", i)
		storedNamesIndex(keys[i])
	}

	storedIndexesMu.Lock()
	count := len(storedIndexes)
	_, oldestSurvived := storedIndexes[keys[0]]
	_, newestSurvived := storedIndexes[keys[len(keys)-1]]
	storedIndexesMu.Unlock()

	assert.Equal(t, maxStoredNameIndexes, count, "bare use is capped at maxStoredNameIndexes")
	assert.False(t, oldestSurvived, "the least-recently-used directory is evicted first")
	assert.True(t, newestSurvived, "the most recently touched directory survives")
}
