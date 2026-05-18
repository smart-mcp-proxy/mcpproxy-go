# Phase 0 Research: Agent-Discoverable Disabled Tools

All design questions were resolved during brainstorming (design of record:
`docs/superpowers/specs/2026-05-18-agent-discoverable-disabled-tools-design.md`).
No `NEEDS CLARIFICATION` markers in the spec. This file records the decisions and
the codebase facts they rest on.

## Decision 1 — Opt-in parameter (not always-on, not ranked-inline)

- **Decision**: A `include_disabled` boolean on `retrieve_tools`, default false.
- **Rationale**: Zero token cost on the default path (SC-001/SC-005); fully
  backward compatible; no change to BM25 ranking. The only weakness
  (discoverability) is closed by the reactive nudge + static hint.
- **Alternatives considered**: (A) always inline demoted — pays tokens every
  search; (B) only-if-top-N — ranking complexity for marginal gain.

## Decision 2 — Lean entry + once-per-response remediation map

- **Decision**: Locked entry = {name, server, one-line description, status};
  one `remediation` map at response top, keyed only by present statuses.
- **Rationale**: Best token/clarity ratio. Per-status prose is amortized; an
  agent rarely needs a locked tool's full input schema to tell the user to
  unlock it.
- **Alternatives considered**: status-only (agent must know each enum's meaning);
  full schema parity (largest payload, unnecessary).

## Decision 3 — Five-state status with fixed precedence

- **Decision**: `server_disabled` → `disabled_by_config` → `disabled_by_user`
  → `pending_approval` → `disabled_unknown`, first match wins.
- **Rationale**: Each maps 1:1 to a distinct remediation. The 5th
  (`disabled_unknown`) prevents a transient lookup error from emitting a *wrong*
  remediation (SC-003).
- **Alternatives**: two-state (forces wrong bucketing of quarantine/server-off);
  raw reason passthrough (unpredictable for agent branching, more tokens).

## Decision 4 — `upstream_servers` conditional counts only

- **Decision**: Per-server `tools` block with counts by reason, emitted only
  when a non-callable count > 0; zero sub-keys omitted.
- **Rationale**: Cheapest possible "hidden capability exists here" signal;
  fully-callable servers gain 0 bytes (SC-005). Names/remediation stay in the
  on-demand `retrieve_tools` path.
- **Alternatives**: no change (agent learns only by accident); counts+names
  (the spam we explicitly avoid).

## Decision 5 — Reactive discovery (static hint + nudges)

- **Decision**: One sentence in the `retrieve_tools` description + a
  status-aware `TOOL_BLOCKED` message + a 0-callable-result count nudge.
- **Rationale**: Static hint removes the wasted empty-search round-trip; the
  reactive nudges guarantee discovery even if the agent ignored the schema text
  (SC-006).

## Codebase facts the design rests on

- **Locked tools are already in the Bleve index.** `DiscoverAndIndexToolsForServer`
  (`internal/runtime/lifecycle.go`) indexes everything `client.ListTools()`
  returns; no callability filter at index time.
- **Filtering is request-time only.** `handleRetrieveToolsWithMode`
  (`internal/server/mcp.go`) builds `callableResults` by `continue`-ing past
  any result where `isToolCallable` is false. The change is to split that loop,
  not add a data path.
- **`Runtime.IsToolConfigDenied(server, tool)`** (added in #468,
  `internal/runtime/tool_quarantine.go`) is the authoritative config-denial
  signal. Reused unchanged.
- **`isToolCallable` collapses reasons**: server-not-enabled, config-denied,
  approval `Disabled`, approval pending/changed, storage error. The classifier
  re-derives the precise reason for *discovery only*; enforcement stays
  collapsed.
- **`MCPProxyServer.blockedToolMessage` / `blockedToolMessageFor(bool)`** already
  exist (status-aware, landed via the #468 PR). Spec FR-008's discovery-path
  pointer (the "retry with include_disabled" sentence) is added on top here,
  guarded so it appears only once `include_disabled` exists.
- **Telemetry** counters are in-memory only (spec 042 pattern); reuse
  `recordBuiltinTool`-style counters — no persistence.

**Output**: No unresolved unknowns. Ready for Phase 1.
