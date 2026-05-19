# Phase 0 Research — Global Tools Page

All open questions were resolved during the brainstorming session with the user; this records the decisions and rejected alternatives. No NEEDS CLARIFICATION remain.

## D1 — Global tool list assembly

**Decision**: New `GET /api/v1/tools` aggregation endpoint, server-side merge of all configured servers.
**Rationale**: One request scales to 500–700 tools; the per-server enrichment loop (approval/disabled/config-denied) already exists in `handleGetServerTools` and the tool-export handler — hoist it over `controller.GetAllServers()`. Single source of truth for web + CLI.
**Alternatives rejected**: Client-side N+1 fan-out (current orphaned `Tools.vue`) — slow, no global sort/paginate, brittle on partial server failure. Reusing the Bleve/BM25 index — excludes disabled tools (spec 049) and imposes relevance order conflicting with column sort.

## D2 — Usage count & last-used

**Decision**: New `storage.Manager.AggregateToolUsage(since time.Time) (map[string]ToolUsageStat, error)` — one reverse cursor pass over `ActivityRecordsBucket`, keyed `serverName + "\x00" + toolName`, counting `tool_call` records with `Timestamp >= since`, tracking max timestamp. Window fixed at 30 days (`time.Now().Add(-30*24h)`).
**Rationale**: The activity bucket is the only authoritative usage source; a single bounded pass is O(records-in-window) and avoids any new persisted counter (no schema change, consistent with privacy/storage minimalism). `Tool` contract already carries `Usage int` / `LastUsed *time.Time` — no contract churn.
**Alternatives rejected**: Maintaining a live per-tool counter bucket — new write path, migration, retention coupling, out of scope per spec. Configurable window — explicitly deferred (spec Out-of-Scope).

## D3 — Search semantics

**Decision**: Client-side (and CLI-side) deterministic substring filter over name+description+server on the full aggregated set; not BM25.
**Rationale**: Audit/cleanup requires seeing *every* match including disabled/config-denied tools, in a stable user-sortable order. At ≤1000 tools the full set filters in <1s client-side (SC-003).
**Alternatives rejected**: BM25 — hides disabled tools, relevance order fights sort (see D1).

## D4 — Disposition of existing `Tools.vue`

**Decision**: Rewrite in place as the table page; delete dead grid/list/card code. Add `/tools` route + WORKSPACE sidebar entry with live count badge.
**Rationale**: The file is orphaned (no route, not in nav). Two competing "tools" surfaces would confuse. Modeling on `Activity.vue` reuses an established, tested layout (stat cards + filter bar + table + pagination + `data-test` hooks) — matches the user's annotated mockup.
**Alternatives rejected**: Keep grid + add table toggle (more surface, no value); brand-new component (leaves dead code, risks divergence).

## D5 — CLI parity placement

**Decision**: Extend the existing `mcpproxy tools` command group: make `tools list` global when `--server` is omitted (filters: `--server`, `--status`, `--risk`, `--approval`; `-o json|yaml`), add `tools enable|disable <server:tool ...>`.
**Rationale**: Project ships CLI/UI parity for every management surface (activity, tokens, upstream, quarantine). Same consolidated endpoint backs it. Today `tools list --server` is debug-only and server-scoped — making the no-arg form global is the least-surprising extension.
**Alternatives rejected**: Separate spec/command group — fragments a cohesive feature; new top-level command — inconsistent with the existing `tools` group.

## D6 — Batch enable/disable mechanism

**Decision**: Orchestrate the existing authenticated per-tool endpoint `POST /api/v1/servers/{id}/tools/{tool}/enabled`, grouped by server, with progress + partial-failure summary (UI toast; CLI per-target lines + non-zero exit on any failure).
**Rationale**: No new write path or persistence; the per-tool toggle is already the authority (spec 049 layered filter). Config-denied tools cannot be force-enabled — server rejects, surfaced as a per-target failure.
**Alternatives rejected**: New bulk endpoint — extra surface for a v1 that's pure orchestration; can be added later if latency demands it (≤700 sequential small POSTs is acceptable for an admin action).
