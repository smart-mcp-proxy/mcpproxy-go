# Quickstart: token-bench (spec 103)

Two independent halves. **Start with the deterministic one** — it needs no model spend and no
credentials, and it delivers value alone. It runs offline *once the tokenizer vocabulary is
cached*; see "Offline note" below, which is a real prerequisite rather than a footnote.

---

## Half 1 — Deterministic replay (US1)

### 1. Export a real workload

From a machine that has been using mcpproxy for real work:

```bash
mcpproxy activity export --format json > ~/replay-corpus.jsonl
```

**Bodies stay off.** The headline — per-mode tool-surface cost — is computed from the fleet's
tool definitions and the call sequence, not from recorded content, so it is fully measured
with bodies off. The export path does not mask, so a bodies-on export is raw and unmasked by
design; take one only if you specifically need measured *response* cost, and delete it after.

> The file is real user traffic. Keep it outside the repository, never commit it, and delete
> it when finished. It is deliberately gitignored nowhere, because it should never be inside
> the repo in the first place.

### 2. Recompute cost across the matrix

```bash
go run ./bench/cmd/bench -replay ~/replay-corpus.jsonl -out bench/results
```

Produces the `replay` block in `bench/results/report.json` plus the dashboard section: per
mode cell, what this workload would have cost, with the exclusion accounting beside it.

### 3. Know which figures you just got

- **Tool-surface cost per mode: `measured`.** No recorded content was needed.
- **Response cost: `estimated` or absent.** Tokenizing needs the text; a byte length only
  supports an estimate. Re-run with bodies on if you need it measured.
- **Discovery-response cost: unavailable.** Internal `retrieve_tools` activity emission
  carries no byte counts, so it is not recoverable from the activity log at all.

### 4. Read the exclusion report before the headline

The exclusion counts tell you whether the headline is trustworthy. A corpus that is 80%
truncated produces a real number over a small slice, and the report says so rather than
pretending otherwise.

### 5. Verify determinism

```bash
go run ./bench/cmd/bench -replay ~/replay-corpus.jsonl -out /tmp/run-a
go run ./bench/cmd/bench -replay ~/replay-corpus.jsonl -out /tmp/run-b
diff /tmp/run-a/report.json /tmp/run-b/report.json && echo "byte-identical (SC-002)"
```

### Offline note

The tokenizer downloads its vocabulary on first use unless a cache directory is set:

```bash
export TIKTOKEN_CACHE_DIR="$HOME/.cache/tiktoken"
```

Setting the variable only names a cache; it does not fill one. A first run still fetches the
vocabulary, so a genuinely offline reproduction needs the cache pre-populated (warm it once
with network access, or vendor/restore a known-good cache in CI). Without that, a reproducer
on a restricted network fails at step one — which is exactly what SC-004 requires to work.

---

## Half 2 — Live agent loop (US2)

**Costs model spend.** Do not start until a model and a ceiling are pinned.

### 1. Stand up the full fleet on one proxy

Configure mcpproxy with **all** suite services at once — filesystem, postgres, and the
credentialed ones if used — even while running a single service's tasks. A single server's
toolset is too small a fleet for proxy modes to differ from baseline, and the full fleet is
also the honest baseline: the same agent, the same tasks, all tools loaded directly.

Enable code execution if the `code_exec` cell is in scope; without it that cell is degenerate
and is skipped with a reason.

Leave the instance running. **The whole matrix crosses on one instance** — cells are selected
by endpoint URL, and only the two serialization axes need config.

### 2. Point the suite at mcpproxy

MCPMark is pinned by commit SHA. Patch its single MCP factory with an env-gated branch that
returns an HTTP MCP server at mcpproxy's URL with the API key header. That is the only change
the suite needs — `list_tools()` and `call_tool()` are the only calls its agent loop makes
against the server object.

### 3. Start with the credential-free core

Filesystem (30 tasks) and postgres (21) need no third-party credentials and are fully local.
That is the clean starting point. GitHub and Notion add real-fleet realism but **perform real
writes** and must be run so they cannot damage real data.

### 4. Run each cell at k >= 4

A single agentic run is noise. Every model-dependent figure is an average over at least four
runs with its spread, or it is not a headline.

### 5. Ingest the results

The suite's per-task metadata carries the pass verdict, token usage split, and turn count; its
trajectory files are what make corrective-versus-infrastructure retry classification possible.
These feed tokens-per-completed-task, completion rate, first-attempt success and retry rates.

---

## Reading the report honestly

- **Check the accounting source on every figure.** Deterministic figures come from the local
  tokenizer; live figures come from provider-reported usage. They are never summed — a
  cross-source aggregate is withheld with a stated reason instead.
- **Check provenance per row**, not just the section badge. A section can legitimately hold a
  measured figure beside an estimated one.
- **A withheld figure is a result**, not a bug. It means the honest number could not be
  computed from what was measured.
- **Completion rate sits beside cost.** A mode that costs less and completes less is not a
  saving, and the report marks it as a regression regardless of its token figure.
- **Every percentage carries its fleet shape.** A percentage without one is not publishable.

## Before publishing

1. Recompute the achievable ceiling **for each fleet shape** — never carry a previous
   ceiling forward. That is the error spec 102 made.
2. Measure the tokenizer's divergence from the pinned model using the token-counting endpoint
   (no inference spend) rather than quoting either of the two contradictory figures currently
   in circulation.
3. Confirm no report is tracked:
   ```bash
   test -z "$(git ls-files bench/results)" && echo "clean (SC-011)"
   ```
   Use `git ls-files`, not `git status --porcelain` — porcelain does not list ignored files and
   would stay silent about an already-tracked report.
