package toolsig

import (
	"fmt"
	"sync"
	"testing"
)

const cacheTestSchema = `{"type":"object","properties":{"origin":{"type":"string"}},"required":["origin"]}`

// TestCache_GetCompilesOnMissAndMemoizes: first Get compiles, repeats are
// pure hits (FR-008 "not per request").
func TestCache_GetCompilesOnMissAndMemoizes(t *testing.T) {
	c := NewCache()

	want, _ := Render(cacheTestSchema, "Create a CDN. More detail.")
	got := c.Get("h1", cacheTestSchema, "Create a CDN. More detail.")
	if got != want {
		t.Fatalf("Get() = %+v, want Render output %+v", got, want)
	}
	if n := c.CompileCount(); n != 1 {
		t.Fatalf("CompileCount after first Get = %d, want 1", n)
	}

	for i := 0; i < 10; i++ {
		if got := c.Get("h1", cacheTestSchema, "Create a CDN. More detail."); got != want {
			t.Fatalf("memoized Get() = %+v, want %+v", got, want)
		}
	}
	if n := c.CompileCount(); n != 1 {
		t.Errorf("CompileCount after repeated Gets = %d, want 1 (memoized)", n)
	}
}

// TestCache_DistinctHashesDistinctEntries: the hash is the key — the same
// hash never recompiles, a new hash does (index rebuilds/definition changes
// naturally invalidate by changing the hash).
func TestCache_DistinctHashesDistinctEntries(t *testing.T) {
	c := NewCache()

	sigA := c.Get("hashA", cacheTestSchema, "A first. A second.")
	sigB := c.Get("hashB", `{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`, "B first. B second.")
	if sigA == sigB {
		t.Fatalf("distinct definitions must yield distinct signatures")
	}
	if n := c.CompileCount(); n != 2 {
		t.Fatalf("CompileCount = %d, want 2 (one per unique hash)", n)
	}

	// Same hash again — no recompile even with different (stale) inputs: the
	// hash covers schema+description, so equal hash means equal definition.
	if got := c.Get("hashA", cacheTestSchema, "A first. A second."); got != sigA {
		t.Errorf("hashA hit = %+v, want %+v", got, sigA)
	}
	if n := c.CompileCount(); n != 2 {
		t.Errorf("CompileCount after hits = %d, want 2", n)
	}
}

// TestCache_WarmThenGetIsHit: Warm (the indexing path) populates the cache so
// the request path's Get never compiles (FR-008).
func TestCache_WarmThenGetIsHit(t *testing.T) {
	c := NewCache()
	for i := 0; i < 5; i++ {
		c.Warm(fmt.Sprintf("h%d", i), cacheTestSchema, "Warmed tool.")
	}
	if n := c.CompileCount(); n != 5 {
		t.Fatalf("CompileCount after warm = %d, want 5", n)
	}
	for i := 0; i < 5; i++ {
		c.Get(fmt.Sprintf("h%d", i), cacheTestSchema, "Warmed tool.")
	}
	if n := c.CompileCount(); n != 5 {
		t.Errorf("CompileCount after post-warm Gets = %d, want 5 (pure hits)", n)
	}
}

// TestCache_RetainHashes_EvictsStale: RetainHashes reconciles the cache to
// the live tool set — entries whose hash is no longer live are evicted, live
// ones survive as pure hits, and an evicted hash recompiles on next Get.
func TestCache_RetainHashes_EvictsStale(t *testing.T) {
	c := NewCache()
	for _, h := range []string{"live-1", "live-2", "stale-1", "stale-2"} {
		c.Warm(h, cacheTestSchema, "Tool.")
	}
	if n := c.Len(); n != 4 {
		t.Fatalf("Len after warm = %d, want 4", n)
	}

	live := map[string]struct{}{"live-1": {}, "live-2": {}}
	evicted := c.RetainHashes(live)
	if evicted != 2 {
		t.Errorf("RetainHashes evicted = %d, want 2", evicted)
	}
	if n := c.Len(); n != len(live) {
		t.Errorf("Len after retain = %d, want %d (cache must match the live tool set)", n, len(live))
	}

	// Survivors are still pure hits.
	before := c.CompileCount()
	c.Get("live-1", cacheTestSchema, "Tool.")
	c.Get("live-2", cacheTestSchema, "Tool.")
	if n := c.CompileCount(); n != before {
		t.Errorf("retained entries recompiled: CompileCount %d -> %d", before, n)
	}

	// Evicted hashes are genuine misses again.
	c.Get("stale-1", cacheTestSchema, "Tool.")
	if n := c.CompileCount(); n != before+1 {
		t.Errorf("evicted hash must recompile on Get: CompileCount = %d, want %d", n, before+1)
	}

	// Empty live set clears everything; nil behaves the same.
	c.RetainHashes(nil)
	if n := c.Len(); n != 0 {
		t.Errorf("RetainHashes(nil) must clear the cache, Len = %d", n)
	}
}

// TestCache_RetainHashes_ConcurrentWithGet runs RetainHashes against a storm
// of Get/Warm calls (under -race) and asserts the cache converges to exactly
// the live set afterward — churn must be race-safe.
func TestCache_RetainHashes_ConcurrentWithGet(t *testing.T) {
	c := NewCache()
	live := map[string]struct{}{"h0": {}, "h2": {}, "h4": {}, "h6": {}}

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				h := fmt.Sprintf("h%d", i%8)
				if w%2 == 0 {
					c.Get(h, cacheTestSchema, "Concurrent tool.")
				} else {
					c.Warm(h, cacheTestSchema, "Concurrent tool.")
				}
				if i%50 == 0 {
					c.RetainHashes(live)
				}
			}
		}(w)
	}
	wg.Wait()

	// Final reconcile: whatever raced back in after the last retain goes away.
	c.RetainHashes(live)
	if n := c.Len(); n != len(live) {
		t.Errorf("Len after final retain = %d, want %d", n, len(live))
	}
	for h := range live {
		before := c.CompileCount()
		c.Warm(h, cacheTestSchema, "Concurrent tool.")
		if c.CompileCount() != before {
			t.Errorf("live hash %s was evicted by RetainHashes", h)
		}
	}
}

// TestCache_ConcurrentGetWarm_RaceClean hammers Get/Warm from many goroutines
// (run under -race) and asserts each unique hash compiled exactly once per
// the memoization contract (a lost race may compile a value that is then
// discarded, but the counter tracks stored compilations).
func TestCache_ConcurrentGetWarm_RaceClean(t *testing.T) {
	c := NewCache()
	const hashes = 8
	const workers = 16

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				h := fmt.Sprintf("h%d", i%hashes)
				if w%2 == 0 {
					c.Get(h, cacheTestSchema, "Concurrent tool.")
				} else {
					c.Warm(h, cacheTestSchema, "Concurrent tool.")
				}
			}
		}(w)
	}
	wg.Wait()

	if n := c.CompileCount(); n != hashes {
		t.Errorf("CompileCount = %d, want %d (once per unique hash)", n, hashes)
	}
	if n := c.Len(); n != hashes {
		t.Errorf("Len = %d, want %d", n, hashes)
	}
}
