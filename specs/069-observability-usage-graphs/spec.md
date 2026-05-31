# Feature Specification: Observability — Usage Statistics & Graphs in the Web UI

**Feature Branch**: `069-observability-usage-graphs`
**Created**: 2026-05-31
**Status**: Draft
**Lineage**: H2-2026 roadmap PILLAR A (Adopt / usability). Builds on the activity log (specs 016/019/024). Paperclip goal `d7164fdf`.
**Input**: Help users SEE where their AI agents spend tokens and time, so they can speed up MCP usage and cut token cost. Surface usage statistics as graphs in the Web UI — tool-call histogram (calls + tokens per tool), response-size-by-tool, tool errors, a tool-call timeline — behind a dashboard switcher, with a fast backend (caching / precomputation).

## Clarifications

### Session 2026-05-31

- Q: Build new telemetry, or surface existing data? → A: Surface existing data. The activity log already records per-call server, tool, status, duration, and request/response byte sizes; this feature aggregates and visualizes it.
- Q: Tokens per tool — do we have them? → A: Not per call. Bytes are recorded; tokens are not. v1 uses response **bytes** as the token proxy and shows the existing "tokens saved by retrieve_tools" metric; accurate per-call token estimation is an explicit later requirement.
- Q: Where do the graphs live? → A: On the existing Dashboard, behind a switcher/tab ("Overview" ↔ "Usage"), reusing the activity REST API.
- Q: How is fast response guaranteed? → A: An incrementally-maintained, actor-owned stats aggregate updated on each activity write (O(1) per write/read), periodically persisted, with a short-TTL cache for wide windows — not an on-demand full scan.
- Q: Charting library? → A: None is installed today; the plan selects one lightweight Vue-compatible library (decision recorded in plan.md). The spec stays library-agnostic.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — See which tools consume the most tokens (Priority: P1)

A user whose agent burns through context opens the Web UI dashboard, switches to the Usage view, and immediately sees which tools return the largest responses (the "token sinks") and which are called most often. They learn that one tool returns huge blobs on every call, and adjust how their agent uses it — saving tokens.

**Why this priority**: This is the core value — "where do my tokens go?" — and it answers the user's primary goal (save tokens / speed up MCP). It is buildable entirely on data already captured (response bytes + call counts), so it delivers the headline insight with no new capture.

**Independent Test**: With a populated activity log, open the Usage view; confirm a per-tool ranking by total + average response size and a per-tool call-count histogram render, ordered correctly, matching the underlying records.

**Acceptance Scenarios**:

1. **Given** recorded tool calls with response sizes, **When** the user opens the Usage view, **Then** tools are shown ranked by total response bytes (the token-sink view) and by call count.
2. **Given** the same data, **When** the user inspects a tool, **Then** its average and total response size and call count are shown.
3. **Given** no activity yet, **When** the user opens the Usage view, **Then** an empty-state is shown (no error).

---

### User Story 2 — Spot failing and slow tools (Priority: P1)

A user sees an error-rate breakdown per tool and per-tool latency, so they can identify tools that fail often (wasting agent round-trips) or respond slowly (slowing the agent), and fix or replace them.

**Why this priority**: Errors and latency directly waste tokens and time — a tool that fails half the time doubles the agent's round-trips. Same data is already captured (status, error, duration). Equal priority to US1 as the other half of "speed up MCP usage."

**Independent Test**: With a log containing successes and errors across tools, confirm an error-rate-per-tool graph and a latency (e.g. p50/p95) view render and match the records.

**Acceptance Scenarios**:

1. **Given** tool calls with success/error statuses, **When** the user opens the Usage view, **Then** error rate (and count) per tool is shown.
2. **Given** calls with recorded durations, **When** viewed, **Then** per-tool latency (median and a high percentile) is shown.

---

### User Story 3 — See activity over time on a timeline (Priority: P2)

A user views a timeline of tool calls (volume over time, sliceable by tool/server/status) to understand usage patterns and correlate spikes with their agent sessions — an at-a-glance companion to the activity log.

**Why this priority**: Valuable for understanding *when* and *in what bursts* tokens are spent, and a natural visual layer over the existing activity log. P2 because US1/US2 deliver the primary "what's expensive" insight first.

**Independent Test**: With time-distributed records, confirm a timeline/volume graph renders with correct buckets and respects the active filters.

**Acceptance Scenarios**:

1. **Given** records across a time window, **When** the user opens the timeline, **Then** call volume is shown bucketed over time.
2. **Given** an active filter (tool/server/status), **When** applied, **Then** the timeline reflects only matching calls.

---

### User Story 4 — Toggle graphs without losing the existing dashboard (Priority: P2)

A user switches between the current status/overview dashboard and the new usage graphs via a clear switcher, and can pick a time window (e.g. 24h / 7d / all). The graphs load fast enough not to delay the dashboard's first paint.

**Why this priority**: The switcher + windowing is what makes the graphs usable day-to-day rather than a separate page; the performance bar is what keeps the dashboard snappy. P2 because the graphs (US1–3) must exist first.

**Independent Test**: Confirm the switcher toggles Overview ↔ Usage, the window selector changes the data range, and the dashboard's initial render is not blocked waiting on graph data.

**Acceptance Scenarios**:

1. **Given** the dashboard, **When** the user toggles to Usage, **Then** the graphs appear and Overview state is preserved on switch-back.
2. **Given** a window selector, **When** the user picks 7d, **Then** all graphs reflect that window.

---

### User Story 5 — Headline "tokens saved by mcpproxy" (Priority: P3)

The dashboard shows a hero number: tokens saved by serving tool discovery through `retrieve_tools` instead of exposing all tools — making mcpproxy's core value visible.

**Why this priority**: Strong motivational/marketing metric and the data partially exists (token-savings metrics). P3 because it draws on a different (discovery-side) data source than the per-call graphs and is a single number, not a graph suite.

**Independent Test**: Confirm the saved-tokens figure renders from the existing token-savings metrics and updates with usage.

**Acceptance Scenarios**:

1. **Given** discovery token-savings metrics, **When** the dashboard loads, **Then** a tokens-saved figure is shown.

## Requirements *(mandatory)*

### Context & Constraints (locked)

- **CN-001**: Surface and aggregate EXISTING activity-log data; do not add a parallel telemetry system. (External telemetry, spec 042, is separate and unaffected.)
- **CN-002**: The graphs backend MUST be fast — incremental precompute + cache, not an on-demand full scan of the log on every request. It MUST NOT block the dashboard's first paint or the request hot path (Constitution I).
- **CN-003**: Aggregation MUST follow actor-based concurrency (Constitution II) — owned by the activity service, updated on write, snapshot for readers.
- **CN-004**: All data stays local (no new external calls). Privacy: graphs show the user their own usage; any sensitive-data flags already in the activity log are not exposed in aggregates.

### Functional Requirements

- **FR-001**: The system MUST provide a per-tool usage aggregation: for each tool (and server), total calls, total + average response size, total + average request size, error count/rate, and latency (median + a high percentile), over a selectable time window.
- **FR-002**: The Web UI MUST present at minimum four visualizations: (a) per-tool call histogram, (b) per-tool response-size ranking (token-sink view), (c) per-tool error rate, (d) a tool-call timeline — all reading the aggregation.
- **FR-003**: The Web UI MUST provide a dashboard switcher between the existing overview and the usage graphs, preserving overview state.
- **FR-004**: The system MUST support time-window selection (at least 24h, 7d, all) applied consistently across all graphs.
- **FR-005**: The aggregation MUST be served fast via incremental precomputation maintained on each activity write and/or a short-TTL cache, with a stated freshness bound; cold start MUST rebuild from the log.
- **FR-006**: v1 MUST use response bytes as the token proxy for the token-spend graphs and clearly label them as size-based.
- **FR-007**: The system MUST display the existing "tokens saved by retrieve_tools / discovery" metric as a headline figure.
- **FR-008**: Graphs MUST honor filtering by tool, server, and status consistent with the existing activity log filters.
- **FR-009**: Empty/low-data states MUST render gracefully (no errors, clear "no data yet").
- **FR-010**: (Phase 2) The system SHOULD capture an estimated token count per call (request and response) and switch the token graphs from bytes to estimated tokens, recorded as an explicit follow-on requirement.
- **FR-011**: The graphs endpoint MUST be covered by API tests, and the UI by the project's Web-UI verification workflow (Playwright + report).

### Key Entities

- **Tool usage aggregate**: per (server, tool) rollup — counts, byte sums/averages, error count, latency percentiles — over a window.
- **Time bucket**: pre-bucketed call counts/sizes over time for the timeline.
- **Tokens-saved figure**: the discovery-side savings metric surfaced on the dashboard.
- **Usage view + switcher**: the dashboard toggle, window selector, and graph components.

## Success Criteria *(mandatory)*

- **SC-001**: A user can identify their top token-consuming (largest-response) tools within one view, without reading raw logs.
- **SC-002**: A user can see error rate and latency per tool in the same view.
- **SC-003**: The timeline shows call volume over time and respects active filters.
- **SC-004**: The usage graphs do not measurably delay the dashboard's first paint (graphs load asynchronously).
- **SC-005**: The aggregation endpoint returns within a fast bound on a large activity log (precompute/cache proven by not scanning the full log per request).
- **SC-006**: Switching Overview ↔ Usage and changing the time window updates all graphs consistently.
- **SC-007**: With an empty log, the Usage view shows a clean empty state.
- **SC-008**: The tokens-saved headline renders from existing metrics.

## Assumptions

- `ActivityRecord` already carries timestamp, type, server, tool, status, error, duration, and request/response byte sizes (confirmed in the activity-log backend); no new per-call capture is needed for v1 graphs.
- A `GET /api/v1/activity/stats` endpoint and an `Activity.vue` view already exist and can be extended rather than replaced.
- No charting library is currently a frontend dependency; adding one lightweight library is acceptable (recorded in plan.md). Frontend changes require `make build` (the UI is embedded).
- Accurate per-call token counts are out of scope for v1 (bytes proxy); FR-010 tracks the upgrade.

## Dependencies

- The activity-log backend (`internal/runtime/activity_service.go`, `internal/storage/activity.go`, `internal/storage/models.go`) and its REST surface (`internal/httpapi/activity.go`).
- The existing token-savings metrics (`internal/config/config.go` TokenMetrics / discovery path).
- The Vue Web UI (`frontend/src/views/Dashboard.vue`, `Activity.vue`) + a charting library.

## Out of Scope

- A new/parallel telemetry pipeline (external telemetry is spec 042).
- Accurate per-call token counting (FR-010 is the phase-2 hook; not v1).
- Cost-in-currency estimates (needs token + pricing; later).
- Cross-session/historical export beyond the existing activity export.

## Edge Cases

- **Very large activity log**: aggregation must not scan it per request (incremental precompute + cache; CN-002/FR-005).
- **Restart**: precomputed aggregate rebuilds from the persisted log/rollup on cold start.
- **Bytes ≠ tokens**: token graphs labeled as size-based until FR-010 lands; avoid implying exact token counts.
- **Sparse/empty data**: clean empty states (FR-009).
- **High-cardinality tools**: rank + top-N with an "other" bucket so the histogram stays readable.
