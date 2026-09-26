# Feature Specification: Real Popularity Signal for the Catalog

**Feature Branch**: `110-catalog-popularity`
**Created**: 2026-09-26
**Status**: Draft
**Input**: User description: "The Popularity struct and CatalogHit.Popularity are declared and consumed by Rank()'s popularity tiebreak and buildSections' Popular landing section, but no production code path ever populates them. The FR-060 tiebreak always compares 0-vs-0 and the empty-query Popular section renders identically to Official. Spec and implement a real popularity signal: pick a concrete source (GitHub stars via SourceCodeURL), design fetch + cache + rate-limit handling, wire it into BuildCatalogHit, and add tests proving Popular differs meaningfully from Official once real data exists."

**Related**: Spec 109 (UX navigation consistency) FR-060 / FR-061 and data-model §9, PR #1383 (`109-j-catalog-add-server`, the branch this stacks on). The finding was raised and deferred twice in 109-j review rounds (1 and 5) as out of scope for a review fix. This spec amends 109 FR-060's definition of the landing sections. The rest of 109 is unchanged.

## Context & Motivation

These facts were checked at `origin/109-j-catalog-add-server` `b1ddae539`:

1. **Nothing sets `Popularity`.** `BuildCatalogHit` (`internal/registries/catalog.go:227`) does not set it. `ServerEntry` (`types.go`) has no field for it, and none of the parsers (`official.go`, `reference.go`, `search.go` `parseDocker`/`parsePulse…`) read one. Every `popularityScore` is therefore 0.
2. **The Popular section is built from a truncated list.** `SearchAll` truncates the ranked pool to `limit` *before* it calls `buildSections(all)` (`catalog.go:190-199`). `Rank` sorts official hits first, so once there are `limit` or more official hits, Popular is a reordering of the same hits Official already shows. Real stars alone would not change that.
3. **Every default source is official, so the two sections cannot differ.** All three default sources (`official`, `reference`, `docker-mcp-catalog`, `internal/config/config.go:1664`) have `Provenance: official`, which means every default hit has `Official = Verified = true`. `Rank`'s third key is popularity, so the Official section ("official hits in rank order") is *already ordered by popularity*. On a default install, Popular would equal Official in both membership and order even with correct star counts.
4. **The data exists at no cost for one source.** Docker Hub's `GET /v2/repositories/mcp/` response, which `parseDocker` already fetches, includes `pull_count` for every image (checked live 2026-09-26: `mcp/fetch` 1,863,661 pulls; 245 images). The parser throws it away.
5. **Most other hits have a GitHub URL.** Official-registry entries carry `repository.url` → `SourceCodeURL` (`official.go:245`), and every reference server links into `github.com/modelcontextprotocol/servers`. GitHub's REST API (`GET /repos/{owner}/{repo}` → `stargazers_count`) allows **60 requests/hour per IP without a token** and 5,000 with one.
6. **The suggested cache does not fit.** `cache.Manager` (`internal/cache/manager.go`) stores `read_cache` tool responses. Its TTL is a fixed `DefaultTTL = 2h`, not 6h, and cannot be set per record. Its records carry Spec 105 authorization frames, and its cleanup sweep deletes expired entries. Refreshing every star count every 2h would exceed the unauthenticated budget with ~120 repos, and deleting expired entries rules out serve-stale.
7. **No surface shows the numbers yet.** The Web UI (`CatalogSearch.vue`), macOS (`CatalogView.swift`) and the CLI table do not render `popularity`, even though 109 FR-061 says each result shows "stars or installs when the source provides them".

## Scope Boundary

| Already exists | Reused, not rebuilt |
|---|---|
| `Popularity{Stars,Installs}`, `CatalogHit.Popularity`, `CatalogResult.Popularity`, the MCP `mcpCatalogServerEntry.Popularity`, TS `CatalogPopularity`, Swift `CatalogPopularity` | Types and wire shape unchanged (`popularity: {stars?, installs?}`, omitempty) |
| `Rank` (FR-060 ordering) | Keys unchanged. Only the popularity comparison changes from "sum" to "stars, then installs" (FR-004) |
| Registry SSRF-hardened client (`sharedRegistryClient`: dial-time private-IP block, redirect host pin, body cap) | Used for the GitHub fetch as is |
| `registries` package-level wiring (`SetVersion`, `SetRegistriesFromConfig`, `SetRegistriesForTest`) | The popularity provider is installed the same way (FR-010) |
| BBolt `config.db` via `storageManager.GetDB()` | A new bucket `catalog_popularity`. `cache.Manager` is not reused (fact 6) |

**Out of scope**: npm/PyPI download counts (a follow-up could add them as more `Installs` sources), GitHub GraphQL batching (requires a token), a config-file knob (env only in v1, see FR-011), telemetry, and changes to `Rank`'s key order.

## User Scenarios & Testing

### User Story 1 — Popular means popular (Priority: P1)

A user opens the Catalog with no query and sees an **Official** section (what the official sources list, in their own order) and a **Popular** section (the most-starred or most-pulled servers across every source). The two sections are visibly different lists.

**Independent test**: use fixture sources with ≥12 official hits and known star/pull counts. Popular should be ordered by popularity, should include a high-star hit that falls outside Official's first 12, and should differ from Official in membership or order.

1. **Given** real popularity for at least one hit, **When** q is empty, **Then** Popular contains only hits with a known signal, ordered stars desc, then installs desc, then `Rank`. It is drawn from the full merged pool before `limit` truncation, has at most one hit per GitHub repository, and is capped at 12.
2. **Given** no popularity is known for any hit (cold cache, GitHub unreachable, no Docker source), **When** q is empty, **Then** Popular is **empty**, not padded with zero-popularity hits, and each surface hides the empty section (Web UI and macOS already do).
3. **Given** q is empty, **Then** Official lists official-source hits in **source order**: registry list order, then each source's native order. It does not re-rank by popularity and is capped at 12.

### User Story 2 — Popularity breaks ties in search results (Priority: P1)

**Given** the query "github" matches several official hits, **Then** hits with more stars rank above those with fewer (FR-060 key 3), and the returned `popularity` shows the numbers.

### User Story 3 — It never makes the catalog slower or flakier (Priority: P1)

1. **Given** GitHub is slow or down, **Then** `SearchAll` returns within its existing budget plus at most the popularity wait (FR-007), and the affected hits simply have no stars. GitHub is never listed in `unavailable[]`, because it is not a catalog source.
2. **Given** the unauthenticated rate limit is exhausted, **Then** no further GitHub requests go out until the reset time, and cached values, including expired ones, keep being served.

### User Story 4 — Users see the numbers (Priority: P2)

The Web UI result row, the macOS catalog row and the CLI `catalog search` table show `★ 21.3k` or `⤓ 1.2M` when popularity is known, and nothing when it is not.

### Edge cases

- SourceCodeURL forms: `https://github.com/o/r`, `…/r.git`, `…/r/tree/main/src/x` (monorepo subpath), `git+https://…`, `http://`, `www.github.com`, a trailing slash, and mixed case. All of them normalize to the key `o/r` (lower-cased). Non-GitHub hosts, gist URLs, and owners or repos that fail GitHub's name rules produce no key and never cause a fetch.
- A monorepo (`modelcontextprotocol/servers`) gives its star count to every server that links into it. Popular shows only one of them (the first by `Rank`). Search tiebreaks still use the repo's count.
- A repo that returns 404 or 451 is negatively cached and shows no stars. A renamed repo's 301 (`/repositories/{id}`, same host) is followed, because the registry client pins redirects to the host only.
- The same `(source,id)` from two sources is already de-duplicated by `SearchAll`. The same repo from two different sources is fetched once, via one cache key.

## Requirements

### Functional Requirements

- **FR-001 (source-native signal)**: `parseDocker` MUST set `Installs = pull_count` when the field is a non-negative number. Docker Hub `star_count` MUST NOT be mapped to `Stars`, because it is on a different scale from GitHub stars. The value travels on a new `ServerEntry.Popularity *Popularity` field tagged `json:"-"`, so `ServerEntry`'s JSON (GET `/registries/{id}/servers`, `search_servers` with `registry`) stays unchanged.
- **FR-002 (BuildCatalogHit)**: `BuildCatalogHit` MUST copy `entry.Popularity` into the hit and fill in `Stars` from the installed provider's **cache only**, with no network call. It stays synchronous and never blocks.
- **FR-003 (repo key)**: A pure `GitHubRepoKey(sourceCodeURL) (key string, ok bool)` MUST normalize the edge-case forms above. Owner: `^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`. Repo: `^[A-Za-z0-9._-]{1,100}$`, excluding `.` and `..`. Any other input returns `ok=false`.
- **FR-004 (comparison)**: `Rank` and the Popular ordering MUST compare popularity as `(stars desc, installs desc)`, with missing = 0. Stars and installs are never added together or converted into each other.
- **FR-005 (sections)**: `buildSections` MUST take the full merged, de-duplicated, source-filtered pool **before** `limit` truncation. Official = official hits in merge (source-native) order, at most 12. Popular = hits with `stars>0 ∨ installs>0`, sorted per FR-004 and then `Rank`, at most one per GitHub repo key, at most 12. This amends Spec 109 FR-060 and data-model §9.
- **FR-006 (fetch)**: The GitHub fetcher MUST send `GET https://api.github.com/repos/{owner}/{repo}` through the SSRF-hardened registry client. The host is a compile-time constant, overridable only by a test hook. The request carries `Accept: application/vnd.github+json`, `X-GitHub-Api-Version: 2022-11-28`, the versioned mcpproxy User-Agent, and `If-None-Match` when an ETag is cached. The response body is capped at 1 MiB and each request has a 10 s timeout. Only `stargazers_count` and `ETag` are read.
- **FR-007 (never block the search)**: `SearchAll` MUST resolve popularity *after* the per-source fan-out. Cached values come back immediately. Misses are queued for a background fetch, and `SearchAll` waits for them for at most `SearchOptions.PopularityWait`: default 800 ms, 0 disables the wait, and the wait never outlives `ctx`. Misses still outstanding keep being fetched after `SearchAll` returns, so the next search has them. Popularity failures never add entries to `unavailable[]`.
- **FR-008 (cache)**: Each entry `{stars, etag, fetched_at, status}` MUST be kept in memory and persisted to the bbolt bucket `catalog_popularity` when a store is attached. The entry is fresh for 24 h after a 200 or 304, 24 h after a 404 or 451 (negative), and 1 h after any other error. Expired entries are still served (stale-while-revalidate) and queued for refresh. The store is capped at 5,000 keys, evicting the oldest `fetched_at` first.
- **FR-009 (rate limit)**: The fetcher MUST (a) run at most 4 requests at once; (b) de-duplicate in-flight and queued keys, with a queue capped at 256 so overflow is dropped and requested again on the next search; (c) spend at most 50 requests per rolling hour without a token and 4,000 with one; (d) stop all fetches until `X-RateLimit-Reset` when `X-RateLimit-Remaining` ≤ 5, or on 403/429 with `Retry-After` or `X-RateLimit-Remaining: 0` (using `Retry-After`, or 60 s when neither header is present); (e) fetch misses in the order `SearchAll` ranked them.
- **FR-010 (wiring)**: `registries.SetPopularityProvider(p)` installs the process-wide provider, and `nil` turns lookups off. The core runtime installs a provider backed by bbolt at startup. The CLI in-process fallback (`catalogSearch` when no daemon is running, and `catalog show`) installs a memory-only one. Tests use `SetPopularityProviderForTest`, which returns a restore func.
- **FR-011 (token & kill switch)**: If `MCPPROXY_GITHUB_TOKEN` is set, it is sent as `Authorization: Bearer …`, only to the constant GitHub API host. The generic `GITHUB_TOKEN` is deliberately **not** read, to avoid leaking an unrelated CI or dev token. `MCPPROXY_CATALOG_POPULARITY=false` (or `0`/`off`) turns off all outbound popularity fetches. Source-native Docker pulls still show.
- **FR-012 (display)**: The Web UI, macOS and CLI table MUST show compact popularity (`★ 1.2k`, `⤓ 3.4M`) when it is known. The REST, MCP and CLI JSON shapes are unchanged.

### Success Criteria

- **SC-001**: A fixture test with 14 official hits (one repo with the most stars sits outside the first 12 in source order) and one non-official Docker hit with pulls must show three things. Popular ≠ Official. Popular[0] is the most-starred hit. Popular contains a hit that Official does not.
- **SC-002**: With no signal known, Popular is empty and Official is unchanged.
- **SC-003**: With a stub GitHub server that sleeps 5 s, `SearchAll` returns within `PopularityWait` + 100 ms of the fan-out finishing, and a second call after the stub answers carries the stars.
- **SC-004**: After a 403 response with `X-RateLimit-Remaining: 0` and a reset time in the future, no further requests reach the stub before that reset time, and cached (stale) values are still returned.
- **SC-005**: Personal and server builds pass lint, and `go test -race ./internal/registries/...` passes. `./scripts/test-api-e2e.sh` passes with popularity turned off, since E2E must not depend on api.github.com.

## Assumptions

- Stars are an adequate proxy for "popular" in the MCP ecosystem. The GitHub star count is the signal most widely used by MCP directories (Glama, PulseMCP, mcp.so).
- 50 requests per hour covers a default catalog (~60–120 distinct repos) within about 2 hours of the first run, and the persisted cache means a restart costs nothing. Users who want it faster can set `MCPPROXY_GITHUB_TOKEN`.
- Repository slugs sent to api.github.com are public catalog data, not user data. The kill switch exists for air-gapped or offline setups.
