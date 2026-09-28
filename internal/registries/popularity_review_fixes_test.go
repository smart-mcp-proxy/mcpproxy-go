package registries

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A 200 that is not a GitHub repo payload (captive portal, proxy error page)
// is a failed fetch: FR-008 keeps the last-known stars and ETag, and the
// bogus ETag must never be sent back as If-None-Match.
func TestGitHubStarsProvider_Malformed200KeepsLastKnownStars(t *testing.T) {
	var reqCount int32
	var lastIfNoneMatch atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastIfNoneMatch.Store(r.Header.Get("If-None-Match"))
		if atomic.AddInt32(&reqCount, 1) == 1 {
			w.Header().Set("ETag", `"good"`)
			_, _ = w.Write([]byte(`{"stargazers_count": 500}`))
			return
		}
		w.Header().Set("ETag", `"portal"`)
		_, _ = w.Write([]byte(`<html>Sign in to the Wi-Fi</html>`))
	}))
	defer srv.Close()

	defer SetGitHubAPIBaseForTest(srv.URL)()
	p := NewGitHubStarsProvider(PopularityOptions{})
	defer p.Close()
	fixedNow := time.Now()
	p.now = func() time.Time { return fixedNow }

	p.Resolve(context.Background(), []string{"o/r"}, time.Second)
	fixedNow = fixedNow.Add(25 * time.Hour)
	p.Resolve(context.Background(), []string{"o/r"}, time.Second)

	if stars, _ := p.Lookup("o/r"); stars != 500 {
		t.Fatalf("a non-JSON 200 cleared the stars: got %d, want the last-known 500", stars)
	}
	p.mu.Lock()
	etag := p.entries["o/r"].ETag
	p.mu.Unlock()
	if etag != `"good"` {
		t.Fatalf("stored ETag = %q, want the last good one", etag)
	}
}

// A 200 without stargazers_count must not be read as "0 stars".
func TestGitHubStarsProvider_200WithoutStargazersIsAFailedFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"message":"ok"}`))
	}))
	defer srv.Close()

	defer SetGitHubAPIBaseForTest(srv.URL)()
	p := NewGitHubStarsProvider(PopularityOptions{})
	defer p.Close()

	p.Resolve(context.Background(), []string{"o/r"}, time.Second)
	p.mu.Lock()
	e := p.entries["o/r"]
	p.mu.Unlock()
	if e == nil || e.Status != 0 || e.ttl() != githubErrorTTL {
		t.Fatalf("expected an error entry with the 1h error TTL, got %+v", e)
	}
}

// After Close, a key a worker still picks off the buffer, or a request cut
// short by the cancellation, must not spend budget or persist a
// shutdown-induced error entry.
func TestGitHubStarsProvider_CloseDoesNotRecordShutdownErrors(t *testing.T) {
	db := openTempPopularityDB(t)
	defer SetGitHubAPIBaseForTest("http://127.0.0.1:1")()
	p := NewGitHubStarsProvider(PopularityOptions{DB: db})
	_ = p.Close()

	p.fetchAndStore("o/r")
	p.applyResult("o/s", nil, 0, 0, "", rateLimitHeaders{}, context.Canceled)

	if n := len(p.store.all()); n != 0 {
		t.Fatalf("store has %d entries after shutdown, want 0", n)
	}
	if !p.budget.hasCapacity(time.Now()) || len(p.budget.hits) != 0 {
		t.Fatalf("a post-Close key spent budget: %d hits", len(p.budget.hits))
	}
}

// Resolve on a closed provider returns at once instead of queueing keys no
// worker will drain and waiting out the full wait.
func TestGitHubStarsProvider_ResolveAfterCloseReturnsImmediately(t *testing.T) {
	defer SetGitHubAPIBaseForTest("http://127.0.0.1:1")()
	p := NewGitHubStarsProvider(PopularityOptions{})
	_ = p.Close()

	start := time.Now()
	p.Resolve(context.Background(), []string{"o/r"}, 2*time.Second)
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("Resolve on a closed provider took %s", d)
	}
	p.mu.Lock()
	n := len(p.queued)
	p.mu.Unlock()
	if n != 0 {
		t.Fatalf("closed provider admitted %d keys", n)
	}
}

// Close wakes waiters on keys the stopped workers never took.
func TestGitHubStarsProvider_CloseWakesPendingWaiters(t *testing.T) {
	t.Setenv("MCPPROXY_CATALOG_POPULARITY", "false") // no workers: keys stay queued
	p := NewGitHubStarsProvider(PopularityOptions{})
	p.disabled = false // admit keys, but nothing drains them
	ch := p.enqueue("o/r")
	if ch == nil {
		t.Fatal("expected the key to be admitted")
	}
	_ = p.Close()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("waiter on a never-fetched key was not woken by Close")
	}
}

type stateStubProvider struct {
	stars int
	state LookupState
}

func (s *stateStubProvider) Lookup(string) (int, LookupState)                 { return s.stars, s.state }
func (s *stateStubProvider) Resolve(context.Context, []string, time.Duration) {}

// A repo confirmed gone during Resolve's wait must lose the stars an earlier
// (Stale) apply put on the hit, keeping only source-native popularity.
func TestApplyCachedStars_NegativeClearsEarlierStars(t *testing.T) {
	stub := &stateStubProvider{stars: 900, state: LookupStale}
	defer SetPopularityProviderForTest(stub)()

	installs := 42
	withInstalls := CatalogHit{Entry: ServerEntry{
		SourceCodeURL: "https://github.com/o/r",
		Popularity:    &Popularity{Installs: &installs},
	}}
	bare := CatalogHit{Entry: ServerEntry{SourceCodeURL: "https://github.com/o/r"}}
	for _, h := range []*CatalogHit{&withInstalls, &bare} {
		if h.Entry.Popularity != nil {
			p := *h.Entry.Popularity
			h.Popularity = &p
		}
		applyCachedStars(h)
		if h.Popularity == nil || h.Popularity.Stars == nil || *h.Popularity.Stars != 900 {
			t.Fatalf("setup: expected stale stars applied, got %+v", h.Popularity)
		}
	}

	stub.state, stub.stars = LookupNegative, 0
	applyCachedStars(&withInstalls)
	applyCachedStars(&bare)

	if withInstalls.Popularity == nil || withInstalls.Popularity.Stars != nil || *withInstalls.Popularity.Installs != 42 {
		t.Fatalf("expected stars cleared and installs kept, got %+v", withInstalls.Popularity)
	}
	if bare.Popularity != nil {
		t.Fatalf("expected Popularity dropped when no signal remains, got %+v", bare.Popularity)
	}
}
