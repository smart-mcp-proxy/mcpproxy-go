# Implementation Plan: Activity Log Web UI

**Branch**: `019-activity-webui` | **Date**: 2025-12-29 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/019-activity-webui/spec.md`

## Summary

Implement a comprehensive Activity Log Web UI for MCPProxy that provides real-time visibility into AI agent tool calls, policy decisions, and server changes. The UI includes a dedicated Activity page with filtering, pagination, and detail views, plus a dashboard widget for at-a-glance monitoring. Real-time updates are delivered via SSE events.

## Technical Context

**Language/Version**: TypeScript 5.9, Vue 3.5, Go 1.24 (backend already exists)
**Primary Dependencies**: Vue 3, Vue Router 4, Pinia 2, Tailwind CSS 3, DaisyUI 4, Vite 5
**Storage**: N/A (frontend consumes REST API from backend)
**Testing**: Vitest, Vue Test Utils, Playwriter (E2E browser testing)
**Target Platform**: Modern web browsers (Chrome, Firefox, Safari, Edge)
**Project Type**: Web application (Vue 3 SPA consuming Go REST API)
**Performance Goals**: Page load <2s, SSE update latency <500ms, filter response <1s
**Constraints**: Must integrate with existing SSE event system, follow DaisyUI component patterns
**Scale/Scope**: Display up to 10,000 activity records with pagination, support 100+ records per page

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | ✅ PASS | Pagination limits records per request; SSE batching prevents UI thrashing |
| II. Actor-Based Concurrency | ✅ N/A | Frontend-only; backend already uses goroutines + channels |
| III. Configuration-Driven Architecture | ✅ PASS | No new config needed; uses existing API endpoints |
| IV. Security by Default | ✅ PASS | Uses existing API key authentication; no new security surface |
| V. Test-Driven Development | ✅ PASS | Vitest unit tests + Playwriter E2E tests planned |
| VI. Documentation Hygiene | ✅ PASS | Will update Docusaurus docs after implementation |

**Architecture Constraints**:

| Constraint | Status | Notes |
|------------|--------|-------|
| Core + Tray Split | ✅ N/A | Web UI only - no tray changes |
| Event-Driven Updates | ✅ PASS | Will add SSE listeners for activity events |
| DDD Layering | ✅ PASS | Frontend follows component-based architecture |
| Upstream Client Modularity | ✅ N/A | No upstream client changes |

## Project Structure

### Documentation (this feature)

```text
specs/019-activity-webui/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output (TypeScript interfaces)
└── tasks.md             # Phase 2 output (/speckit.tasks command)
```

### Source Code (repository root)

```text
frontend/
├── src/
│   ├── components/
│   │   └── ActivityWidget.vue       # NEW: Dashboard activity widget
│   ├── views/
│   │   └── Activity.vue             # NEW: Activity Log page
│   ├── services/
│   │   └── api.ts                   # MODIFY: Add activity API methods
│   ├── stores/
│   │   └── system.ts                # MODIFY: Add activity SSE listeners
│   ├── types/
│   │   └── api.ts                   # MODIFY: Add activity types
│   └── router/
│       └── index.ts                 # MODIFY: Add /activity route
└── tests/
    └── unit/
        └── activity.spec.ts         # NEW: Activity component tests

docs/                                # Docusaurus documentation
└── docs/
    └── web-ui/
        └── activity-log.md          # NEW: Activity Log documentation
```

**Structure Decision**: Extends existing Vue 3 frontend architecture with new Activity page and widget components. Follows established patterns from ToolCalls.vue and Dashboard.vue.

## Complexity Tracking

> No constitution violations - no complexity justification needed.
