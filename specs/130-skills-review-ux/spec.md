# Feature Specification: Skills review navigation

**Feature Branch**: `codex/skills-review-ux`
**Created**: 2026-11-22
**Status**: Implemented and verified locally
**Input**: Make the Skills review queue open only actionable held skills and make a selected skill's details immediately visible on desktop and compact screens.

**Related**: [Spec 125](../125-skills-over-mcp/spec.md), especially User Stories 1–2, FR-017, and the Web UI design in plan D13. This is a follow-up to its completed Web UI work. The skill trust model, approval binding, write endpoints, agent visibility, and native MCP surface remain governed by Spec 125.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Review the held skills named by the queue (Priority: P1)

An administrator follows a skills action from the global Review queue and lands on the server's Skills tab with **Needs review** selected. The list contains only skills they can act on: pending or changed, currently listed, and unblocked. They can switch to **All skills** to inspect the whole catalog. Opening the Skills tab directly starts with All skills.

**Independent Test**: With a server containing pending, changed, approved, blocked, unusable, and no-longer-listed skills, follow both a skill-only queue row's Review link and a mixed row's Skills link. Both show the same actionable held set; opening the tab directly shows all records.

**Acceptance Scenarios**:

1. **Given** a skill-only Review queue row, **When** its Review action is opened, **Then** the URL includes `?tab=skills&state=needs-review` and the Needs review filter is active.
2. **Given** a mixed tool-and-skill Review queue row, **When** its Skills action is opened, **Then** the same filtered Skills tab opens; its tool Review action still opens the tool review.
3. **Given** a direct `/ui/servers/<name>?tab=skills` visit or a click on the server's Skills tab, **When** the list loads, **Then** All skills is active.
4. **Given** pending or changed skills, **When** Needs review is active, **Then** only skills that are currently listed and unblocked appear; approved, unusable, blocked and no-longer-listed records do not appear, and the filter count equals the number shown.
5. **Given** no actionable held skills, **When** Needs review is active, **Then** the tab explains that nothing needs review and still offers All skills.
6. **Given** an existing Skills tab link without `state`, or another server tab link, **When** it opens, **Then** its established destination and behavior are preserved.

### User Story 2 - See the skill I selected immediately (Priority: P1)

An administrator selects a skill and sees its card without scrolling past the entire list. On a wide screen the list and details appear side by side. On a compact screen the detail takes the list's place and offers **Back to skills**. Keyboard focus moves to the detail when it opens and returns to the selected row on Back.

**Independent Test**: With at least 25 skills, select an item near the top on desktop and at a compact viewport. Confirm the detail is visible immediately, and complete a keyboard-only open/back cycle. Then refresh the list while the detail is open.

**Acceptance Scenarios**:

1. **Given** a wide screen and a long list, **When** a skill is selected, **Then** its detail appears beside the list within the current viewport.
2. **Given** a compact screen and a long list, **When** a skill is selected, **Then** the detail replaces the list and a Back to skills control is available without scrolling.
3. **Given** a keyboard selection, **When** the detail opens, **Then** focus moves to its heading or first meaningful control; **When** Back to skills is used, focus returns to the previously selected row.
4. **Given** a list refresh while a skill is open, **When** its reviewed content hash and server incarnation remain the same, **Then** the detail remains open with the updated record; **When** either changes, **Then** the existing stale-review warning and disabled approval remain until the person opens the new version.
5. **Given** a selected skill that no longer qualifies for Needs review after an action, **When** the refreshed filtered list omits it, **Then** its open detail remains visible until the person goes Back or chooses another skill.
6. **Given** the selected skill is removed entirely from the server listing, **When** a refresh completes, **Then** the detail closes. Focus returns to a list control if it was inside the detail and stays where it was otherwise.

### Edge Cases

- A `state` query value other than `needs-review` is treated as All skills; it does not hide records accidentally.
- Switching filters while a detail is open must not swap its content for a different skill. If the selected skill is absent from the newly filtered list, the card remains until Back or another explicit selection.
- Pending network requests, failed refreshes, and `skills.changed` events preserve the existing version and stale-review protections from Spec 125.
- The Skills tab remains administrator-only. Scoped callers and tenants cannot learn held-skill details through a new link, count, filter, or view.
- At narrow widths, the detail has no horizontal overflow and its Back control remains discoverable.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Global Review queue skill-only Review links and mixed-row Skills links MUST target the server Skills tab with `state=needs-review`.
- **FR-002**: The Skills tab MUST offer Needs review and All skills filters. Direct tab entry without `state=needs-review` MUST default to All skills.
- **FR-003**: Needs review MUST include only records with state `pending` or `changed` that are currently listed, unblocked, and eligible for review. Its displayed count MUST equal the visible set.
- **FR-004**: Filtering MUST be presentation-only; no skill approval, blocking, scan, refresh, or agent-availability rule may change.
- **FR-005**: Selecting a skill MUST reveal its existing detail card beside the list at desktop width, or in place of the list at compact width, without requiring a long scroll.
- **FR-006**: A compact detail view MUST offer Back to skills; opening and returning MUST place keyboard focus in the detail and on the previously selected row respectively.
- **FR-007**: A refresh MUST preserve the selected skill and Spec 125's content-hash/incarnation stale guard. A selected record that leaves the current filter after an action MUST remain visible in the detail until Back or another explicit selection.
- **FR-008**: Existing Skills tab deep links and tool-review links MUST continue to work. New navigation and filtering MUST retain existing administrator access boundaries.
- **FR-009**: The Web UI and feature documentation MUST explain both entry paths, the filter definition, and the responsive detail navigation.

### Key Entities

- **Skill record**: The existing Spec 125 record, including state, listed status, blocked mark, current hash, and server incarnation.
- **Skills view state**: The chosen list filter and selected skill. This changes presentation only; it is not an approval state.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In a 25-skill fixture, every queue skills action opens a list containing exactly the actionable held records, with a matching count.
- **SC-002**: A reviewer who selects the first skill can see its detail heading without manual scrolling at desktop and 390 px wide viewport sizes.
- **SC-003**: A keyboard-only reviewer can open a skill and return to the same row with focus restored on compact layout.
- **SC-004**: Refresh and changed-content tests show zero approvals of content other than the version the reviewer opened.
- **SC-005**: Existing direct Skills links, tool Review links, and scoped-access tests continue to pass.

## Commit Message Conventions *(mandatory)*

Use a concise imperative subject. Reference a related issue with `Related #...` when applicable; do not use an auto-closing keyword or AI co-author tag. Include verification in the commit or PR description.
