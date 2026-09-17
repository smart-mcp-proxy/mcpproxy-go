package codescripts

import "time"

// generationSettleTime is how far a directory's stamp must predate a listing
// for its index to be trusted until the stamp moves. Timestamps can be
// coarse (vfat: two seconds; round 13: also a FAT-formatted volume on
// Windows, whose directory write time carries the same two-second
// resolution — NTFS itself is fine-grained, but a scripts directory is not
// guaranteed to live on it), so a write landing in the same tick as the
// recorded stamp would leave it unchanged; until the stamp is older than
// the coarsest tick, requests keep scheduling a refresh — at most one per
// window, and off the request path. The bound depends on the clock alone,
// never on the requested name.
//
// No build tag: shared by both platform index implementations — unix
// (storednames_other.go) and Windows (storedspellings_probe_windows.go) —
// which never build together, so one definition and one clock serve
// whichever is active rather than each keeping its own copy that could
// drift out of step.
const generationSettleTime = 2 * time.Second

// maxRebuildAttempts bounds how many times one rebuild re-lists when the
// directory's generation keeps moving out from under it (round 8 SHOULD): a
// directory that never stops changing must not keep this goroutine listing
// forever, nor block Warm forever. After the bound, whatever the last
// attempt installed stays as the index — the next request finds it stale
// against the directory's CURRENT generation and refuses fail-closed, rather
// than this loop trusting an unconfirmed listing or spinning on one that can
// never confirm.
const maxRebuildAttempts = 3

// rebuildBackoff is the minimum gap between the end of one ASYNC rebuild
// goroutine and the start of the next for the same directory (round 8
// SHOULD). Without it, a directory changing on every request would let
// scheduling spawn a fresh rebuild the instant the bounded one above gives
// up. During the backoff a request's own cost is unchanged — answered
// fail-closed from whatever the index holds (or does not); only the new
// rebuild goroutine is withheld.
const rebuildBackoff = time.Second

// maxStoredNameIndexes bounds a platform's index map for bare (never-Warmed)
// use — the server calls Warm whenever the active scripts directory changes,
// and Warm keeps only that one directory's index (pruneOtherIndexesLocked),
// so this cap matters only for the directories a scoped request alone
// touches without ever being Warmed for. Shared by both platform index
// implementations for the same reason as everything else in this file.
const maxStoredNameIndexes = 4

// indexClock is time.Now, a variable so the tests can settle an index
// without waiting.
var indexClock = time.Now

// SetIndexClockForTest overrides the clock the settle check reads (round 9
// MUST-FIX) and returns a func that restores it. A directory's on-disk
// change stamp cannot be forged from user space — it is exactly what makes
// the settle window a real guarantee — so a caller outside this package
// that needs a freshly written scripts directory treated as settled at once
// (an internal/server fixture, say) has no way to fake it by backdating a
// file; it must move the clock the settle check reads instead, as this
// package's own tests do internally. Test-only: production code never
// calls this, and callers outside this package must restore it (defer the
// returned func, or t.Cleanup) before any other test observes the
// override.
func SetIndexClockForTest(now func() time.Time) (restore func()) {
	prev := indexClock
	indexClock = now
	return func() { indexClock = prev }
}
