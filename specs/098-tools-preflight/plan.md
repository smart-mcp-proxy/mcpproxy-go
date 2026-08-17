# Implementation Plan: Required-Tools Preflight

**Branch**: `098-tools-preflight` | **Date**: 2026-08-15 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/098-tools-preflight/spec.md`

## Summary

A deterministic, side-effect-free per-tool availability check: one shared eligibility evaluator (new `internal/preflight` package) composes existing local state (Bleve index, spec 032 approval records + hashes, stateview connection snapshot + health classification, config policy, spec 094 annotation classifier) into a `ready`-or-one-of-15-failure-reasons verdict with a fixed precedence chain. Exposed via `POST /api/v1/preflight` (chi route in `internal/httpapi`, profile-aware, wait_ms long-poll under the spec 093 shed budget) and `mcpproxy tools preflight` (Cobra, exit codes 0/10/11/12). Every executed preflight writes an activity record. Zero MCP-surface changes; zero upstream I/O (instrumented-transport asserted); sabotage E2E matrix covers every reason cell.

## Technical Context

**Language/Version**: Go 1.24 module toolchain (repo builds with local Go 1.25)
**Primary Dependencies**: existing only — chi (httpapi), bbolt (storage), Bleve (index), zap (logging), Cobra (CLI), swaggo/swag v2 (contract regen). **No new dependencies.**
**Storage**: existing BBolt buckets (read-only for approval records/hashes); no new buckets. Activity log via existing `internal/logs` activity store (new record kind `preflight`).
**Testing**: `go test -race ./internal/...`; handler-level benchmark (`testing.B`); E2E sabotage matrix in `internal/server/e2e_test.go`-style harness with the existing test ctl-server fixtures (DESC_FILE rug-pull trick per QA harness memory); server-edition `go test -tags server`.
**Target Platform**: macOS/Linux/Windows, both editions (personal + server) — feature lives in shared `internal/*`, ships in both automatically.
**Project Type**: single Go project; no frontend changes beyond activity-view tolerance check (records render generically).
**Performance Goals**: p95 < 50 ms for a 10-ID batch at handler level; zero upstream calls (hard assertion).
**Constraints**: byte-identical `tools/list` across all three routing modes; additive-only enum; disclosure tiers (operator vs agent-token); wait_ms ≤ 10 s inside spec 093 shed budget with ≥250 ms poll floor; 503 when runtime unavailable.
**Scale/Scope**: ≤100 IDs per request; evaluator must stay O(ids) with in-memory/BBolt lookups only (constitution: 1,000-tool installs).

## Constitution Check

| Principle | Status | Notes |
|---|---|---|
| I. Performance at Scale | PASS | Stat-only local reads; benchmark-gated p95; no blocking of API requests (wait_ms uses context-aware polling, counted in shed budget) |
| II. Actor-Based Concurrency | PASS | Evaluator is a pure read-side function over stateview `Snapshot()` (lock-free) + BBolt reads; no new goroutines except the bounded wait_ms poll loop owned by the request context |
| III. Configuration-Driven | PASS | No new config fields in v1 (deliberate — avoids the 4-point hot-reload checklist); profile comes from the request |
| IV. Security by Default | PASS | Disclosure tiers enforced in the evaluator context; scope-silence byte-indistinguishable `not_found`; quarantine semantics untouched (preflight reports, never bypasses); REST requires API key as everywhere |
| V. TDD | PASS | Taxonomy table drives table-driven unit tests written first; sabotage E2E matrix is the acceptance gate |
| VI. Documentation Hygiene | PASS | FR-017: rest-api.md, CLI docs, new feature page, usage examples; `make swagger` + generate-types regen |

**Post-design re-check (after Phase 1)**: PASS — no violations introduced; no Complexity Tracking entries needed.

## Project Structure

### Documentation (this feature)

```text
specs/098-tools-preflight/
├── plan.md              # This file
├── research.md          # Phase 0: seam analysis + decisions
├── data-model.md        # Phase 1: entities, enum, precedence
├── quickstart.md        # Phase 1: build → sabotage → verify walkthrough
├── contracts/
│   └── preflight-api.yaml   # OpenAPI fragment for POST /api/v1/preflight
└── tasks.md             # Phase 2 (/speckit.tasks)
```

### Source Code (repository root)

```text
internal/preflight/               # NEW package: evaluator + taxonomy
├── reasons.go                    # reason enum constants, classes, precedence, verdict/exit mapping (single source of truth)
├── evaluator.go                  # Evaluate(ctx, EvalContext, []ToolRef) -> []Result; composes gate inputs
├── evaluator_test.go             # table-driven: every enum cell + precedence co-occurrence pairs
└── bench_test.go                 # handler-level benchmark (SC-002)

internal/contracts/types.go       # PreflightRequest/Response/ToolResult DTOs + reason constants mirror (generate-types → contracts.ts)
internal/server/mcp.go            # dispatch gate consolidation: handleCallToolVariant inline checks delegate to preflight evaluator semantics
internal/server/mcp_visibility.go # describeGateReason reconciled onto shared classification
internal/server/preflight_glue.go # NEW: MCPProxyServer method exposing evaluator with index/stateview/storage/profile wiring (ServerController surface)
internal/httpapi/preflight.go     # NEW: POST /api/v1/preflight handler (validation, tiers, wait_ms, activity record)
internal/httpapi/server.go        # route registration + ServerController interface method
cmd/mcpproxy/tools_cmd.go         # `tools preflight` subcommand (exit codes, -o formats, --pin/--profile/--wait/filter flags)
cmd/mcpproxy/exit_codes.go        # typed preflight exit-code error + central classification
internal/storage/activity_models.go   # activity type "preflight" (allowlist + metadata shape)
internal/runtime/activity_service.go  # synchronous durable RecordPreflight seam
oas/swagger.yaml                  # regenerated (make swagger)
frontend/src/types/contracts.ts   # regenerated (cmd/generate-types)
docs/api/rest-api.md              # endpoint reference
docs/cli-management-commands.md   # CLI reference + exit codes
docs/features/tools-preflight.md  # NEW feature page (concept, taxonomy, recipes)
docs/…                            # usage-example expansion (FR-017)
```

**Structure Decision**: New leaf package `internal/preflight` holds taxonomy + evaluator so both `internal/server` (dispatch reconciliation, glue) and `internal/httpapi` (REST) depend on it without a cycle; `internal/server` remains the only place with index/stateview/storage wiring, exposed to httpapi via one new ServerController method (precedent: `GetToolApprovalStatus`).

## Key Design Decisions (from research.md)

1. **Evaluator inputs are injected snapshots, not live managers**: `EvalContext{Index, Approvals, StateSnapshot, ConfigPolicy, Tier, Profile, Filters, Pins}` — makes the zero-upstream-I/O guarantee structural (the evaluator cannot reach a transport) and unit tests trivial. The glue layer in `internal/server` builds the context; `serverToolNames`'s live-ListTools fallback is unreachable by construction. Evaluator infrastructure read errors (Bleve/BBolt/snapshot) surface as an error → handler 503, never a fabricated reason code.
2. **Dispatch consolidation via shared primitives across ALL dispatch paths**: the shared gate primitives (`preflight.ClassifyTool` and friends) are consumed by `handleCallToolVariant`, the direct-mode callability path (`mcp_direct_callability.go`), code_execution, and stored-script dispatch — not just contract-tested. Known divergences fixed: quarantine-enabled/skip flags honored everywhere; `changed` no longer collapsed into `pending`; `auto_approve_tool_changes` ⇒ ready. Dispatch behavior is ground truth; where dispatch is deliberately fail-open (unindexed tool may still be callable), the no-skew guarantee is one-way (spec FR-002).
3. **Reason enum lives once in `internal/preflight/reasons.go`**, mirrored into `internal/contracts` DTOs and added to the `cmd/generate-types` **template source** (the generator emits hard-coded TypeScript — new types must be added to the generator itself, then regenerated); `frontend/src/types/api.ts` activity-kind union updated separately. Anti-drift unit test asserts enum ≡ contracts constants.
4. **`server_initializing` is server-level**: derived from connection state ∈ {Connecting, Discovering, Authenticating} only; no per-tool indexing progress claims (stateview populates post-Bleve).
5. **PendingAuth → `oauth_required`** mapped explicitly before the health-calculator fallthrough.
6. **Activity record**: new type `preflight` written through the real activity seam — `internal/storage` activity models + `internal/runtime` ActivityService — via a new **synchronous durable** `RecordPreflight` path (the async bounded event channel may drop; FR-014 forbids that for preflight). `RequestID` stays first-class; verdict/counts/per-tool go in `ActivityRecord.Metadata`; existing status vocabulary reused. Every duplicated type vocabulary updated: storage allowlist, CLI activity allowlist/rendering, swagger enums, frontend `api.ts` union + filter menu.
7. **wait_ms**: handler-level context-deadline poll loop (floor 250 ms) re-running the evaluator, bounded by a small dedicated preflight-wait semaphore; exhausted semaphore ⇒ degrade to immediate resolve with `waited_ms: 0` (spec 093 admission control is upstream-call-scoped and not reused here — corrected assumption).
8. **Profiles without index mutation**: exact-ID existence = shared index + profile scope filter (per-profile Bleve sub-indexes are a ranking concern; `ForProfile` lazily creates/caches indexes and MUST NOT be called from the read-only preflight path). Agent-token `ProfilePin` is propagated through REST auth (today it is dropped — fix in scope) and evaluation runs under the intersection of token scope ∩ token pin ∩ requested profile.
9. **OAS/generation reality**: `make swagger` generates from Go swag annotations and excludes `specs/` — the contract YAML here is design documentation; the implementation adds swag annotations on the handler/DTOs and validates with `scripts/verify-oas.sh` / `make swagger-verify`. Response uses the standard `APIResponse{data}` envelope + existing security scheme names.
10. **CLI exit codes**: new typed exit-code error recognized by the central classifier in `cmd/mcpproxy` (exit_codes.go / main.go error mapping) + a `cliclient` Preflight method — codes 10/11/12 cannot be returned ad hoc from the subcommand.
11. **Windows**: no special code — named pipe already provides admin context to the CLI; stated in docs.

## Verification Plan (maps to user requirements)

- **Local feature verification**: isolated dev instance on high port with scratch `--data-dir` **and `--config`** (per repo convention), fixture upstreams from the QA harness (ctl-server with DESC_FILE rug-pull), walk every sabotage cell live; scripted in quickstart.md.
- **code_execution / stored scripts interaction**: run a code_execution script and a stored script (spec 097) end-to-end on the feature branch; assert unchanged behavior + byte-identical `tools/list` snapshots across the three routing modes; document the REST-from-harness composition pattern.
- **Activity-log transparency**: every sabotage-cell run followed by `mcpproxy activity list --request-id` assertion + Web UI activity view spot-check.
- **Cross-model review**: opencode (gpt-5.6-sol) on the full diff; fix→re-review ≤5 rounds per project cap.

## Complexity Tracking

No constitution violations — table intentionally empty.
