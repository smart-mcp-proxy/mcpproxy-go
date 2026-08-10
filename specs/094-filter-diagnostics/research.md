# Research: retrieve_tools Filter Diagnostics (Phase 0)

No NEEDS CLARIFICATION markers existed in the Technical Context — the spec went through 4 Codex cross-review rounds that already forced code-level verification of every behavioral claim. This file consolidates those verified findings as the ground truth the design rests on. All references verified at `origin/main` = `d8cb143bd`.

## R1 — Where filtering happens and what the candidate window is

- **Decision**: `matched_before_filters` = `len(results)` at the point the `annotationFilterActive` branch begins in `handleRetrieveTools` (`internal/server/mcp.go:1561-1593`).
- **Rationale**: at that point results have passed (a) the index search — which normalizes `limit <= 0` to 20 in `internal/index/manager.go:104` and caps at 100 via the handler; (b) scope/callability visibility (`indexedToolVisible`, `internal/server/mcp_visibility.go`) with no backfill; (c) deterministic sort. This is exactly the "effective candidate window" defined in the spec's Edge Cases.
- **Alternatives considered**: counting at the raw-search stage (rejected — would count tools the caller could never see, and could leak existence of out-of-scope tools via arithmetic).

## R2 — Filter semantics that MUST be mirrored, not reimplemented

Verified semantics of `shouldExclude` (`internal/server/mcp_annotations.go:153-181`):

| Filter | Excludes when | "missing" class | "explicit" class |
|---|---|---|---|
| `read_only_only` | annotations nil, `ReadOnlyHint` nil, or `*ReadOnlyHint == false` | nil annotations / nil hint | `false` |
| `exclude_destructive` | NOT (explicit `readOnlyHint==true`) AND (annotations nil, `DestructiveHint` nil, or `true`) | nil annotations / nil hint | `true` |
| `exclude_open_world` | annotations nil, `OpenWorldHint` nil, or `true` | nil annotations / nil hint | `true` |

Evaluation order inside `shouldExclude` is read-only → destructive → open-world; the spec's first-failure attribution adopts the same order. The read-only-implies-non-destructive shortcut (`mcp_annotations.go:164-172`) must be visible in the counts (a tool excluded only because `read_only_only` was requested is attributed to `read_only_only` even if it would also fail `exclude_destructive` — first-failure handles this naturally).

- **Decision**: extract `excludeReason` and make `shouldExclude` delegate to it. Because delegation makes a live parity comparison circular, the test uses an **independent frozen oracle**: the pre-refactor `shouldExclude` body copied verbatim into the test file, plus an explicit truth table for first-failure filter and reason class. Exhaustive domain: (3³ = 27 non-nil hint combinations + 1 nil-annotations state) × 8 filter combinations = **224 cases**, asserting all three outputs (`excluded`, `filterKey`, `explicit`).
- **Rationale**: the frozen oracle detects semantic regressions delegation alone cannot; the counts describe what the filter actually did (spec Edge Cases; SC-004 keeps all existing tests green).

## R3 — Response assembly & byte-identity risk

- `response` map assembled at `mcp.go:1644+`; optional keys already follow the presence-only pattern (`hint`, `notice`, `disabled`, `remediation`, `debug`, `session_risk`). Adding `filter_diagnostics` under FR-001's condition follows the same pattern; when absent, the serialized response is untouched — SC-002's golden guarantee holds by construction.
- Spec 084's TOON surface-isolation test compares retrieve_tools byte-for-byte across toon modes; our block is deterministic (sorted map keys, fixed struct order) so it cannot introduce nondeterminism.
- Spec 085's T011 golden test pins full-mode entry bytes; entries are untouched.

## R4 — The three registrations (FR-009)

Verified: default registration (`mcp.go:805-834`) lacks the three filter params; code-execution builder (`mcp_routing.go:346+`) and call-tool builder (`mcp_routing.go:414+`) both declare them. `retrieveToolsDetailOption()` (`mcp.go:790-800`) is the existing anti-drift helper pattern for exactly this situation.

- **Decision**: new `retrieveToolsAnnotationFilterOptions() []mcp.ToolOption` used by all three registrations; descriptions of all three updated to mention `filter_diagnostics` + the window caveat.
- **Alternatives considered**: leaving the default schema as-is (rejected by spec FR-009 — the discoverability gap is how the field report's confusion started).

## R5 — Serialization determinism & size bound

- Go `encoding/json` sorts map keys alphabetically → FR-003's ordering promise is the encoder's native behavior; no custom marshaler needed. (Memory check: the `json-methods-promote-to-embedders` trap doesn't apply — new standalone struct, no methods on shared/embedded types.)
- Worst reachable fixture (all three filters nonzero, matched=100, omitted=100, 200-char suggestion from the JSON-safe charset) serializes to **463 bytes** — under the 500-byte SC-003 bound (the round-4 reviewer's 468–474 estimate used a conservative all-3-digit-counts allowance; both are safely under the bound).

## R6 — What this feature must NOT interact with

- **Locked/quarantined flow**: `droppedCount`/`disabledEntries` are computed before the annotation branch (`mcp.go:1445-1533`); no shared counters — the two mechanisms stay additive. Out-of-scope hits are silently invisible (`mcp.go:1502`) and belong to neither.
- **REST/swagger/frontend**: MCP response field only — `oas/swagger.yaml`, `contracts.ts` generator, and frontend are out of scope (verified: retrieve_tools responses are not typed there).
- **Config**: no knob (spec Assumptions) ⇒ `DetectConfigChanges`/env/docs checklist not triggered.
