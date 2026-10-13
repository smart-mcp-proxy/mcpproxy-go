---
description: "Tasks for Spec 113 — upstream resilience"
---

# Tasks: Upstream resilience (Spec 113)

**Input**: [spec.md](spec.md), [plan.md](plan.md), [research.md](research.md). TDD: every implementation task is preceded by the task that writes its failing test. Each slice is one PR branched from `main`; task IDs are pre-allocated per slice and MUST NOT be used by another slice. Slices tick their own tasks in their PR description, not in this file (slices do not edit `specs/113-upstream-resilience/**`).

Format: `[ID] [P?] [Story] Description (files) — FR`. `[P]` = can run in parallel with other `[P]` tasks of the same slice.

## Slice 113-a — oauth-refresh-robustness (US1, G1+G2) — T100–T129

- [ ] T100 [P] [US1] Failing test: `ClassifyRefreshError` table — `OAuthError{invalid_grant}` wrapped by mcp-go (`%w`), 200-with-error-body form, `invalid_client`, `unauthorized_client`, `unsupported_grant_type`, `invalid_scope`, mcp-go `"refresh token request failed with status 503: <html>"`, 429, `*net.OpError` dial/reset, `context.DeadlineExceeded`, `server not found`, `server does not use OAuth`, unknown; plus the manual-path typed error (`internal/oauth/refresh_errors_test.go`) — FR-007, FR-008
- [ ] T101 [US1] Implement `ClassifyRefreshError` + `RefreshHTTPError`; derive the existing metric labels from it; keep the substring list only as the last fallback (`internal/oauth/refresh_errors.go`, `refresh_manager.go`) — FR-007, FR-008
- [ ] T102 [P] [US1] Failing test: storage `UpdateOAuthToken(key, mutate)` preserves `ClientID/ClientSecret/CallbackPort/RedirectURI/Created/DisplayName` when `UpdateOAuthClientCredentials` runs concurrently (100 interleaved goroutines, `-race`) (`internal/storage/bbolt_oauth_update_test.go`) — FR-006
- [ ] T103 [US1] Implement `BoltDB.UpdateOAuthToken`; switch `PersistentTokenStore.SaveToken` and the manual refresh path to it (`internal/storage/bbolt.go`, `internal/oauth/persistent_token_store.go`, `internal/upstream/core/client.go`) — FR-006
- [ ] T104 [P] [US1] Failing test: coordinator — N=50 barrier-started `Do` calls on one key run `fn` once and all get the same token; a caller whose observed RT is stale gets the persisted token with `skipped=true` and no `fn` call; a failing `fn` returns the same error to all waiters; a cancelled initiator does not cancel the flight; different keys run in parallel (`internal/oauth/refresh_coordinator_test.go`) — FR-001, FR-003, FR-004
- [ ] T105 [US1] Implement `RefreshCoordinator` (`internal/oauth/refresh_coordinator.go`) — FR-001, FR-003, FR-004, FR-011
- [ ] T106 [P] [US1] Failing test: `PersistentTokenStore.GetToken` with a bound refresher and an expired token refreshes through the coordinator and returns the new token; without a bound refresher it never calls the network (assert on an `httptest` AS request counter); terminal error wraps `transport.ErrOAuthAuthorizationRequired`; transient returns `ErrTokenRefreshTransient` (`internal/oauth/persistent_token_store_test.go`) — FR-002, FR-005
- [ ] T107 [US1] Implement refresher binding: `SetRefresher`/`BindRefreshHandler` with an `atomic.Pointer` handler slot; `GetToken` pre-emption; `SaveToken` never enters the coordinator (`internal/oauth/persistent_token_store.go`, `internal/oauth/config.go` attach, `internal/upstream/core/connection_oauth.go` bind call) — FR-002, FR-005
- [ ] T108 [US1] Route `RefreshOAuthTokenDirect` (both sub-paths) through the coordinator with trigger `proactive`; manual path returns `RefreshHTTPError` (`internal/upstream/core/client.go`, `internal/upstream/manager.go`) — FR-001, FR-007
- [ ] T109 [P] [US1] Failing test (SC-001): rotating `httptest` AS that revokes the grant on refresh-token reuse; expired token; 50 goroutines through a real mcp-go `StreamableHTTP` client with OAuth against an `httptest` MCP server + 1 concurrent `RefreshOAuthTokenDirect`; assert exactly 1 token request, all calls succeed; `-race -count=20` (`internal/upstream/core/oauth_refresh_race_test.go`) — FR-012, SC-001
- [ ] T110 [P] [US1] Failing test: `invalid_client` with a DCR record → `ClearOAuthClientCredentials` called once, schedule `RefreshStateFailed` with the re-register message, zero further retries; second `invalid_client` after a re-registration and before any success → terminal "rejects new registrations"; static client → terminal, credentials untouched; a registration or grant saved by a concurrent login is not cleared (compare-and-clear) (`internal/oauth/refresh_manager_invalid_client_test.go`) — FR-009, SC-002
- [ ] T111 [US1] Implement `invalid_client` handling and per-key re-registration state (`internal/oauth/refresh_manager.go`, `refresh_coordinator.go`) — FR-009
- [ ] T112 [P] [US1] Failing test: `RefreshManager` reads records by server key (token-age log and `EmitOAuthTokenRefreshed` fire for a keyed record) (`internal/oauth/refresh_manager_test.go`) — FR-010
- [ ] T113 [US1] Fix the two `GetOAuthToken(serverName)` lookups to use the server key (needs the server URL: obtain via the runtime interface or store the key on the schedule) (`internal/oauth/refresh_manager.go`) — FR-010
- [ ] T114 [US1] CLI in-memory store path uses the same coordinator (`internal/oauth/config.go` `globalTokenStoreManager`) + test — FR-014
- [ ] T119 [P] [US1] Failing test: bound store returns `ExpiresAt` zero to mcp-go and mcp-go never issues a refresh even when the token expires between `GetToken` and `IsExpired`; reactive refresh with a DCR record and a handler lacking a client id uses stored credentials; terminal latch (no network after `invalid_grant` until a new token is saved) and transient cooldown; compare-and-clear does not clear a registration saved by a concurrent login; login-flow `invalid_client` after re-registration is terminal (`internal/oauth/refresh_coordinator_test.go`, `internal/upstream/core/oauth_refresh_race_test.go`) — FR-002, FR-003, FR-004, FR-009
- [ ] T120 [P] [US1] Failing test: login saves a new token and DCR client while an old refresh flight is in flight; the old flight's success is discarded (login token kept, client/token pair consistent) and its `invalid_grant` failure neither latches nor fails the schedule (`internal/oauth/refresh_coordinator_test.go`) — FR-006a
- [ ] T115 [US1] 5xx/429/network → existing backoff with `failed_server_error`/`failed_network` labels; test asserts backoff sequence unchanged — FR-008, FR-013
- [ ] T116 Docs: `docs/oauth-resource-autodetect.md` or the OAuth troubleshooting page — refresh serialization, `invalid_client` behaviour, new metric labels (published-site rules apply)
- [ ] T117 Gates: race tests, server-tag tests (CI skip regex), golangci-lint v2 ×2, `./scripts/test-api-e2e.sh` (isolated instance)
- [ ] T118 Cross-model review (opencode harness, gpt-6.1-sol), ≤10 rounds
- T121–T129 reserved for 113-a follow-ups

## Slice 113-b — oauth-discovery-overrides (US2, G3+G5) — T130–T159

- [ ] T130 [P] [US2] Failing test: `OAuthConfig` new fields JSON/mapstructure round-trip, `OAuthConfigChanged` detects each, `MergeOAuthConfig` merges each, storage `UpstreamRecord` round-trip (`internal/config/*_test.go`, `internal/storage/*_test.go`) — FR-022, FR-028, FR-029
- [ ] T131 [US2] Add the four fields; update `OAuthConfigChanged`, `MergeOAuthConfig`, `copyOAuthConfig` (`internal/config/config.go`, `merge.go`) — FR-022, FR-028
- [ ] T132 [P] [US2] Failing test: validation table — https ok; http loopback (`localhost`, `127.0.0.5`, `[::1]`) ok; http public host rejected; relative, fragment, userinfo, empty host rejected; load path drops invalid with Warn and boot succeeds (`internal/config/oauth_validation_test.go`) — FR-023
- [ ] T133 [US2] Implement write-time validation (`ValidateDetailed`, REST, MCP `upstream_servers`, CLI) and load-time sanitize (`internal/config/oauth_validation.go`, write paths) — FR-023
- [ ] T134 [P] [US2] Failing test: `OAuthTransportWrapper` rewrites AS metadata fields from overrides, output passes mcp-go URL validation; synthesizes a document when metadata is unreachable and authz+token overrides exist; leaves non-metadata responses untouched (`internal/oauth/transport_wrapper_test.go`) — FR-024
- [ ] T135 [US2] Implement metadata rewrite/synthesis in the wrapper; pass `auth_server_metadata_url` to `OAuthConfig.AuthServerMetadataURL` (`internal/oauth/transport_wrapper.go`, `internal/oauth/config.go`) — FR-024
- [ ] T136 [P] [US2] Failing test: `extra_params` injected on a token endpoint `/oauth2/v1/exchange` learned from metadata; not on `/api/tokens/list`; heuristic still used when no endpoint known; 201→200 normalization follows the same match (`transport_wrapper_test.go`) — FR-025
- [ ] T137 [US2] Implement endpoint-URL matching (`internal/oauth/transport_wrapper.go`) — FR-025
- [ ] T138 [P] [US2] Failing test: end-to-end with `httptest` AS on a non-standard metadata path + override; mcp-go authorization URL host equals override; token exchange and refresh hit the override (`internal/upstream/core/oauth_overrides_test.go`) — FR-024, FR-031
- [ ] T139 [P] [US2] Failing test: discovery cache — 5 `createOAuthConfigInternal` calls → 1 request per discovery URL; different overrides → separate entries; failure cached 30 s then retried; LRU bound; invalidation on a differing `resource_metadata` (`internal/oauth/discovery_cache_test.go`) — FR-026, FR-027, SC-003
- [ ] T140 [US2] Implement `discovery_cache.go` and route preflight discovery through it; invalidation hook from the 401 PRM-URL handling (`internal/oauth/discovery.go`, `discovery_cache.go`, `config.go`, `internal/upstream/core/connection.go` one call) — FR-026, FR-027
- [ ] T141 [P] [US2] Failing test: scope waterfall — PRM scopes + AS advertises `offline_access` → appended once; explicit `oauth.scopes` → unchanged; AS-derived scopes → unchanged; empty → unchanged (`internal/oauth/config_test.go`) — FR-020
- [ ] T142 [US2] Implement `offline_access` append (`internal/oauth/config.go`) — FR-020
- [ ] T143 [P] [US2] Failing test: `grant_types_supported` without `refresh_token` → one Warn per server per process (zap observer); absent field → none (`internal/oauth/discovery_test.go`) — FR-021
- [ ] T144 [US2] Implement the warning (`internal/oauth/discovery.go` or `config.go`) — FR-021
- [ ] T145 [US2] `ServerFieldMaskDecisions` rows `NotSecret` for the four fields; contracts OAuth projection; `cmd/generate-types` + regenerate `frontend/src/types/contracts.ts`; `make swagger`, commit `oas/` — FR-028
- [ ] T150 [P] [US2] Failing test: OAuth client refuses https→http redirect to a non-loopback host and cross-origin redirect of a token/registration POST; same-origin redirect still works; with endpoint overrides and unreachable standard metadata mcp-go never hits `/authorize`, `/token`, `/register` defaults; override with a query string rejected at write time (`internal/oauth/transport_wrapper_test.go`, `internal/config/oauth_validation_test.go`) — FR-023, FR-024, FR-027a
- [ ] T146 [US2] `logSafeURL` everywhere a new URL is logged; test asserts no query string in logs (`internal/oauth`) — FR-030
- [ ] T147 Docs: `docs/configuration.md` OAuth section (four fields, validation, precedence over discovery, `offline_access` rule, cache TTLs)
- [ ] T148 Gates (as T117) incl. server-tag build
- [ ] T149 Cross-model review (opencode harness, gpt-6.1-sol), ≤10 rounds
- T151–T159 reserved for 113-b follow-ups

## Slice 113-c — call-error-taxonomy (US3, G6) — T160–T189

- [ ] T160 [P] [US3] Failing test: `callerr.Classify` table covering every FR-041 rule incl. `isError`, `ErrSessionTerminated`, `OAuthAuthorizationRequiredError`, recorded 401/403, `limiter.LimitError`, `managed.ErrConnectionGenerationChanged`, profile refusal, `jsonschema.ValidationError`, caller `context.Canceled`, `DeadlineExceeded` dispatched vs not, `*net.OpError`, mcp-go `*transport.Error`, recorded 502 with untyped mcp-go error, mcp-go `ErrInvalidParams`/`ErrInternalError`, `errors.New("custom -32001 message")` after a recorded 200, unknown (`internal/callerr/classify_test.go`) — FR-040, FR-041, FR-049
- [ ] T161 [US3] Implement `internal/callerr` — FR-040, FR-041
- [ ] T162 [P] [US3] Failing test: `Outcome.AuditClass()` total mapping table (`internal/callerr/audit_test.go`) — FR-047
- [ ] T163 [US3] Implement mapping; `audit_funnel.go` uses it so audit and activity agree (`internal/callerr`, `internal/server/audit_funnel.go`) — FR-047
- [ ] T164 [P] [US3] Failing test: transport call recorder — status recorded for the request carrying the recorder ctx only; installed in every `CreateHTTPClient` branch (plain, static headers, trace, Retry-After, OAuth); concurrent calls do not cross-contaminate (`internal/transport/call_recorder_test.go`) — FR-042
- [ ] T165 [US3] Implement `WithCallRecorder` + round-tripper; install in `CreateHTTPClient` (`internal/transport/`) — FR-042
- [ ] T166 [P] [US3] Failing test: `ActivityRecord` new fields omitempty; a pre-Spec-113 JSON fixture decodes byte-identically on re-encode (`internal/storage/activity_models_test.go`) — FR-043
- [ ] T167 [US3] Add fields (`internal/storage/activity_models.go`) — FR-043
- [ ] T168 [US3] Stamp fields at every tool-call activity write path (direct, `call_tool_*`, `code_execution` sub-calls, REST `/api/v1/tools/call`); recorder ctx installed at those dispatch sites (`internal/server/mcp.go`, `mcp_routing.go`, `mcp_code_execution.go`, `server.go`) — FR-044, FR-048
- [ ] T169 [P] [US3] Failing integration test (SC-004): `httptest` Streamable HTTP upstream producing isError, 502 HTML, JSON-RPC -32001, reset, timeout, 404 session, 401; assert persisted record and `GET /api/v1/activity` JSON per class (`internal/server/call_error_taxonomy_test.go`) — SC-004
- [ ] T170 [US3] REST + SSE + contracts + `cmd/generate-types`; `ActivityFilter` `error_class`/`fault_domain`; `make swagger`, commit `oas/` (`internal/contracts/activity.go`, `internal/httpapi/activity.go`, `internal/runtime/activity_service.go`, `event_bus.go`) — FR-045, FR-046
- [ ] T171 [US3] CLI: `activity list|show -o json|yaml` fields; `--error-class`, `--fault-domain`; `activity show` table line (`cmd/mcpproxy/activity_cmd.go` + tests) — FR-045, FR-046
- [ ] T172 Docs: `docs/features/activity-log.md` + `docs/cli/activity-commands.md` (class table, fault domains, filters)
- [ ] T173 Gates (as T117)
- [ ] T174 Cross-model review (opencode harness, gpt-6.1-sol), ≤10 rounds
- T175–T189 reserved for 113-c follow-ups

## Slice 113-d — health-call-failure-rate (US4, G7) — T190–T219

- [ ] T190 [P] [US4] Failing test: `callstats.Window` with injected clock — bucket rotation, 5 min expiry, counted vs uncounted, dominant kind, concurrent `Record` under `-race` (`internal/upstream/callstats/window_test.go`) — FR-060, FR-066
- [ ] T191 [US4] Implement `Window`, `Registry` (drop on remove/rename) (`internal/upstream/callstats/`) — FR-060, FR-066
- [ ] T192 [P] [US4] Failing test: local predicate table (or 113-c classifier if merged) — isError, policy refusal, limiter, generation refusal, validation, cancel, auth → uncounted; network, post-dispatch timeout, 5xx, JSON-RPC error → failure; success → counted ok (`internal/upstream/callstats/classify_test.go`) — FR-061
- [ ] T193 [US4] Implement predicate (`internal/upstream/callstats/classify.go`) — FR-061
- [ ] T194 [P] [US4] Failing test: both choke points record — `Manager.CallTool`/`CallToolOnEpoch` and `code_execution` dispatch (`internal/upstream/manager_callstats_test.go`, `internal/server/mcp_code_execution_callstats_test.go`) — FR-062
- [ ] T195 [US4] Record in `Manager.callTool` and after `CallToolOnEpoch` in `mcp_code_execution.go`; expose `Manager.CallStats(server)` and `RecordCallOutcome` (`internal/upstream/manager.go`, `internal/server/mcp_code_execution.go`) — FR-062
- [ ] T196 [P] [US4] Failing test: calculator precedence and boundaries table (quarantined, disabled, disconnected, error, OAuth expired, refresh failed, missing secret, retry stopped each win; 4/4 → healthy; 5 @ 50% → healthy; 5 @ 60% → degraded; summary text) (`internal/health/calculator_test.go`) — FR-063, FR-064, FR-067
- [ ] T197 [US4] Add `CallsInWindow`, `CallFailuresInWindow` and the degraded branch with constants (`internal/health/calculator.go`) — FR-063, FR-064, FR-065
- [ ] T198 [US4] Fill the inputs at all three construction sites from `Manager.CallStats` (`internal/runtime/runtime.go`, `internal/server/server.go`, `internal/server/mcp.go`) + a test that the three agree — FR-063
- [ ] T199 [US4] Health-change SSE: verify the existing poll/diff emits on a failure-rate change; add debounce only if a push path is needed (record which in the PR) — FR-068
- [ ] T200 [P] [US4] Integration test (SC-005): `httptest` upstream failing 6/10 → `/api/v1/servers` health degraded; advance clock past window → healthy — SC-005
- [ ] T201 Docs: health status page (`docs/` health section) — new degraded reason, thresholds
- [ ] T202 Gates (as T117)
- [ ] T203 Cross-model review (opencode harness, gpt-6.1-sol), ≤10 rounds
- T204–T219 reserved for 113-d follow-ups

## Slice 113-e — session-terminated-reinit (US5, G8) — T220–T239

- [ ] T220 [P] [US5] Failing test pinning mcp-go v1.0.0 behaviour: 404 on a non-initialize POST → `ErrSessionTerminated` and session id cleared; 404 with no session id → also `ErrSessionTerminated`; a second `Initialize` on the same client/transport stores the new session id (or document that it cannot) (`internal/upstream/core/session_reinit_mcpgo_test.go`) — FR-088
- [ ] T221 [US5] Implement `core.Client.SessionSnapshot()` and `ReinitializeSession(ctx)` without touching `connectionEpoch` (`internal/upstream/core/session_reinit.go`) — FR-080, FR-081, FR-083
- [ ] T222 [P] [US5] Failing test (SC-006): `httptest` Streamable HTTP server that forgets the session once; single call → 1 initialize, call succeeds, state stays Ready, counter = 1 (`internal/upstream/managed/session_reinit_test.go`) — FR-080, FR-084, FR-085
- [ ] T223 [P] [US5] Failing test: 10 concurrent calls on the same terminated session → exactly 1 initialize, 10 retries, all succeed; callers seeing a newer session id retry without re-init (`session_reinit_test.go`) — FR-082
- [ ] T231 [P] [US5] Failing test: write/destructive tool after 404 → re-init, no repeat, `session_terminated` error, next call succeeds; changed tool hash after re-init → no retry, differential update runs; health `Ping` 404 first → following call joins the re-init (no request sent without a session id); cached-tool-count `ListTools` 404 → re-init, no `Error` state (`internal/upstream/managed/session_reinit_test.go`) — FR-080–FR-083
- [ ] T232 [P] [US5] Failing test: a pinned call waiting on the re-init gate is refused with `ErrConnectionGenerationChanged` when the re-list shows its tool changed; after a re-init the upstream's `notifications/tools/list_changed` on the GET stream is still received (or, if the listener cannot be restarted, the full reconnect path runs and no retry happens) (`internal/upstream/managed/session_reinit_test.go`) — FR-083a
- [ ] T224 [P] [US5] Failing test: retry also 404 → call fails and existing error path runs; re-initialize fails → original path; modern protocol or empty pre-call session id → no retry; non-404 errors → no retry (`session_reinit_test.go`) — FR-080, FR-084, FR-086
- [ ] T225 [US5] Implement single-flight re-init + retry-once in `managed.Client` call, `ListTools` leader and prompt paths; only the final error reaches `isConnectionError`; schedule async tool refresh after re-init (`internal/upstream/managed/client.go`, `prompts.go`) — FR-080–FR-084
- [ ] T226 [US5] Info log + per-client counter in diagnostics JSON (Prometheus counter if a seam exists) — FR-085
- [ ] T227 [P] [US5] Test: `CallToolOnEpoch` pinned call succeeds after re-init (epoch unchanged) — FR-083
- [ ] T228 Docs: troubleshooting note on session loss behaviour
- [ ] T229 Gates (as T117)
- [ ] T230 Cross-model review (opencode harness, gpt-6.1-sol), ≤10 rounds
- T233–T239 reserved for 113-e follow-ups

## Slice 113-f — mcp-cache-hints (US6, G9) — T240–T249

- [ ] T240 [P] [US6] Failing test: modern (2026-07-28) `tools/list` and `prompts/list` on `/mcp`, `/mcp/all`, `/mcp/code`, `/mcp/call`, `/mcp/p/<slug>` carry `ttlMs:0`, `cacheScope:"private"`; legacy responses carry neither (`internal/server/cache_hints_test.go`) — FR-090, FR-092, SC-007
- [ ] T241 [US6] Add `mcpserver.WithCacheHints(0, mcp.CacheScopePrivate)` to the four `NewMCPServer` calls (`internal/server/mcp.go`, `mcp_routing.go`) — FR-090, FR-091
- [ ] T242 [US6] Re-run the three frozen tool-surface golden tests; confirm unchanged — FR-094
- [ ] T243 [US6] Tick T063/T064 in `specs/058-mcp-2026-upgrade/tasks.md` — FR-093
- [ ] T244 Gates (as T117)
- [ ] T245 Cross-model review (opencode harness, gpt-6.1-sol), ≤10 rounds
- T246–T249 reserved

## Deferred (design only, no implementation in Spec 113) — T250–T259

- [ ] T250 [G4] CIMD client metadata document schema (research R-G4)
- [ ] T251 [G4] Maintainer decision: host the document on mcpproxy.app (trust/branding/uptime)
- [ ] T252 [G4] Selection logic: AS advertises CIMD support and no static `client_id` → document URL as `client_id`, skip DCR
- [ ] T253 [G4] Fallback to DCR; server-edition non-loopback callbacks
- [ ] T254 [G4] Test plan against an `httptest` AS
- [ ] T255 [G10] Decision: lift the Spec 107 FR-034 freeze (revert vs fresh design)
- [ ] T256 [G10] Per-(server,user) connection keying and fan-out limits
- [ ] T257 [G10] Broker-backed `TokenStore` adapter + 113-a coordinator keyed by (server key, user)
- [ ] T258 [G10] Per-user tool-list divergence policy
- [ ] T259 [G10] Idle eviction and observability

## Dependencies

```
113-a: T100→T101; T102→T103; T104→T105; T106→T107 (needs T105); T108 (needs T101,T103,T105); T109 (needs T107,T108); T110→T111; T112→T113; T114,T115 after T105; T116–T118 last
113-b: T130→T131; T132→T133; T134→T135; T136→T137; T138 after T135,T137; T139→T140; T141→T142 (needs T140 for cached AS metadata); T143→T144; T145–T149 last
113-c: T160→T161; T162→T163; T164→T165; T166→T167; T168 after T161,T165,T167; T169 after T168; T170,T171 after T167; T172–T174 last
113-d: T190→T191; T192→T193; T194→T195; T196→T197; T198 after T195,T197; T199,T200 after T198; T201–T203 last
113-e: T220 first; T221; T222–T224 → T225; T226,T227 after T225; T228–T230 last
113-f: T240→T241→T242,T243; T244,T245 last
Across slices: none. Slices merge in any order; see plan.md File ownership for expected textual overlaps.
```
