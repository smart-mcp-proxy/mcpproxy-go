// Spec 110 (catalog popularity signal): a PopularityProvider resolves GitHub
// star counts for repo keys (GitHubRepoKey), backed by a cached,
// rate-limited fetcher (githubStarsProvider is the production
// implementation — see popularity_github.go). It is installed process-wide
// via SetPopularityProvider (FR-010) so BuildCatalogHit and SearchAll can
// reach it without threading a dependency through every call site.
package registries

import (
	"context"
	"sync"
	"time"
)

// LookupState is PopularityProvider.Lookup's classification of a cached
// entry (Spec 110 FR-008, zcode review round 1 finding 7):
//
//   - LookupFresh: a positive signal (stars) within its TTL. Display it;
//     no fetch needed.
//   - LookupStale: a positive signal past its TTL. Still display it
//     (stale-while-revalidate), but it needs a refresh.
//   - LookupNegative: no signal to display, and still within its TTL —
//     either a confirmed 404/451, or a transient-error entry backing off
//     within its own (shorter) TTL. Not re-fetched yet either way.
//   - LookupAbsent: no signal, and free to (re)fetch now — never seen, or
//     past a negative/error entry's TTL.
type LookupState int

const (
	LookupAbsent LookupState = iota
	LookupFresh
	LookupStale
	LookupNegative
)

// PopularityProvider is the seam BuildCatalogHit and SearchAll use to read
// and refresh a GitHub repo's star count.
type PopularityProvider interface {
	// Lookup returns the cached star count for key without any I/O. See
	// LookupState for what each state means; only Fresh and Stale carry a
	// meaningful `stars` value.
	Lookup(key string) (stars int, state LookupState)

	// Resolve enqueues any of keys that are Stale or Absent for a background
	// fetch (de-duplicated against what's already queued/in-flight) and
	// waits up to wait — bounded by ctx — for them to land. Fetching
	// continues after Resolve returns even if the wait/ctx expired first.
	// Resolve returns immediately, without enqueueing anything, when the
	// provider is breaker-paused, its rolling budget is exhausted, or none of
	// the requested keys were admitted (e.g. all already fresh, or the queue
	// was full) — see popularity_github.go.
	Resolve(ctx context.Context, keys []string, wait time.Duration)
}

var (
	popularityProviderMu  sync.RWMutex
	popularityProviderVal PopularityProvider
)

// SetPopularityProvider installs the process-wide popularity provider
// (FR-010). nil disables all popularity lookups: BuildCatalogHit adds no
// stars and SearchAll enqueues no fetches — Docker's source-native Installs
// signal is unaffected either way (it never goes through this seam).
func SetPopularityProvider(p PopularityProvider) {
	popularityProviderMu.Lock()
	defer popularityProviderMu.Unlock()
	popularityProviderVal = p
}

// getPopularityProvider returns the currently installed provider, or nil.
func getPopularityProvider() PopularityProvider {
	popularityProviderMu.RLock()
	defer popularityProviderMu.RUnlock()
	return popularityProviderVal
}
