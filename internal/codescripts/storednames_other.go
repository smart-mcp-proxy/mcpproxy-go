//go:build !darwin && !windows

package codescripts

import (
	"errors"
	"io/fs"
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
}

// storedNameIndexes holds one *storedNames per cleaned scripts directory.
var storedNameIndexes sync.Map

// storedNamesIndex returns the index of one cleaned scripts directory,
// creating an empty (never built) one on first use.
func storedNamesIndex(key string) *storedNames {
	v, ok := storedNameIndexes.Load(key)
	if !ok {
		v, _ = storedNameIndexes.LoadOrStore(key, &storedNames{})
	}
	return v.(*storedNames)
}

// dirGeneration is the Lstat tuple that moves whenever a directory's entry
// set can have changed: adding, removing or renaming an entry updates its
// mtime and ctime (ctime cannot be set from user space, so a restored mtime —
// tar, rsync -a — does not hide a change), a replaced directory has another
// inode, and size is the cheap extra. dirGenerationOf reads it per platform.
type dirGeneration struct {
	modTime, changeTime time.Time
	size                int64
	ino                 uint64
}

func (g dirGeneration) equal(o dirGeneration) bool {
	return g.modTime.Equal(o.modTime) && g.changeTime.Equal(o.changeTime) && g.size == o.size && g.ino == o.ino
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

// indexClock is time.Now, a variable so the tests can settle an index
// without waiting.
var indexClock = time.Now

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
func Warm(scriptsDir string) error {
	key := filepath.Clean(scriptsDir)
	idx := storedNamesIndex(key)
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
	idx.rebuild(key)
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
func storedSpellingsOf(scriptsDir string) (storedExactly func(want string) (bool, error), err error) {
	names, err := storedNamesFor(scriptsDir)
	if err != nil {
		return nil, err
	}
	return func(want string) (bool, error) {
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
	}, nil
}

// storedNamesFor returns the exact-name set of scriptsDir as the index holds
// it — never listing on the caller's behalf. One Lstat of the directory reads
// its generation; when the index is behind it (or was never built, or is
// still inside the settle window) a single-flight ASYNCHRONOUS rebuild is
// scheduled and the request is answered from the index that exists: nil for
// a directory never listed, which every name misses (fail closed), the last
// build's error for one that could not be listed.
func storedNamesFor(scriptsDir string) (map[string]struct{}, error) {
	key := filepath.Clean(scriptsDir)
	idx := storedNamesIndex(key)

	info, err := lstat(key)
	if err != nil {
		return nil, err
	}
	gen := dirGenerationOf(info)
	now := indexClock()

	idx.mu.Lock()
	defer idx.mu.Unlock()
	switch {
	case idx.names == nil && idx.err == nil: // never built
		idx.scheduleRebuildLocked(key, now)
	case !idx.gen.equal(gen):
		idx.scheduleRebuildLocked(key, now)
	case !idx.settled && !now.Before(idx.refreshAfter):
		idx.scheduleRebuildLocked(key, now)
	}
	if idx.names == nil {
		return nil, idx.err
	}
	return idx.names, nil
}

// scheduleRebuildLocked starts the directory's rebuild goroutine unless one
// is already in flight, and opens the next refresh window either way.
func (idx *storedNames) scheduleRebuildLocked(key string, now time.Time) {
	idx.refreshAfter = now.Add(generationSettleTime)
	if idx.building {
		return
	}
	idx.beginRebuildLocked()
	spawnIndexRebuild(func() { idx.rebuild(key) })
}

// beginRebuildLocked claims the single-flight slot.
func (idx *storedNames) beginRebuildLocked() {
	idx.building = true
	idx.landed = make(chan struct{})
}

// rebuild lists the directory and installs the result, holding no lock across
// the listing. The stamp is read BEFORE the listing and re-read after it
// under the lock: a write that lands during the listing moves the stamp, and
// the listing is taken again rather than trusted (list-then-stamp race).
// Requests that arrive while a rebuild is in flight see building set and
// schedule nothing; their generation read precedes this re-check, so the
// re-check covers whatever they saw. Ends by releasing the slot and closing
// landed.
func (idx *storedNames) rebuild(key string) {
	for {
		gen, err := idx.build(key)
		idx.mu.Lock()
		if err == nil {
			if info, statErr := lstat(key); statErr == nil && !dirGenerationOf(info).equal(gen) {
				idx.mu.Unlock()
				continue
			}
		}
		idx.building = false
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
