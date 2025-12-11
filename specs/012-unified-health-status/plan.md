# Implementation Plan: Unified Health Status

**Branch**: `012-unified-health-status` | **Date**: 2025-12-11 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/012-unified-health-status/spec.md`

**Note**: This template is filled in by the `/speckit.plan` command. See `.specify/templates/commands/plan.md` for the execution workflow.

## Summary

Implement a unified health status calculation in the backend that provides consistent health information (level, admin state, summary, action) across all four interfaces: CLI, tray, web UI, and MCP tools. The backend calculates health once using a deterministic priority-based algorithm, and all interfaces render the same `HealthStatus` struct. This eliminates the current inconsistency where different interfaces calculate status independently from raw fields.

## Technical Context

**Language/Version**: Go 1.24.0
**Primary Dependencies**: mcp-go (MCP protocol), zap (logging), chi (HTTP router), Vue 3/TypeScript (frontend)
**Storage**: BBolt embedded database (`~/.mcpproxy/config.db`) - existing, no schema changes
**Testing**: `go test`, `./scripts/test-api-e2e.sh`, `./scripts/run-all-tests.sh`
**Target Platform**: macOS, Linux, Windows (cross-platform)
**Project Type**: Backend (Go) + Frontend (Vue 3 SPA)
**Performance Goals**: Health calculation <1ms per server (already fast lock-free StateView reads)
**Constraints**: Must not break existing API responses; health field is additive
**Scale/Scope**: 10-50 upstream servers typical; tested up to 1000 tools

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| **I. Performance at Scale** | ✅ PASS | Health calculation is O(1) per server using existing lock-free StateView; no additional queries required |
| **II. Actor-Based Concurrency** | ✅ PASS | No new locks or mutexes; health calculated from existing StateView snapshot (immutable) |
| **III. Configuration-Driven Architecture** | ✅ PASS | Expiry warning threshold will be configurable via `mcp_config.json` |
| **IV. Security by Default** | ✅ PASS | No security changes; health status only exposes what's already accessible |
| **V. Test-Driven Development** | ✅ PASS | Will add unit tests for `CalculateHealth()`, integration tests for API response, E2E tests for CLI |
| **VI. Documentation Hygiene** | ✅ PASS | Will update CLAUDE.md, OpenAPI spec, and inline code comments |

**Architecture Constraints:**

| Constraint | Status | Notes |
|------------|--------|-------|
| Core + Tray Split | ✅ PASS | Core calculates health; tray/web UI render via SSE/REST API |
| Event-Driven Updates | ✅ PASS | Existing `servers.changed` event will include health status |
| DDD Layering | ✅ PASS | Health calculator is domain logic; placed in new `internal/health/` package |
| Upstream Client Modularity | N/A | No changes to upstream client layers |

## Project Structure

### Documentation (this feature)

```text
specs/012-unified-health-status/
├── plan.md              # This file (/speckit.plan command output)
├── research.md          # Phase 0 output (/speckit.plan command)
├── data-model.md        # Phase 1 output (/speckit.plan command)
├── quickstart.md        # Phase 1 output (/speckit.plan command)
├── contracts/           # Phase 1 output (/speckit.plan command)
│   └── api.yaml         # OpenAPI additions for health field
└── tasks.md             # Phase 2 output (/speckit.tasks command - NOT created by /speckit.plan)
```

### Source Code (repository root)

```text
internal/
├── contracts/
│   └── types.go              # Add HealthStatus struct
├── health/                   # NEW: Health calculation domain logic
│   ├── calculator.go         # CalculateHealth() function
│   └── calculator_test.go    # Unit tests
├── runtime/
│   └── runtime.go            # Integrate health calculation in GetAllServers()
├── httpapi/
│   └── server.go             # Health field already included via contracts.Server
└── server/
    └── mcp.go                # Add health to handleListUpstreams() response

cmd/mcpproxy/
├── upstream_cmd.go           # Update `upstream list` display
└── auth_cmd.go               # Update `auth status` display

frontend/
└── src/
    ├── components/
    │   └── ServerCard.vue    # Use health.level for badge color, show action
    └── views/
        └── Dashboard.vue     # Show "X servers need attention" banner
```

**Structure Decision**: This feature touches existing backend (Go) and frontend (Vue) code. No new top-level directories; health calculation is a new package under `internal/health/`. All other changes modify existing files.

## Complexity Tracking

No constitution violations. All changes align with existing architecture:

- No new abstractions beyond simple `CalculateHealth()` function
- No new dependencies
- No new storage requirements
- No new concurrency patterns
