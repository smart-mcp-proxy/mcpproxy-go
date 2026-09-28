# Contract: health vocabulary (FR-010 – FR-015)

One Go source (`internal/health/constants.go`, `Status*` constants), generated into `frontend/src/types/contracts.ts` by `cmd/generate-types`, decoded by Swift from the golden fixture `internal/health/testdata/status_fixtures.json`.

## `health` object (additive)

```json
{
  "level": "healthy",              // unchanged: breakage severity for badges (research D4)
  "admin_state": "quarantined",    // unchanged
  "summary": "Quarantined for review",
  "detail": "",
  "action": "login",               // always == actions[0] (or "" when actions is empty); the one value change: quarantined + sign-in was "approve" (FR-010)
  "status": "sign_in_required",    // NEW
  "usable": false,                 // NEW: true only when status == "ready"
  "actions": ["login", "approve"]  // NEW: every applicable next step, priority order
}
```

## Status derivation (calculator branch → status)

Every `return` branch of `internal/health/calculator.go` at `638fa805a` maps to exactly one row (several branches share a row, e.g. the OAuth logged-out/expired/error branches under "OAuth login required / re-auth"). T001 has at least one fixture per branch, and T041 fails if a branch has none.

| Calculator branch (existing order) | `status` | `usable` | `actions` (FR-012 order) |
|---|---|---|---|
| `!Enabled` | `disabled` | false | `["enable"]` |
| `Quarantined` and OAuth login required (the quarantined branch now checks the OAuth login-required inputs before returning; `action` becomes `login`, FR-010) | `sign_in_required` | false | `["login","approve"]` |
| `Quarantined` and transport fault (`state=error`) | `error` | false | `["approve","view_logs"]` (attention: a `server_review` item only, never `server_error`, rest-api.md#attention) |
| `Quarantined` otherwise | `needs_review` | false | `["approve"]` |
| missing secret | `needs_secret` | false | `["set_secret"]` |
| OAuth config error / other config error | `needs_config` | false | `["configure"]` |
| automatic reconnection stopped (`RetryStopped`, branch 3b, GH #1145; checked before the connection-state switch; today `unhealthy`, action `restart`, summary = the stop reason) | `error` | false | `["restart","view_logs"]`: summary and detail (attempt count, diagnostic code, last error) unchanged, so it stays distinguishable from a generic connection error by its text |
| endpoint address error | `needs_config` | false | `["edit_url"]` |
| OAuth login required / re-auth | `sign_in_required` | false | `["login"]` |
| connecting / retrying (existing `degraded` branches without an action) | `connecting` | false | `[]` (the calculator is stateless; how long a server has been connecting is the attention function's concern, FR-002) |
| connection state `pending auth` / `pending_auth` (parked awaiting user login, #1013; `oauthAttentionState`: degraded "Sign-in required" for a first-time deferred sign-in, unhealthy "Authentication required" otherwise; action `login` in both) | `sign_in_required` | false | `["login"]` (`level` and `summary` unchanged, so the amber/red distinction survives) |
| call-time OAuth required (branch 4b, `CallTimeOAuthRequired`, MCP-2084: connects and lists tools anonymously but `tools/call` returns "authorization required"; checked after the connection-state switch; degraded, "Sign-in required", action `login`) | `sign_in_required` | false | `["login"]`: tools are listed but cannot be called, so the server is not usable and is a `sign_in_required` attention item |
| connection error | `error` | false | `["restart","view_logs"]` |
| OAuth refresh retrying (`RefreshStateRetrying`, degraded, today's action `view_logs`) | `ready` | true | `["view_logs"]`: still connected on the current token; summary "Token refresh pending" |
| OAuth refresh failed (`RefreshStateFailed`, unhealthy, action `login`) | `sign_in_required` | false | `["login"]` |
| connected (incl. token expiring soon, which keeps its summary) | `ready` | true | `[]` or `["login"]` for "token expiring soon" (a proactive nudge on a usable server: never an attention item, which keys on `status`, rest-api.md#attention) |

Priority order for `actions`: `login` > `set_secret` > `configure` > `edit_url` > `approve` > `restart` > `view_logs` > `enable`.

## Labels (binding for Web UI, macOS window and tray, CLI table)

| `status` | Label | Card/row status line (with detail) | Primary button (`actions[0]`) |
|---|---|---|---|
| `ready` | Online | "Online · 14 tools · scan clean" (or the expiring-token summary) | none when `actions` is empty; "Sign in" when the OAuth token expires soon (`actions: ["login"]`); "View logs" while an OAuth refresh is retrying (`actions: ["view_logs"]`) |
| `connecting` | Connecting | "Connecting…" | — |
| `sign_in_required` | Sign-in required | "Sign in to <name> to load tools" | Sign in |
| `needs_review` | Needs review | "Waiting for your review · 14 tools captured" | Review |
| `needs_secret` | Secret required | "Add <VAR> to connect" | Add secret |
| `needs_config` | Needs configuration | "<detail>" | Fix config / Edit URL |
| `error` | Error | "<summary>" | Restart |
| `disabled` | Disabled | "Disabled" | Enable (a disabled server is never an attention item, FR-002, but its card still offers the one step that brings it back) |

Button labels per action: `login` Sign in · `set_secret` Add secret · `configure` Fix config · `edit_url` Edit URL · `approve` Review (opens the review screen; never approves directly, FR-005) · `restart` Restart · `view_logs` View logs · `enable` Enable.

Colors (Web DaisyUI / macOS): `ready` success/green · `connecting` neutral/gray · `sign_in_required`, `needs_review`, `needs_secret`, `needs_config` warning/orange · `error` error/red · `disabled` neutral/gray. The tray badge keeps its Spec 044 rules (it reads `level` and diagnostics, not `status`).

## CLI

`mcpproxy upstream list`:

```
   NAME         PROTOCOL  TOOLS  STATUS             ACTION
●  filesystem   stdio     14     Online             -
◐  github       http      0      Sign-in required   auth login --server=github
○  scratch      stdio     0      Disabled           upstream enable scratch
```

`STATUS` is the status label (a declared table-text change: it showed the free-text summary such as "Connected (14 tools)", which stays in `-o json` as `health.summary`). `ACTION` keeps today's CLI-hint form, now keyed on `actions[0]` instead of `action`; the strings for existing actions are unchanged: `login` → `auth login --server=<n>`, `restart` → `upstream restart <n>`, `enable` → `upstream enable <n>`, `view_logs` → `upstream logs <n>`, `set_secret` → `Set <detail>`, `configure` → `Edit config`, new `edit_url` → `Edit config`, `approve` → `Approve in Web UI` (109-c), then `review show <n>` once 109-f adds the `review` group. Empty `actions` → `-`. The GH #938 held-tools rule (`serverHoldSummary`) is kept unchanged: the suffix `· N held` is appended to the label (`Online · 2 held`), the all-clear marker is downgraded, and an empty ACTION falls back to `tools list --server=<n>`.

`--status ready|connecting|sign_in_required|needs_review|needs_secret|needs_config|error|disabled` (repeatable). `-o json` rows carry the full `health` object.

## Forbidden renderings (SC-003 test)

For every fixture with `usable=false`, no renderer output contains `healthy`, `Healthy`, `online`, `Online`, `connected` or `Connected` as the server's state.
