# Data Model: Schema-Deferred Direct Mode

**Spec**: [spec.md](spec.md) · **Plan**: [plan.md](plan.md)

All entities are in-memory; no BBolt or Bleve schema changes.

## 1. Direct-surface catalog snapshot (`directCatalog`) — FR-017

Immutable per-rebuild record of everything the direct surface exposes. Built inside
the direct rebuild from `upstreamManager.DiscoverTools`, published by atomic
pointer swap; never mutated after publication.

| Field | Type | Source | Consumers |
|---|---|---|---|
| `entries` | ordered slice of `directCatalogEntry` | sorted DiscoverTools projection | listing rendering (order source) |
| `byDisplayName` | map display name → entry | one pass over the sorted projection; **colliding names are withheld** — neither entry is registered, warning logs both origins (R13 rule 5) | describe_tool direct-id resolution + the listing filters' `(server, tool)` identity; = the registration mapping |
| `byCanonical` | map `server:tool` → entry | same pass | describe_tool canonical-id resolution |
| `serializationMode` | `full` \| `deferred` | live config at build time | FR-014 rebuild guard (compare vs live effective mode) |
| `generation` | uint64 | monotonic counter, incremented once per published catalog | logged on every publish beside entry count + mode; the skew tests assert exactly one increment per paused rebuild and none on a guarded no-op reload, which is what makes "old or new generation?" observable rather than inferred (R13) |

`directCatalogEntry`:

| Field | Type | Notes |
|---|---|---|
| `displayName` | string | `serverName__toolName` (unchanged naming) |
| `serverName`, `toolName` | string | original pair the handler closes over — dispatch never parses `displayName` |
| `description` | string | upstream description (pre-`[server]`-prefix) — the source the renderer reads |
| `renderedDescription` | string | the **exact** string handed to `SetTools` for this entry in this generation, filled in by `renderDirectTools`: `[server] …` in full mode, plus the name+signature suffix in deferred mode when `Peek` hit. Immutable once published. This — never a recomputation — is what the R13 rule 5 discriminator compares against `mcp.Tool.Description` |
| `paramsJSON` | string | upstream input schema — validation + describe source |
| `outputSchemaJSON` | string | optional; describe `output_schema` source |
| `hash` | string | Spec-032 SHA-256 — `toolsig.Cache.Peek` key + validator memo key |
| `annotations` | `*config.ToolAnnotations` | listing + `call_with`/permission derivation |
| `requiredPermission` | string | `requiredPermissionForDirectTool(annotations)` over the **upstream** `*config.ToolAnnotations` (`mcp_direct_scope.go:18`) — absorbs today's `directToolPermissions` map and is the tier on **every** path: listing filter, describe resolver, and (re-derived from its own capture) dispatch. It is used only after the rule-5 discriminator confirms the catalog and registry entries are the same generation. It is **never** derived from the registered `mcp.Tool.Annotations`: that is `mcp.ToolAnnotation` (wrong type for `contracts.DeriveCallWith`) and carries mcp-go's `NewTool` defaults — `destructiveHint=true` survives unless the upstream explicitly overrides it, so reading the tier there would classify nearly every tool destructive and disagree with dispatch (R13 rule 3, corrected in round 8) |

**Invariants**
- Entry and its dispatch handler are built from the same `directCatalogEntry` in
  one pass, so a handler always validates against the definition its own
  registration advertised. This is handler↔schema atomicity, and it is the only
  atomicity the design claims: the `SetTools` call and the catalog publish are
  two publications and cannot be one transaction (R13). What holds across them is
  a safety property, not atomicity — *no request observes a state less
  restrictive than both generations, and no request receives a definition for a
  name the registry is not currently serving* — delivered by R13's **five** rules
  (`SetTools` first then publish, the builder returning the catalog so the
  publisher can swap it; filters deny on catalog miss; the tier taken from the
  catalog entry's upstream-derived `requiredPermission` behind the discriminator;
  describe requires `directServer.GetTool(name) != nil` as well as catalog
  visibility; and no display name ever denotes two origins).
- Membership is decided by this snapshot, not index presence: listed ⇒ describable
  (definition mode) and validatable; unlisted ⇒ `not_found`, even if indexed.
- Collisions (`a` + `b__c` vs `a__b` + `c`): **both entries withheld**, warning
  logs both origins — an ambiguous display name is never served, so listing,
  describe and dispatch agree by construction. Not first-writer-wins: that still
  lets one name denote different origins across a rebuild (R13 rule 5).
- A display name resolved from the catalog is used only after the entry's stored
  `renderedDescription` matches the registered `mcp.Tool.Description`; a mismatch
  means the two publications disagree for that name, and the entry is withheld
  (filter denies, describe answers `not_found`) rather than served from either
  side. The comparison MUST use the stored value, never a re-render: the deferred
  suffix comes from `toolsig.Cache`, which mutates independently of direct
  rebuilds (`Warm` adds entries, `RetainHashes` evicts them —
  `internal/toolsig/cache.go:79`, `:108`), so a miss at registration followed by a
  hit at filter time — or the reverse — would manufacture a false mismatch and
  leave a still-registered tool unlisted and undescribable until the next rebuild.
  That would break "listed ⇒ describable" persistently, not just within the
  publication window.

## 2. Deferred tool entry — FR-004

The `tools/list` rendering of one catalog entry under `deferred`:

| Field | Value |
|---|---|
| `name` | `displayName` (unchanged) |
| `description` | `[server] <description>` + `"\n"` + `<toolName>` + `Signature.Sig` when `Peek(hash)` hits; without that suffix on a cache miss (entry still listed — FR-005). `Sig` is the parameter list only and carries no tool name, so the renderer prepends it; `Signature.Desc` is unused here (deferred keeps the full description) |
| `inputSchema` | exactly `{"type":"object"}` — never `{}`, never absent, no upstream properties/required. **Emitted via `mcp.NewToolWithRawSchema`**, not `mcp.NewTool`: mcp-go's schema marshaller always adds `"properties":{}` and `"required":[]` (research.md R11 / D9) |
| `outputSchema` | absent (stripped — research.md R2); `Tool.MarshalJSON` omits it when unset |
| annotations | byte-identical to full mode. The raw-schema constructor takes no `ToolOption`s **and leaves every hint `nil`**, while `mcp.NewTool` seeds `readOnly=false, destructive=true, idempotent=false, openWorld=true` before options run — so the deferred renderer seeds those same defaults first and then applies the upstream overrides. Copying only the upstream hints would marshal a different (or empty) `annotations` object for the same tool (R11/D9) |

Full-mode rendering is byte-identical to pre-feature behavior (FR-015) modulo the
appended `describe_tool` built-in.

## 3. Serialization mode resolution — FR-001

- Config: `direct_tool_response_mode` ∈ {`full` (default), `deferred`}; invalid
  values fail validation naming the accepted set.
- Precedence: env `MCPPROXY_DIRECT_TOOL_RESPONSE_MODE` > file value; serve flag
  applies only when explicitly set (Spec-085 flag pattern).
- Read path: one helper, `effectiveDirectToolResponseMode()`, mirroring
  `effectiveToolResponseMode` (`internal/server/profile_resolver.go:57`) — reads
  the live snapshot via `p.currentConfig()`, defaults to `full` on empty, and is
  the ONLY read site, so the renderer, the catalog's recorded
  `serializationMode`, and the FR-014 rebuild guard cannot resolve differently.
  Unlike its retrieve_tools sibling it takes no per-call `detail` override: MCP
  `tools/list` has no parameters (spec Non-Goals). Resolved at catalog build time
  and recorded on the catalog; never affects membership (FR-008).
- `routing_mode` value set unchanged; literal `schema_deferred` → validation error
  naming the composition (FR-002).

## 4. Tool id forms (describe_tool on the direct surface) — FR-011

| Form | Example | Resolution |
|---|---|---|
| canonical | `github:create_issue` | `splitServerTool` → `byCanonical` |
| direct | `github__create_issue` | `byDisplayName` (registration mapping; never re-parsed) |

Both forms resolve to the same entry; per-id error vocabulary on this surface:
`not_found` for any id invisible to this session (no reason codes, no existence
signal); visible ids never error in definition mode (snapshot-backed definition),
and under `check:true` return the informative availability verdict from the shared
preflight evaluator.

**The catalog is also the resolution source for the LISTING side** (D10): the two
direct tool filters (`filterDirectModeToolsForAuth`,
`filterDirectToolsForAgentCallability`) resolve display names through
`byDisplayName` rather than `ParseDirectToolName`'s first-`__` split — for the
`(server, tool)` identity **and** the permission tier, and only after the
description discriminator confirms the catalog entry and the registered entry are
the same generation. The tier is the entry's own `requiredPermission`, derived
from the upstream annotations exactly as dispatch derives it; it is deliberately
NOT `DeriveCallWith` over the filtered `mcp.Tool.Annotations`, which carry
mcp-go's constructor defaults and would read "destructive" for almost every tool
(R13 rule 3). The residual this accepts — an annotations-only change inside the
window — is recorded in plan §Complexity Tracking and is bounded by dispatch
re-deriving the tier from its own captured annotations. Without the
catalog-backed identity, a server whose name contains
`__` is evaluated as a different (nonexistent) server by the listing and as the
real pair by describe, producing a describable-but-unlisted id — the exact FR-011
disclosure the catalog exists to prevent.

**Suggestion surfaces are part of the parity contract** (D10): `did_you_mean`
(check mode) and `suggestCanonicalToolID` (definition mode) must draw only from
ids this session's direct listing would include, or be suppressed on this
surface. The shared preflight corpus (`preflight.visibleCorpus`) filters at the
server level only — no permission tier, no tool-level gate — so it is not
listing-parity by itself.

## 5. describe_tool definition (additive delta) — research.md R2

Existing definition object `{name, description, inputSchema, server, annotations,
call_with}` gains optional `output_schema` (raw upstream output schema object),
present iff the resolved tool declares one, on all surfaces, added at the
definition-assembly seam (not `buildFullToolEntry`).

## 6. State transitions

- **Upstream refresh** (`servers.changed`): full catalog rebuild → `SetTools`
  (includes `describe_tool` by construction — FR-018) → mcp-go emits
  `tools/list_changed`.
- **Init** (`initRoutingModeServers`, D15/R14): one initial rebuild before the
  listeners are wired — with no upstreams connected this publishes the built-in
  set (`describe_tool`) and an empty catalog recording the effective mode, so the
  direct surface is never observed empty and the FR-014 guard always has a mode
  to compare against.
- **Config hot-reload** (`config.reloaded`): rebuild iff live effective
  serialization ≠ `catalog.serializationMode`; otherwise no-op, no notification
  (FR-014).
- **Signature cache warm-up lag / tool-level quarantine**: entry listed without
  signature until the tool is approved and indexed; schema stays reachable via
  describe_tool (snapshot-backed) throughout — listing-only degradation.
