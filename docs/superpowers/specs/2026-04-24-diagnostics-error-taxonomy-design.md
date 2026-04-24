# Diagnostics & error taxonomy deep-dive

**Date**: 2026-04-24
**Status**: Approved for speckit flow (user confirmed scope + autonomous execution)
**Repo**: `mcpproxy-go` (Go core + Vue frontend + macOS tray)

## 1. Problem

Of 306 real-user installs with ≥ 1 configured upstream server, only 238 (78 %) have any connected server — so **~22 % of servers never connect at all**, and among those with servers 30 % show `server_count > connected_server_count` (partial failure). Users hit errors that are cryptic (`oauth_refresh_failed`, `docker_status fail`, `deprecated_configs fail`), and the CLI `doctor` output is pass/fail per category with no user-facing fix guidance. The existing `doctor_checks` and `error_category_counts` fields in v2 telemetry already tell us WHICH checks fail, but there's no actionable user path and no stable error-code catalog.

The hypothesis: **every "didn't connect" failure is diagnosable and most are fixable in one click or one command.** We need (a) a stable error-code catalog, (b) user-facing explanations and fix steps per code, (c) surfacing in tray + web UI + CLI, (d) telemetry on which codes occur and which fixes succeed.

## 2. Goals

1. **Stable error-code catalog** — every recoverable failure path gets a code like `MCPX_OAUTH_REFRESH_403`, `MCPX_DOCKER_DAEMON_DOWN`, `MCPX_STDIO_CRASH_ENOENT`. Codes are stable across releases; descriptions can evolve.
2. **Per-code "how to fix"** — each code has: (a) one-sentence human explanation, (b) concrete next steps (click / command / link), (c) deep-link to `docs/errors/<code>.md` that expands the explanation.
3. **Surfacing**:
   - **Tray** — badge showing "N servers failing"; menu group "Fix issues" with per-server fix button that routes to the right action.
   - **Web UI** — per-server error panel with the code + explanation + fix button(s).
   - **CLI** — `mcpproxy doctor --server <name>` shows code + fix steps; `mcpproxy doctor fix --code <CODE>` runs the automated fix when available (with `--dry-run`).
4. **Telemetry** — v3 payload adds `diagnostics.error_code_counts_24h` (bucketed), `diagnostics.fix_attempted_24h` (count of fix-button clicks), `diagnostics.fix_succeeded_24h`. Code names are stable strings — safe to aggregate.
5. **Measurable improvement** — after rollout, the % of installs with `server_count == connected_server_count` rises over 30 days.

## 3. Non-Goals

- Fully automated auto-remediation. User approves every fix.
- Replacing existing logging. The taxonomy layers *on top of* existing zap logs.
- Fixing every failure mode. Scope is limited to the recurring categories we already see in telemetry + the ones we find during the error-inventory phase.
- Changing MCP protocol behavior.

## 4. Error taxonomy (initial catalog — to be expanded during implementation)

Codes follow `MCPX_<DOMAIN>_<SPECIFIC>` with stable identifiers. Each has: `code`, `severity` (info/warn/error), `user_message`, `fix_steps` (ordered list with optional `action` type: `link` / `command` / `button`), `docs_url`.

### 4.1 Initial domains (drawn from existing `error_category_counts` + common GitHub issues)

| Domain | Example codes | Typical fix |
|---|---|---|
| `OAUTH` | `MCPX_OAUTH_REFRESH_EXPIRED`, `MCPX_OAUTH_REFRESH_403`, `MCPX_OAUTH_DISCOVERY_FAILED`, `MCPX_OAUTH_CALLBACK_TIMEOUT` | Re-login via tray / web UI button that triggers the OAuth flow |
| `STDIO` | `MCPX_STDIO_SPAWN_ENOENT`, `MCPX_STDIO_EXIT_NONZERO`, `MCPX_STDIO_HANDSHAKE_TIMEOUT` | Install missing tool (npx/uvx guidance), check working_dir, show last N log lines |
| `HTTP` | `MCPX_HTTP_DNS_FAILED`, `MCPX_HTTP_TLS_FAILED`, `MCPX_HTTP_401`, `MCPX_HTTP_404`, `MCPX_HTTP_5XX` | Check URL, provide auth header, TLS debug; one-click edit server config |
| `DOCKER` | `MCPX_DOCKER_DAEMON_DOWN`, `MCPX_DOCKER_IMAGE_PULL_FAILED`, `MCPX_DOCKER_NO_PERMISSION`, `MCPX_DOCKER_SNAP_APPARMOR` | Show install docs for Docker Desktop / colima; snap-docker specific guidance |
| `CONFIG` | `MCPX_CONFIG_DEPRECATED_FIELD`, `MCPX_CONFIG_PARSE_ERROR`, `MCPX_CONFIG_MISSING_SECRET` | Auto-migrate button for deprecated fields; show diff preview |
| `QUARANTINE` | `MCPX_QUARANTINE_PENDING_APPROVAL`, `MCPX_QUARANTINE_TOOL_CHANGED` | Link to quarantine panel with approve button |
| `NETWORK` | `MCPX_NETWORK_PROXY_MISCONFIG`, `MCPX_NETWORK_OFFLINE` | Show detected system proxy; ping test button |

Exact code list for each domain is produced during the **error-inventory task** (first implementation phase): grep every `zap.Error` call path and every terminal error state in `internal/upstream/*`, `internal/oauth/*`, `internal/server/*`, then map each to a code + message + fix. Spec does not pre-enumerate every code; it mandates the *structure*.

## 5. Implementation structure

### 5.1 New package `internal/diagnostics`

```
internal/diagnostics/
├── catalog.go        // Code, Severity, Message, FixStep, DocsURL types; registry
├── codes.go          // All MCPX_* constants — generated / hand-written
├── classifier.go     // Takes a raw error (from upstream/oauth/docker) → returns a Code
├── registry.go       // In-memory registry; loaded at startup; tests enforce completeness
├── fixers.go         // Optional automated-fix handlers per code (dry-run first)
└── codes_test.go     // Every code must have a message + at least one fix_step
```

### 5.2 Integration points

- `internal/upstream/manager.go` wraps every connection failure into a `DiagnosticError{Code, Cause, ServerID}`.
- `internal/oauth/*` similarly classifies OAuth-specific failures.
- `internal/runtime/stateview/stateview.go` includes latest `DiagnosticError` per server in the snapshot.
- `/api/v1/servers/{name}/diagnostics` extends today's response with a structured `error_code`, `user_message`, `fix_steps` array (existing consumers keep working; new fields additive).
- `/api/v1/diagnostics/fix` — new endpoint that runs a named fix by code for a server (dry-run by default). Idempotent and rate-limited.
- `internal/runtime/activity_service.go` records each fix attempt + outcome so the activity log has audit trail.

### 5.3 Frontend (`frontend/src/`)

- New `components/diagnostics/ErrorPanel.vue` — reusable component rendering `{code, message, fix_steps}`. Steps render as buttons (trigger API) or links (open docs/URL) or inline code (copyable).
- Used from `ServerDetail.vue` (per-server) and from a new `DiagnosticsPage.vue` (aggregate).
- `stores/servers.ts` subscribes to the existing SSE stream; when a server's `error_code` changes, ErrorPanel updates.

### 5.4 macOS tray (Swift)

- Status badge: red dot if any server has `severity=error`; orange if only `warn`.
- Menu section "⚠ Fix issues (N)" — collapses to per-server items. Clicking opens web UI to the right server's diagnostics panel (single-source-of-truth for fix buttons, avoids duplicating fix logic in Swift).
- On macOS the tray already reads `/api/v1/servers`; extend to read the new `error_code` field.

### 5.5 CLI (`cmd/mcpproxy`)

- `mcpproxy doctor` — unchanged default output; add `--server <name>` filter and `--codes` to print codes instead of categories.
- `mcpproxy doctor fix <CODE> --server <name>` — runs the fixer. `--dry-run` by default if the fix is potentially destructive.
- `mcpproxy doctor list-codes` — prints the full catalog (useful for documentation generation and for AI agents).

### 5.6 Docs

Each code gets a page at `docs/errors/<CODE>.md` with: explanation, cause, fix steps, related links. Index at `docs/errors/README.md`. Auto-generated stub from the catalog; hand-written body. Linked from the web UI + tray.

## 6. Telemetry extensions (ties into spec 1)

v3 payload gets a new top-level `diagnostics` object (ships with spec 1's schema bump to avoid a second migration):

```json
{
  "diagnostics": {
    "error_code_counts_24h": {
      "MCPX_OAUTH_REFRESH_EXPIRED": 3,
      "MCPX_DOCKER_DAEMON_DOWN": 1
    },
    "fix_attempted_24h": 2,
    "fix_succeeded_24h": 1,
    "unique_codes_ever": 7
  }
}
```

- `error_code_counts_24h` — capped to top 20 codes per heartbeat to bound payload.
- `fix_attempted_24h` / `fix_succeeded_24h` — how often users click fix buttons.
- `unique_codes_ever` — sanity check for catalog coverage.

## 7. Ground rules

- **Backwards compatible** — existing `doctor_checks` v2 field stays; new fields are additive. v2 dashboards keep working.
- **No auto-remediation without user click.** Fixers never run at startup or on a schedule. Every fix is a response to a button press or CLI invocation.
- **Error codes are stable.** Once shipped, a code's `name` never changes; deprecation = mark it hidden + point to new code.
- **Docs auto-linked.** Every code surfaces a docs URL, and every docs page is real (CI check: no 404).

## 8. Verification plan (required per PR)

1. **Unit tests** — `internal/diagnostics/*_test.go`: every code has a registered message + ≥1 fix step; classifier correctly maps 20+ golden error samples to the right code; fixer dry-runs don't mutate state.
2. **E2E** — extend `./scripts/test-api-e2e.sh`: start mcpproxy, configure a deliberately-broken stdio server (`command: /nonexistent`), hit `/api/v1/servers/.../diagnostics`, assert `error_code == "MCPX_STDIO_SPAWN_ENOENT"`, call `/api/v1/diagnostics/fix` with dry-run and assert expected preview.
3. **curl** — each new endpoint covered by a curl example in `docs/api/rest-api.md` and a smoke test in CI.
4. **Chrome browser** — open the web UI via `claude-in-chrome`, navigate to a broken server's detail page, confirm ErrorPanel renders with code + fix button; click fix (dry-run path), confirm toast.
5. **UI test MCP** — screenshot macOS tray with a broken server, confirm red badge + "Fix issues (N)" menu group; click menu item, assert it opens the web UI to the right URL.
6. **Docs link check** — CI job runs a link checker over `docs/errors/*.md` ensuring each docs page exists for every code registered in the catalog.

## 9. Sequencing

1. **Error inventory** — grep existing codebase, enumerate error sites, produce initial code list (PR: catalog-only, no surfacing).
2. **Diagnostics package + classifier + registry tests** — PR adds `internal/diagnostics` + wires one domain (`STDIO` — highest-volume failures).
3. **Expand to remaining domains** — one PR per domain (OAUTH, HTTP, DOCKER, CONFIG, QUARANTINE, NETWORK), parallelizable.
4. **REST API + telemetry** — add `/diagnostics` structured fields + v3 telemetry (merges with spec 1's schema bump).
5. **Web UI ErrorPanel** — add Vue component + wire from ServerDetail.
6. **macOS tray badges** — add menu group + badge.
7. **CLI `fix` subcommand** — with `--dry-run` default.
8. **Docs pages** — auto-generate stubs, hand-fill bodies.

## 10. Success criteria

1. 100 % of terminal connection errors map to a non-empty `error_code` (no more "unknown failure").
2. Every code has: message, ≥1 fix step, docs URL (link-checked in CI).
3. Over 30 days post-launch, `connected_server_count / server_count` ratio among real-user installs rises.
4. `fix_succeeded_24h / fix_attempted_24h` ≥ 0.5 (half of user-initiated fixes work).
5. `/api/v1/servers/{name}/diagnostics` 200 latency p95 < 50 ms.

## 11. Open questions

- **Automated fix for `MCPX_DOCKER_SNAP_APPARMOR`** — we know (from prior memory) that snap-docker + AppArmor is fundamentally incompatible with our scanner. Should the fix be "disable scanner for this server" or "suggest switching to non-snap Docker"? Decide during domain-3 PR.
- **OAuth re-auth fix UX** — tray button → system browser flow. Needs testing with ≥ 2 providers to ensure the existing flow-coordinator handles concurrent re-auth gracefully.

Both are acceptable to defer to implementation time — they don't block the catalog structure.
