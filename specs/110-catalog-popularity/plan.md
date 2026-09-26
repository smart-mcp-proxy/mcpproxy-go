# Implementation Plan: Real Popularity Signal for the Catalog

**Branch**: `110-catalog-popularity` (stacked on `109-j-catalog-add-server`, PR #1383) | **Spec**: [spec.md](spec.md)

## Summary

This plan fills in `Popularity` from two real sources. Docker Hub `pull_count` comes free with the listing the Docker parser already fetches. GitHub `stargazers_count` is fetched through a cached, rate-limited provider that stays off the search's critical path. It also redefines the two landing sections so they differ: Official keeps source order, and Popular is ranked by the signal, drawn from the pool before truncation, and de-duplicated by repo.

## Technical Context

Go 1.26, backend-only for PR A. It uses existing dependencies only: `go.etcd.io/bbolt`, `zap`, and `net/http` via `sharedRegistryClient`. **No new dependencies.** Frontend (Vue) and Swift changes are limited to one display helper each (PR B).

## Research & Decisions

| # | Decision | Rationale | Rejected alternatives |
|---|---|---|---|
| R1 | **GitHub stars** as the primary signal | `SourceCodeURL` is already filled in for the official-registry and reference sources. MCP directories use it widely. It needs one unauthenticated REST call per repo. | npm downloads (only for npm-packaged stdio servers, and scoped packages cannot use the bulk endpoint). PyPI (pypistats is rate-limited and returns counts on a different scale). |
| R2 | **Docker `pull_count` → `Installs`**, never `Stars` | It costs nothing (already in the payload, verified live). Docker `star_count` is 9–58 while GitHub has ~80k, so mixing them would be meaningless. | Mapping Docker stars → `Stars` (different scale). |
| R3 | **Lexicographic `(stars, installs)`** comparison | The two signals cannot be compared directly. Any conversion factor would be made up, and a review would rightly reject it. A lexicographic order is pure and easy to explain. | A sum (the current code — pulls in the millions would swamp stars). Log-normalized blending (arbitrary weights). |
| R4 | **Own bbolt bucket, not `cache.Manager`** | `cache.Manager` has a fixed 2h TTL (not 6h), deletes expired records (so it cannot serve stale), and adds Spec 105 read_cache authorization frames that are irrelevant here. It would also inflate `read_cache` stats. A small dedicated bucket gets a 24h TTL, serve-stale and ETag storage. | Reusing `cache.Manager` (2h churn × 60/h budget means it never converges). A JSON file in the data dir (another persistence path to maintain, while bbolt is already open). |
| R5 | **Cache-first, with a bounded wait (800 ms) and background completion** | Warm lookups cost 0 ms. A cold landing still gets most of its stars on the first view, and the rest arrive by the next view. The 5 s per-source budget is untouched. | Fully synchronous (adds GitHub latency to every search). Fully async with no wait (the first landing always has an empty Popular). A startup prewarm job (another lifecycle component — deferred; R5 covers most of the value). |
| R6 | **Rolling budget of 50/h unauthenticated + header-driven circuit breaker** | 60/h is per IP, so 10 are left for the user's own unauthenticated `gh`/browser use. `X-RateLimit-Remaining`/`Reset` are authoritative (verified live: `x-ratelimit-limit: 60`, `x-ratelimit-reset`). | A token bucket (more code, and the headers already give the exact state). |
| R7 | **`MCPPROXY_GITHUB_TOKEN`, not `GITHUB_TOKEN`** | Least surprise: a developer's `GITHUB_TOKEN` is often a broad-scope CI or PAT token, so it has to be opted in explicitly. It is only ever sent to the constant host. | Reading `GITHUB_TOKEN`. A config field (5 wiring points; deferred until someone asks). |
| R8 | **Official section = source-native order** | Every default source is official (so all default hits are official), which means rank order would already be popularity order and the sections would be identical by construction. Source order keeps each registry's own curation (the reference list is hand-ordered). | Alphabetical (arbitrary, starts at "a…"). Excluding Popular items from Official (hides official servers). |
| R9 | **Popular: one hit per repo** | The 7 reference servers share `modelcontextprotocol/servers` and would otherwise take 7 of the 12 slots. | Counting no stars for monorepo subpaths (drops genuinely popular servers). |

## Data Model

```go
// types.go
type ServerEntry struct { …; Popularity *Popularity `json:"-"` } // FR-001: source-native, never marshalled

// popularity.go
type PopularityProvider interface {
    // Lookup returns the cached stars for a repo key without I/O. stale=true
    // means past TTL (still usable); ok=false means never fetched / negative.
    Lookup(key string) (stars int, state LookupState) // Fresh | Stale | Negative | Absent
    // Resolve enqueues misses/stale keys (in priority order) and waits up to
    // wait (bounded by ctx) for them; fetching continues after it returns.
    Resolve(ctx context.Context, keys []string, wait time.Duration)
}

// popularity_github.go
type githubStarsProvider struct {
    mu        sync.Mutex
    entries   map[string]*starsEntry  // memory front
    store     starsStore              // nil = memory only; bbolt impl
    queue     chan string             // cap 256
    queued    map[string]chan struct{} // dedup + completion signal
    budget    rollingBudget           // 50/h or 4000/h
    pausedTil time.Time               // breaker
    token     string
    baseURL   string                  // const; test hook overrides
    now       func() time.Time        // test clock
}
type starsEntry struct {
    Stars     int       `json:"stars"`
    ETag      string    `json:"etag,omitempty"`
    FetchedAt time.Time `json:"fetched_at"`
    Status    int       `json:"status"` // 200/304 ok, 404/451 negative, else error
}
```

bbolt bucket `catalog_popularity`: key = `o/r` (lower-case), value = JSON `starsEntry`. Entries are loaded into memory lazily on the first `Lookup` miss. Writes go through synchronously after each fetch (they are rare, ≤50/h). A new key over the cap evicts the oldest `FetchedAt`.

## Flow

```
SearchAll(q)
  fan-out (5s/source) ─► BuildCatalogHit: hit.Popularity = entry.Popularity (docker pulls)
                                           + cache-only Lookup(GitHubRepoKey(entry.SourceCodeURL))
  merge + dedup + source filter ─► pool            // merge order = source order
  official := copy of official hits in pool, merge order   // BEFORE any sort (FR-005)
  sort pool by Rank; keys := repo keys (rank order) whose Lookup is stale/absent
  provider.Resolve(ctx, keys, opts.PopularityWait)   // ctx bounds the wait only; workers use provider ctx
  re-apply Lookup to pool + official hits; re-sort pool by Rank
  sections = {Official: official[:12], Popular: popular(pool)}   // BEFORE truncation
  results  = pool[:limit]
```

## Wiring

| Site | Change |
|---|---|
| `internal/runtime/runtime.go` (near `cache.NewManager`, ~L290) | `registries.SetPopularityProvider(registries.NewGitHubStarsProvider(registries.PopularityOptions{DB: storageManager.GetDB(), Logger: logger}))` unless the kill switch is set. `Close()` on shutdown stops the workers. |
| `cmd/mcpproxy/catalog_cmd.go` `catalogSearch` in-process fallback and `catalog show` | Memory-only provider (`DB: nil`). |
| `internal/registries/testhooks.go` | `SetPopularityProviderForTest`, `SetGitHubAPIBaseForTest`. |
| E2E | `scripts/test-api-e2e.sh` exports `MCPPROXY_CATALOG_POPULARITY=false`, so E2E never calls api.github.com. |

## PR slicing

- **PR A (backend, this branch)**: FR-001…FR-011, SC-001…SC-005. Base `109-j-catalog-add-server`.
- **PR B (display)**: FR-012. Web UI `CatalogSearch.vue` badge + vitest, Swift `CatalogView` badge + `CatalogTests`, CLI table column. Stacked on A.

## Risks

- **Stacking on an open PR.** 109-j is still in review, and round 5 made local-only changes (`16f9e8c9c` is not pushed). PR A touches `catalog.go` `SearchAll`/`buildSections`, so it may conflict when 109-j's later rounds land. Mitigation: keep catalog.go edits confined to those two functions plus `BuildCatalogHit`/`Rank`, and rebase once 109-j merges.
- **Section semantics change** (Official is no longer popularity-sorted). This is an intentional amendment of 109 FR-060. Existing 109 tests that assert Official order must be updated to source order, not deleted.

## Review log

- **zcode round 1 (spec, 2026-09-26)**: 9 findings, all verified and folded into FR-005/006/007/008/009/011 and the flow above. (1) Official must be built from the pre-sort merge order. (2) The per-source cap limits the pool, so empty q now fans out at 50/source, and the Docker single page is a documented limitation. (3) Background fetch uses the provider ctx, not the request ctx. (4) Overflow must not leave dedup entries behind. (5) No wait while paused or over budget. (6) Errors keep the last-known stars. (7) Lookup distinguishes negative from absent. (8) Own request, 10 s ctx timeout, never `registryGet`, no retries. (9) Kill switch lives in the constructor.
