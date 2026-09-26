package registries

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- T005: fetch behavior ---------------------------------------------------

func TestGitHubStarsProvider_Fetch200StoresStarsAndSendsExactHeaders(t *testing.T) {
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.Header().Set("ETag", `"v1"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"stargazers_count": 123}`))
	}))
	defer srv.Close()

	defer SetGitHubAPIBaseForTest(srv.URL)()
	p := NewGitHubStarsProvider(PopularityOptions{})
	defer p.Close()

	p.Resolve(context.Background(), []string{"o/r"}, time.Second)

	stars, state := p.Lookup("o/r")
	if state != LookupFresh || stars != 123 {
		t.Fatalf("expected Fresh/123, got state=%d stars=%d", state, stars)
	}
	if got := gotHeaders.Get("Accept"); got != "application/vnd.github+json" {
		t.Errorf("Accept header = %q", got)
	}
	if got := gotHeaders.Get("X-GitHub-Api-Version"); got != "2022-11-28" {
		t.Errorf("X-GitHub-Api-Version header = %q", got)
	}
	if got := gotHeaders.Get("User-Agent"); got == "" {
		t.Error("expected a non-empty User-Agent")
	}
	if got := gotHeaders.Get("Authorization"); got != "" {
		t.Errorf("expected no Authorization header without MCPPROXY_GITHUB_TOKEN, got %q", got)
	}
	if got := gotHeaders.Get("If-None-Match"); got != "" {
		t.Errorf("expected no If-None-Match on a first fetch, got %q", got)
	}
}

func TestGitHubStarsProvider_BearerTokenSentOnlyWhenConfigured(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"stargazers_count": 1}`))
	}))
	defer srv.Close()

	defer SetGitHubAPIBaseForTest(srv.URL)()
	t.Setenv("MCPPROXY_GITHUB_TOKEN", "secret-token")
	p := NewGitHubStarsProvider(PopularityOptions{})
	defer p.Close()

	p.Resolve(context.Background(), []string{"o/r"}, time.Second)
	if gotAuth != "Bearer secret-token" {
		t.Fatalf("expected 'Bearer secret-token', got %q", gotAuth)
	}
	if p.budget.limit != githubRateLimitAuth {
		t.Fatalf("expected the authenticated rolling budget (%d) once a token is set, got %d", githubRateLimitAuth, p.budget.limit)
	}
}

func TestGitHubStarsProvider_Fetch304RevalidatesKeepsStars(t *testing.T) {
	var reqCount int32
	var gotIfNoneMatch string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&reqCount, 1)
		if n == 1 {
			w.Header().Set("ETag", `"v1"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"stargazers_count": 50}`))
			return
		}
		gotIfNoneMatch = r.Header.Get("If-None-Match")
		w.Header().Set("ETag", `"v1"`)
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	defer SetGitHubAPIBaseForTest(srv.URL)()
	p := NewGitHubStarsProvider(PopularityOptions{})
	defer p.Close()

	fixedNow := time.Now()
	p.now = func() time.Time { return fixedNow }

	p.Resolve(context.Background(), []string{"o/r"}, time.Second)
	if stars, state := p.Lookup("o/r"); state != LookupFresh || stars != 50 {
		t.Fatalf("after first fetch: expected Fresh/50, got state=%d stars=%d", state, stars)
	}

	// Advance the injected clock past the 24h positive TTL so the entry
	// reads Stale and gets re-enqueued.
	fixedNow = fixedNow.Add(25 * time.Hour)

	p.Resolve(context.Background(), []string{"o/r"}, time.Second)
	if gotIfNoneMatch != `"v1"` {
		t.Fatalf("expected If-None-Match %q on the revalidation request, got %q", `"v1"`, gotIfNoneMatch)
	}
	stars, state := p.Lookup("o/r")
	if state != LookupFresh || stars != 50 {
		t.Fatalf("after 304: expected stars preserved (Fresh/50), got state=%d stars=%d", state, stars)
	}
	if got := atomic.LoadInt32(&reqCount); got != 2 {
		t.Fatalf("expected exactly 2 requests, got %d", got)
	}
}

func TestGitHubStarsProvider_Fetch404IsNegative(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	defer SetGitHubAPIBaseForTest(srv.URL)()
	p := NewGitHubStarsProvider(PopularityOptions{})
	defer p.Close()

	p.Resolve(context.Background(), []string{"o/gone"}, time.Second)
	stars, state := p.Lookup("o/gone")
	if state != LookupNegative || stars != 0 {
		t.Fatalf("expected Negative/0 for a 404, got state=%d stars=%d", state, stars)
	}
}

func TestGitHubStarsProvider_Fetch500KeepsLastKnownStars(t *testing.T) {
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&reqCount, 1)
		if n == 1 {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"stargazers_count": 30}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	defer SetGitHubAPIBaseForTest(srv.URL)()
	p := NewGitHubStarsProvider(PopularityOptions{})
	defer p.Close()

	fixedNow := time.Now()
	p.now = func() time.Time { return fixedNow }

	p.Resolve(context.Background(), []string{"o/r"}, time.Second)
	if stars, state := p.Lookup("o/r"); state != LookupFresh || stars != 30 {
		t.Fatalf("after first fetch: expected Fresh/30, got state=%d stars=%d", state, stars)
	}

	fixedNow = fixedNow.Add(25 * time.Hour) // past the 24h positive TTL -> re-enqueued
	p.Resolve(context.Background(), []string{"o/r"}, time.Second)

	stars, state := p.Lookup("o/r")
	if stars != 30 {
		t.Fatalf("expected the last-known stars (30) preserved through a 500, got %d", stars)
	}
	// Freshly time-stamped by the failed refresh, within its own 1h error
	// TTL: still reads Fresh (it HAS a positive count).
	if state != LookupFresh {
		t.Fatalf("expected Fresh (last-known stars, revalidate time advanced), got state=%d", state)
	}
	if got := atomic.LoadInt32(&reqCount); got != 2 {
		t.Fatalf("expected exactly 2 requests, got %d", got)
	}
}

func TestGitHubStarsProvider_BodyCappedAt1MiB(t *testing.T) {
	padding := make([]byte, githubMaxBodyBytes+1000)
	for i := range padding {
		padding[i] = 'x'
	}
	// stargazers_count sits well past the 1 MiB cap, so it must never be read
	// if the cap is actually enforced.
	payload := append([]byte(`{"padding":"`), padding...)
	payload = append(payload, []byte(`","stargazers_count": 999999}`)...)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	defer SetGitHubAPIBaseForTest(srv.URL)()
	p := NewGitHubStarsProvider(PopularityOptions{})
	defer p.Close()

	p.Resolve(context.Background(), []string{"o/r"}, 2*time.Second)
	stars, _ := p.Lookup("o/r")
	if stars == 999999 {
		t.Fatal("expected the 1 MiB body cap to prevent stargazers_count from being read")
	}
}

func TestGitHubStarsProvider_FollowsSameHostRedirect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/old", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/repos/o/new", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/repos/o/new", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"stargazers_count": 42}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	defer SetGitHubAPIBaseForTest(srv.URL)()
	p := NewGitHubStarsProvider(PopularityOptions{})
	defer p.Close()

	p.Resolve(context.Background(), []string{"o/old"}, time.Second)
	stars, state := p.Lookup("o/old")
	if state != LookupFresh || stars != 42 {
		t.Fatalf("expected the same-host redirect to be followed, got state=%d stars=%d", state, stars)
	}
}

// --- T006: TTL/stale/requeue, dedup, overflow, concurrency, budget, breaker -

func TestGitHubStarsProvider_EnqueueDedupsSameKey(t *testing.T) {
	t.Setenv("MCPPROXY_CATALOG_POPULARITY", "false") // no workers; drive enqueue directly
	p := NewGitHubStarsProvider(PopularityOptions{})
	defer p.Close()

	ch1 := p.enqueue("o/r")
	ch2 := p.enqueue("o/r")
	if ch1 == nil || ch2 == nil {
		t.Fatal("expected both enqueue calls to be admitted")
	}
	if ch1 != ch2 {
		t.Fatal("expected the second enqueue of an already-queued key to share its completion channel")
	}
}

func TestGitHubStarsProvider_QueueOverflowDropsWithoutLeavingDedupEntry(t *testing.T) {
	t.Setenv("MCPPROXY_CATALOG_POPULARITY", "false") // no workers; queue never drains
	p := NewGitHubStarsProvider(PopularityOptions{})
	defer p.Close()

	for i := 0; i < githubQueueCap; i++ {
		p.queue <- fmt.Sprintf("filler/%d", i)
	}

	ch := p.enqueue("overflow/key")
	if ch != nil {
		t.Fatal("expected enqueue to return nil once the queue is full")
	}
	p.mu.Lock()
	_, tracked := p.queued["overflow/key"]
	p.mu.Unlock()
	if tracked {
		t.Fatal("expected the dropped key to leave no dedup entry behind (so it can be requested again)")
	}
}

func TestGitHubStarsProvider_ConcurrencyCappedAtFour(t *testing.T) {
	var active int32
	var mu sync.Mutex
	var maxActive int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&active, 1)
		mu.Lock()
		if n > maxActive {
			maxActive = n
		}
		mu.Unlock()
		time.Sleep(80 * time.Millisecond)
		atomic.AddInt32(&active, -1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"stargazers_count": 1}`))
	}))
	defer srv.Close()

	defer SetGitHubAPIBaseForTest(srv.URL)()
	p := NewGitHubStarsProvider(PopularityOptions{})
	defer p.Close()

	keys := make([]string, 0, 12)
	for i := 0; i < 12; i++ {
		keys = append(keys, fmt.Sprintf("owner/repo-%d", i))
	}
	p.Resolve(context.Background(), keys, 3*time.Second)

	mu.Lock()
	got := maxActive
	mu.Unlock()
	if got > githubMaxConcurrentFetches {
		t.Fatalf("observed %d concurrent fetches, want <= %d", got, githubMaxConcurrentFetches)
	}
	if got < 2 {
		t.Fatalf("expected some real concurrency (>=2 at once), got max=%d", got)
	}
	for _, k := range keys {
		if _, state := p.Lookup(k); state != LookupFresh {
			t.Errorf("expected %s to have been fetched (Fresh), got state=%d", k, state)
		}
	}
}

func TestRollingBudget_EnforcesLimitPerWindow(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	b := newRollingBudget(2)
	if !b.allow(now) {
		t.Fatal("expected the 1st request to be allowed")
	}
	if !b.allow(now) {
		t.Fatal("expected the 2nd request to be allowed")
	}
	if b.allow(now) {
		t.Fatal("expected the 3rd request within the same window to be blocked")
	}
	later := now.Add(61 * time.Minute)
	if !b.allow(later) {
		t.Fatal("expected a request to be allowed again once the window has rolled past")
	}
}

func TestGitHubStarsProvider_DefaultBudgetIsUnauthenticated(t *testing.T) {
	p := NewGitHubStarsProvider(PopularityOptions{})
	defer p.Close()
	if p.budget.limit != githubRateLimitUnauth {
		t.Fatalf("expected the unauthenticated budget (%d), got %d", githubRateLimitUnauth, p.budget.limit)
	}
}

// TestGitHubStarsProvider_BreakerPausesOn403 pins SC-004: a 403 carrying
// X-RateLimit-Remaining: 0 and a future reset pauses ALL further fetches
// until that reset, while stale cached values keep being served.
func TestGitHubStarsProvider_BreakerPausesOn403(t *testing.T) {
	var reqCount int32
	resetAt := time.Now().Add(2 * time.Hour) // far enough that test latency can't cross it
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&reqCount, 1)
		if n == 1 {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetAt.Unix(), 10))
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"stargazers_count": 1}`))
	}))
	defer srv.Close()

	defer SetGitHubAPIBaseForTest(srv.URL)()
	p := NewGitHubStarsProvider(PopularityOptions{})
	defer p.Close()

	p.Resolve(context.Background(), []string{"o/r1"}, 500*time.Millisecond)
	if got := atomic.LoadInt32(&reqCount); got != 1 {
		t.Fatalf("expected exactly 1 request before the breaker engaged, got %d", got)
	}

	// Seed a stale cached value to assert it is still served during the pause.
	p.mu.Lock()
	p.entries["o/stale"] = &starsEntry{Stars: 9, Status: http.StatusOK, FetchedAt: time.Now().Add(-48 * time.Hour)}
	p.mu.Unlock()

	p.Resolve(context.Background(), []string{"o/r2"}, 200*time.Millisecond)
	if got := atomic.LoadInt32(&reqCount); got != 1 {
		t.Fatalf("expected the breaker to block further requests before the reset, got %d total requests", got)
	}

	stars, state := p.Lookup("o/stale")
	if state != LookupStale || stars != 9 {
		t.Fatalf("expected the stale cached value to still be served during the pause, got stars=%d state=%d", stars, state)
	}
}

// --- T008: Resolve wait bounds ----------------------------------------------

func TestGitHubStarsProvider_ResolveReturnsAroundWait(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-block
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"stargazers_count": 5}`))
	}))
	defer func() {
		close(block)
		srv.Close()
	}()

	defer SetGitHubAPIBaseForTest(srv.URL)()
	p := NewGitHubStarsProvider(PopularityOptions{})
	defer p.Close()

	start := time.Now()
	p.Resolve(context.Background(), []string{"o/r"}, 100*time.Millisecond)
	elapsed := time.Since(start)
	if elapsed < 100*time.Millisecond {
		t.Fatalf("expected Resolve to wait at least 100ms, returned after %s", elapsed)
	}
	if elapsed > 800*time.Millisecond {
		t.Fatalf("expected Resolve to return promptly after its wait, took %s", elapsed)
	}
	if _, state := p.Lookup("o/r"); state != LookupAbsent {
		t.Fatalf("expected no result yet (stub still blocked), got state=%d", state)
	}
}

func TestGitHubStarsProvider_ResolveNeverOutlivesCtx(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-block
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"stargazers_count": 5}`))
	}))
	defer func() {
		close(block)
		srv.Close()
	}()

	defer SetGitHubAPIBaseForTest(srv.URL)()
	p := NewGitHubStarsProvider(PopularityOptions{})
	defer p.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	p.Resolve(ctx, []string{"o/r"}, 5*time.Second) // wait far longer than ctx
	if elapsed := time.Since(start); elapsed > 800*time.Millisecond {
		t.Fatalf("expected Resolve to return once ctx expired, took %s", elapsed)
	}
}

func TestGitHubStarsProvider_ResolveWaitZeroNeverBlocks(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-block
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"stargazers_count": 5}`))
	}))
	defer func() {
		close(block)
		srv.Close()
	}()

	defer SetGitHubAPIBaseForTest(srv.URL)()
	p := NewGitHubStarsProvider(PopularityOptions{})
	defer p.Close()

	start := time.Now()
	p.Resolve(context.Background(), []string{"o/r"}, 0)
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("expected wait=0 to return immediately without blocking, took %s", elapsed)
	}
}

func TestGitHubStarsProvider_FetchContinuesAfterResolveReturns(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-block
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"stargazers_count": 5}`))
	}))
	defer srv.Close()

	defer SetGitHubAPIBaseForTest(srv.URL)()
	p := NewGitHubStarsProvider(PopularityOptions{})
	defer p.Close()

	p.Resolve(context.Background(), []string{"o/r"}, 50*time.Millisecond) // returns before the stub answers
	if _, state := p.Lookup("o/r"); state != LookupAbsent {
		t.Fatalf("expected no result yet, got state=%d", state)
	}

	close(block) // let the stub answer now

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, state := p.Lookup("o/r"); state == LookupFresh {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected the background fetch to complete and land in the cache after Resolve returned")
}

// --- T009: kill switch -------------------------------------------------------

func TestPopularityFetchesEnabled(t *testing.T) {
	cases := map[string]bool{
		"":      true,
		"true":  true,
		"false": false,
		"0":     false,
		"off":   false,
		"OFF":   false,
		"False": false,
	}
	for v, want := range cases {
		t.Run(fmt.Sprintf("env=%q", v), func(t *testing.T) {
			t.Setenv("MCPPROXY_CATALOG_POPULARITY", v)
			if got := popularityFetchesEnabled(); got != want {
				t.Errorf("popularityFetchesEnabled() = %v, want %v", got, want)
			}
		})
	}
}

func TestGitHubStarsProvider_KillSwitchNeverFetches(t *testing.T) {
	var reqCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&reqCount, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"stargazers_count": 5}`))
	}))
	defer srv.Close()

	defer SetGitHubAPIBaseForTest(srv.URL)()
	t.Setenv("MCPPROXY_CATALOG_POPULARITY", "false")
	p := NewGitHubStarsProvider(PopularityOptions{})
	defer p.Close()

	p.Resolve(context.Background(), []string{"o/r"}, 200*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&reqCount); got != 0 {
		t.Fatalf("expected the kill switch to prevent any outbound request, got %d", got)
	}
}
