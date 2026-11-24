# Tasks: Session Failure Timeline

**Input**: [spec.md](spec.md), [plan.md](plan.md). The plan is a single file: research, data model and contracts are its sections (D1 to D9, Data Model).

**Format**: `[ID] [P?] [Story] Description`. `[P]` = can run in parallel with other `[P]` tasks in the same phase (different files). Stories: US1 (operator finds the failure and what led to it), US2 (REST and modern MCP calls get a session), US3 (a week later the session is whole), US4 (the same from the CLI), US5 (a scoped agent sees only its own trail).

**TDD rule**: in each phase the "Tests first" tasks are written and committed **failing** against the parent commit before any implementation task of that phase. To prove a test bites, revert the implementation, not the test; a neutered build that fails to compile proves nothing.

**Scope rule**: three PRs, each a branch from `main`, each merged before the next is opened: **PR-1** = Phases 1 to 9; **PR-2** = Phase 10; **PR-3** = Phase 11. Phase 12 gates run on every PR. Anything in the spec's non-goals (push sinks, Kubernetes or Loki, child tokens, budgets, approvals, tray parity, telemetry) is out.

**Not to run**: `.specify/scripts/bash/update-agent-context.sh` (it truncates `CLAUDE.md`). Edit the "Recent Changes" entry by hand in T092.

---

## PR-1: record and keep

## Phase 1: characterization (plan Research Q1)

- [ ] T001 [US2] `internal/server/worksession_characterization_test.go`: a REST `POST /api/v1/tools/call` and an MCP request on the 2026-07-28 revision each produce a `tool_call` row with an empty `WorkSessionID` today; the test also records which identity fields each request carries (auth context fields, client name, surface, no workspace root). Commit it passing, with a comment that T028 inverts it

## Phase 2: what counts as an attempt (plan D2; FR-016)

### Tests first

- [x] T010 [P] [US1] `internal/storage/attempt_role_test.go`: table for `attemptRole`: `tool_call` success, error, `blocked`, `rejected`; mirror `internal_tool_call` named `call_tool_*` (not counted); other `internal_tool_call`; blocked `policy_decision` sole (counted, block) and with `Companion` (not counted); warning `policy_decision` (not counted); any counted row with error class `proxy_policy` is a block
- [x] T011 [US1] `internal/server/attempt_invariant_test.go`: drive every site that emits a blocking decision, a rejection or a refusal through its real handler and assert exactly one counted row per attempt, with attempt, failure and block counts separately. Sites: `mcp_routing.go:544-625` and `:744-745`, `mcp.go:3167` and `:3768-3769`, `mcp_code_execution.go:106` and `:1145` with `:1112`, replay `server.go:4533-4543`, `output_sanitisation.go:113`, strict and warn output-schema validation (`mcp.go:8809`), a limiter shed, a refused sandbox child (`mcp_code_execution.go:1293-1299`), builtin management refusals (`mcp.go:4652-4691`)
- [x] T012 [P] [US1] `internal/runtime/concurrency_rejections_test.go`: several shed children of one REST call get distinct request ids, and a child's rejection row carries the id its completion row would have carried

### Implementation

- [x] T013 [US1] `internal/storage/activity_models.go`: add `Companion bool`, `BodiesRemoved bool`, `RunName string` to `ActivityRecord` (all `omitempty`); new `internal/storage/attempt_role.go` with `attemptRole(record)` (makes T010 pass)
- [x] T014 [US1] `internal/server/mcp.go` (`emitActivityPolicyDecision` funnel, `:1180`), `internal/runtime/event_bus.go` (`emitActivityPolicyDecision`): add the companion argument; every call site passes it explicitly per plan D2 "Which blocking decisions have a companion"
- [x] T015 [US1] `internal/server/mcp.go:4652-4691`: builtin management refusals note the `proxy_policy` outcome (`callerr.NoteOutcome`), as the sanitisation path does
- [x] T016 [US1] `internal/reqcontext` (attempt id key), `internal/server/mcp.go` and `internal/server/mcp_code_execution.go:897-900` (set it where the request id is minted), `internal/runtime/concurrency_rejections.go:53-57` (read it, fall back to the transport id) (makes T011, T012 pass)

## Phase 3: work session per request (plan D1; FR-001..009, FR-019)

### Tests first

- [x] T020 [P] [US2] `internal/runtime/worksession_test.go`, injected `now`: the principal carries the token prefix for agent tokens and for client credentials; a client credential rotated inside the idle window gets a new session; run name is its own key field after the principal and never joins two principals; `Touch(id)` moves the idle clock and reinstates a reaped entry; the 1,001st named session of a principal in one day ignores its name
- [x] T021 [P] [US2] `internal/auth/agent_token_test.go` and `internal/storage/client_credentials_test.go`: during a staged client rotation a pending-hash match builds an auth context whose `TokenPrefix` is `PendingPrefix`; the stored record is unchanged; after finalization the prefix is the same value
- [x] T022 [US2] `internal/server/worksession_resolution_test.go`: resolution for the four surfaces (legacy MCP connection, modern MCP request, REST with each `X-MCPProxy-Client` surface, CLI); two tokens on one legacy connection get two sessions; old and pending secret during a staged rotation get two sessions and the pending one keeps its session after finalization; `code_execution` sub-calls inherit the parent's session (FR-005); a legacy connection keeps the id it had (FR-006); no connection record is written for a modern request (Spec 058 US3)
- [ ] T023 [P] [US2] `internal/server/run_header_test.go`: `X-MCPProxy-Run` accepted at 1 to 128 printable characters on REST and MCP over HTTP; an invalid value does not fail the call and is logged once per principal per process (FR-004); the header never reaches an upstream

### Implementation

- [x] T024 [US2] `internal/runtime/worksession.go`: `WorkSessionIdentity` gains the run name field; `Touch`; named-session count per principal per day (makes T020 pass)
- [x] T025 [US2] `internal/storage/agent_tokens.go:1024-1032`, `internal/auth/agent_token.go:307-314`: validation reports which hash matched; the auth context for a pending match carries `PendingPrefix`. List every reader of `AuthContext.TokenPrefix` in the PR description and check each against the new value (makes T021 pass)
- [x] T026 [US2] `internal/server/workspace.go` (`principalFromContext` with generation), `internal/server/session_store.go` (per-principal map in `SessionInfo` replacing the single cached id), new `internal/server/worksession_resolve.go` (builds the identity from the request context on every recorded attempt)
- [x] T027 [US2] `internal/server/mcp_activity_attribution.go`, `internal/runtime/activity_attribution.go`: `WorkSessionID`, `UserID` and `RunName` on the attribution, filled at the emit site; `internal/runtime/activity_service.go`: every row builder copies them (`:566-587`, `:716`, `:779-796`, `:1017`, `:1115`) and falls back to `resolveWorkSession(sessionID)` when the attribution is empty
- [x] T028 [US2] `internal/httpapi/middleware.go` and the MCP HTTP entry: read and validate `X-MCPProxy-Run` in one place, store it on the request context; confirm it is absent from the upstream forwarding allow-list; invert T001 (makes T022, T023 pass)

## Phase 4: storage, summaries, backfill (plan D3, Data Model; FR-010..018, FR-049)

### Tests first

- [ ] T030 [P] [US1] `internal/storage/work_session_summary_test.go`: `SaveActivity` writes the row, the `work_session_rows` entry (flag byte, server) and the summary in one transaction; per-server partition arithmetic (attempts, failures, blocks, stored counters, first and last activity and failure, last failure class, code, tool, reason of at most 200 characters with no body text); index key order equals time order; a row without a work-session id writes no index entry
- [ ] T031 [P] [US3] `internal/storage/work_session_backfill_test.go`: the pass is idempotent and safe with concurrent writes (a row written during it is counted once, FR-018); old completion and blocked-decision pairs count once in both orders and across a batch boundary; two old sheds sharing a request id count twice; a session with more than one credential identity is ownerless; the completion marker stops a second run
- [ ] T032 [P] [US3] `internal/storage/work_session_repair_test.go`: with a summary deleted and index entries kept, start rebuilds the same counts, ownership, attribution and last-failure facts from the retained rows using the stored roles, marks it `rebuilt`, and never lowers an existing summary (FR-013)

### Implementation

- [x] T033 [US1] `internal/storage/work_sessions.go` (new): buckets `work_sessions`, `work_session_rows`, `upstream_events_by_server`; summary and partition types per plan Data Model; `internal/storage/activity.go` (`SaveActivity`, `:55`): index entry and summary update in the same transaction (makes T030 pass)
- [x] T034 [US3] `internal/storage/work_session_backfill.go` (new): batches of 1,000 keys per transaction, role pairing with a five-second window carried across batches, metadata marker; started from `internal/runtime` as a goroutine joined in `Runtime.Close` (makes T031 pass)
- [ ] T035 [US3] `internal/storage/work_session_repair.go` (new): startup walk of distinct work-session ids in the index (one seek per session); `internal/runtime`: at start, a summary with no end gets its last activity time as its end; the cleanup pass and shutdown set `ended_at` once the idle window has closed (FR-015) (makes T032 pass)
- [x] T036 [US3] `internal/storage/manager.go:1607`: `sessionRetentionLimit = 100` no longer applies to work-session summaries (FR-030); the connection-session store keeps its own limit

## Phase 5: retention and pruning (plan D4; FR-030..039, FR-070)

### Tests first

- [ ] T040 [P] [US3] `internal/storage/activity_prune_test.go`, small caps: session window deletes summaries and index entries and keeps rows (FR-036); the age cap removes index entries, sets `truncated_start`, lowers only stored counters; size pass order 3a bodies (every non-body field kept, `BodiesRemoved` set, progress key honoured), 3b rows outside the window, 3c whole sessions oldest last-activity first, 3d trim of the last session; at most 1,000 rows per transaction; the activity bucket and the three new buckets together end at or below the byte budget
- [ ] T041 [P] [US3] `internal/runtime/activity_retention_test.go`: the shortfall record holds time, cause (`age_cap`, `record_cap`, `size_cap`) and the start of the oldest complete session; it is cleared only when the last in-window removal is older than the window, not by a quiet pass
- [x] T042 [P] [US3] `internal/config/effective_bounds_test.go`: `session_retention_days` default 7 through its `Effective*` reader; `activity_max_records` default 400,000; an explicit `100000` in the file is kept
- [x] T043 [P] [US3] `frontend/tests/unit/settings-session-retention.spec.ts`: the field loads, edits and saves beside its siblings

### Implementation

- [x] T044 [US3] `internal/storage/activity.go` (`:419-528`): session-aware prune functions per plan D4 steps 1 to 3d (makes T040 pass)
- [x] T045 [US3] `internal/runtime/activity_service.go` (`runRetentionLoop`, `:339-407`): call order, shortfall metadata record, accessor for the doctor section (makes T041 pass)
- [x] T046 [US3] `internal/config/config.go`, `internal/config/effective_bounds.go`: the new field at all five wiring points (struct, defaults, load, save, effective reader) and the changed default (makes T042 pass)
- [x] T047 [US3] `frontend/src/views/settings/fields.ts:585-587`: `session_retention_days`, number, minimum 1, restart required (makes T043 pass)

## Phase 6: upstream events (plan D5; FR-020..025)

### Tests first

- [ ] T050 [US1] `internal/upstream/types/transition_capture_test.go`: every state mutation appends a record under the lock with old state, new state, time and a copy of the last error; one goroutine delivers them in mutation order; a full queue merges the oldest unsent records of one kind and always delivers the final Ready; run with `-race`
- [ ] T051 [US1] `internal/runtime/upstream_event_source_test.go`: kill a stdio fixture, refuse a connection, expire an OAuth login; record the states and `MCPX_*` codes the observer receives. Correct the kind table in plan D5 from this output before T056
- [x] T052 [P] [US1] `internal/oauth/refresh_coordinator_test.go`: the audit observer reports once per executed failed refresh: transient reactive and proactive failures report; several waiters on one flight give one report; a cancelled caller, a latched terminal outcome, a cooldown and a retired instance (`dialgate.IsRetired`) give none
- [x] T053 [P] [US1] `internal/oauth/refresh_manager_test.go`: one per-server notification number taken under the lock for every schedule install and every accepted failure; the token-saved and failure events carry it
- [ ] T054 [P] [US1] `internal/runtime/upstream_event_collapser_test.go`, injected clock: repeats within one minute rewrite one row with a count and first and last times; 120 rows per server per hour, then merge into the latest row of that kind; collapse per owner, never across owners; SC-007 storm of one failure per second for 24 hours gives at most 2,880 rows; one open `login_required` per server and owner; a stale failure does not open the mark and a stale save does not clear it; a failure delivered before the save that installed its schedule leaves the mark open; a successful login clears it
- [ ] T055 [P] [US5] `internal/httpapi/activity_event_owner_test.go` (`-tags server` for the two-user cases): a foreign user's credential event is absent from the activity list, summary, export and SSE frames, and its detail takes the ordinary not-found exit; counts and paging are computed after the predicate

### Implementation

- [x] T056 [US1] `internal/upstream/types/types.go`: transition queue and drain goroutine, existing `onStateChange` untouched; `internal/upstream` manager: one observer registration point (makes T050 pass)
- [x] T057 [US1] `internal/oauth/refresh_coordinator.go` (`flight`, after `req.Refresh`, `:385-386`, past the retired check `:420-424`): audit observer; `internal/oauth/refresh_manager.go`: notification counter, new token-saved event, number returned by `failScheduleIfCurrent` (makes T052, T053 pass)
- [x] T058 [US1] `internal/runtime/events.go`, `internal/runtime/lifecycle.go`: register the observer at start, classify with `diagnostics.Classify`, publish the new bus event without blocking; `internal/runtime/activity_service.go`: write `upstream_event` rows with the captured time, code and `diagnostics.Get(code).UserMessage` only (FR-021), the owner user id in the server edition, the collapser, and the `upstream_events_by_server` entry (makes T051, T054 pass)
- [x] T059 [US1] Register the type everywhere a type is listed: storage type constants, `ACTIVITY_TYPE_LABELS` in `frontend/src/utils/activity.ts`, `activitySystemTypes` in `cmd/mcpproxy/activity_cmd.go:394-401`, and whatever the backend/frontend label-parity test reads; the System view shows and filters the rows (FR-024)
- [x] T060 [US5] `internal/storage/activity_models.go` (`ActivityFilter`): predicate "not an owned upstream event, or the caller is an administrator or its owner"; `internal/httpapi/activity.go` (`applyActivityScope` `:161-169`, detail `:303-310`, export) and the activity SSE path call it (makes T055 pass)

## Phase 7: read doors (plan D6; FR-040..049)

### Tests first

- [ ] T070 [P] [US5] `internal/httpapi/work_sessions_authz_test.go`: authorization table for the three doors with admin, two agent tokens, a pinned token and, under `-tags server`, two users; a foreign id and a missing id give the same not found; activity on a hidden server changes nothing a scoped caller reads (start, end, status, counts, failure facts, sort order); connection count, profile and the truncated-start marker are omitted for a scoped caller; a scoped token sees a shared upstream event of a server in reach
- [x] T071 [P] [US1] `internal/httpapi/work_sessions_test.go`: list filters `failed`, `include_blocked`, `token`, `client`, `profile`, `run`, `error_class`, `since`, `until`, sort by `last_failure` and `last_activity`, `limit` at most 100; timeline `at=first_failure|failure:<n>|row:<id>|end` with `before` and `after` at most 250 and default 20; current failure index and total over stored failures in reach; with pruned failures the response gives the lifetime count and says earlier failures are no longer stored; bodies omitted unless `include_bodies`; upstream events merged in time order inside the span (SC-005)
- [x] T072 [P] [US5] `internal/httpapi/scope_route_table_guard_test.go`: the three routes classified `scopeFiltered` in `getRouteScopes`; `/api/v1/sessions` stays admin-only (FR-045)
- [x] T073 [P] [US1] `internal/storage/work_sessions_bench_test.go`: list of 1,000 summaries and one timeline page of a 50,000-row session, each against logs of two sizes; the cost does not follow the log size (SC-006)

### Implementation

- [x] T074 [US1] `internal/storage/work_sessions.go`: list all summaries, timeline page by index range, failure navigation over index keys, event range join per server
- [ ] T075 [US5] `internal/httpapi/work_sessions_scope.go` (new): summary projection for the caller and the row-ownership predicate (agent: name and prefix; server user: stored `UserID`), event rows by their own rule
- [x] T076 [US1] `internal/httpapi/work_sessions.go` (new) and route registration in `internal/httpapi/server.go`: `GET /api/v1/work-sessions`, `/work-sessions/{id}`, `/work-sessions/{id}/timeline`; `internal/contracts` types; `redactForeignIdentity` on loaded rows (makes T070 to T072 pass)
- [ ] T077 [US1] `make swagger`: regenerate `oas/swagger.yaml` and `oas/docs.go` (FR-047)

## Phase 8: doctor (plan D7; FR-035)

- [x] T080 [P] [US3] Test first, `internal/management/diagnostics_test.go` and `cmd/mcpproxy/doctor_cmd_test.go`: `ActivityRetention` section with window, shortfall flag, cause, start of the oldest complete session and the settings to raise; one runtime warning line on a shortfall; printed on the all-clear and the issues path; a scoped caller gets no section
- [x] T081 [US3] `internal/contracts` (`ActivityRetentionReport`), `internal/management/diagnostics.go` (beside `oauth_token_storage`, `:295-308`), `cmd/mcpproxy/doctor_cmd.go`, regenerate swagger (makes T080 pass)

## Phase 9: PR-1 end to end and docs

- [x] T090 [US1] `scripts/test-api-e2e.sh`: the morning-after scenario of User Story 1 through REST: an agent token makes calls over REST with `X-MCPProxy-Run`, one fails, the list shows the session as failed and the timeline opens at the failure with the rows before it
- [ ] T091 [P] [US1] Docs: `docs/features/activity-log.md` (work sessions, upstream events, retention order, shortfall), `docs/api/rest-api.md` (three doors, the header), `docs/configuration.md` (`session_retention_days`, the new `activity_max_records` default), `docs/features/agent-tokens.md` (one token per agent, run name, FR-007)
- [ ] T092 [P] `CLAUDE.md` "Recent Changes": one entry for PR-1, written by hand
- [ ] T093 Playwright sweep per `docs/development/web-ui-verification.md` (this PR touches `frontend/src/`)

---

## PR-2: CLI

## Phase 10: `mcpproxy activity sessions` and `session` (plan D8; FR-050..053)

### Tests first

- [ ] T100 [P] [US4] `cmd/mcpproxy/activity_sessions_cmd_test.go`: flags `--failed`, `--no-blocked`, `--token`, `--client`, `--profile`, `--run`, `--error-class`, `--since`, `--until`, `--sort`, `--limit` map to the list door; table columns of User Story 4; `-o json|yaml` carries the REST fields; `--help-json`
- [ ] T101 [P] [US4] `cmd/mcpproxy/activity_session_cmd_test.go`: `session <id>` with `--at`, `--before`, `--after`, `--include-bodies`; `--failures` walks every stored failure and prints overlapping context once; the truncated-start note and the lifetime failure count when earlier failures are gone

### Implementation

- [ ] T102 [US4] `internal/cliclient`: methods for the three doors
- [ ] T103 [US4] `cmd/mcpproxy/activity_cmd.go`: both commands registered in `init()`, shared formatter, `argsOrHelpJSON` (makes T100, T101 pass)
- [ ] T104 [P] [US4] `docs/cli/activity-commands.md` with the morning-after example; `scripts/test-api-e2e.sh` gains the same scenario through the CLI

---

## PR-3: Web UI

## Phase 11: Sessions view and timeline (plan D9; FR-060..066)

### Tests first

- [ ] T110 [P] [US1] `frontend/tests/unit/sessions-panel.spec.ts`: one row per work session with agent token, client, project, started, duration, calls, failures, last failure, reason; "Failed only" with the "include blocked" switch; the truncated-start note; the existing link to Tool calls filtered by the session (FR-065)
- [ ] T111 [P] [US1] `frontend/tests/unit/session-timeline.spec.ts`: opens at the position in the address; the failed row highlighted; upstream and session rows styled apart from calls; millisecond times; previous and next failure; bodies expand on request; "copy as CLI command" gives the `mcpproxy activity session` command for the current view (FR-064); the lifetime-count notice when earlier failures are gone
- [ ] T112 [P] [US1] `frontend/tests/unit/use-scope-query-sessions.spec.ts`: `/activity?view=sessions&ws=<id>&at=<position>` survives reload and scope changes (FR-063)

### Implementation

- [ ] T113 [US1] `frontend/src/types/api.ts`, `frontend/src/services/api.ts`: types and calls for the three doors
- [ ] T114 [US1] `frontend/src/components/activity/SessionsPanel.vue`: read the new list door (makes T110 pass)
- [ ] T115 [US1] `frontend/src/components/activity/SessionTimeline.vue` (new), `frontend/src/views/Activity.vue`, `frontend/src/composables/useScopeQuery.ts` (makes T111, T112 pass)
- [ ] T116 [US1] Playwright sweep with screenshots of both screens and its HTML report; `docs/web-ui/` and `docs/features/activity-log.md` (FR-066)

---

## Phase 12: gates (every PR)

- [ ] T120 `go test -race ./internal/...` and the server-tag run from `CLAUDE.md` (with its `-skip` regex)
- [ ] T121 `./scripts/test-api-e2e.sh`
- [ ] T122 `/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...` and the same with `--build-tags server`
- [x] T123 `scripts/check-upstream-write-sites.sh` and, when the frontend changed, `npm test -- --run` in `frontend/`
- [ ] T124 Cross-model review with opencode `github-copilot/gpt-6.1-sol`, chunked by file, round cap per `CLAUDE.md`

## Dependencies

- Phase 1 → Phase 3 (T028 inverts T001).
- Phase 2 → Phase 4 (roles are stored in the index) → Phases 5, 6, 7.
- Phase 3 → Phase 4 (rows need a work-session id before summaries mean anything).
- Phase 5 → Phase 8 (the doctor section reads the shortfall record).
- Phase 6 → Phase 7 (the timeline joins event rows).
- PR-1 → PR-2 → PR-3.

Story order: US2 (Phases 1, 3) and the counting rule of US1 (Phase 2) come first; US3 (Phases 4, 5, 8) and the rest of US1 (Phases 6, 7) follow; US5 is delivered inside Phases 6 and 7; US4 is PR-2; the Web UI half of US1 is PR-3.

## Parallel examples

- Phase 2: T010 and T012 together; T011 after T013 exists to compile against.
- Phase 3: T020, T021, T023 together.
- Phase 5: T040 to T043 together, then T044 to T047 touch different files and can be split.
- Phase 6: T052 to T055 together while T050 and T056 are worked in sequence.

## Independent test per story

- **US1**: T090 through REST, then T116 in the browser: from the list to the first failure with 20 rows before it in two actions.
- **US2**: T022: every surface produces rows with a work-session id (SC-002).
- **US3**: T040 with T041: after a simulated week under default caps every session is listed and complete, and a forced shortfall shows in `doctor`.
- **US4**: T104: two commands reach the failure.
- **US5**: T070 with T055: no session, count or event outside the caller's reach through any door (SC-008).

## Strategy

The smallest useful slice is Phases 1 to 4 plus Phase 7: REST and modern MCP calls get sessions, summaries exist, and the doors return a failure with its context. Phases 5, 6 and 8 complete PR-1. Phase 6 carries the most risk (a change inside `internal/upstream/types` and the OAuth coordinator); T051 runs before any row is built so the kind table follows what the code emits.
