# Implementation Plan: retrieve_tools Filter Diagnostics

**Branch**: `094-filter-diagnostics` | **Date**: 2026-08-10 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/094-filter-diagnostics/spec.md` (Codex-approved after 4 review rounds)

## Summary

When annotation-based safety filters (`read_only_only`, `exclude_destructive`, `exclude_open_world`) remove query-matched tools from a `retrieve_tools` response, emit a compact, normatively-shaped `filter_diagnostics` block (counts + one suggestion string; never identities) so callers can distinguish "tool doesn't exist" from "tool withheld by filter — and here's why". Purely additive: byte-identical responses whenever no filter omits anything. Also closes the default-registration schema gap (the three filter params are currently undiscoverable on the default surface).

## Technical Context

**Language/Version**: Go 1.24 (module toolchain; repo builds with local Go 1.25)
**Primary Dependencies**: existing only — `mark3labs/mcp-go` (tool registration), stdlib `encoding/json`. No new dependencies.
**Storage**: N/A (no persistence; diagnostics computed per call from in-memory candidate set)
**Testing**: `go test ./internal/server/...` (unit + handler tests, table-driven), golden byte-identity assertions, `go test -race`
**Target Platform**: all (pure server-side response change)
**Project Type**: single Go project — changes confined to `internal/server/`
**Performance Goals**: zero additional index queries or upstream calls (FR-008); O(window) counting inside the existing filter loop; ≤500-byte serialized block (SC-003)
**Constraints**: byte-identical responses when inactive (SC-002); identical block in full/compact modes (FR-007); existing filter semantics frozen
**Scale/Scope**: 3 source files touched (`mcp_annotations.go`, `mcp.go`, `mcp_routing.go`) + tests; candidate window ≤100 tools

## Constitution Check

| Principle | Verdict | Notes |
|---|---|---|
| I. Performance at Scale | PASS | Counting piggybacks on the existing single pass over ≤100 candidates; no extra queries (FR-008). |
| II. Actor-Based Concurrency | PASS | No new goroutines, channels, or shared state; pure request-scoped computation. |
| III. Configuration-Driven | PASS (n/a) | Deliberately no config knob — block is self-suppressing (spec Assumptions). No config field ⇒ the 4-point config-field checklist is not triggered. |
| IV. Security by Default | PASS | Counts + constant suggestion only; no tool/server identities (FR-005) — leak-proof even for stale-index quarantine hits. Filter semantics unchanged. |
| V. TDD | PASS | Tests written first per task order: parity property test, handler condition tests, golden byte-identity, SC-003 size fixture. |
| VI. Documentation Hygiene | PASS | Tool descriptions updated on all three registrations (FR-009); README/CLAUDE.md untouched (no architecture/command change); docs page touched only if one documents retrieve_tools params (checked in tasks). |

Post-design re-check: unchanged — no violations, Complexity Tracking not needed.

## Project Structure

### Documentation (this feature)

```text
specs/094-filter-diagnostics/
├── spec.md              # Approved (Codex round 4)
├── plan.md              # This file
├── research.md          # Phase 0 — code-audit findings the design rests on
├── data-model.md        # Phase 1 — FilterDiagnostics shape + attribution algorithm
├── quickstart.md        # Phase 1 — how to exercise & verify locally
├── contracts/
│   └── filter_diagnostics.schema.json   # Normative JSON Schema of the block
└── checklists/requirements.md
```

### Source Code (repository root)

```text
internal/server/
├── mcp_annotations.go        # MODIFY: excludeReason() extracted; shouldExclude() delegates to it
│                             #         (parity by construction); filterByAnnotationsWithDiagnostics()
├── mcp.go                    # MODIFY: handler wires diagnostics into the response map;
│                             #         default retrieve_tools registration gains the 3 filter params;
│                             #         default description mentions filter_diagnostics + window caveat
├── mcp_routing.go            # MODIFY: both routing-mode retrieve_tools descriptions mention the block
├── mcp_annotations_test.go   # MODIFY: excludeReason ↔ shouldExclude parity property test
└── mcp_filter_diagnostics_test.go  # NEW: condition tests, counts, suggestion selection,
                                    #      SC-003 size fixture + charset assertion, absence golden
```

**Structure Decision**: All changes live in `internal/server/` beside the code they extend; no new packages. The diagnostics type lives in `mcp_annotations.go` next to the filter it observes.

## Design decisions (Phase 1 digest)

1. **Parity by construction, not by discipline**: `shouldExclude` (existing, semantics frozen) is reimplemented as a thin wrapper over a new `excludeReason(annotations, ro, xd, xow) (filterKey string, explicit bool, excluded bool)` which returns the first-failing filter and its reason class. Existing annotation-filter tests plus a new exhaustive property test (all hint-combos × filter-combos) pin the equivalence, so counts can never drift from what the filter actually did (spec Edge Cases).
2. **Response wiring**: inside the existing `annotationFilterActive` branch in `handleRetrieveTools`, replace the `filterByAnnotations` call with `filterByAnnotationsWithDiagnostics`, which returns `(filtered, *filterDiagnostics)`. Attach `response["filter_diagnostics"]` only when `diag.OmittedTotal >= 1` (FR-001). All three surfaces share this one handler, so FR-010 comes free; the code-execution mode's forced-full path is untouched.
3. **Deterministic serialization**: `omitted_by_filter` is a `map[string]reasonCounts`; Go's `encoding/json` sorts map keys alphabetically — exactly the FR-003 ordering promise. Struct field order handles the rest.
4. **Suggestion constants**: two `const` template strings (missing-annotation precedence / explicit-only), filter names joined literally (e.g. `read_only_only, exclude_open_world`). Charset `[a-zA-Z0-9 .,:;()'_-]` enforced by a test over the real constants with maximal filter-name interpolation (FR-006/SC-003).
5. **Registration gap (FR-009)**: extract the three `mcp.WithBoolean` filter options into a shared `retrieveToolsAnnotationFilterOptions()` helper (same pattern as `retrieveToolsDetailOption()`, which exists precisely to stop schema drift between the three registrations) and use it in all three; today the two routing builders duplicate the definitions and the default registration omits them entirely.
6. **What is deliberately NOT touched**: index layer, visibility resolver, locked-tools flow, TOON encoder (operates on call_tool results, not retrieve_tools), REST API/swagger (MCP-only response field), frontend contracts.

## Phase 2 stop

Planning ends here per the speckit workflow; `/speckit.tasks` generates tasks.md next.
