# Phase 1 Data Model: In-Proxy Profiles

No persistent storage entities. Two in-memory types + one derived set.

## 1. ProfileConfig (config entity)

Declared in `internal/config/profiles.go`, embedded in `Config.Profiles []ProfileConfig` (after `Servers`, `config.go:109`).

```go
type ProfileConfig struct {
    Name    string   `json:"name"`              // URL slug, validated
    Servers []string `json:"servers"`           // references to mcpServers[].name
}
```

**Validation rules** (run in `Config.Validate()`, `loader.go:1521`):

| Rule | Source | Severity |
|------|--------|----------|
| `Name` matches `^[a-z0-9][a-z0-9_-]{0,62}$` | FR-007 | **fatal** — reject load, point at offending entry |
| `Name` ∉ reserved `{all, code, call, p}` | FR-007 | **fatal** |
| `Name` unique across all profiles | FR-014 | **fatal** — diagnostic names both occurrences |
| each `Servers[i]` exists in `mcpServers[].name` | FR-015 | **warning** — load, log, omit that server |
| `Servers` empty | edge case | **warning** — legal "deny everything" placeholder |

**Round-trip**: `omitempty` on `Config.Profiles` keeps SC-004 (absent ⇒ byte-identical via `json.MarshalIndent` in `SaveConfig`).

## 2. ProfileScope (request entity)

Declared in `internal/profile/context.go` (~30 LOC, peer of `internal/auth/context.go`). Immutable, request-scoped, injected by `profileMiddleware`.

```go
type ProfileScope struct {
    Name    string              // resolved profile slug (for error messages + activity metadata)
    servers map[string]struct{} // effective set after warn-skip of unknown servers
}

func (p *ProfileScope) Allows(serverName string) bool   // membership test; nil receiver ⇒ no profile ⇒ allow-all (the /mcp path)

func WithProfileScope(ctx context.Context, p *ProfileScope) context.Context
func ProfileScopeFromContext(ctx context.Context) *ProfileScope   // nil when request did not enter via /mcp/p/<slug>
```

`Allows` semantics: a **nil** `*ProfileScope` (request came through `/mcp`, `/mcp/code`, `/mcp/call`) means no profile filtering — preserves FR-010. A non-nil scope filters to its `servers` set.

## 3. Effective Server Set (derived, per request)

Computed at each scope site as the intersection of all active scoping primitives:

```
effective(server) =
       ProfileScope.Allows(server)                      // FR-002/004  (nil scope ⇒ true)
   AND (!enforceAgentScope OR authCtx.CanAccessServer)   // FR-005 Spec 028 token scope
   AND server is enabled                                 // edge case: disabled excluded
   AND server is not quarantined                         // edge case: quarantined excluded
   AND server visible to this user                       // FR-013 Spec 029 per-user (server edition)
```

The two new conditions are the `ProfileScope.Allows` checks; the rest already exist. The profile and token checks are **independent** so the rejection error can name the responsible primitive (FR-012):

- profile-blocked: `"server '<s>' is not in profile '<name>'"`
- token-blocked: existing `"Server '<s>' is not in scope for this agent token"`

## State transitions

`ProfileScope` is immutable for a single request's lifetime. It is resolved **per request** by the profile middleware against the current config snapshot, so a config hot-reload (`configsvc.Update`) takes effect immediately on the next request — including requests on an already-open session. There is no longer-lived "resolved snapshot" pinned to a connection. There is no runtime "active profile" mutation in the MVP (deferred — see spec Out of Scope).

> Per-request resolution (rather than snapshot-until-reconnect) is the deliberate design: it lets an operator narrow or revoke a profile and have it apply at once, which is the safer failure mode. Decision recorded in PR #622 review round 1 (Codex finding #3).
