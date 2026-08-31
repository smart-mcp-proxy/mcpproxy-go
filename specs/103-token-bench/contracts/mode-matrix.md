# Contract: the mode matrix

**5 distinct behaviours + 1 baseline. The 3-axis product's other 7 combinations are
configurable but redundant, each a skip-with-reason
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

## The other 7 combinations of the 3-axis product (FR-017)

The naive product is 3 routing modes x 2 discovery serializations x 2 direct serializations =
**12 combinations, which collapse onto 5 distinct behaviours.**

They are **not impossible to configure** — they are *configurable and behaviourally redundant*,
because on each surface one or both serialization axes have no consumer. Calling them
"impossible" would be wrong; rendering them as zeros would be worse. Each is a
**skip-with-reason row naming the cell it collapses onto.**

| Configured combination | Collapses onto | Reason code |
|---|---|---|
| `retrieve_tools` x full x **deferred** | `retrieve_full` | `axis-ignored` |
| `retrieve_tools` x compact x **deferred** | `retrieve_compact` | `axis-ignored` |
| `direct` x **compact** x full | `direct_full` | `axis-ignored` |
| `direct` x **compact** x deferred | `direct_deferred` | `axis-ignored` |
| `code_execution` x **compact** x full | `code_exec` | `forced-full` |
| `code_execution` x full x **deferred** | `code_exec` | `axis-ignored` |
| `code_execution` x **compact** x **deferred** | `code_exec` | `forced-full` + `axis-ignored` |

### Why each axis is ignored where it is

- **`tool_response_mode` governs exactly one surface**: resolved by
  `effectiveToolResponseMode`, whose single production call is in the `retrieve_tools` handler
  (`internal/server/mcp.go:1584-1585`). Neither the direct nor the code-execution surface reads
  it — the direct surface has no `retrieve_tools` tool at all.
- **`direct_tool_response_mode` governs exactly one surface**: resolved by
  `effectiveDirectToolResponseMode`. Note it has THREE production call sites, not one —
  building the listing (`mcp_routing.go:100`), detecting serialization drift (`:1040-1045`)
  and logging a reload (`:1071-1076`) — but all three concern the direct surface only, so the
  collapse holds. The claim is one *surface*, not one *call site*.
- **`code_execution` forces full**: that surface overwrites the response mode with `full` and
  blanks the detail parameter; `detail` is not in its schema. That is a genuine *override*,
  distinct from an ignored axis, hence its own reason code.

### Separately: a degenerate configuration

`code_execution` with `enable_code_execution: false` is not part of the product above. It is a
configuration in which the surface can discover tools and call none of them. Skip it with
reason `degenerate`.

## Rules

1. **The routing-mode axis is selected by endpoint URL — no config change, no restart.** All
   three routing-mode servers are built at startup and all three endpoints stay mounted
   regardless of config (`internal/server/server.go:2514-2536`).
2. **The two serialization axes DO require config**, and each affects only its own surface.
   A cell is therefore (URL, serialization-config) — **not URL alone**.
   **Both hot-reload, so the matrix still crosses on ONE long-lived instance**, with a config
   apply between serialization cells: `tool_response_mode` is read from the live snapshot on
   every call (`internal/server/profile_resolver.go:49-64`, documented as taking effect "on
   the very next call, without reconstructing the server"), and `direct_tool_response_mode` is
   rebuilt on the `config.reloaded` event (`internal/server/server.go:569-597`).
3. **An ignored axis is recorded as not-applicable**, never as a default value — a
   default would imply a measurement that was never taken.
4. **A skipped row carries a reason code AND the cell it collapses onto, and never renders
   as a zero.** Reuse the existing
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
