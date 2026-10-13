# Design: how mcpproxy should honestly measure and present token savings

**Task**: follow-up to T061 (`webui-savings-vs-bench.md`).
**Date**: 2026-09-01.
**Status**: design proposal. No production code changed. All figures below were
measured on an isolated instance (scratch data-dir + config, port 18744, killed
after) over the committed 7-server reference fleet
(`specs/065-evaluation-foundation/datasets/snapshot-servers.config.json`, 45 tools).

---

## 0. Summary

The Web-UI savings figure is not merely optimistic. On the reference fleet it is:

1. **Non-deterministic.** 25 consecutive requests to `GET /api/v1/stats/tokens`
   over an unchanging fleet returned six distinct values spanning
   **52.35 % – 78.80 %** (spread 26.45 points).
2. **Opposite in sign to the measured truth.** A real three-discovery-call
   session on that fleet cost **9 825 tokens** through mcpproxy versus **4 368**
   without it — the proxy cost 2.25× the baseline, a net **−5 457 tokens**.
3. **Contradicted by its own tooltip**, which tells the user the number "changes
   when you add, remove or reconnect servers — not with each call"
   (`frontend/src/views/Dashboard.vue:833-836`).
4. **Mislabelled**: two surfaces claim the query-side number is a BM25 result
   (`Dashboard.vue:486`, `Usage.vue:78`); the code takes an unranked prefix
   (`internal/server/tokens/savings.go:139`).

The bench headline is closer but **also wrong, in the other direction**: it
prices a proxy menu with code execution force-enabled, which the shipped default
is not.

Recommendation: **stop simulating and start measuring.** Replace the single
percentage with a three-row session ledger computed from the proxy's own
activity log, which already carries everything required. Keep break-even, but as
an explanation, not the headline — and add a second break-even (in fleet size)
because the call-count one is undefined in exactly the regime the user needs an
answer in.

---

## 1. The honest quantity, stated precisely

For one **work session** `s` under fleet `F`:

```
C_baseline(s) = B                                  (no proxy)
C_proxy(s)    = M + Σ_{c ∈ D(s)} r_c               (with proxy)

Net(s) = C_baseline(s) − C_proxy(s) = (B − M) − Σ r_c
```

where

| Term | Meaning | Measured on the reference fleet |
|---|---|---|
| `B` | every upstream tool definition, once, as the client's `tools/list` renders it | **4 368** tokens (45 tools) |
| `M` | mcpproxy's own advertised menu, **as deployed**, carried for the whole session | **4 712** tokens (12 built-ins) |
| `D(s)` | mcpproxy built-in calls whose responses are pure overhead relative to baseline: `retrieve_tools`, `describe_tool`, `read_cache` | 3 calls |
| `r_c` | tokens of each such response | 681 / 1 794 / 2 638 |

`call_tool_read|write|destructive` responses are **excluded** from `D(s)`: they
carry the upstream tool result, which the baseline agent pays for too, so the
term cancels. This is exactly the population split
`storage.InternalCallToolPrefix` already encodes
(`internal/storage/activity_call_population.go:79-91`).

**Net on the reference fleet: 4 368 − (4 712 + 5 113) = −5 457 tokens.**

### Four things this makes visible that the current formula cannot

- **It is per-session, not per-request.** The current label — "smaller tool
  context per request" (`Dashboard.vue:328`), "Tokens saved per request"
  (`Usage.vue:67`) — describes a quantity that does not exist. There is no
  per-request saving; there is a fixed menu cost and a per-call spend against it.
- **`(B − M)` is a budget, not a saving.** Each discovery call spends from it.
  Sign flips at `n* = (B − M)/r̄`.
- **When `B ≤ M` the product costs more before a single call is made.** Here
  `B − M = −344`. No amount of good retrieval recovers that.
- **Response *size* is the lever, not call count.** Measured spread across three
  ordinary queries at `tools_limit: 15` was 681–2 638 tokens (3.9×). `tools_limit`,
  `tool_response_mode: compact` and schema deferral are all mcpproxy's own knobs.
  Call count is the agent's; response size is ours.

### Where the framing in the brief needs correcting

1. **"Every upstream tool definition, once" understates the baseline's
   billing.** In a multi-turn conversation the menu is re-sent every turn. So is
   the proxy's. The proxy's cost is *back-loaded* (menu at turn 1, menu + r₁ after
   the first discovery), so a per-turn integral favours the proxy slightly more
   than the additive comparison above. **Not quantified — open question, §7.**
2. **Prompt caching is unmodelled by every figure here.** A stable tool menu is a
   maximally cacheable prefix, billed at a fraction of input rate on reads.
   Appending discovery responses does not invalidate that prefix, so both sides
   cache; I *reason* the sign is preserved but the dollar magnitudes are not
   derivable from any of these token counts. **Unverified — open question, §7.**
3. **`describe_tool` and `read_cache` belong in the ledger** and are absent from
   the brief's framing. Under Spec 085/102 an agent that receives compact
   signatures and then calls `describe_tool` pays for the same schema twice.
4. **The baseline is partly counterfactual at large fleets.** A user who does not
   run mcpproxy often *cannot* connect every server — Cursor's 40-tool limit and
   OpenAI's 128-function cap (`README.md:41`). Above those caps the honest
   comparison is not "500 tools in context" but "the user connected fewer
   servers". That makes mcpproxy's value a **capability**, not a token saving —
   which matters a great deal for §6.

---

## 2. Is break-even the right frame? Partly.

`bench/breakeven.go:25-40` computes `n* = (naive_full_menu − proxy_menu) / mean_response`.
The arithmetic is right and the guard rails are good: `bench/breakeven.go:31-34`
returns `NoBreakEven` when the numerator is ≤ 0, and `:36-38` refuses to divide
by an unmeasured mean rather than fabricating a verdict.

**Three reasons it should not be the headline.**

1. **It is silent exactly where it matters.** On the reference fleet
   `B − M = −344`, so `ComputeBreakEven` returns `NoBreakEven` with
   `BreakEvenCalls = 0`. The dashboard would have *nothing to say* in the one
   regime where the user is being overcharged. A metric that goes quiet on bad
   news is the same failure as the clamp, wearing a better hat.
2. **It inverts the product message.** "You can afford 3 more discovery calls"
   makes more usage read as worse. True, but it is a strange thing for a product
   to put on its own dashboard as the primary number.
3. **It is modelled when a measurement is available.** §3 shows the real data is
   already recorded.

**Recommended frame: measured session ledger as the headline, two break-evens as
the explanation.**

- **(a) Call break-even** `n* = (B − M)/r̄` — how many discovery calls the fleet
  affords. Defined only when `B > M`.
- **(b) Fleet break-even** — how many upstream tools before `B` exceeds
  `M + Σr` at the user's measured call rate. Defined precisely when (a) is not,
  and it is the actionable one: **fleet size is what the user controls**, and it
  is the axis along which savings actually scale.

Together they always leave something true to say. Neither alone does.

---

## 3. What to compute it from: the activity log already has it

### Verified present today

| Need | Where | Verified |
|---|---|---|
| Session grouping | `ActivityRecord.WorkSessionID` (`internal/storage/activity_models.go:230`) | Three probe calls grouped under one id `ws-79fc67cd0eca9c32` |
| Discovery call count per session | filter `type=internal_tool_call`, `tool_name=retrieve_tools` (`internal/httpapi/activity.go:44` supports `work_session_id`) | 3 records returned |
| Exact response text the agent paid for | `ActivityRecord.Response`, written pre-storage-truncation (`internal/server/mcp.go:2093`) | Stored lengths **2 884 / 11 410 / 8 271** matched the wire payloads **exactly** |
| Truncation flag | `ResponseTruncated`, propagated via `emitActivityInternalToolCallTruncated` (`internal/server/mcp.go:2093` → `internal/runtime/activity_service.go:859,899`) and exported (`internal/httpapi/activity.go:464`) | present |
| Retention window | 90 days / 100 k records / 256 MB (`internal/config/config.go:465-467`, defaults `:1768`) | ample |

Note that `specs/103-token-bench/contracts/replay-input.md` lists the truncation
flag and the export byte fields as *required backend changes*. **Both have since
landed** — that section of the contract is stale.

### Verified missing

1. **`retrieve_tools` records carry no byte counts.** The internal-tool-call
   record is built without `RequestBytes`/`ResponseBytes`
   (`internal/runtime/activity_service.go:884-901`); the upstream `tool_call`
   path does set them (`:607-630`). Confirmed live: `request_bytes` and
   `response_bytes` were `null` on all three probe records.
2. **No token count is persisted anywhere per record.** Tokenizing on read is a
   full scan of the activity window per dashboard request.
3. **The existing usage aggregate cannot help as-is.** `applyToolRollup`
   deliberately drops `internal_tool_call` from the per-tool rollup
   (`internal/runtime/usage_aggregate.go:264-267`) — and its reasoning is
   *correct*: mixing mcpproxy's own latency and sizes into upstream percentiles
   would invent tool rows no upstream owns. Discovery cost needs its **own**
   rollup, not admission into that one.
4. **No fleet snapshot per session.** Historical sessions can only be scored
   against *today's* catalog. `bench/replay.go:47-57` already states this
   constraint and `ReplayCounterfactualLabel` (`:92-101`) is the right wording to
   reuse verbatim.

### The privacy objection does not apply here

`bench/replay.go:22-45` is right to default bodies-off: the **export** path is
unmasked, so an export is raw user traffic leaving the machine. An **in-process**
aggregate is a different posture entirely — the proxy already holds these
records, tokenizes in memory, and emits only counts. That is the same boundary
`replay-input.md` §Privacy rule 5 describes ("nothing crosses the loader boundary
but counts"). This is the strongest argument for computing the figure **inside
the server** rather than reusing the replay pipeline.

---

## 4. Replacing `estimateQueryResultSize`

**Recommendation: delete the simulation.** Use the measured distribution of real
discovery responses; report **median and p90**, never a bare mean, alongside the
sample count.

Why not "a real BM25 ranking over representative queries":

- It fixes the smaller of the two errors. The dominant error is the missing `M`
  term (4 712 tokens), not the ranking (which changed `average_query_result_size`
  between 993 and 2 232 across my samples).
- It replaces one invented number with another. You would have to author the
  representative query set, and the headline becomes a property of a query set
  nobody agreed to — the thing `bench/README.md:60-63` already warns against.
- We can measure it. The measurement is exact (§3, byte-for-byte match).

**But keep a labelled estimate for cold start.** A fresh install with no recorded
discovery calls has no distribution. There, show the structural terms `B` and `M`
— which *are* computable with no history — and say plainly that the session
figure is not measured yet. Never silently substitute one for the other.

### The prefix bug is separate and shippable now

`estimateQueryResultSize` takes `allTools[:topK]` (`savings.go:139`), and
`allTools` is a flatten of `servers` in the order given
(`internal/server/tokens/savings.go:128-131`), and that order comes from
`GetAllServerNames` (called at `internal/runtime/runtime.go:1924`; defined at
`internal/upstream/manager.go:945-954`), which ranges over a Go map. Go
randomises map iteration order per range, so **which 15 tools form the "typical
query" is a fresh coin flip on every HTTP request**. That is what produced the
52.35 – 78.80 % spread. Sorting the returned names is a two-line fix that makes
the current figure at least *stable*, independent of everything else in this
document, and it directly falsifies the shipped tooltip at
`Dashboard.vue:833-836`.

---

## 5. The clamp, and what the UI should actually say

`savings.go:87-89` clamps negatives to zero. **Removing it is necessary but not
sufficient**: `Dashboard.vue:317` renders the chip only
`v-if="tokenSavingsData.saved_tokens_percentage > 0"`, so an unclamped negative
makes the chip *vanish*. That is the same dishonesty by omission, harder to
notice. Both must go together.

A bare "−125 %" is also not the answer. The honest message names the cause and
the lever.

### Regime A — measured, net positive

> **61 % less tool-definition context**
> Median 9 400 tokens saved per session, over your last 12 sessions.
> 312 upstream tools · mcpproxy menu 4 712 · typical session: 3 discovery calls,
> ~1 700 tokens each.

### Regime B — measured, net negative (the reference fleet today)

Lead with the ledger, not the percentage:

> **mcpproxy is costing you context on this fleet.**
>
> | | tokens |
> |---|---|
> | Loading all 45 upstream tools directly | 4 368 |
> | mcpproxy's own 12 tools (carried all session) | −4 712 |
> | Typical session: 3 discovery calls | −5 113 |
> | **Net per session** | **−5 457** |
>
> This flips as your fleet grows: at your current usage mcpproxy starts saving
> above roughly **95 upstream tools**.
> Cut discovery cost now: [use compact responses] · [lower tools_limit from 15]
> You are still getting quarantine, TPA scanning, and no 40-tool client limit.

### Regime C — not enough data

> **Not measured yet.** mcpproxy needs a few sessions with discovery calls before
> it can tell you. Structurally: your catalog is 4 368 tokens; the mcpproxy menu
> is 4 712.

**No percentage at all in Regime C.** A percentage with no denominator on screen
is the failure mode `bench/README.md:60-63` already forbids for published
numbers; it should be forbidden in the product too.

### Three rules the widget must obey

1. Every percentage carries its fleet size and session count on the same screen.
2. The widget must be capable of rendering bad news, in every regime, without
   disappearing.
3. Labels must describe the computation. "BM25 result" (`Dashboard.vue:486`) and
   "via BM25 discovery" (`Usage.vue:78`) are false today and must go regardless
   of whether the rest of this design is adopted.

---

## 6. Concrete code changes

### Tier 0 — outright bugs, shippable independently, no narrative risk

| # | Change | Anchor |
|---|---|---|
| 0.1 | Sort server names so the figure stops changing per request | `internal/upstream/manager.go:949-953` |
| 0.2 | Remove the false BM25 labels | `frontend/src/views/Dashboard.vue:486`, `frontend/src/views/Usage.vue:78` |
| 0.3 | Fix the falsified tooltip ("not with each call") | `frontend/src/views/Dashboard.vue:833-836`, duplicated at `frontend/src/views/Usage.vue:225-229` |

### Tier 1 — the ledger terms

| # | Change | Anchor |
|---|---|---|
| 1.1 | Add the `M` term. `ProxyModeToolDefs` fabricates `&config.Config{EnableCodeExecution: true}` and a zero-value `DisableManagement`/`ReadOnlyMode`, so it returns the **maximal** menu, not the deployed one. Needs a variant that calls `p.buildCallToolModeTools()` on the **live** `p`. | `internal/server/bench_export.go:43-49`; builders at `internal/server/mcp_routing.go:636,689`; management set `internal/server/mcp.go:1147-1148` |
| 1.2 | Unify the renderer. `savings.go` prices a `{"tools":[…]}` JSON envelope; bench prices `name\ndesc\ncanonical-schema`. Same fleet: **4 684** (API) vs **4 368** (bench) — a 316-token, 7.2 % gap purely from rendering. Promote `CanonicalToolText` to a package both import. | `internal/server/tokens/savings.go:97-123`; `bench/arms/baseline.go:41`; `bench/canonical.go:25`; `bench/tokens.go:154` |
| 1.3 | Remove the clamp | `internal/server/tokens/savings.go:87-89` |
| 1.4 | Delete `estimateQueryResultSize` | `internal/server/tokens/savings.go:126-168` |
| 1.5 | Rewire the caller | `internal/runtime/runtime.go:1966-1988` |

Tokenizer note: both sides use `pkoukk/tiktoken-go` and default to
`cl100k_base` (`bench/tokens.go:37`, `internal/server/tokens/models.go:63`), so
the BPE is not the problem — the *rendered string* is. But `internal`'s encoding
is operator-overridable (`internal/config/config.go:583`) and its tokenizer can
be disabled entirely, returning 0 (`internal/server/tokens/tokenizer.go:91-95`);
a disabled tokenizer must render "not available", never "0 % saved".

### Tier 2 — measurement from real sessions

| # | Change | Anchor |
|---|---|---|
| 2.1 | Set `RequestBytes`/`ResponseBytes` on internal-tool-call records (emitter-side, mirroring `rawByteSize` at `internal/server/mcp.go:2647`) | `internal/runtime/activity_service.go:884-901` |
| 2.2 | Add a **separate** discovery rollup — do not admit built-ins into `ToolUsage`; the existing exclusion is correct | `internal/runtime/usage_aggregate.go:264-267`, `:222-231` |
| 2.3 | Persist a token count per discovery record, or fold tokens into the rollup at write time; tokenizing the window per dashboard request will not scale to the 100 k-record default | `internal/config/config.go:466` |
| 2.4 | Exclude `ResponseTruncated` records from the response-cost mean and **count the exclusions**; the stored text overstates what the agent received | `internal/storage/activity_models.go:198`, `internal/server/mcp.go:2087-2093` |
| 2.5 | Carry the counterfactual caveat ("scored against today's fleet") into the UI, reusing the existing wording | `bench/replay.go:92-101` |

### Tier 3 — contract, surfaces, and the other savings numbers

| # | Change | Anchor |
|---|---|---|
| 3.1 | New payload fields; `frontend/src/types/api.ts` is **generated** — edit the generator and `make swagger` | `internal/contracts/types.go:370-377`, `oas/swagger.yaml:2626-2645`, `frontend/src/types/api.ts:517-523` |
| 3.2 | Second endpoint echoes the same figure | `internal/httpapi/activity.go:1022-1032` |
| 3.3 | **Dead surface**: `ServerStats.TokenMetrics` is declared in Go, Swagger and Swift and read by the macOS dashboard, but no Go code ever populates it. Decide: wire or delete. | `internal/contracts/types.go:367`; `native/macos/.../CoreProcessManager.swift:2056-2064`; `DashboardView.swift:78-93,379-427` |
| 3.4 | **Fourth, independent savings estimator**: `hidden × 150 tokens`, feeding telemetry activation. Only-positive by construction, unrelated to everything above. | `internal/server/mcp.go:1879-1892`; `internal/telemetry/activation.go:28,49` |
| 3.5 | Release gate snapshots `tokens_saved` | `cmd/release-gate/invariants.go:172,198-199` |

### Tests that will break (expected, not regressions)

- `internal/server/tokens/savings_test.go:72-73,120-121` — assert non-negative.
- `:178-180` — asserts `SavedTokens == Total − AvgQuery` **and** the ~60 % prefix
  arithmetic. Both are pins on the behaviour being removed.
- `:246-248` — asserts `> 70 %` savings.
- `internal/httpapi/activity_usage_test.go:138-141,275,283` — echo assertions.
- `tests/token-metrics.spec.ts:22-28,163-176` — **already stale**: asserts label
  strings (`Full Tool List Size`, `Typical Query Result`,
  `Per-Server Token Breakdown`) that no longer exist in `Dashboard.vue`. It is
  passing vacuously; treat a failure here as a real find, not a break.
- `internal/server/bench_export_test.go:16,41` pins `ProxyModeToolDefs` to the
  builders — keep that pin, add a live-config variant beside it.

---

## 7. Verified vs open

### Verified in this investigation

- 25 samples of `GET /api/v1/stats/tokens`, unchanging fleet: six distinct
  values, 52.35 – 78.80 %, spread 26.45 points. Root cause is map iteration in
  `GetAllServerNames` feeding the `allTools[:topK]` prefix.
- Live deployed menu = **4 712** tokens (12 tools, `code_execution` served as the
  113-char *disabled stub* — the shipped default). bench's in-process derivation
  reports 5 783 because it pins `EnableCodeExecution: true`; the ~1 071-token
  overstatement matches bench's own note at `bench/mcptools.go:21-24` (1 049).
  **Both published figures are wrong, in opposite directions.**
- Upstream catalog: 4 368 tokens (bench renderer) / 4 684 (the API's own
  renderer) — the renderer gap is real and 7.2 %.
- `B − M = −344`: the proxy is behind before any call. `ComputeBreakEven` returns
  `NoBreakEven` here.
- Three real `retrieve_tools` calls: 681 / 1 794 / 2 638 tokens. Session net
  **−5 457** tokens versus baseline, while the dashboard read +52 … +79 %.
- Activity records: `work_session_id` populated; `request_bytes`/`response_bytes`
  `null`; stored `Response` length identical to the wire payload.
- Both backend changes `replay-input.md` demands (truncation flag on internal
  records; byte fields in the export DTO) have landed.

### Open — stated as unresolved, not guessed

1. **Prompt caching.** Nothing here models cached-input pricing. I reason the
   *sign* survives (appending does not invalidate a prefix, so both sides cache),
   but I did not verify it and no dollar claim follows from these counts.
2. **The per-turn integral.** The proxy's cost is back-loaded within a session, a
   second-order effect favouring the proxy. Not quantified.
3. **Sample size.** One fleet, one client, three queries. `r̄ = 1 704` with a 3.9×
   spread; three points are not a distribution, and the median/p90 design in §5
   is exactly why.
4. **Real `describe_tool` / `read_cache` frequency is unknown.** I generated
   none. If agents call `describe_tool` often, the ledger is worse than measured
   here; if never, slightly better.
5. **Neither renderer is what a model provider actually bills.** `B` and `M` are
   both proxies for the client's own serialization. Unresolved, and it caps how
   precise any of this can honestly claim to be.
6. **A +1 discrepancy I did not chase**: startup logs report `built direct mode
   tools tool_count: 45` then `refreshed direct mode tools tool_count: 46`
   (`internal/server/mcp_routing.go:126,1097`). Whether the direct surface
   carries one tool the catalog does not is unresolved.
7. **Whether the telemetry activation counter should change with the definition.**
   Leaving it produces two contradicting numbers; changing it breaks the
   activation time series. A product decision, not a technical one.

---

## 8. Migration and communication risk

### What breaks

- **The dashboard chip on a small fleet goes from roughly +50…79 % to a negative
  number.** For the reference fleet the honest reading is "−5 457 tokens per
  session". Every existing user below the crossover sees the product tell them it
  costs more than it saves.
- **`README.md:43` claims "~99 % token reduction"** and `docs/social.html:50`
  shows "−99 % tokens". Those are reachable only at very large fleets *and* only
  if `M` is ignored. They cannot survive this change unqualified.
  `bench/README.md:60-63` already says savings approach an asymptote with tool
  count and that a percentage must be quoted with its corpus size — the repo's
  own benchmark documentation already contradicts the marketing.
- `docs/qa/ux-audit-webui-2026-08.md:75,389-396` records a user-visible symptom
  of this ("hero metric … did not move after 16 further tool calls"); the fix
  recorded at `:573-577` accepted the structural framing rather than questioning
  it.

### Sequencing

- **Step 0 — now.** Tier 0. Sorted enumeration and the two false BM25 labels are
  bugs; they need no narrative and no coordination.
- **Step 1 — additive.** Show the ledger rows (`B`, `M`, measured discovery cost)
  *beside* the existing percentage, unchanged. Users see the components one
  release before the headline moves. This is also the cheapest way to find out
  what real fleets look like before committing to a message.
- **Step 2 — swap the headline** to the measured net. Keep the old figure in the
  details panel for one release, relabelled honestly — "catalog reduction
  (excludes mcpproxy's own tools)" — so the drop is explicable rather than
  mysterious.
- **Step 3 — remove the old figure**, and update `README.md`, `docs/social.html`
  and the blog in the *same* release, stating the crossover explicitly.

### The communication problem, and the honest answer

The temptation is to frame this as "we were wrong by 100 points". That is both
worse than necessary and less true than the alternative.

The accurate story is: **savings scale with fleet size, and we now show you where
you sit on that curve.** The product was measuring the right physics on the wrong
axis. At 45 tools mcpproxy does not save context — and it was never mainly a
context-saver at that size. What it gives a small-fleet user is quarantine and
TPA detection, and freedom from the 40-tool client cap that `README.md:41`
already leads with. Those are real, independent of tokens, and they are the thing
to lead with in Regime B.

The risk of *not* doing this is larger than the risk of doing it: a benchmark
already exists in-repo that contradicts the shipped number, the QA audit already
flagged the metric as unexplainable, and the figure is currently random within a
26-point band. Someone will find this. It is much better as a changelog entry
than as an issue.
