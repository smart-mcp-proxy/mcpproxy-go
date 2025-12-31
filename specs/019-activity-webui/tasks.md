# Tasks: Activity Log Web UI

**Input**: Design documents from `/specs/019-activity-webui/`
**Prerequisites**: plan.md (required), spec.md (required), research.md, data-model.md, contracts/

**Tests**: Unit tests with Vitest and E2E verification with Playwriter as specified in the feature requirements.

**Organization**: Tasks are grouped by user story to enable independent implementation and testing of each story.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (e.g., US1, US2, US3)
- Include exact file paths in descriptions

## Path Conventions

- **Frontend**: `frontend/src/` for Vue 3 components and services
- **Tests**: `frontend/tests/unit/` for Vitest tests
- **Docs**: `docs/docs/` for Docusaurus documentation

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Add foundational types and API methods needed by all user stories

- [x] T001 Add Activity TypeScript types to `frontend/src/types/api.ts`
- [x] T002 [P] Add `getActivities()` API method to `frontend/src/services/api.ts`
- [x] T003 [P] Add `getActivityDetail()` API method to `frontend/src/services/api.ts`
- [x] T004 [P] Add `getActivitySummary()` API method to `frontend/src/services/api.ts`
- [x] T005 [P] Add `getActivityExportUrl()` API method to `frontend/src/services/api.ts`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core infrastructure that MUST be complete before ANY user story can be implemented

**CRITICAL**: No user story work can begin until this phase is complete

- [x] T006 Add activity route to `frontend/src/router/index.ts`
- [x] T007 [P] Add "Activity Log" navigation link to `frontend/src/components/SidebarNav.vue`
- [x] T008 [P] Create empty Activity.vue skeleton in `frontend/src/views/Activity.vue`

**Checkpoint**: Foundation ready - user story implementation can now begin

---

## Phase 3: User Story 1 - View Activity Log Page (Priority: P1)

**Goal**: Display a dedicated Activity Log page with a table showing activity records with Time, Type, Server, Details, Status, Duration columns

**Independent Test**: Navigate to `/ui/activity` and verify table displays activity records with all columns, chronologically ordered (most recent first)

### Implementation for User Story 1

- [x] T009 [US1] Implement page header with title and summary section in `frontend/src/views/Activity.vue`
- [x] T010 [US1] Implement data fetching with loading state in `frontend/src/views/Activity.vue`
- [x] T011 [US1] Implement activity table with columns (Time, Type, Server, Details, Status, Duration) in `frontend/src/views/Activity.vue`
- [x] T012 [US1] Add status badge component with colors (success=green, error=red, blocked=orange) in `frontend/src/views/Activity.vue`
- [x] T013 [US1] Add empty state message when no activities exist in `frontend/src/views/Activity.vue`
- [x] T014 [US1] Add error state handling with retry button in `frontend/src/views/Activity.vue`
- [x] T015 [US1] Verify with Playwriter that Activity Log page loads with table displaying columns

**Checkpoint**: User Story 1 complete - Activity Log page displays records in table format

---

## Phase 4: User Story 2 - Real-time Activity Updates (Priority: P1)

**Goal**: Activity updates appear in real-time via SSE without manual refresh

**Independent Test**: Trigger a tool call and observe the activity appear in the table automatically; verify status updates when tool completes

### Implementation for User Story 2

- [x] T016 [US2] Add SSE event listener for `activity.tool_call.started` in `frontend/src/stores/system.ts`
- [x] T017 [P] [US2] Add SSE event listener for `activity.tool_call.completed` in `frontend/src/stores/system.ts`
- [x] T018 [P] [US2] Add SSE event listener for `activity.policy_decision` in `frontend/src/stores/system.ts`
- [x] T019 [US2] Implement window event listeners in Activity.vue to handle SSE events and update table
- [x] T020 [US2] Implement optimistic row updates for started/completed state transitions in `frontend/src/views/Activity.vue`
- [x] T021 [US2] Add connection status indicator showing SSE health in `frontend/src/views/Activity.vue`
- [x] T022 [US2] Implement automatic reconnection handling with visual feedback in `frontend/src/views/Activity.vue`
- [x] T023 [US2] Verify with Playwriter that real-time updates appear when tool calls occur

**Checkpoint**: User Story 2 complete - Real-time updates working via SSE

---

## Phase 5: User Story 3 - Filter Activity Records (Priority: P2)

**Goal**: Users can filter activities by type, server, status, and time range

**Independent Test**: Select filter values and verify only matching records are displayed; multiple filters combine with AND logic

### Implementation for User Story 3

- [x] T024 [US3] Add filter state (type, server, status, start_time, end_time) to Activity.vue
- [x] T025 [US3] Implement type filter dropdown (tool_call, policy_decision, quarantine_change, server_change) in `frontend/src/views/Activity.vue`
- [x] T026 [P] [US3] Implement server filter dropdown (populated from fetched data) in `frontend/src/views/Activity.vue`
- [x] T027 [P] [US3] Implement status filter dropdown (success, error, blocked) in `frontend/src/views/Activity.vue`
- [x] T028 [US3] Implement date range picker with start/end datetime inputs in `frontend/src/views/Activity.vue`
- [x] T029 [US3] Connect filters to API request params and trigger refetch on change
- [x] T030 [US3] Add filter summary display showing active filters in `frontend/src/views/Activity.vue`
- [x] T031 [US3] Add "Clear Filters" button in `frontend/src/views/Activity.vue`
- [x] T032 [US3] Verify with Playwriter that filters work correctly and combine with AND logic

**Checkpoint**: User Story 3 complete - Filtering works for all filter types

---

## Phase 6: User Story 4 - View Activity Details (Priority: P2)

**Goal**: Click on activity row to see full details including request arguments and response data

**Independent Test**: Click a row and verify detail panel shows complete information including ID, type, timestamp, server, tool, status, duration, arguments, response, and error message

### Implementation for User Story 4

- [x] T033 [US4] Add selectedActivity state and click handler to table rows in `frontend/src/views/Activity.vue`
- [x] T034 [US4] Implement collapsible detail panel/drawer showing full activity in `frontend/src/views/Activity.vue`
- [x] T035 [US4] Display activity metadata (ID, type, timestamp, server, tool, status, duration) in detail panel
- [x] T036 [US4] Implement JSON viewer for request arguments in detail panel
- [x] T037 [P] [US4] Implement response viewer with truncation indicator in detail panel
- [x] T038 [P] [US4] Display error message when status is error in detail panel
- [x] T039 [US4] Add close button and keyboard escape handler for detail panel
- [x] T040 [US4] Verify with Playwriter that clicking row opens detail panel with full information

**Checkpoint**: User Story 4 complete - Detail view shows full activity information

---

## Phase 7: User Story 5 - Dashboard Activity Widget (Priority: P2)

**Goal**: Dashboard shows activity summary widget with today's counts and recent activities

**Independent Test**: View dashboard and verify widget shows total count, success count, error count, and 3 most recent activities with "View All" link

### Implementation for User Story 5

- [x] T041 [US5] Create ActivityWidget.vue component in `frontend/src/components/ActivityWidget.vue`
- [x] T042 [US5] Implement summary stats row (total today, success, errors) using getActivitySummary API
- [x] T043 [US5] Implement recent activities list (3 items) with server, tool, time, status
- [x] T044 [US5] Add "View All" link navigating to /activity page
- [x] T045 [US5] Add loading and error states to widget
- [x] T046 [US5] Import and add ActivityWidget to Dashboard.vue in `frontend/src/views/Dashboard.vue`
- [x] T047 [US5] Verify with Playwriter that dashboard widget displays correct counts and recent activities

**Checkpoint**: User Story 5 complete - Dashboard widget showing activity summary

---

## Phase 8: User Story 6 - Export Activity Records (Priority: P3)

**Goal**: Users can export activity records to JSON or CSV format

**Independent Test**: Click export, select format, and verify file downloads with correct content; filtered records export only

### Implementation for User Story 6

- [x] T048 [US6] Add export button with dropdown menu (JSON/CSV options) in `frontend/src/views/Activity.vue`
- [x] T049 [US6] Implement export click handler using getActivityExportUrl with current filters
- [x] T050 [US6] Open export URL in new window/tab for browser download handling
- [x] T051 [US6] Add loading indicator during export initiation
- [x] T052 [US6] Verify with Playwriter that export downloads file in correct format

**Checkpoint**: User Story 6 complete - Export functionality working for both formats

---

## Phase 9: User Story 7 - Paginate Activity Records (Priority: P3)

**Goal**: Navigate through pages of activity records with pagination controls

**Independent Test**: View page with 100+ records, verify pagination controls appear, navigate between pages

### Implementation for User Story 7

- [x] T053 [US7] Add pagination state (currentPage, pageSize, total) to Activity.vue
- [x] T054 [US7] Implement pagination controls (Previous, Next, page info) in `frontend/src/views/Activity.vue`
- [x] T055 [US7] Add page size selector (10, 25, 50, 100) in `frontend/src/views/Activity.vue`
- [x] T056 [US7] Connect pagination to API offset/limit params
- [x] T057 [US7] Reset to page 1 when filters change
- [x] T058 [US7] Verify with Playwriter that pagination navigates correctly

**Checkpoint**: User Story 7 complete - Pagination working for large datasets

---

## Phase 10: User Story 8 - Toggle Auto-refresh (Priority: P3)

**Goal**: Users can toggle auto-refresh to control SSE subscription

**Independent Test**: Toggle auto-refresh off, trigger activity, verify it doesn't appear until toggle on or manual refresh

### Implementation for User Story 8

- [x] T059 [US8] Add autoRefresh state (default: true) to Activity.vue
- [x] T060 [US8] Implement auto-refresh toggle switch in page header in `frontend/src/views/Activity.vue`
- [x] T061 [US8] Connect toggle to SSE event listener subscription/unsubscription
- [x] T062 [US8] Add manual refresh button (visible when auto-refresh disabled)
- [x] T063 [US8] Verify with Playwriter that toggle controls real-time updates

**Checkpoint**: User Story 8 complete - Auto-refresh toggle working

---

## Phase 11: Polish & Cross-Cutting Concerns

**Purpose**: Tests, documentation, and improvements that affect multiple user stories

### Unit Tests

- [x] T064 [P] Create activity type rendering tests in `frontend/tests/unit/activity.spec.ts`
- [x] T065 [P] Create status badge color tests in `frontend/tests/unit/activity.spec.ts`
- [x] T066 [P] Create filter logic tests in `frontend/tests/unit/activity.spec.ts`
- [x] T067 [P] Create pagination logic tests in `frontend/tests/unit/activity.spec.ts`
- [x] T068 [P] Create export URL generation tests in `frontend/tests/unit/activity.spec.ts`

### Documentation

- [x] T069 [P] Create Activity Log documentation in `docs/web-ui/activity-log.md`
- [x] T070 Update CLAUDE.md if needed with new frontend patterns (not needed - existing patterns sufficient)

### Final Verification

- [x] T071 Run full Playwriter E2E verification of all user stories
- [x] T072 Run `npm run test` to verify all unit tests pass
- [x] T073 Run `npm run build` to verify production build succeeds

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup completion - BLOCKS all user stories
- **User Stories (Phase 3-10)**: All depend on Foundational phase completion
  - US1 (View Page) and US2 (Real-time) can proceed in parallel
  - US3 (Filters), US4 (Details), US5 (Widget) can start after US1 completes
  - US6 (Export), US7 (Pagination), US8 (Toggle) can start after US1 completes
- **Polish (Phase 11)**: Depends on all user stories being complete

### User Story Dependencies

| Story | Priority | Dependencies | Can Start After |
|-------|----------|--------------|-----------------|
| US1 - View Activity Log Page | P1 | Phase 2 | Foundational complete |
| US2 - Real-time Updates | P1 | Phase 2 | Foundational complete |
| US3 - Filter Activity | P2 | US1 | US1 complete |
| US4 - View Details | P2 | US1 | US1 complete |
| US5 - Dashboard Widget | P2 | Phase 2 | Foundational complete |
| US6 - Export Records | P3 | US1, US3 | US3 complete |
| US7 - Pagination | P3 | US1 | US1 complete |
| US8 - Auto-refresh Toggle | P3 | US2 | US2 complete |

### Within Each User Story

- Core implementation before integration
- UI components before event handlers
- State management before API connections
- Story complete before Playwriter verification

### Parallel Opportunities

- All Setup tasks T002-T005 can run in parallel
- Foundational tasks T007-T008 can run in parallel
- US1 and US2 can proceed in parallel after Foundational
- US5 (Dashboard Widget) is independent and can run in parallel with US1-US4
- All Polish phase tests (T064-T068) can run in parallel

---

## Parallel Example: Phase 1 Setup

```bash
# Launch all API method additions together:
Task: "Add `getActivities()` API method to frontend/src/services/api.ts"
Task: "Add `getActivityDetail()` API method to frontend/src/services/api.ts"
Task: "Add `getActivitySummary()` API method to frontend/src/services/api.ts"
Task: "Add `getActivityExportUrl()` API method to frontend/src/services/api.ts"
```

## Parallel Example: User Story 3 Filters

```bash
# Launch parallel filter dropdowns together:
Task: "Implement server filter dropdown in frontend/src/views/Activity.vue"
Task: "Implement status filter dropdown in frontend/src/views/Activity.vue"
```

---

## Implementation Strategy

### MVP First (User Stories 1-2 Only)

1. Complete Phase 1: Setup (types and API methods)
2. Complete Phase 2: Foundational (route, nav, skeleton)
3. Complete Phase 3: User Story 1 (basic table view)
4. Complete Phase 4: User Story 2 (real-time updates)
5. **STOP and VALIDATE**: Test US1 and US2 with Playwriter
6. Deploy/demo if ready - users can view activity log with live updates

### Incremental Delivery

1. Complete Setup + Foundational → Foundation ready
2. Add US1 (View Page) → Test → Deploy (basic viewing)
3. Add US2 (Real-time) → Test → Deploy (live updates)
4. Add US3 (Filters) + US4 (Details) → Test → Deploy (full browsing)
5. Add US5 (Widget) → Test → Deploy (dashboard integration)
6. Add US6-US8 (Export, Pagination, Toggle) → Test → Deploy (complete feature)
7. Complete Polish → Final release

### Suggested MVP Scope

**MVP = User Story 1 + User Story 2**

This delivers:
- Activity Log page accessible from navigation
- Table with all columns (Time, Type, Server, Details, Status, Duration)
- Real-time updates via SSE
- Basic empty/error states

Users can immediately start monitoring AI agent activity in real-time.

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story for traceability
- Each user story should be independently completable and testable
- Use Playwriter (`mcp__playwriter__execute`) for E2E verification after each story
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
- Follow existing Vue 3 + DaisyUI patterns from ToolCalls.vue and Dashboard.vue
