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
// spellings a scripts directory holds, listed once per directory GENERATION
// and validated on every request by one Lstat of the directory itself. Every
// request — hit, miss or case-variant alike — then costs the same: a
// directory Lstat and an O(1) set lookup while the index is current, one
// listing when the directory has changed since it was taken. The cost follows
// the administrator's writes, never the requested name.

// storedNames is the exact-spelling index of one scripts directory. names is
// replaced, never mutated, so a set handed out under the lock stays valid
// after it is released.
type storedNames struct {
	mu      sync.Mutex
	gen     dirGeneration // the directory's stamp when names was listed
	settled bool          // gen predates the listing by more than any timestamp tick
	names   map[string]struct{}
}

// storedNameIndexes holds one *storedNames per cleaned scripts directory.
var storedNameIndexes sync.Map

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
// for the index to be trusted without re-listing. Timestamps can be coarse
// (vfat: two seconds), so a write landing in the same tick as the recorded
// stamp would leave it unchanged; until the stamp is older than the coarsest
// tick, every request re-lists. The bound depends on the clock alone, never
// on the requested name.
const generationSettleTime = 2 * time.Second

// indexClock is time.Now, a variable so the tests can settle an index
// without waiting.
var indexClock = time.Now

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

// storedNamesFor returns the current exact-name set of scriptsDir, re-listing
// it under the directory's lock when its generation has moved or the last
// listing was taken too soon after a write to be trusted. The lock covers
// the validation and the rebuild only; callers never hold it across an open.
func storedNamesFor(scriptsDir string) (map[string]struct{}, error) {
	key := filepath.Clean(scriptsDir)
	v, ok := storedNameIndexes.Load(key)
	if !ok {
		v, _ = storedNameIndexes.LoadOrStore(key, &storedNames{})
	}
	idx := v.(*storedNames)

	// Stamped before the listing, so a write that lands during it moves the
	// stamp the next validation compares against.
	info, err := lstat(key)
	if err != nil {
		return nil, err
	}
	gen := dirGenerationOf(info)
	now := indexClock()

	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.names != nil && idx.settled && idx.gen.equal(gen) {
		return idx.names, nil
	}
	entries, err := readDir(key)
	if err != nil {
		return nil, err
	}
	names := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		names[e.Name()] = struct{}{}
	}
	idx.gen, idx.names = gen, names
	idx.settled = now.Sub(gen.latest()) >= generationSettleTime
	return names, nil
}
