---
title: "Anonymous Telemetry"
sidebar_label: "Telemetry"
description: "What the anonymous daily heartbeat contains, what it never contains, and how to disable it."
---

# Anonymous Telemetry

MCPProxy collects anonymous usage statistics to help improve the product. This page explains what is collected, what is not, and how to disable it.

## What is collected

MCPProxy sends a **daily heartbeat** containing only aggregate, non-identifying information. The current schema is **version 9** (`schema_version: 9` in the JSON payload); the schema is forward-compatible so older consumers simply ignore fields they don't recognize.

| Field | Example | Purpose |
|-------|---------|---------|
| `anonymous_id` | `550e8400-...` | Random UUID for deduplication (not linked to you) |
| `machine_id` | `9f86d081...` (64-hex) | Stable, **non-reversible** salted hash of the OS machine id — dedups ephemeral installs whose `anonymous_id` churns every run (schema v6). Empty/omitted when unreadable. Never the raw machine id |
| `version` | `0.21.3` | Track version adoption |
| `edition` | `personal` | Understand edition usage |
| `os` | `darwin` | Platform distribution |
| `arch` | `arm64` | Architecture distribution |
| `server_count` | `12` | Understand scale of usage |
| `connected_server_count` | `8` | Connection success rates |
| `tool_count` | `156` | Tool ecosystem size |
| `uptime_hours` | `47` | Usage patterns |
| `routing_mode` | `retrieve_tools` | Feature adoption |
| `quarantine_enabled` | `true` | Security feature adoption |
| `feature_flags.docker_available` | `true` | Fraction of installs with a reachable Docker daemon (schema v3) |
| `server_protocol_counts` | `{"stdio":3,"http":2,"sse":0,"streamable_http":1,"auto":0}` | Ratio of remote-HTTP vs local-stdio upstreams (schema v3) |
| `server_docker_isolated_count` | `2` | How many configured servers the runtime actually wraps in Docker isolation (schema v3) |
| `feature_flags.docker_isolation_enabled` | `true` | Whether global Docker isolation is turned on (schema v5). Lets us tell "isolation on, 0 matching servers" apart from "isolation off" |
| `feature_flags.docker_cli_source` | `bundled` | How the `docker` CLI was located — fixed enum `path` / `bundled` / `login_shell` / `absent` (schema v5). The direct signal for "Docker installed but not on the spawn PATH" (issue #696). **Never** the path string itself |
| `wizard_shown` | `true` | Whether the onboarding wizard ever rendered for this install (schema v7). Makes "shown but ignored" measurable |
| `wizard_connect_step` | `completed_external` | Onboarding connect-step outcome — fixed enum, widened in v7 (see below) |
| `web_ui_opened` | `12` | Lifetime count of embedded Web UI entrypoint serves (schema v7) |
| `days_since_install` | `14` | Whole-day age of the install (schema v7). A day count, never a timestamp |
| `active_days_30d` | `5` | Distinct UTC days with process activity in the trailing 30 days (schema v7). Only the count — never the per-day breakdown |
| `previous_shutdown` | `clean` | How the previous process instance ended — fixed enum `clean` / `crash`, absent on first run (schema v7) |
| `last_error_code` | `MCPX_DOCKER_CLI_NOT_FOUND` | Most recent stable `MCPX_*` diagnostic code (schema v7). Enum code only, never error text |
| `tpa_scanner` | `{"scans_completed":4,"scans_failed":0,"scans_with_findings":1,"findings":{"high":2},"tool_change_gate_scans":6,"prompt_scans":11}` | Security/TPA scanner activity (schema v8, extended in v9) — counts only, keyed by the fixed severity enum. Omitted entirely when no scan of any kind ran |
| `trust_mode_distribution` | `{"auto":1,"scan":3,"manual":8}` | Configured servers per effective trust tier (schema v9) — fixed enum keys `auto`/`scan`/`manual`, counts only. Never server names |
| `feature_flags.deep_scan_enabled` | `false` | Whether the opt-in deep-scan layer is turned on (schema v8) |
| `preflight` | `{"filter_diag_emitted_24h":3,"availability_block_24h":2,"availability_block_reasons_24h":{"server_quarantined":2},"discovery_omission_24h":5}` | Preflight baseline counters (issue #969) — counts only, reason map keyed by a fixed enum. Omitted entirely when nothing was counted. See below |

The `server_protocol_counts` map uses a **fixed enum of keys** (`stdio`, `http`, `sse`, `streamable_http`, `auto`) — server names and URLs are never included. Unknown or misconfigured protocol values are bucketed into `auto`.

The `docker_cli_source` field is likewise a **fixed enum** (`path`, `bundled`, `login_shell`, `absent`); the resolved path is never transmitted.

Docker isolation failures surface in `error_code_counts_24h` via three stable diagnostic codes (schema v5): `MCPX_DOCKER_CLI_NOT_FOUND` (isolation requested but the `docker` binary is unresolved — issue #696), `MCPX_DOCKER_EXEC_NOT_FOUND` (the image lacks the interpreter the server needs, e.g. `uvx` missing in `python:3.11`), and `MCPX_DOCKER_OCI_RUNTIME` (OCI runtime / architecture-mismatch failures).

## What is NOT collected

The following is **never** collected:

- Server names, URLs, or configurations
- Tool names or descriptions
- API keys, tokens, or credentials
- File paths or environment variables
- IP addresses (stripped by our server before storage)
- User identity, email, or account information
- Tool call content, arguments, or responses
- Any user-generated content
- The **raw** OS machine id or any reversible hardware identifier (only the salted, non-reversible `machine_id` hash is sent — see below)

## Anonymous ID

The anonymous ID is a random UUID (v4) generated on first run. It has **no correlation** to your hardware, user account, or identity. It exists solely to deduplicate heartbeats (so we don't count the same install twice in a day).

You can delete it by removing the `telemetry.anonymous_id` field from your config — a new random ID will be generated on next startup.

## Machine ID (schema v6)

The `anonymous_id` above is a UUID persisted in the config file. In **ephemeral environments** — throwaway `HOME`s, layered Docker builds, CI runners — the config (and therefore the UUID) is regenerated on every run, so a single machine can masquerade as hundreds of distinct installs. That inflates our install counts and defeats deduplication.

`machine_id` fixes this without collecting anything identifying:

- It is a **salted, non-reversible hash** — `HMAC-SHA256` keyed by the OS machine id, scoped by an mcpproxy-specific application key. The **raw machine id is never transmitted**; only the hash leaves your machine.
- The application-specific key means the value **cannot be correlated** with any other application's telemetry that hashes the same OS machine id.
- It is **stable per physical machine**, so ephemeral installs collapse to one identity for counting.
- If the OS machine id **cannot be read** (a container without `/etc/machine-id`, a permission error, or an exotic platform), the field is simply **omitted** — the heartbeat is never blocked, and the backend treats an absent value as "unknown".

`machine_id` respects the **same opt-out** as every other field: when telemetry is disabled (see below), the entire heartbeat — including `machine_id` — is never sent.

## Schema v7 — activation funnel & churn fields (Spec 080)

Schema v7 adds seven purely **additive** signals so we can measure whether installs come back after day one — and, when they don't, whether the last session ended cleanly or crashed. Every field keeps the established privacy posture: **booleans, non-negative integers, or documented fixed enums only** — no timestamps, no per-server identity, no free text. The anonymity scanner (`internal/telemetry/anonymity.go`) enforces these shapes on the serialized payload before every send, and all fields use `omitempty`, so a payload with none of them set is shape-identical to a v6 payload except for `schema_version`.

### Widened enum: `wizard_connect_step`

The onboarding connect-step status (a v4 field) gains a fourth value in v7:

| Value | Meaning |
|-------|---------|
| *(absent)* | Step never shown to this install |
| `completed` | User completed the connect step inside the wizard |
| `completed_external` | **New in v7.** User dismissed the wizard with the connect step untouched, but the install was already connected (via `mcpproxy connect`, the ConnectModal, or manual config). Previously miscounted as `skipped` |
| `skipped` | User dismissed the wizard with the connect step untouched and **no** connection evidence existed |

**Guidance for consumers**: this is a string enum that may widen again. Code that switches on `completed` / `skipped` must treat **unknown values as "other/engaged"**, never as a skip or an error. Statuses recorded before v7 are never rewritten — segment analyses by `schema_version`.

### New fields

| Field | Type | When it is set | Privacy rationale |
|-------|------|----------------|-------------------|
| `wizard_shown` | boolean | `true` once the onboarding wizard has rendered at least once for this install; omitted otherwise. Together with `wizard_engaged` it distinguishes "shown but ignored" from "never shown" | A single boolean about our own UI; carries no user data |
| `web_ui_opened` | non-negative integer | Lifetime count of serves of the embedded Web UI **entrypoint** (index document). Asset and API requests never increment it; it is independent of `surface_requests.webui`. Coarse by design — health checkers fetching `/` count too | A counter of our own page serves; no URLs, sessions, or timing |
| `days_since_install` | non-negative integer | Whole-day UTC age of the install, from a persisted first-install day stamp (independent of `anonymous_id`). `0` on install day; clamped at 0 on clock skew. Omitted when the local store isn't available (short-lived CLI commands) | Only a day **count** is transmitted — the install timestamp itself never leaves the machine |
| `active_days_30d` | non-negative integer (1–30) | Number of distinct UTC days with process activity in the trailing 30-day window. Old days age out | The per-day set is stored locally and **never transmitted** — counters, not timelines |
| `previous_shutdown` | fixed enum `clean` \| `crash` | How the **previous** process instance ended: `clean` = the graceful-shutdown path ran; `crash` = it didn't (SIGKILL, panic, power loss). Absent on a first-ever run — a fresh install is never reported as a crash. Stable across all heartbeats of the current instance | One enum value about our own process lifecycle; no stack traces, no session timing |
| `last_error_code` | fixed enum (`MCPX_*`) | The most recently observed stable diagnostic code (same fixed set as `diagnostics.error_code_counts_24h`), persisted across restarts so a post-crash heartbeat carries the pre-crash code. Absent when no error was ever recorded | Only the enum code is stored and sent — **never** error messages, server names, paths, or stack traces. The scanner rejects any value outside the fixed diagnostics catalog |

Why these exist: telemetry showed most installs connect successfully but never return after day one. `days_since_install` + `active_days_30d` make retention computable from a single heartbeat (no cross-heartbeat identity joins), and `previous_shutdown` + `last_error_code` let the final heartbeat before an install goes silent distinguish "crashed and never came back" from "exited cleanly and never returned".

### Activation: `first_real_tool_call_ever`

The `activation` block gains one additive boolean:

| Field | Type | When it is set | Privacy rationale |
|-------|------|----------------|-------------------|
| `first_real_tool_call_ever` | boolean | `true` once an upstream server has **successfully returned a result** for at least one real tool call (any `call_tool_read` / `call_tool_write` / `call_tool_destructive`). Built-in tools such as `retrieve_tools` do **not** set it, and neither do **attempted** calls that fail — malformed args, a quarantined or disabled tool, a disconnected server, or an upstream error. Monotonic and lifetime-scoped, exactly like `first_retrieve_tools_call_ever` | A single boolean about our own funnel; no tool name, server, or arguments |

**Why it exists.** The retrieve→call funnel used to compare a *lifetime* flag (`first_retrieve_tools_call_ever`) against a *24h windowed* counter (`retrieve_tools_calls_24h` / upstream-call counters). That asymmetry made conversion look like a cliff ("42% search → 16% call") when the true lifetime-vs-lifetime conversion is far higher. This flag is the missing symmetric term.

**Success, not attempt.** The flag is stamped only after the upstream returns a result — deliberately *not* alongside the `upstream_tool_calls` counter, which fires on every invocation including blocked and failed ones. An install whose first tool call was blocked by quarantine has **not** activated, and counting it as activated would hide exactly the breakage this metric exists to surface.

**Guidance for consumers**: measure the funnel step as `first_real_tool_call_ever` over `first_retrieve_tools_call_ever`. Do **not** compare either lifetime flag against a windowed counter, and note that `upstream_tool_calls` counts *attempts* while this flag counts *success* — they are not the same denominator.

### `launch_source` = `tray`

`launch_source` is a v3 field, but its `tray` value was **unreachable in practice until now**: a tray-spawned core has the tray app as its parent (not `launchd`, so not `login_item`) and no TTY (so not `cli`), and nothing told it otherwise — so it fell through to `unknown`. That is why `launch_source` was ~79% `unknown` on the flagship macOS path.

Both trays now stamp `MCPPROXY_LAUNCHED_BY=tray` on the core process they spawn, and the core honours it. The DMG installer's `MCPPROXY_LAUNCHED_BY=installer` still outranks it, so first-run attribution is unchanged.

**Guidance for consumers**: `unknown` counts from before this fix are not comparable with counts after it — segment by version.

All v7 fields ride the **same opt-out** as the rest of the heartbeat: when telemetry is disabled, nothing is transmitted. Local counters may still persist on disk (so re-enabling doesn't fabricate a fresh-install picture), but they never leave the machine.

You can inspect exactly what would be sent — including every v7 field — with:

```bash
mcpproxy telemetry show-payload
```

## Schema v8 — security-scanner stats

Schema v8 adds two purely **additive** signals so we can see whether the TPA / security scanner actually runs in the fleet, whether it fails, and whether it finds anything — without learning *what* it found or *where*.

| Field | Type | When it is set | Privacy rationale |
|-------|------|----------------|-------------------|
| `tpa_scanner.scans_completed` | non-negative integer | Terminal, successful **scan jobs** since the last accepted heartbeat | A count of our own scan runs |
| `tpa_scanner.scans_failed` | non-negative integer | Terminal **scan job** failures in the same window | The scanner id, the server, and the error text are never accepted by the counter API |
| `tpa_scanner.scans_with_findings` | non-negative integer | Subset of `scans_completed` that produced at least one finding | Tells "scanner is running" apart from "scanner is finding things" |
| `tpa_scanner.findings` | map, **fixed enum keys only** (`critical`/`high`/`medium`/`low`/`info`) → non-negative integer | Per-severity finding totals across the window. Severities with a zero total are omitted | Severity is a five-value enum; rule ids, finding titles, tool names, and file paths are dropped before they reach the counter |
| `feature_flags.deep_scan_enabled` | boolean | The `security.deep_scan.enabled` master switch | A single boolean about our own config |

**Unit of measure — one non-deep-scan (Pass 1) scan job.** Every `scans_*` counter counts *scan jobs*, not scanner invocations and not passes:

- a job that runs five scanners counts **once**, however many of them fail — `scans_failed` counts failed jobs (all scanners failed), and a job that loses some scanners but still completes counts only in `scans_completed`;
- the **Pass-2 deep supply-chain audit** that deep scan auto-starts after Pass 1 is **not counted**. Counting it would report ~2× the scans for exactly the deep-scan cohort that `feature_flags.deep_scan_enabled` exists to compare against everyone else;
- **dry-run** jobs are not counted.

The decision lives in the scanner package (`scanCallbackAdapter.countsForTelemetry` in `internal/security/scanner/service.go`), which is the only layer that knows a job's pass and dry-run status; it calls the single-purpose `EmitSecurityScanTelemetry` emitter hook, implemented on `Runtime` (`internal/runtime/event_bus.go`) as the only caller of the counter API. The UI-facing scan events (`EmitSecurityScanCompleted` / `EmitSecurityScanFailed`) deliberately record nothing — they fire per scanner and per pass.

The whole `tpa_scanner` object is **omitted** when every counter is zero (v9 counters included), so an install that never scans emits a payload shape-identical to v7. The anonymity scanner (`internal/telemetry/anonymity.go`, rule `v8_field_invalid`) re-asserts the contract on the serialized payload before every send: whitelisted keys, non-negative integers, and severity-enum keys only — a producer-side regression that leaked a server name or rule id as a map key would block the heartbeat rather than transmit it.

**Never transmitted**: the scanned server's name, the scanner id, rule ids, finding titles or descriptions, matched content, file paths, and scan error messages.

## Schema v9 — making the TPA funnel measurable

The v8 counters above only see **scan jobs**, which most installs never start. Two TPA detection paths run *synchronously, for ordinary users* and emitted nothing at all, so the fleet read as "the scanner never runs". Schema v9 adds a counter for each, plus the denominator they need.

| Field | Type | When it is set | Privacy rationale |
|-------|------|----------------|-------------------|
| `tpa_scanner.tool_change_gate_scans` | non-negative integer | One per changed tool put through the synchronous `trust_mode: scan` gate (`internal/runtime.scanChangeIsClean`) since the last accepted heartbeat | Counts gate **invocations**, not outcomes. Whether the change was auto-approved or held, which server it was, and which checks matched are never accepted by the counter API |
| `tpa_scanner.prompt_scans` | non-negative integer | One per aggregated upstream **prompt** put through the poisoning filter (`internal/server.scanAggregatedPrompts`) in the same window | Same posture: invocation count only — never the prompt name, the server, or the verdict |
| `trust_mode_distribution` | map, **fixed enum keys only** (`auto`/`scan`/`manual`) → non-negative integer | Every heartbeat: configured servers grouped by `ServerConfig.EffectiveTrustMode()` | Three-value enum plus counts. Server names and raw config strings never reach the map |

`trust_mode_distribution` is a **state** field, not a delta counter: it is recomputed from the live config on every heartbeat and never reset, and all three keys are always present (zero included) so consumers can rely on the shape. It is the denominator for `tool_change_gate_scans` — only servers resolving to `scan` can produce a gate scan at all — and the first fleet-wide view of which trust tier installs actually sit in. `EffectiveTrustMode()` is the single resolution point, so an empty (inherit) mode, a typo'd mode, and the legacy `auto_approve_tool_changes` / `skip_quarantine` fields all fold into one of the three tiers before counting.

The two new counters share the window and reset semantics of every other registry counter — zeroed only after an accepted (2xx) heartbeat. **Do not sum them with the v8 job counters**: the units differ (one changed tool / one prompt vs. one scan job).

The anonymity scanner enforces both shapes on the wire form: the v9 counters widen the `tpa_scanner` key whitelist (rule `v8_field_invalid`), and `trust_mode_distribution` gets its own rule `trust_mode_field_invalid` — fixed trust-tier keys with non-negative integer counts, or the heartbeat is blocked.

## Preflight baseline counters (issue #969)

The `preflight` sub-object measures two things the proxy currently does silently:
how often a `retrieve_tools` response **explains a filter** it applied (spec 094's
`filter_diagnostics` block) and whether the agent then acted on that explanation,
and how often a tool the caller asked for **existed but was withheld**.

These counters ship **one release ahead** of the required-tools-preflight feature
on purpose. Without a live pre-feature window there is nothing to compare the
post-feature numbers against, and "did preflight help?" degrades into an argument
about anecdotes.

| Field | Type | What it counts |
|-------|------|----------------|
| `preflight.filter_diag_emitted_24h` | non-negative integer | `retrieve_tools` responses that **delivered** a `filter_diagnostics` block in the last 24h — the denominator for the rest. Counted after response truncation, so a block that `tool_response_limit` cut back out of the payload is not counted (and cannot be "followed") |
| `preflight.filter_diag_missing_annotation_24h` | non-negative integer | Omissions in those blocks caused by **absent upstream annotations** ("fix the server" class), summed across filters |
| `preflight.filter_diag_explicit_24h` | non-negative integer | Omissions caused by an **explicitly unsafe hint** ("the filter is working" class), summed across filters |
| `preflight.filter_diag_followed_24h` | non-negative integer | Blocks the agent **acted on**: a later `retrieve_tools` call in the same MCP session dropped or relaxed a filter the block blamed |
| `preflight.availability_block_24h` | non-negative integer | Policy **blocks** (quarantine, scope, permissions, tool approval, output policy) in the last 24h. Derived as the sum of the reason split below — each reason is stored with its own 24h window, and a separate stored total would drift out of agreement with the split it summarises |
| `preflight.availability_block_reasons_24h` | map, **fixed enum keys only** → non-negative integer | The same total split by reason: `intent_invalid`, `intent_rejected`, `profile_scope`, `token_scope`, `token_permission`, `server_quarantined`, `tool_pending_approval`, `tool_changed_approval`, `tool_not_callable`, `output_sanitisation`, `output_schema`, `other` |
| `preflight.discovery_omission_24h` | non-negative integer | `retrieve_tools` responses that **withheld locked or quarantined matches** the caller could not see (`include_disabled` unset) — the silent-unavailability substrate |

**How the reason keys stay safe.** The classification comes from the gate that
fired, not from parsing the message it wrote: every call site of the single
policy-decision funnel (`emitActivityPolicyDecision`) declares a key from the
closed enum above, while the operator-facing prose — which embeds server and tool
names — stays in the activity log and never reaches telemetry. A key outside the
enum is folded into `other` at write time, filtered again at read time and in the
wire form, and the anonymity scanner (rule `preflight_field_invalid`) blocks the
heartbeat outright if a non-enum key ever reaches the serialized payload.

**The follow-through signal holds no identity.** Detecting "the agent relaxed the
filter" needs only the previous call's blamed filter *keys* plus the session it
belonged to. That note is in-memory, per session, expires after 15 minutes,
is capped, is consumed on first use (one block can be followed at most once), and
is never persisted or transmitted — only the resulting count is.

Because that note is keyed by session, a transport that mints no session id
cannot be credited with a follow-through, while its emissions still count. Read
`filter_diag_followed_24h / filter_diag_emitted_24h` as a **lower bound** on
engagement, not an exact rate.

The whole `preflight` object is **omitted** when every counter is zero, so an
install that never trips one emits a payload shape-identical to one from before
the field existed. Counters are gated at **event time**: nothing is written while
telemetry is opted out, so an occurrence observed during opt-out can never become
transmissible if telemetry is re-enabled later.

**Never transmitted**: the query text, tool or server names, filter values, the
session id, the number of tools in the response, or any part of the suggestion
string the agent saw.

## One-time opt-out signal

When telemetry transitions from **enabled to disabled** (via the CLI, the config
file, or the web UI / macOS app), MCPProxy sends **exactly one** final, anonymous
beacon — an `event: "telemetry_disabled"` carrying **only your anonymous install
ID** and **no usage data**. It lets us count how many installs opt out so we can
gauge how the feature is received. The send is best-effort: if it fails,
telemetry is still disabled. After it, **no further telemetry is emitted**.

Disabling while already disabled (or reloading a config that is already
disabled) sends nothing. Setting `MCPPROXY_TELEMETRY=false` is treated as
"never enabled" and also sends nothing.

## How to disable

There are three ways to disable telemetry:

### 1. CLI (recommended)

```bash
mcpproxy telemetry disable
```

Verify with:
```bash
mcpproxy telemetry status
```

Re-enable anytime:
```bash
mcpproxy telemetry enable
```

### 2. Configuration file

Edit `~/.mcpproxy/mcp_config.json`:

```json
{
  "telemetry": {
    "enabled": false
  }
}
```

### 3. Environment variable

```bash
export MCPPROXY_TELEMETRY=false
```

This overrides the config file setting and is useful for CI/CD environments or system-wide policies.

## Data handling

- Telemetry data is sent to a Cloudflare Worker over HTTPS
- Source IP addresses are stripped before storage
- Data is stored in Cloudflare D1 (EU region)
- Used only for aggregate product analytics
- No third-party analytics services receive the data

## Source code

The telemetry implementation is fully open-source:

- [`internal/telemetry/telemetry.go`](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/internal/telemetry/telemetry.go) — heartbeat logic
- [`internal/config/config.go`](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/internal/config/config.go) — configuration (`TelemetryConfig`)
