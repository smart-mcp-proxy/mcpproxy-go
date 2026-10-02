package registries

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Spec 109 D35 (T160, demo finding #1): a catalog source that times out (the
// official registry's server-side ?search= took 17s in the live demo) used to
// contribute nothing. SearchAll now remembers the listing each source returned
// and, when a live fetch fails, answers from that cache. The source stays in
// unavailable[] (honestly) with fallback="cached_listing" and cached_at, and
// the hits carry FromCache.

// flakySource is a registry endpoint that serves entries, and can be told to
// hang past the per-source timeout or to answer 500.
type flakySource struct {
	srv    *httptest.Server
	slow   atomic.Bool
	broken atomic.Bool
	hits   atomic.Int32
	body   atomic.Value // string
}

func newFlakySource(t *testing.T, entries ...string) *flakySource {
	t.Helper()
	f := &flakySource{}
	f.body.Store("[" + strings.Join(entries, ",") + "]")
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		if f.slow.Load() {
			select {
			case <-time.After(5 * time.Second):
			case <-r.Context().Done():
				return
			}
		}
		if f.broken.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(f.body.Load().(string)))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func entryJSON(id, name, desc string) string {
	return fmt.Sprintf(`{"id":%q,"name":%q,"description":%q}`, id, name, desc)
}

func useFlakyRegistry(t *testing.T, f *flakySource) {
	t.Helper()
	ResetListingCacheForTest()
	t.Cleanup(ResetListingCacheForTest)
	// Registered after the reset so it runs first: a timed-out fetch keeps
	// running in the background and must not leak into the next test.
	t.Cleanup(func() { quiesceWarmBehind(t) })
	withTestRegistries(t, []RegistryEntry{
		{ID: "slowreg", Name: "Slow", ServersURL: f.srv.URL, Provenance: "official"},
	})
}

const fastTimeout = 100 * time.Millisecond

func TestSearchAll_TimedOutSourceFallsBackToCachedListing(t *testing.T) {
	f := newFlakySource(t,
		entryJSON("slow/github-a", "github-a", "GitHub helper"),
		entryJSON("slow/notes-x", "notes-x", "Notes"),
	)
	useFlakyRegistry(t, f)
	opts := SearchOptions{SourceTimeout: fastTimeout, PopularityWait: -1}

	// Browse primes the cache.
	if _, _, unavailable := SearchAll(context.Background(), "", "", 10, opts); len(unavailable) != 0 {
		t.Fatalf("priming browse: unexpected unavailable %+v", unavailable)
	}

	f.slow.Store(true)
	hits, _, unavailable := SearchAll(context.Background(), "github", "", 10, opts)
	if len(hits) != 1 || hits[0].Entry.ID != "slow/github-a" {
		t.Fatalf("expected the cached github hit, got %v", idsOf(hits))
	}
	if !hits[0].FromCache {
		t.Fatal("a cached hit must be marked FromCache")
	}
	if len(unavailable) != 1 {
		t.Fatalf("the source must stay in unavailable[], got %+v", unavailable)
	}
	u := unavailable[0]
	if u.Source != "slowreg" || u.Reason != "timeout after 100ms" {
		t.Fatalf("the reason must stay honest, got %+v", u)
	}
	if u.Fallback != FallbackCachedListing {
		t.Fatalf("Fallback = %q, want %q", u.Fallback, FallbackCachedListing)
	}
	if u.CachedAt == nil || time.Since(*u.CachedAt) > time.Minute {
		t.Fatalf("CachedAt must be the cache's update time, got %v", u.CachedAt)
	}
	if got := ToCatalogResult(hits[0], false); !got.FromCache {
		t.Fatal("the REST DTO must carry from_cache")
	}
}

func TestSearchAll_ServerErrorFallsBackToCachedListingToo(t *testing.T) {
	f := newFlakySource(t, entryJSON("slow/github-a", "github-a", "d"))
	useFlakyRegistry(t, f)
	opts := SearchOptions{SourceTimeout: 2 * time.Second, PopularityWait: -1}
	SearchAll(context.Background(), "", "", 10, opts)

	f.broken.Store(true)
	hits, _, unavailable := SearchAll(context.Background(), "github", "", 10, opts)
	if len(hits) != 1 || !hits[0].FromCache {
		t.Fatalf("a 5xx must fall back like a timeout, got %v", idsOf(hits))
	}
	if len(unavailable) != 1 || unavailable[0].Fallback != FallbackCachedListing {
		t.Fatalf("unavailable = %+v", unavailable)
	}
}

func TestSearchAll_CachedFallbackMatchesNameDescriptionAndID(t *testing.T) {
	f := newFlakySource(t,
		entryJSON("io.github.acme/alpha", "alpha", "does things"),
		entryJSON("zzz/beta", "beta", "Talks to GitHub"),
		entryJSON("zzz/GitHub-gamma", "Gamma", "plain"),
		entryJSON("zzz/delta", "delta", "unrelated"),
	)
	useFlakyRegistry(t, f)
	opts := SearchOptions{SourceTimeout: fastTimeout, PopularityWait: -1}
	SearchAll(context.Background(), "", "", 10, opts)

	f.slow.Store(true)
	hits, _, _ := SearchAll(context.Background(), "  GITHUB ", "", 10, opts)
	got := map[string]bool{}
	for _, h := range hits {
		got[h.Entry.ID] = true
	}
	for _, want := range []string{"io.github.acme/alpha", "zzz/beta", "zzz/GitHub-gamma"} {
		if !got[want] {
			t.Errorf("expected %s (matched on id/description/id), got %v", want, idsOf(hits))
		}
	}
	if got["zzz/delta"] {
		t.Errorf("zzz/delta does not match and must not be returned")
	}
}

func TestSearchAll_ColdCacheKeepsTodaysBehaviour(t *testing.T) {
	f := newFlakySource(t, entryJSON("slow/github-a", "github-a", "d"))
	useFlakyRegistry(t, f)
	f.slow.Store(true)
	hits, _, unavailable := SearchAll(context.Background(), "github", "", 10, SearchOptions{SourceTimeout: fastTimeout, PopularityWait: -1})
	if len(hits) != 0 {
		t.Fatalf("a cold cache contributes nothing, got %v", idsOf(hits))
	}
	if len(unavailable) != 1 || unavailable[0].Fallback != "" || unavailable[0].CachedAt != nil {
		t.Fatalf("no fallback marker without a cached listing, got %+v", unavailable)
	}
}

func TestSearchAll_StaleCacheOver24hIsNotUsed(t *testing.T) {
	f := newFlakySource(t, entryJSON("slow/github-a", "github-a", "d"))
	useFlakyRegistry(t, f)
	opts := SearchOptions{SourceTimeout: fastTimeout, PopularityWait: -1}

	// Background fetches from earlier tests read listingNow when they cache.
	quiesceWarmBehind(t)
	base := time.Now()
	prev := listingNow
	t.Cleanup(func() {
		quiesceWarmBehind(t)
		listingNow = prev
	})
	listingNow = func() time.Time { return base }
	SearchAll(context.Background(), "", "", 10, opts)

	f.slow.Store(true)
	listingNow = func() time.Time { return base.Add(24*time.Hour + time.Minute) }
	hits, _, unavailable := SearchAll(context.Background(), "github", "", 10, opts)
	if len(hits) != 0 || unavailable[0].Fallback != "" {
		t.Fatalf("a listing older than 24h must not be served, got %v / %+v", idsOf(hits), unavailable)
	}

	// exactly at the limit is still served. A timed-out fetch above keeps
	// running in the background and reads listingNow when it caches, so let it
	// finish before the clock is swapped.
	quiesceWarmBehind(t)
	listingNow = func() time.Time { return base.Add(24 * time.Hour) }
	hits, _, _ = SearchAll(context.Background(), "github", "", 10, opts)
	if len(hits) != 1 {
		t.Fatalf("a listing exactly 24h old is still fresh, got %v", idsOf(hits))
	}
}

func TestSearchAll_LiveHitsNeverMarkedCached(t *testing.T) {
	f := newFlakySource(t, entryJSON("slow/github-a", "github-a", "d"))
	useFlakyRegistry(t, f)
	opts := SearchOptions{SourceTimeout: time.Second, PopularityWait: -1}
	for i := 0; i < 2; i++ { // the second call has a warm cache but a healthy source
		hits, _, unavailable := SearchAll(context.Background(), "github", "", 10, opts)
		if len(hits) != 1 || hits[0].FromCache || len(unavailable) != 0 {
			t.Fatalf("live hits must not be marked cached: %v / %+v", hits, unavailable)
		}
	}
}

func TestSearchAll_EmptyQueryBrowseFallsBackToCachedListing(t *testing.T) {
	f := newFlakySource(t,
		entryJSON("slow/a", "a", "d"),
		entryJSON("slow/b", "b", "d"),
	)
	useFlakyRegistry(t, f)
	opts := SearchOptions{SourceTimeout: fastTimeout, PopularityWait: -1}
	SearchAll(context.Background(), "", "", 10, opts)

	f.slow.Store(true)
	hits, sections, unavailable := SearchAll(context.Background(), "", "", 10, opts)
	if len(hits) != 2 || !hits[0].FromCache {
		t.Fatalf("browse must be answered from the cached listing, got %v", idsOf(hits))
	}
	if sections == nil || len(sections.Official) != 2 {
		t.Fatalf("the Official section must be built from the cached hits, got %+v", sections)
	}
	for _, h := range sections.Official {
		if !h.FromCache {
			t.Fatal("section hits must carry FromCache")
		}
	}
	if len(unavailable) != 1 || unavailable[0].Fallback != FallbackCachedListing {
		t.Fatalf("unavailable = %+v", unavailable)
	}
}

func TestSearchAll_KeyMissingSourceNeverFallsBack(t *testing.T) {
	ResetListingCacheForTest()
	t.Cleanup(ResetListingCacheForTest)
	reg := RegistryEntry{ID: "keyed", Name: "Keyed", ServersURL: "http://127.0.0.1:1/servers", RequiresKey: true, Provenance: "official"}
	withTestRegistries(t, []RegistryEntry{reg})
	cacheListing(&reg, []ServerEntry{{ID: "keyed/github", Name: "github"}})

	hits, _, unavailable := SearchAll(context.Background(), "github", "", 10, SearchOptions{SourceTimeout: fastTimeout, PopularityWait: -1})
	if len(hits) != 0 {
		t.Fatalf("a key-missing source never fetched, so it must not serve a cache: %v", idsOf(hits))
	}
	if len(unavailable) != 1 || unavailable[0].Fallback != "" {
		t.Fatalf("unavailable = %+v", unavailable)
	}
}

func TestListingCache_MergeByIDCapAndPrune(t *testing.T) {
	ResetListingCacheForTest()
	t.Cleanup(ResetListingCacheForTest)
	reg := RegistryEntry{ID: "r", ServersURL: "http://one"}

	cacheListing(&reg, []ServerEntry{{ID: "a", Name: "old-a"}, {ID: "b", Name: "b"}})
	cacheListing(&reg, []ServerEntry{{ID: "a", Name: "new-a"}, {ID: "c", Name: "c"}})
	got, at, ok := cachedListing(&reg)
	if !ok || at.IsZero() {
		t.Fatal("expected a fresh cached listing")
	}
	// latest fetch first (latest wins), then the older entries not in it
	if ids := idsOf(hitsFromEntries(got)); strings.Join(ids, ",") != "a,c,b" || got[0].Name != "new-a" {
		t.Fatalf("merge order = %v (%s)", ids, got[0].Name)
	}

	// cap: 2,000 per source, keeping the newest
	var many []ServerEntry
	for i := 0; i < 2500; i++ {
		many = append(many, ServerEntry{ID: fmt.Sprintf("e%04d", i)})
	}
	cacheListing(&reg, many)
	got, _, _ = cachedListing(&reg)
	if len(got) != listingCacheMaxEntries || got[0].ID != "e0000" {
		t.Fatalf("expected %d entries led by the latest fetch, got %d (first %s)", listingCacheMaxEntries, len(got), got[0].ID)
	}

	// an empty result never wipes a listing
	cacheListing(&reg, nil)
	if got, _, _ = cachedListing(&reg); len(got) != listingCacheMaxEntries {
		t.Fatal("caching an empty result must be a no-op")
	}

	// editing the source URL never serves the old listing
	edited := RegistryEntry{ID: "r", ServersURL: "http://two"}
	if _, _, ok := cachedListing(&edited); ok {
		t.Fatal("a different servers_url is a different cache key")
	}

	// pruning drops keys that are no longer configured
	other := RegistryEntry{ID: "keep", ServersURL: "http://keep"}
	cacheListing(&other, []ServerEntry{{ID: "k"}})
	pruneListingCache([]RegistryEntry{other, edited})
	if _, _, ok := cachedListing(&reg); ok {
		t.Fatal("the pruned (id, url) must be gone")
	}
	if _, _, ok := cachedListing(&other); !ok {
		t.Fatal("a still-configured listing must survive pruning")
	}
}

func hitsFromEntries(entries []ServerEntry) []CatalogHit {
	out := make([]CatalogHit, len(entries))
	for i, e := range entries {
		out[i] = CatalogHit{Entry: e}
	}
	return out
}

func TestListingCache_ReturnsACopy(t *testing.T) {
	ResetListingCacheForTest()
	t.Cleanup(ResetListingCacheForTest)
	reg := RegistryEntry{ID: "r", ServersURL: "http://one"}
	cacheListing(&reg, []ServerEntry{{ID: "a", Name: "a"}})
	got, _, _ := cachedListing(&reg)
	got[0].Name = "mutated"
	again, _, _ := cachedListing(&reg)
	if again[0].Name != "a" {
		t.Fatal("callers must not be able to mutate the cache")
	}
}

func TestListingCache_ConcurrentSearchAllRace(t *testing.T) {
	f := newFlakySource(t,
		entryJSON("slow/github-a", "github-a", "d"),
		entryJSON("slow/notes-x", "notes-x", "d"),
	)
	useFlakyRegistry(t, f)
	opts := SearchOptions{SourceTimeout: 200 * time.Millisecond, PopularityWait: -1}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				if g%2 == 0 && i == 2 {
					f.slow.Store(true)
				}
				q := ""
				if i%2 == 1 {
					q = "github"
				}
				SearchAll(context.Background(), q, "", 10, opts)
				if g == 7 && i == 4 {
					f.slow.Store(false)
				}
			}
		}(g)
	}
	wg.Wait()
}

func TestSearchAll_FallbackWithinSC011Budget(t *testing.T) {
	f := newFlakySource(t, entryJSON("slow/github-a", "github-a", "d"))
	useFlakyRegistry(t, f)
	opts := SearchOptions{SourceTimeout: fastTimeout, PopularityWait: -1}
	SearchAll(context.Background(), "", "", 10, opts)

	f.slow.Store(true)
	start := time.Now()
	hits, _, _ := SearchAll(context.Background(), "github", "", 10, opts)
	if elapsed := time.Since(start); elapsed > fastTimeout+200*time.Millisecond {
		t.Fatalf("the fallback must not extend the budget: took %s", elapsed)
	}
	if len(hits) != 1 {
		t.Fatalf("expected the cached hit, got %v", idsOf(hits))
	}
}

// quiesceWarmBehind waits until no background (warm-behind) fetch is running,
// so a test may swap package-level hooks such as listingNow without racing
// the goroutine a timed-out search leaves behind.
func quiesceWarmBehind(t *testing.T) {
	t.Helper()
	waitFor(t, "background fetches to finish", 10*time.Second, func() bool {
		warmBehind.mu.Lock()
		defer warmBehind.mu.Unlock()
		return len(warmBehind.inflight) == 0
	})
}
