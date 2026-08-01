# Data Model — Tray Glance v2 (Spec 090)

## Swift (tray) — pure model layer

### ActivityEntry (existing, extended access)

Existing `API/Models.swift` struct; decodes `metadata: [String: JSONValue]?`.
New derived accessors (extension, no stored fields):

| Accessor | Source | Notes |
|----------|--------|-------|
| `intentReason: String?` | `metadata.intent.reason` | call rows' subtitle |
| `blockReason: String?` | `metadata.reason` | policy rows' subtitle |
| `reason: String?` | blockReason for `policy_decision`, else intentReason | pipeline-facing |
| `outcomeClass: OutcomeClass` | `.blocked` for type `policy_decision` (decision blocked/block), else `.call` | grouping key part |

`GlanceEvent` (SSE adapter) now populates `metadata` with the whitelisted
context (intent map / decision+reason) so live and polled entries expose the
same accessors, and adapts `activity.policy_decision` (status `"blocked"`,
provisional id `"<request_id>:policy_decision"`).

### GlanceRun (new, pure)

A maximal run of consecutive qualifying records sharing a group key.

| Field | Type | Rule |
|-------|------|------|
| `records` | `[ActivityEntry]` | newest-first, ≥1 |
| `key` | `(server: String, tool: String, outcomeClass: OutcomeClass)` | grouping key (FR-002) |
| `count` | `Int` | `records.count`; suffix `×N` rendered when > 1 (FR-003) |
| `newest` | `ActivityEntry` | supplies age; error clause comes from newest *erroring* record (FR-004) |
| `identity` | `String` | `recordKey(oldest)` — stable across head-extension (FR-024) |
| `worstStatus` | `String` | error > success within `.call` runs; `.blocked` runs are uniformly blocked (FR-002/004) |
| `displayReason` | `String?` | newest record in run having a reason (FR-004) |

Pipeline (FR-001, all in `GlanceSelection`):
`qualifies` (now admits `policy_decision` blocked records; management built-ins
still excluded) → `collapseByRequestID` → `groupConsecutive` → `prefix(5)`.
Dropped records never split runs (they are removed before grouping).

### GlancePresence (new, pure)

| Value | Boundary (vs `last_activity`, fallback start time) |
|-------|-----------------------------------------------------|
| `.active` | age < 5 min |
| `.idle` | 5 min ≤ age ≤ 30 min (boundaries inclusive-idle) |
| `.seen` | 30 min < age ≤ 24 h |
| excluded | age > 24 h, or no parseable timestamp |

`ClientPresenceRow`: `name` ("Unknown client" fallback), `version`,
`lastActivity`, `state`. Dedup key: `name + version`, keep most recent.
Rows: top 5 by recency. Summary: counts over the FULL deduped set,
`"N active · M idle"`, seen excluded, empty states omitted (FR-017–FR-020).

### Rendering deltas (GlanceSection, non-pure)

- Status icon: success → `nil` image; error → red `xmark.circle`; blocked →
  orange `exclamationmark.triangle` (shape-distinct from error) (FR-010/011).
- Subtitle: `if #available(macOS 14.4, *) { item.subtitle = reason.truncated(60) }`;
  a subtitle appearing/disappearing on an existing row while the menu is open
  is a structural change → deferred rebuild (FR-023). **Atomic preflight**:
  `updateInPlace` precomputes every row's old/new line count BEFORE mutating
  anything; if any row's line count would change, it returns `false` having
  made zero mutations (no half-updated menu). Tested with a case where a LATER
  row changes line count, proving earlier rows were not already rewritten.
- Presence indicator images: filled circle (active) / filled gray (idle) /
  hollow circle (seen) — fill+shape distinct, constant per state (FR-018).
- Block order: summary → Activity (24h) → Recent → Clients (FR-021).

## Go (core) — additive deltas

### Activity list projection (`internal/httpapi/activity.go`)

`exclude_payloads=true` keeps a metadata whitelist instead of dropping all
metadata:

```text
metadata.intent.reason, metadata.intent.operation_type,
metadata.decision, metadata.reason,
metadata.client_name, metadata.client_version
```

Everything else in metadata plus `arguments`/`response` remains excluded.

### Policy decision record (`event_bus.go`, `activity_service.go`, `server/mcp.go`)

`EmitActivityPolicyDecision(serverName, toolName, sessionID, requestID, decision, reason)`
— new `requestID` param; payload gains `request_id`; the persistence subscriber
copies it to `ActivityRecord.RequestID`. All 16 emit sites across
`internal/server` (13 in `mcp.go`, plus `mcp_routing.go` and
`output_sanitisation.go`, whose helper signatures gain the parameter) pass the
dispatch request id, minting it above the earliest policy gate when needed.

### Sessions listing (`internal/storage/manager.go`)

`GetRecentSessions(limit, status)`: collect all retained sessions (≤100),
sort by `LastActivity` desc, then apply `status` filter and `limit`.
API shape unchanged.
