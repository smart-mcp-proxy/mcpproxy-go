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
| `byDisplayName` | map display name → entry | built with first-writer-wins collision guard (logged) | describe_tool direct-id resolution; = the registration mapping |
| `byCanonical` | map `server:tool` → entry | same pass | describe_tool canonical-id resolution |
| `serializationMode` | `full` \| `deferred` | live config at build time | FR-014 rebuild guard (compare vs live effective mode) |

`directCatalogEntry`:

| Field | Type | Notes |
|---|---|---|
| `displayName` | string | `serverName__toolName` (unchanged naming) |
| `serverName`, `toolName` | string | original pair the handler closes over — dispatch never parses `displayName` |
| `description` | string | upstream description (pre-`[server]`-prefix) |
| `paramsJSON` | string | upstream input schema — validation + describe source |
| `outputSchemaJSON` | string | optional; describe `output_schema` source |
| `hash` | string | Spec-032 SHA-256 — `toolsig.Cache.Peek` key + validator memo key |
| `annotations` | `*config.ToolAnnotations` | listing + `call_with`/permission derivation |
| `requiredPermission` | string | `requiredPermissionForDirectTool(annotations)` — absorbs today's `directToolPermissions` map; permission-tier gate for listing filter AND describe resolver |

**Invariants**
- Entry and its dispatch handler are built from the same `directCatalogEntry` in
  one pass and installed by one `SetTools` call (atomicity, FR-017).
- Membership is decided by this snapshot, not index presence: listed ⇒ describable
  (definition mode) and validatable; unlisted ⇒ `not_found`, even if indexed.
- Collisions (`a` + `b__c` vs `a__b` + `c`): deterministic sorted iteration,
  first writer kept, loser logged — describe and dispatch agree by construction.

## 2. Deferred tool entry — FR-004

The `tools/list` rendering of one catalog entry under `deferred`:

| Field | Value |
|---|---|
| `name` | `displayName` (unchanged) |
| `description` | `[server] <description>` + `"\n"` + compact signature when `Peek(hash)` hits; without the signature suffix on a cache miss (entry still listed — FR-005) |
| `inputSchema` | exactly `{"type":"object"}` — never `{}`, never absent, no upstream properties/required |
| `outputSchema` | absent (stripped — research.md R2) |
| annotations | unchanged from full mode |

Full-mode rendering is byte-identical to pre-feature behavior (FR-015) modulo the
appended `describe_tool` built-in.

## 3. Serialization mode resolution — FR-001

- Config: `direct_tool_response_mode` ∈ {`full` (default), `deferred`}; invalid
  values fail validation naming the accepted set.
- Precedence: env `MCPPROXY_DIRECT_TOOL_RESPONSE_MODE` > file value; serve flag
  applies only when explicitly set (Spec-085 flag pattern).
- Read path: live snapshot (`p.currentConfig()`), resolved at catalog build time
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

## 5. describe_tool definition (additive delta) — research.md R2

Existing definition object `{name, description, inputSchema, server, annotations,
call_with}` gains optional `output_schema` (raw upstream output schema object),
present iff the resolved tool declares one, on all surfaces, added at the
definition-assembly seam (not `buildFullToolEntry`).

## 6. State transitions

- **Upstream refresh** (`servers.changed`): full catalog rebuild → `SetTools`
  (includes `describe_tool` by construction — FR-018) → mcp-go emits
  `tools/list_changed`.
- **Config hot-reload** (`config.reloaded`): rebuild iff live effective
  serialization ≠ `catalog.serializationMode`; otherwise no-op, no notification
  (FR-014).
- **Signature cache warm-up lag / tool-level quarantine**: entry listed without
  signature until the tool is approved and indexed; schema stays reachable via
  describe_tool (snapshot-backed) throughout — listing-only degradation.
