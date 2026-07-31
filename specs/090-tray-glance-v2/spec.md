# Feature Specification: Tray Glance v2 — Grouped Calls, Intent Reasons, Failure-Only Marks, Idle Clients

**Feature Branch**: `090-tray-glance-v2`
**Created**: 2026-07-31
**Status**: Draft (revised after Codex review round 1)
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

**Independent Test**: Feed the row pipeline a recorded sequence containing consecutive same-tool runs; verify the rendered rows collapse each run to one row with the correct count, ordering, and age, while non-consecutive repeats stay separate rows.

**Acceptance Scenarios**:

1. **Given** the activity feed holds 19 consecutive `jira_get_issue` calls followed by older calls to other tools, **When** the user opens the menu, **Then** the first row reads as one entry with a ×19 count and the remaining rows show the older distinct tools.
2. **Given** a run of identical calls, **When** the row renders, **Then** its age is the age of the newest call in the run.
3. **Given** calls to tool A, then tool B, then tool A again, **When** rows render, **Then** the two A episodes remain separate rows (grouping is consecutive-only, preserving the timeline).
4. **Given** a run of 12 calls where one failed, **When** the row renders, **Then** the row is marked as failed (failure dominates the group) and shows the newest failing call's error clause.
5. **Given** a single (non-repeated) call, **When** the row renders, **Then** no count suffix is shown.
6. **Given** two calls to the same tool separated only by records that never render (management built-ins, collapsed wrapper records), **When** rows render, **Then** the two calls belong to one run — exclusion happens before grouping, so excluded records never split a run.

---

### User Story 2 - The reason a call happened is visible (Priority: P2)

The user glances at the menu and understands not just *which* tools ran but *why*: each row shows the caller-declared intent reason (e.g. "Handoff: move ticket to review per user request") as a secondary, visually subdued line under the tool name.

**Why this priority**: The reason field is the difference between an audit trail and a story. It exists on almost every call already; showing it turns the glance into an explanation of agent behavior.

**Independent Test**: Render rows from records with and without intent reasons; verify the reason renders as a subdued second line on a standard (non-view-backed) menu row, truncated to fit, with the full text available on hover and to assistive technology.

**Acceptance Scenarios**:

1. **Given** a call with intent reason "Verify the failed transition did not change the ticket" on macOS 14.4 or newer, **When** the row renders, **Then** the reason appears as the row's standard subtitle — a visually subdued second line of the same menu row.
2. **Given** a reason longer than the reason budget, **When** the row renders, **Then** the reason is tail-truncated with an ellipsis and the full reason is available in the row's tooltip.
7. **Given** the app runs on macOS older than 14.4, **When** a row with a reason renders, **Then** the row renders as a single line and the reason remains available via tooltip and assistive-technology label (documented degradation; the subtitle mechanism does not exist there).
3. **Given** a call without a reason (e.g. an internal discovery call), **When** the row renders, **Then** the row renders as a single line with no empty second line.
4. **Given** a grouped row (×N) whose newest record has no reason but an older record in the run does, **When** the row renders, **Then** the reason shown is the newest record in the run that has one.
5. **Given** a row with a reason, **When** read by assistive technology, **Then** the spoken label includes the reason.
6. **Given** a call arriving over the live stream, **When** its row renders before the next poll, **Then** the reason is already present (the live event carries the intent data).

---

### User Story 3 - Only failures are marked, and blocked calls appear (Priority: P2)

Successful calls render without any status icon — quiet by default. A failed call carries a red failure mark plus its first error clause; a policy-blocked call carries a distinct warning mark plus the block reason. Policy blocks — invisible today — appear as rows.

**Why this priority**: Failure visibility is the core safety value of the glance, and it is currently diluted by 95% green noise. Blocked calls are the proxy *doing its job* and must be seen.

**Independent Test**: Render a feed containing successes, errors, and policy blocks; verify successes have no status icon, errors and blocks have distinct marks, and blocked records qualify as rows with their block reason.

**Acceptance Scenarios**:

1. **Given** a successful call, **When** the row renders, **Then** it carries no status icon and assistive technology still announces it as succeeded.
2. **Given** a failed call, **When** the row renders, **Then** it carries a red failure mark and shows the first clause of the error message.
3. **Given** a policy-blocked call (e.g. "Intent rejected: tool variant conflicts with server annotations"), **When** the feed refreshes, **Then** the block appears as a row with a warning mark distinct from the failure mark, and the block reason occupies the row's second line.
4. **Given** a mix of successes and one failure in the feed, **When** the menu opens, **Then** the failure is visually the only marked row among them.
5. **Given** 27 consecutive blocked attempts at the same tool, **When** rows render, **Then** they form one blocked row with a ×27 count, and never merge with successful calls to the same tool.

---

### User Story 4 - Clients show presence honestly instead of vanishing (Priority: P2)

The user opens the menu after lunch. Instead of "No connected clients", the Clients section lists the AI clients that recently used the proxy, each with a presence state: **active** (used it in the last few minutes), **idle** (quiet but recent), or **seen** (older), with the time since last activity.

**Why this priority**: An empty Clients section is actively misleading — the user reported it as the most confusing part of the current menu. Presence states match the stateless reality of the transport.

**Independent Test**: Feed the section session records with various last-activity ages and statuses; verify classification into active/idle/seen, deduplication per client, ordering, and that the section is non-empty whenever any retained session falls inside the lookback window.

**Acceptance Scenarios**:

1. **Given** a client whose last activity was 2 minutes ago, **When** the section renders, **Then** the client shows as active (filled indicator).
2. **Given** a client whose last activity was 20 minutes ago (session may already be closed), **When** the section renders, **Then** the client shows as idle with its age (e.g. "idle · 20m").
3. **Given** a client last seen 3 hours ago within the lookback window, **When** the section renders, **Then** the client shows as seen with its age, visually quieter than idle.
4. **Given** one client with several sessions (e.g. claude-code reconnecting), **When** the section renders, **Then** the client appears once, classified by its most recent session.
5. **Given** no retained session inside the lookback window, **When** the section renders, **Then** the section states no recent clients (and only then).
6. **Given** both active and idle clients, **When** the summary line renders, **Then** it reads counts by state (e.g. "2 active · 1 idle") instead of a single client count.
7. **Given** a session that started two hours ago but was active one minute ago, alongside many newer short sessions, **When** the section renders, **Then** that client still appears as active — recency of *activity*, not of session start, decides both inclusion and ordering.

---

### User Story 5 - The 24h picture is the first thing under the summary (Priority: P3)

The Activity (24h) histogram entry sits directly under the summary line, above the Recent rows, so the day-level picture comes before the call-level detail.

**Why this priority**: Pure reordering — valuable but trivially small, and independent of everything else.

**Independent Test**: Open the menu; verify block order is summary → Activity (24h) → Recent rows → Clients.

**Acceptance Scenarios**:

1. **Given** the glance is visible, **When** the menu opens, **Then** the Activity (24h) entry appears directly below the summary line and above the "Recent" header.

---

### Edge Cases

- A run larger than the fetched page: the count reflects only the fetched window (e.g. a 150-call run over a 100-record page shows ×100); no claim is made beyond the page.
- The five most recent ungrouped calls are one run: grouping frees rows, so older distinct tools *within the fetched page* fill the remaining rows; if the entire page is one run, one row renders.
- A live-streamed (not yet persisted) record extends a group that started from polled records — the group must not split or double-count when the reconciling poll replaces provisional records.
- A grouped row's count or age changes while the menu is open: the row's text updates in place; but a change that would alter the row's line count (reason appearing/disappearing) or the number of menu items is structural and waits for menu close.
- A record with no reason and no error renders as a single-line row; mixed feeds render mixed one- and two-line rows.
- A blocked record has no paired tool-call record (the call never dispatched) — it renders standalone.
- A policy decision whose decision is a warning or redaction (not a block) does not qualify as a row.
- A client session with no client name renders as "Unknown client", still classified by age.
- A session with no last-activity value falls back to its start time; a session whose timestamps cannot be parsed is excluded.
- Clock skew or a stale timestamp yielding a negative age renders as "0s", never a negative.
- When the core is stopped or unreachable, the glance block hides entirely (existing behavior, unchanged).

## Requirements *(mandatory)*

### Functional Requirements

**Row pipeline** (pure, unit-testable, in this exact order):

- **FR-001**: The row pipeline MUST process fetched records in four ordered steps: (1) qualify records (drop management built-ins and other non-qualifying records), (2) collapse wrapper/upstream record pairs sharing a request identity, (3) group maximal runs of consecutive surviving records sharing the same group key, (4) take the first five groups. Records dropped in steps 1–2 MUST NOT split a run in step 3.
- **FR-002**: The group key MUST be (server, tool, outcome class), where outcome class separates policy blocks from calls: blocked records group only with blocked records; successful and errored calls group together.
- **FR-003**: A group of N > 1 records MUST render a "×N" suffix; a single-record group renders no suffix.
- **FR-004**: A grouped row MUST derive its age from the newest record of the run, its reason from the newest record in the run that has one, and its status from the worst outcome in the run, ordered error > success. When a run contains an error, the displayed error clause comes from the newest erroring record.

**Reason display**:

- **FR-005**: Each row MUST display its reason, when present, via the standard menu-row subtitle mechanism (macOS 14.4+): call rows show the caller-declared intent reason; blocked rows show the policy block reason. On macOS versions where the subtitle mechanism does not exist (< 14.4, the app's current deployment floor is 13), the row renders single-line and the reason is available via tooltip and assistive-technology label only.
- **FR-006**: The reason subtitle has its own character budget (60 characters), independent of the label budget; longer reasons are tail-truncated with an ellipsis. The full reason MUST be available via tooltip and assistive-technology label on all macOS versions.
- **FR-007**: Rows for records without a reason MUST render as a single line.
- **FR-008**: Live-streamed events MUST carry their intent reason into the row without waiting for the reconciling poll.
- **FR-009**: Activity and client rows MUST remain standard text menu items; custom view-backed rows are prohibited for these rows. The subtitle mechanism is a property of standard menu items and preserves keyboard navigation and VoiceOver behavior.

**Status marking**:

- **FR-010**: Successful rows MUST render without a status icon; assistive technology MUST still announce the outcome.
- **FR-011**: Failed rows MUST carry a red failure mark and the first clause of the error message; blocked rows MUST carry a visually distinct warning mark (distinct in shape, not color alone).
- **FR-011a**: When a failed call has both an error and an intent reason, the title line composes as "label [×N] · error-clause — age" and the subtitle remains the intent reason — the error never displaces the reason. Truncation precedence on the title line: the error clause is tail-truncated to a 40-character budget before the label's existing middle-truncation budget (34) is tightened; the age is never truncated. Tooltip carries the full label, full reason, and full error message.
- **FR-012**: Policy-blocked records (persisted status "blocked" / decision "block" only — warnings and redactions do not qualify) MUST qualify as glance rows, including when no corresponding tool-call record exists.

**Data contract** (backend):

- **FR-013**: The activity listing used by the glance poll MUST be able to return, per record, the small contextual fields — intent reason, intent operation type, policy decision and reason, client name — while continuing to omit bulky payload fields (arguments, responses). The poll's payload size must stay within the same order of magnitude as today's projected poll (tens of KB, not hundreds).
- **FR-014**: The glance poll MUST include policy-decision records (type filter extended).
- **FR-015**: Policy-decision records MUST carry the same request identity as other activity records, in both live events and persisted records, so live rows reconcile with polled rows without duplication. Records predating this change (no request identity) fall back to their storage identity and are never collapsed.
- **FR-016**: The sessions listing used by the tray MUST return sessions ordered by *last activity* (not by session start) among retained sessions, regardless of session status, with the ordering applied before any truncation.
- **FR-016a**: The tray's session poll MUST request the entire retained-session page (the retention cap, currently 100, unfiltered by status) so that deduplication and summary counts operate over every retained session, not a truncated page.

**Clients section**:

- **FR-017**: The Clients section MUST consider retained sessions whose last activity falls within a 24-hour lookback window, regardless of session status, deduplicated per client (name + version, keeping the most recent), each classified by time since last activity: active (< 5 min), idle (≥ 5 min and ≤ 30 min), seen (> 30 min). Boundary values 5:00 and 30:00 are idle.
- **FR-018**: The section MUST display at most five client rows, ordered by most recent activity; each row shows a presence indicator whose shape or fill differs per state (not color alone) and, for idle/seen states, the time since last activity.
- **FR-019**: The summary line MUST count all qualifying clients in the lookback window (not only displayed rows), reported by state as "N active · M idle", omitting empty states; "seen" clients are excluded from the summary counts, so a feed with only "seen" clients yields a summary with no client segment while the rows remain visible.
- **FR-020**: The "no recent clients" placeholder MUST appear only when no retained session falls within the lookback window.

**Layout**:

- **FR-021**: The Activity (24h) entry MUST appear directly below the summary line and above the "Recent" header.

**Preserved invariants**:

- **FR-022**: Opening the menu MUST NOT trigger any network request. This MUST be verified by a test that snapshots the request counters, drives the actual menu-open path, and asserts a zero delta.
- **FR-023**: While the menu is open, existing menu items' text MAY update in place, but the number of menu items, their order, and each row's line count MUST NOT change; such structural changes are deferred to menu close. ("Identity" here means the menu-item objects; an item MAY come to represent a different record, in which case its click payload, icon, and accessibility text are rewritten together.)
- **FR-024**: A grouped row's stable identity for in-place updates is the record identity of the *oldest* record in its run (stable while a run extends with newer records).
- **FR-025**: All rows MUST remain reachable by keyboard navigation and meaningfully announced by VoiceOver.

### Key Entities

- **Activity record**: One proxied event — a tool call, internal tool call, or policy decision — with server, tool, outcome, timestamp, optional reason (caller intent or policy block reason), optional error message, and a request identity used for wrapper collapse and live/poll reconciliation (absent only on legacy policy records, which are then never collapsed).
- **Glance run (grouped row)**: A maximal sequence of consecutive qualifying records sharing a group key (server, tool, outcome class), presented as one row with count, newest age, newest available reason, and worst outcome; identified across updates by its oldest record.
- **Client presence**: A deduplicated view of one client application derived from its retained sessions: display name, version, last-activity time, and derived state (active / idle / seen).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Replaying the committed reference fixture (`specs/090-tray-glance-v2/fixtures/activity-replay.jsonl`, a sanitized derivative of the 6-week export preserving its event order, run-length, status, and reason-length distributions — 1,564 events, 52 blocked / 32 error / 1,480 success) through the row pipeline, no two adjacent rendered rows ever share a group key, and a burst of ≥ 19 identical calls occupies exactly one of the five rows.
- **SC-002**: On macOS 14.4+, for call records carrying a reason (98.7% in the reference export), the reason (possibly truncated) is visible directly in the open menu without any further interaction.
- **SC-003**: In a feed where under 6% of events are failures (the reference ratio), failed or blocked rows are the only rows carrying status marks.
- **SC-004**: During sequential replay of the reference fixture, every policy block becomes visible as (part of) a rendered row at some point; today that number is zero.
- **SC-005**: After 20 minutes of client inactivity, the Clients section still names the client (as idle) rather than showing an empty state; among retained sessions, the empty state appears only when nothing was active within 24 hours.
- **SC-006**: Driving the real menu-open path issues zero network requests, asserted via request-counter deltas.
- **SC-007**: All existing glance unit-test suites still pass, extended to cover the pipeline order, grouping, reason rendering, failure-only marks, blocked rows, presence classification, and boundary timestamps.
- **SC-008**: Visual encodings (subdued subtitle, distinct failure/blocked marks, presence indicator fill/shape) are verified by the manual test protocol at `specs/090-tray-glance-v2/verification/manual-protocol.md` — a step list with expected screenshots covering: two-line row with reason (macOS 14.4+), single-line fallback (macOS 13 if available, else waived), failed row, blocked row, grouped ×N row, each presence state, VoiceOver walk of the five rows, and summary line variants — since automated tests assert only the model-level encoding.

## Assumptions

- Presence thresholds (5 min active, 30 min idle, 24 h lookback) are fixed presentation policy, not user-configurable; the idle boundary matches the session inactivity timeout (30 min).
- The dedupe key for clients is client name + version; two versions of the same client are two rows.
- Session retention (currently the 100 most recent sessions) bounds the client lookback: a client evicted from retention is absent even if it was active within 24 hours. The guarantee in FR-020/SC-005 is scoped to retained sessions.
- Blocked rows draw their reason from the policy decision's own reason field; call rows draw theirs from the caller-declared intent.
- The grouped count reflects the fetched window (up to 100 records per poll); runs longer than the window display the in-window count.
- The existing histogram submenu content is unchanged; only its position moves.
- The app's deployment floor stays at macOS 13; the reason subtitle is a macOS 14.4+ enhancement with a documented single-line degradation below that. Raising the floor is a separate product decision, out of scope here.
- The raw 6-week export contains workplace data and is NOT committed; the committed fixture is a sanitized derivative with equivalent statistical structure.
- Backend changes are limited to: the lightweight contextual-metadata projection (FR-013), request identity on policy decisions (FR-015), and last-activity ordering for the sessions listing (FR-016). No storage schema migration is required; legacy records degrade gracefully.

## Out of Scope

- Connection-level (TCP) client tracking — impossible with a stateless transport and explicitly rejected.
- Any Web UI changes.
- Grouping non-consecutive records or cross-tool clustering (e.g. by work session).
- User-configurable thresholds, row counts, or lookback windows.
- Windows/Linux tray parity (the Go systray app has no glance section).
- Raising the session retention cap or persisting handshake-only sessions.

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
