# Feature Specification: Tray Glance v2 — Grouped Calls, Intent Reasons, Failure-Only Marks, Idle Clients

**Feature Branch**: `090-tray-glance-v2`
**Created**: 2026-07-31
**Status**: Draft
**Input**: User description: "Tray glance v2 — grouped calls, intent reasons, failure-only marks, idle clients. Iterate on the merged tray glance section (PR #930) in the native macOS tray, driven by a real 6-week activity export from a work laptop (3,582 events)."

## Context & Evidence

The tray glance section (shipped in PR #930) shows the five most recent tool calls, connected clients, and a 24h histogram. Field feedback from daily use on a work laptop, backed by a 6-week activity export (3,582 events), identified five gaps:

1. **Repetition floods the rows.** 1,479 tool calls in the export collapse into 809 consecutive same-tool runs; individual runs reach 19× (`jira_get_issue`) and 100× (`retrieve_tools`). Today each call takes one of the five rows, so a burst of one tool hides everything else.
2. **No context.** Callers attach an intent reason to 98.7% of tool calls (median 51 characters, e.g. "Verify the failed transition did not change the ticket"), but the menu never shows it.
3. **Success marks are noise.** 1,480 of 1,564 outcome-bearing events succeeded. A green checkmark on nearly every row carries no information; the 32 errors and 52 policy blocks are what deserve attention — and policy blocks are currently invisible to the glance entirely.
4. **The 24h histogram is buried** below the Recent and Clients lists, though it is the best single answer to "what's been happening?"
5. **Clients is almost always empty.** Only sessions in "active" status are shown, but client connections are stateless HTTP: sessions close after 30 minutes of inactivity and are only persisted once real work happens. An empty section reads as "nothing is connected", which is wrong and erodes trust in the menu. (Connection-level tracking cannot fix this: the transport is stateless by design, so "recently seen / idle" is the honest presentation.)

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Repeated calls collapse into one row (Priority: P1)

An agent hammers one tool (e.g. 19 consecutive `jira_get_issue` calls) while working. The user opens the tray menu and still sees a useful cross-section of recent activity: the burst occupies a single row labeled with a repeat count (e.g. "jira-gcore:jira_get_issue ×19"), and the remaining rows show the other tools that ran before it.

**Why this priority**: This is the headline complaint. Without grouping, the five rows routinely show one tool five times, and every other change in this feature is invisible behind the flood.

**Independent Test**: Feed the glance a recorded sequence containing consecutive same-tool runs; verify the rendered rows collapse each run to one row with the correct count, ordering, and age, while non-consecutive repeats stay separate rows.

**Acceptance Scenarios**:

1. **Given** the activity feed holds 19 consecutive `jira_get_issue` calls followed by older calls to other tools, **When** the user opens the menu, **Then** the first row reads as one entry with a ×19 count and the remaining rows show the older distinct tools.
2. **Given** a run of identical calls, **When** the row renders, **Then** its age is the age of the newest call in the run.
3. **Given** calls to tool A, then tool B, then tool A again, **When** rows render, **Then** the two A episodes remain separate rows (grouping is consecutive-only, preserving the timeline).
4. **Given** a run of 12 calls where one failed, **When** the row renders, **Then** the row is marked as failed (failure dominates the group) and its failure detail is available on the row.
5. **Given** a single (non-repeated) call, **When** the row renders, **Then** no count suffix is shown.

---

### User Story 2 - The reason a call happened is visible (Priority: P2)

The user glances at the menu and understands not just *which* tools ran but *why*: each row shows the caller-declared intent reason (e.g. "Handoff: move ticket to review per user request") as a secondary, visually subdued line under the tool name.

**Why this priority**: The reason field is the difference between an audit trail and a story. It exists on almost every call already; showing it turns the glance into an explanation of agent behavior.

**Independent Test**: Render rows from records with and without intent reasons; verify the reason renders as a subdued second line, truncated to fit, with the full text available on hover and to assistive technology.

**Acceptance Scenarios**:

1. **Given** a call with intent reason "Verify the failed transition did not change the ticket", **When** the row renders, **Then** the reason appears as a second, visually subdued line of the same menu row.
2. **Given** a reason longer than the row budget, **When** the row renders, **Then** the reason is truncated with an ellipsis and the full reason is available in the row's tooltip.
3. **Given** a call without a reason (e.g. an internal discovery call), **When** the row renders, **Then** the row renders as a single line with no empty second line.
4. **Given** a grouped row (×N), **When** the row renders, **Then** the reason shown is the newest call's reason.
5. **Given** a row with a reason, **When** read by assistive technology, **Then** the spoken label includes the reason.

---

### User Story 3 - Only failures are marked, and blocked calls appear (Priority: P2)

Successful calls render without any status icon — quiet by default. A failed call carries a red failure mark plus its first error clause; a policy-blocked call carries a distinct warning mark plus the block reason. Policy blocks — invisible today — appear as rows.

**Why this priority**: Failure visibility is the core safety value of the glance, and it is currently diluted by 95% green noise. Blocked calls are the proxy *doing its job* and must be seen.

**Independent Test**: Render a feed containing successes, errors, and policy blocks; verify successes have no status icon, errors and blocks have distinct marks, and blocked records qualify as rows with their block reason.

**Acceptance Scenarios**:

1. **Given** a successful call, **When** the row renders, **Then** it carries no status icon and assistive technology still announces it as succeeded.
2. **Given** a failed call, **When** the row renders, **Then** it carries a red failure mark and shows the first clause of the error message.
3. **Given** a policy-blocked call (e.g. "Intent rejected: tool variant conflicts with server annotations"), **When** the feed refreshes, **Then** the block appears as a row with a warning mark distinct from the failure mark, and the block reason is shown where a reason line would be.
4. **Given** a mix of successes and one failure in the feed, **When** the menu opens, **Then** the failure is visually the only marked row among them.

---

### User Story 4 - Clients show presence honestly instead of vanishing (Priority: P2)

The user opens the menu after lunch. Instead of "No connected clients", the Clients section lists the AI clients that recently used the proxy, each with a presence state: **active** (used it in the last few minutes), **idle** (quiet but recent), or **seen** (older), with the time since last activity.

**Why this priority**: An empty Clients section is actively misleading — the user reported it as the most confusing part of the current menu. Presence states match the stateless reality of the transport.

**Independent Test**: Feed the section session records with various last-activity ages and statuses; verify classification into active/idle/seen, deduplication per client, and that the section is non-empty whenever any client was seen recently.

**Acceptance Scenarios**:

1. **Given** a client whose last activity was 2 minutes ago, **When** the section renders, **Then** the client shows as active (filled indicator).
2. **Given** a client whose last activity was 20 minutes ago (session may already be closed), **When** the section renders, **Then** the client shows as idle with its age (e.g. "idle · 20m").
3. **Given** a client last seen 3 hours ago within the lookback window, **When** the section renders, **Then** the client shows as seen with its age, visually quieter than idle.
4. **Given** one client with several sessions (e.g. claude-code reconnecting), **When** the section renders, **Then** the client appears once, classified by its most recent session.
5. **Given** no client activity within the lookback window, **When** the section renders, **Then** the section states no recent clients (and only then).
6. **Given** both active and idle clients, **When** the summary line renders, **Then** it reads counts by state (e.g. "2 active · 1 idle") instead of a single client count.

---

### User Story 5 - The 24h picture is the first thing under the summary (Priority: P3)

The Activity (24h) histogram entry sits directly under the summary line, above the Recent rows, so the day-level picture comes before the call-level detail.

**Why this priority**: Pure reordering — valuable but trivially small, and independent of everything else.

**Independent Test**: Open the menu; verify block order is summary → Activity (24h) → Recent rows → Clients.

**Acceptance Scenarios**:

1. **Given** the glance is visible, **When** the menu opens, **Then** the Activity (24h) entry appears directly below the summary line and above the "Recent" header.

---

### Edge Cases

- A run larger than the fetched page (e.g. 100+ identical calls): the count shows what is known (e.g. ×100) without claiming precision beyond the fetched window.
- All five rows would be one group: grouping frees rows, so older distinct tools fill the remaining rows.
- A live-streamed (not yet persisted) record extends a group that started from polled records — the group must not split or double-count when the reconciling poll replaces provisional records.
- A grouped row's count changes while the menu is open: the row updates in place (count/age text), but the menu's structure (row count) must not change under the pointer; structural changes wait for menu close.
- A record with no reason and no error renders as a single-line row; mixed feeds render mixed one- and two-line rows without misalignment.
- A blocked record has no paired tool-call record (the call never dispatched) — it must still render standalone.
- A client session with no client name renders as "Unknown client", still classified by age.
- Clock skew or a stale timestamp yielding a negative age renders as "0s", never a negative.
- When the core is stopped or unreachable, the glance block hides entirely (existing behavior, unchanged).

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST collapse consecutive activity rows that share the same server and tool into a single row bearing the run's size as a "×N" suffix when N > 1.
- **FR-002**: A grouped row MUST derive its age from the newest record of the run, its reason from the newest record that has one, and its status from the worst outcome in the run (any error or block outranks success).
- **FR-003**: Grouping MUST be consecutive-only: records of the same tool separated by a different tool's record form separate rows.
- **FR-004**: Each row MUST display the caller-declared intent reason, when present, as a visually subdued second line, truncated to the row budget; the full reason MUST be available via tooltip and assistive-technology label.
- **FR-005**: Rows for records without a reason MUST render as a single line.
- **FR-006**: Live-streamed events MUST carry their intent reason into the row without waiting for the reconciling poll.
- **FR-007**: Successful rows MUST render without a status icon; assistive technology MUST still announce the outcome.
- **FR-008**: Failed rows MUST carry a red failure mark and the first clause of the error message; blocked rows MUST carry a visually distinct warning mark and the block reason.
- **FR-009**: Policy-blocked calls MUST qualify as glance rows, including when no corresponding tool call record exists.
- **FR-010**: The Activity (24h) entry MUST appear directly below the summary line and above the Recent rows.
- **FR-011**: The Clients section MUST list clients seen within a 24-hour lookback window regardless of session status, deduplicated per client (name + version), each classified by time since last activity: active (< 5 minutes), idle (5–30 minutes), seen (> 30 minutes).
- **FR-012**: Each client row MUST show a presence indicator whose shape or fill differs per state (not color alone) and, for idle/seen, the time since last activity.
- **FR-013**: The summary line MUST report client counts by state — "N active · M idle" — omitting empty states; clients in the "seen" state are not counted in the summary.
- **FR-014**: The "no clients" placeholder MUST appear only when no client was seen within the lookback window.
- **FR-015**: Opening the menu MUST NOT trigger any network request (existing invariant, preserved).
- **FR-016**: While the menu is open, row content MAY update in place, but the number and identity of menu items MUST NOT change; structural changes are deferred to menu close (existing invariant, preserved).
- **FR-017**: All rows MUST remain reachable by keyboard navigation and meaningfully announced by VoiceOver (existing invariant, preserved).

### Key Entities

- **Activity record**: One proxied event — a tool call, internal tool call, or policy decision — with server, tool, outcome, timestamp, optional intent reason, optional error message, and a request identity used for deduplication.
- **Glance run (grouped row)**: A maximal sequence of consecutive activity records sharing server + tool, presented as one row with count, newest age, newest reason, and worst outcome.
- **Client presence**: A deduplicated view of one client application derived from its recent sessions: display name, version, last-activity time, and derived state (active / idle / seen).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Replaying the reference 6-week export through the row pipeline, no two adjacent rendered rows ever show the same server + tool, and a burst of ≥ 19 identical calls occupies exactly one of the five rows.
- **SC-002**: For call records carrying a reason (98.7% in the reference export), the reason (possibly truncated) is visible directly in the open menu without any further interaction.
- **SC-003**: In a feed where under 6% of events are failures (the reference ratio), failed or blocked rows are the only rows carrying status marks.
- **SC-004**: Every policy block in the reference export produces a visible row; today that number is zero.
- **SC-005**: After 20 minutes of client inactivity, the Clients section still names the client (as idle) rather than showing an empty state; the empty state appears only after 24 hours without any client.
- **SC-006**: Opening the menu issues zero network requests, verified by the existing counting test seam.
- **SC-007**: All existing glance unit-test suites still pass, extended to cover grouping, reason rendering, failure-only marks, blocked rows, and presence classification.

## Assumptions

- Presence thresholds (5 min active, 30 min idle, 24 h lookback) are fixed presentation policy, not user-configurable, matching the session inactivity timeout (30 min) on the idle boundary.
- The dedupe key for clients is client name + version; two versions of the same client are two rows.
- The row budget for the reason line equals the existing label budget; median reasons (~51 chars) fit untruncated.
- Blocked rows draw their reason from the policy decision's own reason field; call rows draw theirs from the caller-declared intent.
- The grouped count reflects the fetched window (up to 100 records per poll); runs longer than the window display the in-window count.
- The existing histogram submenu content is unchanged; only its position moves.
- No backend/API changes are required; all data needed (intent metadata, policy decisions, session recency including closed sessions) is already served by existing endpoints and live events.

## Out of Scope

- Connection-level (TCP) client tracking — impossible with a stateless transport and explicitly rejected.
- Any Web UI changes.
- Grouping non-consecutive records or cross-tool clustering (e.g. by work session).
- User-configurable thresholds, row counts, or lookback windows.
- Windows/Linux tray parity (the Go systray app has no glance section).

## Commit Message Conventions *(mandatory)*

### Issue References
- ✅ **Use**: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

### Example Commit Message
```
feat(tray): group repeated glance rows and show intent reasons

Related #[issue-number]

Collapse consecutive same-tool calls, render intent reasons as a second
line, mark only failures, reorder the 24h histogram, and show idle
clients instead of an empty section.
```
