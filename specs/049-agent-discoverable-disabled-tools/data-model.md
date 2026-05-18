# Phase 1 Data Model: Agent-Discoverable Disabled Tools

No persistent storage. All entities are request-time, in-memory response shapes
(extensions to existing `internal/contracts` types) plus one pure enum.

## Entity: DisabledToolStatus (enum)

| Value | When (first match wins) | Remediation class |
|-------|-------------------------|-------------------|
| `server_disabled` | owning server `Enabled == false` | "enable the server first" |
| `disabled_by_config` | `Runtime.IsToolConfigDenied(server,tool)` true | "operator policy; edit mcp_config.json; NOT user-overridable" |
| `disabled_by_user` | approval record exists and `Disabled == true` | "ask the user to re-enable in the UI" |
| `pending_approval` | approval status pending/changed (Spec 032) | "ask the user to approve in the UI" |
| `disabled_unknown` | storage/lookup error or indeterminate | "reason undetermined; check server logs" |

- Validation: exactly one value per locked tool. Precedence is total and
  deterministic. Unknown is the catch-all so the function is total.
- Derived purely from: `ServerConfig.Enabled`, `IsToolConfigDenied`, the
  `ToolApprovalRecord` (read-only), never written.

## Entity: LockedToolEntry (response item, additive)

Lean discovery entry for a non-callable tool.

| Field | Type | Notes |
|-------|------|-------|
| `name` | string | tool name (existing) |
| `server` | string | owning server (existing) |
| `description` | string | existing one-line description, untruncated |
| `status` | DisabledToolStatus | the single reason |

Emitted only inside the `disabled` section of a `retrieve_tools` response when
`include_disabled=true`. Absent otherwise.

## Entity: RemediationMap (response-level, additive)

`map[DisabledToolStatus]string`, emitted once per `retrieve_tools` response,
containing only keys for statuses actually present in that response's locked
entries. Absent when no locked entries returned.

## Entity: ServerToolCounts (server list/get item, additive)

Per-server rollup, attached to an `upstream_servers` server entry **only** when
at least one non-callable count > 0.

| Field | Type | Emitted when |
|-------|------|--------------|
| `callable` | int | block present |
| `disabled_by_config` | int | value > 0 |
| `disabled_by_user` | int | value > 0 |
| `pending_approval` | int | value > 0 |
| `server_disabled` | int | value > 0 |
| `disabled_unknown` | int | value > 0 |

Whole block omitted when every tool on the server is callable (SC-005). Zero
sub-keys omitted (`omitempty`).

## Contract type changes (`internal/contracts/types.go`)

All additive, `json:",omitempty"`, so default responses are byte-identical
(FR-002 / SC-001):

- Extend the `retrieve_tools` response wrapper with optional `disabled []LockedToolEntry`
  and `remediation map[string]string`.
- Extend the `upstream_servers` server entry with optional `tools *ServerToolCounts`.
- Add `ConfigDenied` is already present from #468; no change needed there.

## State transitions

None. The classifier is a pure function of current state; nothing transitions.
