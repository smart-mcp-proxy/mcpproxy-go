# Implementation Plan: Session Failure Timeline

**Branch**: `124-session-failure-timeline` | **Date**: 2026-11-15 | **Spec**: [spec.md](spec.md)

All code references are to this branch at `057daf0bc` (code identical to `origin/main` at `3519e664c`).

This plan follows the single-file style of `specs/123-oauth-token-encryption/plan.md`: research, data model and contracts are sections here, not separate files.

## Summary

Every recorded attempt gets a work-session id that is resolved from the request, not from the MCP connection. Storage writes the activity row, a per-session index entry and the session summary in one transaction. Cleanup becomes session-aware: bodies go first, then rows outside the window, then whole sessions. Upstream state changes and OAuth refresh failures become collapsed activity rows. Three new REST doors, two CLI commands and a reworked Sessions view read the result.

## Technical Context

- **Language**: Go 1.26; TypeScript 5.9 / Vue 3.5 for the Web UI.
- **Storage**: BBolt `config.db`. New buckets, no schema change to existing ones except one optional field on `ActivityRecord`.
- **Dependencies**: none new.
- **Testing**: `go test -race`, `scripts/test-api-e2e.sh`, vitest (`frontend/tests/unit/`), the Playwright sweep (`scripts/run-web-smoke.sh`).
- **Editions**: both. Server-edition specifics are limited to the owning user on summaries and on per-user credential events.
- **Performance goal**: the write path adds at most three extra key writes per row inside the existing transaction; list and timeline reads never scan the activity bucket.

## Research: answers to the spec's open questions

**Q1. What identity does a call without a connection carry?**

- REST `POST /api/v1/tools/call` (`internal/httpapi/server.go:6358`): `auth.AuthContextFromContext(ctx)` gives `Type`, `AgentName`, `TokenPrefix`, `UserID`, `ClientID`, `CredentialKind` (`internal/auth/context.go:107`). The surface comes from the `X-MCPProxy-Client` header, already parsed by `telemetry.ParseClientSurface` (`internal/httpapi/middleware.go:55`); `toolCallRequestSource` (`internal/httpapi/concurrency_shed.go:18`) only tells CLI from REST. No client name, no workspace root.
- MCP on the 2026-07-28 revision: the same auth context, plus client name and version from `requestClientInfo(ctx)` (`internal/server/mcp_activity_attribution.go:117`), which `activityAttribution` already uses as a fallback. No workspace root, because roots are fetched from a connection.
- Both get an empty work-session id today: `ActivityService.resolveWorkSession("")` returns `""` (`internal/runtime/activity_service.go:176`), and `markSessionWorked` returns early for a modern request (`internal/server/workspace.go:197`).

**Q2. Where are the failure facts updated?** In the same BBolt transaction as the activity row (D3). The summary can then never disagree with the rows after a crash, and the backfill needs no lock of its own.

**Q3. How does pruning find a session's rows?** Through a per-session index bucket keyed `<work-session id>\0<activity key>` (D3). There is no index on activities today; `ActivityFilter.Matches` filters `WorkSessionID` by full scan (`internal/storage/activity_models.go:597`).

**Other facts that shape the design**

- The work-session tracker is in memory only and ids restart with the process (`internal/runtime/worksession.go:113-214`). A session therefore never continues across a restart. This is kept.
- `EnsurePersisted` resolves once per connection and caches the id (`internal/server/session_store.go:338-340`), so the idle clock does not move while a connection stays open, and a connection shared by several tokens gives them one id.
- The principal is `agent:<name>` or `agent:<user>:<name>` (`internal/server/workspace.go:152-167`); it has no credential generation.
- Activity rows are written by one goroutine, the `ActivityService` loop, from bus events (`handleEvent`, `internal/runtime/activity_service.go:458`), except `RecordToolCallRejected` (`:537`), which writes on the caller's goroutine behind the same write barrier. The bus drops events for a lagging subscriber.
- The usage aggregate already counts one call once: `s.usage.Apply(record)` (`:744`) keys off `InternalCallToolPrefix`.
- Cleanup runs age, then count (down to 90%), then size, once at start and then every `activity_cleanup_interval_min` (`runRetentionLoop`, `:339-407`). Size is the sum of key and value lengths in the bucket.
- There is no bus event for a lost connection. The supervisor emits `server.connected`, `server.disconnected` and `server.state_changed`, and `Runtime.supervisorEventForwarder` (`internal/runtime/lifecycle.go:2938`) turns them into `servers.changed`. `oauth.refresh_failed` exists (`internal/runtime/events.go:24`, emitted from `internal/oauth/refresh_manager.go:805-838`). Nothing detects a stdio process exit as such; it surfaces as a disconnect with an `MCPX_STDIO_*` code. Fixed messages per code are `CatalogEntry.UserMessage` (`internal/diagnostics/catalog.go`).
- `/api/v1/sessions` refuses every non-admin (`requireAdminRead`, `internal/httpapi/tokens.go:154`). Scoped activity reads go through `applyActivityScope` (`internal/httpapi/activity.go:161`); the pin is already folded into `AllowedServers` before a handler runs (`internal/httpapi/pin_boundary.go:30`). Every GET route must be classified in `getRouteScopes` (`internal/httpapi/scope_route_table_guard_test.go`).

## Decisions (with alternatives rejected)

### D1. The work session is resolved per request, at the emit site

A new function in `internal/server` builds a `runtime.WorkSessionIdentity` from the request context on every recorded attempt and returns the work-session id. `activityAttribution` (`mcp_activity_attribution.go:56-107`) gains a `WorkSessionID` field, so the id travels on the bus event with the other attribution. `ActivityService` uses it when present and falls back to `resolveWorkSession(sessionID)` for emitters that carry no attribution.

Identity, in order of the tracker key:

| Field | Legacy MCP connection | Modern MCP request | REST tool call |
|---|---|---|---|
| Principal | auth context | auth context | auth context |
| Client name, version | session info | `requestClientInfo` | surface: `cli`, `webui`, `tray`, `rest` |
| Workspace root | session info | empty | empty |
| Run name | header, HTTP transports only | header | header |

- **Principal gains the credential generation (FR-009).** `principalFromContext` appends the token prefix for agent tokens and the client id for client credentials. Other principal kinds are unchanged.
- **Per-request credential (FR-009).** The connection's cached id becomes a small map from principal to work-session id in `SessionInfo`. A request whose principal has no entry resolves its own.
- **Idle clock (FR-019).** The tracker gets `Touch(id)`, called for every recorded attempt. For a legacy connection the cached id is kept (FR-006) and `Touch` reinstates a reaped entry instead of minting a new id.
- **Run name (FR-003, FR-004, FR-008).** `X-MCPProxy-Run` is read in one place for REST and for MCP over HTTP, validated (1 to 128 printable characters), and stored on the request context. It becomes its own field in the tracker key, after the principal. The existing `CorrelationID` override is not used, because it replaces the whole key and would join principals. The tracker counts named sessions per principal per day and ignores the name past 1,000. The header is not in the upstream forwarding allow-list and a test asserts it never reaches an upstream.
- **Spec 058 US3 stays.** `markSessionWorked` keeps its early return: no connection record is written for a modern request. Only work-session resolution moves out of `EnsurePersisted`.

Rejected: resolving in `ActivityService` from the stored row. The loop has no request context, so it cannot see the credential, the client name of a modern request or the run name.

### D2. What counts as an attempt

One pure function in `internal/storage`, `attemptRole(record)`, returns whether the row opens an attempt, whether the attempt failed, and whether it was a policy block. It reuses the rule the usage aggregate applies today, extended with the pairing `profiles_suggest.go:207-215` handles:

- `tool_call`: opens an attempt; failed when its status is not success.
- `internal_tool_call` named `call_tool_*`: never counted (mirror).
- other `internal_tool_call`: opens an attempt.
- `policy_decision`: opens an attempt only when it is a refusal with no `tool_call` row for the same request, which is the case for pre-dispatch refusals written by `RecordToolCallRejected`. A post-dispatch decision that blocks the result (output-schema validation, `internal/server/mcp.go:8809`) marks the attempt of its request id as failed without opening a new one. A warning changes nothing.

The first task of PR-1 is a table test that feeds every emitter's row shape through `attemptRole`, so the rule is pinned to what the code writes, not to this description.

To apply "same request id" exactly and without a scan, a fourth bucket holds one entry per attempt (D3, `work_session_attempts`). A row with a request id looks its attempt up there: absent means a new attempt; present means the same attempt, and a row that turns it from success to failure updates the entry and the summary once. A row with no request id is its own attempt and gets no entry.

An entry may be removed only when no later row of its request can arrive. That is decided by a closed mark, not by age:

- **Closing.** The request handler emits one `attempt.closed` bus event (work-session id, request id) when it returns, after its last row. The bus is first in, first out per subscriber, so the single writer sees it after every row of that request and sets the closed mark on the entry. The synchronous `RecordToolCallRejected` path writes its entry already closed: a pre-dispatch refusal is the only counted row of its request. The step 2 table test also asserts, per emitter, that the close event follows the last row.
- **Row count.** The entry counts its indexed rows. `SaveActivity` increments it; every cleanup step that deletes a row reads that row anyway and decrements the entry of its request id in the same transaction.
- **Removal.** Every cleanup pass removes all entries that are closed and have no rows, and an entry goes with its session's summary (steps 1 and 3c of D4). No age is involved and no order is needed: a closed entry with no rows has no further use. A late row cannot be counted twice, because an entry is never removed before its request has closed.
- **Lost close events.** A close event can be lost with a dropped bus event or a crash. At start every entry is marked closed, since no request survives a restart. A running pass treats an unclosed entry as closed once one hour has passed since the latest row of its request was written. HTTP deadlines can be disabled, so no shorter bound is safe; a request that writes rows more than an hour apart is the one case this can close early, and its entry still has rows then, so it is not removed.
- **Hard bound (FR-034).** Unclosed entries are the only ones cleanup cannot remove. The writer keeps at most 1,024 of them in total; when one more would be added, the oldest unclosed entry is marked closed. Keys and values are length-bounded (D3: under 512 bytes each), so they hold under 512 KiB, half of the smallest non-zero byte budget (`activity_max_size_mb: 1`). The cap can only cost exactness if a request is still writing rows after 1,024 newer attempts were opened and all of its earlier rows were already trimmed; a test pins the cap and the bound.

### D3. Storage

Four new buckets. All writes happen in the transaction that writes the activity row.

- **`work_sessions`**: key = work-session id; value = JSON summary (Data Model).
- **`work_session_rows`**: key = `<work-session id>\0<activity key>`; value = one flag byte (attempt, failure, policy block, bodies removed) followed by the server name. Serves the timeline page, failure navigation, reach filtering without loading rows, and whole-session removal.
- **`work_session_attempts`**: key = `<work-session id>\0<request id>`; key = `<work-session id>\0<request id>` (a request id longer than 128 bytes is stored as its SHA-256); value = one outcome byte (success, failure, policy block), the closed mark, the time of its latest row (8 bytes), the count of indexed rows (4 bytes) and the server name. Makes attempt counting exact (D2).
- **`upstream_events_by_server`**: key = `<server>\0<activity key>`; value = owner user id or empty. Serves the timeline join.

`Manager.SaveActivity` (`internal/storage/activity.go:55`) is the single entry point: when the record has a work-session id it also writes the index entry and updates the summary. The asynchronous loop and the synchronous `RecordToolCallRejected` both go through it, so neither can skip the summary.

`ActivityRecord` gains `BodiesRemoved bool` and `RunName string`, both `omitempty`.

**Backfill (FR-014, FR-018).** A marker in the existing metadata bucket records completion. On start, a goroutine joined in `Runtime.Close` walks the activity bucket in batches of 1,000 keys per transaction. For each row with a work-session id and no index entry it writes the index entry and applies the row to the summary. The index-entry check makes the pass idempotent and safe against rows written while it runs. A session whose rows show more than one credential identity is marked ownerless and is visible to administrators only.

**Repair (FR-013).** Separately from the one-time backfill, every start walks the distinct work-session ids of the index bucket (one seek per session, keys only). An id with index entries and no summary gets a summary rebuilt from its indexed rows and marked `rebuilt`. An existing summary is never lowered.

Rejected: deriving summaries on read. A list of 1,000 sessions would scan the whole log (SC-006).
Rejected: a separate failures bucket. The flag byte in the index is enough, since navigation walks index keys only; the benchmark in the test plan decides whether that holds at 50,000 rows.

### D4. Retention and pruning

`runRetentionCleanup` keeps its three steps, each made session-aware. Every step writes in batches of at most 1,000 rows per transaction (FR-038).

1. **Session window.** Summaries whose last activity is older than `session_retention_days` are deleted with their index entries. Their rows stay (FR-036).
2. **Age cap.** As today, oldest first. A deleted row that has an index entry takes the entry with it, and its session's summary gets `truncated_start`. Only the stored counters (`stored_failures`, `stored_blocks`) are reduced; lifetime counts and first and last failure facts are never lowered (FR-013, FR-039). The same rule applies to steps 3c and 3d.
3. **Size and count caps**, one ordered pass from the oldest key:
   - **3a. Bodies.** While over the size budget, rewrite the row without `Arguments` and `Response` and set `BodiesRemoved`. A metadata key remembers how far this has gone, so later passes do not re-read stripped rows.
   - **3b. Rows outside the window.** In the same pass, delete rows that have no work-session id or whose summary is gone or older than the window.
   - **3c. Whole sessions.** If still over, take summaries inside the window by oldest last activity and delete each whole through its index prefix: rows, index entries, summary. Record the shortfall.
   - **3d. Last session.** If one session is all that is left and it is still over, trim it from its oldest end and set `truncated_start`.
4. **Attempt entries.** After steps 1 to 3, in the same pass, one keys-and-values walk of `work_session_attempts` removes every entry that is closed and has no rows (D2), and closes unclosed entries past the request-lifetime bound. Step 3d repeats this for the last session before it measures again, so the rows it trimmed release their entries in the same pass. What can remain beyond rows is the unclosed entries, capped at 1,024 (D2).

The byte budget counts the activity bucket and the four new buckets (FR-034). The count cap counts activity rows only, as today.

**Shortfall (FR-035).** A metadata record holds the time it was last observed, the cause (`age_cap`, `record_cap`, `size_cap`) and the start of the oldest complete session. It also holds the time of the last removal inside the window. The record is cleared only when that time is older than the retention window, which is when complete history covers the window again. A quiet pass alone does not clear it.

**Defaults.** `activity_max_records` default becomes 400,000. Installs whose config file already holds an explicit `100000` keep it, and the doctor warning is their signal. `session_retention_days` is new, default 7, resolved through an `Effective*` reader like the other bounds (`internal/config/effective_bounds.go`).

Rejected: making the size cap soft for sessions inside the window. It breaks the disk bound, which the spec lists as an anti-goal.

### D5. Upstream events

A new activity type `upstream_event`, written by `ActivityService` so it stays on the single writer.

Neither existing signal can carry this. The supervisor's bus event holds only a level, a title, a message and a `connected` flag derived from the title (`internal/runtime/supervisor/actor_pool.go:62-79`). The supervisor's error-code edge (`shouldNotifyErrorCode`, `supervisor.go:1070-1098`) is a sampled predicate that by its own comment misses transient failures and recoveries, and its handler reads the connection state after the fact.

The transition itself happens in one place, the state manager (`internal/upstream/types/types.go`). Its existing callback cannot be the source as it is: `ConnectionInfo` has no transition time, and the callback is dispatched synchronously on some paths and in a new goroutine on the error path (`types.go:344-350`, `:385-390`), so a loss can be delivered after the recovery that followed it.

The plan therefore captures the transition inside the state manager, under its lock:

- Every state mutation appends a small transition record to an ordered queue while the lock is held: old state, new state, the time taken at that moment, and a copy of the last error. The existing `onStateChange` callback and its dispatch are not changed.
- One goroutine per state manager drains the queue in order and hands each record to a transition observer. The queue is bounded; when full, the oldest unsent records of the same kind are merged, never the newest dropped, so a final Ready is always delivered.
- The upstream manager exposes one registration point for the observer. The runtime registers at start, classifies the copied error with `diagnostics.Classify` to get the `MCPX_*` code, and publishes a new bus event without blocking.
- `ActivityService` writes the row with the captured time as its timestamp, so the order against calls is the order things happened (SC-005), and per server the records arrive in mutation order.

| Kind | Rule on the transition |
|---|---|
| `connection_lost` | state leaves Ready, any code not listed below |
| `process_exited` | state leaves Ready with a code in the `MCPX_STDIO_EXIT*` family |
| `login_required` | code is `MCPX_OAUTH_LOGIN_REQUIRED` or the re-auth code |
| `connection_restored` | state returns to Ready after a recorded loss |
| `oauth_refresh_failed` | every failed refresh flight (below) |

**OAuth refresh failures.** The existing `oauth.refresh_failed` event is not enough: it is emitted only when the schedule is given up (terminal class, retries exhausted, token expired too long; `internal/oauth/refresh_manager.go:788-838`), and an ordinary failed attempt only reschedules (`:844-846`). The refresh manager therefore reports each failed flight where its outcome is known, before those branches: server, failure class, the time of the failure, whether it is terminal, and in the server edition the credential's owner. It reports once per flight, from the flight leader; waiters on the same flight and readers of a cached terminal outcome report nothing. The runtime publishes it as the same kind of bus event as the state transitions. The existing `oauth.refresh_failed` event keeps its meaning and its consumers. Test: a transient failure followed by a successful refresh gives one event row and no login-required row.

No new process monitoring is added. `process_exited` is a classification of an existing disconnect, not a new detector. Step 7 of PR-1 starts with a test that kills a stdio fixture, refuses a connection and expires an OAuth login, and records which states and codes the observer receives; the table above is corrected from that test before the rows are built. A reconnect storm produces a record on every attempt; the collapser below bounds what is stored.

The row carries server, kind, time, the `MCPX_*` code and `diagnostics.Get(code).UserMessage`. The raw error string is not copied (FR-021). In the server edition, events about a user's upstream credential carry that user id for access control.

**Collapsing (FR-022, FR-025).** `ActivityService` keeps a small in-memory table keyed by server, kind and owner. A repeat within one minute rewrites the existing row with a higher count and a new last time. A server that has written 120 rows in the current hour merges further events into its latest row of that kind. The table is lost on restart, which costs at most one extra row per key.

**Join (FR-023).** The timeline merges the session's index range with the event ranges of the servers in the summary's per-server partitions, limited to the caller's reach and to the span the caller may see.

### D6. Read doors

New routes, registered as `scopeFiltered` in `getRouteScopes`. `/api/v1/sessions` is untouched (FR-045).

- `GET /api/v1/work-sessions`: query `failed`, `include_blocked`, `token`, `client`, `profile`, `run`, `error_class`, `since`, `until`, `sort` (`last_failure` or `last_activity`), `limit` (max 100), `offset`.
- `GET /api/v1/work-sessions/{id}`
- `GET /api/v1/work-sessions/{id}/timeline`: query `at` (`first_failure`, `failure:<n>`, `row:<activity id>`, `end`), `before`, `after` (each max 250, default 20), `include_bodies`.

**Scope (FR-046, FR-048, FR-049).** One helper projects a summary for the caller:

- Admin: the summary as stored.
- Scoped caller: not owner (name and prefix, or user id) gives not found. Otherwise the per-server partitions are filtered through `scopeAllowedServers`, and start, end, last activity, status, counts and failure facts are recomputed from what is left. Connection count, profile and the truncated-start marker are omitted. No partition left gives not found.

The timeline applies the same reach to index entries by their stored server name before any row is loaded, then loads rows for the page only and passes each request row through a row-ownership predicate and `redactForeignIdentity`. Upstream event rows carry no agent identity and are authorized by their own rule, never by that predicate: the server is in the caller's reach, the event time is inside the projected span (FR-023, FR-048), and the event has no owner or its owner is the caller's user. A scoped agent token therefore sees shared events of its servers and no event about another user's credential. The predicate is new, because the existing one cannot serve user principals: `activityIdentityOwner` gives a non-agent caller an owner with no prefix, and `Owns` rejects an empty prefix (`internal/httpapi/scope_attribution.go:72-85`, `internal/storage/activity_models.go:326-335`). For an agent token it is the existing name-and-prefix rule. For a server-edition user it is the row's stored `UserID`. The same predicate decides summary ownership.

That needs the user id on every row that belongs to a session. Today only the three tool-call builders set it (`internal/runtime/activity_service.go:716`, `:1017`, `:1115`); policy-decision and synchronous rejection rows do not (`:566-587`, `:779-796`), and `ActivityAttribution` has no user field (`internal/runtime/activity_attribution.go:19-38`). `ActivityAttribution` therefore gains `UserID` beside `WorkSessionID` (D1), filled at the emit site from the auth context, and every builder copies it. The summary takes its owner from the first row. Failure index and total come from the stored per-server failure counts in reach.

The list loads all summaries, projects, filters, sorts and pages in memory, as `GetRecentSessionsFiltered` does for connections. At 1,000 sessions this is one bucket read.

### D7. Doctor

`contracts.Diagnostics` gains `ActivityRetention *ActivityRetentionReport`: window in days, shortfall flag, cause, start of the oldest complete session, settings to raise. Built in `internal/management` from a runtime accessor, attached where `oauth_token_storage` is attached (`internal/management/diagnostics.go:295-308`), with one runtime warning line when there is a shortfall. Admin only; a scoped caller gets no section. `cmd/mcpproxy/doctor_cmd.go` prints it on both the all-clear and the issues path. `oas/swagger.yaml` is regenerated.

### D8. CLI

In `cmd/mcpproxy/activity_cmd.go`, registered in its `init()`:

- `mcpproxy activity sessions [--failed] [--no-blocked] [--token] [--client] [--profile] [--run] [--error-class] [--since] [--until] [--sort] [--limit]`
- `mcpproxy activity session <id> [--failures] [--before N] [--after N] [--at …] [--include-bodies]`

Both use new `cliclient` methods, the shared formatter and `argsOrHelpJSON`. With `--failures` the command walks failures with the timeline door and prints overlapping context once.

### D9. Web UI

- `SessionsPanel.vue` reads the new list door and shows the columns of FR-060, the "Failed only" toggle with its "include blocked" switch, and the truncated-start note.
- A new `SessionTimeline.vue` renders one page of the timeline door with previous and next failure controls, row kinds styled apart, millisecond times, expandable bodies and the "copy as CLI command" action.
- Address: `/activity?view=sessions&ws=<id>&at=<position>`, registered in `useScopeQuery.ts` so it survives reloads and scope changes.
- Types in `frontend/src/types/api.ts`, calls in `frontend/src/services/api.ts`.

## Data Model

**Work session summary** (`work_sessions`)

| Field | Notes |
|---|---|
| `id` | work-session id |
| `token_name`, `token_prefix`, `user_id`, `client_id` | ownership; prefix and user id never returned |
| `ownerless` | true for backfilled mixed-credential sessions |
| `client_name`, `surface`, `project_name`, `run_name` | display and filters |
| `profile`, `profile_source` | latest |
| `started_at`, `last_activity_at`, `ended_at` | `ended_at` empty while active |
| `connection_count` | distinct MCP connection ids seen |
| `truncated_start_at` | set when rows were trimmed |
| `rebuilt` | set when rebuilt from rows (FR-013) |
| `servers` | map of server name to partition |

**Partition** (per server; the empty name holds MCPProxy's own tools): lifetime `attempts`, `failures`, `blocks`; stored `stored_failures`, `stored_blocks`; `first_activity_at`, `last_activity_at`, `first_failure_at`, `last_failure_at`; last failure `error_class`, `error_code`, `tool`, `reason` (at most 200 characters, built from error class, code and the catalogue message, never from bodies).

**State**: active, then ended when the idle window has closed (set by the cleanup pass or at shutdown), then active again if a row arrives. At start, a summary with no end gets its last activity time as its end.

## Delivery

**PR-1: record and keep.** Order of work, each step test first:

1. Characterization test: a REST tool call and a modern MCP request produce rows with an empty work-session id today. The test is then inverted.
2. `attemptRole` and its table test over every emitter's row shape.
3. Tracker changes: principal with generation, run name field, `Touch`, named-session limit.
4. Request-level resolution and the attribution field; per-principal cache on the connection.
5. Storage: buckets, `SaveActivity` transaction, summary update, backfill.
6. Session-aware cleanup, body stripping, attempt-entry removal, shortfall record, the two config changes. `session_retention_days` is wired at every point a field needs, including the Web UI settings catalogue beside its siblings (`frontend/src/views/settings/fields.ts:585-587`: number, minimum 1, restart required) with a load, edit and save test (FR-070). Because this PR touches `frontend/src/`, it runs the Playwright sweep.
7. Upstream events and the collapser: state-manager capture, the per-flight OAuth refresh report, and the new type registered everywhere a type is listed (the storage type constants, `ACTIVITY_TYPE_LABELS` in `frontend/src/utils/activity.ts`, which the System view and the backend/frontend label-parity test read, and the CLI's system types), so the System view shows and filters the rows in this PR (FR-024).
8. REST doors, scope projection (request rows and event rows), route-table entries, swagger.
9. Doctor section.
10. Docs: `docs/features/activity-log.md`, `docs/api/rest-api.md`, `docs/configuration.md`, `docs/features/agent-tokens.md` (one token per agent, run name).

**PR-2: CLI.** The two commands, `cliclient` methods, `docs/cli/activity-commands.md`.

**PR-3: Web UI.** The panel, the timeline, unit tests, the Playwright sweep and its report, `docs/web-ui/`.

Each PR is merged before the next is opened.

## Test Plan

- **Unit, storage**: `attemptRole` table; summary arithmetic including per-server partitions; index key order; backfill idempotence with concurrent writes; each cleanup step with a small cap, including the order in 3b to 3d, the age-cap trim and the shortfall record; body stripping keeps every non-body field; attempt entries: a closed entry with no rows is removed in the next pass whatever its age, an unclosed one is kept, an entry with rows is kept, a companion row delivered after all rows were trimmed and the pass ran changes no lifetime count or failure fact, start closes every entry, the 1,024 cap, and the five buckets together at or below the byte budget.
- **Unit, runtime**: tracker with injected `now` (generation, run name, `Touch`, idle reopen, named-session limit); the collapser with injected clock, including the 120-per-hour bound and SC-007's 24-hour storm; a transient OAuth refresh failure followed by recovery; several waiters on one failed flight give one event.
- **Unit, server**: resolution for the four surfaces; two tokens on one connection get two sessions; `code_execution` sub-calls inherit; the run header is not forwarded upstream.
- **HTTP**: the route-table guard; an authorization table for the three doors with admin, two agent tokens, a pinned token and, under `-tags server`, two users; not-found parity for foreign and missing ids; a hidden server's activity changes nothing a scoped caller reads; timeline positions with and without pruned failures; page bound; a scoped token sees a shared upstream event of a server in reach and, under `-tags server`, no event owned by another user.
- **Benchmarks (SC-006)**: list of 1,000 summaries and one timeline page of a 50,000-row session, each run against logs of two sizes to show the cost does not follow the log.
- **E2E**: `scripts/test-api-e2e.sh` gains the morning-after scenario of User Story 1 through REST and the CLI.
- **Web UI**: vitest for the panel and timeline, the settings field and the `upstream_event` type in the System view; the Playwright sweep with screenshots of both screens.
- **Race and lint**: `go test -race ./internal/...`, the server-tag run from `CLAUDE.md`, and both `golangci-lint` v2 runs.

## Constitution Check

| Principle | Status |
|---|---|
| I. Performance at scale | Reads use the index, never the log. The write path adds up to three key writes in the existing transaction; a benchmark guards it. |
| II. Actor-based concurrency | Summary, index and event rows are written by the existing single writer. The tracker keeps its existing mutex; nothing new is shared. |
| III. Configuration-driven | One new setting and one changed default, both in the config file; no tray state. |
| IV. Security by default | New doors are scoped and refuse by default; upstream event text is allow-listed; the run name is a label only. |
| V. TDD | Each step in Delivery starts with its failing test. |
| VI. Documentation hygiene | Docs are part of each PR. |

No violations to justify.

## Risks

- **Write amplification.** Up to four writes per row instead of one. Mitigation: one transaction, small values, benchmark before and after.
- **Summary size.** A session touching many servers carries many partitions. Bounded by the number of configured servers.
- **Dropped bus events.** A row dropped by the bus is also missing from the summary. The two stay consistent, which is the property FR-013 asks for; the loss itself is existing behaviour.
- **Sessions do not survive a restart.** A long agent run across a core restart shows as two sessions. Persisting the tracker is out of scope.
- **Existing configs keep `activity_max_records: 100000`.** The doctor warning is the remedy; the release notes say so.
- **Behaviour change for token grouping.** Adding the generation to the principal changes work-session ids for agent tokens once, at upgrade.
