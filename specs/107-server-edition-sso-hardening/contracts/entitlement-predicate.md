# Entitlement predicate, owner resolution and session-principal hook

## 1. `entitledServerNamesFor(user *users.User, isAdmin bool) ([]string, error)` + wrapper `entitledServerNames(userID, isAdmin)` — `internal/serveredition/api/user_handlers.go:808` (PR-C)

The **only** function that answers "may this user see, use, mint against, connect to or diagnose server *N*" for a tenant (FR-004). Non-nil result always (empty = deny-all). Today `entitledServerNames(userID, isAdmin)` never loads the user record (`ListUserServers(userID)` + the live admin servers, `:809-849`); the group term (FR-009) needs `User.Groups`, so the core becomes **`entitledServerNamesFor(user, isAdmin)`**, which takes the already-loaded record, and `entitledServerNames(userID, isAdmin)` is the wrapper that performs exactly one `GetUser` and calls it — the REST door handlers use the wrapper (one load per request), the owner resolver (§2) calls the core with the record it already loaded, so an agent-token authentication stays at **one** `GetUser`.

```
personal(u)  = names of u's personal records minus collidesWithAdminConfig (unchanged, :853-869)
shared       = { s ∈ live.Servers : s.Shared }                       # live via AdminServersProvider
grant(u)     = ⋃ access.group_servers[g] for g ∈ u.Groups  ∪  (access.default_servers if no g matches any key)
               where "*" expands to every s ∈ shared
entitled(u)  = personal(u) ∪ { s ∈ shared : access == nil ∨ s ∈ grant(u) }          # tenants
entitled(admin) = whole configuration (unchanged) — used by mint/rotate and the literal "*" only
```

Rules: `access` read live through `ServerEditionConfigProvider`; group values compared exactly (case-sensitive); server names compared exactly on the bare name; a group with no map entry contributes nothing; `u.Groups == nil` (pre-upgrade record) = matches no key; errors from the user store or a nil live config → error (fail closed → `ErrAgentTokenScopeUnavailable` / 503 on REST); a nil `user` handed to the core → error (never an empty grant that could be mistaken for "no groups").

Helper `tenantEntitled(r, userID) (set map[string]struct{}, isAdmin bool, err error)` wraps it for the door handlers. **Guard test** (`user_handlers_shared_guard_test.go`): walks the non-test AST of `internal/serveredition/api` and fails on any selector `.Shared` outside `entitledServerNamesFor` (the core; the `entitledServerNames` wrapper reads nothing itself), the administrator projection helper `adminSharedProjection` (the one permitted second reader, used only for `admin_user` list doors — FR-004 "administrator projection unchanged") and the administrator-only file `admin_handlers.go` (exempted by file name: the share toggle `:551-597` writes `found.Shared = req.Shared` and the `/admin/servers` enrichment `:637` reads `sc.Shared` — `/admin/servers` is the whole-config surface, not a tenant door; the walk is untyped, so the file exemption also covers `req.Shared` on the request struct). The guard is behaviour-red on HEAD (seven tenant-door sites trip it).

## 2. `storage.SetAgentTokenOwnerResolver` — replaces `SetAgentTokenOwnerGate` + `SetAgentTokenScopeResolver` (`internal/storage/agent_tokens.go:846,865`) (PR-C)

```go
type OwnerResolution struct {
    Active         bool
    UserID, Email  string
    Provider, Role string   // role derived live from admin_emails
    Entitled       []string // non-nil; the NARROWED grant = narrowScopeToEntitled(granted, entitledServerNamesFor(user, isAdmin), isAdmin)
                            // (`user_handlers.go:978ff`, today's NarrowTokenServerScope body): ["*"] stays literal for an administrator,
                            // is materialised into the entitlement set for a tenant, [] when nothing survives
}
type AgentTokenOwnerResolver func(userID string, granted []string) (OwnerResolution, error)
func (m *Manager) SetAgentTokenOwnerResolver(r AgentTokenOwnerResolver)
```

`ValidateAgentToken` (today `:911-939`): when `token.UserID != ""` and a resolver is installed — one call; `err != nil` → `ErrAgentTokenScopeUnavailable` (unless the resolver reports the owner missing/disabled, which → `ErrAgentTokenOwnerInactive`); `!Active` → `ErrAgentTokenOwnerInactive`; else `token.AllowedServers = intersectAllowedServers(granted, res.Entitled)` — `res.Entitled` is already the narrowed grant, so the intersection is the storage-side narrow-only fence exactly as today (`intersect(["*"], ["*"]) = ["*"]`: an administrator's literal star survives, FR-009 / spec Definitions "literal for administrators"; `res.Entitled` MUST NOT be the raw whole-configuration list, which the wildcard rule of `intersectAllowedServers` (`agent_tokens.go:697-738`) would materialise into a frozen snapshot — round-6 finding) **and** `token.OwnerEmail/OwnerProvider/OwnerRole = res.Email/Provider/Role` — three new `json:"-"` fields on `auth.AgentToken` (`data-model.md` §3; never written to BBolt) that `AgentToken.AuthContext()` copies into `AuthContext.Email/Provider/Role`. This is the only implementable carrier: `ValidateAgentToken` returns the token (`:943`) and every caller builds the context from it (`httpapi/server.go:580`, `server/server.go:427`, `sse_scope.go:43`). The resolver, built in `setupMultiUserOAuth`, does exactly one `userStore.GetUser(userID)`, computes the entitlement set with `entitledServerNamesFor(user, isAdmin)` (§1) on that record and returns `Entitled = narrowScopeToEntitled(granted, thatSet, isAdmin)`. Installed **first** in `setupMultiUserOAuth` (before any fallible step — the existing fail-closed ordering comment at `setup.go:45-56` still applies). Ownerless tokens are untouched.

`intersectAllowedServers(current, proposed)` returns `[]string{}` (non-nil) when the result is empty or `proposed` is empty (`:707-709`, `:735-737` today return nil).

## 3. Restricted marker — `internal/preflight.ScopeInputs` (PR-C)

`ScopeInputs.Restricted bool`: set by `internal/httpapi/preflight.go:139-146` whenever `authCtx != nil && !authCtx.IsAdmin()`. `normalizeTokenServers` (`preflight/scope.go:150-162`): `Restricted && len(servers)==0` → deny-all (every id out of scope); unrestricted callers keep "empty = no restriction". `ResolveScope` gains no other change; MCP in-band preflight already enumerates through `serverInScope` (`preflight_glue.go:200-233`).

## 4. Session-principal hook — `internal/httpapi` (PR-C)

```go
// internal/auth/context.go — the type lives in auth, not httpapi: httpapi imports auth,
// so an AuthContext field of an httpapi type would be an import cycle. Defined by T076
// (PR-C phase C.2) because T078 in the same phase records it; T083 (C.3) only consumes it.
type CredentialKind string // socket|api_key|bearer_jwt|agent_token|cookie|anonymous

// internal/httpapi/session_principal.go
type SessionPrincipalResolver func(r *http.Request, kind auth.CredentialKind, value string) (*auth.AuthContext, error)
func (s *Server) SetSessionPrincipalResolver(f SessionPrincipalResolver)   // nil in the personal build
```

Presence of a credential source is header/query **membership** (`r.Header.Values("X-API-Key")`, `r.URL.Query().Has("apikey")`), so an empty `X-API-Key:` or `Authorization: Bearer ` is a present, failing credential (401) and never falls through to the cookie.

Called by `apiKeyAuthMiddleware` in exactly two places: (3) a bearer that is neither `mcp_agt_` nor the global key → `kind=bearer_jwt`; (5) no `X-API-Key`/bearer/`?apikey=` present and the `mcpproxy_session` cookie is → `kind=cookie`. Returns `UserContext` (tenant: `AllowedServers` = entitlement set, materialised) or `AdminUserContext`, both with `CredentialKind` set; `(nil, nil)` = not a principal → 401. The resolver is built in `internal/serveredition/setup.go` from the same `userStore.GetUser` + `ValidateBearerToken` + live `IsAdminEmail` the server-edition middleware uses (`middleware.go:138-214`, `:228-246`); it never reads the cookie when asked for a bearer and vice versa.

Immediately after a `user`-typed principal is installed, `tenantSessionAllowlist.Allows(method, path)` decides; on `false` the fixed 403 body is written and the handler never runs. The SSE refresher (`sse_scope.go:17-43`) calls the same resolver with the original `(kind, value)` before each frame; `(nil, nil)` or an error ends the stream.

## 5. `auth.AuthContext` additions (PR-C/PR-D)

`CredentialKind auth.CredentialKind` (defined in this package); `IsSessionPrincipal() bool` (`cookie|bearer_jwt`); `auth.ParseTokenExpiry(expiresIn string, now time.Time) (time.Time, error)` — the expiry rule shared by core `/tokens` and `/user/tokens` (positive, ≤ 365 d; `httpapi.parseExpiry` becomes a wrapper); `CanRevealSecrets()` returns `false` for session principals; `UserContext(...)` takes the entitlement set (never nil). `AgentToken.AuthContext()` **changes** (T076, `internal/auth/agent_token.go`): it additionally copies the token's non-persisted `OwnerEmail/OwnerProvider/OwnerRole` into `Email/Provider/Role`, which storage stamped on the validated token value (§2).

## 6. Freshness bound (FR-011, documented in `docs/features/agent-tokens.md` and the deploy guide)

`session_ttl + max(bearer_token_ttl, longest owned token expiry ≤ 365 d)`; administrator `disable` is immediate through the owner resolution; a JWT can neither renew itself nor mint/rotate tokens (session-cookie-only doors).
