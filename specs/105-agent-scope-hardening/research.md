# Research: Agent-Token Scope Hardening — open decisions resolved

**Phase 0 evidence** is [gap-map.md](gap-map.md) (54 gaps, each with an adversarial verdict and `file:line`) and [prior-diff-assessment.md](prior-diff-assessment.md). This file records only the decisions the spec left open or that the gap map surfaced as design choices. Each was decided under the repo's zero-interruption rule; the PR that implements it restates the decision in its body.

## D1 — Pinned token whose pin has zero reach (FR-003/004, gap FR003-G8)

**Decision**: refuse. `set_profile <pin>` and `/mcp/p/<pin>` answer with the same body a deleted pin produces; no session mutation.
**Rationale**: the observable today is "does my configured pin still exist" (admitted if it exists with zero reach, refused if deleted) — a profile-existence oracle held by the one caller the pin was meant to confine. FR-004's non-disclosure goal covers it even though the predicate text is worded for unpinned tokens. #1225 F2 locked "configured pin always selectable" before this oracle was noticed; the test (`TestHandleSetProfile_PinnedTokenSelectsDisjointPin`) is inverted, not deleted.
**Alternatives**: keep admitting (preserves #1225 behaviour, keeps the oracle); admit but answer with an empty server set (still discloses existence via status). Rejected.

## D2 — Anonymous callers and legacy/internal cache entries (FR-002, gap FR001-G1/G2)

**Decision**: legacy (nil-producer or unknown-version) entries are refused for **every** caller kind including anonymous and administrator, and invalidated on refusal (committed delete). Internal entries (registry, guesser; stamped `CallerKindInternal` by their writers) are refused for every `read_cache` caller **without** eviction.
**Rationale**: `spec.md:36` makes anonymous callers administrator-shaped, and SC-005 names legacy + fresh-internal entries as the administrator exception — so refusing admins here is spec-conformant, not a widening. Evicting internal entries on refusal would turn guessable keys (`npm:<pkg>`, `registry-servers:<id>:…`) into an eviction DoS against `runtime.go:2160`/`guesser.go:258`, the legitimate readers.
**Alternatives**: keep admin access to legacy entries (violates SC-004 "refused for administrator and agent callers"); evict internal entries too (DoS). Rejected.

## D3 — Profile-scoped administrator on the retrieve fallback path (FR-005 / SC-005)

**Decision**: an administrator bound to a profile (URL or session) that has **no per-profile index** (fallback to the shared index) is treated as profile-scoped for the search cut, i.e. `SearchScoped` with the profile's server set; an administrator with no profile keeps `Search` byte-for-byte.
**Rationale**: the profile is the admin's own choice; today the fallback silently returns out-of-profile hits that the per-profile-index path would not, so the two admin paths disagree with each other. Aligning them is not a Spec 105 exception because the unprofiled admin response is unchanged (SC-005 holds on the surfaces the goldens capture).
**Alternatives**: leave the fallback fleet-wide (inconsistent admin behaviour across identical requests). Rejected.

## D4 — Unresolved target identity (FR-009 G4/G5)

**Decision**: for **scoped callers** (`auth.IsScopedCaller`), a tool whose identity cannot be resolved from the live snapshot (undiscovered, stale generation) is refused with the insufficient-permission body and zero upstream calls, on retrieve and nested paths. Administrators and unknown-server callers keep the existing fail-open (`mcp_code_execution.go:990-1000`) because `ToolAnnotationFunc`'s string contract cannot express "refuse" and admins are not the threat model. "No approval record" while the quarantine gate is active (`quarantine_enabled && !IsQuarantineSkipped()`) is **pending**, not ready, at every gate site (`tool_gate.go`, `ClassifyTool`, direct callability, preflight).
**Rationale**: HEAD grants the `destructive` tier to a ghost tool (`mcp_code_execution.go:1260-1263`), so a full-tier scoped token reaches the upstream with an unverified name — the exact "acts on servers outside its grant" shape. The pending rule closes the pre-migration window where a collapsed `erase` record would otherwise be inherited by `ns:erase`.
**Alternatives**: refuse for admins too (breaks in-process fixtures that register an upstream without a StateView entry, e.g. `mcp_call_tool_trim_test.go:57`, for no security gain); keep no-record = ready (re-opens G1). Rejected.

## D5 — Kind-first cache authorization (FR-001 G6)

**Decision**: evaluate `reader.Unrestricted()` (administrator, no profile narrowing) **before** the profile-set comparison, but keep the deny-all guard (`len(reader.ProfileServers)==0 → false` for profile-scoped readers) ahead of the shortcut. Result: an admin session bound to profile `{a}` can read an unscoped admin entry and a wider-profile entry; a pinned wildcard agent still cannot read an unpinned agent's entry.
**Rationale**: `authorization_test.go:65-72` pins the opposite (declined as #1226 R1-F2), but FR-001's rule is superset-of-authorization, and an administrator's authorization is a superset of any profile scope. The deny-all guard ordering is the codex-diff lesson (prior-diff-assessment hunk 1).
**Alternatives**: keep profile-first (over-refusal, admin inconsistency). Rejected.

## D6 — Scope-aware search mechanics (FR-005 G1)

**Decision**: `index.Manager.SearchScoped(query, limit, servers []string)` composes the existing BM25 text query with a bleve `server_name` disjunction (`NewDisjunctionQuery` of `NewTermQuery` per allowed server) under one `NewConjunctionQuery`, `Size=limit`. Counts use `server_name` facet terms (`bleve.go:458-486`) intersected with the allowed set.
**Rationale**: exhaustive `Size=documentCount` then filter loads seven stored fields per hit and cannot meet FR-011 on 527 tools; a capped oversize (e.g. `4×limit`) does not in general equal the exhaustive top-K when hidden servers dominate the ranking. A query-level filter keeps BM25 scoring identical for the authorized documents (scores are per-document; the conjunction only excludes) and preserves the documented "scoring over the selected corpus" semantics.
**Alternatives**: oversize-then-filter (ranking-dependent correctness); per-token indexes (memory, rebuild cost). Rejected; oversize-then-filter is retained only as the fallback if the facet/term path proves slower in the FR-011 test.

## D7 — Identity stamp carrier on rendered direct tools (FR-008)

**Decision**: a private Go struct value in `mcp.Tool.Meta` (`_meta`) — unforgeable from the wire, readable by both `WithToolFilter`s, stripped for every caller before the response is serialised (the admin early-return at `mcp_direct_scope.go:48-50` is removed so admins pass through the same strip and the `direct_full_prefeature` golden's upstream `_meta.anthropic/maxResultSizeChars` pointer is restored verbatim).
**Rationale**: mirrors the shipped prompt stamp (`aggregatedPromptStamp` / `stripAggregatedPromptServer`, `mcp_routing.go:1349-1388`) — proven under the same mcp-go value-copy filter semantics. Reading the catalog by display name is the seam bug being fixed; the stamp is the only per-publication identity that survives the filter.
**Alternatives**: catalog generation counter compared at filter time (races the seam exactly as today); resolving through the registry (the registry entry is what leaks). Rejected.

## D8 — Log attribution key and unattributed lines (FR-007)

**Decision**: a dedicated key `app.mcpproxy/owner` (raw server name) stamped at `logger.go:385`; the attributed reader matches the **last** ` | ` segment's **first** occurrence of that key (child output may contain ` | ` and JSON). Lines without the key are withheld from scoped callers by rule; administrators, REST and CLI readers keep the whole file.
**Rationale**: the existing `server` key is user-controllable through echoed child output (duplicate-key JSON keeps the last value), so it cannot be the ownership signal. A per-record filter is cheaper and more robust than splitting files (which would still collide for `a/b` vs `a_b` after sanitisation).
**Alternatives**: separate files keyed by raw name hash (breaks the documented file layout, tooling, and rotation); accept collisions (spec violation). Rejected. Known retained effect: two lumberjack sinks on one file can produce torn fragments; those are non-attributable and therefore withheld.

## D9 — Container ownership (FR-007 G5)

**Decision**: `ensureNoExistingContainers` and the disconnect fallback filter `docker ps` results by label `com.mcpproxy.server=<raw>` first, then by the regex `^mcpproxy-<sanitised>-[a-z0-9]{4}$`; only containers matching **both** are removed or logged. Pre-label containers and user-`--name` containers are left alone (they never were ours by the new rule).
**Rationale**: the label exists (`instance.go:57-65`) and is the only signal that disambiguates `a/b` from `a-b`; the regex guards against a foreign process re-using the label prefix.
**Alternatives**: label only (weaker); rename containers with a hash (breaks scanner lookup MCP-2123, out of scope). Rejected.

## D10 — FR-011 measurement

**Decision**: an in-run test asserting `p95(scoped) − p95(admin) ≤ 20 ms` over 200 iterations of `retrieve_tools` on the 527-tool snapshot (`loadDeferredLargeCorpus`), skipped under `-race`; the cross-revision (merge-base) bound is a documented manual `benchstat` step in quickstart, not CI.
**Rationale**: `bench.yml` is tag-only and non-blocking; `-race` inflates ~20× and would make any absolute ceiling meaningless.

## D11 — Disposition of the abandoned partial implementation

Per [prior-diff-assessment.md](prior-diff-assessment.md): port hunks 1–6 and 10 with the listed changes; discard 7 (regresses no-record semantics), 8 and 9 (do not compile / do not address G1). Nothing is applied wholesale — every hunk lands inside its PR's TDD cycle.
