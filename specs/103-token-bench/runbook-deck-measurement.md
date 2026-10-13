# Runbook: measure the "Twenty-Eight Round Trips" workload with token-bench

Produces exact `cl100k_base` token counts AND a USD figure for the deck's
slides 4, 7 and 9, replacing the deck's own `bytes ÷ 4` estimate.

Run this **on the laptop the deck was measured on** — it needs the same fleet
(gcore-mcp-server, jira-gcore, gitlab-gcore, github, slack-gcore) and the stored
`jira-triage` script.

---

## 0. Prerequisites

- The mcpproxy core running with that fleet.
- This branch checked out (`spec103-codeexec-response-saving`) and Go 1.25.
- Nothing else required — no API key, no model spend. Everything is computed
  from the local activity log.

## 1. Confirm the activity log will capture what we need

```bash
mcpproxy activity list --limit 5
```

Records must be present. If the log is empty, the run below still works — it
just has to be a fresh run rather than a historical one.

## 2. Run the workload once, through mcpproxy

Run `/jira-triage` exactly as in the deck — same board, same 3 tickets and
7 PRs. One run is enough. The measurement reads what the proxy recorded, so
**nothing about how you invoke it needs to change.**

If you want the inline-script comparison for slide 7 as well, run it a second
time with the script pasted inline instead of called by name. That gives both
the "stored" and "first-run" numbers, which is the distinction slide 9 currently
blurs.

## 3. Export the activity log

```bash
mcpproxy activity export --include-bodies --output ~/deck-run.jsonl
```

`--include-bodies` is required: without it, costs fall back to byte-length
estimates and cannot be priced in tokens at all.

**This file contains raw, unmasked Jira and GitHub content.** The export path
does not mask payloads. Keep it local and delete it when you are done (step 6).
The bench loader refuses any input path inside the repository checkout, so put
it in your home directory as above.

## 4. Measure

```bash
go run ./bench/cmd/codeexecsaving -in ~/deck-run.jsonl -bodies-unmasked
```

Reports, per `code_execution` call and in total:

- `baseline` — what an agent making those sub-calls itself would have paid
- `proxy` — the script sent plus the single result returned
- `saving` in tokens, and **net USD** at `$3/$15` per Mtok with cached input

Flags: `-in-price`, `-out-price`, `-cache-mult` (use `-cache-mult 1.0` for the
uncached case; the default `0.1` assumes a cached prompt prefix).

Then the input/output split, which is what makes the USD figure move:

```bash
go run ./bench/cmd/iosplit -in ~/deck-run.jsonl
```

## 5. What to expect, and the one number to watch

At ~34 sub-calls the workload sits far right of every crossover measured so far,
so code execution should win decisively on both tokens and money.

The number worth reading carefully is **output**. Tool-call arguments are tokens
the model GENERATED (billed ~5× input); responses are tokens it reads. Code
execution replaces N argument-writings with ONE script, at a fixed cost however
few calls it replaces. On small workloads that inverts the sign — measured
break-even is ~2 sub-calls in raw tokens but ~6 in money once input is cached.

Stored scripts are handled correctly: mcpproxy records the RESOLVED source for
the audit trail, but bench charges only what the model actually sent (the name
plus input), so lever 3 is not penalised for a script the model never wrote.

## 6. Clean up

```bash
rm ~/deck-run.jsonl
```

---

## Sending the results back

Paste the output of both commands. That is enough to rewrite slides 4, 7 and 9
with measured figures; no raw content needs to leave the machine, because both
commands emit counts only.
