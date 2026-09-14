# Implementation Plan: Agent-Token Scope Hardening

**Branch**: `105-agent-scope-hardening` | **Date**: 2026-09-14 | **Spec**: [spec.md](spec.md)
**Input**: [spec.md](spec.md) · [gap-map.md](gap-map.md) (54 gaps, each adversarially verified against `b39800a89`) · [prior-diff-assessment.md](prior-diff-assessment.md) (disposition of an abandoned partial implementation)

## Summary

Spec 105 is the acceptance contract for "every MCP request an agent token makes is authorized by that token's own effective scope". Five fix PRs (#1223 target tier, #1224 tail_log, #1225 set_profile, #1226 read_cache, #1227 prompts) shipped the first half; the gap map confirms **54 remaining gaps across every FR** and that **not one is already satisfied by an unrelated path**. This plan delivers them as **nine PRs in a fixed merge order** — `A → (B ∥ D ∥ E ∥ H0) → C → F → G → H1` — each one roadmap task, each independently astra-reviewed, each carrying the differential oracle for its own scenarios. The technical approach is uniform: compute every scoped quantity from the one **effective-scope predicate** that already exists (`serverInScope` + `resolveActiveProfile`), stamp identity at the point of production (catalog entry, cache record, log record, approval record) and read it back at every consumer, and produce every refusal from a single constructor so hidden ≡ nonexistent in status, body and timing class. Administrators are untouched except for the SC-005 exception list.

## Technical Context

**Language/Version**: Go 1.26 (`go.mod`), both editions (`-tags server` for server edition tests)
**Primary Dependencies**: existing only — `mark3labs/mcp-go` v1.0.0 (tool filters re-run at `tools/call`, see Risks), `blevesearch/bleve` (BM25 + `server_name` facet), `go.etcd.io/bbolt`, `zap` + `lumberjack`, `santhosh-tekuri/jsonschema/v6`. **No new dependencies.**
**Storage**: BBolt `config.db` (`agent_tokens`, `tool_approvals`, cache records — gain a provenance version), Bleve index (`internal/index/bleve.go` docID scheme changes for raw tool names), per-server log files (`internal/logs/logger.go`)
**Testing**: `go test -race`, table-driven differential oracle (two fixtures: three-server `{a, b, a__b}` with sentinels vs `a`-only), zero-upstream-call counting oracle, byte-exact goldens (`internal/server/testdata/`, `toolslist_goldens/`), CI `-skip 'E2E|Binary|MCPProtocol|…'` regex for `internal/server`
**Target Platform**: macOS/Linux/Windows daemon; Docker isolation paths touched (FR-007 G5)
**Project Type**: single Go module, backend only (no frontend/tray changes; docs under `docs/features/`)
**Performance Goals**: FR-011 — on the frozen 527-tool snapshot (+50-prompt set, 10-page cache entry), for `retrieve_tools`, `read_cache`, `prompts/list`, `tools/list`: scoped p95 within 20 ms of admin, and admin p95 regresses ≤ max(10%, 5 ms) vs merge-base on the CI reference runner; constitution I (<100 ms BM25 on 1,000 tools) unchanged
**Constraints**: SC-005 admin byte-parity on every surface except the named exceptions; three frozen tool-surface goldens must not change except H0's two description strings; `isToolCallable`/`indexedToolVisible` stays quarantine-blind (Spec 085); bbolt `Update` rolls back on non-nil return; mcp-go `WithToolFilter` is re-evaluated at call time
**Scale/Scope**: 54 gaps, ~35 production files, ~25 test files inverted or added, 4 docs files; nine PRs

## Constitution Check

*GATE: evaluated before Phase 0; re-evaluated after Phase 1 (see end of file).*

| Principle | Status | Notes |
|---|---|---|
| I. Performance at Scale | PASS with measurement | FR-005 G1 replaces `Search(query, limit)` for scoped callers with a scope-aware search proven equal to the exhaustive filtered derivation (research D6; exhaustive is the only fallback). FR-011: in-run 20 ms delta test over four operations + merge-base admin-regression CI job on the reference runner (research D10). |
| II. Actor-Based Concurrency | PASS | No new goroutines or locks. Identity stamps ride existing immutable snapshots (catalog entries, `StateView`, cache records). Cache invalidation happens inside the existing bbolt `Update` closure; stats mutate only on the committed path. |
| III. Configuration-Driven | PASS | No new config field. Behaviour is derived from agent-token scope + profile config already in `mcp_config.json`; hot-reload paths (profile add/delete, deleted pin) already covered by #1225/#1227 tests and extended in D. |
| IV. Security by Default | PASS (this is the principle being enforced) | Every change fails closed: no-identity definitions and prompts withheld from everyone, unresolved target identity refused for every caller (D4), legacy/internal cache entries refused, unattributed log lines withheld from scoped callers, foreign containers never removed. Admin fail-open paths kept only where SC-005 names them. |
| V. TDD | PASS | Every gap has a test sketch in gap-map §1; each PR lands its failing tests first (differential oracle + counting oracle), then the fix. 16 tests that pin pre-105 behaviour are **inverted, never deleted** (gap-map §7 list). |
| VI. Documentation Hygiene | PASS | `docs/features/agent-tokens.md` (invariant, covered surfaces, retained effects, target-tier rule, secrets warning), `docs/features/profiles.md:70` (cleared-selection semantics), `docs/code_execution/*` (stored-script enumeration is admin-only). No CLAUDE.md/README change (no command or architecture change). |
| Core+Tray split | PASS | Core only. |
| Performance testing | PASS | FR-011 in-run test + merge-base CI job (research D10). |

No violations → Complexity Tracking is empty.

## Project Structure

### Documentation (this feature)

```text
specs/105-agent-scope-hardening/
├── spec.md                    # contract (19 astra rounds); §Spec text fixes below amend it in PR A
├── plan.md                    # this file
├── gap-map.md                 # Phase 0 evidence: 54 verified gaps, building blocks, PR split, risks
├── prior-diff-assessment.md   # Phase 0 evidence: disposition of the abandoned codex/105 diff
├── research.md                # Phase 0 decisions (design questions the spec left open)
├── data-model.md              # Phase 1: identity stamps, provenance record, effective scope
├── contracts/
│   ├── refusals.md            # one refusal shape per surface; hidden ≡ nonexistent tables
│   └── differential-oracle.md # the two-fixture harness every PR asserts against
├── quickstart.md              # per-PR verification recipe (tests, goldens, lint, astra)
└── tasks.md                   # /speckit.tasks output
```

### Source Code (repository root)

```text
internal/
├── auth/context.go                      # IsScopedCaller / CanAccessServer (read-only reuse)
├── cache/{authorization,manager,models}.go        # B: provenance version, refuse-and-invalidate, kind-first check
├── config/{config.go,tool_identity.go}  # A: RawName on ToolMetadata (new file tool_identity.go)
├── index/{bleve.go,manager.go}          # A: raw-name docIDs + rebuild trigger · C: SearchScoped + facet counts
├── logs/logger.go                       # E: attributed tail reader (filter-before-limit)
├── oauth/config.go                      # E: callback stop logs through the subject-bound logger
├── upstream/core/{client.go,docker.go}  # A: raw names in StateView · E: label-scoped container cleanup
├── runtime/{lifecycle.go,tool_quarantine.go,runtime.go}   # A: exact-name producers · B: internal cache writer kind
├── experiments/guesser.go               # B: internal cache writer kind
├── preflight/classify.go                # A: nil approval under active quarantine ≠ ready
├── jsruntime/runtime.go                 # G: nested-call refusal shape
├── codescripts/codescripts.go           # H0: stored-script enumeration admin-only
└── server/
    ├── mcp.go                           # C: scoped search/count/usage/risk · G: available-servers, profile∩token gate · B: read_cache collapse
    ├── mcp_annotations.go               # C: analyzeSessionRiskScoped
    ├── mcp_visibility.go                # C: scoped count primitive · G: describe not-found policy order
    ├── mcp_describe_tool.go / mcp_describe_direct.go      # G: authorized-corpus not-found + suggestions
    ├── mcp_direct_catalog.go            # F: positive built-in set, empty-raw-name withheld · G: shadow canonical map
    ├── mcp_direct_scope.go              # F: stamp-aware filters, strip for all callers · G: tier out of WithToolFilter
    ├── mcp_direct_callability.go        # A: lookupToolApproval reader · F: stamp read
    ├── mcp_routing.go                   # F: Meta identity stamp on rendered tools, builtin names from constructors, empty-name prompts
    ├── tool_gate.go                     # A: exact-wins + no-record-under-quarantine=pending
    ├── preflight_glue.go                # A: approval reader via lookupToolApproval
    ├── mcp_code_execution.go            # A: unresolved identity refuses scoped · G: nested refusal · H0: descriptions + enumeration
    ├── profile_tool.go / server.go      # D: selectable predicate on /mcp/p/*, uniform 404, URL-precedence in set_profile
    ├── cache_authz.go / content_forward.go            # B: parent provenance through pagination
    └── *_test.go                        # per-PR inverted tests + H1 harness (scope_differential_test.go, scope_http_matrix_test.go, scope_latency_test.go)
docs/
├── features/agent-tokens.md             # H0 + A: invariant, surfaces, retained effects, target-tier rule, warning
├── features/profiles.md                 # D: cleared-selection line
└── code_execution/{overview,cookbook,troubleshooting,api-reference}.md   # H0
```

## Delivery Structure — nine PRs, one roadmap task each

Order is a dependency order, not a preference. Parallel groups share no **function** (B and E both edit `mcp.go` in disjoint regions; see research D14 for the same-function hotspots that force the serial part of the order).

| # | PR (roadmap task id) | FR / gaps | Why here |
|---|---|---|---|
| **A** | `scope-target-identity-producers` | FR-009 G1–G7 | First: it is a **data-migration event** (raw-name approval records + index docIDs). Every discovery on HEAD deletes exact-name records (`lifecycle.go:783`), so the producer must ship before any reader relies on exact identity. Amends spec text (§ below). Unresolved identity refuses every caller (D4). |
| **B** | `scope-cache-legacy-invalidation` | FR-001/002 G1–G7 | Independent of A; touches `internal/cache` + `mcp.go:5613-5690` only. |
| **D** | `scope-selectable-profile-predicate` | FR-003/004 G1–G8 | Independent; `profile_tool.go` + `server.go` profile middleware. Introduces `mintAgentToken` fixture that H1 reuses. |
| **E** | `scope-log-attribution` | FR-007 G1–G6 | Independent; `logs`, `oauth`, `docker`, `mcp.go:5787-5803`. |
| **H0** | `scope-regression-suite` part 1 (FR-012) | FR-012 G1–G3 | Independent and small; the only PR allowed to regenerate goldens (two description strings). Carries the docs invariant text. |
| **C** | `scope-retrieve-tools` | FR-005 G1–G5 | After A (both edit `bleve.go`; A's docID change lands first so C's facet counts are over raw names). |
| **F** | `scope-direct-publication` | FR-008 G1–G7 | After A (`mcp_direct_callability.go` reader). Introduces the Meta identity stamp G depends on. |
| **G** | `scope-refusal-shapes` | FR-010 G1–G7 | After C (scoped-count primitive for describe not-found) and F (stamp + filter split). |
| **H1** | `scope-regression-suite` part 2 | FR-011/013/014, SC-007 | Last: the differential and HTTP-matrix harness would fail until C/F/G land; it also proves no pinned-reversal test survived. |

Each PR: failing tests first → fix → `go test -race -count=1 -skip 'E2E|Binary|MCPProtocol|TestInfoEndpoint|TestGracefulShutdownNoPanic|TestSocketInfoEndpoint' ./internal/server/...` + the touched packages → `git diff --stat -- internal/server/testdata` empty (H0 excepted) → golangci-lint v2 → opencode `gpt-6-astra` review (≤10 rounds, verify each finding before fixing) → PR body with "Follow-ups / Spec 105 gaps" checklist naming every gap id it closes.

## Design — the four mechanisms

### 1. One effective-scope predicate, applied before any cut

`serverInScope(authCtx, profileScope, name)` (`mcp_visibility.go:161-166`) composed with `resolveActiveProfile(ctx)` (`profile_resolver.go:118-164`; pin > URL > session, stale pin = deny-all) is the **only** way any new code decides "is server *s* inside this caller's scope". Never re-derive from `AllowedServers` or from the profile alone.

- **C (FR-005)**: `index.Manager.SearchScoped(query, limit, allowedServers)` = conjunction(text query, boost-0 `server_name` disjunction), **proven** score-identical to the FR-005 per-fixture derivation (exhaustive `Search` over the selected corpus, filtered, cut to K) by a test on the 527-tool snapshot; the only permitted fallback is that exhaustive derivation (research D6). Scoped callers and **profile-scoped administrators on the shared-index fallback** go through it (D3, SC-005 amendment); unprofiled administrators keep `Search` byte-for-byte. `total_indexed_tools` uses `server_name` facet term counts filtered by the effective set; `usage_summary.top_tools` filters `GetToolStats(∞)` by scope **and** approval (the exact-tool callability predicate, so indexed-but-pending/changed history is excluded — `mcp_entry_builder_test.go:170-208` shows index presence ≠ approval) then cuts to 10 (admin keeps `GetToolStats(10)` verbatim — the `retrieve_full_stats` golden pins `top_tools:[]`); ordinary retrieve discovery stays quarantine-blind. `session_risk` calls `analyzeSessionRiskScoped(snapshot, serverDiscoverable)`.
- **G (FR-010 G1/G7)**: the no-live-client branch's `Available servers:` list is filtered through `serverInScope` (admin unchanged); the profile-before-token ordering on `call_tool_*` and in the sandbox becomes a single **effective set = profile ∩ token** check with one body for agent callers, so `b ∈ P, b ∉ token` and `zzz` are indistinguishable.
- **D (FR-004)**: `profileMiddleware` evaluates `selectableProfileNames` (`profile_tool.go:188-210`, keyed on `auth.IsScopedCaller`) for `/mcp/p/<slug>`, `/mcp/p`, `/mcp/p/`; a not-selectable, missing, deleted, pin-mismatched or no-profiles outcome all flow through **one** refusal constructor → identical 404 status + body (no `available` list for scoped callers). Admin/anonymous keep today's three branches. **D (FR-003 URL precedence)**: `set_profile` on `/mcp/p/<slug>` stores the selection but reports `servers` = token ∩ **URL profile** (the URL governs; `profile_resolver.go:143-154` returns the URL scope before the session selection), so selecting a disjoint session profile reports the URL profile's intersection, not an empty set (`spec.md:112,126`).

### 2. Identity stamped at production, read at every consumer

| artifact | stamp | producer | consumers |
|---|---|---|---|
| Tool approval / index doc (A) | `RawName` (exact `ns:erase`, never first-colon-stripped) | `checkToolApprovals`, `applyDifferentialToolUpdate`, bleve docID = `server` + raw name | `lookupToolApproval` (exact wins; legacy collapsed record approves only its own raw name), direct callability, preflight |
| Cache record (B) | `Producer` authorization snapshot + `Version` | `cacheStoreAs` (already), internal writers stamped `CallerKindInternal` | `GetRecordsAs`: nil/unknown version → refuse **and** invalidate (committed delete); internal → refuse **without** evicting (legitimate `Peek`/`Get` readers stay); child pages inherit the **parent's** producer through `ReadCacheResponse.Producer` (`json:"-"`) |
| Rendered direct tool (F) | private-struct `_meta` stamp `{owner, rawName, tier}` from the catalog entry that produced it | `renderDirectTools` | scope filter and callability filter read the stamp (neither removes it); a **terminal third filter** strips it for every caller including administrators (drop the admin early-return) so the `direct_full_prefeature` golden stays byte-exact (its `maxResultSizeChars` comes from the response hook, after the filters); built-ins are identified from `builtinDirectToolNames` populated by the constructors, never by parse failure; unstamped non-built-in and empty raw name → withheld from everyone (SC-005 exception). **Aggregated prompts (FR-006, F)**: `filterAggregatedPromptsForAuth` withholds unstamped prompts for **every** caller, not only when `enforce` is set (`mcp_direct_scope.go:185-212`), and `prompts/get` refuses them; admin control fixture recorded. Protocol-level test proves the stamp is absent on the wire and that the scope filter re-runs at `tools/call` (research D7). |
| Log record (E) | the **existing** zap field `server=<raw name>` at `logger.go:385` — no new field, administrator records unchanged (research D8) | every per-server writer; child text only ever a field value (`monitoring.go:214`), audited by test | `ReadUpstreamServerLogTailAttributed(name, n)`: console encoder — first ` | {` boundary (left to right) whose suffix is exactly one complete JSON object; JSON encoder — whole line; filter by `server` **before** taking the last *n*; no accepted boundary → unattributed → withheld from scoped callers; admin/REST/CLI keep the whole-file reader |
| Container (E) | existing label `com.mcpproxy.server=<raw>` | `instance.go:57-65` | `ensureNoExistingContainers`, the disconnect name-pattern fallback AND the image-name fallback (`docker.go:243-248,296-329`) all filter by label + regex `^mcpproxy-<san>-[a-z0-9]{4}$`; foreign containers are never listed into a's log nor removed (D9) |

### 3. Refusals: one constructor per surface, hidden ≡ nonexistent

Pattern from `tailLogNotFound` (`mcp.go:5717-5720`) and `visibleCorpus.notFoundResult` (`preflight/evaluator.go:617-682`). Precedence stays scope → tier → other. "Indistinguishable" is the **whole wire envelope** (JSON-RPC code, message, data, result shape), status and timing class — never message text alone (research D12). Concretely: describe_tool definition mode evaluates existence and case-correction over the **authorized corpus only** (`toolVisibleToSession` reorder in `mcp_visibility.go:51-72`; `mcp_describe_direct.go:150` `continue` instead of `return`); direct catalog keeps a **shadow canonical map** so an authorized canonical `x__y:z` is never withdrawn because a hidden `x` displays `y:z` as `x__y:z`; read_cache collapses unauthorized/not-found/expired into the `cache key not found` body for agent callers (real BBolt errors stay distinct); stored-script not-found no longer enumerates for agent callers; inside a publication seam the direct handler's scope refusal emits the same `-32602` envelope the call-time filter emits for an unregistered name. For the direct surface the call-time `WithToolFilter` evaluates scope → tier → callability in the spec's order: hidden → `-32602`; over-tier on an authorized server → **passed through** to the handler, which answers insufficient-permission (tier-first, so over-tier + pending is insufficient-permission); in-scope, within-tier but disabled/quarantined/pending/changed → `-32602` unchanged (FR-010(3)). `tools/list` withholds over-tier and non-callable tools as today (research D13).

### 4. Two-fixture differential oracle (H1, consumed by every PR)

`scope_differential_test.go`: `newScopeFixture(t, full bool)` builds either `{a, b, a__b}` (sentinel strings in every hidden name/description/log line/cache payload) or `{a}`; `runScopeScenario(t, name, fn)` runs `fn` against both with an `a`-only token, normalises **nondeterministic** fields only (ids, timestamps — never seeded usage counts), excludes the ranking-dependent fields SC-001 names from the cross-fixture equality and **asserts each excluded field against its per-fixture derivation** (the unchanged selected-corpus `Search`, exhaustive, filtered to authorized hits, cut to K — `spec.md:113`), then asserts byte equality of the rest plus sentinel absence. `scope_http_matrix_test.go` mints real agent tokens (`mintAgentToken(name, allowed, perms, pin)` from D) and drives every `/mcp*` surface × operation over HTTP through `mcpAuthMiddleware`; `n/a` cells assert the unregistered-name `-32602`. `scope_latency_test.go` + a merge-base CI job cover FR-011's four operations and both bounds (research D10). The FR-009 acceptance tables are generated at their spec shape: retrieve **54 cells** = 3 permission sets × 3 target tiers × 3 variants × strict on/off, plus direct and nested permission-set × target-tier tables. PRs A–G ship standalone tests on the Phase-1 fixtures; H1 re-registers them by US id (research D14; SC-007 inventory table lives in H1's file header).

## Spec text amendments (folded into PR A, no behaviour beyond what the gap map verified)

1. **FR-008**: name the empty-raw-tool-name analogue of the prompt rule (G7): a catalog entry with an empty raw name has no identity and is withheld from every caller.
2. **FR-003/FR-004**: record the pinned zero-reach decision (G8): a pinned token whose pin has zero reach (empty profile, ghost servers, disjoint grant) is **refused** with the deleted-pin body — see research.md D1.
3. **SC-007**: restate as the gate on FR-005/008/010 PRs plus the list of pinned-reversal assertions to invert (the five sibling branches merged before the spec was committed).
4. **FR-009**: align the FR-009 wording ("MUST refuse scoped callers") with the Edge Case at `spec.md:107`: a failed or stale identity resolution on a known server is refused for **every** caller, administrators included (research.md D4); the unknown-server branch is out of scope of this rule.
6. **SC-005**: add two named administrator exceptions — FR-005 profile-scoped administrator on the shared-index fallback receives the exhaustive-filtered top-K (research.md D3); FR-001 caller-kind-first ordering lets a profile-bound administrator redeem any snapshot (research.md D5).
5. **FR-002**: anonymous callers on a deployment with `require_mcp_auth=false` are administrator-shaped (`spec.md:36`) and keep legacy-entry access **only** through the SC-005 exception; the spec's "every caller" therefore includes them for refusal, and the plan follows the spec (research.md D2).

## Complexity Tracking

None — no constitution violations to justify.

## Constitution Check — post-design

Re-evaluated after Phase 1 and after astra plan-review round 1 (14 findings, all applied — research D3–D8, D10, D12–D14): unchanged, all PASS. `SearchScoped` is a bleve query composition whose equality to the exhaustive derivation is a test, not an assumption; the FR-011 tests make both bounds executable.

## Phase 2 hand-off

`/speckit.tasks` generates `tasks.md` from this plan: one task block per PR A…H1, each starting with the failing tests (gap-map §1 sketches) and ending with the astra round. Dependencies: A before C/F/G/H1; F before G; C before G; D's `mintAgentToken` before H1; H0 anywhere after A's spec amendments (it edits the same docs file).
