# Feature Specification: Upstream resilience: OAuth refresh, discovery, call-error taxonomy and health (gaps vs Anthropic MCP-proxy talk)

**Feature Branch**: `113-upstream-resilience`
**Created**: 2026-10-05
**Status**: Draft
**Input**: Maintainer instruction: "work autonomously / create specs/PRs with fixes / review using opencode Sol 6.1". Source material: the 2026-10-01 read-only audit of mcpproxy against the talk "How Anthropic uses MCP for its own product" (https://www.youtube.com/watch?v=e8DMLtP5ibk), gaps G1–G10, each re-verified against `origin/main` @ `8442428d1` in [research.md](research.md).

**Related**: Spec 023 (OAuth refresh manager, `RefreshState`), Spec 022 (redirect-URI port persistence, DCR clearing), Spec 058 (MCP 2026 upgrade: FR-019 cache hints, T063/T064), Spec 085 (pre-dispatch argument validation), Spec 105 (connection-generation pinning, `CallToolOnEpoch`), Spec 107 (audit line `error_class`, FR-034 frozen credential broker), Spec 112 (client header forwarding: `internal/transport/http.go` header func).

**Delivery**: six independent PR slices (113-a … 113-f), each branched from `main`, none stacked on another, none editing these spec files. Each slice owns one user story, one FR group and one pre-allocated task-ID range in [tasks.md](tasks.md). File ownership between slices is fixed in [plan.md](plan.md#file-ownership) so slices can merge in any order.

| Slice | Gap(s) | User story | FRs | Tasks |
|---|---|---|---|---|
| 113-a oauth-refresh-robustness | G1, G2 | US1 | FR-001 – FR-014 | T100 – T129 |
| 113-b oauth-discovery-overrides | G3, G5 | US2 | FR-020 – FR-033 | T130 – T159 |
| 113-c call-error-taxonomy | G6 | US3 | FR-040 – FR-051 | T160 – T189 |
| 113-d health-call-failure-rate | G7 | US4 | FR-060 – FR-068 | T190 – T219 |
| 113-e session-terminated-reinit | G8 | US5 | FR-080 – FR-088 | T220 – T239 |
| 113-f mcp-cache-hints | G9 | US6 | FR-090 – FR-094 | T240 – T249 |
| deferred | G4 (CIMD), G10 (server-edition per-user creds) | — | — | T250 – T259 (design only) |

## Definitions

- **Server key**: `oauth.GenerateServerKey(name, url)` (`<name>_<sha256(name|url)[:16]>`), the BBolt key of a server's `OAuthTokenRecord`. All refresh serialization in this spec is keyed by it.
- **Refresh flight**: one network `grant_type=refresh_token` request for one server key, plus the persist of its result.
- **Reactive refresh**: mcp-go `OAuthHandler.getValidToken` refreshing because `TokenStore.GetToken` returned an expired token (`client/transport/oauth.go:231`). It holds no lock.
- **Proactive refresh**: `oauth.RefreshManager.executeRefresh` → `upstream.Manager.RefreshOAuthToken` → `core.Client.RefreshOAuthTokenDirect`, which uses either `handler.RefreshToken` or, for DCR records the handler has no client id for, the manual `refreshTokenWithStoredCredentials` HTTP call.
- **DCR client**: a client id obtained by Dynamic Client Registration and persisted on the token record (`ClientID` set while `ServerConfig.OAuth.ClientID` is empty). A **static client** comes from `oauth.client_id`.
- **Call outcome**: the result of one upstream `tools/call` dispatch: success, `isError:true` tool result, or a Go error.
- **Session-terminated**: mcp-go `transport.ErrSessionTerminated`, returned by `StreamableHTTP.sendHTTP` on **any** HTTP 404 (`streamable_http.go:763-768`), after it clears the stored session id with `CompareAndSwap`.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — OAuth refresh never burns a rotating refresh token (Priority: P1) — slice 113-a

A user connects an OAuth upstream whose authorization server (AS) rotates refresh tokens (one-time use). Several AI clients call its tools at the moment the access token expires, while the proactive refresh timer also fires. Today each path refreshes independently with the same refresh token; the AS honours the first and treats the second as replay, which on many providers revokes the whole grant, so the user must log in again. After this story exactly one refresh request leaves mcpproxy per expiry, every concurrent caller gets the new token, and when the AS says the grant or the client is dead the user gets one correct next step instead of up to 50 silent retries.

**Why this priority**: losing the grant is a user-visible outage that looks random; it is the highest-impact gap in the audit.

**Independent Test**: an `httptest` AS that rotates refresh tokens and revokes the grant on reuse; N goroutines plus a proactive refresh against an expired token; assert one token request and N successes.

**Acceptance Scenarios**:

1. **Given** an expired access token and a rotating AS, **When** 20 goroutines call through the reactive path and the proactive refresh fires concurrently, **Then** the AS receives exactly one `refresh_token` request and all 21 callers observe the same new access token.
2. **Given** a refresh flight already rotated the token, **When** a later caller that read the old token enters the coordinator, **Then** it re-reads the persisted record, sees a different refresh token, and returns it without a network request.
3. **Given** the AS answers `400 {"error":"invalid_grant"}`, **When** any path refreshes, **Then** every waiter of that flight gets the same terminal error, the RefreshManager stops retrying (`RefreshStateFailed`), and health shows the login action.
4. **Given** a DCR client and the AS answers `401 {"error":"invalid_client"}`, **When** refresh fails, **Then** the stored DCR registration is cleared, the server shows the login action, and the next login re-registers exactly once; a second `invalid_client` after that re-registration and before any successful token response is terminal with an actionable message.
5. **Given** a static `oauth.client_id` and `invalid_client`, **Then** refresh is terminal at once, the static credentials are kept, and the message tells the operator to fix `oauth.client_id`/`oauth.client_secret`.
6. **Given** the AS answers 503, 429 or the connection resets, **Then** the existing exponential backoff applies (10 s → 300 s cap, `DefaultMaxRetries` 50).
7. **Given** a DCR registration is written by `UpdateOAuthClientCredentials` while a refresh saves a token, **Then** neither write loses the other's fields.

### User Story 2 — OAuth discovery that works with non-standard providers and is not refetched on every connect (Priority: P2) — slice 113-b

An operator adds an upstream whose AS is not discoverable the standard way (metadata on a non-standard URL, or endpoints split across hosts), or whose AS only issues refresh tokens when `offline_access` is requested. Today they cannot point mcpproxy at the right endpoints, never get a refresh token, and every reconnect re-runs several discovery requests.

**Why this priority**: unblocks providers that cannot be used at all today and removes reconnect latency; lower than US1 because it affects fewer servers.

**Independent Test**: `httptest` PRM + AS servers with request counters; config overrides; assert endpoints used, scopes requested and discovery request counts.

**Acceptance Scenarios**:

1. **Given** scopes are auto-derived from Protected Resource Metadata (PRM) and AS metadata `scopes_supported` contains `offline_access`, **When** the authorization URL is built, **Then** `offline_access` is requested once.
2. **Given** `oauth.scopes` is set explicitly, **Then** the requested scopes are exactly the configured list.
3. **Given** AS metadata has `grant_types_supported` present without `refresh_token`, **Then** one Warn line per server per process names the server and says tokens will not be refreshable.
4. **Given** `oauth.token_endpoint` (and/or `authorization_endpoint`, `registration_endpoint`, `auth_server_metadata_url`) is set, **When** the server connects, **Then** the overridden URLs are used for authorization, token exchange, refresh and DCR.
5. **Given** an override `http://` URL on a non-loopback host, **When** saved via REST/MCP/CLI, **Then** the write is rejected with a field-specific message; the same value hand-edited into the file loads with a Warn, is ignored, and boot continues.
6. **Given** `extra_params` and an AS whose token endpoint path is `/oauth2/v1/exchange` (no `/token` substring), **Then** the params are injected into the token request; a request to another path on the same host is not modified.
7. **Given** a server reconnects 5 times within an hour, **Then** PRM and AS metadata are fetched for one connect only (including mcpproxy's resource auto-detection and the metadata fetch of each new mcp-go handler); after the server URL or any of the four override fields changes they are fetched again (scopes, client id and `extra_params` do not affect discovery); a failed discovery is retried after the short failure TTL.

### User Story 3 — Every failed tool call says what failed and whose fault it is (Priority: P1) — slice 113-c

A user looks at the Activity log and sees `status=error` for a call. Today they cannot tell whether the network dropped, the upstream returned HTTP 502, the upstream answered with a JSON-RPC error, the tool itself reported a business error (`isError:true`), the session expired, auth failed, or mcpproxy refused the call. After this story each record carries `error_class`, `fault_domain` and, when known, `upstream_http_status`, in the REST API and CLI JSON output.

**Why this priority**: it is the foundation for every reliability decision (alerting, health, retries) and is cheap.

**Independent Test**: a pure classifier with table tests; an `httptest` upstream producing each failure; assert the persisted record and the REST/CLI JSON.

**Acceptance Scenarios**:

1. **Given** an upstream tool returns `isError:true`, **Then** the record has `error_class=tool_error`, `fault_domain=upstream` and no `upstream_http_status`.
2. **Given** the upstream returns HTTP 502 with an HTML body, **Then** `error_class=http`, `fault_domain=upstream`, `upstream_http_status=502`.
3. **Given** the upstream returns a JSON-RPC error (including a server-specific code such as `-32001`), **Then** `error_class=jsonrpc`.
4. **Given** the connection is refused or reset, **Then** `error_class=network`; a deadline exceeded is `timeout`.
5. **Given** mcpproxy refuses the call (profile policy, concurrency limiter shed, stale connection generation), **Then** `error_class=proxy_policy`, `fault_domain=proxy`; for pre-dispatch argument validation `fault_domain=client`.
6. **Given** an activity record written before this spec, **Then** it decodes unchanged and the new fields are absent in REST and CLI output.

### User Story 4 — A server that fails most calls is not shown as healthy (Priority: P2) — slice 113-d

An upstream stays connected but returns 5xx or times out on most tool calls. Today its health stays `healthy` because health only looks at connection, OAuth, refresh and quarantine state. After this story health turns `degraded` with a summary like "7 of 10 tool calls failed in the last 5 min", and recovers on its own when calls succeed again.

**Why this priority**: removes a misleading green light; depends on nothing else.

**Independent Test**: calculator table tests plus a rolling-window unit test with an injected clock.

**Acceptance Scenarios**:

1. **Given** ≥5 counted calls in the last 5 minutes of which >50% were transport/HTTP/JSON-RPC/timeout failures, **Then** `level=degraded` and `summary` names the failure count, total and window.
2. **Given** the failures are `isError:true` tool results or proxy policy refusals, **Then** health is unaffected.
3. **Given** 4 calls all failed, **Then** health is unaffected (minimum sample).
4. **Given** the server is quarantined, disabled, disconnected, in error, or needs OAuth login, **Then** that existing status wins over the failure-rate degradation.
5. **Given** the window slides past the failures, **Then** health returns to `healthy` with no user action.

### User Story 5 — An upstream session loss does not fail the next tool call (Priority: P2) — slice 113-e

A Streamable HTTP upstream restarts or expires its sessions. The next tool call gets HTTP 404 for the old `Mcp-Session-Id`. Today mcpproxy reports the call as failed and flips the server to `Error` (the substring "terminated" matches `managed.Client.isConnectionError`), triggering a full reconnect. After this story mcpproxy re-initializes the session in place and retries that one request once; the caller sees success.

**Why this priority**: common in practice (serverless MCP servers, deploys) and turns a user-visible failure into a non-event.

**Independent Test**: `httptest` Streamable HTTP server that forgets its session once; concurrent callers; assert one re-initialize, all calls succeed, server state stays `Ready`.

**Acceptance Scenarios**:

1. **Given** a legacy-protocol session with a known session id and a server that 404s it once, **When** a read-only tool is called, **Then** one `initialize` and one `tools/list` are sent, the call is retried once and succeeds, the server state is unchanged, and an Info log plus counter record the re-init.
1a. **Given** the same situation for a write or destructive tool, **Then** the session is re-initialized, the call is NOT repeated and fails with `error_class=session_terminated`, and the next call succeeds on the new session.
1b. **Given** the health `Ping` hits the 404 first, **When** a tool call follows, **Then** the call joins the re-init instead of being sent without a session id.
2. **Given** 10 concurrent read-only calls hit the same terminated session, **Then** exactly one `initialize` is sent and each of the 10 calls is retried once.
3. **Given** the retry also fails with 404 (or any error), **Then** the call fails and existing error handling applies.
4. **Given** the negotiated protocol is 2026-07-28 (no session id) or mcpproxy never obtained a session id, **Then** a 404 is not treated as session-terminated and no retry happens.
4a. **Given** the re-init's `tools/list` shows the called tool's identity hash changed, **Then** the call is not retried and the existing differential-update and quarantine path runs.
5. **Given** `tools/list` hits a terminated session, **Then** the same re-init-and-retry-once applies.

### User Story 6 — Explicit, safe cache hints on mcpproxy's own list results (Priority: P3) — slice 113-f

A modern (2026-07-28) MCP client or a caching intermediary reads mcpproxy's `tools/list`. The listing depends on the caller's profile, agent-token scope and session, so it must never be shared across callers or served stale after an approval or a quarantine. mcp-go v1.0.0 already stamps `ttlMs:0, cacheScope:"private"` by default on modern responses (`server/response.go`, fail-closed default); this story makes that an explicit, tested mcpproxy decision on every MCP server instance so a dependency bump cannot silently change it, and closes Spec 058 T063/T064.

**Independent Test**: a modern-protocol `tools/list` and `prompts/list` against each MCP endpoint; assert `ttlMs` and `cacheScope`.

**Acceptance Scenarios**:

1. **Given** a modern client, **When** it calls `tools/list` on `/mcp`, `/mcp/all`, `/mcp/code`, `/mcp/call` or `/mcp/p/<slug>`, **Then** the result carries `cacheScope:"private"` and `ttlMs:0`.
2. **Given** a legacy client, **Then** responses are byte-identical to today (no new fields).

### Edge Cases

- A refresh flight's initiating caller has its context cancelled: the flight continues on a detached context with its own 30 s timeout; other waiters still get the result; the cancelled caller returns its own `ctx.Err()`.
- A read-only token probe (`upstream.Manager` status checks at `manager.go:2279` and `:2682`, the `appctx` adapter) MUST never trigger a network refresh.
- The AS returns a 200 with an OAuth error body (GitHub style): mcp-go already surfaces it as a wrapped `transport.OAuthError`; classification uses that.
- A token without `expires_at`: never refreshed proactively (unchanged).
- `invalid_client` while a manual login is in progress: credentials are not cleared mid-flow (`OAuthFlowCoordinator.IsFlowActive`).
- Discovery cache: two servers with the same URL but different oauth overrides must not share a cache entry.
- Call classification when mcpproxy's own deadline fires before dispatch vs after: both `timeout`; `fault_domain` is `upstream` if the request reached the transport, else `proxy`.
- Failure-rate window for a server that is removed or renamed: its window is dropped.
- Session re-init on an OAuth server whose token expires at the same moment: the re-init request goes through the normal auth header path, so US1 serialization applies.
- A modern-protocol upstream that 404s because its URL moved: not retried (US5 scenario 4).

## Requirements *(mandatory)*

### Functional Requirements

#### US1 / 113-a — OAuth refresh robustness (G1, G2)

- **FR-001**: All refresh flights for one server key MUST be serialized process-wide by a single-flight coordinator in `internal/oauth`. It MUST cover the reactive mcp-go path, `RefreshOAuthTokenDirect` via `handler.RefreshToken`, and the manual `refreshTokenWithStoredCredentials` path.
- **FR-002**: The reactive path MUST be taken over by the token store mcp-go is given. When the store has a refresher bound (bound only to the store `createOAuthConfigInternal` hands to mcp-go, after the handler exists), `GetToken` (a) refreshes through the coordinator whenever the persisted token is within its refresh margin (the existing grace-period rule, or for tokens shorter than the grace period, less than half their lifetime or 30 s left) and has a refresh token, and (b) returns the token to mcp-go with `ExpiresAt` zeroed. mcp-go's `Token.IsExpired` treats a zero `ExpiresAt` as never expiring, so `getValidToken` can never reach its own unlocked `refreshToken`, even if a returned token expires between `GetToken` and the `IsExpired` check. Expiry is owned by mcpproxy (the persisted record keeps the real `expires_at`; a 401 from the upstream still triggers the existing auth-required path). Store instances without a bound refresher keep today's read-only behaviour.
- **FR-003**: Inside a flight the coordinator MUST re-read the persisted record first. If its refresh token differs from the one the caller observed (a fingerprint, never the value in logs), it MUST return that record without a network request. Both triggers MUST use the same refresh function, which keeps today's two sub-paths: `handler.RefreshToken` when the live handler has a client id, otherwise the stored-DCR-credentials request (`refreshTokenWithStoredCredentials`). A reactive refresh MUST NOT send an empty `client_id`.
- **FR-004**: All waiters of one flight MUST receive the same result, success or error. A flight MUST run on a context detached from any single caller (`context.WithoutCancel` + 30 s timeout). The coordinator MUST keep per-key outcome state so later callers do not hammer the AS: after a terminal failure it latches and every caller gets the terminal error with no network request until a new token or new client registration is saved (login); after a transient failure it applies a cooldown equal to the RefreshManager's current backoff (minimum 10 s) during which callers get `ErrTokenRefreshTransient` with no network request.
- **FR-005**: On a terminal failure inside `GetToken`, the store MUST return an error that wraps `transport.ErrOAuthAuthorizationRequired` (so mcp-go's `sendHTTP` raises `OAuthAuthorizationRequiredError` and the existing login surfaces fire). On a transient failure it MUST return a distinct wrapped sentinel `oauth.ErrTokenRefreshTransient`, not the expired token, so mcp-go does not start a second unlocked refresh.
- **FR-006**: `PersistentTokenStore.SaveToken` and the manual refresh path MUST persist through one BBolt read-modify-write transaction (a new `storage.BoltDB` method) that preserves `ClientID`, `ClientSecret`, `CallbackPort`, `RedirectURI`, `Created` and `DisplayName` from the record as it exists inside that transaction. The manual path MUST NOT write back a whole record it read before the network call.
- **FR-006a**: Every flight MUST carry the generation it started from (fingerprint of the refresh token it sends plus the client id). Its persist MUST be a compare-and-swap inside the FR-006 transaction: if the stored refresh token or client id no longer match that generation (a login or another writer superseded it), the flight's result is discarded and the stored token is returned instead. Its terminal side effects (latch, schedule failure, DCR clearing, events) MUST likewise apply only while the stored generation still matches; a stale completion never re-latches or fails a newer grant.
- **FR-007**: Refresh errors MUST be classified structurally: `var oe transport.OAuthError; errors.As(err, &oe)` (value target: mcp-go defines `Error()` on the value type and wraps the value with `%w`, both for non-2xx JSON bodies via `extractOAuthError` and for 200-with-error bodies) for the RFC 6749 §5.2 `error` code; the HTTP status from a typed error the manual path returns, or parsed from mcp-go's fixed `"refresh token request failed with status %d: "` prefix for non-JSON bodies (for JSON OAuth error bodies on the handler path the status is not recoverable and classification uses the code); `net.Error`, `context.DeadlineExceeded` and `syscall` errors for network. Substring matching MAY remain only as the last fallback for non-compliant bodies, with each pattern covered by a test.
- **FR-008**: Classes and handling: `invalid_grant` → terminal, re-login; `invalid_client` → FR-009; `unauthorized_client`, `unsupported_grant_type`, `invalid_scope` → terminal with the code in the message; OAuth codes `server_error` and `temporarily_unavailable`, HTTP 5xx, 429, network, timeout → transient (existing backoff); anything else → `failed_other` (existing backoff, then existing give-up). The `server not found` / `server does not use OAuth` terminal class stays. Terminal side effects (schedule state, DCR clearing, events) MUST run once per flight in a coordinator completion hook, whichever trigger started the flight, and the RefreshManager is notified through that hook. Metrics labels keep the existing values and add `failed_invalid_client`, `failed_server_error`. A transient failure where the request may have reached the AS (response lost) is retried like any transient failure: if the AS had rotated the token, the old token is already unusable and the retry's `invalid_grant` leads to re-login, which is no worse than not retrying.
- **FR-009**: `invalid_client` with a DCR client MUST clear the stored DCR registration unless a login flow is active, and only if the stored `ClientID` still equals the client id the failed request used (compare-and-clear in one BBolt transaction, so a stale flight cannot clear a registration a concurrent login just saved). It then sets the schedule to failed with "client registration rejected by the authorization server; sign in again to re-register", and allows exactly one re-registration via the normal login flow. A second `invalid_client` for a client registered after such a clear, before any successful token response, from either refresh or the authorization-code exchange of the login flow, MUST be terminal with a message telling the user the AS rejects new registrations. With a static client it MUST be terminal at once and MUST NOT touch credentials.
- **FR-010**: `RefreshManager` MUST read token records by server key, not display name (fixes `refresh_manager.go:363` and `:666`, which call `storage.GetOAuthToken(serverName)` and so never find keyed records; the refreshed event is never emitted today).
- **FR-011**: Every flight MUST log one Info line (server, trigger `reactive|proactive`, outcome class, whether the network call was skipped by FR-003); never token values.
- **FR-012**: Concurrency tests MUST be deterministic (barrier-started goroutines, `httptest` AS counting requests) and pass under `-race -count=20`.
- **FR-013**: No new config fields; no change to `DefaultMaxRetries`, backoff constants or `TokenRefreshGracePeriod`.
- **FR-014**: The CLI in-memory store path (`globalTokenStoreManager`, used when no storage exists) MUST use the same coordinator, keyed by server name.

#### US2 / 113-b — OAuth discovery, offline_access and endpoint overrides (G3, G5)

- **FR-020**: When `oauth.scopes` is not set and the scopes were taken from PRM `scopes_supported` (waterfall priority 2 in `createOAuthConfigInternal`), and the AS metadata `scopes_supported` contains `offline_access`, mcpproxy MUST add `offline_access` once. When the scopes came from AS metadata (priority 3) they already include it if advertised. It MUST NOT be added when `oauth.scopes` is set or when the list is empty because nothing was advertised.
- **FR-021**: When AS metadata contains `grant_types_supported` and it lacks `refresh_token`, mcpproxy MUST log one Warn per server per process. Absence of the field is not a warning.
- **FR-022**: `config.OAuthConfig` MUST gain optional `authorization_endpoint`, `token_endpoint`, `registration_endpoint` and `auth_server_metadata_url` (`json` snake_case, `omitempty`, matching `mapstructure` tags).
- **FR-023**: Each override MUST be an absolute `https` URL, or `http` on a loopback host (`localhost`, `127.0.0.0/8`, `::1`), with a host and no fragment, userinfo or query string (mcp-go `GetAuthorizationURL` appends `?` literally and cannot merge an existing query; extra authorize parameters belong in `extra_params`). Validation MUST run only at write time (`ValidateDetailed`, REST create/PATCH, MCP `upstream_servers`, CLI); at load an invalid override is dropped with a name-only Warn and boot continues (never in `validateDetailedCore`).
- **FR-024**: `auth_server_metadata_url` replaces discovery and is passed to mcp-go `OAuthConfig.AuthServerMetadataURL`. mcp-go's `OAuthConfig` has no endpoint fields, so endpoint overrides MUST take effect by rewriting the matching fields of the AS metadata JSON response inside `OAuthTransportWrapper` (mcp-go fetches metadata through `OAuthConfig.HTTPClient`, which is the wrapper). Whenever any endpoint override is set, mcpproxy MUST also set `OAuthConfig.AuthServerMetadataURL` (the override, else the preflight-found URL, else the RFC 8414 URL derived from the authorization endpoint's origin) so mcp-go never falls back to its own auto-discovery or its in-memory `/authorize`, `/token`, `/register` defaults (`getDefaultEndpoints`). The rewritten document MUST still pass mcp-go `validateAuthServerMetadataURLs`. When that metadata URL is unreachable or non-200 and the overrides cover `authorization_endpoint` and `token_endpoint`, the wrapper MUST answer it with a synthetic document built from the overrides (issuer = authorization endpoint origin). With an explicit metadata URL mcp-go skips PRM and leaves its RFC 8707 `resourceURL` empty; mcpproxy's auto-detected `resource` keeps flowing through `extra_params` exactly as today (authorize URL and token requests); DCR request bodies are unchanged in this spec.
- **FR-025**: `extra_params` injection and the 201→200 normalization MUST match on the effective token endpoint URL (scheme, host, path; overridden or discovered) when known, and fall back to the existing `/token` / `/authorize` path heuristic only when no endpoint is known. Note: the browser authorization URL never passes through this wrapper; authorize-time params are appended in `core.Client.handleOAuthAuthorization`, unchanged.
- **FR-026**: Discovery results MUST be cached per key = hash(server URL, the four override fields): the preflight calls (`DiscoverAuthServerURL`, `FindWorkingMetadataURL`, `DiscoverScopesFromProtectedResource`, `DiscoverScopesFromAuthorizationServer`), mcpproxy's resource auto-detection PRM fetch, and the AS metadata GET each new mcp-go handler issues (served by the wrapper from the cache). Success TTL 1 h, failure TTL 30 s, single-flight per key, bounded size (256 entries, LRU). A 401 that advertises a different `resource_metadata` URL than the cached one MUST invalidate the entry.
- **FR-027**: A change to the server URL or any override field yields a new cache key, so no hot-reload hook is needed; other oauth fields (scopes, client id, `extra_params`) do not affect discovery and keep the entry. The old key ages out.
- **FR-027a**: The OAuth HTTP client (`OAuthTransportWrapper`'s `http.Client`) MUST refuse a redirect that changes scheme from https to http on a non-loopback host, and MUST refuse any redirect of a token or registration POST to a different origin, so credentials (code, refresh token, client secret) are never re-sent in clear text or to another host.
- **FR-028**: The config-field checklist MUST be applied: `config.OAuthConfigChanged`, `MergeOAuthConfig`/`copyOAuthConfig` (`internal/config/merge.go`); `ServerFieldMaskDecisions` rows (`NotSecret`) in `internal/oauth/serverfields.go`; the OAuth projection in `contracts` + `cmd/generate-types` + regenerated `frontend/src/types/contracts.ts`; `make swagger`; `docs/configuration.md` OAuth section. These are per-server fields: no `MCPPROXY_` env override, and `DetectConfigChanges` already covers server edits.
- **FR-029**: The new fields MUST round-trip through storage (`UpstreamRecord.OAuth` is `*config.OAuthConfig`) with a round-trip test.
- **FR-030**: Logs MUST use `logSafeURL` for every override and discovered URL.
- **FR-031**: A test MUST prove the browser authorization URL host equals the `authorization_endpoint` override (mcp-go `GetAuthorizationURL` reads the rewritten metadata).
- **FR-032**: Servers without overrides see no behaviour change other than FR-020, FR-021 and caching.
- **FR-033**: Slice 113-b MUST NOT edit `internal/oauth/refresh_manager.go` or `internal/oauth/persistent_token_store.go`.

#### US3 / 113-c — Call-error taxonomy (G6)

- **FR-040**: A pure classifier (new package `internal/callerr`, no I/O) MUST map `(result *mcp.CallToolResult, err error, facts)` to `error_class` ∈ {`network`, `timeout`, `http`, `jsonrpc`, `tool_error`, `session_terminated`, `auth`, `proxy_policy`, `proxy_internal`, `cancelled`} and `fault_domain` ∈ {`upstream`, `proxy`, `client`}, plus `upstream_http_status` when known. `cancelled` is added to the audit's nine-value list so caller cancellation is not mislabelled as an upstream fault.
- **FR-041**: Rules, first match wins: nil error and `result.IsError` → `tool_error/upstream`; `transport.ErrSessionTerminated` → `session_terminated/upstream`; mcp-go `OAuthAuthorizationRequiredError`, `AuthorizationRequiredError`, `ErrUnauthorized`, or recorded HTTP 401/403 → `auth/upstream`; profile refusals, `limiter.LimitError`, `managed.ErrConnectionGenerationChanged`, quarantine/approval refusals → `proxy_policy/proxy`; Spec 085 pre-dispatch argument validation and `jsonschema.ValidationError` → `proxy_policy/client`; `audit.ErrSanitisationFailed` → `proxy_internal/proxy` with the audit class kept as `sanitisation`; caller `context.Canceled` → `cancelled/client`; `context.DeadlineExceeded` or `net.Error.Timeout()` → `timeout` (`upstream` if dispatched, else `proxy`); dial/reset/EOF/`syscall` errors, mcp-go `*transport.Error`, `transport.ErrTransportClosed` → `network/upstream`; a recorded non-2xx HTTP status → `http/upstream`; a JSON-RPC error response → `jsonrpc/upstream`, recognised as mcp-go sentinels from `mcp.JSONRPCErrorDetails.AsError`, or any other error returned after the response was received (HTTP: recorder saw a 2xx; stdio/SSE: the error is not one of the transport error types above, since those transports surface every received JSON-RPC error through `AsError`); anything else → `proxy_internal/proxy`. `Dispatched` MUST be set by `core.Client` when it hands the request to mcp-go (a ctx flag), for every transport, not inferred from the HTTP recorder.
- **FR-042**: `upstream_http_status` MUST come from a per-call, context-scoped recorder filled by a `RoundTripper` layer in `internal/transport` (status of the response to that call's POST). Reason: mcp-go returns non-2xx non-JSON responses as an untyped `fmt.Errorf("request failed with status %d: ...")` and unmapped JSON-RPC codes as `errors.New(message)`. The existing `transport.HTTPError` / `transport.JSONRPCError` have no production constructor callers today, so `audit.ErrorClassOf` branches on them never fire; they MUST NOT be relied on.
- **FR-043**: `storage.ActivityRecord` MUST gain `ErrorClass string` (`json:"error_class,omitempty"`), `FaultDomain string` (`json:"fault_domain,omitempty"`), `UpstreamHTTPStatus int` (`json:"upstream_http_status,omitempty"`). Old records decode unchanged; success records carry none of them.
- **FR-044**: Every tool-call activity write path (direct surface, `call_tool_*`, `code_execution` sub-calls, REST `/api/v1/tools/call`) MUST stamp the three fields from the same classifier.
- **FR-045**: `contracts.ActivityRecord` (REST), the SSE activity payload, `cmd/generate-types` and `mcpproxy activity list|show -o json|yaml` MUST expose them; `activity show` table output adds an "Error class" line when present. `make swagger` MUST be run and `oas/` committed.
- **FR-046**: `ActivityFilter` MUST accept `error_class` and `fault_domain` (REST query params; CLI `--error-class`, `--fault-domain`).
- **FR-047**: The Spec 107 audit line keeps its frozen `error_class` vocabulary; the new classifier MUST provide a total mapping to it (`network`→`upstream_unavailable`; `timeout`→`upstream_timeout`; `http` 502/503/504→`upstream_unavailable`, other `http`→`upstream_error`; `jsonrpc`, `tool_error`, `session_terminated`, `auth`→`upstream_error`; `proxy_policy` with `fault_domain=client`→`validation`; sanitisation failures→`sanitisation`; `cancelled`→`cancelled`; others→`internal`), used by `internal/server/audit_funnel.go` so the two surfaces agree. Every value `audit.ErrorClassOf` produces today for a production error MUST map to the same audit class. Table-tested.
- **FR-048**: `isError` tool results MUST keep their current `status` value; they are distinguished only via `error_class=tool_error`.
- **FR-049**: The classifier MUST NOT read error message text except a documented last-resort fallback list covered by tests.
- **FR-050**: Slice 113-c MUST NOT edit `internal/health/**` or `internal/upstream/managed/client.go`.
- **FR-051**: No change to status values, retention, or existing filters.

#### US4 / 113-d — Health from call-failure rate (G7)

- **FR-060**: A per-server rolling window (5 min, constant) of call outcomes MUST be kept in memory (bucketed counters, e.g. 30 × 10 s buckets; O(1) per call).
- **FR-061**: Counting is a denylist over typed proxy-side outcomes, so it needs no HTTP status: excluded from both numerator and denominator are `isError:true` results, profile/quarantine refusals, `limiter.LimitError` (covers admission waits and their deadlines), `managed.ErrConnectionGenerationChanged`, argument validation, caller `context.Canceled`, and auth-required errors; every other error returned by the dispatch (network, timeout after admission, HTTP, JSON-RPC, session-terminated that survived any retry, untyped mcp-go errors) is a counted failure; successes count in the denominator. If 113-c has merged when 113-d is implemented, the predicate MUST be expressed over its classifier instead.
- **FR-062**: Outcomes MUST be recorded by one helper invoked from both dispatch choke points: `upstream.Manager.callTool` (`internal/upstream/manager.go`; covers the direct surface, `call_tool_*` and REST) and the `code_execution` dispatch in `internal/server/mcp_code_execution.go` (which calls `GetClient(...).CallToolOnEpoch` directly and bypasses the manager). The registry lifecycle (drop on server removal, rename or re-add) is hooked in `upstream.Manager`'s remove-server path. Not in `internal/upstream/managed/client.go`; not derived from `ActivityRecord`.
- **FR-063**: `health.HealthCalculatorInput` MUST gain `CallsInWindow` and `CallFailuresInWindow`. All three construction sites (`internal/runtime/runtime.go`, `internal/server/server.go`, `internal/server/mcp.go`) MUST fill them from the same source.
- **FR-064**: Rule: wherever the calculator would return `healthy` (including the early healthy return for an OAuth token inside the expiry-warning window, and any other healthy return), and `CallsInWindow ≥ 5` and `CallFailuresInWindow / CallsInWindow > 0.5`, the result is `degraded` with `summary` "N of M tool calls failed in the last 5 min" and `detail` naming the dominant failure kind; `action` uses the existing vocabulary (`view_logs` if present, else empty). Implement as one wrapper applied to every healthy result. Every existing non-healthy outcome takes precedence.
- **FR-065**: Thresholds MUST be Go constants (`CallFailureWindow`, `CallFailureMinSamples`, `CallFailureRatio`); no config field.
- **FR-066**: The window MUST be dropped when a server is removed or renamed, and is not persisted.
- **FR-067**: Calculator precedence MUST be covered by table tests (quarantined, disabled, disconnected, error, OAuth expired, refresh failed, missing secret each beat the failure rate) and boundary tests (4/4 failures, 5 calls at exactly 50%, 5 calls at 60%).
- **FR-068**: When the failure rate changes a server's computed health level, the existing servers-changed SSE event MUST fire (debounced, at most once per 10 s per server).

#### US5 / 113-e — Session-terminated re-init and retry (G8)

- **FR-080**: mcpproxy MUST remember the last session id it obtained from a successful legacy-protocol `initialize` ("known session"). When any Streamable HTTP request made through `managed.Client` — `tools/call`, `tools/list` (leader and the cached-tool-count path), `prompts/list`, `prompts/get`, and the health `Ping` — returns `errors.Is(err, transport.ErrSessionTerminated)` while a known session exists and the negotiated protocol is pre-2026-07-28, or is about to be sent while the transport's session id is empty but a known session exists (another request already hit the 404 and mcp-go cleared it), mcpproxy MUST re-initialize the MCP session on the same transport before sending (or retrying) the request. List, get and ping requests are retried exactly once after a re-init.
- **FR-081**: A `tools/call` is retried once after the re-init only when (a) the tool is read-only for mcpproxy (Spec 018 operation type `read`, i.e. reachable via `call_tool_read`, or annotated `readOnlyHint:true`), because mcp-go maps every HTTP 404 to `ErrSessionTerminated` and a non-conforming server or intermediary could return 404 after executing; and (b) the synchronous `tools/list` that follows the re-init shows the called tool with the same identity/approval hash the caller was certified against (Spec 105/032). Otherwise the call fails with `error_class=session_terminated` and a message saying the session was re-established and the call was not repeated; the next call uses the new session.
- **FR-082**: Concurrent requests that observe the same known session as terminated MUST share one re-initialize (single-flight keyed by server + known session id); requests issued while a re-init flight is running wait for it and then use the new session; requests that find a newer known session in place proceed without re-initializing.
- **FR-083**: A re-init whose synchronous `tools/list` shows the toolset unchanged MUST NOT bump `connectionEpoch`, so Spec 105 pinned calls stay valid. If any tool identity changed, the existing `applyDifferentialToolUpdate` path runs (with its normal epoch and quarantine semantics) and pinned calls on the old generation are refused as today.
- **FR-083a**: Every request that waited on a re-init flight (FR-082) MUST, after the flight completes and before sending, re-run the same generation checks a fresh call would: `connectionEpoch` for pinned calls and, for `tools/call`, the called tool's identity hash against the re-listed toolset; a mismatch returns the existing `ErrConnectionGenerationChanged` refusal. A re-init MUST also restore the standalone GET notification listener (mcp-go's `listenForever` exits on a 404 and `Start` does not restart it once initialized) so `tools/list_changed` keeps arriving; if that cannot be done on the existing transport, the re-init MUST instead take the existing full reconnect path (today's behaviour) and the request is not retried.
- **FR-084**: A session-terminated error MUST NOT reach `isConnectionError` / `setErrorIfCurrentConnection` unless the retry also fails or the re-initialize fails; then the existing path applies to the final error.
- **FR-085**: Each re-init MUST be logged at Info (server, method, outcome; session ids only as 8-char prefixes) and counted in an in-memory per-server counter exposed in the server's diagnostics JSON (and as a Prometheus counter if the observability manager has a seam for it).
- **FR-086**: No retry for any other status or error, and none for write or destructive tools (FR-081). The retry relies on the Streamable HTTP rule that a server rejects an unknown session id with 404 before executing the method.
- **FR-087**: Stdio and SSE transports are unchanged.
- **FR-088**: The exact mcp-go v1.0.0 behaviour (`streamable_http.go` `sendHTTP` 404 branch; `SendRequest` mapping a 404 on `initialize` to `ErrLegacySSEServer`; `client.Client.Initialize` being callable again on the same transport) MUST be verified by a test before implementation; any divergence is recorded in the PR body.

#### US6 / 113-f — Cache hints (G9)

- **FR-090**: Every mcp-go `MCPServer` mcpproxy constructs for clients (`internal/server/mcp.go` main server; `internal/server/mcp_routing.go` `directServer`, `codeExecServer`, `callToolServer`) MUST be built with an explicit `mcpserver.WithCacheHints(0, mcp.CacheScopePrivate)`.
- **FR-091**: `public` MUST NOT be used: every mcpproxy listing depends on profile, agent-token scope, quarantine/approval state or session. TTL stays 0 because a quarantined or revoked tool must disappear from the next list immediately.
- **FR-092**: Tests MUST assert `ttlMs:0` and `cacheScope:"private"` on modern `tools/list` and `prompts/list` for each endpoint, and that legacy responses carry neither field.
- **FR-093**: The slice MAY tick T063 and T064 in `specs/058-mcp-2026-upgrade/tasks.md` (the only spec file any slice may touch).
- **FR-094**: The tests assert on the wire, so a dependency bump that drops or changes the hints fails CI.

### Key Entities

- **RefreshFlight** (113-a): server key, observed refresh-token fingerprint, result token, error, done channel.
- **RefreshErrorClass** (113-a): `invalid_grant | invalid_client | unauthorized_client | unsupported_grant_type | invalid_scope | server_error | rate_limited | network | timeout | server_gone | other`, plus HTTP status.
- **OAuthConfig override fields** (113-b): four optional URLs.
- **DiscoveryCacheEntry** (113-b): key, PRM, AS metadata, working metadata URL, expiry, error.
- **CallOutcomeClass** (113-c): `error_class`, `fault_domain`, `upstream_http_status`.
- **CallFailureWindow** (113-d): per-server ring of buckets {calls, failures, per-kind counts}.
- **SessionReinitFlight** (113-e): server, old session id, result.

## Success Criteria *(mandatory)*

- **SC-001**: With 50 concurrent callers on an expired token against a rotating AS, the AS sees exactly 1 refresh request in 20/20 runs under `-race` (113-a).
- **SC-002**: A dead DCR client causes 0 automatic refresh retries after the `invalid_client` response (today up to 50) and at most one re-registration (113-a).
- **SC-003**: A server reconnecting 5 times in an hour issues preflight discovery requests for 1 connect, not 5 (113-b).
- **SC-004**: For each of the 10 error classes a test produces a real condition and the persisted record and REST JSON carry the expected class (113-c).
- **SC-005**: A connected server failing 6 of 10 calls is `degraded` at the next health computation and `healthy` again once the failures leave the 5 min window (113-d).
- **SC-006**: A single upstream session loss produces 0 failed tool calls and 0 transitions to `Error` (113-e).
- **SC-007**: Modern `tools/list` on every endpoint carries `cacheScope:"private"`, `ttlMs:0` (113-f).
- **SC-008**: Every slice passes `go test -race` on affected packages, the server-tag test command with the CI skip regex, both golangci-lint v2 runs, and `./scripts/test-api-e2e.sh` against an isolated instance.

## Out of Scope / Deferred

- **G4 CIMD** (OAuth Client ID Metadata Documents): needs a publicly hosted https client metadata document (e.g. on mcpproxy.app) — a product decision. Design sketch in research.md R-G4; T250–T254 are design placeholders.
- **G10 server-edition per-user upstream credentials**: confirmed not applied to upstream calls; this is the deliberate Spec 107 FR-034 freeze. Design sketch in research.md R-G10; T255–T259 are placeholders.
- Requesting all AS `scopes_supported` when PRM advertises none (waterfall priority 3) is over-broad but unchanged here.

## Commit Message Conventions *(mandatory)*

### Issue References
- Use `Related #[issue-number]`. Do NOT use `Fixes` / `Closes` / `Resolves`.

### Co-Authorship
- Do NOT include `Co-Authored-By: Claude` trailers or "Generated with Claude Code" lines (maintainer rule for this repo).

### Example Commit Message
```
fix(oauth): serialize token refresh per server (Spec 113-a)

Related #<issue>

## Changes
- single-flight refresh coordinator keyed by server key
- structured RFC 6749 section 5.2 refresh error classification

## Testing
- 50-goroutine rotating-AS test, -race -count=20
```
