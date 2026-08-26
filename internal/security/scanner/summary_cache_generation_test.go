package scanner

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The scan-summary cache is written by GetScanSummary AFTER it has read BBolt
// with no lock held, and invalidated by the scan callbacks. Without a
// generation token the two interleave into a lost invalidation: a reader that
// sampled the pre-scan state stores it on top of the invalidation a starting
// scan just performed, and every later reader is served that stale summary from
// cache. It matters because quarantine_security's scan_server waits for the job
// to settle and then reports GetScanSummary's verdict verbatim to an agent — a
// resurrected pre-scan "clean" would be handed over as the new scan's result.

// TestCacheScanSummary_DropsWriteInvalidatedMidComputation is the direct
// regression: sample a generation, invalidate (as a starting scan does), then
// try to store. The store must be refused.
func TestCacheScanSummary_DropsWriteInvalidatedMidComputation(t *testing.T) {
	svc := newFreshInstallService(t)
	const server = "victim"

	// A reader misses the cache and samples the generation.
	svc.summaryCacheMu.RLock()
	gen := svc.summaryCacheGen[server]
	svc.summaryCacheMu.RUnlock()

	// While it reads BBolt, a scan starts and invalidates.
	svc.invalidateScanSummaryCache(server)

	// The in-flight reader now tries to publish its pre-scan summary.
	stale := &ScanSummary{Status: "clean"}
	svc.cacheScanSummary(server, stale, gen)

	svc.summaryCacheMu.RLock()
	cached, ok := svc.summaryCache[server]
	svc.summaryCacheMu.RUnlock()

	assert.False(t, ok, "a summary computed before the invalidation must not be cached")
	assert.Nil(t, cached)
}

// TestCacheScanSummary_StoresWhenGenerationUnchanged pins the other half: the
// guard must not break ordinary caching, or spec 047's negative caching would
// stop working and every poll would re-scan the BBolt job bucket.
func TestCacheScanSummary_StoresWhenGenerationUnchanged(t *testing.T) {
	svc := newFreshInstallService(t)
	const server = "quiet"

	svc.summaryCacheMu.RLock()
	gen := svc.summaryCacheGen[server]
	svc.summaryCacheMu.RUnlock()

	summary := &ScanSummary{Status: "clean"}
	svc.cacheScanSummary(server, summary, gen)

	svc.summaryCacheMu.RLock()
	cached, ok := svc.summaryCache[server]
	svc.summaryCacheMu.RUnlock()

	require.True(t, ok, "an uncontended summary must still be cached")
	assert.Same(t, summary, cached)

	// The nil sentinel ("checked, never scanned") must cache too.
	const empty = "untouched"
	svc.summaryCacheMu.RLock()
	emptyGen := svc.summaryCacheGen[empty]
	svc.summaryCacheMu.RUnlock()
	svc.cacheScanSummary(empty, nil, emptyGen)

	svc.summaryCacheMu.RLock()
	sentinel, sentinelOK := svc.summaryCache[empty]
	svc.summaryCacheMu.RUnlock()

	assert.True(t, sentinelOK, "the negative-cache sentinel must still be stored")
	assert.Nil(t, sentinel)
}

// TestInvalidateScanSummaryCache_AdvancesGeneration pins that invalidation is
// what moves the token — a second invalidation must not reuse the first's
// generation, or two scans in a row would reopen the window.
func TestInvalidateScanSummaryCache_AdvancesGeneration(t *testing.T) {
	svc := newFreshInstallService(t)
	const server = "twice"

	svc.summaryCacheMu.RLock()
	start := svc.summaryCacheGen[server]
	svc.summaryCacheMu.RUnlock()

	svc.invalidateScanSummaryCache(server)
	svc.invalidateScanSummaryCache(server)

	svc.summaryCacheMu.RLock()
	end := svc.summaryCacheGen[server]
	svc.summaryCacheMu.RUnlock()

	assert.Equal(t, start+2, end, "each invalidation must advance the generation")

	// A reader holding the pre-invalidation token is still refused.
	svc.cacheScanSummary(server, &ScanSummary{Status: "clean"}, start)
	svc.summaryCacheMu.RLock()
	_, ok := svc.summaryCache[server]
	svc.summaryCacheMu.RUnlock()
	assert.False(t, ok)
}
