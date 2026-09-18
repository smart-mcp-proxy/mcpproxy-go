# Data Model

No BBolt schema migration. Every persisted change is an additive JSON field on an existing record (absent → zero value on decode) or a config key. Wire shapes for config keys are in `contracts/config-keys.md`; the audit line in `contracts/audit-line.schema.json`.

## 1. Persisted: `users.User` (`internal/serveredition/users/models.go:15-24`, bucket `users` + `users_by_email`) — PR-B/PR-C

| Field | JSON | Type | Written by | Read by | Notes |
|---|---|---|---|---|---|
| `Groups` | `groups` | `[]string` | `upsertUser` on every successful login (B); wholesale replace, `[]` on absence/overage/non-array | `entitledServerNames` (C), `GET /auth/me`, `GET /admin/users` | Opaque, exact, case-sensitive strings; never sent to any IdP/upstream. Legacy providers always store `[]`. Upgraded records decode as `nil` → treated as "matches no key" until next login (documented fail-closed window). |
| `GroupsUpdatedAt` | `groups_updated_at` | `time.Time` (omitempty) | same login write | `/auth/me`, `/admin/users` | Zero on upgraded records. |
| `SubjectRebindArmedAt` | `subject_rebind_armed_at` | `*time.Time` (omitempty) | enable handler on a real `Disabled true→false` transition (set); disable handler (clear); first successful login while set (clear, same store write as the rebind) | `HandleCallback` subject check; `/admin/users` | Absent on every upgraded record = closed. Single-use, durable, administrator-opened (FR-023). |
| `Provider`, `ProviderSubjectID` | unchanged names | | refreshed on **every** successful login (today `Provider` never, `ProviderSubjectID` only with an avatar, `oauth_handler.go:389-397`) | subject binding | `User.Validate` (`models.go:48-54`) admits `oidc` and keeps requiring a non-empty subject (critic G5). |

Store contract additions (`users/store.go`): `UpdateUserLogin(ctx, verified LoginClaims) (LoginOutcome, error)` is **transaction-owned** — it re-reads the record by the email index **inside one `db.Update`**, evaluates the subject rule there (bind when empty/equal; rebind on a provider change or when `SubjectRebindArmedAt` is set, clearing the flag in the same write; otherwise `ErrSubjectMismatch` with nothing written; `ErrUserDisabled` on a disabled record), writes groups/subject/provider/last-login and returns `LoginOutcome{User, Created, Rebound, RebindConsumed}`. A pre-mutated `*User` cannot provide this: today's `GetUserByEmail` is a separate `View` (`store.go:128-164`) and `UpdateUser` re-reads only for the email index (`:166-221`), so two callbacks could both observe the armed flag and rebind to different subjects. Two concurrent logins with different subjects while the flag is set → exactly one rebinds, the other is `subject_mismatch` (FR-023 fixture, T038). `LoginClaims{Email, Provider, Subject, Name, AvatarURL, Groups []string, GroupsKnown bool}` carries only verified values. No new bucket, no index on groups (entitlement is computed per request from one `GetUser`).

## 2. Persisted: `users.Session` (`models.go:59-67`) — unchanged shape

`IPAddress` is now derived through `config.ForwardedHeaders(r, trustedProxies)` (right-most untrusted hop) instead of an unconditional `X-Forwarded-For` read (`session_store.go:129-151`). No field change.

## 3. Persisted: `auth.AgentToken` (`internal/auth/agent_token.go:33-45`) — persisted shape unchanged; three non-persisted carrier fields (PR-C)

Stored grant `AllowedServers` remains the original grant (Spec 106 data model). Per-owner cap counts tokens sharing `UserID` (ownerless = one owner). `ExpiresAt` is always set for owned tokens (pinned by test; only ownerless operator tokens may be zero).

| Field | JSON | Type | Written by | Read by | Notes |
|---|---|---|---|---|---|
| `OwnerEmail`, `OwnerProvider`, `OwnerRole` | `-` (`json:"-"`, **never persisted** — the BBolt record is `json.Marshal`ed, `agent_tokens.go:199`, so the tag keeps them out of the store) | `string` | `storage.ValidateAgentToken` from the single owner resolution (§6), on the **returned** `*AgentToken` value only | `AgentToken.AuthContext()` copies them into `AuthContext.Email/Provider/Role` | The token value is the only carrier storage can stamp: `ValidateAgentToken` returns `*auth.AgentToken` (`agent_tokens.go:943`) and every caller builds the context from it (`httpapi/server.go:580`, `server/server.go:427`, `sse_scope.go:43`). T071 asserts the persisted record bytes never contain them. |

## 4. Ephemeral: `auth.AuthContext` (`internal/auth/context.go:14-39`) — PR-C/PR-D

| Field | Change | Set by |
|---|---|---|
| `Email`, `Provider`, `Role` | now populated for **agent tokens** from the single owner resolution at authentication (today only `UserID`, `agent_token.go:54-72`); never persisted on the token record — carried on the validated token's `json:"-"` `Owner*` fields (§3) and copied by `AgentToken.AuthContext()` | `storage.ValidateAgentToken` stamps the token value via `SetAgentTokenOwnerResolver`; `AgentToken.AuthContext()` copies (C) |
| `CredentialKind` (new) | `socket\|api_key\|bearer_jwt\|agent_token\|cookie\|anonymous` — records which FR-001 source authenticated the request; drives the session-only mint doors (FR-011), `caller.kind` (FR-013) and `CanRevealSecrets` (FR-002) | `apiKeyAuthMiddleware`, `mcpAuthMiddleware`, server-edition middleware  (type + constants defined by T076 in PR-C phase C.2; `stdioAuthContext` tags no kind — stdio identity comes from `transport.ConnectionSourceStdio`, PR-D) |
| `AllowedServers` for `UserContext` | materialised entitlement set (never nil, never `"*"`) instead of `nil` (`context.go:155-176`) | session-principal resolver (C) |
| `IsSessionPrincipal()` (new method) | `CredentialKind ∈ {cookie, bearer_jwt}` | — |
| `CanRevealSecrets()` | additionally `false` for session principals | — |

## 5. Ephemeral: `audit.Attempt` (context value, `internal/audit/attempt.go`) — PR-D

Immutable per dispatch attempt, installed before the first gate:

```go
type Attempt struct {
    RequestID, TransportRequestID, ParentID string
    SessionID, WorkSessionID               string
    Server, Tool                           string // canonical pair (Spec 105 FR-009)
    Operation                              string // read|write|destructive|unknown
    Surface                                string // call_tool_read|call_tool_write|call_tool_destructive|direct|code_execution|rest
    Source                                 string // mcp|api|internal — from the mount point, never X-MCPProxy-Client
    Origin                                 string // local|socket|remote(reserved)
    ClientName, ClientVersion, ClientIP    string // client.* ; IP via trusted_proxies
    Profile, ProfilePin                    string
    ArgsSHA256                             string // RFC 8785 over StripInternalArgs(args), pre-masking
    ArgsBytes                              int
    StartedAt                              time.Time
}
```

`Attempt` never holds arguments, responses, error text or `_auth_*` values (the structural rule of FR-015 is enforced by construction: the line builder has no parameter through which they could arrive).

## 6. Ephemeral: `storage.OwnerResolution` (replaces two callbacks) — PR-C

```go
type OwnerResolution struct {
    Active               bool
    UserID, Email        string
    Provider, Role       string   // role live from admin_emails
    Entitled             []string // non-nil; the NARROWED grant: narrowScopeToEntitled(granted, entitledServerNamesFor(user, isAdmin), isAdmin)
                                  // — ["*"] literal for an administrator, materialised for a tenant (contracts/entitlement-predicate.md §2)
}
type agentTokenOwnerResolver func(userID string, granted []string) (OwnerResolution, error)
```

Storage semantics (unchanged in spirit from `agent_tokens.go:911-939`): store error or `Active=false` → `ErrAgentTokenOwnerInactive`; resolver error → `ErrAgentTokenScopeUnavailable`; `token.AllowedServers = intersectAllowedServers(granted, Entitled)` (non-nil empty on empty; `Entitled` is already narrowed, so this is the narrow-only fence as today and an administrator's literal `"*"` survives — `intersect(["*"], ["*"]) = ["*"]`, FR-009); `token.OwnerEmail/OwnerProvider/OwnerRole = res.Email/Provider/Role` (§3). **One `GetUser` per authentication**: the resolver (built in `setup.go`) loads the record once, computes the entitlement set from that loaded record through `entitledServerNamesFor(user, isAdmin)` (`contracts/entitlement-predicate.md` §1) and narrows the stored grant against it with `narrowScopeToEntitled` (never returning the raw configuration list as `Entitled`) — the predicate never performs its own `GetUser` on this path, even though it now reads `User.Groups`.

## 7. Ephemeral: OIDC provider state (`internal/serveredition/auth/oidc_provider.go`) — PR-B

| Object | Fields | Lifetime |
|---|---|---|
| `discoveryDoc` | `Issuer, AuthorizationEndpoint, TokenEndpoint, JWKSURI, UserinfoEndpoint, TokenEndpointAuthMethodsSupported, IDTokenSigningAlgValuesSupported`, `fetchedAt`, `expiresAt` | lazy on first login; TTL from cache headers clamped to [5 min, 24 h]; re-fetched on error/expiry; never at boot |
| `jwksCache` | `map[kid]crypto.PublicKey`, `fetchedAt` | with discovery; one refetch per login attempt on unknown `kid` |
| `pendingState` (extends `oauth_handler.go:37-50`) | `+ Nonce string`, `+ RedirectRejected bool`, `+ CreatedAt` (exists) | 10-min sweep **and** a 10,000-entry cap, oldest evicted on insert (FR-021) |
| `OAuthUserInfo` (`oauth_providers.go:52-57`) | `+ Groups []string`, `+ EmailVerified *bool` | per callback |

## 8. Config: `ServerEditionConfig` (`internal/config/server_edition_config.go`) — PR-A/B/C

Removed (A): `WorkspaceIdleTimeout`, `MaxUserServers`. Retained for compatibility with a boot warning when `true` (A): `StoreIDPTokens`. Added (B): `PublicURL string`, `SessionCookieSecure string` (`auto|true|false`), `OAuth.IssuerURL`, `OAuth.Scopes []string`, `OAuth.GroupsClaim`, `OAuth.EmailVerifiedPolicy`, `OAuth.DisplayName`, `OAuth.AllowInsecureIssuer bool`. Added (C): `Access *ServerEditionAccessConfig{GroupServers map[string][]string; DefaultServers []string}`. `Validate()` becomes non-mutating; `ApplyDefaults()` (new, called once at setup) owns `SessionTTL`/`BearerTokenTTL` defaults, Microsoft `TenantID=common`, `Scopes` default + `openid`, `GroupsClaim="groups"`, `EmailVerifiedPolicy=refuse_false`, `SessionCookieSecure=auto`, and the `MCPPROXY_CRED_KEY` fallback.

Personal build (A): `type ServerEditionConfig struct{ raw json.RawMessage }` with verbatim `MarshalJSON`/`UnmarshalJSON`; same for `AuthBrokerConfig`.

`AuthBrokerConfig` (server, A): removed `Header`, `HeaderFormat`; accepted modes `{oauth_connect}` only. Server-build normaliser (`loader.go`, beside the `teams` alias at `:271-285`): drops `server_edition.max_user_servers`, `server_edition.workspace_idle_timeout`, `auth_broker.header`, `auth_broker.header_format`, and the whole `auth_broker` block of a server whose `mode ∈ {token_exchange, entra_obo}` — one warning each; write-time validation refuses the same with the same message.

## 9. Config: top-level `Config` (`internal/config/config.go`) — PR-B/PR-D (edition-neutral)

- `TrustedProxies []string` `json:"trusted_proxies,omitempty"` — CIDRs or addresses; env `MCPPROXY_TRUSTED_PROXIES` (comma list); hot; `slices.Equal` clause.
- `AuditLog *AuditLogConfig` `json:"audit_log,omitempty"` — wire struct `{Enabled *bool; Path string; Stdout *bool; MaxSizeMB, MaxBackups, MaxAgeDays *int; Compress *bool}` — pointer fields so omitted-vs-explicit is observable (omitted `compress` → `true`, explicit `false` → `false`; omitted `max_size_mb` → 50, explicit `0` → validation error; explicit values always win) — resolved into a plain `ResolvedAuditLog{Enabled, Path string, Stdout bool, MaxSizeMB, MaxBackups, MaxAgeDays int, Compress bool}` by `EffectiveAuditLog`; env `MCPPROXY_AUDIT_LOG_ENABLED|PATH|STDOUT`; restart-pinned; `jsonEqual` clause. Effective default computed by `config.EffectiveAuditLog(cfg, transport)` — `transport` is `http|stdio`; under the native stdio transport (stdout carries JSON-RPC) the **absent-block** server default resolves to *disabled* + one WARN, an **explicit** `enabled:true, stdout:true` with no `path` is a `StartupError` (exit 4) at sink construction — never silently disabled (FR-014 "an explicit value always wins") — and an explicit `path` is honoured (build-tagged): personal → `Enabled=false`; server with block absent → `{Enabled:true, Stdout:true, Path:""}` on HTTP, `{Enabled:false}` on stdio.
- Build-tagged accessors (`serveredition_accessors{,_stub}.go`): `EffectiveRequireMCPAuth`, `IdPProviderFamily`, `ServerEditionEnabled`, `PublicURL`, `EffectiveAuditLog`, `ForwardedHeaders(r, trusted)` (edition-neutral, lives in `trusted_proxies.go`).

## 10. Activity records (`internal/storage/activity_models.go`) — unchanged

No new field. `UserID/UserEmail` continue to be lifted from `_auth_*`; the audit line joins to the activity record on `request_id`. Historical `credential_broker` rows stay readable and labelled (FR-034).

## 11. Telemetry (`internal/telemetry`) — PR-B

`FeatureFlagSnapshot` + `ServerEditionEnabled bool` `json:"server_edition_enabled"`, `IdPProvider string` `json:"idp_provider"` (closed enum); `HeartbeatPayload` + `UserCountBucket string` `json:"user_count_bucket,omitempty"`; `SchemaVersion = 13`. `EnvMarkers` unchanged (`IsContainer` already present).

## 12. Frontend state — PR-B/PR-C

`stores/auth.ts`: `provider {display_name}` from the public probe; `principalKind: 'tenant'|'admin'|'api_key'` from `/auth/me` role + presence of an API key; every non-allowlisted core call gated on `principalKind !== 'tenant'` (FR-041). `AdminUser` interface + `groups`, `groups_updated_at`, `subject_rebind_armed_at`; `AdminServer` + `groups: string[]` (read-only chips derived client-side from `GET /config` → `server_edition.access`, administrator only). No `contracts.ts` change (the generator emits vocabularies only; the in-sync test proves it).
