# Phase 0 Research: In-Proxy Profiles

All design questions were resolved in `spec.md` (Resolved Design Decisions / Assumptions / Implementation Design) and confirmed against current code on 2026-06-07. No outstanding NEEDS CLARIFICATION.

## Decision 1 — Routing: single `/mcp/p/` prefix handler + middleware, not per-profile server instances

- **Decision**: Register one handler for the `/mcp/p/` prefix that strips the slug, resolves the profile from the live config snapshot, injects `ProfileScope` into the request context, and delegates to the **existing** retrieve_tools-mode MCP server (`p.server`).
- **Rationale**: The routing-mode endpoints (`/mcp/all|code|call`) register a separate `StreamableHTTPServer` per path at startup via `mux.Handle()` over Go's `http.ServeMux` (verified `server.go:1674-1690`, `GetMCPServerForMode` in `mcp_routing.go:600`). `http.ServeMux` cannot de-register routes at runtime, so that pattern can't be mirrored for N hot-reloadable profile slugs. A prefix handler + context injection mirrors how `mcpAuthMiddleware` injects `AuthContext` and needs zero per-profile server construction.
- **Alternatives rejected**: (a) per-profile server instances — impossible to hot-reload with ServeMux; (b) swapping to chi just for this — unnecessary churn; the prefix handler works on the existing mux.

## Decision 2 — Filtering: parallel auth-type-independent check, not `AuthContext.AllowedServers` overwrite

- **Decision**: Add a second, independent filter condition at each existing scope site, gated on `profileScope != nil` (not on `enforceAgentScope`).
- **Rationale**: The existing gate is `enforceAgentScope := authCtx != nil && !authCtx.IsAdmin()` (`mcp.go:1113`). An unauthenticated `/mcp/p/...` connection is assigned `AdminContext()`, where `IsAdmin()==true` ⇒ `enforceAgentScope==false` and `CanAccessServer` returns true unconditionally. Stuffing the profile's servers into `AuthContext.AllowedServers` would therefore be bypassed for the common unauthenticated case. A parallel check runs for every auth type, yields the FR-005 intersection and the FR-012 distinct error messages for free, and requires **no** change to `AuthContext`, the `agent_tokens` bucket, or token validation.
- **Mandatory regression test**: "An unauthenticated connection at `/mcp/p/<slug>` is still filtered to the profile's servers." Must pass before merge.
- **Alternatives rejected**: overwriting `AllowedServers` (insecure for admin/unauth, conflates two primitives, breaks FR-012 attribution).

## Decision 3 — Index: filter at the call site, do not partition BM25

- **Decision**: Filter `retrieve_tools` results and `call_tool_*` targets by intersecting the active profile's server set; leave the BM25 index global.
- **Rationale**: Server cardinality is typically dozens; a per-request set lookup over an already-paginated result is cheap and keeps Constitution I (<100ms BM25) intact. A per-profile `profile` index field would complicate hot reload, profile rename, and statistics with no measurable payoff at current scale (Constitution III: read from source of truth, don't duplicate).
- **Alternatives rejected**: index-level `profile` field (deferred to a future spec if scale demands).

## Decision 4 — Unknown server reference → warn-and-skip (FR-015)

- **Decision**: A profile referencing a non-existent server name loads with a non-fatal warning; that server is omitted from the effective set.
- **Rationale**: Parity with Spec 028 agent tokens; config must stay loadable after a server rename. Hot-error variant not needed.

## Decision 5 — `/mcp` semantics unchanged; profiles purely additive

- **Decision**: `/mcp` always exposes the full union regardless of whether `profiles` is configured. Profiles are opt-in narrowing via `/mcp/p/<slug>` only.
- **Rationale**: No breaking change for existing `/mcp` clients (SC-002). A `strict` mode (require a profile) is a possible later spec.

## Decision 6 — Reserved slugs `{all, code, call, p}`

- **Decision**: Reject these four slugs at config load.
- **Rationale**: `/mcp/all|code|call` are live bound routing endpoints (Spec 031); `p` is the profile prefix itself. There is no actual path collision (profiles live under `/mcp/p/`), but reserving them avoids operator confusion.

## Decision 7 — Hot-reload via existing `configsvc` atomic snapshot

- **Decision**: Resolve the profile per request from `runtime.Config()` (lock-free `atomic.Value` snapshot via `configsvc`). In-flight sessions keep their resolved snapshot until reconnect; new connections pick up new profiles.
- **Rationale**: Mirrors how config hot-reload already works for active connections (`configsvc/service.go`, `runtime.go:282`). No new reload machinery; adding `profiles` to the hot-reloadable field set is the only change.

## Seam verification summary (2026-06-07)

| Seam | Location | Status |
|------|----------|--------|
| Routing modes | `server.go:1674-1690`, `mcp_routing.go:600` | MATCHES |
| Auth middleware + AuthContext | `server.go:167`, `auth/context.go:14-27,49,72,102` | MATCHES |
| retrieve_tools scope gate | `mcp.go:1113` | MATCHES |
| call_tool_* scope gate | `mcp.go:1529` (`handleCallToolVariant`) | MATCHES |
| Config struct | `config.go:101,109` (`Servers`), `loader.go:352,1521` | MATCHES |
| Activity metadata | `contracts/activity.go:49` (`Metadata`) | MATCHES |
| Hot-reload snapshot | `configsvc/service.go`, `runtime.go:282` | MATCHES |
