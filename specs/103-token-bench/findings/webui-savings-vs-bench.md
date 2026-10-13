# Finding: the Web UI's savings figure and the bench headline disagree in SIGN

**Task**: T061 — compare the Web-UI/status savings figure against the bench headline over one live fleet.
**Date**: 2026-09-01
**Status**: reproduced, mechanism established, not yet fixed.

Spec 103's own Assumptions say a contradiction between two measurements over the same
data is *a finding to investigate*. This is that contradiction, and it is not a rounding
difference.

## CORRECTION (2026-09-01): both figures were wrong, and one of them was mine

A follow-up investigation (`honest-savings-design.md`) established two things
that this document got wrong when first written. The DIRECTION of the finding
survives; the framing and the magnitudes do not.

**1. The Web-UI figure is non-deterministic, not merely optimistic.** Twenty-five
consecutive calls to `/api/v1/stats/tokens` over an unchanging fleet returned six
distinct values spanning **52.35%–78.80%**. `Manager.GetAllServerNames`
(`internal/upstream/manager.go:945-954`) ranges over a Go map, and
`estimateQueryResultSize` takes a PREFIX of that ordering
(`internal/server/tokens/savings.go:139`) — so the "typical query" is re-drawn on
every HTTP request. The +68.4% quoted below was one sample from that spread.
This also contradicts the shipped tooltip, which tells users the figure changes
when servers change and "not with each call".

**2. The bench figure quoted below is ALSO wrong.** `ProxyModeToolDefs`
hardcodes `EnableCodeExecution: true` (`internal/server/bench_export.go:47`)
while the shipped default serves a disabled stub, so bench's 5783-token proxy
menu overstates what a default deployment actually carries. The live menu is
**4712**. Treating bench as the correct side of the comparison, as this document
originally did, was wrong: both instruments were miscounting, in opposite
directions.

**What survives.** With the corrected menu, the reference fleet still costs more
than it saves before a single call: 4368 − 4712 = **−344 tokens**. A measured
3-call session cost 9825 against a 4368 baseline — **−5457 tokens** — while the
dashboard simultaneously read somewhere between +52% and +79%. The sign of the
original finding holds; its magnitude and its attribution of correctness do not.

## What was measured

One isolated mcpproxy instance, the committed 7-server reference config, 45 upstream
tools, both figures read from the same running process:

| Source | Figure |
|---|---|
| `GET /api/v1/stats/tokens` (Web UI / status) | **+68.4% saved** (4684 → 1478, saved 3206) |
| `bench -live` headline, `retrieve_tools` cell | **−32.5%** (baseline 4364, proxy menu 5783) |

Same fleet. Opposite sign. One says the proxy saves two thirds of the tool-surface cost;
the other says it costs a third more than not using it.

## Why they disagree

`SavingsCalculator.CalculateProxySavings` (`internal/server/tokens/savings.go`) computes:

```
SavedTokens = totalTokens - avgQuerySize
```

- `totalTokens` — every upstream tool definition (4684 here)
- `avgQuerySize` — one simulated `retrieve_tools` result of `topK` tools (1478 here)

**The proxy's own menu is never subtracted.** An agent talking to mcpproxy carries
`retrieve_tools`, the three `call_tool_*` variants, `read_cache`, `code_execution` and the
management tools in its context for the whole session — 5783 tokens on this fleet, which is
*larger than the entire upstream baseline it replaces*. That term does not appear in the
formula at all.

The bench headline includes it, which is why the two disagree, and why they disagree by
roughly the size of the proxy menu.

## The second problem, which is worse

```go
metrics.SavedTokens = totalTokens - avgQuerySize
if metrics.SavedTokens < 0 {
    metrics.SavedTokens = 0
}
```

Negative savings are **clamped to zero**. So the figure is structurally incapable of
reporting the state bench just measured — that at this fleet size the proxy costs more than
it saves. The clamp guarantees the number is never bad news, which is precisely the property
a savings metric must not have.

## Why this is not merely a benchmark disagreement

The bench figure is internal. The Web-UI figure is **shown to users**. It is optimistic by
construction in two independent ways: it omits the dominant cost term at small fleet sizes,
and it cannot express the case where the product does not pay off.

Neither is a rounding problem, and neither is visible to a reader of the number.

## What is NOT claimed here

- **This does not mean mcpproxy does not save tokens.** Savings scale with fleet size: the
  proxy menu is a fixed cost while the upstream catalog grows linearly, so the sign flips
  once the fleet is large enough. 45 tools is simply below that crossover. Bench's own
  break-even analysis exists to locate it.
- **It does not mean the Web-UI number is fabricated.** It answers a coherent question —
  "how much smaller is one query result than the whole catalog?" — but that is not the
  question its label implies, and it is not what a user pays.
- The `topK` sample is also an arbitrary prefix of the tool list rather than a BM25 ranking
  (documented in `estimateQueryResultSize`), so `avgQuerySize` is a stand-in, not a measured
  retrieval result. That is a third, smaller inaccuracy in the same figure.

## Suggested direction

1. Subtract the proxy menu, so the figure answers what the user pays. `ProxyModeToolDefs`
   already supplies the per-mode menu with real schemas, and bench proves the two agree
   to the token.
2. Remove the clamp and let the figure go negative, with the UI presenting a small fleet
   honestly ("your fleet is below the break-even point") rather than showing zero.
3. Quote the fleet size alongside the percentage, as spec 103 requires of every published
   percentage — the number is meaningless without it.

## Reproduction

```bash
# isolated instance, scratch data-dir and config, high port
mcpproxy serve --config <scratch>/mcp_config.json --data-dir <scratch> --listen 127.0.0.1:18733

curl -s -H 'X-API-Key: <key>' http://127.0.0.1:18733/api/v1/stats/tokens

go run ./bench/cmd/bench -live -proxy http://127.0.0.1:18733 -api-key <key> \
  -corpus-v2 specs/083-discovery-profiler/datasets/corpus_v2.tools.json -out <scratch>/bench
```
