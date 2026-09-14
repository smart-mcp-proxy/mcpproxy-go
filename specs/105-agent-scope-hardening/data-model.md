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
- Authorization check order (FR-001, D5): **caller kind first** — administrator reader qualifies for any snapshot; agent reader never qualifies for an administrator snapshot; then, agents only: deny-all guard (empty effective profile → false) → allowed servers ⊇ → permissions ⊇ → pin equality → profile-set ⊇. `Unrestricted()` renamed `IsAdministrator()`.

## Rendered direct tool stamp (F — FR-008)

```
directToolStamp{ Owner string; RawName string; Tier permission }   // private struct value in mcp.Tool.Meta
```
- Written by `renderDirectTools` from the catalog entry; read by both tool filters; stripped for every caller before serialisation.
- `builtinDirectToolNames`: populated from the built-in tool constructors (`buildDescribeToolTool().Name`, …) at server construction; a name is a built-in iff present here.
- Withheld from every caller: unstamped non-built-in; empty `RawName`; catalog entry with display-name collision (existing rule).

## Log record ownership (E — FR-007)

No new field: the existing zap field `server=<raw server name>` (`logger.go:385`) is the ownership signal (D8). Attributed reader: console encoder — scan ` | {` boundaries left to right, accept the first whose suffix decodes as exactly one complete JSON object, read `server` from it; JSON encoder — the whole line is the object. Filter by exact raw name **before** taking the last *n* lines; `lines_returned` = filtered length. Lines with no accepted boundary are unattributed ⇒ withheld from scoped callers. Subject-evidence rule (D8 rule 3): `server` match is necessary but not sufficient — a container record is attributable only when it carries `container_owner=<raw name>` (new field on housekeeping records, taken from the `com.mcpproxy.server` label) equal to the requested server; container records without it (all pre-upgrade records, ID-only records) and callback records naming another server are withheld. Sanitised names are never evidence (`a/b` ≡ `a-b`). Producer rule: child-controlled text is only ever a field value, never the message.

Container ownership: label `com.mcpproxy.server=<raw>` (exists) AND name `^mcpproxy-<sanitised>-[a-z0-9]{4}$`.

## Refusal shapes (G/D/B — FR-010/004/001)

One constructor per surface; hidden ≡ nonexistent in status, body and timing class:

| surface | constructor | body |
|---|---|---|
| retrieve dispatch (`call_tool_*`) scope | existing not-in-scope result (`mcp.go:2289`) | unchanged text; `Available servers:` filtered by effective scope |
| describe_tool definition mode | `visibleCorpus.notFoundResult` over authorized corpus | not-found + suggestion computed over authorized corpus only |
| direct surface hidden tool | mcp-go filter (`-32602 tool '<name>' not found`) | unchanged (scope stays in `WithToolFilter`) |
| direct surface over-tier on authorized server | handler insufficient-permission result | `Permission denied: … requires '<tier>'` (filter passes over-tier tools through; tier-first over callability, D13) |
| read_cache (MCP + REST) | `cache key not found` | unauthorized / not-found / expired collapse for agent callers |
| `/mcp/p/*` for scoped callers | single 404 constructor | `unknown profile` without `available` |
| stored script not found | existing error without enumeration | admin keeps `Available scripts (N)` |
| tail_log | `tailLogNotFound` (exists) | unchanged |
