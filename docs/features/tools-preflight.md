---
id: tools-preflight
title: Required-Tools Preflight
sidebar_label: Tools Preflight
sidebar_position: 13
description: Deterministic, side-effect-free availability check for required tools before an automation spends model tokens
keywords: [preflight, automation, cron, ci, availability, exit codes, reliability]
---

# Required-Tools Preflight

Check that a list of required tools is ready — and if not, learn exactly why — **before** an agent session spends a single model token. One CLI command or one REST call answers, per tool, with a machine-readable reason code, a retryability flag, and a remediation hint.

```bash
mcpproxy tools preflight gh-ops:sync_issues slack:post_message
echo $?   # 0 = go, 10 = retry later, 11 = operator action needed, 12 = unknown tool id
```

## Why

Recurring headless automations (cron jobs, CI pipelines, n8n flows) depend on a small, stable set of tools. When one of those tools silently disappears — the server got quarantined, a tool definition changed and tripped the rug-pull guard, an OAuth token expired — the failure surfaces as a **silent discovery miss**: the agent searches, finds nothing, and improvises or fails ambiguously. Diagnosing that ambiguity in the wild has cost users days to weeks ([issue #969](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/969)).

Preflight replaces the silent miss with a deterministic gate: the wrapper fails fast, names the root cause, and can branch on the exit code alone — retry later, page the operator, or fail the pipeline.

## Concept: a stat, not a ping

A preflight is **observational only**. It reads local proxy state — the tool index, tool-approval records and hashes, the connection-state snapshot, and configuration policy — and performs:

- **Zero upstream calls.** No server is contacted, ever — even a dead one.
- **Zero mutation.** No connects, reconnects, re-indexing, or config/approval changes are triggered.

That makes it deterministic and cheap enough to run before every scheduled job. It also defines the honest limit: preflight is a *point-in-time eligibility check*, not a call-success guarantee. A network race or an upstream crash a second later can still fail the real call — preflight tells you the proxy would not have refused it.

## Surfaces

### CLI

```bash
mcpproxy tools preflight <server:tool>... \
  [--profile <name>] \
  [--pin <server:tool>=sha256/v<N>:<hex>]... \
  [--read-only-only] [--exclude-destructive] [--exclude-open-world] \
  [--wait <duration>] \
  [-o json|yaml|table]
```

```text
$ mcpproxy tools preflight gh-ops:sync_issues gh-ops:nope
ID                  STATUS       REASON     RETRYABLE  ACTION     DETAIL
gh-ops:sync_issues  ready        -          -          -          -
gh-ops:nope         unavailable  not_found  false      configure  No tool with this id is available.
VERDICT: unknown_ids (exit 12)
```

(`-o json` additionally carries a `did_you_mean` array with up to 3 nearest-name suggestions on `not_found`.)

### REST

```bash
curl -s -X POST -H "X-API-Key: $API_KEY" \
  http://127.0.0.1:8080/api/v1/preflight \
  -d '{
    "tools": [
      {"id": "gh-ops:sync_issues"},
      {"id": "slack:post_message", "pin_hash": "sha256/v1:9f86d081884c7d65..."}
    ],
    "wait_ms": 5000
  }'
```

```json
{
  "success": true,
  "data": {
    "verdict": "ready",
    "checked_at": "2026-08-15T03:00:00Z",
    "waited_ms": 0,
    "tools": [
      { "id": "gh-ops:sync_issues", "status": "ready" },
      { "id": "slack:post_message", "status": "ready" }
    ]
  }
}
```

**HTTP status reports whether the check executed, never what it found.** A fully blocked toolset is still a 200 — the verdict lives in the body. Only a malformed request (400: empty list, more than 100 entries, conflicting duplicate pins, unknown profile, `wait_ms` out of range, or a body that is oversized, doubled, or carries an unknown field) or a proxy that cannot answer honestly (503: runtime unavailable, local state unreadable, or the audit record could not be persisted) is non-200.

A failing tool carries the full diagnosis:

```json
{
  "id": "gh-ops:sync_issues",
  "status": "unavailable",
  "reason": "tool_changed",
  "retryable": false,
  "action": "approve",
  "detail": "Tool definition changed after approval",
  "remediation": "The tool definition changed after approval; review the diff and re-approve it (Web UI, Server detail -> Tools)."
}
```

Full request/response schema: [REST API](../api/rest-api.md). CLI flag reference: [Management Commands](../cli/management-commands.md).

### In band: `describe_tool` check mode

The CLI and REST surfaces need a harness *outside* the session. An agent already inside an MCP session gates itself with the same evaluator by adding `check: true` to the [`describe_tool`](./search-discovery.md) call it already knows:

```json
{
  "name": "describe_tool",
  "arguments": {
    "tool_ids": ["gh-ops:sync_issues", "slack:post_message", "gh-ops:nope"],
    "check": true
  }
}
```

```json
{
  "verdict": "blocked",
  "checked_at": "2026-08-16T09:14:02.117Z",
  "request_id": "1755331442117-describe_tool-42",
  "results": [
    { "id": "gh-ops:sync_issues", "status": "ready" },
    { "id": "slack:post_message", "status": "unavailable", "reason": "server_quarantined",
      "retryable": false, "action": "approve",
      "detail": "Server \"slack\" is quarantined; its tools are withheld pending review.",
      "remediation": "Review the quarantined server and approve it if it is trusted (Web UI, Quarantine)." },
    { "id": "gh-ops:nope", "status": "unavailable", "reason": "not_found", "retryable": false,
      "action": "configure", "detail": "No tool with this id is available.",
      "remediation": "Check the tool id (format <server>:<tool>) against mcpproxy tools list.",
      "did_you_mean": ["gh-ops:sync_issues"] }
  ]
}
```

**Check vs. describe.** They answer different questions and it is worth keeping them apart: describe (no `check`) returns *what a tool looks like* — the full JSON Schema, for building arguments after a lossy compact signature. Check returns *whether you may call it*, and no schema at all. Branch on the presence of `verdict`.

**The agent loop it is meant for**: one call before the plan, not one per step.

```text
plan  → describe_tool{tool_ids: [the 8 ids the plan calls], check: true}
        verdict "ready"              → execute the plan
        verdict "degraded_retryable" → wait and re-check (nothing to escalate yet)
        verdict "blocked"            → tell the user exactly what to approve/enable, in THIS turn
        verdict "unknown_ids"        → re-run retrieve_tools; an id is wrong or the tool moved
```

Differences from the out-of-band surfaces, all deliberate:

| | In band (`check: true`) | REST / CLI |
|---|---|---|
| Batch cap | **50** ids (verdict-only results are ~30–60 tokens each; the cap exists so the response cannot become a discovery bypass) | 100 |
| Annotation filters | `filters: {read_only_only, exclude_destructive, exclude_open_world}` — named `filters` because that is the word `retrieve_tools` already teaches agents; the REST body calls the same object `policy` | `policy` |
| Hash pins | not accepted. `expect_hashes` is a **reserved** field name: sending it is an error, never a silent no-op | `pin_hash` / `--pin` |
| Disclosure tier | always the [agent-token tier](#disclosure-tiers), whatever the session's credentials | operator tier for API key / socket / named pipe |
| Scope | the session's own (agent-token `allowed_servers` ∩ profile pin ∩ active profile). There is no `profile` parameter — an agent cannot re-point its own scope by asking | `profile` in the request |
| Waiting | none. A `degraded_retryable` verdict is the agent's cue to retry on its own schedule rather than hold an MCP call open | `wait_ms` / `--wait` |
| Failures | request errors and "cannot evaluate" both come back as MCP tool errors that say **no verdict was computed** — never as a verdict | 400 / 503 |

Duplicate ids are deduplicated (one result per unique id, in first-occurrence order); ids are trimmed first, and each result echoes the normalized id. One malformed id is a per-id `not_found`, never a batch failure.

**Where check mode is not available**: `describe_tool` is registered on the default `/mcp` server and the `retrieve_tools` routing mode (`/mcp/call`, `/mcp/p/<slug>`) only. In `code_execution` and direct [routing modes](./routing-modes.md) there is no `describe_tool`, and therefore no in-band check — the interim path there is `POST /api/v1/preflight` from the harness that schedules the session. Registering the built-in on those surfaces is a later phase.

## The reason taxonomy

A per-tool result is `ready` or `unavailable` with **exactly one** reason from a closed 15-code enum. The enum only ever grows (treat unknown codes as non-retryable). `server_saturated` is reserved for a future revision.

| Reason | Class | `retryable` | Default `action` | Set verdict | CLI exit |
|---|---|---|---|---|---|
| `server_initializing` | retryable | true | — | degraded_retryable | 10 |
| `server_unhealthy` | retryable | true | `restart`/`login`/`view_logs` (from diagnostics) | degraded_retryable | 10 |
| `server_disabled` | fix-state-first | false | `enable` | blocked | 11 |
| `server_quarantined` | fix-state-first | false | `approve` | blocked | 11 |
| `tool_pending_approval` | fix-state-first | false | `approve` | blocked | 11 |
| `tool_changed` | fix-state-first | false | `approve` | blocked | 11 |
| `tool_blocked_by_user` | fix-state-first | false | `enable` | blocked | 11 |
| `oauth_required` | fix-state-first | false | `login` | blocked | 11 |
| `hash_mismatch` | fix-state-first | false | `configure` | blocked | 11 |
| `server_not_in_scope` (operator tier only) | permanent-config | false | `configure` | blocked | 11 |
| `tool_denied_by_config` | permanent-config | false | `configure` | blocked | 11 |
| `missing_annotation` | permanent-config | false | `configure` | blocked | 11 |
| `policy_filtered` | permanent-config | false | — | blocked | 11 |
| `not_found` | permanent-config | false | `configure` | unknown_ids | 12 |
| `server_not_configured` | permanent-config | false | `configure` | unknown_ids | 12 |

The three classes tell a wrapper what to do without reading anything else:

- **Retryable** — time will fix it (a server is starting up or briefly unhealthy). Back off and retry.
- **Fix-state-first** — an operator has to act: approve, enable, log in, or re-pin. Retrying without the action is futile.
- **Permanent-config** — the request and the configuration disagree: a typo'd id, a removed server, a policy that excludes the tool. Fix the automation or the config.

### Set verdict and exit codes

The set-level verdict (and the CLI exit code) is the **worst class present**: `unknown_ids` (12) > `blocked` (11) > `degraded_retryable` (10) > `ready` (0). Transport and usage errors use the general CLI exit code 1. A cron wrapper can branch retry-vs-page-vs-fix on the exit code alone — no JSON parsing.

### Precedence

When multiple states co-occur for one id, exactly one reason is reported — the first match in this fixed order:

```
server_not_configured → server_not_in_scope → server_quarantined → server_disabled
→ not_found → tool_denied_by_config → tool_blocked_by_user → tool_changed
→ tool_pending_approval → hash_mismatch → oauth_required → server_unhealthy
→ server_initializing → annotation filters → ready
```

Notable consequences:

- A nonexistent tool on a **quarantined server** reports `server_quarantined`, not `not_found` — quarantined servers' tools are never indexed, so existence is unknowable there.
- On a server that is not yet Ready, the connection-state verdict (`server_initializing` / `server_unhealthy`) is returned instead of `not_found` — the proxy will not claim per-tool knowledge it doesn't have.
- `hash_mismatch` fires only once the tool is known to exist with a current stored hash; every earlier state wins over it.
- Annotation filters are evaluated last, in the fixed order `read_only_only` → `exclude_destructive` → `exclude_open_world`. Within the first filter that excludes, the verdict is `missing_annotation` when the hint is absent and `policy_filtered` when the hint is explicitly unsafe.

## Hash pinning

Pin a tool to the schema you validated your automation against. If the tool's definition drifts — even while everything else is green — the preflight reports `hash_mismatch` instead of letting the job run against a schema it has never seen:

```bash
mcpproxy tools preflight gh-ops:sync_issues \
  --pin gh-ops:sync_issues=sha256/v1:9f86d081884c7d65...
```

The pin format `sha256/v<N>:<hex>` embeds the proxy's hash schema version, so a proxy-side hash-algorithm bump is distinguishable from genuine upstream drift. Current pins are published on the operator-tier tool listings (`mcpproxy tools list -o json` and the per-tool REST payload) — copy them from there.

## Waiting out transient states

`wait_ms` (REST) / `--wait` (CLI, cap 10 s) polls local state while — and only while — **every** failure is retryable-class:

- The moment any non-retryable failure appears, the wait terminates early (waiting cannot help a `server_quarantined`).
- The request always resolves at the deadline with current reasons — it never hangs.
- Polling has a 250 ms floor, and waiting slots are a small fixed budget; when the budget is exhausted the request degrades gracefully to an immediate answer with `waited_ms: 0`.

This turns "the proxy restarted 3 seconds before the cron tick" from a failed night into a 4-second delay.

## Disclosure tiers

Preflight answers with different candor depending on who is asking:

| | Operator tier (API key, Unix socket, named pipe) | Agent-token tier (agent tokens, OAuth users, **and the whole in-band surface**) |
|---|---|---|
| Out-of-scope server | `server_not_in_scope` + detail explaining that a session under this profile sees `not_found` | plain `not_found` — byte-indistinguishable from a genuinely unknown id |
| Unconfigured server | `server_not_configured` | the same plain `not_found`, so a probe cannot enumerate what exists behind a scope |
| Tool hashes | published on ready results | never |
| `did_you_mean` | nearest visible ids | only within the token's own scope |

**The in-band surface is always the agent-token tier**, whatever credentials the MCP session presents. `/mcp` is unauthenticated by default and its middleware hands such requests a full admin context for client compatibility, so an auth context in band proves nothing about who is calling — a tier derived from it would be a tier the caller chooses. Operators wanting the full diagnosis (scope names, hashes) use the REST surface over an authenticated channel, where it already exists. In the **server edition**, an ordinary OAuth-authenticated user is also the agent-token tier on every surface: tenant users get verdicts, not scope diagnostics or hash pins.

The agent-token behavior is deliberate **scope-silence**: an out-of-scope probe learns nothing — not even that the server exists. `did_you_mean` suggestions (nearest-name, up to 3) are computed over the caller-visible index only and never name a quarantined server's tools. See [Agent Tokens](./agent-tokens.md) and [Profiles](./profiles.md).

A token's evaluation scope is the intersection of its `allowed_servers`, its `profile_pin`, and any `profile` in the request — so naming another profile can only narrow it. If the pinned profile has since been **deleted**, the scope becomes deny-all and every id answers `not_found`: the pin is a restriction the operator applied, and losing the profile it names must never hand the token a wider view than it had before. The live MCP session path resolves the same way — a preflight's `not_found` for a stale pin is never a false alarm the session would contradict. Re-mint the token (or re-create the profile) to restore it.

## Transparency: every preflight is on the record

Every executed preflight writes an [activity log](./activity-log.md) record — synchronously, before the 200 is returned. If the record cannot be persisted, the preflight itself fails with 503: a check nobody can audit afterwards would undercut the transparency the feature exists to provide.

The same rule holds in band: one check-mode call writes exactly one record (marked `surface: mcp-check`) before the verdict is returned, and a failed write fails the tool call. The `request_id` in the response body is that record's id, so an agent can hand a human the exact handle that finds the run — `mcpproxy activity list --request-id <id>` — without leaving the session.

The record carries the request ID, the requested-id count (unique ids, after dedup), the set verdict, and per-tool reason codes (ids and enum codes only — no descriptions, no upstream tool arguments, and nothing leaves the machine). An in-band record additionally carries the call's own `arguments` — the raw `tool_ids` array as the agent sent it, plus any annotation `filters` — so the raw requested count stays readable next to the deduped one:

```bash
RID=$(curl -si -X POST -H "X-API-Key: $API_KEY" http://127.0.0.1:8080/api/v1/preflight \
  -d '{"tools":[{"id":"gh-ops:sync_issues"}]}' \
  | awk -F': ' '/X-Request-Id/{print $2}' | tr -d '\r')

mcpproxy activity list --request-id "$RID"
# preflight record: ready (1 id) — mcpproxy activity show <id> lists the per-tool verdicts
```

A failed nightly job is diagnosable next morning from the activity log alone: find the preflight record, read the reason codes, done. The same record renders in the Web UI activity view.

## Recipes

### Cron / systemd timer

Branch on the exit code — no JSON parsing needed:

```bash
#!/usr/bin/env bash
# nightly-sync.sh — gate the agent session on its required tools
set -u

mcpproxy tools preflight gh-ops:sync_issues slack:post_message --wait 10s
case $? in
  0)  exec run-nightly-agent-session ;;             # all ready — go
  10) exit 75 ;;                                    # EX_TEMPFAIL: transient, let the next tick retry
  11) notify-oncall "nightly-sync blocked: operator action needed (see mcpproxy activity list)"; exit 1 ;;
  12) notify-oncall "nightly-sync misconfigured: unknown tool id — did a server get renamed?"; exit 1 ;;
  *)  notify-oncall "nightly-sync: preflight itself failed (proxy down?)"; exit 1 ;;
esac
```

### GitHub Actions

On a self-hosted runner that can reach the proxy:

```yaml
jobs:
  preflight:
    runs-on: self-hosted
    steps:
      - name: Required tools are ready
        run: mcpproxy tools preflight gh-ops:sync_issues slack:post_message --wait 10s -o json

  agent-session:
    needs: preflight        # never starts (and never bills tokens) unless preflight passed
    runs-on: self-hosted
    steps:
      - run: ./run-agent-session.sh
```

The `-o json` output lands in the step log, so a red run shows the per-tool reasons without a re-run.

### n8n

Add an **HTTP Request** node before the agent branch:

- **Method / URL**: `POST http://127.0.0.1:8080/api/v1/preflight`
- **Header**: `X-API-Key: <your key>`
- **JSON body**: `{"tools": [{"id": "gh-ops:sync_issues"}, {"id": "slack:post_message"}], "wait_ms": 5000}`

Then an **IF** node on `{{ $json.data.verdict }}`:

- `ready` → proceed to the agent node.
- `degraded_retryable` → a **Wait** node and loop back (bounded).
- anything else → a notification node carrying `{{ $json.data.tools }}`, which already contains the per-tool reasons and remediations.

## Composing with code_execution and stored scripts

[Code execution](./code-execution.md) scripts and [stored scripts](./code-execution.md#stored-scripts) depend on upstream tools exactly the way agents do — and fail the same way when one disappears. The v1 pattern is **REST-from-harness**: the thing that *schedules* the script runs the preflight, before any model or sandbox is involved.

```bash
# Gate a stored script's schedule on the tools it calls
mcpproxy tools preflight gh-ops:list_prs gh-ops:get_pr slack:post_message --wait 10s \
  && mcpproxy code exec --script fetch-prs --input='{"owner":"acme","repo":"api"}'
```

Or from any HTTP harness, the same `POST /api/v1/preflight` call shown above, followed by the `code_execution` MCP call:

```json
{
  "code": "var prs = call_tool('gh-ops', 'list_prs', {owner: 'acme', repo: 'api'}); prs.ok ? prs.result : {error: prs.error};"
}
```

Note that a preflight is *not* callable from inside the sandbox — scripts cannot reach the REST API, which is exactly why the gate belongs in the harness. `describe_tool` check mode does not help here either: `code_execution` routing mode carries no `describe_tool` at all, so the harness call above is the path for those sessions.

## Roadmap (later phases)

The shared evaluator + REST + CLI shipped first; [in-band check mode](#in-band-describe_tool-check-mode) followed. Deliberately deferred:

- **`describe_tool` (and therefore check mode) in `code_execution` and direct routing modes** — today those sessions preflight from the harness.
- **In-band hash pins** — the `expect_hashes` field name is reserved and currently rejected; pins stay a REST/CLI concern, where harnesses author them.
- **`readyz` probe endpoint** and SSE readiness events.
- **Tool lockfile** (`mcpproxy tools lock/verify`) and registered automation contracts with change-time warnings.
- **Agent-token-carried required-tools contracts** and an MCP extension for capability negotiation.
- **Per-user verdicts in the server edition** (`as_user` reserved): v1 verdicts are operator-view, so `oauth_required` reflects global connection state, not the calling user's own token.
- **`server_saturated`** (queue-saturation verdicts, reserved).

Design background: [issue #969](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/969) and the spec + research records in the repo — [098 (evaluator, REST, CLI)](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/specs/098-tools-preflight/spec.md) and [099 (in-band check mode)](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/specs/099-describe-check-mode/spec.md).

## See also

- [REST API](../api/rest-api.md) — endpoint schema, validation rules, envelope
- [Management Commands](../cli/management-commands.md) — `tools preflight` flag and exit-code reference
- [Activity Log](./activity-log.md) — where preflight records land
- [Tool Quarantine](./tool-quarantine.md) — the approval states behind `tool_pending_approval` / `tool_changed`
- [Security Quarantine](./security-quarantine.md) — the server states behind `server_quarantined`
- [Agent Tokens](./agent-tokens.md) · [Profiles](./profiles.md) — the scoping behind the disclosure tiers
