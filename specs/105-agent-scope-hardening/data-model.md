# Data Model: Agent-Token Scope Hardening

No new persisted entity types. Four existing artifacts gain an **identity** or **provenance** field; one derived value (effective scope) is named so every consumer computes it the same way.

## Effective scope (derived, per request)

```
EffectiveScope{
  Kind            CallerKind          // admin | anonymous(admin-shaped) | agent | user
  Servers         set[string] | *     // token AllowedServers ∩ profile servers (pin > URL > session); stale pin ⇒ ∅
  Permissions     set[tier]           // exact-match, no implication (read ⊉ write)
  ProfileScope    *ProfileScope       // nil when no profile is in effect
}
```
- Source: `auth.AuthContext` + `resolveActiveProfile(ctx)` (`profile_resolver.go:118-164`); predicate `serverInScope(authCtx, profileScope, name)` (`mcp_visibility.go:161-166`).
- Rule: `Servers` is computed **once per request** and reused for search, counts, ranking, risk, suggestions, refusals. Empty `AllowedServers` on an agent context is deny-all (fixtures must use `["*"]`).
- Admin short-circuit: `authCtx == nil || authCtx.IsAdmin()` with `ProfileScope == nil` ⇒ unrestricted (SC-005 baseline).

## Tool identity (A — FR-009)

`config.ToolMetadata` gains `RawName string` (new `internal/config/tool_identity.go`): the exact upstream-reported name (`ns:erase`), distinct from the canonical `server:rawName` id and from the first-colon-stripped `tool_name`.

| store | key today | key after A | migration |
|---|---|---|---|
| `tool_approvals` (bbolt) | `server` + `extractToolName(name)` (collapses `ns:erase` → `erase`) | `server` + `RawName` | one-shot: on first discovery after upgrade, a collapsed record whose raw name is not exactly present is left in place and approves **only** `erase`; `ns:erase` gets its own `pending` record |
| bleve doc id | `server:SplitN(name,":",2)[1]` | `server:` + `RawName` | index rebuild trigger (schema-version bump, precedent `tool_quarantine.go:437`) |
| `StateView` tools | raw names already | unchanged | — |
| approval hash | desc + schema (annotations excluded) | unchanged | documented: a tier change alone does not re-quarantine |

Reader: `lookupToolApproval(server, rawName)` — exact record wins; no record under active quarantine gate ⇒ `pending`; legacy collapsed record matches only its own raw name.

## Cache record provenance (B — FR-001/002)

```
Record{ …existing…, Producer *Authorization, Version uint8 }
Authorization{ Kind, AllowedServers, Permissions, ProfilePin, ProfileServers }   // existing
```
- `Version`: 0/absent = legacy ⇒ refuse every caller, delete inside the committed `Update`, stats mutate only after commit. Current = 1.
- `Kind = internal` (new writer stamp for `runtime.go:2226`, `guesser.go:349`): refuse `read_cache` for every caller, **never evict**.
- Recursive pagination: `ReadCacheResponse.Producer` (`json:"-"`) carries the **parent's** authorization to the child page store, so provenance is monotone down the chain.
- Authorization check order: deny-all guard → `reader.Unrestricted()` → kind → servers/permissions/pin/profile-set (D5).

## Rendered direct tool stamp (F — FR-008)

```
directToolStamp{ Owner string; RawName string; Tier permission }   // private struct value in mcp.Tool.Meta
```
- Written by `renderDirectTools` from the catalog entry; read by both tool filters; stripped for every caller before serialisation.
- `builtinDirectToolNames`: populated from the built-in tool constructors (`buildDescribeToolTool().Name`, …) at server construction; a name is a built-in iff present here.
- Withheld from every caller: unstamped non-built-in; empty `RawName`; catalog entry with display-name collision (existing rule).

## Log record ownership (E — FR-007)

Each per-server writer stamps `app.mcpproxy/owner=<raw server name>` (`logger.go:385`). Attributed reader: parse the last ` | ` segment as JSON, take the **first** `app.mcpproxy/owner`, filter by exact raw name **before** taking the last *n* lines; `lines_returned` = filtered length. Lines without the key are unattributed ⇒ withheld from scoped callers.

Container ownership: label `com.mcpproxy.server=<raw>` (exists) AND name `^mcpproxy-<sanitised>-[a-z0-9]{4}$`.

## Refusal shapes (G/D/B — FR-010/004/001)

One constructor per surface; hidden ≡ nonexistent in status, body and timing class:

| surface | constructor | body |
|---|---|---|
| retrieve dispatch (`call_tool_*`) scope | existing not-in-scope result (`mcp.go:2289`) | unchanged text; `Available servers:` filtered by effective scope |
| describe_tool definition mode | `visibleCorpus.notFoundResult` over authorized corpus | not-found + suggestion computed over authorized corpus only |
| direct surface hidden tool | mcp-go filter (`-32602 tool '<name>' not found`) | unchanged (scope stays in `WithToolFilter`) |
| direct surface over-tier on authorized server | handler insufficient-permission result | `Permission denied: … requires '<tier>'` (tier out of the filter) |
| read_cache (MCP + REST) | `cache key not found` | unauthorized / not-found / expired collapse for agent callers |
| `/mcp/p/*` for scoped callers | single 404 constructor | `unknown profile` without `available` |
| stored script not found | existing error without enumeration | admin keeps `Available scripts (N)` |
| tail_log | `tailLogNotFound` (exists) | unchanged |
