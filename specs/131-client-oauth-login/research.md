# Research: Browser login for MCP clients

Code references are from `main` at `dfc3d418f`. Product decisions and their alternatives are in [design.md](./design.md); this file covers what planning had to settle against the code.

## R1. Turning routes on and off without a restart

- **Finding**: the outer mux is built once in `(*Server).startCustomHTTPServer` (`internal/server/server.go:3380`, mux at 3431). `http.ServeMux` has no unregister. Existing switchable features decide per request inside an always-registered handler (`/mcp/s/` in `serveSingleServer`, `/debug/pprof/`, `enable_skills` through `RefreshSkillsAvailability`).
- **Decision**: register `/mcp/oauth`, `/mcp/oauth/`, `/oauth/`, `/.well-known/oauth-protected-resource/`, `/.well-known/oauth-authorization-server` once. Each is a gate with three states read per request from config and the ever-used marker (cached, refreshed on `EventTypeConfigReloaded` and on first grant):
  - off, never used → call the handler that owned the path before (`mcpHandler` for `/mcp/oauth*`; the root `/` handler for the others);
  - off, used → 404;
  - on → serve.
- **Rationale**: the only way to get hot reload and FR-002 together.
- **Alternatives**: restart-gated field (rejected: constitution III, and the Settings switch would need a restart); rebuilding the mux (rejected: touches every route for one feature).
- **Test**: for each new pattern and a table of methods/headers, the response of the gate (off, never used) equals the response of the unwrapped previous handler, byte for byte on status, headers of interest and body.

## R2. Where an access token is authenticated

- **Finding**: `(*Server).mcpAuthMiddleware` (`server.go:605`) extracts the token (636), handles `mcp_agt_`/`mcp_cli_` at 677 via `storageManager.ValidateAgentToken` and `agentToken.AuthContext()` (723), then the admin key (733). With `require_mcp_auth` off an unknown token falls to the anonymous context (756).
- **Decision**: the OAuth addresses get their own chain `hostValidation → tagMCP → oauthResourceAuth → loggingHandler(streamableServer)`. `oauthResourceAuth` accepts only `Authorization: Bearer mcp_oat_…` whose grant is live, whose resource equals the request's origin + path, and whose client matches the path. Anything else (no token, API key, `mcp_cli_`, wrong audience) → `401` with the `WWW-Authenticate` challenge. It never falls to anonymous. `mcpAuthMiddleware` on the other routes gains one branch: a `mcp_oat_` token is always `401` there (wrong audience), also when `require_mcp_auth` is off, but only while the feature is on or was used, so FR-002 holds.
- **Rationale**: audience binding (FR-007) and "no anonymous fallback" (FR-034) are properties of the OAuth addresses; keeping them out of the shared middleware keeps the default-off path untouched.

## R3. The auth context of a browser login

- **Finding**: `(*AgentToken).AuthContext` (`internal/auth/agent_token.go:339`) sets `Type=AuthTypeAgent`, `CredentialKind=agent_token`, `TokenKind`, `ClientID`, `ProfilePin`, `ProfileMode`. `CredentialKind` constants (`internal/auth/context.go:13-30`) have no OAuth kind.
- **Decision**: add `CredentialKindOAuth = "oauth"`. Add `auth.ClientAuthContext(clientID, pin, mode, kind)` used by both the `mcp_cli_` path and the OAuth path, so the two cannot drift. Activity gains `grant_id` in metadata.
- **Risk**: code that switches on `CredentialKind == agent_token` to mean "scoped caller". **Mitigation**: a source test lists every comparison against `CredentialKindAgentToken` and requires each to be classified as "also applies to oauth" or "API-key specific".

## R4. One binding, two records

- **Finding**: the binding lives on the `Kind=client` record; `storage.UpdateClientCredentialBinding(clientID, pin, mode)` (`internal/storage/client_credentials.go:233`) is called only from `ClientsService.setBindingLocked` (`internal/runtime/clients_service.go:539`). There is no index by client id (`findClientTokenRecordLocked` scans). `ValidateTokenInvariants` refuses a client record without a secret hash, expiry or with locked + empty pin.
- **Decision**: new bucket `oauth_client_bindings`, key = client id, value `{pin, mode, display_name, updated_at}`. `UpdateClientCredentialBinding` writes the API-key record if present and the OAuth record if present, in one transaction; it fails only if neither exists. `setBindingLocked` drops its "must be `CredentialStateClient`" precondition in favour of "client has an API key or a live grant". Startup check: for a client with both, the API-key record wins and the mismatch is logged at WARN.
- **Alternatives**: single new bucket for all bindings (rejected: downgrade reads the stale API-key record, FR-028); credential-less `AgentToken` (rejected: fails invariants on older binaries and pollutes token lists).

## R5. The binding guard

- **Finding**: `ActiveNamedBinding(t, now)` (`internal/runtime/binding_guard.go:114`) and the evaluator in `internal/server/profile_binding_guard.go` (`BindingGuardActiveBindings` at 186, `activeNamedClientBinding` at 76) read `auth.AgentToken` only. Fixes are `require_mcp_auth` and `set_anonymous_profile` (`internal/profile/contract.go:192`).
- **Decision**: the evaluator's set of active bindings becomes the union of (a) today's records and (b) clients in `oauth_client_bindings` with a named pin and at least one grant that is neither revoked nor lapsed. `BindingRef` is unchanged (it names client and profile). Approval calls `CheckBindingGuard` with the candidate binding before any write; the consent REST `GET` returns the guard's verdict and fixes so the page can show them first (FR-022).

## R6. Refresh tokens

- **Decision**: `mcp_ort_<grantID>_<secret>_<mac>`; `grantID` 16 random bytes (base62), `secret` 32 random bytes, `mac = HMAC-SHA256(key, "ort|"+grantID+"|"+secret)` truncated to 16 bytes. Key: `auth.GetOrCreateHMACKey(dataDir)` (`agent_token.go:580`), the key agent tokens already use, with a distinct label so the MAC can never collide with a token hash. Stored: `HashToken(secret, key)` and a 12-character prefix.
- **Check order** (FR-009): parse and MAC → load grant → compare secret hash in constant time → rotate or revoke, in one `db.Update`.
- **Access token**: `mcp_oat_<random 32 bytes>`, stored hashed in the grant (`access_hash`, `access_expires_at`); one live access token per grant. A second index bucket is not needed: the lookup key is the hash, kept in `oauth_grants` under a sub-bucket `by_access_hash → grant id`.

## R7. Loopback only

- **Finding**: `newHostValidationHandler` (`internal/server/host_validation.go:41`) validates `Host` and `Origin` on loopback connections and passes non-loopback connections through. No helper inspects `r.RemoteAddr`. `httpx.IsLoopbackHost` (`internal/httpx/origin.go:19`) exists.
- **Decision**: `peerIsLoopback(r)` parses `r.RemoteAddr` with `netip.ParseAddrPort` and requires `IsLoopback()`; the gate requires it together with `httpx.IsLoopbackHost(r.Host)` (a `trusted_hosts` name is not enough in version 1). Requests over the tray socket are not TCP loopback and get 404. Server edition: `config.ServerEditionEnabled(cfg)` → 404.
- **Origin**: `scheme://host` from the request (`https` when the listener is TLS), used verbatim as issuer and as the resource prefix. `127.0.0.1` and `localhost` are different origins by design.

## R8. Rate limits and caps

- **Finding**: no HTTP rate-limit middleware exists. `limiter.TokenRates` (`internal/upstream/limiter/token_rate.go`) is a per-key token bucket with a clock seam and idle sweep.
- **Decision**: one private `TokenRates` in `internal/mcplogin/limits.go`; keys `register|<ip>`, `authorize|<ip>`, `token|<ip>`. Defaults: register 10/min burst 5, authorize 30/min burst 10, token 60/min burst 20. `http.MaxBytesReader` 16 KiB on register and token. Pending registrations capped at 50 (oldest pending dropped). Over limit → `429` with `Retry-After`.

## R9. Events and activity

- **Finding**: events are published through an injected `publish func(Event)`; admin-only types are listed in `adminConfigEventTypes` (`internal/httpapi/sse_scope.go`). `writeChangeRecord` (`clients_service.go:444`) is the single `profile_change` builder; `profile.ChangeKind` has no approve/deny.
- **Decision**: new event `oauth.authorization_pending` `{request_id, client_id, client_label}` (admin-only, no reported name beyond the sanitised label) and reuse of `credentials.changed` for grant create/revoke. New change kinds `approve`, `deny`, `replay_revoke`. The actor comes from `runtime.RequestActor(ctx)`; a replay revocation is recorded with actor kind `system`.

## R10. Storage laziness

- **Finding**: `initBuckets` (`internal/storage/bbolt.go:160`) creates a fixed list at open; other buckets (`agent_tokens`, `tpa_bundle_state`, …) are created with `CreateBucketIfNotExists` in the write path and readers treat a nil bucket as empty. Markers live in `MetaBucket` (`markOAuthCompactAdvisedTx`, `oauth_binding.go:110`).
- **Decision**: all three buckets are lazy; nothing is added to `initBuckets`. `oauth_login_ever_used` is written in the transaction that stores the first grant. A test opens a database, runs a default-off server through the e2e route table, and asserts the bucket list is unchanged.
- **Purge**: `ForgetClientCredential` path gains `forgetOAuthClientTx(tx, clientID)`.

## R11. Config field

- **Finding**: wiring points for a hot bool: struct field (`internal/config/config.go`), `DefaultConfig`, `Field…`/`envOverride` (`process_overrides.go`, `loader.go`), `DetectConfigChanges` / `undiffedHotConfigFields` (`internal/runtime/config_hotreload.go`), swagger, `frontend/src/views/settings/fields.ts`, `SettingsCatalog.swift`, `docs/configuration/config-file.md`.
- **Decision**: `MCPOAuthEnabled bool json:"mcp_oauth_enabled,omitempty"`, env `MCPPROXY_MCP_OAUTH_ENABLED`, hot. `omitempty` keeps existing config files byte-identical after a save.

## R12. Connect

- **Finding**: `buildServerEntry` (`internal/connect/clients.go:566`) and `withAPIKeyHeader` (636) build entries; `baseURL()` returns `http://<listen>/mcp`; `ClientDef` has no OAuth flag; Claude Desktop goes through `mcp-remote`.
- **Decision**: `ClientDef.SupportsBrowserLogin bool`, set only for clients that pass the real-client check. `ConnectOptions.Auth` (`key` default, `oauth`). For `oauth` the entry is built with `baseURL()+"/oauth/"+clientID` and no header; the credential minter is not called. The precondition token and preview cover the new option unchanged.

## R13. Real clients and plain HTTP

- **Open risk carried from the design**: the issuer is `http://127.0.0.1:<port>`. RFC 8414 and the MCP authorization spec ask for HTTPS. Whether each client accepts it, and whether it follows protected-resource metadata for a path below the MCP root, is an empirical question.
- **Decision**: a PR-1 gate. Run Claude Code, Cursor, VS Code and MCP Inspector against a built core on a per-client path; record for each: discovery, registration name, redirect URI shape, token exchange, refresh. Results go to `verification/real-clients.md` and decide (a) `SupportsBrowserLogin` per client and (b) the docs' supported list. If no client completes the flow over plain HTTP, stop and report before PR-2: the fallback is serving the OAuth endpoints only when local TLS is on.
