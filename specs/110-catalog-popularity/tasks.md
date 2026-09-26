# Tasks: Real Popularity Signal for the Catalog

**Input**: [spec.md](spec.md), [plan.md](plan.md). TDD: every task writes its failing test first.

## Phase 1 — Pure pieces (PR A)

- [x] T001 [P] [US1] `GitHubRepoKey` + table test covering every edge-case form in the spec (`internal/registries/popularity_key.go`, `popularity_key_test.go`) — FR-003
- [x] T002 [P] [US1] `parseDocker` maps `pull_count` → `ServerEntry.Popularity.Installs`, ignores `star_count`, and ignores negative or non-numeric values. Add `ServerEntry.Popularity json:"-"` plus a test that `ServerEntry` JSON is unchanged (`search.go`, `types.go`, `search_test.go`) — FR-001
- [x] T003 [US2] Replace `popularityScore` sum with `comparePopularity` using (stars, installs). Add rank tests: stars beat installs, installs break ties between equal stars, existing tests stay green (`catalog.go`, `rank_test.go`) — FR-004

## Phase 2 — Provider (PR A)

- [x] T004 [US3] `PopularityProvider` interface, `SetPopularityProvider`, `SetPopularityProviderForTest`, `SetGitHubAPIBaseForTest` (`popularity.go`, `testhooks.go`) — FR-010
- [x] T005 [US3] `githubStarsProvider` fetch against an httptest stub. Cover: 200 (stars + ETag stored), 304 (refresh `FetchedAt`, keep stars), 404 (negative), 500 (1h error TTL), exact request headers, bearer only when `MCPPROXY_GITHUB_TOKEN` is set, body cap, and a 301 redirect on the same host (`popularity_github.go`, `popularity_github_test.go`) — FR-006, FR-011
- [x] T006 [US3] TTL, serve-stale and requeue via an injected clock. Cover dedup of queued/in-flight keys, queue overflow drop, concurrency ≤4 (stub counts peak concurrency), rolling budget of 50/h, breaker on `Remaining ≤ 5`, and 403/429 with `Retry-After` (SC-004) — FR-008, FR-009
- [x] T007 [US3] bbolt store: round-trip, lazy load, cap eviction, and surviving a provider restart (a temp DB) (`popularity_store.go`, `popularity_store_test.go`) — FR-008
- [x] T008 [US3] `Resolve` wait bounds: returns by `wait` and by `ctx` cancel, fetches continue afterwards, and `wait=0` never blocks (SC-003) — FR-007
- [x] T009 [US3] Kill switch: with `MCPPROXY_CATALOG_POPULARITY=false` no request ever reaches the stub — FR-011

## Phase 3 — Catalog integration (PR A)

- [x] T010 [US1] `BuildCatalogHit` copies `entry.Popularity` and adds cache-only stars (`catalog.go`, `catalog_popularity_test.go`) — FR-002
- [x] T011 [US1] `SearchAll`: add `SearchOptions.PopularityWait` (default 800 ms), Resolve after the fan-out, re-apply and re-rank, and build sections from the pool before truncation — FR-005, FR-007
- [x] T012 [US1] `buildSections`: Official in source order, Popular signal-only, one per repo, capped at 12. **SC-001 and SC-002 tests** (fixture registries through `SetRegistriesForTest` + the stub provider)
- [x] T013 [US3] SearchAll with a GitHub stub that sleeps 5 s: returns within the wait, the second call has the stars, and `unavailable[]` is untouched (SC-003)
- [x] T014 Update any 109 test that asserted Official in popularity/rank order so it asserts source order instead (`internal/registries/*_test.go`, `internal/httpapi/catalog*_test.go`, `cmd/mcpproxy/catalog_cmd*_test.go`)

## Phase 4 — Wiring (PR A)

- [x] T015 Runtime installs the bbolt-backed provider and closes it on shutdown (`internal/runtime/runtime.go`)
- [x] T016 CLI in-process fallback and `catalog show` install the memory provider (`cmd/mcpproxy/catalog_cmd.go`)
- [x] T017 `scripts/test-api-e2e.sh` exports `MCPPROXY_CATALOG_POPULARITY=false`
- [x] T018 Docs: `docs/features/` catalog page — popularity sources, `MCPPROXY_GITHUB_TOKEN`, kill switch
- [x] T019 Verify: both golangci-lint runs (bare + `--build-tags server`), `go test -race ./internal/registries/... ./internal/httpapi/... ./cmd/mcpproxy/...`, and `./scripts/test-api-e2e.sh`

## Phase 5 — Display (PR B, stacked)

- [ ] T020 [P] [US4] Web UI: `formatPopularity` util + vitest (`frontend/tests/unit/`) + badge in `CatalogSearch.vue` result row
- [ ] T021 [P] [US4] macOS: badge in `CatalogView` row + `CatalogTests` formatting test
- [ ] T022 [P] [US4] CLI: `catalog search` table shows a POPULARITY column
- [ ] T023 [US4] Playwright web-ui sweep of the catalog landing with a seeded stub
