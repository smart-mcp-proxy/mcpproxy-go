# Implementation Plan: Activity Log Backend

**Branch**: `016-activity-log-backend` | **Date**: 2025-12-26 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/016-activity-log-backend/spec.md`

## Summary

Implement a comprehensive activity logging system for MCPProxy that records all tool calls, policy decisions, and quarantine events. The system provides REST API endpoints for querying activity history with filtering and pagination, real-time SSE events for UI updates, and export capabilities for compliance. Built on existing BBolt storage, event bus, and SSE infrastructure.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10)
**Primary Dependencies**: BBolt (storage), Chi router (HTTP), Zap (logging), existing event bus
**Storage**: BBolt database (existing `~/.mcpproxy/config.db`)
**Testing**: Go test framework, existing E2E test infrastructure
**Target Platform**: Cross-platform (Linux, macOS, Windows)
**Project Type**: Single Go module with internal packages
**Performance Goals**:
- Query <100ms for 10,000 records (SC-001)
- SSE delivery <50ms (SC-002)
- Handle 100 concurrent tool calls without data loss (SC-003)
**Constraints**:
- Non-blocking recording (FR-019)
- Configurable retention (90 days default, 100K records max)
- Response truncation for large payloads
**Scale/Scope**: Up to 100,000 activity records with automatic pruning

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Evidence |
|-----------|--------|----------|
| I. Performance at Scale | PASS | Query <100ms for 10K records, non-blocking recording via event bus |
| II. Actor-Based Concurrency | PASS | Uses existing event bus (channels), async storage operations |
| III. Configuration-Driven | PASS | Retention policy configurable via config file |
| IV. Security by Default | PASS | Uses existing API key authentication |
| V. TDD | PASS | Unit + E2E tests required by spec |
| VI. Documentation Hygiene | PASS | CLAUDE.md update required, API docs generated |

**Architecture Constraints**:

| Constraint | Status | Evidence |
|------------|--------|----------|
| Core + Tray Split | PASS | Activity service in core, tray receives SSE events |
| Event-Driven Updates | PASS | Uses existing event bus for SSE propagation |
| DDD Layering | PASS | Storage (infra) → Runtime (app) → HttpAPI (presentation) |
| Upstream Client Modularity | N/A | No upstream client changes |

**No violations requiring justification.**

## Project Structure

### Documentation (this feature)

```text
specs/016-activity-log-backend/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output (OpenAPI)
└── tasks.md             # Phase 2 output (via /speckit.tasks)
```

### Source Code (repository root)

```text
internal/
├── storage/
│   ├── activity.go          # NEW: ActivityRecord storage operations
│   ├── activity_models.go   # NEW: ActivityRecord, ActivityType
│   └── manager.go           # Extended: Activity methods
├── runtime/
│   ├── activity_service.go  # NEW: Activity recording service
│   ├── events.go            # Extended: Activity event types
│   └── event_bus.go         # Extended: Activity event helpers
├── httpapi/
│   ├── activity.go          # NEW: REST handlers for /api/v1/activity
│   └── server.go            # Extended: Route registration
├── contracts/
│   └── activity.go          # NEW: API request/response types
└── server/
    └── mcp.go               # Extended: Emit activity events on tool calls

tests/
├── internal/storage/
│   └── activity_test.go     # NEW: Activity storage tests
├── internal/httpapi/
│   └── activity_test.go     # NEW: Activity API tests
└── e2e/
    └── activity_e2e_test.go # NEW: End-to-end activity tests
```

**Structure Decision**: Follows existing package layout with new files for activity functionality. No new packages needed - extends existing storage, runtime, and httpapi packages.

## Complexity Tracking

> No violations requiring justification.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| N/A | - | - |
