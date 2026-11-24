# Research: Browser login for MCP clients

Code references are from `main` at `dfc3d418f`. Product decisions and their alternatives are in [design.md](./design.md); this file covers what planning had to settle against the code.

## R1. Turning routes on and off without a restart

- **Finding**: the outer mux is built once in `(*Server).startCustomHTTPServer` (`internal/server/server.go:3380`, mux at 3431). `http.ServeMux` has no unregister. Existing switchable features decide per request inside an always-registered handler (`/mcp/s/` in `serveSingleServer`, `/debug/pprof/`, `enable_skills` through `RefreshSkillsAvailability`).
- **Gate state**: read from the live config and the marker on each request; nothing about the decision is cached across a config change.
- **Decision**: register only exact paths plus two subtrees whose no-slash form is also registered: `/mcp/oauth`, `/mcp/oauth/`, `/oauth/register`, `/oauth/authorize`, `/oauth/token`, `/.well-known/oauth-authorization-server`, `/.well-known/oauth-protected-resource/mcp/oauth`, `/.well-known/oauth-protected-resource/mcp/oauth/`. A subtree pattern such as `/oauth/` is not used: `ServeMux` would answer `/oauth` with a redirect before any gate runs, which differs from today's 404. Each is a gate with three states read per request from config and the ever-used marker (cached, refreshed on `EventTypeConfigReloaded` and on first grant):
  - off, never used → call the handler that owned the path before (`mcpHandler` for `/mcp/oauth*`; the root `/` handler for the others);
  - off, used → 404;
  - on → serve.
- **Rationale**: the only way to get hot reload and FR-002 together.
- **Alternatives**: restart-gated field (rejected: constitution III, and the Settings switch would need a restart); rebuilding the mux (rejected: touches every route for one feature).
- **Test**: build the complete mux twice, with and without the new registrations, and compare answers for a table of paths (the registered ones, their slash and no-slash variants, `/oauth`, `/oauth/`, `/oauth/x`, `/.well-known/oauth-protected-resource`, `/.well-known/x`), methods and credentials, in all three gate states where the legacy mux is the reference for "off, never used".

## R2. Where an access token is authenticated

- **Finding**: `(*Server).mcpAuthMiddleware` (`server.go:605`) extracts the token (636), handles `mcp_agt_`/`mcp_cli_` at 677 via `storageManager.ValidateAgentToken` and `agentToken.AuthContext()` (723), then the admin key (733). With `require_mcp_auth` off an unknown token falls to the anonymous context (756).
- **Decision**: the OAuth addresses get their own chain `hostValidation → tagMCP → oauthResourceAuth → loggingHandler(streamableServer)`. `oauthResourceAuth` accepts only `Authorization: Bearer mcp_oat_…` whose grant is live, whose resource equals the request's origin + path, and whose client matches the path. Anything else (no token, API key, `mcp_cli_`, wrong audience) → `401` with the `WWW-Authenticate` challenge. It never falls to anonymous. `mcpAuthMiddleware` on the other routes gains one branch: a `mcp_oat_` token is always `401` there (wrong audience), also when `require_mcp_auth` is off, but only while the feature is on or was used, so FR-002 holds.
- **Rationale**: audience binding (FR-007) and "no anonymous fallback" (FR-034) are properties of the OAuth addresses; keeping them out of the shared middleware keeps the default-off path untouched.

## R3. The auth context of a browser login

- **Finding**: `(*AgentToken).AuthContext` (`internal/auth/agent_token.go:339`) sets `Type=AuthTypeAgent`, `CredentialKind=agent_token`, `TokenKind`, `ClientID`, `ProfilePin`, `ProfileMode`. `CredentialKind` constants (`internal/auth/context.go:13-30`) have no OAuth kind.
- **Finding (review)**: `AuthContext()` also sets `AgentName`, `TokenPrefix`, `TokenGeneration` and `RateLimit` (`agent_token.go:343-358`); work-session grouping (`workspace.go:158-190`) and activity attribution (`mcp_activity_attribution.go:62-71`) read them.
- **Decision**: add `CredentialKindOAuth = "oauth"` and `AuthContext.GrantID`. One builder, `auth.ClientAuthContext(ClientIdentity)`, is used by both paths. For a browser login: `AgentName = ClientTokenName(clientID)` (the same `client-<id>` an API key has, so activity and filters match), `TokenPrefix` = the grant's display prefix, `TokenGeneration` = grant id (so each login is its own work session and a revoked login never merges with a new one), `RateLimit` = the client rate limit configuration an API key gets. The MCP session record stores the grant id at `initialize`, so session-backed activity emitted without a request context still carries it.
- **Risk**: code that switches on `CredentialKind == agent_token` to mean "scoped caller". **Mitigation**: a source test lists every comparison against `CredentialKindAgentToken` and requires each to be classified as "also applies to oauth" or "API-key specific".

## R4. One binding, two records

- **Finding**: the binding lives on the `Kind=client` record; `storage.UpdateClientCredentialBinding(clientID, pin, mode)` (`internal/storage/client_credentials.go:233`) is called only from `ClientsService.setBindingLocked` (`internal/runtime/clients_service.go:539`). There is no index by client id (`findClientTokenRecordLocked` scans). `ValidateTokenInvariants` refuses a client record without a secret hash, expiry or with locked + empty pin.
- **Finding (review)**: the binding is also written by connect rollback (`clients_service_connect.go:355`), by mint and replacement mint (`client_credentials.go:156-219`) and by profile rename/reassign through `RepinProfile` / `RestorePins` (`internal/storage/agent_token_repin.go`, called from `internal/runtime/config_funnel.go:126-143`). Profile users are found through `ListAgentTokens()` in `internal/runtime/profiles_service.go`.
- **Decision**: new bucket `oauth_client_bindings`, key = client id, value `{pin, mode, display_name, updated_at}`. A transaction-local helper `writeClientBindingTx(tx, clientID, pin, mode)` writes the API-key record if present and the OAuth record if present; it fails only if neither exists. Update, mint, reconnect, rollback, repin and restore all call it inside their own transaction. Minting an API key for a client that has only browser logins writes the binding the caller asked for to both records, and first copies the client's OAuth binding into the new record's `PriorProfilePin`/`PriorProfileMode` (today these are captured only from an earlier API-key record, `client_credentials.go:186-200`). An aborted or undone connect restores that snapshot to the OAuth record through `writeClientBindingTx`, so a wider binding from a connect that never took effect cannot stay on the existing logins. `profiles_service.go` gets a `ListClientBindings()` view that includes OAuth-only clients. `setBindingLocked` drops its "must be `CredentialStateClient`" precondition in favour of "client has an API key or a live grant". Startup check: for a client with both, the API-key record wins and the mismatch is logged at WARN. The browser-login request path reads the binding with the same rule (`CurrentClientBinding`), never the OAuth entry alone, so a change made by an older binary is honoured even before the repair runs. The writer validates a binding only when an OAuth copy exists; API-key-only writes stay exactly as before. A profile-rename rollback (`RestorePins`) decides once per client, by the entry's `write_id`, and restores both copies or neither; it does not touch the connect snapshot (`PriorProfile*`), so undoing a connect returns a client to its pre-connect binding as it does for API keys today.
- **Alternatives**: single new bucket for all bindings (rejected: downgrade reads the stale API-key record, FR-028); credential-less `AgentToken` (rejected: fails invariants on older binaries and pollutes token lists).

## R5. The binding guard

- **Finding**: `ActiveNamedBinding(t, now)` (`internal/runtime/binding_guard.go:114`) and the evaluator in `internal/server/profile_binding_guard.go` (`BindingGuardActiveBindings` at 186, `activeNamedClientBinding` at 76) read `auth.AgentToken` only. Fixes are `require_mcp_auth` and `set_anonymous_profile` (`internal/profile/contract.go:192`).
- **Finding (review)**: `GuardState` holds only `Tokens []auth.AgentToken` (`binding_guard.go:44-47`); `BindingGuardDelta` and `BindingGuardFixes` evaluate explicit current and candidate snapshots (`profile_binding_guard.go:118-180`), and config changes build token-only candidates (`config_funnel.go:102-112`, `218-224`).
- **Decision**: `GuardState` gains `OAuthBindings []OAuthBinding{ClientID, Pin, Mode, Live}`. Every snapshot producer (clients service, credentials service, config funnel, offline CLI, anonymous-profile check) fills it from storage; a read error refuses the change. An approval builds the candidate by adding or replacing its own client's entry with `Live: true` before anything is stored. Delta, fixes, `ConservativeBindingGuard` and `StrictOfflineBindingGuard` treat an entry with a named pin and `Live` as an active binding, deduplicated with the API-key record of the same client. `BindingRef` is unchanged (it names client and profile). Approval calls `CheckBindingGuard` with the candidate binding before any write; the consent REST `GET` returns the guard's verdict and fixes so the page can show them first (FR-022).

## R6. Refresh tokens

- **Decision**: `mcp_ort_<grantID>_<secret>_<mac>`; `grantID` 16 random bytes (base62), `secret` 32 random bytes, `mac = HMAC-SHA256(key, "ort|"+grantID+"|"+secret)` truncated to 16 bytes. Key: `auth.GetOrCreateHMACKey(dataDir)` (`agent_token.go:580`), the key agent tokens already use, with a distinct label so the MAC can never collide with a token hash. Stored: `HashToken(secret, key)` and a 12-character prefix.
- **Check order** (FR-009, FR-032): parse and MAC → load grant → revoked or `refresh_expires_at` passed → `invalid_grant`, no write, lifetime not extended → compare secret hash in constant time → rotate or revoke, in one `db.Update`.
- **Access token**: `mcp_oat_<random 32 bytes>`, stored hashed in the grant (`access_hash`, `access_expires_at`); one live access token per grant. A second index bucket is not needed: the lookup key is the hash, kept in `oauth_grants` under a sub-bucket `by_access_hash → grant id`.

## R7. Loopback only

- **Finding**: `newHostValidationHandler` (`internal/server/host_validation.go:41`) validates `Host` and `Origin` on loopback connections and passes non-loopback connections through. No helper inspects `r.RemoteAddr`. `httpx.IsLoopbackHost` (`internal/httpx/origin.go:19`) exists.
- **Decision**: `peerIsLoopback(r)` parses `r.RemoteAddr` with `netip.ParseAddrPort` and requires `IsLoopback()`; the gate requires it together with `httpx.IsLoopbackHost(r.Host)` (a `trusted_hosts` name is not enough in version 1). Requests over the tray socket are not TCP loopback and get 404. Server edition: `config.ServerEditionEnabled(cfg)` → 404.
- **Origin**: `scheme://host` from the request (`https` when the listener is TLS), used verbatim as issuer and as the resource prefix. `127.0.0.1` and `localhost` are different origins by design.

## R8. Rate limits and caps

- **Finding**: no HTTP rate-limit middleware exists. `limiter.TokenRates` (`internal/upstream/limiter/token_rate.go`) is a per-key token bucket with a clock seam and idle sweep.
- **Decision**: one private `TokenRates` in `internal/mcplogin/limits.go`; keys `register|<ip>`, `authorize|<ip>`, `token|<ip>`. Defaults: register 10/min burst 5, authorize 30/min burst 10, token 60/min burst 20. `http.MaxBytesReader` 16 KiB on register and token. Registrations that are not approved (`pending` and `authorized`) are capped at 50 together; the oldest of them is dropped. Over limit → `429` with `Retry-After`.

## R9. Events and activity

- **Finding**: events are published through an injected `publish func(Event)`; admin-only types are listed in `adminConfigEventTypes` (`internal/httpapi/sse_scope.go`). `writeChangeRecord` (`clients_service.go:444`) is the single `profile_change` builder; `profile.ChangeKind` has no approve/deny.
- **Decision**: new event `oauth.authorization_pending` `{request_id, client_id, client_label}` (admin-only, no reported name beyond the sanitised label) and reuse of `credentials.changed` for grant create/revoke. New change kinds `approve`, `deny`, `replay_revoke`. `approve`, `deny` and `revoke` take the actor from `runtime.RequestActor(ctx)`. `issue` and `replay_revoke` happen on the public token endpoint: they name the administrator stored in the grant's `approved_by` and add `trigger: client`.

## R10. Storage laziness

- **Finding**: `initBuckets` (`internal/storage/bbolt.go:160`) creates a fixed list at open; other buckets (`agent_tokens`, `tpa_bundle_state`, …) are created with `CreateBucketIfNotExists` in the write path and readers treat a nil bucket as empty. Markers live in `MetaBucket` (`markOAuthCompactAdvisedTx`, `oauth_binding.go:110`).
- **Decision**: all three buckets are lazy; nothing is added to `initBuckets`. `oauth_login_ever_used` is written in the transaction that stores the first grant. A test opens a database, runs a default-off server through the e2e route table, and asserts the bucket list is unchanged.
- **Finding (review)**: `ForgetClientCredential` is shared by client forget and client revoke (`clients_service.go:695-724`), and its restoring variant aborts a failed connect (`clients_service_connect.go:393-399`).
- **Decision**: four distinct storage operations. `ForgetClient` revokes the API-key record exactly as today's forget does (the tombstone keeps the name reserved) and deletes all grants, the OAuth binding and registrations left without a grant. `RevokeClient` marks the API key and every grant revoked and keeps them for display. `RevokeClientAPIKey` touches the API-key record only. Connect rollback (`ForgetClientCredentialRestoringPrior`) touches the API-key record only and restores the binding through `writeClientBindingTx`.

## R11. Config field

- **Finding**: wiring points for a hot bool: struct field (`internal/config/config.go`), `DefaultConfig`, `Field…`/`envOverride` (`process_overrides.go`, `loader.go`), `DetectConfigChanges` / `undiffedHotConfigFields` (`internal/runtime/config_hotreload.go`), swagger, `frontend/src/views/settings/fields.ts`, `SettingsCatalog.swift`, `docs/configuration/config-file.md`.
- **Decision**: `MCPOAuthEnabled bool json:"mcp_oauth_enabled,omitempty"`, env `MCPPROXY_MCP_OAUTH_ENABLED`, hot. `omitempty` keeps existing config files byte-identical after a save.

## R12. Connect

- **Finding**: `buildServerEntry` (`internal/connect/clients.go:566`) and `withAPIKeyHeader` (636) build entries; `baseURL()` returns `http://<listen>/mcp`; `ClientDef` has no OAuth flag; Claude Desktop goes through `mcp-remote`.
- **Decision**: `ClientDef.SupportsBrowserLogin bool`, set only for clients that pass the real-client check. `ConnectOptions.Auth` (`key` default, `oauth`). For `oauth` the entry is built with `baseURL()+"/oauth/"+clientID` and no header; the credential minter is not called. The precondition token and preview cover the new option unchanged.

## R13. Real clients and plain HTTP

- **Open risk carried from the design**: the issuer is `http://127.0.0.1:<port>`. RFC 8414 and the MCP authorization spec ask for HTTPS. Whether each client accepts it, and whether it follows protected-resource metadata for a path below the MCP root, is an empirical question.
- **Decision**: a PR-1 gate. Run Claude Code, Cursor, VS Code and MCP Inspector against a built core on a per-client path; record for each: discovery, registration name, redirect URI shape, token exchange, refresh. Results go to `verification/real-clients.md` and decide (a) `SupportsBrowserLogin` per client and (b) the docs' supported list. If no client completes the flow over plain HTTP, stop and report before PR-2: the fallback is serving the OAuth endpoints only when local TLS is on.

## R14. The initialize-name warning

- **Finding**: the name a client sends at `initialize` is already recorded for presence (`clientidentity`, `ClientInfoNames` matching in `internal/connect/clients.go`).
- **Decision**: at `initialize` on an OAuth address, store the sanitised name on the grant (`observed_client_name`, `observed_at`), off the request path like last-used. The mismatch is derived on read: the observed name, normalised, is neither the registration's reported name nor, for a registry client, one of its id, display name or `ClientInfoNames`. It is advisory, shown per login, and takes no part in selection or authorization.

## R15. When a login is stored

- **Decision**: approval writes no registration, login, binding or marker. It records the decision (client, profile, mode, replace choice, approving actor) on the in-memory request, writes the `approve` activity row (activity is the one thing it persists) and issues the code. The **token exchange** is the commit: under `bindingWriteMu` it confirms the request is still `approved`, then in one transaction re-checks the binding revision and the guard with the decided candidate, creates the OAuth-side binding whenever the client has none (a new client, and also an existing API-key client getting its first login, from that client's current binding), promotes the registration to `approved`, stores the grant (with the redirect URI used, resource, issuer, `approved_by`), revokes the replaced login if asked, and sets the ever-used marker; then it writes the `issue` row. If the re-check fails the exchange answers `invalid_grant`, nothing is stored and a `deny` row with reason `binding_changed` or `guard` is written, naming the decision's approving administrator with `trigger: client`.
- **Cancellation**: revoking a client, forgetting a client and turning the feature off cancel every request that targets that client (by address or by decision), pending or approved, and with it any unconsumed code. They run under the same `bindingWriteMu` as the commit, so an exchange either completes before them (and its grant is then revoked or deleted by them) or finds its request cancelled. The binding revision alone would not catch a revoke, which changes neither pin nor mode. For turn-off the cancellation is a synchronous step of applying a config in which `mcp_oauth_enabled` goes from true to false, on both the API update path and the file-reload path, before the new config becomes visible; it does not depend on `EventTypeConfigReloaded`, which is asynchronous and may be dropped (`event_bus.go:206-219`). Events only refresh caches and notify surfaces. The gates read the live config on every request.
- **Re-checks inside the commit transaction**, each failing with `invalid_grant` and no write: the registration still exists (it may have been evicted or deleted with its last grant; it is never recreated from the code); a decision that creates a new client still finds that id absent and not reserved (two approved requests for the same new id: the second fails, it is not turned into "existing client"); the binding revision, where the revision of an absent client is a fixed sentinel; the guard.
- **Rationale**: an approval the client never completes leaves no login, no client row and no marker. One point of commit also removes the question of what a code refers to.
- **Consequence**: a restart between approval and exchange drops the code; the client starts again.

## R16. Guard verdict on the consent page

- **Finding (review)**: on the generic address the client, profile and mode are not known when the page loads, so one `guard` field on the request cannot describe the user's choice.
- **Decision**: `POST /api/v1/oauth/authorizations/{id}/preview` (read-only, admin) takes the proposed client (existing or new), profile and mode and returns the guard verdict, its fixes and the access sentence. The page calls it whenever the selection becomes complete or changes, and enables Approve only on a clean verdict. `GET` returns a verdict only when the address fixes a client that already has a binding.
