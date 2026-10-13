# Data Model: Required-Tools Preflight

## Enums

### PreflightStatus
`ready` | `unavailable`

### PreflightReason (closed, 15 codes, additive-only; unknown ⇒ treat as non-retryable)

| Code | Class | retryable | action | Set verdict | Exit |
|---|---|---|---|---|---|
| server_initializing | retryable | true | none | degraded_retryable | 10 |
| server_unhealthy | retryable | true | view_logs (best-effort restart/login) | degraded_retryable | 10 |
| server_disabled | fix_state | false | enable | blocked | 11 |
| server_quarantined | fix_state | false | approve | blocked | 11 |
| tool_pending_approval | fix_state | false | approve | blocked | 11 |
| tool_changed | fix_state | false | approve | blocked | 11 |
| tool_blocked_by_user | fix_state | false | enable | blocked | 11 |
| oauth_required | fix_state | false | login | blocked | 11 |
| hash_mismatch | fix_state | false | configure | blocked | 11 |
| server_not_in_scope † | permanent | false | configure | blocked | 11 |
| tool_denied_by_config | permanent | false | configure | blocked | 11 |
| missing_annotation | permanent | false | configure | blocked | 11 |
| policy_filtered | permanent | false | none | blocked | 11 |
| not_found | permanent | false | configure | unknown_ids | 12 |
| server_not_configured | permanent | false | configure | unknown_ids | 12 |

† operator tier only; agent-token tier reports `not_found` (byte-indistinguishable from ordinary not_found).

Reserved (documented, not implemented): `server_saturated`.

### SetVerdict
`ready` (exit 0) < `degraded_retryable` (10) < `blocked` (11) < `unknown_ids` (12). Worst present wins.

### Precedence (first match per ID)
server_not_configured → server_not_in_scope† → server_quarantined → server_disabled → not_found → tool_denied_by_config → tool_blocked_by_user → tool_changed → tool_pending_approval → hash_mismatch → oauth_required → server_unhealthy → server_initializing → annotation filters (read_only_only → exclude_destructive → exclude_open_world; per owning filter: nil hint ⇒ missing_annotation, explicit unsafe ⇒ policy_filtered) → ready.

## Request / Response DTOs (contracts/types.go → generated contracts.ts)

### PreflightRequest
| Field | Type | Rules |
|---|---|---|
| tools | []PreflightToolRef | 1–100 entries after dedup; empty ⇒ 400 |
| profile | string? | must exist ⇒ else 400 |
| policy | PreflightPolicy? | three optional bools (read_only_only, exclude_destructive, exclude_open_world) |
| wait_ms | int? | 0–10000; >10000 ⇒ 400 |

### PreflightToolRef
| Field | Type | Rules |
|---|---|---|
| id | string | `server:tool`; malformed ⇒ per-ID not_found with format hint (not request error) |
| pin_hash | string? | `sha256/v{N}:{hex}`; duplicates of same id with different pin ⇒ 400 |

### PreflightResponse
| Field | Type | Notes |
|---|---|---|
| verdict | SetVerdict | |
| checked_at | RFC3339 | |
| waited_ms | int? | present when wait_ms used |
| tools | []PreflightToolResult | ordered by first occurrence of unique id in request |

### PreflightToolResult
| Field | Type | Notes |
|---|---|---|
| id | string | echoed requested id |
| status | PreflightStatus | |
| reason | PreflightReason? | unavailable only |
| retryable | bool? | unavailable only |
| action | string? | health-action vocabulary |
| detail | string? | occurrence-specific |
| remediation | string? | one actionable instruction |
| hash | string? | operator tier + ready only |
| did_you_mean | []string? | not_found only; ≤3; caller-visible scope |

## Evaluator types (internal/preflight)

- **EvalContext**: `{Index IndexReader, Approvals ApprovalReader, State StateReader, Policy ConfigPolicy, Tier (operator|agent_token), ProfileScope, Filters, Pins map[string]Pin}` — all narrow read interfaces; no transport reachable.
- **Result**: mirrors PreflightToolResult minus serialization concerns.
- **ClassifyTool**: shared classification consumed by evaluator, `classifyServerToolStatus`, `describeGateReason` (D2).

## Activity record (kind `preflight`)

`{request_id, ts, ids_count, verdict, reasons: map[code]count, per_tool: [{id, status, reason?}]}` — local activity log only; never exported to telemetry (counts/enum codes would be the only telemetry-safe fields, out of scope v1).

## State transitions

None persisted — the evaluator is a pure point-in-time read; wait_ms re-evaluates the same pure function until deadline/ready/non-retryable.
