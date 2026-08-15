# Phase 0 Research: Required-Tools Preflight

Full external research (19 gateways, 16 papers, 11 infra patterns, client-side survey) and the decision log live in the research artifact "Required-Tools Preflight — Research & Alternatives (mcpproxy #969)", verified 2026-08-15. This file records the codebase-seam decisions that drive the plan. No NEEDS CLARIFICATION items remain — all product decisions were locked 2026-08-15 and reporter-confirmed 2026-08-13 (issue #969).

## D1. Where does the evaluator live?

- **Decision**: New leaf package `internal/preflight` (taxonomy + pure evaluator), glue in `internal/server/preflight_glue.go`, REST in `internal/httpapi/preflight.go`.
- **Rationale**: `internal/server` owns index/stateview/storage wiring; `internal/httpapi` talks to it via the `ServerController` interface (precedent: `GetToolApprovalStatus`). A leaf package avoids an httpapi→server import cycle and keeps the evaluator free of transports (structural zero-I/O).
- **Alternatives**: methods on `MCPProxyServer` only (rejected: untestable without full server; enum would have no single home); `internal/contracts` (rejected: contracts is DTO-only by convention).

## D2. How are the three disagreeing gates consolidated?

Existing gates and their known divergences (verified against source, 2026-08):

| Gate | Location | Divergence |
|---|---|---|
| Dispatch inline checks | `handleCallToolVariant` (internal/server/mcp.go ~2032–2074) | Ground truth; honors quarantine-enabled/skip flags; emits `emitActivityPolicyDecision` |
| `describeGateReason` | internal/server/mcp_visibility.go ~112 | Applies pending/changed gate only when quarantine enabled && not skipped — matches dispatch |
| `classifyServerToolStatus` | internal/server/mcp.go ~5673 | Checks approval **unconditionally** (FP source for `auto_approve_tool_changes` servers) and collapses `changed` → `pending_approval` |

- **Decision**: Introduce `preflight.ClassifyTool(inputs)` as the single classification; refactor `classifyServerToolStatus` and `describeGateReason` to delegate; dispatch keeps its own code path but a contract test asserts dispatch refusal ⇔ non-ready classification for every evaluator-represented gate (FR-002). Dispatch behavior wins on every divergence: quarantine flags honored, `changed` distinct from `pending`, auto-approved changes = ready.
- **Alternatives**: literal single call site for dispatch too (rejected for v1: dispatch path is latency-sensitive and interleaved with arg validation; semantic equivalence + contract test achieves no-skew with less blast radius).

## D3. State sources per reason code

| Reason | Source |
|---|---|
| `server_not_configured` | config: server name absent |
| `server_not_in_scope` | profile scope (`serverInScope`/profile.Allows) — operator tier only |
| `server_quarantined` | ServerConfig.Quarantined |
| `server_disabled` | ServerConfig.Enabled == false |
| `not_found` | Bleve `GetToolsByServer`/lookup miss (server known, tool absent) — **no** live ListTools fallback |
| `tool_denied_by_config` | `isToolConfigDenied` (enabled_tools/disabled_tools) |
| `tool_blocked_by_user` | ToolApprovalRecord.Disabled |
| `tool_changed` | ToolApprovalRecord.Status == changed (when quarantine gates apply) |
| `tool_pending_approval` | ToolApprovalRecord.Status == pending (incl. scan-hold) |
| `hash_mismatch` | pin vs ToolApprovalRecord.CurrentHash (+ HashSchemaVersion match) |
| `oauth_required` | connection state PendingAuth (explicit map, before health fallthrough) |
| `server_unhealthy` | connection state Error/Disconnected via stateview snapshot; detail/diagnostic from Spec 044 classifier when present |
| `server_initializing` | connection state Connecting/Discovering/Authenticating — server-level only |
| `missing_annotation` / `policy_filtered` | spec 094 `excludeReason` per named ID under caller filters |

## D4. Pin format

- **Decision**: `sha256/v{HashSchemaVersion}:{hex}` (e.g. `sha256/v2:9f2c41ab…`). A pin whose schema version differs from the record's `HashSchemaVersion` reports `hash_mismatch` with detail "hash schema changed (proxy upgrade); relock", distinguishable from genuine drift in `detail` while remaining one reason code.
- **Rationale**: proxy upgrades can re-version the hash algorithm (backfill exists in tool_quarantine.go); pins must not silently rot into false drift alarms.

## D5. did_you_mean

- **Decision**: nearest-name via prefix + Levenshtein ≤2 over the **caller-visible** indexed tool names (scope- and tier-filtered before matching; quarantined-server names excluded). Max 3 suggestions. New small helper in `internal/preflight`; `lookupIndexedTool` is exact-match only and not reused for this.

## D6. Activity record

- **Decision**: reuse the existing activity-log storage with a new record kind `preflight`; payload `{ids_count, verdict, reasons{code:count}, per_tool[{id,status,reason?}]}`, request-ID correlated. CLI `activity list` and the Web UI render unknown kinds generically today (verified in list rendering paths) — plus a small type-label addition for polish.
- **Alternatives**: separate preflight log bucket (rejected: breaks the one-place transparency story).

## D7. wait_ms & spec 093 shed budget

- **Decision**: handler-level poll loop (floor 250 ms) on the request context, deadline = min(wait_ms, 10 000 ms); the request is registered with the existing concurrency-shed accounting exactly like other long requests; early-terminate on first non-retryable failure.

## D8. tools/list byte-identity guard

- **Decision**: snapshot test that serializes registered tool schemas for all three routing modes and compares against committed goldens (also closes the pre-existing drift-test gap flagged in the research: routing modes already silently lack some default-surface params — goldens capture current state, not fix it, to keep this feature zero-MCP-change).

## Resolved: all Technical Context unknowns

None outstanding — stack, storage, testing, platforms fixed by repo conventions; product contract fixed by the decision log.
