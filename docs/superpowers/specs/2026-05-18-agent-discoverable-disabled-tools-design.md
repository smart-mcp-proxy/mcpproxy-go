# Agent-Discoverable Disabled Tools — Design

- **Date:** 2026-05-18
- **Status:** Approved (brainstorming), pending implementation plan
- **Author:** Claude (brainstormed with Algis)
- **Builds on:** PR #468 (`feat/config-tool-allowlist` — layered config tool filter, introduces `config_denied` and `Runtime.IsToolConfigDenied`)
- **Delivery:** Standalone follow-up PR. The four UX fixes (#1–#4) from the #468 review land in #468 itself; this design is everything else.

## 1. Problem

`mcpproxy` exposes a curated, filtered tool surface to agents. Today `retrieve_tools`
runs `isToolCallable()` as a hard post-search filter: any disabled or config-denied
tool is dropped from results and treated as non-existent. `upstream_servers` only
counts callable tools.

Consequence: when a capability an agent needs to complete a task exists on an
upstream but is locked (by operator config, by the user, by quarantine, or because
the server is off), the agent gets **zero signal**. It cannot tell the user
"the `delete_repo` tool exists but is disabled — enable it to proceed", and it
cannot distinguish a lock the user can lift (UI toggle) from operator policy the
user *cannot* lift (mcp_config.json), so any suggestion it makes is a guess.

## 2. Goal & non-goals

**Goal:** Let an agent, on demand, discover that a relevant capability exists but
is locked, learn *why* in a machine-branchable way, and relay the *correct*
remediation to the user/operator — at near-zero token cost when the feature is
not exercised.

**Non-goals:**

- No change to enforcement. A discovered locked tool remains non-callable.
  `isToolCallable()` is untouched; this is a discovery/observability layer only.
- No change to default `retrieve_tools` / `upstream_servers` output when the
  feature is not triggered (byte-for-byte backward compatible).
- No new persistent storage. Classification is computed at query time from data
  already loaded (config + approval records + StateView snapshot).

## 3. Key facts established during brainstorming

- **Disabled tools are already in the Bleve index.** `DiscoverAndIndexToolsForServer`
  indexes everything `client.ListTools()` returns with no disabled/config filter;
  filtering is purely query-time in the `callableResults` loop of
  `handleRetrieveToolsWithMode` (`internal/server/mcp.go`). Surfacing locked tools
  is therefore a change to *that loop*, not a new data path.
- **`isToolCallable` collapses several distinct reasons** (server off, config-denied,
  user-disabled, quarantine pending/changed, storage error). The discovery layer
  re-derives a precise reason; enforcement stays collapsed.
- **`Runtime.IsToolConfigDenied(server, tool)`** (added in #468) is the
  config-vs-user discriminator and is reused as-is.

## 4. Design

### 4.1 `retrieve_tools` — opt-in `include_disabled`

- New optional boolean parameter `include_disabled` (default `false`). Tool schema
  description gains exactly one sentence (the "static hint", §4.4).
- In `handleRetrieveToolsWithMode`, the existing loop that builds `callableResults`
  changes: when a result is dropped by `isToolCallable`, **count it always**; and
  **if `include_disabled` is true**, classify it (§4.3) and append to a separate
  `disabledResults` slice.
- Agent-scope filtering (`authCtx.CanAccessServer`) is applied **before**
  classification — an agent never sees locked tools on servers it cannot access.
- Response ordering: callable results first with their **existing ranking
  unchanged**, then `disabledResults`, capped at `min(limit, 10)` entries to bound
  tokens against a pathologically restrictive config.
- Per disabled entry (lean shape):
  - `name`
  - `server`
  - `description` (the existing one-line description; already short — not truncated)
  - `status` (enum, §4.3)
- A single `remediation` map is emitted **once** at the top level of the response,
  containing only the keys for statuses actually present in `disabledResults`
  (§4.3). Per-tool remediation prose is **not** emitted.
- Telemetry: increment an in-memory `include_disabled` usage counter (consistent
  with spec 042 — in-memory only, never persisted) so adoption is observable.

### 4.2 `upstream_servers` — conditional counts

- `operation="list"` and `operation="get"`: each server entry gains a `tools`
  block **only when at least one non-callable count is > 0**:

  ```json
  "tools": { "callable": N, "disabled_by_config": N, "disabled_by_user": N, "pending_approval": N }
  ```

  Zero-valued sub-keys are omitted; the whole `tools` block is omitted when every
  tool is callable. Computed from the StateView snapshot the existing
  `getVisibleToolCount` path already walks — one extra classification pass, no new
  storage reads.

### 4.3 Status taxonomy & classification

`status` is one of five values. Classification order is **first match wins**:

| Order | Condition | `status` | `remediation` text (emitted once if present) |
|------|-----------|----------|----------------------------------------------|
| 1 | Server not enabled | `server_disabled` | "Its server is disabled. Ask the user to enable the server first." |
| 2 | `IsToolConfigDenied` true | `disabled_by_config` | "Locked by operator policy in mcp_config.json (enabled_tools/disabled_tools). The user cannot enable this from the UI; ask the operator to change the server config." |
| 3 | Approval record `Disabled` | `disabled_by_user` | "Disabled by the user. Ask the user to re-enable it in the mcpproxy UI (Server detail → Tools) or via the API." |
| 4 | Approval status pending/changed | `pending_approval` | "Awaiting security approval. Ask the user to review and approve it in the mcpproxy UI." |
| 5 | Storage error / indeterminate | `disabled_unknown` | "Reason undetermined; check server logs." |

`disabled_unknown` (5th bucket) exists so a transient storage error never causes a
*wrong* remediation (e.g. telling the user to toggle a UI switch for a
config-locked tool). The four happy-path states stay clean.

### 4.4 Reactive triggers (discoverability)

Option C's only weakness is the agent not knowing the flag exists. Closed two ways:

1. **Static hint** (one-time, cheap): one sentence appended to the `retrieve_tools`
   parameter/tool description — *"Set `include_disabled:true` to also surface
   tools that exist but are currently locked by config, user, or quarantine,
   with remediation guidance."*
2. **Reactive nudges:**
   - The status-aware `TOOL_BLOCKED` message (the #1 fix shipping in #468) gains,
     for the config/user/pending cases: *"Run retrieve_tools with
     include_disabled:true to see locked capabilities and remediation."*
   - When `retrieve_tools` returns **0 callable results** but the always-on
     drop-counter (§4.1) is > 0, append a one-line note to the result:
     *"N relevant tools exist but are locked; retry with include_disabled:true
     for details."* — count only, never the entries, so the nudge is a few
     tokens regardless of how many are locked.

### 4.5 Error handling & edges

- Classification storage error → `disabled_unknown` (never a misleading
  remediation).
- `server_disabled` reliability caveat: a fully-disabled server does not re-list
  its tools, so its entries surface in `retrieve_tools` only if stale in the
  index. The authoritative signal for a fully-off server is the
  `upstream_servers` server `state`, not search. Documented as a known limitation;
  no code attempts to "freshen" a disabled server's tools.
- Backward compatibility: default path (`include_disabled` absent/false, all
  tools callable) is byte-for-byte unchanged. New behavior is additive behind the
  flag and the conditional `tools` block.

## 5. Components & boundaries

| Unit | Responsibility | Depends on |
|------|----------------|------------|
| `classifyDisabledTool(server, tool) -> status` | Pure mapping from (config, approval record, server-enabled state) to one of 5 statuses. No I/O beyond reads already done. | `Runtime.IsToolConfigDenied`, storage approval lookup, server-enabled check |
| `retrieve_tools` handler change | Split dropped results into `disabledResults`, cap, attach `remediation` map, emit 0-result nudge | `classifyDisabledTool` |
| `upstream_servers` list/get change | Per-server count rollup, conditional emit | `classifyDisabledTool`, StateView snapshot |
| `TOOL_BLOCKED` message (#468) | Status-aware text + flag pointer | `IsToolConfigDenied` (already in #468) |

`classifyDisabledTool` is the single source of truth for "why is this tool not
callable" used by both surfaces — it can be unit-tested in isolation against the
five branches without standing up an MCP server.

## 6. Testing

- **Classifier (`internal/server` or `internal/runtime`):** table test, one case
  per status including first-match-wins ordering (e.g. a tool that is both
  config-denied *and* user-disabled resolves to `disabled_by_config`) and the
  `disabled_unknown` fallback on injected storage error.
- **`retrieve_tools`:**
  - `include_disabled` absent/false → result identical to today (regression guard,
    reuse existing fixtures).
  - `include_disabled` true → callable-first ordering preserved; cap of
    `min(limit,10)` enforced; `remediation` map keyed only by present statuses;
    agent-scope filter still applied before classification.
  - 0 callable + locked matches → nudge note present with correct count;
    0 callable + no locked matches → no nudge.
- **`upstream_servers`:** `tools` block omitted when all callable; present and
  correct (zero sub-keys omitted) when mixed.
- **OAS:** regenerate `oas/swagger.yaml` + `oas/docs.go` for the new
  `include_disabled` param and disabled-entry/`tools` shapes; run
  `./scripts/verify-oas-coverage.sh`.
- Full suite: `go test ./internal/... -race`, `./scripts/test-api-e2e.sh`.

## 7. Out of scope / future

- Surfacing locked tools in the Web UI search (the UI already shows per-tool lock
  badges on the server detail page from #468).
- A "request enable" workflow where the agent programmatically asks the user to
  approve — this design only *informs*; acting on it is the agent's/user's call.
- Freshening a disabled server's tool list for more reliable `server_disabled`
  discovery.
