# Implementation Plan: Structured Server State

**Branch**: `013-structured-server-state` | **Date**: 2025-12-13 | **Updated**: 2025-12-16 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/013-structured-server-state/spec.md`

## Summary

**Prior Work Completed** (merged via #192):
- ✅ `HealthStatus` struct and `health.CalculateHealth()` function
- ✅ `Health` field on `contracts.Server`
- ✅ Frontend health status integration in ServerCard.vue and Dashboard.vue
- ✅ Action buttons (Login, Restart, Enable, Approve) working

**Remaining Work**:
1. Add structured state objects (`OAuthState`, `ConnectionState`) to Server for richer state info
2. Refactor `Doctor()` to aggregate from `server.Health` instead of raw field access
3. Consolidate Dashboard UI to remove duplicate diagnostics section

## Technical Context

**Language/Version**: Go 1.24.0
**Primary Dependencies**: mcp-go (MCP protocol), zap (logging), chi (HTTP router), Vue 3/TypeScript (frontend)
**Storage**: BBolt embedded database (`~/.mcpproxy/config.db`) - no schema changes
**Testing**: go test, ./scripts/test-api-e2e.sh, ./scripts/run-all-tests.sh
**Target Platform**: macOS, Linux, Windows (desktop)
**Project Type**: Web application (Go backend + Vue frontend)
**Performance Goals**: Health calculation <10ms per server (SC-006)
**Constraints**: Backwards compatibility with existing flat fields (FR-003)
**Scale/Scope**: Up to 1,000 servers (from constitution)

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | PASS | Health calculation <10ms per server; no blocking operations |
| II. Actor-Based Concurrency | PASS | Health calculated on request from existing state; no new goroutines needed |
| III. Configuration-Driven | PASS | No new configuration required; uses existing server state |
| IV. Security by Default | N/A | Internal refactor; no new security surface |
| V. Test-Driven Development | PASS | Unit tests for new types; E2E tests for API backwards compat |
| VI. Documentation Hygiene | PASS | Update CLAUDE.md with new types; update API docs |

| Architecture Constraint | Status | Notes |
|------------------------|--------|-------|
| Core + Tray Split | PASS | Changes in core only; tray consumes via existing API |
| Event-Driven Updates | PASS | Health changes propagate via existing SSE events |
| DDD Layering | PASS | New types in contracts; health logic in health package |
| Upstream Client Modularity | PASS | No changes to 3-layer client design |

**Gate Result**: PASS - No violations requiring justification

## Project Structure

### Documentation (this feature)

```text
specs/013-structured-server-state/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
└── tasks.md             # Phase 2 output (/speckit.tasks)
```

### Source Code (repository root)

```text
# Backend (Go) - DONE
internal/
├── contracts/
│   └── types.go          # ✅ HealthStatus struct exists
├── health/
│   ├── calculator.go     # ✅ CalculateHealth() implemented
│   └── constants.go      # ✅ Level/AdminState/Action constants

# Backend (Go) - REMAINING
internal/
├── contracts/
│   └── types.go          # TODO: Add OAuthState, ConnectionState types
├── management/
│   └── diagnostics.go    # TODO: Refactor Doctor() to use Health
├── upstream/
│   ├── manager.go        # TODO: Populate structured state objects
│   └── types/types.go    # ConnectionInfo already has most data (source)
└── httpapi/
    └── server.go         # TODO: Serialize new fields in server responses

# Frontend (Vue) - DONE
frontend/src/
├── types/api.ts          # ✅ HealthStatus interface exists
├── components/
│   └── ServerCard.vue    # ✅ Uses health.action for buttons
└── views/
    └── Dashboard.vue     # ✅ "Servers Needing Attention" banner works

# Frontend (Vue) - REMAINING
frontend/src/
├── types/api.ts          # TODO: Add OAuthState, ConnectionState interfaces
└── views/
    └── Dashboard.vue     # TODO: Remove duplicate diagnostics section

# Tests
internal/
├── health/calculator_test.go      # ✅ Tests exist
└── management/diagnostics_test.go # TODO: Update for aggregation
```

**Structure Decision**: Web application structure - Go backend with Vue frontend. Health calculation is complete; remaining work is state objects and UI consolidation.

## Complexity Tracking

No constitution violations - section not required.
