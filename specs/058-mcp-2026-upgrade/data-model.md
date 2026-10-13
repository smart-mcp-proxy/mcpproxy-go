# Data Model: MCP 2026-07-28 Upgrade

**Feature**: 058-mcp-2026-upgrade · **Phase**: 1 · **Date**: 2026-09-03

This upgrade adds no persisted entity and no database migration. What it changes is **which identity travels with a request** and **what era a hop speaks**. The entities below are therefore mostly in-memory or on-the-wire; the two persistence touch-points are called out explicitly.

## 1. Protocol Era

The single most load-bearing concept. Every hop has one, and mcpproxy has two hops.

| Attribute | Values | Source |
|---|---|---|
| Era | `legacy` (≤ `2025-11-25`) / `modern` (`2026-07-28`) | `mcp.IsModernProtocol(v)` |
| Client-facing era | pinned `legacy` during rollout | `server.WithStreamableHTTPProtocolVersions` |
| Upstream-facing era | pinned `legacy` during rollout | `initRequest.Params.ProtocolVersion` |
| Per-request accessor | `server.IsModernRequest(ctx)` | populated before every hook and dispatch |

**Invariant**: the two eras are independent. A single proxied call may be modern on the client side and legacy upstream, or the reverse. Anything that assumes one era for both hops is wrong.

**State transitions**: none persisted. Era is derived per request from `_meta` (modern) or the `initialize` handshake (legacy). There is no era stored anywhere.

## 2. Session (existing, now era-dependent)

`SessionStore.sessions map[string]*SessionInfo` — unchanged in shape, but **only ever populated for legacy clients**, because `initialize` does not exist in the modern era and the registration hooks never fire.

| Field | Legacy | Modern |
|---|---|---|
| Session id | minted, stable for the connection | `""` (ephemeral per-request session) |
| Client name / version | captured at `initialize` | must come from `_meta` clientInfo |
| Workspace roots | fetched via server-initiated request | unavailable (no server-initiated requests) |
| Per-session token stats | accumulated | not accumulated |
| Active profile (tier 3) | settable via `set_profile` | refused |

**Validation rule**: guards must test `sessionID == ""`, never `session == nil`. A modern request always has a non-nil session object.

**Open modelling decision** (carried to tasks): whether `storage.SessionRecord` and per-session token stats become legacy-only, or are re-keyed on the request-carried identity that `runtime.WorkSessionIdentity` already approximates.

## 3. Profile Scope (existing, unchanged shape)

Resolution is a three-tier precedence chain. This upgrade changes only tier 3's availability.

| Tier | Carrier | Request-carried? | Modern era |
|---|---|---|---|
| 1 | agent-token pin | yes (Authorization header) | works |
| 2 | `/mcp/p/<slug>` URL path | yes (request line) | works |
| 3 | `set_profile` session state | **no** | **refused** |

**New derived view**: `resolveActiveProfileForList` — identical to `resolveActiveProfile` but skipping tier 3, used by the list filters so `*/list` never varies by connection state in either era.

## 4. Request Metadata (`_meta`) — new inbound surface

Per-request envelope replacing the connection-level handshake. Read-only for mcpproxy in this feature.

| Field | Use here |
|---|---|
| `protocolVersion` | era detection |
| `clientInfo` | **replacement source for client name/version attribution** |
| `clientCapabilities` | must be mirrored onto the upstream hop |
| trace context | FR-022, into activity records |
| `requestState` | MRTR only; deferred |

## 5. Activity Record (existing, two affected fields)

No schema change. Two fields change *distribution* for modern clients:

- `session_id` becomes `""` for every modern request.
- `work_session_id` likewise.

**Consequence**: activity rows from modern clients cannot be grouped by session in the Web UI. A request-carried correlation key is needed before the client-facing pin lifts. This is a display concern, not a storage migration.

## 6. Tool Schema Hash (Spec 032) — the one persistence risk

| Attribute | Value |
|---|---|
| Stored as | `ParamsJSON` on the tool record, plus a SHA-256 in the quarantine baseline |
| Computed from | the **library-decoded** `mcp.ToolInputSchema`, re-marshalled |
| Risk | v1.0.0 rewrites draft-07 `#/definitions/…` → `#/$defs/…` on decode |

If the decoded form changes, the hash changes, and Spec-032 reports every affected tool as "changed" (rug-pull signal) after a version bump that changed nothing upstream. Because the hash input is the decoded struct rather than the raw upstream bytes, **this is a real coupling between library version and security state**, and the plan treats it as such. Blast-radius measurement is in flight; mitigation is one of: normalize before hashing, hash raw upstream JSON, or a one-time baseline migration.

## 7. In-flight Cancellation Key (library-internal, security-relevant)

| Attribute | Value |
|---|---|
| Key | `"<sessionID>:<requestID>"` |
| Modern value | `":<requestID>"` — **identical across all clients** |
| Registered | every request carrying a JSON-RPC id |
| Consumed by | `notifications/cancelled` |

Not mcpproxy's data, but it becomes mcpproxy's exposure the moment the client-facing modern era is enabled: two clients that pick the same JSON-RPC id can cancel each other's calls. Tracked as R1.
