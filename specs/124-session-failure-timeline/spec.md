# Feature Specification: Session Failure Timeline

**Feature Branch**: `124-session-failure-timeline`
**Created**: 2026-11-13
**Status**: Draft
**Input**: Maintainer brainstorm, 2026-11-12/13. Goal in the maintainer's words: give people a reliable MCP layer with protection and logging, so that MCPProxy is used in software factories (fleets of unattended coding agents). First of five agreed directions (audit trail, child tokens, call-time guardrails, headless reliability, factory recipe).

**Related**: Spec 082 (work sessions), Spec 108 (scope attribution: token, client, profile on activity and session rows), Spec 113-c (call-error taxonomy: `error_class`, `fault_domain`), #1175 / #1176 (activity and tool-call storage bounds), `docs/features/activity-log.md`, `docs/cli/activity-commands.md`.

All code references are to `origin/main` at `3519e664c`.

## Context and Motivation

An operator starts several coding agents in the evening and reads the result the next morning. When a run went wrong they need three facts: the exact time a call failed, the reason, and what the same agent did before it.

Telemetry for the 14 days to 2026-11-12 (664 active installs) shows this use is already here: 101 installs run in containers, 106 installs made 462,000 tool calls through `POST /api/v1/tools/call`, and the busiest installs make about 25,000 calls a day. About 1.13 million upstream calls were made in total, 31,000 of them destructive.

MCPProxy records each call well: millisecond timestamp, error class, request id, token, client, profile, arguments and response (`storage.ActivityRecord`, `internal/storage/activity_models.go:200-290`). What it cannot do reliably is put those calls back together as "one agent's run" and keep them for a week:

1. **Calls without a connection id have no session.** The work-session id on an activity row is looked up through the MCP connection id (`resolveWorkSession(sessionID)` in `internal/runtime/activity_service.go:698,791,1006,1106`, wired to `SessionStore.WorkSessionID` at `internal/server/mcp.go:609`; the audit line takes the same route at `internal/server/audit_funnel.go:153`). `POST /api/v1/tools/call` creates no MCP session (`internal/httpapi/server.go:6364`, `internal/server/server.go:4648`), so those rows carry neither id. A request on the 2026-07-28 MCP revision is served by an ephemeral session whose id is empty, and the stdio transport reports the constant `stdio` (`internal/server/profile_resolver.go:15-27`). Those requests lose their work session too, and on purpose: `markSessionWorked` returns early for every modern request (`internal/server/workspace.go:187-199`, Spec 058 US3), and it is the only path to `ResolveWorkSession`. This is read from the code, not yet shown by a test. Modern-revision clients are the future majority, so this is the main case and not an edge case.
2. **The session list forgets within hours.** Session records are capped at 100 (`sessionRetentionLimit`, `internal/storage/manager.go:1607`), one per connection, and real clients reconnect every few minutes.
3. **The activity log can drop the middle of a run.** It prunes by age (90 days), count (100,000 records) and size (256 MB), oldest record first, with no regard for which session a record belongs to (`PruneOldActivities`, `PruneExcessActivities`, `PruneActivitiesToSize`, `internal/storage/activity.go:419-528`). At 25,000 calls a day the count cap is reached in four days.
4. **A session says nothing about failure.** `SessionRecord` holds start, end, call count, token and profile (`internal/storage/manager.go:1542-1580`). Nothing tells the operator which session failed.
5. **The cause is often not in the activity log.** An upstream process exiting or an OAuth refresh failing is written to `main.log` and the per-server log. No activity row is emitted for either, so the timeline shows the failed call and not the event that caused it.

## The Decision

The unit of the audit trail is the **work session** (Spec 082): one principal, one client, one project, continuous until 30 minutes of idle. It is not the MCP connection id, which the protocol is removing. For a factory this means one agent token per agent gives one timeline per agent.

Every tool call is assigned to a work session at the time it is recorded, whatever surface it arrived on. Each work session has a durable summary that carries its failure facts. Sessions are retained by age (7 days by default) and pruned as whole units. Upstream trouble is recorded as activity rows and merged into the timeline of every session that was using that server. The Web UI and the CLI both open a session at its first failure with the calls that came before it.

Under size pressure the oldest argument and response bodies are removed before any row (maintainer decision, 2026-11-14). What is stored when a call is recorded is unchanged; what is kept under pressure changes. Time, reason and order survive the week; old payloads may not.

The feature lives in the personal edition and is read through the Web UI and the CLI. The server edition inherits it unchanged.

## Scope Boundary

Delivered as three PRs, each merged before the next starts:

- **PR-1 (record and keep)**: work-session assignment for every call, the durable session summary with failure facts, age-based session retention and session-aware pruning, upstream event rows, the retention warning in `doctor`, and the REST read doors.
- **PR-2 (CLI)**: the session listing and the session timeline commands.
- **PR-3 (Web UI)**: the Sessions view changes and the timeline view, with the Playwright sweep required by `docs/development/web-ui-verification.md`.

Out of scope:

- Push sinks of any kind (webhook, syslog, Loki client, SIEM).
- Kubernetes and Loki integration. This is planned for the server edition as a separate spec.
- A tamper-evident ledger. The activity log stays a debugging record, as `docs/features/activity-log.md` states.
- Grouping several agents into one "factory run".
- Child tokens, per-token budgets, argument constraints and call-time approval. These are the next two directions and get their own specs.
- Any change to which arguments and response bodies are stored when a call is recorded, or to their per-record size caps. (What is kept under size pressure does change: see FR-032.)
- macOS tray parity.
- New telemetry fields.

## Definitions

- **Work session**: as in Spec 082. Identified by a work-session id. Made of zero or more MCP connections plus any calls that arrived without a connection.
- **Principal**: the credential the call was authenticated with: an agent token (by name), a client credential, the admin API key, or the local socket.
- **Surface**: where a call entered: `mcp`, or for REST calls the value the caller already sends in the surface header (`cli`, `webui`, `tray`), or `rest` when none is sent.
- **Run name**: an optional label a REST caller sends so that its calls group as one work session and can be found by name.
- **Attempt**: one call as the operator counts it. All activity rows that share one request id are one attempt: the `tool_call` row, its `call_tool_*` mirror (`InternalCallToolPrefix`, `internal/storage/activity_models.go:112-119`) and any `policy_decision` rows written for the same call, before or after dispatch. A call to one of MCPProxy's own tools (`retrieve_tools`, `describe_tool`, `code_execution`, the admin tools) is an attempt with its single `internal_tool_call` row. A row with no request id is its own attempt.
- **Failure**: an attempt whose final outcome is not success: the call returned an error to the caller or was refused. A `policy_decision` row that only warns, or that records a decision which did not change the outcome, does not make the attempt a failure. A **policy block** is a failure refused by MCPProxy itself (error class `proxy_policy`); it is counted apart from other failures.
- **Upstream event**: something that happened to an upstream server and was not a call: the connection was lost, the process exited, an OAuth token refresh failed, the server needs a new login, the connection came back.
- **Session summary**: the durable per-work-session record defined in Key Entities.
- **Retention window**: the age up to which sessions are kept, 7 days by default.

## User Scenarios & Testing *(mandatory)*

### User Story 1: The operator finds last night's failure and what led to it (Priority: P1)

An operator ran four agents overnight, each with its own agent token. In the morning they open Activity → Sessions, switch on "Failed only", and see one row per agent run that had a failure, newest failure first, with the failure count, the time of the last failure and a short reason. They open the first row. The timeline opens at the first failure, with the 20 rows before it. One row above the failed call is an amber row: the GitHub server's OAuth token refresh failed 600 ms earlier. They step to the next failure with one click.

**Why this priority**: this is the question the feature exists to answer. Nothing else matters if it cannot be answered.

**Independent Test**: start the core with a fixture upstream; make 30 successful calls with one agent token, make the fixture fail its OAuth refresh, make two more calls that fail, and stop. Query the session list filtered to failed sessions and the timeline of the one session returned.

**Acceptance Scenarios**:

1. **Given** a work session with 30 successful calls followed by 2 failed calls, **When** the operator lists failed sessions, **Then** the session appears with failure count 2, the first and last failure times to the millisecond, and the error class and reason of the last failure.
2. **Given** that session, **When** the operator opens its timeline with default options, **Then** the view is positioned at the first failure, shows the 20 rows before it in time order, and marks the failed row.
3. **Given** an upstream event recorded for a server the session called, with a timestamp inside the session's span, **When** the timeline is read, **Then** the event appears in time order among the calls and is marked as an upstream event, not as a call by the agent.
4. **Given** a session with 9 failures, **When** the operator moves to the next or previous failure, **Then** the view is positioned at that failure with the same amount of context, and shows "failure N of 9".
5. **Given** a failed row, **When** it is shown, **Then** it carries the error class, the error code when one exists, the error message, the request id and the duration, and its arguments and response can be opened.
6. **Given** a session with no failures, **When** the operator opens it, **Then** the timeline opens at its most recent row.

### User Story 2: A script that calls tools over REST gets a timeline (Priority: P1)

A nightly script calls `POST /api/v1/tools/call` 2,900 times with an agent token. Today none of those calls belong to a session. After this feature they form one work session per run, listed next to the MCP agents. The script may send a run name, and the operator can then find the run by that name.

**Why this priority**: 106 installs make their calls this way, and for them the audit trail does not exist today.

**Independent Test**: make 50 REST tool calls with one agent token and no run name, wait past the idle window, make 50 more with a run name. Read the session list.

**Acceptance Scenarios**:

1. **Given** REST tool calls from one principal on one surface with gaps shorter than the idle window, **When** they are recorded, **Then** all carry the same work-session id and one session summary exists for them.
2. **Given** a gap longer than the idle window, **When** the next call arrives, **Then** it starts a new work session.
3. **Given** two REST callers using different agent tokens, **When** both send the same run name, **Then** they are in different work sessions. A run name never joins calls across principals.
4. **Given** a run name, **When** the session is listed, **Then** the name is shown and the list can be filtered by it.
5. **Given** an MCP call that arrives without a connection id, **When** it is recorded, **Then** it is assigned to a work session derived from its principal, client name and project when known.
6. **Given** tool calls made from the Web UI, the tray or the CLI, **When** they are recorded, **Then** they form work sessions under their own surface and do not mix with an agent's session.

### User Story 3: A week later the session is still whole (Priority: P1)

Six days after a run the operator is asked why an agent force-pushed a branch. They find the session by token and date and read it from its first call.

**Why this priority**: the maintainer set one week as the requirement. A timeline that is gone, or that has lost its beginning, answers nothing.

**Independent Test**: with a small record cap configured, write three sessions of known size and age so that the cap forces pruning; verify which rows remain. Separately, advance the clock past the retention window and run cleanup.

**Acceptance Scenarios**:

1. **Given** 500 work sessions that all ended within the retention window, **When** the list is read, **Then** all 500 are present. No count cap removes a session inside the window.
2. **Given** a session whose last activity is older than the retention window, **When** cleanup runs, **Then** its summary is removed. Its activity rows follow the activity log's own age cap as today.
3. **Given** the size cap is exceeded, **When** pruning runs, **Then** it first removes the bodies of the oldest rows and keeps the rows. **Given** that is not enough or the record cap is exceeded, **Then** it removes rows that belong to no session and whole sessions older than the retention window first, oldest first, and then whole sessions inside the window, oldest first. It does not remove part of one session while an older complete session remains.
4. **Given** pruning had to remove a session that was inside the retention window, **When** `mcpproxy doctor` or the diagnostics API is read, **Then** it reports that the configured caps hold less than the retention window, states how far back complete history reaches, and names the settings to raise.
5. **Given** a single session that is all that is left to prune and alone exceeds the caps, **When** pruning runs, **Then** that session is trimmed from its oldest end, its summary is marked as having a truncated start with the time of its earliest remaining row, and its failure facts stay those of the whole session.
6. **Given** the activity log within its caps, **When** the core restarts, **Then** every session summary is unchanged and agrees with the stored rows.

### User Story 4: The same answers from the terminal (Priority: P2)

On a machine reached over SSH the operator lists failed sessions and prints one session around its failures. Output is a table by default and JSON with `-o json`, like the other activity commands.

**Why this priority**: factories run on headless machines. The Web UI is not always reachable; the CLI is.

**Independent Test**: against a core seeded as in User Story 1, run the two commands and compare the JSON with the REST responses.

**Acceptance Scenarios**:

1. **Given** sessions with and without failures, **When** the operator lists sessions with the failed-only option, **Then** only failed sessions are printed, newest failure first, with token, client, project, start, duration, calls, failures, last failure time and reason.
2. **Given** a session id, **When** the operator prints its timeline with the failures option and a context of N, **Then** each failure is printed with the N rows before it, upstream events included and marked, and overlapping contexts are printed once.
3. **Given** `-o json`, **When** either command runs, **Then** the output carries the same fields as the REST response.
4. **Given** an id that matches no session, **When** the timeline command runs, **Then** it exits non-zero with a message that says so and suggests the listing command.

### User Story 5: A scoped agent sees only its own trail (Priority: P2)

An agent token that may reach two servers reads the session list. It sees its own sessions only, and in a timeline it sees upstream events only for servers it may reach.

**Why this priority**: the read doors are new, and every new read door is a place where the scope boundary of Spec 118 can leak.

**Independent Test**: with two agent tokens and three servers, read every new door with each token and with the admin key.

**Acceptance Scenarios**:

1. **Given** sessions made with tokens A and B, **When** token A lists sessions, **Then** only A's sessions are returned and every count reflects only them.
2. **Given** token A asks for a session made with token B, **When** the door answers, **Then** the answer is the same not-found response as for an id that does not exist.
3. **Given** an upstream event on a server token A may not reach, **When** token A reads a timeline, **Then** the event is absent.
4. **Given** a profile-pinned token, **When** it reads these doors, **Then** its reach is the grant narrowed by the pin, as on every other read door.

### Edge Cases

- **Reconnect storm.** An upstream that fails to connect every few seconds would write thousands of event rows a day. Repeats of the same event kind on the same server within a short window are stored as one row with a count and the time of the first and last occurrence.
- **An event with no session using the server.** It is stored and appears in the System view of the activity log. It appears in no session timeline.
- **A session that used a server only after the event.** The event is shown only if its timestamp is inside the session's span. An event before the session started is not part of it.
- **Calls inside `code_execution`.** They carry the parent call's work session. A failed inner call counts as a failure of the session once; the parent's own failure, if it also failed, is a second row and a second failure.
- **Unauthenticated use.** With no principal the work session is derived from client and project as Spec 082 already does. For REST calls with no principal and no run name, calls group by surface.
- **A run name that is too long or carries control characters.** The call is still served. The name is not recorded and the call is grouped as if none was sent.
- **Clock change.** A timeline is ordered by each row's recorded wall-clock time, which is how the activity log is keyed (`activityKey`, `internal/storage/activity.go:17-21`). After the system clock steps backwards, new rows can sort before older ones. This is accepted; no separate sequence is added.
- **Rows written before the upgrade.** Rows that carry a work-session id are summarised once on first start. Rows that carry none stay in the activity log and belong to no session.
- **A session with only blocked calls.** It is still a session; its policy-block count equals its attempt count.
- **A legacy connection that resumes after its session ended.** A connection keeps the work-session id it was first given (`EnsurePersisted`, `internal/server/session_store.go:309-340`). When a row arrives for a session already marked ended, the session is active again and its end is cleared.
- **Crash.** A session with no recorded end at start is given its last activity time as its end.
- **Token regenerated or recreated under the same name.** The new secret has a new prefix and starts a new work session (FR-009). Sessions made with the old secret are not the new token's own and read as not found for it, as activity rows already do.
- **Server removed from the configuration.** Its upstream event rows stay in the timeline of past sessions until those sessions leave the retention window.

## Requirements *(mandatory)*

### Functional Requirements

**Assignment**

- **FR-001**: Every `tool_call`, `internal_tool_call`, `policy_decision` and `prompt_get` activity row recorded after the upgrade MUST carry a work-session id, on every surface, including REST tool calls and MCP calls on the 2026-07-28 revision.
- **FR-002**: When a call has no MCP connection, the work session MUST be derived from the principal, the surface or client name when the request carries one, the project when known, and the run name when sent, with the same idle window as Spec 082. Project is usually unknown for these calls, because workspace roots are fetched from a connection.
- **FR-003**: A caller MAY send a run name in a request header, on REST tool calls and on MCP over HTTP. It MUST be at most 128 printable characters. It MUST be treated as a label and never as a credential: it never changes what the caller may do, and it never joins calls of different principals.
- **FR-007**: Two agents that share one credential, send no run name and report the same client name cannot be told apart, and their calls form one work session. The documentation MUST say so and MUST recommend one agent token per agent, or a run name per agent, for unattended use. The session list MUST show when a session's principal is the shared admin key.
- **FR-004**: An invalid run name MUST NOT fail the call. It is ignored and the fact is logged once per principal per process run.
- **FR-008**: The run name MUST be combined with the principal when the session is derived, so that it can never override or replace the principal. It MUST NOT be forwarded to any upstream server. A principal may open at most 1,000 named sessions a day; beyond that, calls are grouped as if no name was sent. The documentation MUST say the name is stored and shown in clear and is not a place for secrets.
- **FR-005**: Calls made inside `code_execution` MUST carry the work session of the call that started them.
- **FR-006**: Work-session assignment for calls that already had one (MCP connections with a stable id) MUST NOT change.
- **FR-009**: A work session MUST belong to exactly one credential. The principal used to derive it MUST include the credential's generation (the token prefix), so that a token regenerated or recreated under the same name starts a new work session even inside the idle window. The check is made per request, not per connection: one MCP connection can carry requests from different tokens (`internal/server/mcp.go:7043-7047`), and today a connection keeps the work-session id it was first given whoever presents on it (`internal/server/session_store.go:324-332`). A request whose credential differs from the one its connection's work session belongs to MUST be assigned to that credential's own work session. These are the deliberate changes to FR-006; today the principal is the token name alone (`internal/server/workspace.go:155-167`).
- **FR-019**: Every recorded attempt MUST refresh the idle clock of its work session. Today the clock moves only when a connection is first resolved (`EnsurePersisted`, `WorkSessionTracker.Resolve`), so a connection that stays busy for longer than the idle window can reconnect into a different work session.

**Session summary**

- **FR-010**: The system MUST keep one durable summary per work session with the fields listed in Key Entities.
- **FR-011**: The summary's failure facts MUST be: failure count, time of first failure, time of last failure, and the error class, error code, server, tool and a short reason of the last failure.
- **FR-012**: The short reason MUST be at most 200 characters and MUST come from fields the activity row already exposes to the same caller. It MUST NOT contain argument or response bodies.
- **FR-013**: The summary MUST survive a restart. Its counts and failure facts are lifetime facts of the session and are the record of truth once rows have been trimmed or removed. A summary that is missing at start is rebuilt from the rows that remain and marked as rebuilt; a rebuild MUST NOT lower the counts of an existing summary.
- **FR-017**: The summary MUST store the credential identity needed to decide ownership the way activity rows do: token name and token prefix (`ActivityIdentityOwner`, `internal/storage/activity_models.go:310-335`), and the owning user in the server edition. The prefix is never returned by any door.
- **FR-014**: On first start after the upgrade the system MUST build summaries for existing rows that carry a work-session id. The pass MUST be bounded by the activity log's own caps and MUST NOT delay the core from serving requests.
- **FR-015**: A session's end MUST be recorded once its idle window has closed, at the latest by the next cleanup pass, and when the core stops, so that duration is known for finished sessions. A later row for the same work-session id reopens it.
- **FR-016**: The summary MUST count calls blocked by policy separately from other failures, so that the list can show and filter them apart.

**Upstream events**

- **FR-020**: The system MUST record an activity row when an upstream server's connection is lost, its process exits, its OAuth token refresh fails, it enters a state that needs a new login, and when its connection is restored.
- **FR-021**: An upstream event row MUST carry only: the server name, the event kind, the time, the diagnostic error code when one exists (the `MCPX_*` catalogue), and the fixed reason text that belongs to that code or kind. It MAY also carry, for access control only and never returned, the owning user of the credential the event is about. It MUST NOT carry free text from the upstream, the identity provider or the operating system: no URLs, account names, file paths, response fragments, token values, headers or environment values. The raw error stays in the server log, where it is today.
- **FR-025**: In the server edition, an event about one user's upstream credential MUST be visible only to that user and to administrators. Such events MUST be collapsed (FR-022) per owner, never across owners, so that no row's count or times mix two users.
- **FR-022**: Repeats of the same event kind for the same server within one minute MUST be stored as one row with a count and the first and last times. The number of event rows one server can add MUST be bounded per hour; at the bound, further events are merged into the server's latest row of that kind and nothing is dropped from the counts.
- **FR-023**: A session timeline MUST include an upstream event when the session called that server and the event's time is within the session's span: from its first row to its recorded end, or to the present for a session that is still active. For a scoped caller the span and the status are those derived under FR-048, so that an event is never shown because of activity outside the caller's reach.
- **FR-024**: Upstream event rows MUST appear in the existing System view of the activity log and be filterable by type.

**Retention**

- **FR-030**: Session summaries MUST be retained by age. A session whose last activity is within the retention window MUST NOT be removed by a count cap. The fixed cap of 100 MUST no longer apply to work-session summaries.
- **FR-031**: The retention window MUST default to 7 days and be configurable in days. It is read at start, like the other retention settings.
- **FR-032**: When the activity log's size cap forces pruning, the system MUST first remove the stored argument and response bodies of the oldest rows, keeping every other field of the row, and only then remove rows. A row whose bodies were removed MUST say so when read.
- **FR-037**: When rows must be removed, the order MUST be: rows with no work session and rows of sessions older than the window, oldest first; then whole sessions inside the window, oldest last-activity first.
- **FR-033**: A session inside the window MUST be removed whole or not at all, with one exception: when a single session is all that is left to prune and it alone exceeds the caps, it is trimmed from its oldest end and its summary records the truncated start.
- **FR-038**: A cleanup pass MUST NOT read the whole activity log more than once, and MUST do its writes in batches of at most 1,000 rows per transaction, as the existing scans do (`streamScanPerTx`), so that a tool call never waits for a whole pass.
- **FR-018**: The one-time summary build (FR-014) MUST be safe to run while calls are being recorded: a row written during the build is counted exactly once.
- **FR-034**: The existing caps (age, record count, size) MUST keep bounding the activity log's stored bytes, as measured today (`PruneActivitiesToSize`, `internal/storage/activity.go:522-528`). Session summaries, upstream event rows and any index this feature adds MUST be counted against the same byte budget. The documented exception stays: pruning frees pages for reuse and does not shrink the file on disk.
- **FR-039**: The age cap of the activity log applies as today and is independent of the session retention window. If the age cap is set shorter than the window, the shortfall is reported under FR-035.
- **FR-035**: When pruning removes or trims a session inside the window, the system MUST record the shortfall. `mcpproxy doctor` and `GET /api/v1/diagnostics` MUST report it with the age of the oldest complete session and the settings to raise. The report clears once history again covers the window.
- **FR-036**: When a summary is removed, its remaining activity rows MUST stay readable through the existing activity filters until the activity log prunes them.

**Read doors (REST)**

- **FR-040**: The API MUST list work sessions with: id, principal attribution (token name, client id, client name), surface, project name, run name, profile, start, end or last activity, status (active or ended), call count, failure facts, connection count and truncated-start marker.
- **FR-041**: The list MUST support: failed only, with policy blocks included or excluded; filter by token, client, profile, run name, error class of the last failure and time range; sort by last failure or by last activity; pagination.
- **FR-042**: The API MUST return one session's timeline: activity rows and upstream events in time order, each marked with its kind (call, internal call, policy decision, prompt, upstream event, session start, session end).
- **FR-043**: The timeline MUST be addressable by position: at the first failure, at the Nth failure, at a given row, or at the end, with a number of rows before and after. Defaults: first failure, 20 before, 20 after. It MUST return the index of the current failure and the total.
- **FR-044**: Timeline rows MUST omit argument and response bodies by default and include them on request. This is a deliberate new default for the timeline; the activity list keeps its own default of including them. A timeline page MUST return at most 500 rows.
- **FR-045**: The existing `/api/v1/sessions` doors MUST keep their paths, their response shape and their admin-only read rule (`requireAdminRead`, `internal/httpapi/server.go:6948`). The `session` and `work_session_id` activity filters MUST keep working. The work-session doors are new, separate endpoints.
- **FR-046**: Authorization of the new doors:
  - Admin API key and local socket: every session, every row, every upstream event.
  - Agent token: only sessions it owns (FR-017), and within them only what its current reach allows, where reach is the grant narrowed by the pin (Spec 118). Rows, upstream events, the attempt and failure counts, the first and last failure, the reason, the failure index and the previous/next navigation MUST all be computed over the rows in reach. A session with no row in reach answers as not found.
  - Server-edition user: their own sessions, with the same reach rule.
  - A session the caller may not see MUST answer exactly as an id that does not exist. A caller for whom a door cannot be scoped MUST be refused.
  - Filters by token, client or profile from a scoped caller follow the Spec 108 disclosure rule: a record the caller did not make reads as unattributed.
- **FR-048**: For a scoped caller, every value a door returns or sorts and filters by MUST be derived only from the part of the session in the caller's reach: start, end, last activity, duration, status, attempt and failure counts, first and last failure, reason, and the synthetic start and end rows. A value that cannot be derived that way (connection count, latest profile, truncated-start marker) MUST be withheld. Activity on a server outside reach MUST NOT change anything the caller can observe.
- **FR-049**: To make FR-048 possible without reading rows outside the requested page, the summary MUST keep its counts, first and last activity and first and last failure per server. For an administrator the session's facts are the lifetime facts of FR-013. For a scoped caller they are the sum over the servers in reach.
- **FR-047**: The OpenAPI document and `docs/api/rest-api.md` MUST describe the new doors.

**CLI**

- **FR-050**: The CLI MUST list work sessions with the filters and sort of FR-041 and print the columns of User Story 4.
- **FR-051**: The CLI MUST print one session's timeline, with an option to show only the failures and N rows before each (default 20), and an option to include bodies.
- **FR-052**: Both commands MUST support `-o json|yaml` and `--help-json`.
- **FR-053**: `docs/cli/activity-commands.md` MUST document both commands with an example for the morning-after case.

**Web UI**

- **FR-060**: Activity → Sessions MUST show one row per work session with: agent token, client, project, started, duration, calls, failures, last failure, reason. It MUST offer a "Failed only" filter and sort by last failure.
- **FR-061**: A session whose start was truncated MUST say so in its row and at the top of its timeline.
- **FR-062**: Opening a session MUST show its timeline positioned per FR-043, with the failed row highlighted, upstream and session rows visually distinct from calls, timestamps to the millisecond, and previous-failure and next-failure controls with "failure N of M".
- **FR-063**: The timeline MUST be linkable: its address identifies the session and the position, so that it can be shared and reloaded.
- **FR-064**: The timeline MUST offer the equivalent CLI command for the current view.
- **FR-065**: The existing link from a session to the Tool calls view filtered by that session MUST remain.
- **FR-066**: `docs/web-ui/` and `docs/features/activity-log.md` MUST describe the view and the retention behaviour.

**Configuration**

- **FR-070**: One new setting, the session retention window in days, default 7. It MUST be wired at every point a configuration field needs (load, save, defaults, reference documentation, Web UI settings where the sibling retention settings appear).

### Key Entities

- **Work session summary**: id; principal attribution (token name, client id, client name); stored credential identity for ownership (token prefix, owning user; never returned); surface; project name; run name; latest profile and its source; start time; last activity time; end time when ended; attempt count; failure count; policy-block count; first failure time; last failure time; last failure's error class, error code, server, tool and short reason; connection count; truncated-start time when trimmed.
- **Upstream event**: an activity row of a new type with server, event kind (connection lost, process exited, OAuth refresh failed, login required, connection restored), time, error code, short reason, repeat count, first and last occurrence.
- **Timeline row**: a view over an activity row or an upstream event, or a synthetic row for session start and end, with its kind and its position in the session.
- **Retention shortfall**: the fact that complete history is shorter than the retention window, with the time it was last observed and the age of the oldest complete session.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Starting from the session list, an operator reaches the first failed call of a given night's session, with the 20 rows before it, in at most two actions in the Web UI and two commands in the CLI.
- **SC-002**: 100% of tool calls recorded after the upgrade belong to a work session, on every surface. Verified by a test that calls through MCP with a connection id, MCP without one, REST with each surface value, and `code_execution`.
- **SC-003**: With the default settings and a load of 10,000 attempts a day spread over 20 sessions a day, every session from the last 7 days is listed and complete.
- **SC-004**: When the caps cannot hold 7 days, the operator is told in `doctor` within one cleanup interval, and no session inside the window is ever missing rows from its middle.
- **SC-005**: For a failure caused by an upstream connection loss or an OAuth refresh failure, the upstream event is in the same timeline, placed between the session's last attempt before the event and the failed attempt.
- **SC-006**: The session list for 1,000 sessions and one timeline page of a 50,000-row session are each served without reading rows outside the page, shown by a benchmark whose cost does not grow with the size of the rest of the log.
- **SC-007**: Under a simulated reconnect storm of one failed connection attempt per second for 24 hours (injected clock), one server adds at most 2,880 event rows and `config.db` stays within the existing size cap.
- **SC-009**: At 25,000 attempts a day (about 50,000 stored rows a day) with the default caps, and rows that average at most 600 bytes once their bodies are removed, the time, result, error class and order of every call from the last 7 days remain readable.
- **SC-008**: An agent token can read no session, count or upstream event outside its reach through any new door, verified by the REST authorization table used for existing doors.

## Assumptions

Decisions made by the designer, not by the maintainer. Each can be changed in review.

- **Run name header**: `X-MCPProxy-Run`. The surface header the clients already send is reused as is.
- **Failures include policy blocks, counted apart.** A call blocked by quarantine or a profile is a failure of the session, shown with error class `proxy_policy`. "Failed only" includes them by default and one click excludes them, so that 74,000 quarantine blocks cannot bury a real failure.
- **The record cap default rises from 100,000 to 400,000.** The cap counts stored rows, not attempts. A call through `call_tool_*` stores two rows (the call and its mirror), so a week at the measured top rate of 25,000 attempts a day is about 350,000 rows, plus upstream events. The 256 MB size cap is unchanged and stays the bound on disk.
- **The run name is accepted on MCP over HTTP as well as REST**, since modern-revision MCP calls have no other way to tell two agents on one credential apart. Stdio clients cannot send it.
- **Upstream events are stored once per server, not once per session.** The timeline joins them by server and time. This keeps the row count bounded when many sessions share a server.
- **Session start and end are synthetic timeline rows** derived from the summary, not stored activity rows.
- **The Sessions view changes its unit** from one row per connection to one row per work session. Connections stay reachable from the session and through `/api/v1/sessions`.
- **Web UI, tray and CLI tool calls form their own sessions** by surface. They are not hidden.
- **The idle window stays 30 minutes** for calls without a connection, the same as Spec 082.
- **Upstream event repeat window is one minute, and one server may add at most 120 event rows an hour.**
- **No live push of the session list.** The view refreshes on the existing activity events and on demand.
- **The tool-call history store** (`server_<id>_tool_calls`, 1,000 per server) is unchanged and is not part of the timeline.
- **Project name** is the basename of the workspace root the client disclosed. It stays on the machine, as today.

## Open Questions for the Plan

- Which identity fields a 2026-07-28 MCP request carries (client name, project). The first task of the plan is a test that shows today's empty work session for such a request and lists what is available to derive one. Spec 058 US3 gated session persistence for these requests on purpose; the plan must keep that decision for connection records and add work-session assignment beside it.
- Where the failure facts are updated: in the same transaction as the activity row, or by the cleanup pass. FR-013 requires only that they agree after a restart.
- How session-aware pruning finds a session's rows within FR-038: activity keys are ordered by time, not by session, so this needs an index or a single ordered pass.
