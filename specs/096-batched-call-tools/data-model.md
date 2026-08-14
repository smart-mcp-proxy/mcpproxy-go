# Data Model: Batched call_tools() (Spec 096)

## Entities

### BatchRequestElement (sandbox value, not persisted)
| Field | Type | Rules |
|-------|------|-------|
| `server` | string | required, non-empty |
| `tool` | string | required, non-empty |
| `args` | object | optional; defaults to `{}`; non-object → whole call malformed |

Validation: element must be a plain object; sparse array holes are malformed. First offending element index is named in the error message.

### BatchResultSlot (sandbox value, not persisted)
Exactly the `call_tool()` envelope:
- success: `{ok: true, result: <wire-shaped JSON value>}`
- failure: `{ok: false, error: {code: string, message: string}}`

Ordering invariant: `slots[i]` corresponds to `requests[i]` for all i; `len(slots) == len(requests)` always (when the call itself is well-formed).

Error codes reused verbatim from the lone-call path (promoted to constants during extraction): `INVALID_ARGS`, `MAX_TOOL_CALLS_EXCEEDED`, `SERVER_NOT_ALLOWED`, `ACCESS_DENIED`, `PERMISSION_DENIED`, `UPSTREAM_ERROR`, `SERIALIZATION_ERROR`.

### BatchOptions (sandbox value)
| Field | Type | Rules |
|-------|------|-------|
| `max_parallel` | integer | optional; 1–32; fractional/non-numeric/out-of-range → whole call malformed; unknown option keys ignored |

### Config: `code_execution_max_parallel`
| Property | Value |
|----------|-------|
| JSON key | `code_execution_max_parallel` |
| Type | integer |
| Default | 8 (absent or 0 → 8 at post-load defaulting) |
| Valid range | 1–32 (validation error outside) |
| Hot-reload | applies to executions starting after the change (read via `currentConfig()`) |
| Editions | identical in both |

### ToolCallRecord (existing, extended usage only)
Batch-dispatched elements append the same `jsruntime.ToolCallRecord` a lone call appends, in input order, on the script goroutine after the join. Pre-dispatch failures append exactly what a lone call failing the same gate appends today (i.e., nothing — parity per FR-010).

## Fixed product constants
| Constant | Value | Where |
|----------|-------|-------|
| Batch length cap | 100 | `internal/jsruntime` (named const) |
| max_parallel ceiling | 32 | shared by config validation + options validation |

## State transitions (per element)

```
validated ──fail──▶ slot=error(INVALID_ARGS)            [no budget, no dispatch]
validated ──pass──▶ gate-checked ──fail──▶ slot=error(gate code)   [no budget]
gate-checked ─pass─▶ budget-checked ─fail─▶ slot=error(MAX_TOOL_CALLS_EXCEEDED)
budget-checked ─pass─▶ dispatched ──▶ normalized ──▶ slot=ok/error  [budget consumed, record appended]
dispatched ──ctx cancelled──▶ (execution already failed; no observable value)
```
