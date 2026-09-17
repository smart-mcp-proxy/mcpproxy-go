//go:build !darwin && !windows

package codescripts

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
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
// directory's GENERATION. No request ever lists: it answers from the index
// that exists — an exact hit is re-probed by the candidate's own no-follow
// stat and opened no-follow, so a removed or replaced file fails closed; a
// script added since the listing is refused until the rebuild lands,
// milliseconds later (the administrator's directory read sees it at once).
// Every request — hit, miss or case-variant, cold or warm, in a directory of
// ten thousand entries or none — costs the same bounded number of directory
// primitives and an O(1) set lookup. Listing cost follows the
// administrator's writes, never the requested name, and never lands on a
// caller's goroutine.
//
// The index answers ONLY for the generation it was built against (round 8
// MUST-FIX): a stale index is refused exactly as a never-built one is, fail
// closed, until its own rebuild lands (a call landing within milliseconds of
// a directory change is refused once — retry). The generation is checked
// once more after an exact-set hit's no-follow open (round 8 MUST-FIX, the
// lookup→open race): gen-before == index.gen == gen-after is what proves the
// file the open just read is the one the index vouched for.
//
// A matching generation is not enough on its own (round 9 MUST-FIX): a
// coarse filesystem timestamp (vfat: two seconds) can leave a directory's
// stamp UNCHANGED across a rename that lands in the same tick as the stamp
// the index was listed against. An index may therefore AUTHORIZE a hit only
// once it is SETTLED: its stamp predates the listing by at least
// generationSettleTime, so no write still landing on that stamp could have
// escaped it.
//
// Round 11 MUST-FIX (the directory-path ABA hole): every check above —
// gen-before, the candidate probe, gen-after — and the eventual open used to
// be FOUR INDEPENDENT resolutions of scriptsDir BY PATH. A replaceable
// symlink, ancestor directory, or bind mount retargeted between two of those
// steps and back before the next one let each step separately agree with a
// DIFFERENT directory than the one the others saw — st_dev (round 9) rules
// out a substitution visible during one snapshot, never an alternation
// across several. The fix (dirfd_other.go) binds the entire request to ONE
// retained directory descriptor: opened by path exactly once, then every
// generation read, the candidate probe, and the open itself are all
// performed RELATIVE TO THAT DESCRIPTOR (fstat / fstatat / openat) — no
// second path resolution exists for anything to retarget. See
// dirfd_other.go for the full account and storedSpellingsOf below for where
// the descriptor is opened and released.
//
// Round 11 SHOULD (cancellable rebuilds): a directory that keeps changing
// must not leave orphaned rebuild goroutines running forever after their
// index has been evicted (LRU) or pruned (Warm keeping only the active
// directory) — each index owns a context that eviction cancels, and its
// rebuild goroutine (the ASYNC, request-scheduled kind only — Warm's own
// synchronous rebuild is what the caller is waiting on and always runs to
// completion) checks it between listing attempts and once more before
// installing a result, so a cancelled rebuild stops promptly and writes
// nothing nobody will read. See storedNames.rebuild below.

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
	// forever. Set at the end of every ASYNC rebuild, win or lose; zero
	// means none has ever finished.
	nextAttempt time.Time

	// ctx/cancel bind this index's ASYNC rebuild goroutines to the index's
	// own lifetime (round 11 SHOULD): every place that discards this index
	// — LRU eviction, Warm pruning every OTHER directory, forgetIndex —
	// cancels ctx before the map forgets it, so a rebuild goroutine still
	// mid-listing for a directory nobody will query through THIS index any
	// longer stops re-listing and installs nothing rather than racing the
	// eviction to finish a write no reader needed. wg is the seam a test (or
	// a future caller) waits on to know the goroutine has actually
	// returned, not merely that cancel was called; every rebuild call — the
	// async ones AND Warm's own synchronous one — is wg.Add(1)'d before it
	// starts, so wg.Wait() always reflects work truly in flight.
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// storedIndexes holds one *storedNames per cleaned scripts directory,
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
// creating an empty (never built) one — with its own cancellation context —
// on first use, and records the access for LRU eviction.
func storedNamesIndex(key string) *storedNames {
	storedIndexesMu.Lock()
	defer storedIndexesMu.Unlock()
	idx, ok := storedIndexes[key]
	if !ok {
		ctx, cancel := context.WithCancel(context.Background())
		idx = &storedNames{ctx: ctx, cancel: cancel}
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
// more than maxStoredNameIndexes, cancelling each one's rebuild context
// first (round 11 SHOULD) so an in-flight async rebuild for a directory this
// map no longer tracks does not keep listing. storedIndexesMu must be held.
func evictExcessLocked() {
	for len(storedIndexesLRU) > maxStoredNameIndexes {
		oldest := storedIndexesLRU[0]
		storedIndexesLRU = storedIndexesLRU[1:]
		if idx, ok := storedIndexes[oldest]; ok {
			idx.cancel()
		}
		delete(storedIndexes, oldest)
	}
}

// pruneOtherIndexesLocked drops every index but keep — Warm's own promise
// that only the active scripts directory stays warm — cancelling each
// dropped index's rebuild context first (round 11 SHOULD). storedIndexesMu
// must be held.
func pruneOtherIndexesLocked(keep string) {
	for k, idx := range storedIndexes {
		if k != keep {
			idx.cancel()
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

// forgetIndex removes one directory's index entirely, cancelling its
// rebuild context first (round 11 SHOULD), forcing the next
// storedNamesIndex(key) to start from a fresh, never-built index. Production
// code never calls this directly (pruneOtherIndexesLocked and
// evictExcessLocked cover the two bounding cases); it exists so tests can
// force a cold index without reaching into the map's internals.
func forgetIndex(key string) {
	storedIndexesMu.Lock()
	defer storedIndexesMu.Unlock()
	if idx, ok := storedIndexes[key]; ok {
		idx.cancel()
	}
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

// dirGeneration is the stat tuple that moves whenever a directory's entry
// set can have changed: adding, removing or renaming an entry updates its
// mtime and ctime (ctime cannot be set from user space, so a restored mtime —
// tar, rsync -a — does not hide a change), a replaced directory has another
// inode, and size is the cheap extra. dev is the device the inode lives on
// (round 9 MUST-FIX): an inode number is unique only WITHIN a device, so
// without it a bind-mount swap to another filesystem whose directory happens
// to collide on inode, size, mtime and ctime would read as the SAME
// generation. dirGenerationOf reads the tuple from a path-based Lstat result
// (used by the package's tests and by the pre-round-11 callers that still
// have only a path, never a descriptor); dirFdGeneration in dirfd_other.go
// reads the identical tuple from an already-open descriptor via fstat — the
// form every request and rebuild actually uses (round 11 MUST-FIX).
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

// rebuildBackoff is the minimum gap between the end of one ASYNC rebuild
// goroutine and the start of the next for the same directory (round 8
// SHOULD). Without it, a directory changing on every request would let
// scheduleRebuildLocked spawn a fresh rebuild the instant the bounded one
// above gives up. During the backoff a request's own cost is unchanged: one
// open and one fstat, answered fail-closed from whatever the index holds (or
// does not).
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

// spawnIndexRebuild runs one ASYNC index rebuild on its own goroutine. A
// variable so the tests can hold a rebuild back and prove what a request
// does on its own goroutine, then land it deliberately.
var spawnIndexRebuild = func(rebuild func()) { go rebuild() }

// Warm builds the stored-name index of scriptsDir on the caller's goroutine,
// so the first scoped request finds it ready. The server calls it when it
// learns its scripts directory; it is never called on a request's behalf.
// The listing is taken after Warm was called (a rebuild already in flight is
// waited for, then Warm lists again), so the index reflects the directory as
// it was at the call. A directory that cannot be opened or listed leaves a
// failed index (scoped callers are refused as unreadable until the directory
// changes) and the failure is returned for logging. On darwin and Windows
// there is no index and Warm is a no-op.
//
// Warm also keeps ONLY scriptsDir's index (round 9 SHOULD): the server calls
// Warm whenever the active scripts directory changes, so this is the point
// that knows which directory is current — every other directory's index is
// dropped (its rebuild context cancelled, round 11 SHOULD) rather than left
// to accumulate for as long as the process runs.
//
// Warm's own rebuild is never cancelled by that pruning (round 11 SHOULD):
// cancellation exists to stop an ASYNC rebuild nobody is waiting for from
// outliving the index that scheduled it, not to let a concurrent caller's
// eviction of a DIFFERENT directory silently turn this synchronous call —
// which the caller is blocked on and whose error it trusts — into a no-op.
// scriptsDir's own index is never among the ones pruneOtherIndexesLocked
// drops here, so this is only a concern for a hypothetical concurrent Warm
// of a different directory; the rebuild call below simply does not consult
// ctx, so it always runs to completion and its result is always installed.
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
	// cancellable is false for the reason in the doc comment above.
	idx.wg.Add(1)
	idx.rebuild(key, false, false)
	idx.mu.Lock()
	defer idx.mu.Unlock()
	return idx.err
}

// storedSpellingsOf answers, for one scoped request, whether scriptsDir holds
// an entry spelled exactly `want`, and hands back how to open and re-verify
// the winning candidate — all bound to the SINGLE directory descriptor this
// call opens (round 11 MUST-FIX; see dirfd_other.go and the package doc
// comment above). The index is validated once per request against that
// descriptor's own generation, and only an index hit is probed, so an
// absent name and a differently cased one cost the same. A directory that
// cannot be opened or listed is an error the scoped resolver reports as
// unreadable, as the administrator's directory read always has (SC-005).
//
// The returned open func opens the winning candidate relative to the same
// descriptor (openatEntry) rather than a fresh resolution of the path — the
// core of the round 11 MUST-FIX. verifyUnchanged is the post-open recheck
// (round 8 MUST-FIX, the lookup→open race): the SAME descriptor's
// generation, read once more after the open; gen-before == index.gen ==
// gen-after is what proves the file the open just read is the one the index
// vouched for. closeSession releases the descriptor once the caller is done
// with it, whether or not a candidate was ever opened — callers must call it
// exactly once (resolve, in codescripts.go, defers it immediately).
func storedSpellingsOf(scriptsDir string) (storedExactly func(want string) (bool, error), open func(path string) (*os.File, error), verifyUnchanged func(f *os.File, want string) error, closeSession func(), err error) {
	key := filepath.Clean(scriptsDir)

	dirfd, err := openScopedDir(key)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	closeSession = func() { _ = unix.Close(dirfd) }

	gen, err := fstatDirGeneration(dirfd)
	if err != nil {
		closeSession()
		return nil, nil, nil, nil, err
	}

	names, lookupErr := storedNamesFor(key, dirfd, gen)
	if lookupErr != nil {
		closeSession()
		return nil, nil, nil, nil, lookupErr
	}

	storedExactly = func(want string) (bool, error) {
		if _, ok := names[want]; !ok {
			return false, nil
		}
		if err := fstatatEntry(dirfd, want); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	}
	open = func(path string) (*os.File, error) {
		return openatEntry(dirfd, filepath.Base(path))
	}
	// f and want are unused here: the SAME descriptor's own generation
	// recheck (below) is what this platform can prove, and it needs
	// neither the opened file nor the requested spelling — see the
	// darwin/Windows counterpart in storedspellings_probe.go and
	// storedspellings_probe_windows.go, which prove the spelling itself on
	// f because they have no directory-generation index to recheck.
	verifyUnchanged = func(_ *os.File, _ string) error {
		cur, err := fstatDirGeneration(dirfd)
		if err != nil {
			return err
		}
		if !cur.equal(gen) {
			return errIndexGenerationChanged
		}
		return nil
	}
	return storedExactly, open, verifyUnchanged, closeSession, nil
}

// storedNamesFor returns the exact-name set of scriptsDir as the index holds
// it for THIS request's already-open descriptor and its freshly read
// generation (round 11 MUST-FIX: dirfd and gen both come from the SAME open,
// never a path lookup of their own) — never listing on the caller's behalf.
// When dirfd's generation does not equal the index's OWN generation (never
// built, behind, or a rebuild merely scheduled or in flight for it) — or the
// index is not yet SETTLED (round 9 MUST-FIX) — the request is answered as
// fail-closed as a never-built index: nil names, no error, nothing scheduled
// beyond the rebuild that (still) needs to run.
//
// Because dirfd's generation already carries the directory's device and
// inode (dirGeneration, round 9 MUST-FIX), this is also what refuses a
// request whose descriptor resolves to a DIFFERENT directory than the one
// the index was built from, even should every other field of the stamp
// happen to collide: idx.gen.equal(gen) requires the identical dev+ino, so
// an index built from directory A never authorizes a request whose dirfd
// opened directory B.
func storedNamesFor(key string, dirfd int, gen dirGeneration) (names map[string]struct{}, err error) {
	idx := storedNamesIndex(key)
	now := indexClock()

	idx.mu.Lock()
	defer idx.mu.Unlock()

	// current is whether the index is BUILT and answers for exactly this
	// generation — the necessary condition for scheduling logic below: an
	// out-of-date generation always reschedules, an in-date-but-unsettled
	// one reschedules at most once per window.
	current := (idx.names != nil || idx.err != nil) && idx.gen.equal(gen)

	switch {
	case !current:
		idx.scheduleRebuildLocked(key, now)
	case !idx.settled && !now.Before(idx.refreshAfter):
		idx.scheduleRebuildLocked(key, now)
	}

	// authorized additionally requires the index to be SETTLED (round 9
	// MUST-FIX): a matching-but-unsettled generation is refused exactly as
	// a mismatched one is, because a coarse timestamp cannot rule out a
	// rename that landed on the very stamp being trusted.
	if !current || !idx.settled {
		return nil, nil
	}
	return idx.names, idx.err
}

// scheduleRebuildLocked starts the directory's ASYNC rebuild goroutine
// unless one is already in flight or the backoff since the last one has not
// elapsed (round 8 SHOULD), and opens the next refresh window either way.
// During the backoff a request's own cost is unaffected — one open, one
// fstat, answered fail-closed from whatever the index holds (or does not) —
// only a NEW rebuild goroutine is withheld.
func (idx *storedNames) scheduleRebuildLocked(key string, now time.Time) {
	idx.refreshAfter = now.Add(generationSettleTime)
	if idx.building {
		return
	}
	if !idx.nextAttempt.IsZero() && now.Before(idx.nextAttempt) {
		return
	}
	idx.beginRebuildLocked()
	// wg.Add happens before spawnIndexRebuild hands the closure off (which
	// may run it synchronously, in a test that holds rebuilds back) so
	// idx.wg.Wait() is never called before the matching Add is visible.
	idx.wg.Add(1)
	spawnIndexRebuild(func() { idx.rebuild(key, true, true) })
}

// beginRebuildLocked claims the single-flight slot.
func (idx *storedNames) beginRebuildLocked() {
	idx.building = true
	idx.landed = make(chan struct{})
}

// rebuild lists the directory (through dirfd_other.go's fd-bound primitives:
// round 11 MUST-FIX) and installs the result, holding no lock across the
// listing. cancellable selects whether this call honours idx.ctx (true for
// every ASYNC, request-scheduled rebuild) or always runs to completion
// (false, for Warm's own synchronous call — see Warm's doc comment for why).
//
// A change during the listing itself (list-then-stamp race) is caught by
// listScopedDirOnce's own before/after generation read on the SAME
// descriptor and retried — up to maxRebuildAttempts (round 8 SHOULD): a
// directory that never stops changing cannot keep this goroutine re-listing
// forever, nor keep Warm blocked forever. Giving up leaves whatever the LAST
// attempt installed; that attempt's own generation almost certainly no
// longer matches the directory's current one, so storedNamesFor's own check
// finds the index stale and refuses fail-closed exactly as it would a
// rebuild still in flight.
//
// When cancellable and idx.ctx is done — the index has been evicted or
// pruned since this rebuild started (round 11 SHOULD) — the loop stops at
// the next checkpoint (between attempts, and once more right before
// installing) and installs NOTHING: there is no reader left this index
// could still be wrong for, so there is no reason to pay for, or trust, a
// listing nobody will read. Ends by releasing the single-flight slot and
// closing landed either way, so a concurrent waiter (Warm, or another
// request) is never left blocked; when backoffAfter is set (every
// spawnIndexRebuild-triggered call), it also opens the backoff window
// before another rebuild of this directory may start.
func (idx *storedNames) rebuild(key string, backoffAfter, cancellable bool) {
	defer idx.wg.Done()
	for attempt := 1; ; attempt++ {
		if cancellable && idx.ctx.Err() != nil {
			idx.finishRebuild(backoffAfter)
			return
		}
		before, after, names, listErr := listScopedDirOnce(key)
		now := indexClock()
		if cancellable && idx.ctx.Err() != nil {
			idx.finishRebuild(backoffAfter)
			return
		}
		if listErr == nil && attempt < maxRebuildAttempts && !before.equal(after) {
			continue
		}
		gen := after
		if listErr != nil {
			gen = before
		}
		idx.mu.Lock()
		idx.names, idx.err, idx.gen = names, listErr, gen
		idx.settled = listErr == nil && now.Sub(gen.latest()) >= generationSettleTime
		idx.building = false
		if backoffAfter {
			idx.nextAttempt = indexClock().Add(rebuildBackoff)
		}
		close(idx.landed)
		idx.mu.Unlock()
		return
	}
}

// finishRebuild releases the single-flight slot and closes landed without
// installing anything — used only when a cancellable rebuild stops early
// (round 11 SHOULD).
func (idx *storedNames) finishRebuild(backoffAfter bool) {
	idx.mu.Lock()
	idx.building = false
	if backoffAfter {
		idx.nextAttempt = indexClock().Add(rebuildBackoff)
	}
	close(idx.landed)
	idx.mu.Unlock()
}

// listScopedDirOnce opens key once, reads its generation, lists its entries
// through the SAME descriptor, and reads the generation once more — all
// round 11 MUST-FIX: a single open serves the generation read AND the
// listing, so a change during the listing (the list-then-stamp race) is
// caught by the two reads disagreeing, without ever resolving the path a
// second time. A variable so the tests can inject the directory-open seam's
// behaviour directly; the primitives it calls (dirfd_other.go) are
// themselves variables for finer-grained races.
var listScopedDirOnce = defaultListScopedDirOnce

func defaultListScopedDirOnce(key string) (before, after dirGeneration, names map[string]struct{}, err error) {
	dirfd, err := openScopedDir(key)
	if err != nil {
		return dirGeneration{}, dirGeneration{}, nil, err
	}
	defer func() { _ = unix.Close(dirfd) }()

	before, err = fstatDirGeneration(dirfd)
	if err != nil {
		return dirGeneration{}, dirGeneration{}, nil, err
	}
	entryNames, err := listScopedDir(dirfd, key)
	if err != nil {
		return before, dirGeneration{}, nil, err
	}
	after, err = fstatDirGeneration(dirfd)
	if err != nil {
		return before, dirGeneration{}, nil, err
	}
	names = make(map[string]struct{}, len(entryNames))
	for _, n := range entryNames {
		names[n] = struct{}{}
	}
	return before, after, names, nil
}
