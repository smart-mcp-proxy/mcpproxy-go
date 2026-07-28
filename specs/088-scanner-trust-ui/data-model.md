# Data Model: Security Scanner Web UI + Trust-Mode Controls (088)

Frontend view-model entities only — no storage or backend schema changes. Field names mirror the shipped spec-086 payloads (generated `frontend/src/types/contracts.ts`).

## TrustModeState

| Field | Type | Source | Notes |
|-------|------|--------|-------|
| `raw` | `string \| undefined` | `Server.trust_mode` (GET /api/v1/servers) | Omitted when unset; may hold migrated legacy value or an invalid string |
| `effective` | `'auto' \| 'scan' \| 'manual'` | derived | `raw` if recognized, else `'manual'` (mirror of backend `EffectiveTrustMode()`) |
| `isDefault` | `boolean` | derived | `raw` empty/undefined |
| `isInvalid` | `boolean` | derived | `raw` non-empty and unrecognized → UI shows raw + "effective: manual" |

**Write path**: `PATCH /api/v1/servers/{id}` body `{"trust_mode": "<mode>"}`; response envelope may carry `restart_required: true` → surface notice. Never write `auto_approve_tool_changes` / `skip_quarantine`.

**Validation**: selector offers exactly `auto | scan | manual`; `auto` requires confirmed warning (tool changes trusted unscanned + unscanned admission at add time).

## HoldEvidence

| Field | Type | Source | Notes |
|-------|------|--------|-------|
| `reason` | `'scan_findings' \| 'scan_coverage' \| ''` | `Tool.held_reason` (GET /servers/{id}/tools, diff endpoint) | Empty → no evidence chrome at all (pre-086 records) |
| `verdict` | `'dangerous' \| 'warnings' \| 'clean' \| ''` | `Tool.held_verdict` | `clean` + `scan_coverage` = precautionary hold, never styled as threat |
| `signals` | `string[]` | `Tool.held_signals` | Delivered list only (≤16, producer order, no overflow count) |
| `tpaIds` | `string[]` | derived | Signals matching `/^tpa\.(TPA-\d{4}-\d{4})\./` → extracted id, deduped, ordered first |
| `heuristics` | `string[]` | derived | Remaining signals, after TPA ids in display order |

**Display rule**: cap collapses heuristics only; TPA ids always visible; "+N more" counts received-but-collapsed items only.

**Lifecycle**: fields cleared server-side on any transition out of held state — UI must re-render from payload, never cache evidence past a refresh.

## QuarantineBannerState (derivation table)

Inputs: `trust_mode` (effective), `quarantined`, `security_scan.status`, `security_scan.finding_counts`, `security_scan.risk_score`.

| Priority | State | Condition (first match wins) | Copy intent | Actions |
|----------|-------|------------------------------|-------------|---------|
| 1 | `scan-running` | `quarantined && effective==scan && status=='scanning'` | Scan in progress; result determines next step (no auto-approval promise) | none required |
| 2 | `scan-failed` | `quarantined && status=='failed'` | Scan could not complete — held as a precaution, NOT a threat verdict | Retry scan · manual review |
| 3 | `scan-blocked` | `quarantined && effective==scan && (status=='dangerous' \|\| status=='warnings')` | Latest scan verdict blocked automatic approval | View report · review/approve |
| 4 | `manual-review` | `quarantined` (all remaining) | Awaiting manual review (+ latest summary if `status` not `not_scanned`) | Approve · (no summary → Run scan CTA) |

Copy constraints: never claim admission-window provenance; never promise automatic approval (both facts are server-internal).

## ScanOutcomeSummary

Direct mapping of `Server.security_scan` (`contracts.SecurityScanSummary`): `status` (`clean|warnings|dangerous|failed|not_scanned|scanning`), `risk_score`, `finding_counts {dangerous,warning,info,total}`, `last_scan_at`, `deep_scan` descriptor (`skipped_scanners[]` renders "skipped", not error). Read-only; refreshed on `mcpproxy:scan-settled` / `mcpproxy:servers-changed`.

## Consumed events

| Window event (from stores/system.ts) | SSE origin | Trigger for |
|--------------------------------------|------------|-------------|
| `mcpproxy:scan-settled` | `security.scan_settled` (payload: server_name, status, findings_summary — no verdict/risk → refetch) | scan summary, banner, approvals refresh (matching server only) |
| `mcpproxy:servers-changed` | `servers.changed` | approvals + server projection refresh (covers CLI/MCP approvals) |
