# Feature Specification: Global Tools Overview Page

**Feature Branch**: `050-global-tools-page`
**Created**: 2026-05-18
**Status**: Draft
**Input**: User description: "Global Tools Overview page (issue #437): a single table-style page listing every tool across all configured MCP servers, with filtering, sorting, and batch enable/disable, for auditing and curating large MCP setups."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - See every tool in one place (Priority: P1)

A user with many MCP servers configured (e.g., 12 servers exposing ~500 tools, some setups ~700) opens a single "Tools" page and sees one table containing every tool from every configured server — including tools from disabled servers and tools that are individually disabled or denied by config. Each row shows the tool name, its server, a short description, a risk indicator, approval status, whether it is currently enabled, how many times it has been used recently, and when it was last used.

**Why this priority**: This is the core of the request. Without a single global view, auditing tools across many servers is impractical — the user must visit each server's detail page individually. This story alone delivers the primary value: visibility.

**Independent Test**: Configure multiple servers (including one disabled), open the Tools page, and confirm every tool from every server appears in the table with its server, state, approval status, and usage columns populated.

**Acceptance Scenarios**:

1. **Given** several configured servers with many tools, **When** the user opens the Tools page, **Then** all tools from all servers are listed in one table with no per-server navigation required.
2. **Given** a server that is disabled, **When** the user opens the Tools page, **Then** that server's tools still appear, clearly indicated as not enabled.
3. **Given** a tool that has never been called, **When** the user views its row, **Then** its usage count shows zero and its last-used value shows that it has never been used.
4. **Given** the page is open, **When** the user reads the summary cards, **Then** the total number of tools and the counts of enabled, disabled, and pending-approval tools are shown and match the table contents.

---

### User Story 2 - Find and narrow down tools (Priority: P1)

The user filters and sorts the table to focus on a subset: typing in a search box to match by tool name, description, or server; choosing a specific server; filtering by enabled/disabled state, by risk level, or by approval status; and sorting any column (e.g., by last-used to find stale tools, or by usage count to find unused ones).

**Why this priority**: With hundreds of tools, an unfiltered list is unusable for decision-making. Search and filter are required for the page to deliver on its "audit and cleanup" purpose, so this is co-equal P1 with the listing itself.

**Independent Test**: With the full list loaded, type a substring and confirm only matching tools remain; apply each dropdown filter and confirm the list narrows correctly; click a column header and confirm the order changes; confirm disabled and config-denied tools remain visible when matched (search must not hide them).

**Acceptance Scenarios**:

1. **Given** the full tool list, **When** the user types text in the search box, **Then** only tools whose name, description, or server contains that text remain visible, and disabled/denied tools are not excluded from results.
2. **Given** the full tool list, **When** the user selects a server in the server filter, **Then** only that server's tools are shown.
3. **Given** the full tool list, **When** the user filters by state, risk, or approval status, **Then** only tools matching that criterion are shown, and filters combine (all active filters apply together).
4. **Given** a filtered/unfiltered list, **When** the user clicks a sortable column header, **Then** the rows reorder by that column and a second click reverses the order.
5. **Given** more tools than fit on one page, **When** the user pages through results, **Then** the active filters and sort order are preserved across pages.

---

### User Story 3 - Bulk enable/disable tools (Priority: P2)

The user selects multiple tools (individually, or all tools currently shown on the page) and applies a single batch action to enable or disable them all at once. Progress is shown, and if some tools fail, the user is told which succeeded and which did not.

**Why this priority**: This is the highest-leverage cleanup action and explicitly requested ("50–100 of them needing to be disabled"). It depends on the listing and filtering stories existing first, so it is P2 — valuable but built on P1.

**Independent Test**: Select several tools across different servers, choose "Disable selected", and confirm each selected tool's state changes to disabled and the table reflects it; repeat with "Enable selected"; simulate a partial failure and confirm the user sees a per-tool success/failure summary.

**Acceptance Scenarios**:

1. **Given** the tools table, **When** the user checks individual rows, **Then** a batch action bar appears showing how many tools are selected.
2. **Given** a filtered table, **When** the user uses "select all", **Then** only the tools currently shown (matching active filters, on the current page) are selected.
3. **Given** one or more selected tools, **When** the user clicks "Disable selected", **Then** every selected tool becomes disabled and the table and summary cards update to reflect the new states.
4. **Given** a batch action where some tools fail to update, **When** the action completes, **Then** the user sees how many succeeded and which specific tools failed, and successfully changed tools remain changed.

---

### User Story 4 - CLI parity for global tool curation (Priority: P2)

A user driving mcpproxy from the terminal or scripts needs the same global view and curation actions the web page offers, without a browser. They run a single command to list every tool across all servers (with the same server/state/risk/approval filters and machine-readable JSON/YAML output), and run enable/disable commands that accept one or more `server:tool` targets to curate in bulk from a script.

**Why this priority**: mcpproxy's product principle is CLI/UI parity for every management surface (the project ships activity, tokens, upstream, quarantine, etc. as CLI commands mirroring the UI). A global tools page with no CLI equivalent breaks that contract and blocks automation/scripted cleanup. It depends on the same consolidated data source as the web page, so it is P2.

**Independent Test**: With multiple servers configured, run the global list command with no server argument and confirm all tools across all servers are listed with state/approval/usage columns and that `-o json` emits the same consolidated data; run the disable command with several `server:tool` arguments and confirm each becomes disabled (verifiable via the list command and the web page).

**Acceptance Scenarios**:

1. **Given** multiple configured servers, **When** the user runs the tools list command without specifying a server, **Then** tools from all servers are listed in one output, including disabled-server and individually-disabled tools.
2. **Given** the global list command, **When** the user applies server/state/risk/approval filters or requests JSON/YAML output, **Then** the output reflects exactly the same filtering semantics and data as the web page's consolidated source.
3. **Given** one or more `server:tool` targets, **When** the user runs the enable (or disable) command, **Then** every targeted tool's state changes accordingly and a per-target success/failure summary is printed; partial failures leave succeeded targets changed and exit with a non-zero status.
4. **Given** an invalid or unknown `server:tool` target, **When** the user runs an enable/disable command, **Then** a clear error identifies the bad target without aborting valid targets in the same invocation.

---

### Edge Cases

- **No servers / no tools**: The page shows an empty state explaining that connecting MCP servers will populate it, with a link to server management.
- **A server is unreachable or errors while listing its tools**: The page still renders all tools it could gather and indicates that one or more servers could not be fully read, rather than failing the whole page.
- **Very large tool count (700+)**: The page remains usable — filtering, sorting, and paging stay responsive and do not require the user to wait on a server-by-server load.
- **A tool's state changes elsewhere (another client, CLI) while the page is open**: Refreshing the page reflects the current authoritative state; the page does not show stale enable/disable state after a refresh.
- **Search interacts with cleanup intent**: Searching must never hide disabled or config-denied tools that match the query, because those are exactly the tools a user is trying to find and clean up.
- **Batch action partially applied then interrupted**: Tools already changed stay changed; the summary makes the partial outcome explicit so the user can retry only the failures.
- **Config-denied tools**: Tools blocked by configuration policy are shown as not enabled and are visually distinguishable from user-disabled tools; the page does not let the user "enable" a config-denied tool into an inconsistent state.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST provide a single page that lists every tool from every configured MCP server, including tools from disabled servers and individually disabled or config-denied tools.
- **FR-002**: The system MUST present the global tool list from one consolidated data source so the page does not degrade as the number of servers grows (no per-server sequential loading visible to the user).
- **FR-003**: Each tool row MUST display: tool name, originating server, a truncated description, a risk indicator (read / write / destructive), approval status, current enabled state, recent usage count, and last-used time (or an explicit "never used" indication).
- **FR-004**: The page MUST show summary counts for total tools, enabled tools, disabled tools, and tools pending approval, and these MUST stay consistent with the underlying list.
- **FR-005**: The system MUST derive "enabled" as a single clear state from the underlying user-disabled and config-denied conditions, so the user is never shown an ambiguous enabled/disabled status.
- **FR-006**: Recent usage count and last-used time MUST be derived from recorded activity over a fixed recent window; tools with no activity in that window MUST report zero usage and "never used" (within the window) without error.
- **FR-007**: The page MUST provide a free-text search that filters the visible tools by substring match against tool name, description, and server, and MUST NOT exclude disabled or config-denied tools from search results.
- **FR-008**: The page MUST provide filters for server, enabled/disabled (and config-denied) state, risk level, and approval status, and these filters MUST combine (logical AND).
- **FR-009**: The page MUST allow sorting by any displayed column, with ascending/descending toggle, and MUST preserve active filters and sort order while paging through results.
- **FR-010**: The page MUST allow the user to inspect a single tool's full details (including its input schema) without leaving the page.
- **FR-011**: The user MUST be able to select multiple tools, including a "select all currently shown" action that selects only the tools matching active filters on the current page.
- **FR-012**: The user MUST be able to apply a batch "enable" or "disable" action to all selected tools in one operation, with visible progress.
- **FR-013**: When a batch action partially fails, the system MUST report which tools succeeded and which failed, and MUST leave successfully changed tools in their new state.
- **FR-014**: The page MUST be reachable from the primary navigation and MUST display a live count of total tools in the navigation entry.
- **FR-015**: The page MUST show clear empty, loading, and partial-error states.
- **FR-016**: The existing tool-discovery mechanism used by AI agents MUST remain unchanged and unaffected by this feature (this page is a separate audit surface, not a change to agent discovery).
- **FR-017**: The CLI MUST provide a command that lists every tool across all configured servers when no specific server is given, drawing from the same consolidated data source as the web page (no per-server invocation required by the user).
- **FR-018**: The CLI global list command MUST support the same filtering dimensions as the web page (server, enabled/disabled/config-denied state, risk level, approval status) and MUST support machine-readable output (JSON/YAML) consistent with the rest of the CLI.
- **FR-019**: The CLI MUST provide commands to enable and to disable tools that accept one or more `server:tool` targets in a single invocation, applying the same per-tool state change as the web page's batch action.
- **FR-020**: When a CLI enable/disable invocation contains multiple targets, the command MUST process each independently, print a per-target success/failure summary, leave successfully changed targets changed, and exit non-zero if any target failed; an invalid target MUST NOT abort the remaining valid targets.

### Out of Scope (v1)

The following appear in the originating issue as desirable but are explicitly deferred to a future iteration and MUST NOT be implemented in this feature:

- Permanently hiding tools (distinct from disabling them)
- Assigning custom tags or categories to tools
- Resetting tool permissions in bulk
- User-configurable usage/analytics time window
- Any change to the agent-facing relevance-ranked tool discovery

### Key Entities *(include if feature involves data)*

- **Tool (global view)**: A single tool exposed by one MCP server, as presented on this page. Attributes: name, owning server, description, risk/operation classification, approval status, user-disabled flag, config-denied flag, derived enabled state, recent usage count, last-used timestamp.
- **Tool usage summary**: Per-tool aggregation over a fixed recent activity window — a count of invocations and the most recent invocation time. Absence of records means zero usage / never used within the window.
- **Global tools response**: The consolidated payload backing the page: the full list of tools (across all servers) plus aggregate counts (total, enabled, disabled, pending approval).
- **Batch selection**: The set of tools the user has selected for a bulk action, scoped to what is currently shown when "select all" is used.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A user managing 12+ servers and ~500 tools can see all tools in one place and locate a specific tool by name or server in under 15 seconds, without visiting any per-server page.
- **SC-002**: A user can disable a set of 50+ unwanted tools in a single batch operation from one screen, instead of toggling them one by one across multiple server pages.
- **SC-003**: With 700 tools loaded, applying a search term, a filter, or a column sort updates the visible list in under 1 second as perceived by the user.
- **SC-004**: Disabled and config-denied tools remain discoverable on the page (including via search), so 100% of cleanup-candidate tools can be located from this single surface.
- **SC-005**: After a batch enable/disable, the page's displayed states and summary counts match the system's authoritative state on the next load, with any partial failures explicitly surfaced to the user.
- **SC-006**: Opening or using the page does not slow down or alter AI-agent tool discovery; agent discovery behavior is unchanged.
- **SC-007**: A user with no browser can list all tools across all servers and disable a set of unwanted tools entirely from the command line, with the same filtering and data as the web page, and a script can detect partial failures via exit status.

## Assumptions

- "Recent" usage is aggregated over a fixed 30-day window for v1; making the window configurable is explicitly out of scope.
- Per-tool enable/disable uses the existing per-server tool toggle capability; batch actions are an orchestration over that existing capability, not a new persistence model.
- Approval status, user-disabled state, and config-denied state already exist as authoritative per-tool data (from prior security/quarantine and layered-config work) and are surfaced as-is.
- The risk indicator reuses the existing operation-type classification (read / write / destructive); no new risk model is introduced.
- The page replaces the existing unused/orphaned tools view rather than adding a parallel one, to avoid two competing "tools" surfaces.
- Search is a deterministic substring filter over the already-loaded set, deliberately not relevance-ranked discovery, because the audit use case requires seeing every matching tool (including disabled ones) in a stable, user-sortable order.
- Reasonable web-app defaults apply for pagination size, debounce on search input, and user-friendly error messaging.
- CLI parity is delivered within this feature (not a separate spec) because it consumes the same consolidated data source and per-tool state-change capability as the web page; it extends the existing `mcpproxy tools` command group (today `tools list --server` is debug-only and server-scoped) with a global default and curation subcommands, following the project's established CLI/UI parity pattern (activity, tokens, upstream, quarantine).

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #437` — links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #437`, `Closes #437`, `Resolves #437`

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors. (This also matches the repo-specific rule of no Claude git attribution in mcpproxy-go.)

### Example Commit Message
```
feat: global tools overview page

Related #437

Adds a single table-style page listing every tool across all configured
MCP servers, with substring search, server/state/risk/approval filters,
column sorting, and batch enable/disable.

## Changes
- Consolidated global tools data source with per-tool usage aggregation
- Tools page with summary cards, filter bar, sortable table, batch actions
- Navigation entry with live tool count

## Testing
- Backend table tests for usage aggregation + aggregation handler (race)
- API E2E assertion for the global tools payload shape
- Playwright UI sweep with HTML report (empty/loaded/filter/sort/batch)
```
