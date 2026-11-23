# Implementation Plan: Skills review navigation

**Branch**: `codex/skills-review-ux` | **Date**: 2026-11-22 | **Spec**: [spec.md](spec.md)
**Input**: Follow-up to Spec 125 Web UI delivery.

## Summary

Link the global Review queue to a filtered Skills tab, and make skill selection immediately reveal its card in a responsive list/detail layout. Reuse Spec 125's list response, review token, hash/incarnation stale guard, and existing approval handlers.

## Technical Context

**Language/Version**: TypeScript, Vue 3
**Primary Dependencies**: Vue Router, existing UI components and API client
**Storage**: None added; filter and selection are view state
**Testing**: Vitest unit/component tests and Playwright desktop/mobile interaction checks
**Target Platform**: Web UI, desktop and compact browser widths
**Project Type**: Existing web frontend
**Performance Goals**: Detail heading visible immediately after selection; filtering a typical 25-skill listing is synchronous
**Constraints**: Preserve server-side authorization, approval hash/incarnation binding, and all direct links
**Scale/Scope**: One review-queue link helper and one Skills tab/list/detail flow; no API or data-model change

## Constitution Check

| Principle | Assessment |
| --- | --- |
| Performance at Scale | Pass: filter existing in-memory listing; avoid new network calls on selection. |
| Actor-Based Concurrency | Pass: no Go concurrency change; preserve the existing request sequencing. |
| Configuration-Driven Architecture | Pass: no configuration change. |
| Security by Default | Pass: use the existing admin-only tab and approvals; no new content exposure. |
| Test-Driven Development | Pass: use focused regression tests for link, filter, focus, and stale review behavior. |
| Documentation Hygiene | Pass: update the README, feature guide, and Web UI pages with the new behavior. |

## Design

1. Change the review-queue Skills link helper so skill-only Review and mixed-row Skills add `state=needs-review`. Keep mixed-row tool Review routing intact.
2. Read the filter from the route. Only `needs-review` activates filtering; an absent or unknown state shows All skills. Define the predicate from existing record fields: `pending` or `changed`, currently listed, unblocked, and eligible. The count comes from the filtered array.
3. Keep selection independent of the filtered array. Use a two-column layout at desktop width. On compact width show either list or detail, with Back to skills in detail. Move focus to the detail on open and back to the saved row on return.
4. Preserve the current `Review` snapshot and reload reconciliation. If an action removes the selected item from Needs review, keep its detail open. If it disappears entirely from the refreshed catalog, return to the list with a clear state.
5. Validate at desktop and 390 px width, including keyboard navigation, 25-skill data, hash changes during review, empty filtered state, and direct/deep links.

## Project Structure

### Documentation

```text
specs/130-skills-review-ux/{spec,plan,tasks,quickstart}.md
specs/130-skills-review-ux/checklists/requirements.md
docs/features/skills.md
docs/web-ui/{dashboard,server-detail}.md
```

### Source Code

```text
frontend/src/utils/reviewQueue.ts
frontend/src/components/ReviewQueueList.vue
frontend/src/views/ServerDetail.vue
frontend/src/components/skills/SkillsPanel.vue
frontend/src/components/skills/SkillDetail.vue
frontend/tests/unit/{review-queue-skills,server-detail-skills-tab,skills-panel}.spec.ts
e2e/web-ui-sweep/skills-tab.spec.ts
scripts/run-skills-tab-sweep.sh
```

**Structure Decision**: Frontend-only presentation changes within existing components. No new API contract or data model is needed.

## Complexity Tracking

No constitution violations or new infrastructure.
