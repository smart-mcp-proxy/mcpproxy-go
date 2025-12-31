# Research: Activity Log Web UI

**Branch**: `019-activity-webui` | **Date**: 2025-12-29

## Overview

This document captures research findings for implementing the Activity Log Web UI feature. All technical decisions are based on existing codebase patterns and RFC-003 requirements.

## Research Findings

### 1. Backend API Endpoints (Already Implemented)

**Decision**: Use existing REST API endpoints from spec 016-activity-log-backend

**Endpoints Available**:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/v1/activity` | GET | List activities with filters (type, server, tool, session_id, status, intent_type, start_time, end_time, limit, offset) |
| `/api/v1/activity/{id}` | GET | Get single activity details |
| `/api/v1/activity/export` | GET | Export activities as JSON Lines or CSV (format, include_bodies params) |
| `/api/v1/activity/summary` | GET | Get aggregated statistics (period: 1h, 24h, 7d, 30d) |

**Rationale**: Backend already implements RFC-003 section 1.2 requirements. No new endpoints needed.

**Alternatives Considered**: None - backend is complete.

### 2. SSE Event Types for Real-time Updates

**Decision**: Subscribe to existing activity SSE event types

**Event Types** (from `internal/runtime/events.go`):

| Event Type | Description | Payload |
|------------|-------------|---------|
| `activity.tool_call.started` | Tool execution begins | server_name, tool_name, session_id, request_id, arguments |
| `activity.tool_call.completed` | Tool execution finishes | server_name, tool_name, status, error_message, duration_ms, response, response_truncated |
| `activity.policy_decision` | Policy blocks a tool call | server_name, tool_name, session_id, decision, reason |
| `activity.quarantine_change` | Server quarantine state changes | server_name, quarantined, reason |

**Rationale**: Events are already emitted by the runtime. Frontend just needs to add listeners.

**Alternatives Considered**: Polling - rejected due to latency and resource usage.

### 3. Frontend Component Architecture

**Decision**: Follow existing Vue 3 patterns from ToolCalls.vue and Dashboard.vue

**Component Structure**:

```
frontend/src/
├── views/
│   └── Activity.vue           # Main page component (like ToolCalls.vue)
├── components/
│   └── ActivityWidget.vue     # Dashboard widget (like recent tool calls section)
├── services/
│   └── api.ts                 # Add activity API methods
├── stores/
│   └── system.ts              # Add activity SSE listeners
└── types/
    └── api.ts                 # Add activity TypeScript interfaces
```

**Rationale**: Consistency with existing codebase. ToolCalls.vue provides excellent template for table with filters, pagination, and expandable rows.

**Alternatives Considered**:
- Separate activity store - rejected, system store already handles SSE
- New component library - rejected, DaisyUI patterns already established

### 4. Filter Options Endpoint

**Decision**: Build filter options client-side from fetched activities

**Rationale**: The spec references `/api/v1/activity/filter-options/{filter}` endpoint, but this doesn't exist in the backend. Instead, we'll:
1. Fetch activities with the current filters
2. Extract unique values for type, server, status from the response
3. Populate dropdowns dynamically

This matches the pattern used in ToolCalls.vue for server and session filters.

**Alternatives Considered**:
- Add new backend endpoint - requires backend changes, spec 016 is complete
- Hardcode filter options - loses dynamic server/tool discovery

### 5. Date Range Picker Implementation

**Decision**: Use native HTML5 date inputs with DaisyUI styling

**Implementation**:
```html
<input type="datetime-local" class="input input-bordered" />
```

**Rationale**:
- Native browser support is excellent in 2024+
- No additional dependencies needed
- DaisyUI styling integrates seamlessly

**Alternatives Considered**:
- Third-party date picker library (flatpickr, vue-datepicker) - adds dependencies
- Custom date picker component - unnecessary complexity

### 6. Pagination Strategy

**Decision**: Server-side pagination with limit/offset

**Implementation**:
- Default page size: 100 records
- Use existing API params: `limit` and `offset`
- Show: page N of M, total count
- Navigation: Previous, Next, first/last page

**Rationale**: Matches existing ToolCalls.vue pagination pattern. Server-side pagination handles large datasets efficiently.

**Alternatives Considered**:
- Client-side pagination - doesn't scale for 10,000+ records
- Infinite scroll - harder to navigate to specific records

### 7. Export Implementation

**Decision**: Direct link to `/api/v1/activity/export` with current filters

**Implementation**:
```typescript
const exportUrl = `/api/v1/activity/export?format=${format}&${currentFilters}`
window.open(exportUrl) // Browser handles download
```

**Rationale**: Backend already streams response with proper Content-Disposition header. No frontend processing needed.

**Alternatives Considered**:
- Fetch and create blob - unnecessary when backend already handles streaming
- Generate client-side - can't handle large datasets

### 8. Dashboard Widget Design

**Decision**: Compact summary card similar to "Recent Tool Calls" section

**Widget Contents**:
- Header: "Activity" with "View All" link
- Stats row: Total today, Success count, Error count
- List: 3 most recent activities (type icon, server:tool, relative time, status badge)

**Rationale**: Consistent with existing dashboard sections. Uses `/api/v1/activity/summary` for stats.

**Alternatives Considered**:
- Chart visualization - adds complexity, summary numbers are sufficient
- Separate widget component - yes, ActivityWidget.vue for reusability

### 9. Auto-refresh Toggle

**Decision**: Toggle controls SSE subscription, not polling

**Implementation**:
```typescript
const autoRefresh = ref(true)

watch(autoRefresh, (enabled) => {
  if (enabled) {
    subscribeToActivityEvents()
  } else {
    unsubscribeFromActivityEvents()
  }
})
```

**Rationale**: SSE is already established pattern. Toggle just controls whether we listen for events.

**Alternatives Considered**:
- Polling with interval - SSE is more efficient
- Always-on with visual buffer - harder to audit static state

### 10. Status Indicators

**Decision**: Use DaisyUI badge classes with consistent colors

| Status | Badge Class | Color |
|--------|-------------|-------|
| success | `badge-success` | Green |
| error | `badge-error` | Red |
| blocked | `badge-warning` | Orange/Yellow |

**Rationale**: Matches existing ToolCalls.vue status badges. Consistent visual language.

## Resolved Clarifications

All technical decisions have been made based on existing codebase patterns. No clarifications needed from spec - the RFC-003 section 1.4 requirements are clear and the backend implementation provides complete guidance.

## Dependencies

### Required (Already Available)
- Vue 3.5+ with Composition API
- Vue Router 4
- Pinia 2
- Tailwind CSS 3 + DaisyUI 4
- Existing SSE infrastructure in system.ts

### New Additions (This Feature)
- None - all dependencies already present

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Large datasets slow down UI | Medium | Medium | Pagination (100 records/page), truncated responses |
| SSE connection drops | Low | Low | Existing auto-reconnect in system.ts |
| Export timeout for large datasets | Low | Medium | Backend already streams; add progress indicator if needed |

## Next Steps

1. **Phase 1**: Generate data-model.md with TypeScript interfaces
2. **Phase 1**: Generate contracts/ with API client methods
3. **Phase 1**: Generate quickstart.md with implementation steps
4. **Phase 2**: Generate tasks.md for implementation
