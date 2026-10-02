package registries

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Spec 109 fix-catalog-rank T172 (D36.11): a fetch that outlives the 5s source
// budget finishes in the background and warms the listing cache; the budget,
// the unavailable[] wording and the FR-060 contract do not change.

func waitFor(t *testing.T, what string, within time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// slowThenHang answers the first request after `delay` and then hangs every
// later one until released, counting how many requests are in flight.
type slowThenHang struct {
	body     string
	delay    time.Duration
	released chan struct{}
	calls    atomic.Int32
	inflight atomic.Int32
	cancels  atomic.Int32
}

func (s *slowThenHang) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n := s.calls.Add(1)
	s.inflight.Add(1)
	defer s.inflight.Add(-1)
	if n == 1 {
		select {
		case <-time.After(s.delay):
		case <-r.Context().Done():
			s.cancels.Add(1)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(s.body))
		return
	}
	select {
	case <-s.released:
	case <-r.Context().Done():
		s.cancels.Add(1)
	}
}

func newSlowThenHang(t *testing.T, body string, delay time.Duration) (*slowThenHang, *httptest.Server) {
	t.Helper()
	h := &slowThenHang{body: body, delay: delay, released: make(chan struct{})}
	srv := httptest.NewServer(h)
	// close(released) runs BEFORE srv.Close (LIFO), so Close never waits on a
	// request the test left hanging.
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(h.released) })
	return h, srv
}

func TestSearchAll_TimedOutFetchWarmsTheListingCache(t *testing.T) {
	t.Cleanup(SetCatalogWarmBehindForTest(2*time.Second, 2))
	t.Cleanup(ResetListingCacheForTest)
	h, srv := newSlowThenHang(t, `[{"id":"acme/github-late","name":"github late"}]`, 300*time.Millisecond)
	reg := RegistryEntry{ID: "slow", Name: "Slow", ServersURL: srv.URL}
	withTestRegistries(t, []RegistryEntry{reg})

	opts := SearchOptions{SourceTimeout: 100 * time.Millisecond, PopularityWait: -1}
	hits, _, unavailable := SearchAll(context.Background(), "github", "", 10, opts)
	if len(hits) != 0 || len(unavailable) != 1 || unavailable[0].Reason != "timeout after 100ms" || unavailable[0].Fallback != "" {
		t.Fatalf("first search: hits=%v unavailable=%+v, want an empty timeout (nothing cached yet)", idsOf(hits), unavailable)
	}

	waitFor(t, "the background fetch to warm the cache", 2*time.Second, func() bool {
		_, _, ok := cachedListing(&reg)
		return ok
	})

	// The server now hangs: the second search times out too, and is answered
	// from the warmed listing.
	hits, _, unavailable = SearchAll(context.Background(), "github", "", 10, opts)
	if len(hits) != 1 || hits[0].Entry.ID != "acme/github-late" || !hits[0].FromCache {
		t.Fatalf("second search: hits=%v, want the warmed hit marked from cache", idsOf(hits))
	}
	if len(unavailable) != 1 || unavailable[0].Fallback != FallbackCachedListing || unavailable[0].Reason != "timeout after 100ms" {
		t.Fatalf("second search unavailable = %+v", unavailable)
	}
	if h.calls.Load() < 2 {
		t.Fatalf("expected a second request, got %d", h.calls.Load())
	}
}

func TestSearchAll_WarmBehindSlotsBounded(t *testing.T) {
	t.Cleanup(SetCatalogWarmBehindForTest(5*time.Second, 2))
	t.Cleanup(ResetListingCacheForTest)
	h := &slowThenHang{released: make(chan struct{})}
	h.calls.Store(1) // every request hangs
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(h.released) })
	withTestRegistries(t, []RegistryEntry{{ID: "hang", Name: "Hang", ServersURL: srv.URL}})

	opts := SearchOptions{SourceTimeout: 150 * time.Millisecond, PopularityWait: -1}
	for i := 0; i < 3; i++ {
		_, _, unavailable := SearchAll(context.Background(), "x", "", 10, opts)
		if len(unavailable) != 1 || unavailable[0].Reason != "timeout after 150ms" {
			t.Fatalf("search %d unavailable = %+v", i, unavailable)
		}
	}
	// The third search found both slots taken and ran under the plain budget:
	// its request is cancelled at the timeout.
	waitFor(t, "the third request to be cancelled", 2*time.Second, func() bool { return h.cancels.Load() >= 1 })
	time.Sleep(50 * time.Millisecond)
	if got := h.inflight.Load(); got > 2 {
		t.Fatalf("%d requests in flight, want ≤ 2 background fetches per source", got)
	}
	if got := h.cancels.Load(); got != 1 {
		t.Fatalf("cancelled requests = %d, want exactly the third", got)
	}
}

func TestSearchAll_WarmBehindHonoursItsOwnTimeout(t *testing.T) {
	t.Cleanup(SetCatalogWarmBehindForTest(250*time.Millisecond, 2))
	t.Cleanup(ResetListingCacheForTest)
	h := &slowThenHang{released: make(chan struct{})}
	h.calls.Store(1)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(h.released) })
	withTestRegistries(t, []RegistryEntry{{ID: "hang", Name: "Hang", ServersURL: srv.URL}})

	SearchAll(context.Background(), "x", "", 10, SearchOptions{SourceTimeout: 50 * time.Millisecond, PopularityWait: -1})
	waitFor(t, "the background request to start", time.Second, func() bool { return h.inflight.Load() == 1 })
	// The background context's own 250ms timeout cancels it.
	waitFor(t, "the warm-behind timeout to cancel the fetch", 3*time.Second, func() bool { return h.inflight.Load() == 0 })
	if h.cancels.Load() != 1 {
		t.Fatalf("cancels = %d, want the background fetch cancelled by its own timeout", h.cancels.Load())
	}
}

func TestSearchAll_CallerCancelDoesNotCancelWarmBehind(t *testing.T) {
	t.Cleanup(SetCatalogWarmBehindForTest(3*time.Second, 2))
	t.Cleanup(ResetListingCacheForTest)
	h, srv := newSlowThenHang(t, `[{"id":"acme/github-late","name":"github late"}]`, 250*time.Millisecond)
	reg := RegistryEntry{ID: "slow", Name: "Slow", ServersURL: srv.URL}
	withTestRegistries(t, []RegistryEntry{reg})

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		SearchAll(ctx, "github", "", 10, SearchOptions{SourceTimeout: 5 * time.Second, PopularityWait: -1})
	}()
	waitFor(t, "the request to arrive", time.Second, func() bool { return h.calls.Load() == 1 })
	cancel()
	wg.Wait()

	waitFor(t, "the background fetch to finish and warm the cache", 3*time.Second, func() bool {
		_, _, ok := cachedListing(&reg)
		return ok
	})
	if h.cancels.Load() != 0 {
		t.Fatal("the caller's cancellation must not cancel the in-flight fetch")
	}
}

func TestSearchAll_ReferenceSourceNeverRunsInBackground(t *testing.T) {
	if _, ok := acquireWarmBehind("probe"); !ok {
		t.Fatal("a slot should be free")
	}
	releaseWarmBehind("probe")

	reg := RegistryEntry{ID: "reference", Name: "Reference", Protocol: protocolReference, ServersURL: "builtin://reference"}
	key := listingKey(&reg)
	out := fetchSourceWithinBudget(context.Background(), reg, time.Second, func(context.Context, func([]ServerEntry, bool)) ([]ServerEntry, error) {
		warmBehind.mu.Lock()
		held := warmBehind.inflight[key]
		warmBehind.mu.Unlock()
		if held != 0 {
			t.Errorf("the reference source took %d background slot(s)", held)
		}
		return nil, nil
	})
	if out.err != nil {
		t.Fatal(out.err)
	}
}
