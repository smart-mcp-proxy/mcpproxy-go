# DRAFT — "We projected 70% and measured 29.7%" (write-up for mcpproxy.app/blog)

> **Status: DRAFT, not published.** The blog lives in a separate repository
> (`mcpproxy.app-website`). This file is spec 103 T060: the text, the figures and
> the provenance of every figure, staged for review here so it can be checked
> against the harness that produced it before it becomes a public claim.
>
> **Publish as an extension of the existing 2026-03-19 post, "BM25 vs Embeddings
> vs Lua"** — not as a second post. That post argued the retrieval design; this
> is what the design measured. Duplicating its setup would leave two posts with
> two half-answers.
>
> **Blocking before publication** (FR-031, FR-032, SC-004, SC-012):
> 1. US2 has not run — every tokens-per-*completed*-task figure below is marked
>    *not yet measured*, and the post must not imply otherwise.
> 2. The tokenizer↔model divergence tolerance (FR-022) is not established; the
>    repo's ~60% and the provider's ~15–20% guidance differ threefold and
>    neither is sourced.
> 3. The negative live result in §4 is a single observation on one fleet. It
>    stays in the post — it is the most useful thing in it — but it must be
>    labelled as one run, not as a curve.
> 4. §6's reproduction line for the payload decomposition points at the
>    harness's tests, because the decomposition has no CLI flag today. Re-check
>    it against the shipped flags at publication time; a reproduction procedure
>    that does not run is worse than none.

---

## 1. We projected ~70% and measured 29.7%

Spec 102 deferred tool input schemas off the direct surface: the agent gets
names, descriptions and a pointer, and fetches full schemas only for the tools
it actually intends to call. The projection was **~70%** savings on the tool
surface.

The measurement was **29.7%** on the 45-tool reference corpus and **34.8%** on a
527-tool snapshot.

That is the interesting result, and this post is about the gap rather than about
the win. Two questions follow from it, and we can answer one of them properly
today:

* **Where does the payload actually go**, if not into schemas? (§2 — answered)
* **Does a smaller menu make an agent finish more work per token?** (§5 — *not
  yet measured*, and no figure in this post claims it)

**Fleet shapes.** 45 tools: the frozen reference corpus, 7 no-auth servers
(filesystem 14, git 12, memory 9, sqlite 6, time 2, fetch 1,
sequential-thinking 1), every tool carrying a full JSON input schema, frozen
2026-07-14. 527 tools: the LiveMCPTool snapshot, 70 servers. A percentage
without its fleet shape is not a result, so every number below carries one.

---

## 2. Where the payload actually goes

We decomposed the rendered tool-definition payload of the 45-tool corpus by
attributing every token to a component. Tokens are **not** additive across
concatenation — BPE merges across component boundaries — so the payload is
tokenized once and each token attributed by byte offset, rather than counting
components separately and hoping the shares sum.

| Component | Share of payload (45-tool corpus) |
|---|---:|
| Input schema | **59.4%** |
| Description | 36.5% |
| Name | 2.4% |
| Structural (separators) | 1.7% |

*Provenance: measured, deterministic tokenizer (`cl100k_base`), 45-tool frozen
corpus, all 45 tools schema-bearing.*

**Read this carefully, because it is easy to over-read.**

* On **this** corpus, schemas dominate — so "descriptions are the real cost" is
  not true here. Deferring schemas has room to work.
* It still does **not** explain the 29.7%, and we are not claiming it does.
  Spec 102 measured the production **wire** payload, which additionally carries
  annotation defaults and wire framing; this decomposition covers the corpus
  rendering (name + description + schema). Adjudicating the wire figure with
  corpus-rendering evidence would be answering a different question in a
  confident voice. The harness says so in the report, next to the verdict.
* The 527-tool snapshot **cannot** answer this question at all: all 527 of its
  tools carry zero schemas. A 0% schema share there means the corpus lacks the
  thing under test — not that schemas are cheap. Any decomposition that reported
  it as a confirmation would be measuring its own dataset gap.

So: the achievable ceiling is recomputed **per fleet**, never carried forward.
Carrying a ceiling forward is exactly the error that produced the 70%
projection.

---

## 3. What deferring schemas saves on a recorded workload

We replayed a real recorded workload — the actual sequence of tool calls a real
agent made — and recomputed what it would have cost under each mode, against the
45-tool fleet.

**`direct_full` → `direct_deferred`: 1296 tokens, 29.5%.**

*Provenance: measured, deterministic tokenizer, 45-tool frozen corpus.
Counterfactual over recorded traffic.*

Three things this figure is not:

1. **It is not observed agent behaviour.** A recording carries the calls, the
   arguments and the responses. It does not carry the prompt, the conversation,
   the model's state, or any oracle for whether the user's goal was met. Replay
   can say what the same call shape would have cost under a different
   serialization. It cannot say what the agent would have *done* — which calls
   it would have attempted, whether the first attempt would have been right,
   whether it would have finished. Those are new decisions by a model that is
   not in the recording. Every replay figure is labelled a counterfactual for
   this reason.
2. **It is not a historical reconstruction.** The workload is scored against the
   fleet you supply, not the fleet as it stood when the session was recorded.
   That is internally valid across modes and is not a claim about the past.
3. **It is not a complete workload cost.** By default replay does not read
   recorded bodies (they are raw, unmasked user traffic), so absolute
   whole-workload cost is reported as *unavailable* — never as zero. The
   `direct_full` vs `direct_deferred` delta survives bodies-off precisely
   because the call responses are identical between those two modes and cancel.

29.5% here and 29.7% in spec 102 are the same order of magnitude from two
independent code paths on the same fleet size. That is reassuring. It is not a
confirmation, because they are measured over different payloads (§2).

---

## 4. Where the savings do not materialise

**On a 45-tool fleet, a live run showed `retrieve_tools` costing MORE than
simply listing every tool.** Negative savings. The proxy lost.

*Provenance: measured live, single run, 45-tool fleet. One observation, not a
curve — k ≥ 4 with spread is not established for it.*

The mechanism is not mysterious, and it generalises:

* A proxy mode has a **fixed floor**. `retrieve_tools`, the `call_tool_*`
  variants, `describe_tool`, `read_cache` and the management tools are in
  context on every turn, whether or not they are used. That floor is a constant
  number of tokens.
* A naive menu has **no floor and a slope**: it costs whatever the fleet costs,
  and nothing more.
* Below some fleet size the floor exceeds the menu. At 45 tools — seven small
  servers — we are below it.

So the honest shape of the claim is: **mcpproxy's savings scale with fleet size,
and on a small fleet it is overhead.** If you run one or two servers with a few
dozen tools between them, load them directly. The proxy earns its floor back
when the menu gets long — the regime the 527-tool snapshot represents — and the
quarantine, isolation and observability features may still be worth it to you at
any size, but that is a different argument and should not be smuggled in as a
token saving.

**The crossover point is not yet measured.** We can tell you the direction and
the mechanism, and we can tell you that 45 tools is on the wrong side of it. We
cannot yet tell you where it is, and we are not going to guess in public.

---

## 5. What we have not measured

Listing this is the point of the post, not an appendix to it.

| Question | Status |
|---|---|
| Tokens per **completed** task, per mode | **Not yet measured** — needs a live agent loop under a pinned model |
| Task completion rate per mode | **Not yet measured** |
| First-attempt success rate per mode | **Not yet measured** |
| Corrective vs infrastructure retry rates | **Not yet measured** |
| Cache-read token split | **Not yet measured** — the public suite's per-task output has no cache-read field; it must come from the provider response |
| The fleet size at which the proxy breaks even | **Not yet measured** |
| Tokenizer-vs-model divergence tolerance | **Not yet established** (see the draft header) |

Everything in §§2–4 is deterministic arithmetic: same inputs, same numbers, no
model involved. Everything in this table needs a model, real spend, and at least
four runs per cell before it is worth printing.

**Why the distinction is load-bearing:** menu size is not work done. A mode that
shows a smaller menu and causes one extra failed call has saved nothing. Until
the table above is filled, every saving on this page is a statement about
context cost, not about productivity — and we would rather say that plainly than
let a reader infer the stronger claim.

We also hold ourselves to four rules, which are worth stating because they
constrain what we are allowed to publish next:

* **Never sum accounting sources.** Deterministic figures come from the tiktoken
  tokenizer; live figures come from provider-reported usage. A cross-source
  aggregate is withheld with a stated reason, never computed.
* **Never quote a percentage without its fleet shape.**
* **Never report a mode that costs less while completing less as a saving.** It
  is a regression, whatever the token figure says.
* **Never let a truncated record contribute silently.** Truncation understates
  cost *in our favour*, which is the failure this whole exercise exists to
  prevent; excluded records are counted and reported.

---

## 6. Reproducing this

The deterministic figures reproduce exactly. Nothing here needs an API key or
model spend.

```bash
git clone https://github.com/smart-mcp-proxy/mcpproxy-go && cd mcpproxy-go

# The tokenizer downloads its vocabulary once. Setting the cache variable only
# NAMES a cache; it does not fill one. Warm it with network access once:
export TIKTOKEN_CACHE_DIR="$HOME/.cache/tiktoken"
go run ./bench/cmd/bench \
  -corpus-v2 specs/083-discovery-profiler/datasets/corpus_v2.tools.json \
  -out /tmp/warm

# The frozen-corpus mode comparison across every encoding arm:
go run ./bench/cmd/bench \
  -corpus-v2 specs/083-discovery-profiler/datasets/corpus_v2.tools.json \
  -arms all -out bench/results

# §2, the payload decomposition. NOTE FOR REVIEW: the decomposition has no CLI
# flag yet — today it is exercised from the harness's own tests. Replace this
# with the shipped invocation before publishing, or the procedure is wrong.
go test ./bench/ -run TestPayloadDecomposition -count=1
```

For §3 you supply your own recording — we cannot ship ours, and that is a
limitation, not modesty (see §7):

```bash
# On a machine that has been using mcpproxy for real work:
mcpproxy activity export --format json > ~/replay-corpus.jsonl

# A fleet input is MANDATORY: a recording alone has no menu to cost, so a
# recording-only invocation is a hard error, not a degraded run.
CORPUS=specs/083-discovery-profiler/datasets/corpus_v2.tools.json
go run ./bench/cmd/bench -replay ~/replay-corpus.jsonl -corpus-v2 $CORPUS -out /tmp/run-a
go run ./bench/cmd/bench -replay ~/replay-corpus.jsonl -corpus-v2 $CORPUS -out /tmp/run-b

# Two runs must be byte-identical once the wall-clock stamp is removed:
diff <(jq 'del(.generated_at)' /tmp/run-a/report.json) \
     <(jq 'del(.generated_at)' /tmp/run-b/report.json)

rm -f ~/replay-corpus.jsonl   # it is raw user traffic; delete it when done
```

Reports are never committed — only the code, the fixtures and the methodology
are versioned, so the numbers you get are the numbers your machine computed.

---

## 7. Limitations, in one place

* **The replayed workload is private.** It is real user traffic, so it cannot be
  published, which means §3's figure is **not independently reproducible** on
  our inputs — only on yours. It is deliberately not the sole support for any
  claim here.
* **Frozen corpora are not your fleet.** 45 tools over 7 small servers and a
  527-tool snapshot are two points. Your tool descriptions are probably longer
  than ours, which moves §2's split.
* **The 527-tool snapshot carries no schemas**, so it bounds nothing about
  schema deferral.
* **§4 is one live run**, on one fleet, with no spread.
* **Nothing here measures success.** See §5.
* **The tokenizer is not the model's tokenizer.** Deterministic figures are
  computed with `cl100k_base`; a pinned model will count differently, and the
  size of that divergence is not yet established.

---

## 8. So which mode should you run?

| Your fleet | What the measurements support |
|---|---|
| A handful of servers, tens of tools | **Load them directly.** At 45 tools the proxy's discovery surface cost more than the full menu (§4). Use mcpproxy for quarantine/isolation/observability if you want those, not for token savings. |
| Hundreds of tools, many servers | The regime the design targets. Deferring schemas removed 29.5% of the tool-surface cost on our recorded workload (§3) and schemas are 59.4% of the payload on the schema-bearing corpus (§2) — but we have not measured the crossover, so measure your own fleet with the commands in §6. |
| Anything, and you care about completed work | **We do not yet have a number for you.** §5. |

---

### Provenance table (every figure on this page)

| Figure | Value | Source | Fleet shape | Provenance |
|---|---|---|---|---|
| Spec 102 projection | ~70% | spec 102 | — | projected, superseded |
| Spec 102 measurement | 29.7% | spec 102 | 45 tools (wire payload) | measured |
| Spec 102 measurement | 34.8% | spec 102 | 527 tools | measured |
| Schema share | 59.4% | US4 decomposition | 45 tools (corpus rendering) | measured, tokenizer |
| Description share | 36.5% | US4 decomposition | 45 tools | measured, tokenizer |
| Name share | 2.4% | US4 decomposition | 45 tools | measured, tokenizer |
| Structural share | 1.7% | US4 decomposition | 45 tools | measured, tokenizer |
| `direct_full` → `direct_deferred` | 1296 tokens / 29.5% | US1 replay | 45 tools | measured, tokenizer, **counterfactual** |
| `retrieve_tools` vs naive menu | negative savings | live run | 45 tools | measured, single run |
| Everything in §5 | — | — | — | **not yet measured** |
