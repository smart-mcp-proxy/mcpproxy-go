//go:build !darwin && !windows

package codescripts

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Linux and the BSDs resolve names case-sensitively on their native
// filesystems, but a case-folding mount (vfat, an ext4 casefold directory, a
// bind mount from a case-insensitive host) finds `backdoor.JS` for
// `backdoor.js` just as APFS and NTFS do — and unlike those it offers no
// single-entry call that reports how an entry is spelled on disk: F_GETPATH
// does not exist, and a readlink of /proc/self/fd/N echoes the spelling that
// was looked up, not the one stored. The only exact answer is the directory
// listing, and a listing paid on a scoped caller's request is what Spec 105
// FR-012 forbids: its cost grows with the directory, and paying it only when
// a probe hits (codex r5 #1) made the presence of a differently cased entry
// cost O(directory) while absence cost O(1) — a timing oracle on the stored
// names.
//
// So the scoped resolver answers from a stored-name INDEX instead: the exact
// spellings a scripts directory holds, maintained OFF the request path. The
// index is built when the server learns its scripts directory (Warm) and
// rebuilt by a single-flight goroutine whenever a request finds it behind the
// directory's GENERATION (one Lstat of the directory itself). No request ever
// lists: it answers from the index that exists — an exact hit is re-probed by
// the candidate's own Lstat and opened no-follow, so a removed or replaced
// file fails closed; a script added since the listing is refused until the
// rebuild lands, milliseconds later (the administrator's directory read sees
// it at once). Every request — hit, miss or case-variant, cold or warm, in a
// directory of ten thousand entries or none — costs a directory Lstat and an
// O(1) set lookup (codex r6 #1). Listing cost follows the administrator's
// writes, never the requested name, and never lands on a caller's goroutine.
//
// The index answers ONLY for the generation it was built against (codex r7
// #1 / round 8 MUST-FIX). Scheduling a rebuild is not the same as having one:
// earlier rounds handed back whatever the index held while a mismatched
// generation's rebuild was merely scheduled or in flight, and a stale index
// can still vouch for an entry under a spelling the directory no longer has
// it under — on a case-folding mount, a rename lets the stale hit's own
// Lstat fold onto whatever now occupies that name. So a generation mismatch
// refuses exactly as a never-built index refuses, fail closed, until its own
// rebuild lands (a call landing within milliseconds of a directory change is
// refused once — retry). The generation is checked once more after an
// exact-set hit's no-follow open (round 8 MUST-FIX, the lookup→open race):
// gen-before == index.gen == gen-after is what proves the file the open just
// read is the one the index vouched for, not a replacement that landed in
// the window between the probe and the open.
//
// A matching generation is not enough on its own (round 9 MUST-FIX): a
// coarse filesystem timestamp (vfat: two seconds) can leave a directory's
// stamp UNCHANGED across a rename that lands in the same tick as the stamp
// the index was listed against — the index is then "current" by the
// gen-equality test above while still blind to the rename, and the renamed
// entry's own Lstat folds onto it on a case-folding mount exactly as a stale
// index's does. An index may therefore AUTHORIZE a hit only once it is
// SETTLED: its stamp predates the listing by at least generationSettleTime,
// so no write still landing on that stamp could have escaped it. An
// unsettled index — current or not — is refused with the same non-disclosing
// not-found a never-built index gets; the documented consequence is that a
// scoped call is refused for up to ~generationSettleTime after any change to
// the scripts directory (retry). The rebuild-scheduling cadence below is
// unaffected by this: it already runs at most once per settle window while
// unsettled, whether or not this request's own hit is authorized.
//
// A directory that never stops changing cannot be allowed to keep a rebuild
// goroutine re-listing forever, or Warm blocked forever, or a fresh rebuild
// spawning the instant the last one gave up (round 8 SHOULD): one rebuild
// re-lists at most maxRebuildAttempts times, and scheduleRebuildLocked
// withholds a new rebuild goroutine for rebuildBackoff after the previous one
// ends — a request's own cost is unaffected either way, since a stale or
// absent index answers fail-closed at the same one-Lstat cost regardless.

// storedNames is the exact-spelling index of one scripts directory. names is
// replaced, never mutated, so a set handed out under the lock stays valid
// after it is released.
type storedNames struct {
	mu      sync.Mutex
	names   map[string]struct{} // nil until a build has landed, or when it failed
	err     error               // the last build's failure; nil when names is valid
	gen     dirGeneration       // the directory's stamp when names was listed
	settled bool                // gen predates the listing by more than any timestamp tick

	// building is the single-flight flag: at most one rebuild goroutine per
	// directory. landed is closed when that rebuild has finished, so Warm
	// and the tests can wait for it without polling.
	building bool
	landed   chan struct{}

	// refreshAfter bounds how often an UNSETTLED index schedules a refresh:
	// at most once per generationSettleTime, whatever the request rate.
	refreshAfter time.Time

	// nextAttempt bounds how soon a NEW rebuild goroutine may start after
	// the previous one finished (round 8 SHOULD): a continuously changing
	// directory would otherwise let scheduleRebuildLocked spawn another
	// rebuild the instant the last one gives up, listing back to back
	// forever. Set at the end of every rebuild, win or lose; zero means
	// none has ever finished.
	nextAttempt time.Time
}

// storedNameIndexes holds one *storedNames per cleaned scripts directory,
// bounded so it tracks the directories actually in use rather than every
// directory ever used (round 9 SHOULD): the server calls Warm whenever the
// active scripts directory changes, and Warm keeps only the directory it was
// just called for (pruneOtherIndexesLocked) — so in normal operation exactly
// one index is warm. storedNamesIndex additionally caps the map itself at
// maxStoredNameIndexes, evicting the least-recently-used entry, for the bare
// (never-Warmed) case a scoped request alone can produce.
var (
	storedIndexesMu  sync.Mutex
	storedIndexes    = map[string]*storedNames{}
	storedIndexesLRU []string // least-recently-used first; a touched key moves to the end
)

// maxStoredNameIndexes bounds storedIndexes for bare (never-Warmed) use.
const maxStoredNameIndexes = 4

// storedNamesIndex returns the index of one cleaned scripts directory,
// creating an empty (never built) one on first use, and records the access
// for LRU eviction.
func storedNamesIndex(key string) *storedNames {
	storedIndexesMu.Lock()
	defer storedIndexesMu.Unlock()
	idx, ok := storedIndexes[key]
	if !ok {
		idx = &storedNames{}
		storedIndexes[key] = idx
	}
	touchIndexLocked(key)
	evictExcessLocked()
	return idx
}

// touchIndexLocked moves key to the most-recently-used end of the LRU order.
// storedIndexesMu must be held.
func touchIndexLocked(key string) {
	for i, k := range storedIndexesLRU {
		if k == key {
			storedIndexesLRU = append(storedIndexesLRU[:i], storedIndexesLRU[i+1:]...)
			break
		}
	}
	storedIndexesLRU = append(storedIndexesLRU, key)
}

// evictExcessLocked drops the least-recently-used indexes once the map holds
// more than maxStoredNameIndexes. storedIndexesMu must be held.
func evictExcessLocked() {
	for len(storedIndexesLRU) > maxStoredNameIndexes {
		oldest := storedIndexesLRU[0]
		storedIndexesLRU = storedIndexesLRU[1:]
		delete(storedIndexes, oldest)
	}
}

// pruneOtherIndexesLocked drops every index but keep — Warm's own promise
// that only the active scripts directory stays warm. storedIndexesMu must be
// held.
func pruneOtherIndexesLocked(keep string) {
	for k := range storedIndexes {
		if k != keep {
			delete(storedIndexes, k)
		}
	}
	kept := storedIndexesLRU[:0]
	for _, k := range storedIndexesLRU {
		if k == keep {
			kept = append(kept, k)
		}
	}
	storedIndexesLRU = kept
}

// forgetIndex removes one directory's index entirely, forcing the next
// storedNamesIndex(key) to start from a never-built index. Production code
// never calls this directly (pruneOtherIndexesLocked and evictExcessLocked
// cover the two bounding cases); it exists so tests can force a cold index
// without reaching into the map's internals.
func forgetIndex(key string) {
	storedIndexesMu.Lock()
	defer storedIndexesMu.Unlock()
	delete(storedIndexes, key)
	for i, k := range storedIndexesLRU {
		if k == key {
			storedIndexesLRU = append(storedIndexesLRU[:i], storedIndexesLRU[i+1:]...)
			break
		}
	}
}

// forEachIndex calls fn for every currently held index. Production code
// never needs this (each request or Warm call addresses one directory); it
// exists so tests can wait out every rebuild goroutine the suite has left in
// flight, whatever directories they touched.
func forEachIndex(fn func(*storedNames)) {
	storedIndexesMu.Lock()
	idxs := make([]*storedNames, 0, len(storedIndexes))
	for _, idx := range storedIndexes {
		idxs = append(idxs, idx)
	}
	storedIndexesMu.Unlock()
	for _, idx := range idxs {
		fn(idx)
	}
}

// dirGeneration is the Lstat tuple that moves whenever a directory's entry
// set can have changed: adding, removing or renaming an entry updates its
// mtime and ctime (ctime cannot be set from user space, so a restored mtime —
// tar, rsync -a — does not hide a change), a replaced directory has another
// inode, and size is the cheap extra. dev is the device the inode lives on
// (round 9 MUST-FIX): an inode number is unique only WITHIN a device, so
// without it a bind-mount swap to another filesystem whose directory happens
// to collide on inode, size, mtime and ctime would read as the SAME
// generation — the stale index would then vouch for a spelling that was
// never proven on the filesystem now actually mounted there.
// dirGenerationOf reads the tuple per platform.
type dirGeneration struct {
	modTime, changeTime time.Time
	size                int64
	ino                 uint64
	dev                 uint64
}

func (g dirGeneration) equal(o dirGeneration) bool {
	return g.modTime.Equal(o.modTime) && g.changeTime.Equal(o.changeTime) &&
		g.size == o.size && g.ino == o.ino && g.dev == o.dev
}

// latest is the later of the two timestamps.
func (g dirGeneration) latest() time.Time {
	if g.changeTime.After(g.modTime) {
		return g.changeTime
	}
	return g.modTime
}

// generationSettleTime is how far a directory's stamp must predate a listing
// for the index to be trusted until the stamp moves. Timestamps can be coarse
// (vfat: two seconds), so a write landing in the same tick as the recorded
// stamp would leave it unchanged; until the stamp is older than the coarsest
// tick, requests keep scheduling a refresh — at most one per window, and off
// the request path. The bound depends on the clock alone, never on the
// requested name.
const generationSettleTime = 2 * time.Second

// maxRebuildAttempts bounds how many times one rebuild re-lists when the
// directory's generation keeps moving out from under it (round 8 SHOULD): a
// directory that never stops changing must not keep this goroutine listing
// forever, nor block Warm forever. After the bound, whatever the last
// attempt installed stays as the index — the next request finds it stale
// against the directory's CURRENT generation and refuses fail-closed (the
// MUST-FIX rule above), rather than this loop trusting an unconfirmed
// listing or spinning on one that can never confirm.
const maxRebuildAttempts = 3

// rebuildBackoff is the minimum gap between the end of one rebuild goroutine
// and the start of the next for the same directory (round 8 SHOULD). Without
// it, a directory changing on every request would let scheduleRebuildLocked
// spawn a fresh rebuild the instant the bounded one above gives up — the
// same unbounded listing cost, just resumed one goroutine later. During the
// backoff a request's own cost is unchanged: one Lstat, answered fail-closed
// from whatever the index holds (or does not).
const rebuildBackoff = time.Second

// indexClock is time.Now, a variable so the tests can settle an index
// without waiting.
var indexClock = time.Now

// SetIndexClockForTest overrides the clock the settle check reads (round 9
// MUST-FIX) and returns a func that restores it. A directory's ctime cannot
// be forged from user space — it is exactly what makes the settle window a
// real guarantee — so a caller outside this package that needs a freshly
// written scripts directory treated as settled at once (an internal/server
// fixture, say) has no way to fake it by backdating a file; it must move the
// clock the settle check reads instead, as this package's own tests do
// internally. Test-only: production code never calls this, and callers
// outside this package must restore it (defer the returned func, or
// t.Cleanup) before any other test observes the override.
func SetIndexClockForTest(now func() time.Time) (restore func()) {
	prev := indexClock
	indexClock = now
	return func() { indexClock = prev }
}

// spawnIndexRebuild runs one index rebuild on its own goroutine. A variable
// so the tests can hold a rebuild back and prove what a request does on its
// own goroutine, then land it deliberately.
var spawnIndexRebuild = func(rebuild func()) { go rebuild() }

// Warm builds the stored-name index of scriptsDir on the caller's goroutine,
// so the first scoped request finds it ready. The server calls it when it
// learns its scripts directory; it is never called on a request's behalf.
// The listing is taken after Warm was called (a rebuild already in flight is
// waited for, then Warm lists again), so the index reflects the directory as
// it was at the call. A directory that cannot be stat-ed or listed leaves a
// failed index (scoped callers are refused as unreadable until the directory
// changes) and the failure is returned for logging. On darwin and Windows
// there is no index and Warm is a no-op.
//
// Warm also keeps ONLY scriptsDir's index (round 9 SHOULD): the server calls
// Warm whenever the active scripts directory changes, so this is the point
// that knows which directory is current — every other directory's index is
// dropped rather than left to accumulate for as long as the process runs.
func Warm(scriptsDir string) error {
	key := filepath.Clean(scriptsDir)
	idx := storedNamesIndex(key)
	storedIndexesMu.Lock()
	pruneOtherIndexesLocked(key)
	storedIndexesMu.Unlock()
	for {
		idx.mu.Lock()
		if !idx.building {
			idx.beginRebuildLocked()
			idx.mu.Unlock()
			break
		}
		landed := idx.landed
		idx.mu.Unlock()
		<-landed
	}
	// backoffAfter is false: Warm is the server's own explicit request for a
	// current index (at startup, or when the active scripts directory
	// moves), not a request-triggered rebuild guarding against runaway
	// churn — it must not spend part of the round 8 SHOULD backoff a moment
	// after startup refuses the very first real change to the directory.
	idx.rebuild(key, false)
	idx.mu.Lock()
	defer idx.mu.Unlock()
	return idx.err
}

// storedSpellingsOf answers, for one scoped request, whether scriptsDir holds
// an entry spelled exactly `want`: an index hit, confirmed by the candidate's
// own Lstat (the entry may have gone since the listing; the no-follow open
// remains the authoritative check). The index is validated once per request,
// and only an index hit is probed, so an absent name and a differently cased
// one cost the same. A directory that cannot be stat-ed or listed is an error
// the scoped resolver reports as unreadable, as the administrator's directory
// read always has (SC-005).
//
// The second return is a post-open recheck (round 8 MUST-FIX, the
// lookup→open race): the directory's generation as read for THIS lookup,
// wrapped so the caller can re-read it once more after the open and refuse
// if it moved — gen-before == index.gen == gen-after is what proves the file
// the open just read is the one the index vouched for, not a replacement
// that landed in the window between the probe and the open.
func storedSpellingsOf(scriptsDir string) (storedExactly func(want string) (bool, error), verifyUnchanged func(f *os.File, want string) error, err error) {
	names, gen, err := storedNamesFor(scriptsDir)
	if err != nil {
		return nil, nil, err
	}
	storedExactly = func(want string) (bool, error) {
		if _, ok := names[want]; !ok {
			return false, nil
		}
		if _, err := lstat(filepath.Join(scriptsDir, want)); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	}
	// f and want are unused here: the index's own generation recheck (below)
	// is what this platform can prove, and it needs neither the opened
	// descriptor nor the requested spelling — see the darwin/Windows
	// counterpart in storedspellings_probe.go, which proves the spelling
	// itself on f because it has no directory-generation index to recheck.
	verifyUnchanged = func(_ *os.File, _ string) error {
		info, err := lstat(scriptsDir)
		if err != nil {
			return err
		}
		if !dirGenerationOf(info).equal(gen) {
			return errIndexGenerationChanged
		}
		return nil
	}
	return storedExactly, verifyUnchanged, nil
}

// storedNamesFor returns the exact-name set of scriptsDir as the index holds
// it — never listing on the caller's behalf — together with the directory
// generation this request read. One Lstat of the directory reads that
// generation; when it does not equal the index's OWN generation (never
// built, behind, or a rebuild merely scheduled or in flight for it), the
// request is answered as fail-closed as a never-built index: nil names, no
// error, nothing scheduled beyond the rebuild that (still) needs to run.
//
// Round 8 MUST-FIX: earlier rounds scheduled that rebuild but still handed
// back whatever the index held before — a stale index that had listed an
// entry under an EARLIER spelling stayed good enough to authorize it. On a
// case-folding mount that is exploitable: warm the index with `report.js`,
// rename it to `REPORT.JS` (which moves the directory's generation), and
// the stale index's own Lstat of `report.js` still succeeds by folding onto
// the renamed file — a stale index is not evidence about the directory's
// CURRENT contents, whatever it used to be right about. The index now
// answers ONLY for the generation it was built against; any other request
// gets the same non-disclosing not-found a directory it has never seen
// would get, until the rebuild it schedules lands. Documented consequence:
// a scoped call landing within milliseconds of a change to the directory is
// refused once — retry.
func storedNamesFor(scriptsDir string) (names map[string]struct{}, gen dirGeneration, err error) {
	key := filepath.Clean(scriptsDir)
	idx := storedNamesIndex(key)

	info, err := lstat(key)
	if err != nil {
		return nil, dirGeneration{}, err
	}
	// A directory the process may not READ is answered with the unreadable
	// form whatever the index holds — the same reason the administrator's
	// listing gives (SC-005), and the same answer on every platform. Opening
	// the directory (no readdir) is one constant-cost syscall; without it a
	// scripts directory that lost its read bit after the index was built
	// would be reported not-found until a rebuild recorded the error.
	dirFile, err := os.Open(key)
	if err != nil {
		return nil, dirGeneration{}, err
	}
	_ = dirFile.Close()
	gen = dirGenerationOf(info)
	now := indexClock()

	idx.mu.Lock()
	defer idx.mu.Unlock()

	// current is whether the index is BUILT and answers for exactly this
	// generation — the necessary condition for scheduling logic below, which
	// stays exactly as round 8 left it: an out-of-date generation always
	// reschedules, an in-date-but-unsettled one reschedules at most once per
	// window.
	current := (idx.names != nil || idx.err != nil) && idx.gen.equal(gen)

	switch {
	case !current:
		idx.scheduleRebuildLocked(key, now)
	case !idx.settled && !now.Before(idx.refreshAfter):
		idx.scheduleRebuildLocked(key, now)
	}

	// authorized additionally requires the index to be SETTLED (round 9
	// MUST-FIX, doc comment above): a matching-but-unsettled generation is
	// refused exactly as a mismatched one is, because a coarse timestamp
	// cannot rule out a rename that landed on the very stamp being trusted.
	if !current || !idx.settled {
		return nil, dirGeneration{}, nil
	}
	return idx.names, gen, idx.err
}

// scheduleRebuildLocked starts the directory's rebuild goroutine unless one
// is already in flight or the backoff since the last one has not elapsed
// (round 8 SHOULD), and opens the next refresh window either way. During the
// backoff a request's own cost is unaffected — one Lstat, answered
// fail-closed from whatever the index holds (or does not) — only a NEW
// rebuild goroutine is withheld.
func (idx *storedNames) scheduleRebuildLocked(key string, now time.Time) {
	idx.refreshAfter = now.Add(generationSettleTime)
	if idx.building {
		return
	}
	if !idx.nextAttempt.IsZero() && now.Before(idx.nextAttempt) {
		return
	}
	idx.beginRebuildLocked()
	spawnIndexRebuild(func() { idx.rebuild(key, true) })
}

// beginRebuildLocked claims the single-flight slot.
func (idx *storedNames) beginRebuildLocked() {
	idx.building = true
	idx.landed = make(chan struct{})
}

// rebuild lists the directory and installs the result, holding no lock across
// the listing. The stamp is read BEFORE the listing and re-read after it
// under the lock: a write that lands during the listing moves the stamp, and
// the listing is taken again rather than trusted (list-then-stamp race) — up
// to maxRebuildAttempts (round 8 SHOULD): a directory that never stops
// changing cannot keep this goroutine re-listing forever, nor keep Warm
// blocked forever. Giving up leaves whatever the LAST attempt installed;
// that attempt's own generation almost certainly no longer matches the
// directory's current one (it kept moving), so storedNamesFor's own check
// finds the index stale and refuses fail-closed exactly as it would a
// rebuild still in flight — this loop never leaves a lie standing, it
// simply stops asserting anything. Requests that arrive while a rebuild is
// in flight see building set and schedule nothing; their generation read
// precedes this re-check, so the re-check covers whatever they saw. Ends by
// releasing the slot and closing landed; when backoffAfter is set (every
// spawnIndexRebuild-triggered call — Warm's own direct call passes false),
// it also opens the backoff window before another rebuild of this directory
// may start.
func (idx *storedNames) rebuild(key string, backoffAfter bool) {
	for attempt := 1; ; attempt++ {
		gen, err := idx.build(key)
		idx.mu.Lock()
		if err == nil && attempt < maxRebuildAttempts {
			if info, statErr := lstat(key); statErr == nil && !dirGenerationOf(info).equal(gen) {
				idx.mu.Unlock()
				continue
			}
		}
		idx.building = false
		if backoffAfter {
			idx.nextAttempt = indexClock().Add(rebuildBackoff)
		}
		close(idx.landed)
		idx.mu.Unlock()
		return
	}
}

// build takes one listing of key and installs it — or the failure — as the
// index, replacing names atomically under the lock.
func (idx *storedNames) build(key string) (dirGeneration, error) {
	var (
		gen   dirGeneration
		names map[string]struct{}
	)
	info, err := lstat(key)
	now := indexClock()
	if err == nil {
		gen = dirGenerationOf(info)
		var entries []fs.DirEntry
		if entries, err = readDir(key); err == nil {
			names = make(map[string]struct{}, len(entries))
			for _, e := range entries {
				names[e.Name()] = struct{}{}
			}
		}
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.names, idx.err, idx.gen = names, err, gen
	// Settled means no write can still land on this stamp: the tick was over
	// before the listing began, so nothing the listing missed shares it.
	idx.settled = err == nil && now.Sub(gen.latest()) >= generationSettleTime
	return gen, err
}
