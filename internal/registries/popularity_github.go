package registries

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.etcd.io/bbolt"
	"go.uber.org/zap"
)

const (
	githubAPIBaseURLDefault = "https://api.github.com"

	githubMaxConcurrentFetches = 4    // FR-009(a)
	githubQueueCap             = 256  // FR-009(b)
	githubMaxCacheKeys         = 5000 // FR-008
	githubRequestTimeout       = 10 * time.Second
	githubMaxBodyBytes         = 1 << 20 // 1 MiB (FR-006)

	githubFreshTTL     = 24 * time.Hour // FR-008: 200/304 and 404/451
	githubErrorTTL     = 1 * time.Hour  // FR-008: any other error
	githubBreakerPause = 60 * time.Second

	githubRateLimitUnauth = 50   // FR-009(c): requests/hour without a token
	githubRateLimitAuth   = 4000 // FR-009(c): requests/hour with a token

	githubLowRemainingThreshold = 5 // FR-009(d)
)

// githubAPIBaseOverride lets SetGitHubAPIBaseForTest point new providers at
// an httptest.Server; production providers always read the compile-time
// default (FR-006: "overridable only by a test hook").
var githubAPIBaseOverride atomic.Pointer[string]

func currentGitHubAPIBase() string {
	if p := githubAPIBaseOverride.Load(); p != nil {
		return *p
	}
	return githubAPIBaseURLDefault
}

// starsEntry is the cached record for one GitHub repo key (plan.md data
// model). Persisted as JSON in popularityBucketName.
type starsEntry struct {
	Stars     int       `json:"stars"`
	ETag      string    `json:"etag,omitempty"`
	FetchedAt time.Time `json:"fetched_at"`
	Status    int       `json:"status"` // 200/304 ok, 404/451 negative, else the last error status (0 = transport error)
}

func (e *starsEntry) ok() bool {
	return e.Status == http.StatusOK || e.Status == http.StatusNotModified
}

func (e *starsEntry) negative() bool {
	return e.Status == http.StatusNotFound || e.Status == http.StatusUnavailableForLegalReasons
}

// ttl is how long e stays fresh (FR-008): 24h for a confirmed answer
// (positive OR negative), 1h for anything else (transport error, 5xx, 403,
// 429 — the breaker, not this TTL, is what actually throttles those).
func (e *starsEntry) ttl() time.Duration {
	if e.ok() || e.negative() {
		return githubFreshTTL
	}
	return githubErrorTTL
}

// PopularityOptions configures NewGitHubStarsProvider.
type PopularityOptions struct {
	// DB is the bbolt database to persist the cache to. nil means memory
	// only (the CLI in-process fallback — plan.md wiring table).
	DB *bbolt.DB
	// Logger receives best-effort diagnostics (store errors, etc). A nil
	// Logger falls back to zap.NewNop().
	Logger *zap.Logger
}

// rollingBudget is a simple sliding-window request counter (FR-009c): at
// most `limit` `allow` calls may return true within any trailing `window`.
type rollingBudget struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   []time.Time
}

func newRollingBudget(limit int) *rollingBudget {
	return &rollingBudget{limit: limit, window: time.Hour}
}

func (b *rollingBudget) prune(now time.Time) {
	cutoff := now.Add(-b.window)
	kept := b.hits[:0]
	for _, t := range b.hits {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	b.hits = kept
}

// hasCapacity reports whether a request could be spent right now, without
// consuming any budget (a peek, used by Resolve's fast bail-out).
func (b *rollingBudget) hasCapacity(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prune(now)
	return len(b.hits) < b.limit
}

// allow is the authoritative, budget-consuming check made by a worker right
// before it actually issues a request.
func (b *rollingBudget) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prune(now)
	if len(b.hits) >= b.limit {
		return false
	}
	b.hits = append(b.hits, now)
	return true
}

// githubStarsProvider is the production PopularityProvider (plan.md data
// model): a cached, rate-limited GitHub stargazers_count fetcher behind a
// fixed 4-worker pool.
type githubStarsProvider struct {
	mu      sync.Mutex
	entries map[string]*starsEntry
	store   *popularityStore // nil = memory only

	queue  chan string
	queued map[string]chan struct{} // in-flight+queued dedup; closed on completion

	logger   *zap.Logger
	token    string
	disabled bool // FR-011 kill switch, read once at construction
	baseURL  string

	now func() time.Time // injected clock (tests)

	budget    *rollingBudget
	pausedMu  sync.Mutex
	pausedTil time.Time

	// baseCtx is the provider's OWN context for background fetches — never
	// the caller's Resolve ctx (zcode review finding 3). Cancelled by Close.
	baseCtx context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	closeOnce sync.Once
}

var _ PopularityProvider = (*githubStarsProvider)(nil)

// popularityFetchesEnabled reads the FR-011 kill switch. Read once, inside
// NewGitHubStarsProvider (zcode review finding 9: "the switch is read inside
// the provider constructor, which then returns a no-op provider, so the
// runtime and CLI call sites both honour it").
func popularityFetchesEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MCPPROXY_CATALOG_POPULARITY"))) {
	case "false", "0", "off":
		return false
	default:
		return true
	}
}

// NewGitHubStarsProvider constructs the production PopularityProvider. When
// the FR-011 kill switch is set, the returned provider still answers Lookup
// from whatever is already cached (e.g. a bbolt store from before the switch
// was flipped) but starts no worker goroutines and Resolve is a no-op — a
// caller never needs to branch on the switch itself.
func NewGitHubStarsProvider(opts PopularityOptions) *githubStarsProvider {
	logger := opts.Logger
	if logger == nil {
		logger = zap.NewNop()
	}

	token := os.Getenv("MCPPROXY_GITHUB_TOKEN") // FR-011: never the generic GITHUB_TOKEN
	limit := githubRateLimitUnauth
	if token != "" {
		limit = githubRateLimitAuth
	}

	var store *popularityStore
	if opts.DB != nil {
		s, err := newPopularityStore(opts.DB)
		if err != nil {
			logger.Warn("catalog popularity: failed to open bbolt bucket; continuing memory-only", zap.Error(err))
		} else {
			store = s
		}
	}

	baseCtx, cancel := context.WithCancel(context.Background())
	p := &githubStarsProvider{
		entries:  make(map[string]*starsEntry),
		store:    store,
		queue:    make(chan string, githubQueueCap),
		queued:   make(map[string]chan struct{}),
		logger:   logger,
		token:    token,
		disabled: !popularityFetchesEnabled(),
		baseURL:  currentGitHubAPIBase(),
		now:      time.Now,
		budget:   newRollingBudget(limit),
		baseCtx:  baseCtx,
		cancel:   cancel,
	}
	if store != nil {
		p.entries = store.all()
		p.mu.Lock()
		for len(p.entries) > githubMaxCacheKeys {
			p.evictIfNeededLocked()
		}
		p.mu.Unlock()
	}
	if !p.disabled {
		p.startWorkers()
	}
	return p
}

// Close stops all background fetch workers and returns immediately if
// already closed. It satisfies io.Closer so callers (internal/runtime) can
// hold the provider as an io.Closer without naming this unexported type.
func (p *githubStarsProvider) Close() error {
	p.closeOnce.Do(func() {
		p.cancel()
		p.wg.Wait()
	})
	return nil
}

func (p *githubStarsProvider) startWorkers() {
	for i := 0; i < githubMaxConcurrentFetches; i++ {
		p.wg.Add(1)
		go p.worker()
	}
}

func (p *githubStarsProvider) worker() {
	defer p.wg.Done()
	for {
		select {
		case <-p.baseCtx.Done():
			return
		case key, ok := <-p.queue:
			if !ok {
				return
			}
			p.fetchAndStore(key)
		}
	}
}

// fresh reports whether e is still within its TTL as of p.now().
func (p *githubStarsProvider) fresh(e *starsEntry) bool {
	return p.now().Sub(e.FetchedAt) < e.ttl()
}

// getEntryLocked returns key's entry, lazily loading it from the store on a
// memory miss (plan.md: "Entries are loaded into memory lazily on the first
// Lookup miss"). Caller must hold p.mu.
func (p *githubStarsProvider) getEntryLocked(key string) *starsEntry {
	if e, ok := p.entries[key]; ok {
		return e
	}
	if p.store != nil {
		if e, ok := p.store.get(key); ok {
			p.entries[key] = e
			return e
		}
	}
	return nil
}

// Lookup implements PopularityProvider.
func (p *githubStarsProvider) Lookup(key string) (int, LookupState) {
	p.mu.Lock()
	e := p.getEntryLocked(key)
	p.mu.Unlock()
	if e == nil {
		return 0, LookupAbsent
	}
	return p.classify(e)
}

// classify maps an entry to its LookupState (FR-008, zcode review finding
// 7). Whether stars are displayable depends only on whether there IS a
// positive count (e.Stars > 0) and whether it's still fresh — this covers a
// successful fetch, and also a since-erroring refresh that still carries a
// last-known-good count forward (FR-008's "keep the last-known stars").
func (p *githubStarsProvider) classify(e *starsEntry) (int, LookupState) {
	fresh := p.fresh(e)
	if e.Stars > 0 {
		if fresh {
			return e.Stars, LookupFresh
		}
		return e.Stars, LookupStale
	}
	// No signal to show right now: either a confirmed 404/451, or an entry
	// that has never successfully resolved (including one currently backing
	// off after an error, within its own shorter TTL).
	if fresh {
		return 0, LookupNegative
	}
	return 0, LookupAbsent
}

// Resolve implements PopularityProvider.
func (p *githubStarsProvider) Resolve(ctx context.Context, keys []string, wait time.Duration) {
	if p.disabled || len(keys) == 0 {
		return
	}

	// zcode review finding 5: bail out immediately, without enqueueing
	// anything, when we already know no fetch will be attempted right now.
	now := p.now()
	if p.pausedNow(now) || !p.budget.hasCapacity(now) {
		return
	}

	var waiters []<-chan struct{}
	for _, key := range keys {
		if ch := p.enqueue(key); ch != nil {
			waiters = append(waiters, ch)
		}
	}
	if len(waiters) == 0 || wait <= 0 {
		return
	}

	waitCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	for _, ch := range waiters {
		select {
		case <-ch:
		case <-waitCtx.Done():
			return
		}
	}
}

// enqueue admits key to the fetch queue, returning a channel closed once the
// fetch (or a no-op drop) completes — or nil if key needs no fetch (already
// fresh), is already queued/in-flight (its existing completion channel is
// returned instead, so callers share it rather than double-fetching), or the
// queue is full (FR-009b: overflow is simply dropped — no dedup entry is
// created, so nothing waits on it and the next search can request it again).
func (p *githubStarsProvider) enqueue(key string) <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()

	if e := p.getEntryLocked(key); e != nil && p.fresh(e) {
		return nil
	}
	if ch, ok := p.queued[key]; ok {
		return ch
	}
	select {
	case p.queue <- key:
		ch := make(chan struct{})
		p.queued[key] = ch
		return ch
	default:
		return nil
	}
}

// complete removes key from the in-flight/queued dedup set and closes its
// completion channel, waking any Resolve callers waiting on it.
func (p *githubStarsProvider) complete(key string) {
	p.mu.Lock()
	ch, ok := p.queued[key]
	delete(p.queued, key)
	p.mu.Unlock()
	if ok {
		close(ch)
	}
}

func (p *githubStarsProvider) pausedNow(now time.Time) bool {
	p.pausedMu.Lock()
	defer p.pausedMu.Unlock()
	return now.Before(p.pausedTil)
}

// pauseUntil extends the breaker pause to at least until, never shortening
// an existing longer pause.
func (p *githubStarsProvider) pauseUntil(until time.Time) {
	p.pausedMu.Lock()
	defer p.pausedMu.Unlock()
	if until.After(p.pausedTil) {
		p.pausedTil = until
	}
}

// fetchAndStore fetches one key (or drops it, if paused/over budget) and
// always completes it. It runs on p.baseCtx — a context owned by the
// provider, detached from whatever Resolve call originally enqueued the key
// (zcode review finding 3), so the fetch outlives that call.
func (p *githubStarsProvider) fetchAndStore(key string) {
	defer p.complete(key)

	now := p.now()
	if p.pausedNow(now) {
		return // breaker engaged: retried on a later Resolve call
	}
	if !p.budget.allow(now) {
		return // rolling budget exhausted: retried on a later Resolve call
	}

	owner, repo, ok := splitRepoKey(key)
	if !ok {
		return
	}

	p.mu.Lock()
	prev := p.getEntryLocked(key)
	p.mu.Unlock()

	ctx, cancel := context.WithTimeout(p.baseCtx, githubRequestTimeout)
	defer cancel()

	status, stars, etag, headers, fetchErr := p.doRequest(ctx, owner, repo, prev)
	p.applyResult(key, prev, status, stars, etag, headers, fetchErr)
}

// rateLimitHeaders is the subset of GitHub's response headers the breaker
// (FR-009d) reads.
type rateLimitHeaders struct {
	remaining  string
	reset      string
	retryAfter string
}

// doRequest issues a single, non-retrying GET against p.baseURL (FR-006:
// "There are no retries; the breaker is the only backoff" — this
// deliberately does NOT use registryGet, which pins to a *RegistryEntry and
// retries 429/5xx three times, burning budget against a rate-limited API).
func (p *githubStarsProvider) doRequest(ctx context.Context, owner, repo string, prev *starsEntry) (status, stars int, etag string, headers rateLimitHeaders, err error) {
	reqURL := p.baseURL + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)

	u, parseErr := url.Parse(reqURL)
	if parseErr != nil {
		return 0, 0, "", headers, parseErr
	}
	if blockErr := hostLiteralBlocked(u.Host, registryAllowPrivateFetch.Load()); blockErr != nil {
		return 0, 0, "", headers, blockErr
	}
	if guardErr := guardRegistryTargetHost(ctx, reqURL); guardErr != nil {
		return 0, 0, "", headers, guardErr
	}

	req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, http.NoBody)
	if reqErr != nil {
		return 0, 0, "", headers, reqErr
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", registryUserAgent())
	if prev != nil && prev.ETag != "" {
		req.Header.Set("If-None-Match", prev.ETag)
	}
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}

	resp, doErr := sharedRegistryClient().Do(req)
	if doErr != nil {
		return 0, 0, "", headers, doErr
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, githubMaxBodyBytes+1))
	if readErr != nil {
		return 0, 0, "", headers, readErr
	}

	headers = rateLimitHeaders{
		remaining:  resp.Header.Get("X-RateLimit-Remaining"),
		reset:      resp.Header.Get("X-RateLimit-Reset"),
		retryAfter: resp.Header.Get("Retry-After"),
	}

	switch resp.StatusCode {
	case http.StatusOK:
		var payload struct {
			StargazersCount int `json:"stargazers_count"`
		}
		_ = json.Unmarshal(body, &payload)
		return resp.StatusCode, payload.StargazersCount, resp.Header.Get("ETag"), headers, nil
	case http.StatusNotModified:
		s := 0
		if prev != nil {
			s = prev.Stars
		}
		etag := resp.Header.Get("ETag")
		if etag == "" && prev != nil {
			etag = prev.ETag
		}
		return resp.StatusCode, s, etag, headers, nil
	default:
		return resp.StatusCode, 0, "", headers, nil
	}
}

// applyResult stores the outcome of one fetch attempt (FR-008: a failed
// refresh keeps the last-known stars/etag and only advances FetchedAt; only
// a definitive 404/451 clears the stars) and, on an actual HTTP response
// (fetchErr == nil), updates the breaker from its rate-limit headers.
func (p *githubStarsProvider) applyResult(key string, prev *starsEntry, status, stars int, etag string, headers rateLimitHeaders, fetchErr error) {
	now := p.now()
	entry := &starsEntry{FetchedAt: now, Status: status}

	switch {
	case fetchErr != nil:
		entry.Status = 0
		if prev != nil {
			entry.Stars, entry.ETag = prev.Stars, prev.ETag
		}
	case status == http.StatusOK:
		entry.Stars = stars
		entry.ETag = etag
	case status == http.StatusNotModified:
		entry.Stars = stars
		entry.ETag = etag
	case status == http.StatusNotFound || status == http.StatusUnavailableForLegalReasons:
		// Definitive negative: Stars stays 0 regardless of what prev had —
		// the repo really is gone/unavailable now.
	default:
		// Any other status (5xx, 403, 429, …): not definitive — keep the
		// last-known stars/etag, just advance the (short, 1h) revalidate
		// time via FetchedAt above.
		if prev != nil {
			entry.Stars, entry.ETag = prev.Stars, prev.ETag
		}
	}

	p.mu.Lock()
	p.entries[key] = entry
	p.evictIfNeededLocked()
	p.mu.Unlock()

	if p.store != nil {
		if err := p.store.put(key, entry); err != nil {
			p.logger.Warn("catalog popularity: failed to persist entry", zap.String("key", key), zap.Error(err))
		}
	}

	if fetchErr == nil {
		p.applyBreaker(status, headers)
	}
}

// evictIfNeededLocked drops the single oldest-FetchedAt entry once the cache
// exceeds its cap (FR-008). Caller must hold p.mu. Only ever one entry over
// cap at a time, since insertion happens one key at a time.
func (p *githubStarsProvider) evictIfNeededLocked() {
	if len(p.entries) <= githubMaxCacheKeys {
		return
	}
	oldestKey := ""
	var oldestTime time.Time
	first := true
	for k, e := range p.entries {
		if first || e.FetchedAt.Before(oldestTime) {
			oldestKey, oldestTime, first = k, e.FetchedAt, false
		}
	}
	if oldestKey == "" {
		return
	}
	delete(p.entries, oldestKey)
	if p.store != nil {
		if err := p.store.delete(oldestKey); err != nil {
			p.logger.Warn("catalog popularity: failed to evict entry", zap.String("key", oldestKey), zap.Error(err))
		}
	}
}

// applyBreaker updates the pause window from one response's rate-limit
// headers (FR-009d): a 403/429 (Retry-After, else the reset time, else a 60s
// default) or ANY response reporting X-RateLimit-Remaining <= 5 (the reset
// time, else 60s).
func (p *githubStarsProvider) applyBreaker(status int, headers rateLimitHeaders) {
	now := p.now()

	if status == http.StatusForbidden || status == http.StatusTooManyRequests {
		if until, ok := parseRetryAfter(headers.retryAfter, now); ok {
			p.pauseUntil(until)
			return
		}
		if until, ok := parseUnixSeconds(headers.reset); ok {
			p.pauseUntil(until)
			return
		}
		p.pauseUntil(now.Add(githubBreakerPause))
		return
	}

	if remaining, ok := parseNonNegativeInt(headers.remaining); ok && remaining <= githubLowRemainingThreshold {
		if until, ok := parseUnixSeconds(headers.reset); ok {
			p.pauseUntil(until)
			return
		}
		p.pauseUntil(now.Add(githubBreakerPause))
	}
}

func splitRepoKey(key string) (owner, repo string, ok bool) {
	i := strings.IndexByte(key, '/')
	if i <= 0 || i == len(key)-1 {
		return "", "", false
	}
	return key[:i], key[i+1:], true
}

func parseNonNegativeInt(v string) (int, bool) {
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func parseUnixSeconds(v string) (time.Time, bool) {
	if v == "" {
		return time.Time{}, false
	}
	sec, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(sec, 0), true
}

// parseRetryAfter supports both Retry-After forms (RFC 9110): an integer
// number of seconds, or an HTTP-date.
func parseRetryAfter(v string, now time.Time) (time.Time, bool) {
	if v == "" {
		return time.Time{}, false
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return now.Add(time.Duration(secs) * time.Second), true
	}
	if t, err := http.ParseTime(v); err == nil {
		return t, true
	}
	return time.Time{}, false
}
