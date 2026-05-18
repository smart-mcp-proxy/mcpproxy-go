# Implementation Plan: Global Tools Overview Page

**Branch**: `050-global-tools-page` | **Date**: 2026-05-18 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/050-global-tools-page/spec.md`

## Summary

Add one consolidated read endpoint, `GET /api/v1/tools`, that returns every tool from every configured server enriched with approval/disabled/config-denied state plus per-tool usage (count + last-used) aggregated from the activity log over a fixed 30-day window. Surface it three ways: a rewritten web Tools page (replacing the orphaned `frontend/src/views/Tools.vue`) modeled on `Activity.vue` with summary cards, filter bar, sortable table, and batch enable/disable; and CLI parity by extending the existing `mcpproxy tools` group with a global `tools list` (no `--server`, filters, JSON/YAML) and `tools enable|disable <server:tool ...>` batch subcommands. The agent-facing BM25 discovery path is untouched.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10); TypeScript 5.9 / Vue 3.5 (frontend)
**Primary Dependencies**: Chi router, BBolt (`go.etcd.io/bbolt`), Zap, Cobra (CLI), `mark3labs/mcp-go`; Vue 3, Pinia, Vue Router, Tailwind/DaisyUI, Vite — all existing, no new deps
**Storage**: BBolt `~/.mcpproxy/config.db` — read-only on the hot path. Reuses existing `ActivityRecordsBucket` (aggregation pass) and `ToolApprovalBucket` (enrichment). No schema change, no migration.
**Testing**: `go test -race ./...` (table tests), `./scripts/test-api-e2e.sh` (API E2E), Playwright UI sweep with self-contained HTML report
**Target Platform**: Cross-platform desktop/server (personal + server editions, same code path; no build-tagged code)
**Project Type**: web (Go backend + Vue frontend) + CLI
**Performance Goals**: Per Constitution I (≤1000 tools): aggregation endpoint responds well under typical web latency; client-side filter/sort/search over the full set updates in <1s at 700 tools (SC-003). Usage aggregation is a single bounded cursor pass over the activity bucket.
**Constraints**: No change to BM25/`retrieve_tools` behavior (FR-016/SC-006). Disabled & config-denied tools MUST remain listed (FR-001/FR-007). 30-day window fixed in v1.
**Scale/Scope**: 12+ servers, 500–700 tools typical; one new endpoint, one new storage method, one rewritten Vue view + router/sidebar wiring, three CLI subcommands.

## Constitution Check

*GATE: re-checked after Phase 1 design — still passing.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | PASS | Single bounded cursor pass for usage; endpoint reuses the existing per-server enrichment loop (already used by the tool-export endpoint). No new per-tool DB round-trips beyond the existing approval lookup. Client-side filter/sort avoids per-interaction server round-trips. |
| II. Actor-Based Concurrency | PASS | Read-only endpoint. No new goroutines/locks; reuses management service + storage `View` transactions. No shared mutable state. |
| III. Configuration-Driven Architecture | PASS | No tray state. Frontend reads via REST. 30-day window is a documented constant, not new config — keeps v1 surface minimal per spec Out-of-Scope. |
| IV. Security by Default | PASS | Read endpoint behind existing API-key auth. Batch enable/disable reuses the existing authenticated per-tool endpoint; a config-denied tool cannot be force-enabled (server enforces). BM25/quarantine untouched. |
| V. Test-Driven Development | PASS | Backend: failing table tests first for `AggregateToolUsage` + handler. CLI: command tests incl. partial-failure exit code. UI: Playwright sweep. E2E extended. `golangci-lint` clean. |
| VI. Documentation Hygiene | PASS | Update `CLAUDE.md`, `oas/swagger.yaml`, `docs/api/rest-api.md`, `docs/cli-management-commands.md`, hints panel; commit verification report. |

No violations → Complexity Tracking not required.

## Project Structure

### Documentation (this feature)

```text
specs/050-global-tools-page/
├── plan.md              # This file
├── spec.md              # Feature spec (committed)
├── research.md          # Phase 0 — decisions
├── data-model.md        # Phase 1 — entities & response shape
├── quickstart.md        # Phase 1 — manual verification recipe
├── contracts/
│   └── global-tools-api.md   # GET /api/v1/tools contract
├── checklists/requirements.md
├── execution_log.md     # autonomous-run state
├── verification/        # Playwright screenshots + report.html (added during impl)
└── tasks.md             # Phase 2 — /speckit.tasks output
```

### Source Code (repository root)

```text
internal/
├── storage/
│   ├── activity.go            # + AggregateToolUsage(since) — single cursor pass
│   └── activity_test.go       # + table tests (no activity / multi / window boundary / never-used)
├── contracts/
│   └── types.go               # + GlobalToolsResponse, GlobalToolsStats (Tool already has Usage/LastUsed)
├── httpapi/
│   ├── server.go              # + route GET /api/v1/tools + handleGetGlobalTools (mirrors export loop globally)
│   └── server_global_tools_test.go  # handler tests (multi-server merge, disabled servers, enrichment)
└── runtime / management       # thin pass-through to expose AggregateToolUsage to httpapi if needed

cmd/mcpproxy/
├── tools_cmd.go               # global `tools list` (no --server) + filters; `tools enable|disable <server:tool ...>`
└── tools_cmd_test.go          # + command tests incl. partial-failure exit code

frontend/src/
├── views/Tools.vue            # REWRITTEN: Activity.vue-style table (delete dead grid/list code)
├── router/index.ts            # + /tools route
├── services/api.ts            # + getGlobalTools()
└── (sidebar component)        # WORKSPACE "Tools" item with live tool-count badge

oas/swagger.yaml               # + /api/v1/tools
docs/                          # rest-api.md, cli-management-commands.md, CLAUDE.md
scripts/test-api-e2e.sh        # + assertion for GET /api/v1/tools shape+stats
```

**Structure Decision**: Web + CLI feature on the existing single-repo layout. No new packages — extend `internal/storage`, `internal/contracts`, `internal/httpapi`, `cmd/mcpproxy`, and the Vue app in place, following the established `GET /api/v1/servers/{id}/tools` → export-loop pattern (`server.go` ~4268) hoisted to global scope.
