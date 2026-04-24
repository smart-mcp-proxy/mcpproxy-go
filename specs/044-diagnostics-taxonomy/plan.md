# Implementation Plan: Diagnostics & Error Taxonomy

**Branch**: `feat/diagnostics-taxonomy` (spec dir `044-diagnostics-taxonomy`)
**Date**: 2026-04-24
**Spec**: [spec.md](./spec.md)
**Design**: [../../docs/superpowers/specs/2026-04-24-diagnostics-error-taxonomy-design.md](../../docs/superpowers/specs/2026-04-24-diagnostics-error-taxonomy-design.md)

## Summary

Add a stable error-code catalog (`MCPX_<DOMAIN>_<SPECIFIC>`) that maps every terminal failure in mcpproxy's upstream, OAuth, Docker, config, and network paths to a code with a user-facing message, fix steps, severity, and docs URL. Surface these on the per-server diagnostics REST endpoint, in the Vue web UI (ErrorPanel), macOS tray (badge + Fix Issues menu), and CLI (`mcpproxy doctor fix`). Fixes are never auto-invoked; destructive fixes default to dry-run. Telemetry v3 gains a `diagnostics` sub-object (counters in memory only).

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10), TypeScript 5.9 / Vue 3.5, Swift 5.9 (macOS 13+)
**Primary Dependencies** (Go): Cobra CLI, Chi router, Zap logging, BBolt storage, `mark3labs/mcp-go` (existing); no new third-party Go deps
**Primary Dependencies** (frontend): Vue 3, Pinia 2, Tailwind CSS, DaisyUI 4, Vite 5 (existing)
**Primary Dependencies** (tray): SwiftUI + AppKit (existing)
**Storage**: No new persistent storage. Diagnostic state lives on in-memory stateview snapshot. Fix-attempt audit rows reuse existing activity log (`ActivityBucket` in BBolt). Telemetry counters are in-memory only (consistent with spec 042).
**Testing**: `go test ./internal/diagnostics/... -race`, extension of `./scripts/test-api-e2e.sh`, Vue component tests (vitest), Swift unit tests, `mcpproxy-ui-test` MCP tools for tray verification, `claude-in-chrome` for web UI.
**Target Platform**: macOS 13+ (tray), cross-platform core (macOS/Linux/Windows), modern browsers for web UI.
**Project Type**: Web application (backend Go + Vue frontend) plus native macOS tray.
**Performance Goals**: Per-server diagnostics endpoint p95 < 50 ms (SC-003). Catalog registry lookup O(1).
**Constraints**: No auto-remediation (FR-021). Additive API only (FR-007). Destructive fixes dry-run by default (FR-022). Fix endpoint rate-limited 1/s per (server, code) (FR-008).
**Scale/Scope**: Initial catalog target ≥ 25 codes across 7 domains. Fix attempts: single-digit per user per day typical; rate limit handles accidental burst.

## Constitution Check

Evaluated against `.specify/memory/constitution.md` v1.1.0:

| Principle | Status | Notes |
|---|---|---|
| I. Performance at Scale | PASS | Diagnostics lookup is O(1) in-memory map; endpoint p95 target 50 ms documented in SC-003. No indexing or search impact. |
| II. Actor-Based Concurrency | PASS | DiagnosticError stored on existing stateview snapshot (already actor-managed). Fix endpoint synchronizes per-(server,code) via rate-limiter + activity log serialization; no new locks required on the hot path. |
| III. Configuration-Driven Architecture | PASS | No new config is mandatory; catalog is compiled-in code (stable by FR-004). Optional future config for disabling fix UI is not in scope. No tray-local state introduced. |
| IV. Security by Default | PASS | No auto-remediation (FR-021). Destructive fixes dry-run by default (FR-022). Fix attempts audited in activity log (FR-023). No weakening of quarantine, API-key, or localhost-binding defaults. |
| V. Test-Driven Development | PASS | Registry completeness test (FR-003), classifier golden-sample tests, E2E test extension, and web/tray verification all scheduled in tasks.md phase breakdown. |
| VI. Documentation Hygiene | PASS | Each code has a `docs/errors/<CODE>.md` page (FR-016). CI link-check (FR-017). `CLAUDE.md` updated if architecture changes. `docs/api/rest-api.md` updated for new endpoint. |

**Architecture constraints**:
- **Core + Tray Split**: Swift tray reads the new `error_code`/`severity` fields from the existing REST snapshot; fix logic stays in core. Tray does NOT maintain its own state. PASS.
- **Event-Driven Updates**: Diagnostic state changes flow through existing `servers.changed` events; no new event type required. Web UI ErrorPanel subscribes via existing SSE stream. PASS.
- **DDD Layering**: `internal/diagnostics/` sits in the domain layer (pure logic — catalog + classifier). Integrations in `internal/upstream/`, `internal/oauth/`, and `internal/httpapi/` respect existing boundaries. PASS.
- **Upstream Client Modularity**: No changes to core/managed/cli client layering; only `managed` wraps its terminal errors in a `DiagnosticError`. PASS.

**Gate result**: All gates PASS. No complexity-tracking entries required.

## Project Structure

### Documentation (this feature)

```text
specs/044-diagnostics-taxonomy/
├── spec.md              # User-facing feature specification
├── plan.md              # This file
├── research.md          # Phase 0 — open questions resolved
├── data-model.md        # Phase 1 — catalog entry + diagnostic error shapes
├── contracts/
│   ├── diagnostics-openapi.yaml     # OpenAPI fragment for /diagnostics + /diagnostics/fix
│   └── catalog-schema.json          # JSON schema for CatalogEntry
├── quickstart.md        # Developer onboarding + E2E smoke path
├── checklists/
│   └── requirements.md  # Spec quality checklist (written by /speckit.specify)
└── tasks.md             # Phase 2 — generated by /speckit.tasks
```

### Source Code (repository root)

```text
internal/diagnostics/                 # NEW — domain package
├── catalog.go                        # CatalogEntry type, registry getter
├── codes.go                          # All MCPX_* code constants
├── classifier.go                     # raw error → Code mapping
├── registry.go                       # in-memory registry population
├── fixers.go                         # fixer function registry (each fix keyed by code)
├── types.go                          # DiagnosticError, FixStep, Severity types
├── catalog_test.go                   # completeness + uniqueness tests (FR-003)
├── classifier_test.go                # golden-sample error → code tests
└── fixers_test.go                    # dry-run correctness tests

internal/upstream/
└── manager.go                        # MODIFIED — wrap terminal connection errors

internal/oauth/
└── *.go                              # MODIFIED — classify OAuth failures

internal/runtime/stateview/
└── stateview.go                      # MODIFIED — add DiagnosticError per server

internal/httpapi/
├── server.go                         # MODIFIED — register new routes
├── diagnostics_per_server.go         # NEW — GET /api/v1/servers/{name}/diagnostics
├── diagnostics_fix.go                # NEW — POST /api/v1/diagnostics/fix
└── diagnostics_per_server_test.go    # NEW — integration tests

cmd/mcpproxy/
├── doctor.go                         # MODIFIED — add --server flag and --codes option
├── doctor_fix.go                     # NEW — `doctor fix <CODE> --server <name>` subcommand
└── doctor_list_codes.go              # NEW — `doctor list-codes` subcommand

frontend/src/
├── components/diagnostics/
│   ├── ErrorPanel.vue                # NEW
│   ├── FixStep.vue                   # NEW
│   └── ErrorPanel.test.ts            # NEW
├── views/ServerDetail.vue            # MODIFIED — render ErrorPanel
└── stores/servers.ts                 # MODIFIED — consume error_code from snapshot

native/macos/MCPProxy/
├── StatusBar/StatusBarController.swift  # MODIFIED — badge state
├── Menu/FixIssuesMenu.swift             # NEW — "Fix issues (N)" submenu
└── API/DiagnosticsDecoder.swift         # NEW — decode error_code/severity

docs/errors/                             # NEW
├── README.md                            # catalog index (auto-generated from registry)
├── MCPX_STDIO_SPAWN_ENOENT.md
├── MCPX_OAUTH_REFRESH_EXPIRED.md
├── ... (one per registered code)

scripts/
├── test-diagnostics-e2e.sh              # NEW — dedicated diagnostics E2E
└── check-errors-docs-links.sh           # NEW — CI link-check for docs/errors/
```

**Structure Decision**: Web application (backend + frontend) plus native tray — fits repo's existing split. All Go additions are under `internal/diagnostics/` (new domain package) with focused edits to `internal/upstream/`, `internal/oauth/`, `internal/runtime/stateview/`, `internal/httpapi/`, and `cmd/mcpproxy/`. Frontend additions are scoped to `frontend/src/components/diagnostics/` and `views/ServerDetail.vue`. Tray additions live under `native/macos/MCPProxy/` in focused new files.

## Phase 0 — Research

See [research.md](./research.md). Summary of resolved questions:
1. **Fix endpoint rate-limit implementation** → chi middleware + `sync/atomic` counter keyed by (server, code); in-memory, no new dependency.
2. **MCPX_DOCKER_SNAP_APPARMOR fix behavior** → offer both options as distinct fix_steps (switch Docker flavor, OR disable scanner for that server); no auto-remediation either way.
3. **OAuth re-auth concurrency** → reuse existing `internal/oauth/coordinator.go` singleflight; no new concurrency primitive.
4. **Catalog layout** → hand-written constants in `codes.go`; registry populated in package `init()`.
5. **Classifier strategy** → typed error-cause sentinels + `errors.As` in `classifier.go`; fall back to string matching only for third-party errors without structured types.
6. **Telemetry v3 dependency** → defer Phase H if spec 042 v3 client not merged at integration time; document the deferral.
7. **Docs generation** → `docs/errors/README.md` auto-generated from catalog via `go generate`; individual pages hand-written; CI link-checker asserts 1:1 code↔file correspondence.

## Phase 1 — Design & Contracts

Artifacts produced in this phase:

- **data-model.md** — formal shapes of `CatalogEntry`, `FixStep`, `Severity`, `DiagnosticError`, and `FixAttempt` activity-log row.
- **contracts/diagnostics-openapi.yaml** — OpenAPI 3.0 fragment for:
  - `GET /api/v1/servers/{name}/diagnostics` (extended response)
  - `POST /api/v1/diagnostics/fix` (new endpoint)
- **contracts/catalog-schema.json** — JSON schema that mirrors the CatalogEntry Go type; used for the `doctor list-codes --json` output contract and for docs-generation tooling.
- **quickstart.md** — developer-focused: "How to add a new error code in 5 steps" plus E2E smoke walkthrough.

### Agent context update

Ran `.specify/scripts/bash/update-agent-context.sh claude` to refresh CLAUDE.md's Active Technologies with: "044-diagnostics-taxonomy: internal/diagnostics package, per-server diagnostics REST, docs/errors/ catalog".

### Re-evaluated Constitution Check (post-design)

No new deviations. Gates remain PASS.

## Phase 2 — Tasks (out of scope for this command)

Task breakdown will be produced by `/speckit.tasks` and stored in `tasks.md`. High-level outline (for reference only; authoritative list lives in tasks.md):

1. Error inventory sweep → seed `codes.go` + `registry.go`.
2. Catalog completeness test + classifier skeleton (STDIO first).
3. Wire `DiagnosticError` into upstream manager + stateview snapshot.
4. Extend `/api/v1/servers/{name}/diagnostics` response; add E2E in `test-diagnostics-e2e.sh`.
5. Add `POST /api/v1/diagnostics/fix` with dry-run default and rate-limit.
6. Domain classifiers: OAUTH, HTTP, DOCKER, CONFIG, QUARANTINE, NETWORK.
7. Frontend `ErrorPanel.vue` + `FixStep.vue` + `ServerDetail.vue` wiring; visual verification via `claude-in-chrome`.
8. macOS tray badge + "Fix Issues" menu; visual verification via `mcpproxy-ui-test`.
9. CLI `doctor fix`, `doctor list-codes`, `doctor --server` extensions.
10. Docs pages + link-check script.
11. Telemetry v3 `diagnostics` sub-object (deferred if spec 042 not merged).
12. PR assembly + verification report.

## Complexity Tracking

No Constitution violations — table intentionally empty.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|---|---|---|
| _(none)_ | — | — |
