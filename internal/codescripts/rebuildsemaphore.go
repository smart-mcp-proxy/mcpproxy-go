package codescripts

// spawnIndexRebuild runs one ASYNC index rebuild on its own goroutine,
// shared by both platform index implementations (storednames_other.go,
// storednames_windows.go — they never build together). A variable so the
// tests can hold a rebuild back and prove what a request does on its own
// goroutine, then land it deliberately.
var spawnIndexRebuild = func(rebuild func()) { go rebuild() }

// maxConcurrentRebuilds bounds how many ASYNC index-rebuild goroutines may
// run at once, PROCESS-WIDE across every directory's index and both
// platform index implementations (round 13 SHOULD, finding 5) — the unix
// one (storednames_other.go) and the Windows one (storedspellings_probe_windows.go),
// both of which share this single semaphore rather than each keeping its
// own bound. Cancellation (round 11 SHOULD, idx.ctx) stops an evicted
// index's rebuild only at its next checkpoint — between listing attempts,
// or right before installing — never mid-listing, which is uninterruptible;
// a directory backed by a slow or stalled filesystem can therefore leave a
// rebuild goroutine (and its retained directory handle/descriptor) running
// for as long as that one blocking listing takes, however many DIFFERENT
// directories keep triggering new ones in the meantime. rebuildSlots below
// is what keeps that count bounded rather than merely eventually-cancelled.
//
// No build tag: this file compiles identically on every platform so the
// unix and Windows index implementations — which never build together —
// can each import the identical semaphore and bound without duplicating its
// definition (or its capacity) in two places that could drift apart.
const maxConcurrentRebuilds = 2

// rebuildSlots is the process-wide semaphore a scheduleRebuildLocked
// implementation acquires before spawning an ASYNC rebuild and releases
// when that rebuild returns — win, lose, or cancelled. A directory whose
// rebuild cannot acquire a slot is not queued or blocked on one becoming
// free: scheduling simply does not spawn it this time, leaving the index
// exactly as stale as it already was. Nothing is lost — the NEXT request
// against that directory still finds it stale and tries to schedule again,
// acquiring a slot afresh. A capacity of 2 lets one directory's rebuild
// proceed while another directory's request-triggered rebuild is scheduled
// too, without letting an unbounded number of evicted, still-listing
// goroutines accumulate.
var rebuildSlots = make(chan struct{}, maxConcurrentRebuilds)
