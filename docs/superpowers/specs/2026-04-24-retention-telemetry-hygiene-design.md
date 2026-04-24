# Retention telemetry hygiene + activation instrumentation + auto-start defaults

**Date**: 2026-04-24
**Status**: Approved for speckit flow (user confirmed scope + autonomous execution)
**Repos touched**: `mcpproxy-go`, `mcpproxy-telemetry`, `mcpproxy-dash`

## 1. Problem

After excluding CI installs via the dashboard's existing version-rule + GitHub-IP filter, real-user day-1 retention is **38 %** across 337 installs, with sharp OS segmentation: **macOS 54 %** (76/142), **Windows 42 %** (5/12), **Linux 26 %** (48/183). Three gaps block further work:

1. **CI attribution is heuristic.** The worker classifies CI post-hoc using GitHub Actions CIDRs + "version doesn't start with `v`". Both miss real CI (non-GitHub runners, properly-versioned container images) and occasionally flag real users. There's no ground truth in the payload.
2. **Activation is invisible.** We see `server_count`, `connected_server_count`, and `surface_requests.tray`, but we cannot see: (a) whether an IDE (Claude Code / Cursor / VS Code / Windsurf / Codex CLI / Gemini CLI) ever actually called `/mcp`, (b) whether the user ran `retrieve_tools`, (c) whether auto-start-at-login is configured, (d) how the core was launched (tray / login-item / CLI / installer).
3. **Tray adoption is soft-gated.** macOS retention at 54 % is healthy but we can lift it: ~39 % of macOS v2 installs never recorded a tray request (core running without tray = user quit tray or launched from CLI). Auto-start-at-login is opt-in today.

## 2. Goals

1. **Ground-truth CI classification** — mcpproxy itself decides it's running in CI / cloud IDE / container / headless / interactive, and publishes that verdict in the heartbeat. Dashboard filters on the real field; version-rule heuristic becomes fallback for pre-v3 rows only.
2. **Activation funnel visible** — from the dashboard, answer "of today's real-human first-runs, what % connected a server, what % connected at least one IDE, what % called `retrieve_tools`?" for the first time.
3. **Auto-start default ON** on macOS and Windows tray, with telemetry confirming it. Installer opens the tray automatically on its final step.
4. **Safety** — D1 backed up before any migration, zero PII added, existing stored IP/city re-audited.

## 3. Non-Goals

- Changing the heartbeat cadence (still 24 h + startup-kick).
- Changing `anonymous_id` generation, storage, or lifecycle.
- Adding any identifier that correlates with a human (email, machine name, username, file paths, config contents).
- UI redesign of existing "Connect MCPProxy to AI Agents" dialog — only telemetry hooks.
- Changes to server edition telemetry (we have 0 `edition=server` installs today; defer).
- Removing the dashboard's version-rule CI filter; it stays as fallback for rows where `env_kind` is absent.

## 4. Payload schema v3

Bump `schema_version` from 2 → 3 and extend `payload_json` with five new fields. All other v2 fields unchanged.

### 4.1 New fields

| Field | Type | Values | How computed (client-side) |
|---|---|---|---|
| `env_kind` | enum string | `interactive` \| `ci` \| `cloud_ide` \| `container` \| `headless` \| `unknown` | See §4.2 decision tree |
| `launch_source` | enum string | `tray` \| `login_item` \| `cli` \| `installer` \| `unknown` | See §4.3 |
| `autostart_enabled` | bool \| null | true / false / null if unknown on platform | macOS: read launchd plist for login item; Windows: read registry `Run` key; Linux: always null |
| `activation` | object | see §4.4 | Ever-true flags + last-24h counters |
| `env_markers` | object | see §4.5 | **Booleans only** — never the env-var value |

### 4.2 `env_kind` decision tree (client, in order)

1. Any of `CI=true`, `GITHUB_ACTIONS`, `GITLAB_CI`, `JENKINS_URL`, `CIRCLECI`, `BUILDKITE`, `TF_BUILD`, `TRAVIS`, `DRONE`, `BITBUCKET_BUILD_NUMBER`, `TEAMCITY_VERSION`, `APPVEYOR`, `GITEA_ACTIONS` set → `ci`.
2. Any of `CODESPACES`, `GITPOD_WORKSPACE_ID`, `REPL_ID`, `STACKBLITZ_ENV`, `DAYTONA_WS_ID`, `CODER_AGENT_TOKEN` set → `cloud_ide`.
3. `/.dockerenv` or `/run/.containerenv` exists, OR `$container` = `podman|docker|oci`, AND none of the above → `container`.
4. OS = `darwin` or `windows` → `interactive` (tray/installer platforms).
5. OS = `linux` AND (`$DISPLAY` set OR `$WAYLAND_DISPLAY` set OR stdin is a TTY) AND none of 1-3 → `interactive`.
6. OS = `linux`, none of the above → `headless`.
7. Otherwise → `unknown`.

Detection runs **once at startup** and is cached for the process lifetime.

### 4.3 `launch_source`

- `installer` → set by installer passing `MCPPROXY_LAUNCHED_BY=installer` on first post-install launch (cleared after one heartbeat).
- `login_item` → set when the OS launched the binary as a login item (Mac: LSBackgroundOnly + parent is `launchd`; Windows: process tree rooted at `explorer.exe` Run key launcher).
- `tray` → set when core was started via tray socket handshake (core already receives this; surface it).
- `cli` → default when interactive TTY + no parent-of-launchd.
- `unknown` → anything else.

### 4.4 `activation` object

```json
{
  "first_connected_server_ever": true,
  "first_mcp_client_ever": true,
  "first_retrieve_tools_call_ever": false,
  "mcp_clients_seen_ever": ["claude-code", "cursor", "unknown"],
  "retrieve_tools_calls_24h": 12,
  "estimated_tokens_saved_24h_bucket": "100_1k",
  "configured_ide_count": 2
}
```

- `first_*_ever` — monotonic booleans persisted in BBolt under a new `activation` bucket. Once true, stays true.
- `mcp_clients_seen_ever` — set of User-Agent / client-name fingerprints observed on `/mcp` (client identifies itself via `initialize` params.clientInfo.name per MCP spec). Deduped. Capped at 16 entries to bound payload size. Unknown clients logged as `"unknown"`.
- `retrieve_tools_calls_24h` — sliding-window counter (increment on each `retrieve_tools` builtin call, decay on 24h heartbeat).
- `estimated_tokens_saved_24h_bucket` — bucketed to prevent cardinality: `"0"`, `"1_100"`, `"100_1k"`, `"1k_10k"`, `"10k_100k"`, `"100k_plus"`. Estimate = Σ (tools_not_exposed_to_client * avg_tokens_per_tool_schema). Computed at heartbeat time.
- `configured_ide_count` — count of IDE config files touched by the "Connect MCPProxy to AI Agents" UI, read from the existing config-write tracker.

### 4.5 `env_markers` (booleans only — no values)

```json
{
  "has_ci_env": false,
  "has_cloud_ide_env": false,
  "is_container": false,
  "has_tty": true,
  "has_display": true
}
```

Used for dashboard deep-drill and for sanity-checking `env_kind`. **Never** store the env var value itself.

### 4.6 Removed from payload

`ip_address`, `city`, `country`, `region` **are not transmitted** — they're derived by the worker from Cloudflare request headers. That's already true today; this spec makes it explicit.

## 5. Worker changes (`mcpproxy-telemetry`)

1. **D1 backup before migration** — required first step:
   ```bash
   npx wrangler d1 export mcpproxy-telemetry --remote \
     --output=backups/mcpproxy-telemetry-$(date +%Y%m%d-%H%M%S).sql
   ```
   Committed to a private-repo backup branch, not the public one.
2. **Schema migration** — add `env_kind TEXT` + `launch_source TEXT` + `autostart_enabled INTEGER` columns, indexed on `env_kind`. Write in `migrations/` with up/down SQL. New v3 rows populate them from the parsed JSON; v2 rows leave them NULL.
3. **Validation** — reject payloads where `env_kind` is not in the allowed enum, and where `env_markers` contains any non-boolean value (defense in depth against client bugs leaking values).
4. **Backfill job** — one-off `scripts/backfill-envkind.ts` that reads existing 2,615 rows and computes a heuristic `env_kind` with a `_inferred` suffix (e.g. `ci_inferred`, `interactive_inferred`). Uses: version-rule, GH Actions IP rule, country × OS × uptime patterns. Stored in same column so dashboard code doesn't care. Document in spec that `_inferred` values are heuristic.
5. **PII audit** — review `ip_address`, `city`, `region` storage. Current retention is indefinite. Propose: truncate IP to /24 (IPv4) or /48 (IPv6) on ingest, drop exact IP after 30 days. Applies to all schema versions.

## 6. Dashboard changes (`mcpproxy-dash`)

1. Prefer `env_kind` over version-rule when present (non-NULL, non-`unknown`). Fall back to existing version-rule + GH IP classifier for NULL rows.
2. New pages / sections:
   - **Activation funnel** on `/` overview: first-run → server configured → server connected → IDE connected → `retrieve_tools` called. Counts + conversion %.
   - **Launch source mix** on `/` overview: stacked bar of `launch_source` for last 30 days.
   - **CI transparency** panel on `/ci` (exists): show both classifications (ground-truth + heuristic), a confusion matrix, and residual "unknown" count.
3. `ci=exclude` default becomes `env_kind NOT IN ('ci', 'ci_inferred', 'cloud_ide', 'cloud_ide_inferred')` for v3+ rows. Dashboards must make this transparent (small badge: "real humans only").

## 7. mcpproxy-go changes

1. **`internal/telemetry`**
   - New file `env_kind.go` — detection logic per §4.2, cached at startup.
   - New file `activation.go` — BBolt-backed monotonic flags + rolling counters + token estimator.
   - Extend `payload_v2.go` → `payload_v3.go` (copy + add fields; keep v2 builder for tests); bump `schema_version` constant.
   - Extend existing `surface_requests` tracker to also increment the new `retrieve_tools_calls_24h` counter when the builtin tool fires.
2. **`internal/server/mcp.go`** (or wherever MCP `initialize` is handled) — record observed `clientInfo.name` into `activation.mcp_clients_seen_ever` via the telemetry service.
3. **`cmd/mcpproxy-tray`** (Swift)
   - On install / first launch: if macOS tray's login-item is not set, **set it ON by default**. Show a first-run dialog: "Launch at login" checked by default with a clear opt-out link.
   - Expose the current login-item state via the socket so core can include it in `autostart_enabled`.
4. **Installer changes**
   - macOS DMG: post-install script sets `MCPPROXY_LAUNCHED_BY=installer` and launches the tray once.
   - Windows Inno Setup / WiX: final-step checkbox "Launch MCPProxy now" (default checked) that launches `mcpproxy-tray.exe` with the same env var.
   - Linux `.deb`: no change (no tray today; opt-in systemd user unit already documented).

## 8. Ground rules (non-negotiable)

- **Anonymity preserved** — `anonymous_id` is still a random UUID generated locally, unchanged.
- **No PII added** — env-var detection is boolean-only (`has_ci_env`, not the value). No env var value, file path, username, hostname, or email ever leaves the client. IDE fingerprints use MCP `clientInfo.name` (a short enum-like string: `"claude-code"`, `"cursor"`, etc.), never user paths.
- **D1 backup mandatory** before any `ALTER TABLE` or backfill. Backup file is committed to a private repo.
- **PII audit of existing data** is part of the same spec — propose IP truncation + retention policy.
- **Telemetry remains opt-out** — no change to existing `MCPPROXY_TELEMETRY=false` escape hatch or the `telemetry disable` CLI.

## 9. Success criteria

1. Dashboard's "real human installs" count changes by ≤ 5 % when flipping from version-rule to `env_kind` filter (i.e. heuristic and ground truth agree). Large deltas are acceptable if explainable.
2. Activation funnel page is live and shows non-zero values for each step on v0.25+ rows within 7 days of release.
3. macOS tray installs on v0.25+ show `autostart_enabled=true` for ≥ 90 % of first heartbeats.
4. `launch_source=installer` appears on ≥ 50 % of new macOS installs' first heartbeat.
5. Zero validation-rejection errors on the worker over a 7-day window after v0.25 is the default.
6. Day-2 retention on macOS v0.25+ ≥ Mac v0.24 (78 %) — release is a no-regression + hopefully a lift.

## 10. Verification plan (required for every PR)

1. **Unit tests (Go)** — for every detection branch in `env_kind.go`, every activation-flag transition, every payload-v3 field. Run `go test -race ./internal/telemetry/...`.
2. **E2E (existing)** — `./scripts/test-api-e2e.sh` must pass. Add a new check: `mcpproxy code exec` or a direct heartbeat-builder test that asserts the v3 payload shape.
3. **curl** — start mcpproxy, inspect `/api/v1/status` and any debug endpoint that surfaces the payload; confirm the v3 fields render correctly.
4. **Chrome (`claude-in-chrome`)** — open the dashboard locally (`mcpproxy-dash`) after deploying the worker to preview, confirm new panels render, CI toggle behaves correctly.
5. **UI test MCP (`mcpproxy-ui-test`)** — screenshot the macOS tray after first-run to confirm the auto-start dialog renders + default checked; confirm tray menu shows state.
6. **Worker tests** — `vitest` must cover the new validation rules + backfill classifier.
7. **D1 restore drill** — take the backup, restore to a staging D1, verify row count matches.

## 11. Sequencing

Items 1-4 below are in dependency order; each can be its own PR.

| # | Repo | PR | Blocked by |
|---|---|---|---|
| 1 | `mcpproxy-telemetry` | D1 backup + schema migration + worker validation + backfill script | — |
| 2 | `mcpproxy-go` | Payload v3 + env_kind + activation bucket + tray auto-start default + installer launch-step | 1 |
| 3 | `mcpproxy-dash` | Use env_kind, add activation funnel, add launch-source mix | 1 + 2 (so v3 rows exist to render) |
| 4 | `mcpproxy-telemetry` | PII audit follow-through — IP truncation, retention job | — (parallel with 1) |

## 12. Open questions (answered inline — none remaining)

All major design decisions confirmed in the 2026-04-24 brainstorm.
