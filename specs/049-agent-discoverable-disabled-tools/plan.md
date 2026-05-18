# Implementation Plan: Agent-Discoverable Disabled Tools

**Branch**: `049-agent-discoverable-disabled-tools` | **Date**: 2026-05-18 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/049-agent-discoverable-disabled-tools/spec.md`

## Summary

Add an opt-in `include_disabled` parameter to `retrieve_tools` so agents can, on
demand, discover tools that exist but are not callable, each tagged with a
5-state `status` and a once-per-response `remediation` map. Add a conditional
per-server tool-counts block to `upstream_servers` list/get. Make the
`TOOL_BLOCKED` rejection status-aware (config-policy vs user-disable) and add a
zero-callable-result nudge. Pure request-time classification reusing the
`Runtime.IsToolConfigDenied` signal from #468 — no enforcement change, no new
persistent storage, default output byte-for-byte unchanged.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10)
**Primary Dependencies**: `mark3labs/mcp-go` (MCP protocol), Chi router, BBolt
(read-only here), Zap, existing Bleve index
**Storage**: None new. Classification computed at request time from config +
existing `ToolApprovalRecord` (BBolt, read-only) + StateView snapshot.
**Testing**: `go test ./internal/...` (unit + integration),
`./scripts/test-api-e2e.sh`, manual curl + live MCP connection verification per
user request.
**Target Platform**: Linux/macOS/Windows core server (`mcpproxy serve`)
**Project Type**: single (Go backend); no frontend work (UI lock badges already
shipped in #468).
**Performance Goals**: Constitution I — discovery still <100ms for 1k tools;
classification is O(results) using the same per-tool lookups discovery already
performs (`isToolCallable` path) plus one `IsToolConfigDenied` (linear over
configured servers, already used by #468).
**Constraints**: Default path byte-for-byte unchanged (SC-001); locked portion
of any response ≤ 10 entries (SC-004); telemetry in-memory only (FR-013).
**Scale/Scope**: ~3 backend touch points (retrieve_tools handler,
upstream_servers list/get, TOOL_BLOCKED message — last already partly done in
#468), 1 new pure classifier, no schema/migration.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | PASS | Request-time only; reuses lookups discovery already performs; cap bounds payload. Add a micro-benchmark guard for the classify loop at 1k tools. |
| II. Actor-Based Concurrency | PASS | Read-only at request time; no new locks/mutexes; no new goroutines; respects context. |
| III. Configuration-Driven | PASS | `include_disabled` is a request parameter, not config. No new config keys. Reuses `enabled_tools`/`disabled_tools` (#468). |
| IV. Security by Default | PASS | Discovery/observability only — `isToolCallable` enforcement untouched (FR-011). Agent server-scope filter applied BEFORE classification (FR-007). No new exposure for inaccessible servers. |
| V. Test-Driven Development | PASS | Plan mandates tests-first: classifier table test, retrieve_tools regression + opt-in tests, upstream_servers conditional-counts test, nudge test — all written before handler edits. |
| VI. Documentation Hygiene | PASS | Tasks include OAS regen + `verify-oas-coverage.sh`, CLAUDE.md/docs note, quickstart.md. |

No violations. Complexity Tracking section intentionally empty.

## Project Structure

### Documentation (this feature)

```text
specs/049-agent-discoverable-disabled-tools/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output (curl + MCP verification recipe)
├── contracts/           # Phase 1 output (retrieve_tools + upstream_servers deltas)
├── checklists/
│   └── requirements.md  # speckit.specify output (all pass)
└── tasks.md             # speckit.tasks output (NOT created here)
```

### Source Code (repository root)

```text
internal/
├── server/
│   ├── mcp.go                          # retrieve_tools handler: split callableResults
│   │                                   #   vs disabledResults; cap; remediation map;
│   │                                   #   0-result nudge. blockedToolMessage already
│   │                                   #   status-aware (landed via #468 PR).
│   │                                   # upstream_servers list/get: conditional counts.
│   └── mcp_disabled_discovery_test.go  # NEW: opt-in on/off, ordering, cap, remediation
│                                       #   keys, agent-scope-before-classify, nudge
├── runtime/
│   ├── tool_quarantine.go              # NEW ClassifyDisabledTool(server,tool)->status
│   └── tool_disabled_classify_test.go  # NEW: 5-state precedence table + unknown fallback
└── contracts/
    └── types.go                        # extend discovery/list shapes (status,
                                         #   remediation, per-server counts) — additive

oas/
├── swagger.yaml                        # regenerated
└── docs.go                             # regenerated
```

**Structure Decision**: Single Go project. All changes are backend under
`internal/`. No frontend (UI lock affordances shipped in #468). The single new
unit is a pure classifier in `internal/runtime` consumed by both MCP surfaces in
`internal/server/mcp.go`, keeping one source of truth for "why is this tool not
callable" (mirrors the design's component boundary).

## Complexity Tracking

No constitution violations — section intentionally empty.
