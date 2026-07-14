# Anonymous Telemetry

MCPProxy collects anonymous usage statistics to help improve the product. This page explains what is collected, what is not, and how to disable it.

## What is collected

MCPProxy sends a **daily heartbeat** containing only aggregate, non-identifying information. The current schema is **version 7** (`schema_version: 7` in the JSON payload); the schema is forward-compatible so older consumers simply ignore fields they don't recognize.

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
