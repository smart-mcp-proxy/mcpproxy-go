# Phase 0 Research: token-bench (spec 103)

Five topics, resolved 2026-08-31 against the tree at `b7d803e76` and against the suites'
current published code. Every decision below carries the evidence that settled it.

---

## 1. How to attach to the existing `bench/` harness

**Decision**: Use the "new measurement mode" seam twice — a flag on `cmd/bench`, a driver file
in package `bench`, and an additive report block. Add no new arm, no new binary, no new
package tree beyond one loader.

**Rationale**: `bench/` has three distinct extension seams and only one of them fits:

| Seam | For | Fits spec 103? |
|---|---|---|
| New **arm** (`bench/arms/`) | one deterministic way of rendering *tool definitions* into context text | **No** — spec 103 measures modes, not renderings. The renderings already exist. |
| New **corpus** (`bench/corpusio/` + an `OfflineSection`) | one more frozen tool universe to score | **No** — replay crosses the mode matrix; it is not one more corpus |
| New **measurement mode** (flag + driver + report block) | a new kind of measurement | **Yes, twice** |

The precedent to copy is spec 085's flip gates: a `-flip-gates` flag, a driver
(`bench/flipgate.go`) parameterised over a function type so the arithmetic is unit-testable
with no proxy running, production transport isolated in `bench/mcpcaller.go`, and the result
hung off the live report. `-live` is the same shape at larger scale.

**Alternatives considered**: adding replay as an arm (rejected — an arm renders tool
definitions and must satisfy the arm-interface contract including determinism over a corpus,
which a session trace is not); adding it as a corpus (rejected — a corpus is one tool universe
scored by many arms, whereas replay is one workload scored across the mode matrix).

**Report extension cost**: additive optional fields with `omitempty`, plus matching optional
properties in `specs/083-discovery-profiler/contracts/report-v2.schema.json`, plus one
provenance key per block. **No `report_version` bump** — that is reserved for changing an
existing field's meaning or shape, and spec 103 changes none. Precedent: `LatencyV2`'s
`rest_search`/`mcp_discovery` were added beside the flat fields the same way.

> The schema has no `additionalProperties: false`, so an undeclared block would validate
> silently. Do not rely on that — the schema file is the reviewed contract and new blocks must
> be declared in it.

**Finding that becomes work**: SC-011 ("no generated report is committed") is enforced only by
`bench/.gitignore` and convention. There is no CI job or test that fails on a committed
report. The gate must assert that nothing under the results directory is **tracked** —
`test -z "$(git ls-files bench/results)"`. A `git status --porcelain` check does NOT work:
porcelain omits ignored files and would stay silent about an already-committed report.

---

## 2. What replay can consume, and the privacy posture it forces

**Decision**: Consume JSONL from `GET /api/v1/activity/export?format=json` (equivalently
`mcpproxy activity export --format json`) in a new `bench/replaycorpus/` package. **Default to
bodies-off.** Add `request_bytes` / `response_bytes` to the export contract.

**Rationale**:

- **CSV is unusable** — it drops `work_session_id`, arguments, response and all byte fields.
- **Nothing in `bench/` references activity today**, so the loader is purely additive.
- **The export path never masks.** `maskActivityPayloads` is wired into the list and detail
  handlers only. Bodies-on export is raw and unmasked, by design — it is the
  compliance/incident-response surface. This is why bodies-off is the default and bodies-on is
  an explicit opt-in with a warning.
- **`has_sensitive_data` is set asynchronously *after* the record is persisted.** A freshly
  exported record can be sensitive but not yet flagged. Exclude-by-flag is therefore a
  best-effort reducer and must never be documented as a guarantee.

**The one worthwhile backend change**: `request_bytes` and `response_bytes` exist on the
storage record, documented as *measured pre-truncation*, but are absent from the export
contract entirely. Adding them (two DTO fields, two lines in the export projection) lets a
truncated record carry an explicitly-estimated response cost instead of being dropped.

**Correction applied after review, then corrected again**: an early draft claimed these give
an *accurate* cost basis without bodies. They do not — they are byte lengths, and tokenizing
needs the text. A second draft over-corrected, claiming tool-surface cost carries the headline
for all five cells. The precise position:

- **A fleet input is mandatory.** A menu is a property of the tool definitions, and the export
  carries no fleet snapshot, so replay must be paired with a frozen fleet corpus or a live
  proxy. A recording-only invocation computes nothing.
- **No cell yields an absolute complete-workload cost bodies-off.** Complete workload includes
  every consumed response, and that text is absent. Bodies-off gives menu cost per cell plus
  the cross-mode DELTA between the two direct cells, whose identical call responses cancel.
- **`code_exec` and both `retrieve_tools` cells have no bodies-off delta either**: the
  code-execution surface also registers `retrieve_tools`
  (`internal/server/mcp_routing.go:670-676`), and the `retrieve_tools` cells' mode changes the
  response body itself.
- **Tokenization of bodies must happen inside the loader**, so no text crosses the boundary
  that the privacy invariant draws.
- **`retrieve_tools` response cost is recoverable with bodies on** — the handler writes the
  FULL response to the activity log (`internal/server/mcp.go:2061-2064`). It is unavailable
  only in the bodies-off configuration, not unrecoverable in general.
- **The export carries no fleet snapshot**, so a replay scores a recorded workload against
  today's fleet. Internally valid across modes; not a historical reconstruction.

**Alternatives considered**: reading BBolt directly (rejected — bypasses the contract, couples
the harness to storage internals, and defeats the privacy gating entirely); building a new
capture mechanism (rejected — explicitly out of scope in the spec, and unnecessary).

---

## 3. The mode matrix: 5 valid cells, not 12

**Decision**: A matrix cell is **one MCP endpoint plus the one serialization axis that governs
that endpoint's surface** — not a 3×2×2 config product.

| # | Routing mode / endpoint | `tool_response_mode` | `direct_tool_response_mode` | Notes |
|---|---|---|---|---|
| 1 | `retrieve_tools` — `/mcp/call` | full | n/a | today's default |
| 2 | `retrieve_tools` — `/mcp/call` | compact | n/a | spec 085 |
| 3 | `direct` — `/mcp/all` | n/a | full | pre-102 direct |
| 4 | `direct` — `/mcp/all` | n/a | deferred | spec 102 |
| 5 | `code_execution` — `/mcp/code` | forced full | n/a | needs `enable_code_execution: true` |

Plus the existing `baseline` arm as the FR-020 denominator.

**The 7 skipped combinations and their FR-017 reasons**:

- `code_execution × compact` (×2 across the direct axis) — **forced**: the code-execution
  surface overwrites the response mode with `full` and blanks the detail parameter; `detail`
  is not even in that surface's schema.
- `direct × {full, compact}` on the *discovery* axis (×2) — **inapplicable**:
  `tool_response_mode` has exactly one consumer, inside the `retrieve_tools` handler. The
  direct surface has no `retrieve_tools` tool at all.
- `retrieve_tools × deferred`, `code_execution × deferred` (×2) — **inapplicable**:
  `direct_tool_response_mode` is read only when building the direct listing.
- `code_execution` with `enable_code_execution: false` — **degenerate**: the surface can
  discover tools and call none of them.

**Crossing the matrix is cheap.** All three routing-mode servers are built at startup and all
three endpoints are permanently mounted regardless of config — the source comments this
explicitly — so the routing-mode axis is selected **by URL**, with no restart. The two
serialization axes do need config, but **both hot-reload**: `tool_response_mode` is read from
the live snapshot on every call (documented as taking effect "on the very next call, without
reconstructing the server") and `direct_tool_response_mode` is rebuilt on the `config.reloaded`
event. So the whole matrix crosses on ONE long-lived instance, with a config apply between
serialization cells.

**Reuse the existing skip shape** rather than inventing one: `ArmResult.Skipped` /
`SkipReason` and the `SkippedArmResult` constructor already implement "a skipped row with a
reason, never a zero", which is exactly what FR-017 asks for.

**This directly simplifies the spec**: FR-015/FR-016/FR-017 describe a matrix that is 5 rows
plus 7 documented skips, not a 12-cell product.

---

## 4. Public agent-loop suite

**Decision**: Adopt **MCPMark** (`eval-sys/mcpmark`, Apache-2.0), SHA-pinned. Adopt
**MCP-Atlas** only as a *corpus donor* for fleet-shape work, not as an agent loop.

**Both previously-unverified questions resolve in MCPMark's favour**:

1. **Per-task token counts**: yes. Its per-task `meta.json` carries
   `execution_result.success`, `token_usage{input, output, total, reasoning}`, `turn_count`
   and `agent_execution_time`; `messages.json` holds the full trajectory, which is what makes
   corrective-vs-infrastructure retry classification possible. Its aggregator already emits
   `pass@1{avg,std}`, `pass@k`, `pass^k` and per-run cost.
2. **Can it point at a single endpoint?** Yes — the load-bearing question, and the answer is
   that MCPMark has exactly **one** MCP factory seam. Pointing it at mcpproxy is a single
   env-gated `elif` branch returning an HTTP MCP server at mcpproxy's URL. Its transport is
   the official Python SDK streamable-HTTP — the same transport `bench/mcpcaller.go` already
   drives. `list_tools()` and `call_tool()` are the only two calls its agent loop makes.

**Consequence**: the spec's fallback (build an in-repo task set) is **not needed**.

**Fleet shaping matters for honesty.** Stock MCPMark connects to one server per run, so the
per-task fleet is a single server's toolset — far too small for mcpproxy to show its
asymptote. Configure mcpproxy with *all* MCPMark services simultaneously while running one
service's tasks. This is a pure mcpproxy-config change and it is also the honest FR-020
baseline: the same agent, the same tasks, all tools loaded directly.

**Credential-free core**: filesystem (30 tasks) + postgres (21) need zero third-party
credentials and are fully local — the FR-027-clean starting point. GitHub (23) and Notion (28)
add real-fleet realism but perform real writes and need credentials.

**Alternatives considered**: MCP-Atlas as the loop (rejected — its value is its 36-server /
220-tool *shape*, ideal for FR-024's fleet-shape decomposition, but it is not an agent-loop
harness); τ²-bench (rejected — needs an MCP bridge that does not exist); BFCL / ToolBench /
API-Bank (rejected — not MCP); LiveMCPBench as a loop (rejected — stale since 2025-08, though
its committed 527-tool snapshot remains useful as a corpus).

---

## 5. Token accounting for a live loop

**Decision**: Keep tiktoken (`cl100k_base`) as the *sole* source for everything deterministic.
Read provider-reported `usage` **directly off the model response** in the agent driver for the
live loop. **Never sum the two.**

**Rationale**: The harness already solved "two token shapes must not be mixed into one
number", for schemas: when either side lacks schema counts, the savings ratio is **withheld
with a stated reason** rather than computed. The break-even computation carries the same rule
in its own doc comment and errors rather than fabricating a verdict. Spec 103's never-summed
constraint is that same rule on a new axis, so it is enforceable with machinery that already
exists — the extension is a new section and a source label, not new aggregation logic.

For the provider this project would pin, `usage` maps 1:1 onto FR-014's required split
(input / output / cache-read) with no derivation, so reading response fields is simpler than
proxying. **This is provider-specific and unverified until the provider is pinned** — the
mapping must be re-checked against whichever provider is chosen, and it does NOT hold for the
suite's own per-task output (see the gap below).

**Gap to close at task time**: the suite's own per-task output records input / output / total /
reasoning — it has **no cache-read field**. So FR-014's cache-read axis cannot come from the
suite's report; it must be captured in the driver from the provider response, or explicitly
declared out of reach for suite-driven runs. The single MCP-factory patch alone does not
establish FR-014 coverage. An LLM proxy is kept
only as the fallback for a closed-box suite whose loop cannot be edited — which MCPMark, being
patchable at one seam, is not.

**Alternatives considered**: an LLM proxy as the primary path (rejected — an extra hop and a
second failure mode on a run that costs real money, and it sees nothing the response object
does not already carry; a silently dropped record would understate cost *in the project's
favour*, the same failure class FR-002 forbids for truncation); feeding provider-derived retry
rates into the existing session estimator (rejected — that formula's other terms are tiktoken
counts, so it would produce exactly the summed hybrid the spec forbids; a measured session
cost must be a new row type sourced wholly from provider usage); reusing
`internal/server/tokens/savings.go` (rejected — a production/Web-UI path whose "query result"
is an arbitrary prefix of the tool list rather than a ranking, and whose "all tools" side is a
hand-built JSON shape rather than the live builders; reusing it would reintroduce exactly the
drift IC-002 forbids).

**There is no existing code anywhere in the repo that reads a provider usage field.** Every
token count in the tree is produced by the local tiktoken wrapper. Cache-token handling is
entirely greenfield — no `cache_read` concept exists anywhere.

### Two hazards this creates

- **`ReportV2.Tokenizer` is a report-level singleton** naming one estimator. Once provider
  usage enters the same envelope a reader could take that field to describe it too. Resolve
  **additively**: leave the existing field's meaning intact and give each new block its own
  accounting-source field (provider + pinned model). Narrowing the existing field would itself
  be a meaning change and would require a version bump.
- **`RetryRateForArm` returns 0.0 for unknown arms**, indistinguishable from a measured 0.0.
  With FR-013 replacing assumed rates only where measurements exist, one table could mix a
  measured rate and a defaulted one under a single section-level `estimated` badge. Session
  cost rows need a **per-row** provenance field.

---

## Cross-cutting: reproducibility hazards found

1. **tiktoken downloads its vocabulary at runtime** from a remote host unless
   `TIKTOKEN_CACHE_DIR` / `DATA_GYM_CACHE_DIR` is set. Nothing in the repo or in
   `bench.yml` sets it, so the documented "offline estimator" property holds only after a warm
   cache. **An outside reproducer on a restricted network fails at step one** — which defeats
   SC-004. Fix: setting the variable is necessary but NOT sufficient — it names a cache without
   filling one. CI must also **populate or restore** the cache (a cache-restore step, or a
   vendored copy), and the reproduction procedure must tell an outsider to warm it once.
2. **The tokenizer caveat is unsourced and internally contradicted.** The repo states up to
   ~60% underestimation versus Claude; Anthropic's own guidance says ~15–20% on typical text
   (more on code). The two differ threefold and neither is sourced in-repo. Publishing
   FR-022's numeric tolerance on top of either is a credibility risk. Fix: measure it with
   `count_tokens` against the frozen corpora for the pinned model — no inference spend, and it
   doubles as a genuine `measured` figure for the per-fleet-shape ceiling.
3. **The two savings numbers have never been compared.** The Web-UI/status figure and the
   bench headline are independently implemented over the same fleet. The spec's own
   Assumptions say a contradiction between measurements is a finding to investigate. Running
   both against one live proxy is cheap and should happen early.

## Deferred to the operator

- Pinned model and spend ceiling for the live loop (US1 is unblocked without it).
- Whether cache-read tokens count toward the headline composite (a definition decision).
- Whether the `playwright_webarena` task subset (21 of 127) needs a self-hosted instance.
- The exact corrective-vs-infrastructure retry boundary. Whatever rule is chosen **must be
  applied identically to the baseline arm and the proxy arms**, or the comparison is biased
  toward whichever arm carries richer error signal.
