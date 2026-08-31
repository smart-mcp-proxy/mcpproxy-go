# Contract: the mode matrix

**5 valid cells + 1 baseline. 7 structurally impossible combinations, each a skip-with-reason
row.** This file is the source of truth for FR-015, FR-016 and FR-017.

## Valid cells

| id | Endpoint | `tool_response_mode` | `direct_tool_response_mode` | Requires |
|----|----------|----------------------|------------------------------|----------|
| `retrieve_full` | retrieve_tools surface | `full` | n/a | — (default) |
| `retrieve_compact` | retrieve_tools surface | `compact` | n/a | spec 085 |
| `direct_full` | direct surface | n/a | `full` | — |
| `direct_deferred` | direct surface | n/a | `deferred` | spec 102 |
| `code_exec` | code-execution surface | forced `full` | n/a | `enable_code_execution: true` |
| `baseline` | none — all upstream tools loaded directly | n/a | n/a | FR-020 denominator |

`baseline` is not an mcpproxy mode. It is the same agent doing the same tasks with every
upstream tool inline, and it is what every percentage is measured against.

## Skipped combinations (FR-017)

| Combination | Reason code | Why |
|---|---|---|
| `code_execution` × `compact` | `forced` | The code-execution surface overwrites the response mode with `full` and blanks the detail parameter; `detail` is not in that surface's schema. |
| `code_execution` × `compact` × direct axis | `forced` | Same cause, second axis. |
| `direct` × `full` (discovery axis) | `inapplicable` | `tool_response_mode` has exactly one consumer, inside the retrieve_tools handler. The direct surface has no `retrieve_tools` tool. |
| `direct` × `compact` (discovery axis) | `inapplicable` | Same cause. |
| `retrieve_tools` × `deferred` | `inapplicable` | `direct_tool_response_mode` is read only when building the direct listing. |
| `code_execution` × `deferred` | `inapplicable` | Same cause. |
| `code_execution` with `enable_code_execution: false` | `degenerate` | The surface can discover tools and call none of them. |

## Rules

1. **A cell is selected by endpoint URL, not by a config change.** All three routing-mode
   servers are built at startup and all three endpoints stay mounted regardless of config.
   The whole matrix therefore crosses on ONE long-lived proxy instance.
2. **Only the two serialization axes need config**, and each affects only its own surface.
3. **An inapplicable axis is recorded as not-applicable**, never as a default value — a
   default would imply a measurement that was never taken.
4. **A skipped cell carries a reason code and never renders as a zero.** Reuse the existing
   skipped-row shape (`Skipped` / `SkipReason` plus the skipped-result constructor) rather
   than inventing a second one.
5. **Capability toggles** (batching, stored scripts, validate-before-dispatch) are binary
   conditions applied to the cells where each is available, and the report enumerates which
   cells each applies to. They are not additional axes of a product.
6. **Cell ids are stable** across runs and releases, so results remain comparable (FR-028).

## Fleet shaping

Every cell must be measured against the SAME fleet in a given comparison, and the fleet must
be large enough for proxy modes to differ from baseline. A single upstream server's toolset is
too small to show the asymptote; load the full fleet even when exercising one service's tasks.
