# Implementation Plan: Upstream resilience (Spec 113)

**Branch**: `113-upstream-resilience` (spec only) | **Date**: 2026-10-05 | **Spec**: [spec.md](spec.md) | **Research**: [research.md](research.md)

## Summary

Six independent PR slices close the verified gaps G1–G3 and G5–G9 from the Anthropic MCP-proxy talk audit. 113-a serializes OAuth refresh per server across all three refresh paths and classifies refresh errors structurally. 113-b adds `offline_access`, four per-server OAuth endpoint overrides and a preflight discovery cache. 113-c classifies every tool-call outcome (`error_class`, `fault_domain`, `upstream_http_status`) onto the activity record. 113-d degrades health on a high rolling call-failure rate. 113-e re-initializes a terminated Streamable HTTP session and retries once. 113-f makes mcp-go's safe default cache hints explicit and tested. G4 (CIMD) and G10 (server-edition per-user credentials) are design-only.

## Technical Context

- **Language**: Go 1.26 backend; generated TS types only where REST shapes change (113-b, 113-c). No Web UI or Swift work in this spec.
- **Dependencies**: existing only (mcp-go v1.0.0, bbolt, zap, `golang.org/x/sync` already in `go.mod`). **No new modules.**
- **Storage**: 113-a adds one BBolt method (no schema change); 113-c adds three `omitempty` fields to `ActivityRecord`; 113-b adds four `omitempty` fields to `config.OAuthConfig` (persisted inside `UpstreamRecord.OAuth`).
- **Testing**: table tests for pure functions; `httptest` authorization servers and Streamable HTTP upstreams; `-race`; server-tag test command with the CI skip regex; `./scripts/test-api-e2e.sh` against an isolated instance (high port, scratch HOME/data dir).
- **Constraints**: slices never stack and never edit `specs/113-upstream-resilience/**`; file ownership below; both editions build.

## Constitution Check

- Security by default: yes. Refresh serialization reduces grant revocation; overrides are https-or-loopback and write-time validated; cache hints stay `private`.
- No new dependencies: yes.
- Both editions: no build-tagged code is changed; every slice runs the server-tag build and lint.
- Tests first: every implementation task is preceded by its failing test task.
- Backward compatibility: new JSON fields are `omitempty`; old activity records and configs load unchanged.

## File ownership

A file listed for one slice MUST NOT be edited by another slice. Shared files are listed with the allowed scope.

| Slice | Owns (may edit) | Must not edit |
|---|---|---|
| 113-a | `internal/oauth/refresh_manager.go`, `internal/oauth/persistent_token_store.go`, new `internal/oauth/refresh_coordinator.go`, new `internal/oauth/refresh_errors.go`, `internal/oauth/config.go` (**only** the lines that attach the refresher to the store after the handler exists, and `globalTokenStoreManager`), `internal/upstream/core/client.go` (**only** `RefreshOAuthTokenDirect`, `refreshTokenWithStoredCredentials`), `internal/upstream/manager.go` (**only** `RefreshOAuthToken`), `internal/upstream/core/connection_oauth.go` (**only** one call binding the live handler to the store after the mcp-go client is created), `internal/storage/bbolt.go` (**only** a new token-update method) | `transport_wrapper.go`, `discovery.go` |
| 113-b | `internal/oauth/transport_wrapper.go`, `internal/oauth/discovery.go`, new `internal/oauth/discovery_cache.go`, `internal/oauth/config.go` (scope waterfall, metadata URL selection, wrapper construction — **not** the refresher attach lines), `internal/oauth/serverfields.go`, `internal/config/config.go` (`OAuthConfig`, `OAuthConfigChanged`), `internal/config/merge.go`, `internal/config/oauth_validation.go`, contracts/generate-types OAuth projection, `docs/configuration.md` OAuth section, `oas/`, `internal/upstream/core/connection.go` (**only** one discovery-cache invalidation call in the 401 PRM-URL handling) | `refresh_manager.go`, `persistent_token_store.go` |
| 113-c | new `internal/callerr/`, `internal/transport/` (new recorder round-tripper + its install in `CreateHTTPClient`), `internal/storage/activity_models.go`, `internal/contracts/activity.go`, `internal/httpapi/activity.go`, `internal/runtime/activity_service.go`, `internal/runtime/event_bus.go` (activity payload only), activity write sites in `internal/server/mcp.go`, `mcp_routing.go`, `mcp_code_execution.go`, `server.go`, `internal/server/audit_funnel.go`, `internal/audit/error_class.go` (mapping only), `cmd/mcpproxy/activity_cmd.go`, `cmd/generate-types`, `oas/` | `internal/health/**`, `internal/upstream/managed/client.go` |
| 113-d | new `internal/upstream/callstats/` (window + predicate), `internal/upstream/manager.go` (**only** the `callTool` recording hook and the remove-server window drop, distinct hunks from 113-a's `RefreshOAuthToken`), `internal/server/mcp_code_execution.go` (**only** the one recording call after `CallToolOnEpoch`), `internal/health/calculator.go` (+ tests), the three `HealthCalculatorInput` construction sites | `internal/upstream/managed/client.go`, `ActivityRecord` |
| 113-e | `internal/upstream/managed/client.go` (incl. health ping and cached-tool-count paths), `internal/upstream/managed/prompts.go`, new `internal/upstream/core/session_reinit.go` (core helpers: pre-call session id, protocol era, re-initialize) | `internal/upstream/core/client.go` refresh functions |
| 113-f | `internal/server/mcp.go` (**only** the `NewMCPServer` option list), `internal/server/mcp_routing.go` (**only** the three `NewMCPServer` option lists), new `internal/server/cache_hints_test.go`, `specs/058-mcp-2026-upgrade/tasks.md` (tick T063/T064) | everything else |

Expected textual overlaps (resolved by rebasing on whichever merged first, no semantic conflict): `internal/server/mcp_code_execution.go` (113-c activity stamp vs 113-d recording call), `internal/upstream/manager.go` (113-a vs 113-d, different functions), `internal/server/mcp.go` (113-c write sites vs 113-f options), `oas/` (113-b vs 113-c, regenerate with `make swagger` after rebase).

## Design per slice

### 113-a — oauth-refresh-robustness

```go
// internal/oauth/refresh_coordinator.go
type RefreshTrigger string // "reactive" | "proactive"
type RefreshFunc func(ctx context.Context, current *storage.OAuthTokenRecord) (*client.Token, error)

// Do runs at most one refresh flight per key. observedRT is the refresh token
// the caller read (compared, never logged). Inside the flight: re-read the
// record; if its RefreshToken != observedRT, return it with skipped=true.
// The flight runs on context.WithoutCancel(ctx) with a 30 s timeout. Waiters
// get the same (*client.Token, error). Callers whose own ctx ends return ctx.Err().
func (c *RefreshCoordinator) Do(ctx context.Context, key string, observedRT string,
    trigger RefreshTrigger, load func() (*storage.OAuthTokenRecord, error), fn RefreshFunc) (*client.Token, bool, error)

var ErrTokenRefreshTransient = errors.New("oauth: token refresh failed transiently")

// internal/oauth/refresh_errors.go
type RefreshErrorClass string
func ClassifyRefreshError(err error) (cls RefreshErrorClass, httpStatus int)
// replaces classifyRefreshError's body; the old metric labels are derived from cls.
```

- `PersistentTokenStore` gains `SetRefresher(fn RefreshFunc)` (unexported field, atomic). `GetToken`: unchanged read; if refresher set: refresh via `coordinator.Do(..., "reactive")` when within the refresh margin (FR-002), then return the token with `ExpiresAt` zeroed so mcp-go's `IsExpired` is always false; terminal → `fmt.Errorf("%w: %v", transport.ErrOAuthAuthorizationRequired, cls)`; transient → `ErrTokenRefreshTransient`. The coordinator holds per-key `{terminalLatched bool; cooldownUntil time.Time}` cleared by `SaveToken`/`UpdateOAuthClientCredentials` of a new token or registration (FR-004).
- One refresh function (in `core`, bound into the store and used by `RefreshOAuthTokenDirect`) selects the sub-path: `handler.RefreshToken` if the handler has a client id, else stored DCR credentials (FR-003).
- The refresher for the reactive path performs the token request itself through the handler's HTTP client (so `extra_params` and 201 normalization apply) — implemented as a call to `handler.RefreshToken(ctx, rt)`; `handler.RefreshToken` calls `SaveToken` on the same store, which does NOT recurse into refresh. Careful: `SaveToken` must not take the coordinator (it runs inside the flight).
- `createOAuthConfigInternal` builds the store before mcp-go builds the handler (the handler lives inside the transport created by `internal/transport/http.go` from `OAuthConfig`). So the store gets a refresher that resolves the handler lazily through an `atomic.Pointer[transport.OAuthHandler]` slot; `core` binds the slot right after the mcp-go client is created, using the existing `extractOAuthHandler`/`getOAuthHandlerLocked` (`internal/upstream/core/client.go:705-735`): `oauth.BindRefreshHandler(tokenStore, handler)`. Until bound (or for stores never handed to mcp-go), `GetToken` behaves as today. Reconnects create a new handler; binding again replaces the pointer.
- `RefreshOAuthTokenDirect` → `coordinator.Do(..., "proactive")` for both sub-paths; manual path returns a typed `*RefreshHTTPError{Status, OAuthCode}` and persists via the new storage method.
- `storage.BoltDB.UpdateOAuthToken(serverKey string, mutate func(rec *OAuthTokenRecord) error) error` — single `db.Update`; creates the record if absent.
- Terminal handling runs in a coordinator completion hook (once per flight, either trigger) which updates the RefreshManager schedule and emits events; per-key state `{reregisteredAt time.Time, succeededSince bool}` implements the one-re-registration rule; the login flow's code exchange reports `invalid_client` into the same state. DCR clearing uses a new compare-and-clear storage method (`ClearOAuthClientCredentialsIf(serverKey, expectedClientID)`), only for DCR (`ServerConfig.OAuth == nil || ClientID == ""`), only when `!flowCoordinator.IsFlowActive(serverName)`.

### 113-b — oauth-discovery-overrides

- `config.OAuthConfig` + `AuthorizationEndpoint`, `TokenEndpoint`, `RegistrationEndpoint`, `AuthServerMetadataURL` (`json:"authorization_endpoint,omitempty" mapstructure:"authorization_endpoint"`, …).
- `ValidateOAuthEndpointOverrides(o *OAuthConfig) []FieldError` in `oauth_validation.go`, called from `ValidateDetailed` and the REST/MCP write paths; load-time sanitize drops invalid values with a Warn.
- `OAuthTransportWrapper` gains `endpoints struct{ token, authz, registration *url.URL; metadataURL string; overrides map[string]string }`. `RoundTrip`: (1) GET of the metadata URL → read body (limit 1 MiB), decode into `map[string]any`, overwrite override keys, re-encode, replace body and `Content-Length`; if the upstream fetch fails or is non-200 and overrides cover `authorization_endpoint`+`token_endpoint`, synthesize a document; (2) token matching by effective token URL (scheme+host+path equality) when known, else the old heuristic.
- The wrapper learns the discovered token endpoint from the metadata it rewrites (same response), so injection does not need a second discovery.
- With any endpoint override, `createOAuthConfigInternal` always sets `OAuthConfig.AuthServerMetadataURL` (FR-024) so mcp-go never uses `getDefaultEndpoints`.
- The wrapper's `http.Client` gets a `CheckRedirect` enforcing FR-027a.
- The wrapper serves the AS metadata GET from the discovery cache when fresh (FR-026), so new handlers do not refetch.
- `discovery_cache.go`: `type discoveryKey [32]byte`; `cacheGet/Put` with TTL; single-flight per key; LRU 256. `createOAuthConfigInternal` reads PRM/AS results through it. 401 invalidation: the existing PRM-URL handling in `core.Client` (`connection.go` resource-metadata extraction) calls `oauth.InvalidateDiscovery(serverURL)` when the advertised URL differs.
- `offline_access`: after the waterfall, if `scopeSource == "prm"` and the cached AS metadata advertises it, append.
- Warn once: a `sync.Map` of server names already warned.

### 113-c — call-error-taxonomy

```go
// internal/callerr
type Class string    // network|timeout|http|jsonrpc|tool_error|session_terminated|auth|proxy_policy|proxy_internal|cancelled
type Domain string   // upstream|proxy|client
type Facts struct{ Dispatched bool; HTTPStatus int; ResponseReceived bool; CallerCancelled bool }
type Outcome struct{ Class Class; Domain Domain; HTTPStatus int }
func Classify(result *mcp.CallToolResult, err error, f Facts) (Outcome, bool) // false on success
func (o Outcome) AuditClass() audit.ErrorClass // FR-047
```

- `internal/transport`: `WithCallRecorder(ctx) (context.Context, *CallRecorder)`; a `RoundTripper` wrapper installed in every `CreateHTTPClient` branch (as the outermost layer, so 112's header func and OAuth are inside) stores the status of each response whose request ctx carries a recorder. The recorder is installed by the server dispatch sites (not in `managed`), passed down via ctx.
- Facts: `Dispatched` = the recorder saw a request; `ResponseReceived` = it saw any response.
- Activity: the three new fields are set where `Status`/`ErrorMessage` are set today; `ActivityFilter` gains `ErrorClass`, `FaultDomain`.

### 113-d — health-call-failure-rate

```go
// internal/upstream/callstats
const (CallFailureWindow = 5*time.Minute; CallFailureMinSamples = 5; CallFailureRatio = 0.5)
type Kind uint8 // network|timeout|http|jsonrpc|session
type Window struct{ /* 30 buckets of 10 s, mutex */ }
func (w *Window) Record(now time.Time, counted bool, failed bool, kind Kind)
func (w *Window) Snapshot(now time.Time) (calls, failures int, dominant Kind)
type Registry struct{ /* map[server]*Window */ }
func Classify(result *mcp.CallToolResult, err error) (counted, failed bool, kind Kind) // local predicate unless 113-c merged
```

- `upstream.Manager` owns a `*callstats.Registry`; `callTool` records after dispatch; the remove-server path drops the window; `Manager.CallStats(server)` is read by the three health input builders; `code_execution` dispatch records via `upstreamManager.RecordCallOutcome(server, result, err)`.
- Calculator: every `healthy` return goes through one `applyCallFailureRate(result, input)` wrapper (incl. the early return for an OAuth token inside the expiry-warning window); summary text constant-formatted.
- SSE: the existing health-change detection in the runtime (which already diffs health on its poll) fires naturally; the debounce is only needed if a push path is added. Implementer verifies and records which.

### 113-e — session-terminated-reinit

- `core` (new file `session_reinit.go`): `func (c *Client) SessionSnapshot() (id string, modern bool)` reading the Streamable HTTP transport's `GetSessionId()` and `serverInfo.ProtocolVersion`; `func (c *Client) ReinitializeSession(ctx) error` sending `initialize` + `notifications/initialized` on the existing mcp-go client/transport (verify mcp-go allows it; if the mcp-go `Client` refuses a second `Initialize`, fall back to building a new transport+client inside `core.Client` **without** changing `connectionEpoch`, and record that in the PR).
- `managed.Client` keeps `knownSessionID` (set after each successful legacy `initialize`). A `withSession(ctx, method, fn)` helper wraps `callTool`, `runListToolsAsLeader`, the cached-tool-count `ListTools`, prompt calls and the health `Ping`: before sending, if the transport session id is empty but `knownSessionID` is set → join/start the re-init flight; after `ErrSessionTerminated` with a known session and legacy protocol → re-init flight (keyed by `knownSessionID`), which runs `initialize` then a synchronous `tools/list` compared with current identity hashes. Retry: lists/get/ping once; `tools/call` once only for read-only tools whose hash is unchanged (FR-081); otherwise return a `session_terminated` error. Only the final error reaches `isConnectionError`.
- Counter: `atomic.Int64` per client exposed in diagnostics.

### 113-f — mcp-cache-hints

- Add `mcpserver.WithCacheHints(0, mcp.CacheScopePrivate)` to the four `NewMCPServer` calls.
- `cache_hints_test.go`: modern `initialize` (protocol 2026-07-28) + `tools/list` and `prompts/list` per endpoint, assert fields; legacy assert absent. Check the frozen tool-surface goldens are unaffected (they are legacy-era).

## Verification gates (every slice)

1. `go build ./...` and `go build -tags server -o /dev/null ./cmd/mcpproxy`.
2. `go test -race` on touched packages; for `internal/server` and `internal/httpapi` use `go test -race -tags server -timeout 20m -skip "E2E|Binary|MCPProtocol|TestInfoEndpoint|TestGracefulShutdownNoPanic|TestSocketInfoEndpoint" <pkgs>`.
3. `/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...` and again with `--build-tags server`.
4. `make swagger` when REST/OAS changes (113-b, 113-c); commit `oas/`.
5. `./scripts/test-api-e2e.sh` against an isolated instance.
6. Cross-model review via the opencode harness (github-copilot/gpt-6.1-sol), ≤10 rounds per PR.

## Complexity Tracking

| Item | Why needed | Simpler alternative rejected because |
|---|---|---|
| Refresh pre-emption inside `TokenStore.GetToken` | Only seam that stops mcp-go's unlocked refresh | Forking mcp-go is a maintenance burden; a lock elsewhere cannot stop mcp-go reading a stale token |
| Metadata-response rewrite for endpoint overrides | mcp-go has no endpoint config | Same as above |
| Transport call recorder | mcp-go discards HTTP status and unmapped JSON-RPC codes | String parsing is brittle |
