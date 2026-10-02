package registries

import (
	"context"
	"errors"
	"sync"
	"time"
)

// catalog_warm_behind.go: "warm behind" for a slow catalog source (Spec 109
// D37.11, FR-060).
//
// The official registry answers a cold ?search= in 4-25 s, longer than the 5 s
// per-source budget (FR-060). A timed-out fetch used to be cancelled, so nothing
// was cached and every later search started cold again. Now each network fetch
// runs under its OWN context (a copy of the caller's values, no cancellation,
// bounded by catalogWarmBehindTimeout) while SearchAll still waits only the
// source budget and reports "timeout after 5s" exactly as before. A fetch that
// finishes later calls cacheListing, so the NEXT search that times out is
// answered from the warmed cache (D35), and a warm registry answers live.
//
// Bounded on purpose: at most catalogWarmBehindSlots fetches per source
// (listingKey) may run past the budget at once. A source already holding all its
// slots falls back to the old behaviour, cancelled at the budget. The built-in
// reference source is served in-binary and never runs in the background.

const (
	// catalogWarmBehindTimeout bounds one background fetch.
	catalogWarmBehindTimeout = 30 * time.Second
	// catalogWarmBehindSlots is how many background fetches one source may hold.
	catalogWarmBehindSlots = 2
)

var warmBehind = struct {
	mu       sync.Mutex
	timeout  time.Duration
	slots    int
	inflight map[string]int
}{timeout: catalogWarmBehindTimeout, slots: catalogWarmBehindSlots, inflight: make(map[string]int)}

// acquireWarmBehind takes one background slot for key, returning the
// background timeout, or false when the source already holds all of them.
func acquireWarmBehind(key string) (time.Duration, bool) {
	warmBehind.mu.Lock()
	defer warmBehind.mu.Unlock()
	if warmBehind.inflight[key] >= warmBehind.slots {
		return 0, false
	}
	warmBehind.inflight[key]++
	return warmBehind.timeout, true
}

func releaseWarmBehind(key string) {
	warmBehind.mu.Lock()
	defer warmBehind.mu.Unlock()
	if warmBehind.inflight[key] <= 1 {
		delete(warmBehind.inflight, key)
		return
	}
	warmBehind.inflight[key]--
}

// sourceFetchFunc fetches one source's entries. onPartial receives hits that
// are already known while the fetch is still running (see
// searchCatalogSourceProgress), and whether the main query is among them; it
// may be ignored.
type sourceFetchFunc func(ctx context.Context, onPartial func(entries []ServerEntry, mainLanded bool)) ([]ServerEntry, error)

// sourceFetchOutcome is what SearchAll gets back for one source. entries is the
// fetch's result (on error: whatever hits arrived anyway, e.g. the official
// protocol's expansion hits); timedOut says the budget, not the source, ended
// the wait.
type sourceFetchOutcome struct {
	entries  []ServerEntry
	err      error
	timedOut bool
}

type partialEntries struct {
	mu         sync.Mutex
	entries    []ServerEntry
	mainLanded bool
}

func (p *partialEntries) set(entries []ServerEntry, mainLanded bool) {
	p.mu.Lock()
	p.entries = entries
	p.mainLanded = p.mainLanded || mainLanded
	p.mu.Unlock()
}

func (p *partialEntries) get() ([]ServerEntry, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.entries, p.mainLanded
}

// fetchSourceWithinBudget runs fetch for one source and waits at most budget
// for it. On success the entries are cached as the source's listing (D35). When
// the budget runs out first and the source holds a free background slot, the
// fetch keeps running (bounded by the warm-behind timeout, independent of ctx)
// and caches its listing when it lands.
func fetchSourceWithinBudget(ctx context.Context, reg RegistryEntry, budget time.Duration, fetch sourceFetchFunc) sourceFetchOutcome {
	sctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	key := listingKey(&reg)
	var bgTimeout time.Duration
	background := false
	if reg.Protocol != protocolReference {
		bgTimeout, background = acquireWarmBehind(key)
	}

	partial := &partialEntries{}
	run := func(c context.Context) sourceFetchOutcome {
		entries, err := fetch(c, partial.set)
		if err == nil {
			cacheListing(&reg, entries)
		}
		return sourceFetchOutcome{entries: entries, err: err}
	}

	if !background {
		out := run(sctx)
		out.timedOut = out.err != nil && sctx.Err() != nil
		return out
	}

	bctx, bcancel := context.WithTimeout(context.WithoutCancel(ctx), bgTimeout)
	done := make(chan sourceFetchOutcome, 1)
	go func() {
		defer releaseWarmBehind(key)
		defer bcancel()
		done <- run(bctx)
	}()

	select {
	case out := <-done:
		out.timedOut = out.err != nil && sctx.Err() != nil
		return out
	case <-sctx.Done():
		// A result that landed at the same instant still counts.
		select {
		case out := <-done:
			out.timedOut = out.err != nil
			return out
		default:
		}
		entries, mainLanded := partial.get()
		if mainLanded {
			// The main query decides availability (D37.2): it answered, only
			// an expansion is still running, so the source is not unavailable.
			// The fetch keeps going in the background and caches the full
			// listing when it lands.
			return sourceFetchOutcome{entries: entries}
		}
		return sourceFetchOutcome{entries: entries, err: errors.New("source budget exceeded"), timedOut: true}
	}
}
