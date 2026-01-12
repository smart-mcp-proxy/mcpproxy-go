# Implementation Plan: Expand Activity Log

**Branch**: `024-expand-activity-log` | **Date**: 2026-01-12 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/024-expand-activity-log/spec.md`

## Summary

Expand the Activity Log to capture system lifecycle events (start/stop), internal tool calls (retrieve_tools, call_tool_*, code_execution), and configuration changes. Enhance the Web UI with multi-select event type filtering, an Intent column, and sortable columns. Extend CLI to support comma-separated event type filtering. Update Docusaurus documentation.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10) + TypeScript 5.x / Vue 3.5
**Primary Dependencies**: Cobra CLI, Chi router, BBolt storage, Zap logging, mark3labs/mcp-go, Vue 3, Tailwind CSS, DaisyUI
**Storage**: BBolt database (`~/.mcpproxy/config.db`) - ActivityRecord model
**Testing**: `go test`, E2E via `./scripts/test-api-e2e.sh`, Claude browser automation for Web UI
**Target Platform**: macOS, Linux, Windows desktop
**Project Type**: Web application (Go backend + Vue frontend)
**Performance Goals**: Activity records queryable within 100ms for up to 10,000 records; SSE events delivered within 50ms
**Constraints**: Non-blocking activity recording; existing retention policy (7 days, 10,000 records)
**Scale/Scope**: Up to 1,000 tools across multiple upstream servers; Activity Log handles ~10,000 records

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | ✅ PASS | Activity recording is non-blocking via event-driven architecture. No changes to tool indexing/search. |
| II. Actor-Based Concurrency | ✅ PASS | ActivityService uses goroutine + channel pattern. New event types use same pattern. |
| III. Configuration-Driven Architecture | ✅ PASS | No new config settings required. Uses existing activity retention config. |
| IV. Security by Default | ✅ PASS | Activity logging enhances auditability. No security regressions. |
| V. Test-Driven Development (TDD) | ✅ PASS | Tests required for new event types, API changes, and Web UI features. |
| VI. Documentation Hygiene | ✅ PASS | Documentation updates explicitly required in spec (FR-027 to FR-030). |

**Architecture Constraints:**

| Constraint | Status | Notes |
|------------|--------|-------|
| Core + Tray Split | ✅ PASS | Changes are in core only. Tray receives updates via SSE. |
| Event-Driven Updates | ✅ PASS | New event types use existing EventBus pattern. |
| DDD Layering | ✅ PASS | Storage models in `internal/storage/`, service in `internal/runtime/`, API in `internal/httpapi/`. |
| Upstream Client Modularity | ✅ N/A | No changes to upstream client layers. |

## Project Structure

### Documentation (this feature)

```text
specs/024-expand-activity-log/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
└── tasks.md             # Phase 2 output (via /speckit.tasks)
```

### Source Code (repository root)

```text
# Backend (Go)
internal/
├── storage/
│   └── activity_models.go    # Extended ActivityType enum
├── runtime/
│   ├── activity_service.go   # Extended handlers for new event types
│   └── events.go             # New EventType constants
├── httpapi/
│   └── activity.go           # Multi-type filter support in API
└── server/
    └── mcp_handler.go        # Emit internal_tool_call events

cmd/mcpproxy/
├── activity_cmd.go           # Extended --type flag validation
└── serve_cmd.go              # Emit system_start/system_stop events

# Frontend (Vue)
frontend/src/
├── views/
│   └── Activity.vue          # Multi-select filter, Intent column, sortable columns
├── components/
│   ├── SidebarNav.vue        # Enable Activity Log menu item
│   └── JsonViewer.vue        # Already exists with syntax highlighting
└── types/
    └── api.ts                # Extended ActivityType enum

# Documentation (Docusaurus)
docs/
├── features/
│   └── activity-log.md       # Document new event types
└── cli/
    └── activity-commands.md  # Document multi-type filtering

# Tests
internal/
├── storage/activity_test.go  # Tests for new activity types
├── runtime/activity_service_test.go  # Tests for event handlers
└── httpapi/activity_test.go  # Tests for multi-type filter API
```

**Structure Decision**: Existing web application structure with Go backend and Vue frontend. Changes extend existing files rather than creating new ones.

## Complexity Tracking

No constitution violations requiring justification. The implementation extends existing patterns:

- New ActivityType constants extend existing enum
- New event handlers follow existing ActivityService pattern
- Multi-type filtering extends existing filter logic
- Web UI changes extend existing Activity.vue component

## Constitution Check (Post-Design)

*Re-evaluation after Phase 1 design artifacts completed.*

| Principle | Status | Post-Design Notes |
|-----------|--------|-------------------|
| I. Performance at Scale | ✅ PASS | Multi-type filter uses OR logic on iteration; pagination limits to 100 records so performance impact minimal. Client-side sorting handles max 100 records. |
| II. Actor-Based Concurrency | ✅ PASS | All new event emissions go through existing EventBus channel. ActivityService handles events in single goroutine. |
| III. Configuration-Driven Architecture | ✅ PASS | No new config required. All new features use existing infrastructure. |
| IV. Security by Default | ✅ PASS | Config change logging enhances audit trail. Internal tool call logging provides visibility into agent behavior. |
| V. Test-Driven Development (TDD) | ✅ PASS | quickstart.md defines test plan. Unit tests for new types, E2E for API, manual UI testing via browser automation. |
| VI. Documentation Hygiene | ✅ PASS | Documentation files identified in quickstart.md. Docusaurus updates planned for features and CLI sections. |

**Architecture Constraints (Post-Design):**

| Constraint | Status | Post-Design Notes |
|------------|--------|-------------------|
| Core + Tray Split | ✅ PASS | All changes in core server. SSE delivers events to tray/web UI. No tray code changes needed. |
| Event-Driven Updates | ✅ PASS | Four new EventType constants. ActivityService extended to handle config_change events. |
| DDD Layering | ✅ PASS | data-model.md confirms: storage models, runtime service, httpapi handler, server emission points. |
| Upstream Client Modularity | ✅ N/A | No changes to upstream client layers. |

## Phase 1 Artifacts

| Artifact | Path | Status |
|----------|------|--------|
| Research | [research.md](./research.md) | Complete |
| Data Model | [data-model.md](./data-model.md) | Complete |
| API Contracts | [contracts/activity-api-changes.yaml](./contracts/activity-api-changes.yaml) | Complete |
| Quickstart | [quickstart.md](./quickstart.md) | Complete |

## Next Steps

1. Run `/speckit.tasks` to generate tasks.md
2. Implement in order defined in quickstart.md
3. Test using Claude browser automation for Web UI and Bash for CLI
