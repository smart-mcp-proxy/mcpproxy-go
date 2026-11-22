# Tasks: Skills review navigation

**Input**: [spec.md](spec.md), [plan.md](plan.md), [quickstart.md](quickstart.md)
**Tests**: Focused regressions cover review routing and the responsive Skills panel.

## Phase 1: Review queue entry (User Story 1)

**Independent test**: Both skills actions open the filtered tab; mixed-row tool Review still opens tool review.

- [x] T001 [US1] Add link and filter tests in `frontend/tests/unit/review-queue-skills.spec.ts`, `frontend/tests/unit/server-detail-skills-tab.spec.ts`, and `frontend/tests/unit/skills-panel.spec.ts` for skill-only and mixed queue rows, direct Skills entry, invalid query, actionable membership, count, and empty state.
- [x] T002 [US1] Add `state=needs-review` to skill-review destinations in `frontend/src/utils/reviewQueue.ts` and keep `frontend/src/components/ReviewQueueList.vue` tool Review behavior.
- [x] T003 [US1] Read the Skills filter query and scope it to the Skills tab in `frontend/src/views/ServerDetail.vue`. Provide Needs review and All skills filtering in `frontend/src/components/skills/SkillsPanel.vue`.

## Phase 2: Immediate detail (User Story 2)

**Independent test**: A 25-item list opens detail in view on desktop and compact screens, with keyboard focus round trip and unchanged stale guard.

- [x] T004 [US2] Add component and browser tests in `frontend/tests/unit/skills-panel.spec.ts` and `e2e/web-ui-sweep/skills-tab.spec.ts` for desktop and 390 px list/detail layout, keyboard focus, refresh, filtered-out selection, and removed selection.
- [x] T005 [US2] Render desktop list/detail columns and compact detail replacement with Back to skills in `frontend/src/components/skills/SkillsPanel.vue`.
- [x] T006 [US2] Preserve the review snapshot and focus target across selection, actions, and refresh in `frontend/src/components/skills/SkillsPanel.vue`.

## Phase 3: Documentation and validation

- [x] T007 [P] Update `docs/features/skills.md`, `docs/web-ui/dashboard.md`, and `docs/web-ui/server-detail.md` with entry paths, filter criteria, and responsive detail interaction.
- [x] T008 Run Vitest and the Skills tab Playwright sweep, and verify both widths against live Hugging Face data. Record the results in [verification/report.html](verification/report.html).
- [x] T009 Run Spec Kit consistency review against Spec 125 and confirm no API, trust/write, or authorization changes entered the spec/docs patch.

## Dependencies

T001 → T002 → T003. T004 → T005 → T006. T007 can proceed in parallel with implementation after the interaction is fixed. T008 and T009 follow both stories.
