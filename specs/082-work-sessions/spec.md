# Feature Specification: Work Sessions

**Feature Branch**: `082-work-sessions`
**Created**: 2026-07-12
**Status**: Draft
**Input**: User description: "Work sessions: make 'session' mean the user's work, not the transport connection."

## Problem

Today a "session" in MCPProxy is a transport connection: one record per MCP `initialize` handshake, keyed by the connection's session id. That is not what a user means by the word.

Measured on a real user's machine over roughly one day:

| Observation | Value |
|---|---|
| Session records | 100 (cap) — 101 created |
| From one client (`claude-code`) | 99 |
| Records with **zero** tool calls | 99 of 100 |
| Median duration | **0.0 minutes** (max 18 seconds) |
| Interval between records | every ~15.3 minutes, **including overnight** |

Almost every "session" is an automated agent that connects, does nothing, and disconnects while the user is asleep. Meanwhile the records the user *does* care about are pushed out of the 100-record cap within a day — which is why the Activity Log's Session filter shows bare hashes (`...139c9`) for anything older than about 24 hours: the session record its rows pointed at has already been evicted.

The user cannot answer the question they actually have: **"show me what happened while I was working on project X."**

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Stop counting connections that did nothing (Priority: P1)

A user opens the Sessions page and sees only sessions where something actually happened. Background agents, health probes, and clients that connect but never call a tool do not appear, and do not consume the retention budget.

**Why this priority**: This alone removes ~99% of the observed noise, requires no new concepts, and is independent of every other story. It is the difference between a Sessions page that is unusable and one that is merely incomplete. It also stops the session store from evicting the records the other stories depend on.

**Independent Test**: Connect a client that performs the handshake and immediately disconnects without calling a tool; confirm no durable session record is created. Then connect a client that calls one tool; confirm exactly one record appears.

**Acceptance Scenarios**:

1. **Given** a client connects and disconnects without invoking any tool, **When** the user views the Sessions page, **Then** no record for that connection is shown.
2. **Given** a client connects and invokes at least one tool, **When** the user views the Sessions page, **Then** exactly one record for that work appears.
3. **Given** 100 background agents connect and do nothing, followed by one real user session, **When** the user views the Sessions page, **Then** the real session is present and has not been evicted.

---

### User Story 2 - Group my work by project (Priority: P2)

A user working in two projects sees their activity attributed to each project by name. The Activity Log's session filter offers entries like "Claude Code · mcpproxy-go" rather than an opaque id, so the user can select the work they did on one project and see only that.

**Why this priority**: This is the feature the user asked for — the ability to say "what happened while I worked on project X". It depends on Story 1 to be usable (otherwise the entries are drowned in noise) but delivers the headline value.

**Independent Test**: Run one client in project A and one in project B, invoke a tool in each, and confirm the Activity Log filter offers two distinct, project-named entries that each filter to only their own records.

**Acceptance Scenarios**:

1. **Given** a client working in a project the system can identify, **When** the user opens the Activity Log session filter, **Then** the entry is labelled with the client and the project name.
2. **Given** a client that does not disclose its project, **When** the user opens the session filter, **Then** the entry is labelled with the client and start time, and is still selectable — it is never a bare id.
3. **Given** the user selects a project-named entry, **When** the list refreshes, **Then** only records from that work are shown.

---

### User Story 3 - My session survives the client reconnecting (Priority: P2)

A user works continuously on one project for an hour. Their client silently reconnects several times during that hour (a normal, invisible event). The user still sees **one** session for that hour's work, not one per reconnect.

**Why this priority**: Without this, Story 2 fragments: a single hour of work on one project appears as many separate entries, and the filter still does not answer the user's question. It is the difference between "grouped by project" and "grouped by the work I did".

**Independent Test**: Invoke a tool, force the client to reconnect, invoke another tool within the idle window, and confirm both records belong to the same session.

**Acceptance Scenarios**:

1. **Given** a client invokes a tool, reconnects, and invokes another tool within the idle window, **When** the user views the session filter, **Then** one session covers both records.
2. **Given** a client stops working for longer than the idle window and then resumes, **When** the user views the session filter, **Then** two separate sessions are shown.
3. **Given** two different clients are working in the same project at the same time, **When** the user views the session filter, **Then** each client has its own session.

---

### User Story 4 - Ask the same question from the command line (Priority: P3)

An operator or agent can list sessions and filter activity by session from the CLI, with the same meaning as the Web UI.

**Why this priority**: Consistency across surfaces, and it makes the feature scriptable. Lower priority because the Web UI already answers the reported need.

**Independent Test**: List sessions from the CLI and filter activity by one of them; confirm the result matches the Web UI for the same session.

**Acceptance Scenarios**:

1. **Given** recorded work sessions, **When** the operator lists sessions from the CLI, **Then** each is shown with its client, project, and time range.
2. **Given** a session identifier, **When** the operator filters activity by it, **Then** only that session's records are returned.

---

### Edge Cases

- **Client discloses no project** (measured: one of the four tested clients does not). The session must still be created, named, and selectable — degraded, never broken.
- **No authenticated principal** (no agent token in use). Grouping must still work, falling back to what is known.
- **Two conversations, same client, same project, at the same time.** These collapse into one session. A known and accepted limitation of the chosen approach; it must be documented, not a silent surprise.
- **A long pause in the middle of one conversation.** Exceeding the idle window splits one conversation into two sessions. Accepted; the window is configurable.
- **A client reports many projects at once** (a multi-root workspace). The system must behave deterministically rather than picking arbitrarily.
- **Historical records** written before this feature cannot be attributed to a work session. They must remain viewable and must not break the UI.
- **A client goes away without saying goodbye** (crash, kill, network drop). The session must still be closed out by inactivity, not left open forever.
- **The project name looks like sensitive data.** Project names come from local filesystem paths and must never be transmitted off the machine.

## Requirements *(mandatory)*

### Functional Requirements

**Connections (transport tier)**

- **FR-001**: The system MUST NOT create a durable session record for a client connection that performs only a handshake and never performs any activity (no tool call, no tool retrieval).
- **FR-002**: The system MUST create the record on first activity, so that a client which connects, waits, and then works is recorded correctly.
- **FR-003**: The system MUST continue to satisfy the MCP protocol's own connection requirements unchanged; suppressing a *record* MUST NOT change what is sent on the wire.
- **FR-004**: The system MUST retain enough sessions to cover a normal working period. The retention limit MUST be re-evaluated once handshake-only noise is removed, and MUST be configurable.

**Work sessions (user tier)**

- **FR-005**: The system MUST attribute each unit of recorded activity to a work session.
- **FR-006**: A work session MUST be identified by the combination of: the authenticated principal (when present), the client's identity (name and version), and the project the client is working in (when the client discloses it).
- **FR-007**: A work session MUST continue across client reconnections that occur within a configurable idle window. Activity separated by more than the idle window MUST begin a new work session.
- **FR-008**: The idle window MUST default to a value reflecting a natural pause in human work (30 minutes) and MUST be configurable.
- **FR-009**: The system MUST obtain the project from the client using the mechanism the MCP protocol provides for that purpose, and MUST do so at most once per connection.
- **FR-010**: The system MUST degrade gracefully when the client discloses no project: the work session is still formed from the remaining attributes.
- **FR-011**: The system MUST accept an optional client-supplied conversation identifier and, when present, prefer it over the derived identity. No client offers one today; this exists so that when one does, the feature becomes exact rather than heuristic.
- **FR-012**: The work-session attribution MUST be written onto each activity record at the moment the record is created, so that it remains correct after the connection and its transport record are gone.
- **FR-013**: When two clients are working in the same project at the same time, each MUST have its own work session.
- **FR-014**: When a client reports multiple projects, the system MUST choose deterministically (the first reported root), so the same workspace always yields the same session.

**Surfaces**

- **FR-015**: The Activity Log session filter MUST present work sessions, labelled with the client and the project (falling back to client and time when no project is known). It MUST NEVER present a bare identifier as the only label.
- **FR-016**: The Sessions page MUST list work sessions, showing client, project, time range, and activity volume.
- **FR-017**: Selecting a work session MUST filter activity to exactly that session's records.
- **FR-018**: The CLI MUST expose the same work sessions, with the same meaning as the Web UI.
- **FR-019**: Activity recorded before this feature MUST remain viewable; it is not attributed to a work session and MUST NOT be presented as if it were.

**Privacy & durability**

- **FR-020**: Project names derive from local filesystem paths. The system MUST store and display only what is needed to identify the project to its owner, and MUST NOT transmit project names or paths in telemetry.
- **FR-021**: The work-session concept MUST NOT depend on the MCP handshake or the transport's session identifier continuing to exist, because a forthcoming protocol revision removes both.

### Key Entities

- **Connection** — one client transport attachment. Ephemeral; exists only while the client is attached. Recorded durably only if it produced activity.
- **Work Session** — a period of one client's continuous work, in one project, under one principal. Spans connections. This is what the user means by "session" and what every surface shows.
- **Activity Record** — one unit of recorded work (a tool call or retrieval). Carries its work-session attribution permanently, written at creation.
- **Project** — the workspace a client is working in, as disclosed by the client. Optional; absent for some clients.
- **Principal** — the authenticated identity making the request (agent token or API key), where one exists.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: On the reporting user's usage pattern — where ~99 of 100 records were handshake-only noise — the number of sessions shown over a day drops by at least 95%, and every session shown corresponds to work the user actually did.
- **SC-002**: A user who worked on one project for an hour, during which their client reconnected several times, sees **one** session for that hour, not one per reconnect.
- **SC-003**: A user who worked on two projects can select either one and see only the activity from that project.
- **SC-004**: No entry in the session filter is ever labelled with only an opaque identifier — every entry names a client, and names a project when the client disclosed one.
- **SC-005**: A session worked in yesterday is still correctly named today; session labels do not decay as newer connections arrive.
- **SC-006**: For a client that discloses no project, sessions are still created, named, listed, and selectable.
- **SC-007**: No project name or filesystem path leaves the machine.

## Assumptions

1. **Idle window default of 30 minutes.** A natural break in human work: long enough to survive a pause within a task, short enough that yesterday's work does not merge into today's. Configurable.
2. **The first reported project wins** when a client reports several. Deterministic, and matches the common case (a client rooted at one workspace).
3. **Same client + same project + same principal, concurrently, collapses to one work session.** Accepted: no client exposes a conversation identifier today, so the two are genuinely indistinguishable to us. FR-011 is the escape hatch for when one does.
4. **A record is created on first activity, not on connect.** Chosen over "create then reap" because reaping leaves a window in which the noise is visible and still consumes the retention budget.
5. **"Activity" means a tool call or tool retrieval** — the things a user would recognise as work. A handshake, capability exchange, or keepalive is not activity.
6. **Historical activity is not backfilled.** The information needed to attribute it no longer exists. Those records remain viewable and unattributed.

## Dependencies

- The activity record must be able to carry attribution written at creation time. Established by #839, which persists the client name on the record for exactly this reason: an attribution looked up later decays once the referenced record is evicted.
- The client must disclose its project through the MCP protocol's own mechanism. Measured across the clients available for testing: 3 of 4 disclose it; 1 does not.
- A forthcoming MCP revision removes the handshake and the transport session identifier, and changes the mechanism used to obtain the project. FR-021 exists so this feature outlives that change; the project-fetch must therefore sit behind a single seam.

## Out of Scope

- Backfilling historical activity records.
- Distinguishing two concurrent conversations from the same client in the same project (see Assumption 3).
- Any change to what MCPProxy sends on the wire to clients or upstream servers.
- Implementing the forthcoming stateless protocol revision. This feature must merely survive it.
- Server-edition OAuth user identity as the principal (see Integrity Review, item 6).
- The pre-existing server-edition bucket collision (see Integrity Review, item 7) — a separate defect, not this feature's to fix, but this feature must not make it worse.

## Integrity Review *(cross-model review against the existing codebase, 2026-07-12)*

A cross-model review of this spec against the real code found nine collisions. They are recorded here because several **correct the spec**, and the plan must honour them.

**Corrections to the spec (the spec was wrong):**

1. **Roots MUST NOT be fetched during the initialize handshake.** `afterInitialize` fires *before* the initialize response is sent (`mcp-go` `request_handler.go:133` → `createResponse` at `:134`). Requesting roots there would deadlock: the client waits for the initialize result while we wait for its roots answer. **FR-009 is amended**: the project MUST be obtained *after* the handshake completes, asynchronously, with a timeout, and MUST never block or delay a client request. (Empirically, clients answer a roots request immediately after `notifications/initialized` — that is the safe trigger.)

2. **Work-session attribution MUST be a first-class field, not metadata.** Activity filtering compares `record.SessionID` (`internal/storage/activity_models.go:201`); REST, CSV export, CLI and frontend all key on `session_id`. Attribution buried in a metadata map would be stamped but **unfilterable**, silently failing FR-017. **FR-012 is amended** to require a first-class, filterable field.

3. **Only *durable persistence* may be deferred — never the in-memory session.** The in-memory session map (`session_store.go:53`) is what resolves the client name for every activity record (`SetSessionClientResolver`, added in #839). Deferring it would make the first activity of every session lose its client identity. **FR-001/FR-002 are clarified**: suppress the *storage write*, not the in-memory registration.

4. **Deferred creation races the existing stats write.** `UpdateSessionStats` requires the row to already exist and errors if it does not (`storage/manager.go:1443,1465`). Creating the record on first activity means the first call's stats would be dropped with a warning. The write path must ensure the record exists before stats are applied.

5. **Disconnect must stay quiet.** `RemoveSession` unconditionally closes the session in storage (`session_store.go:106`), which returns "session not found" for anything never persisted (`storage/manager.go:1298`). Without care, every idle disconnect would log a warning — trading record noise for log noise.

**Constraints the plan must respect (the spec was right, but incomplete):**

6. **The principal is only partly available.** MCP auth resolves agent tokens and the API key (`server/server.go:210-286`), but server-edition OAuth identity is a *separate* middleware and `/mcp` does not pass through it. FR-006's principal is therefore implementable for agent-token/API-key today, and **not** for server-edition OAuth users. Out of scope; the derivation must degrade without it.

7. **Pre-existing bucket collision (not caused by this feature).** Core MCP sessions use BBolt bucket `"sessions"` (`storage/models.go:30`); server-edition *user login* sessions use a bucket of the same name (`serveredition/users/store.go:18`) on the same DB (`serveredition_wire.go`). The admin handler unmarshals every value in that bucket as an auth session. Work-session records MUST NOT be added to that bucket. **This is an existing defect worth its own fix.**

8. **"Activity" and `tool_call_count` are not the same thing.** `retrieve_tools` emits an activity record but does *not* increment `tool_call_count` (only `UpdateSessionStats` does). A retrieval-only session would correctly exist under FR-001 while displaying zero tool calls. The surfaces must not present that as an empty session.

9. **Session retention is hardcoded.** The 100-record cap lives at `storage/manager.go:1261` with no configuration path, and the activity retention default is 7 days (not the 90 assumed elsewhere in comments). FR-004 is therefore a real change, not a config tweak.

## Commit Message Conventions *(mandatory)*

### Issue References
- ✅ **Use**: `Related #[issue-number]`
- ❌ **Do NOT use**: `Fixes #`, `Closes #`, `Resolves #`

### Co-Authorship
- ❌ **Do NOT include** AI attribution or generation notices.
