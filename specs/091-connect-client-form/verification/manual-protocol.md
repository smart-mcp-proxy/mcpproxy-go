# Manual Verification Protocol — Native Connect Client Form (Spec 091)

Run on the built app (`scripts/build-swift-app.sh`) against a dev core
(isolated data dir + config, high port). Capture screenshots into
`specs/091-connect-client-form/verification/shots/`.

## Setup

1. Seed client configs in a scratch HOME (or point the core's client registry
   paths at scratch copies): one client with an mcpproxy entry (connected),
   one with a config but no entry, one CREATE-CAPABLE client with no config
   file (e.g. Cursor), one non-create-capable client with no config file
   (OpenCode), and one with a deliberately malformed config (invalid JSON).
2. Start the core; start the app connected over the local socket.

## Checks

| # | Check | Expect | Screenshot |
|---|-------|--------|------------|
| 1 | Menu item | "Connect Client…" next to "Add Server…" | `01-menu.png` |
| 2 | List states | config present / no config found / unsupported-disabled rows | `02-list.png` |
| 2b | No content reads on open | run once against a REAL client location under App-Data TCC (e.g. actual Claude Desktop container): opening the list triggers no TCC prompt; alternatively verify via core logs that zero config-content reads occur on list | `02b-tcc.png` or log excerpt |
| 3 | SC-001 stopwatch | menu → connected client < 30 s, no browser | note time below |
| 4 | Preview (add) | entry text + path + timestamped-backup statement, before any Connect control | `03-preview-add.png` |
| 5 | Preview (replace) | structural summary of the existing entry (name/type/query+userinfo-stripped endpoint/header+env names — no secret values) AND the replacement both visible; for an adopted equivalent entry the summary names its actual key | `04-preview-replace.png` |
| 6 | Preview (create) | "file will be created… Undo removes it" statement | `05-preview-create.png` |
| 7 | Credential notice | shown when entry embeds the admin credential | `06-credential.png` |
| 8 | Connect + refresh | row flips to connected without reopening | `07-connected.png` |
| 9 | Undo (session) | Undo visible after connect; restores config byte-exact (diff the file); gone after form reopen | `08-undo.png` |
| 9b | Undo (created file) | for the create-capable no-config client: connect creates the file, Undo removes it (verify file absent afterwards) | `08b-undo-create.png` |
| 9c | Conflict precondition | between preview and Connect, externally change ONLY a masked credential value inside the existing entry: core rejects with conflict (token detects what sanitization hides), form re-previews | `08c-conflict.png` |
| 9d | Non-create-capable absent | OpenCode with no config: Connect unavailable, core refusal shown verbatim | `08d-opencode.png` |
| 10 | Disconnect | confirmation names file + entry; entry removed after | `09-disconnect.png` |
| 11 | Malformed config | "config unreadable" state, Connect control absent | `10-malformed.png` |
| 12 | Core down | waiting state; auto-populates when core starts (≤ 2 s poll) | `11-waiting.png` |
| 13 | Legacy path | dashboard connect control routes into this form (no preview-less native connect remains) | `12-dashboard.png` |
| 14 | VoiceOver | ⌘F5: list rows, preview text, and buttons announced; identifiers stable | note below |

## Results (executed)

**Status: RUN 2026-08-01 06:22–06:40** — macOS 26 / Darwin 25.5.0; app built from this branch (+ `MCPPROXY_SOCKET_PATH` override), isolated core `:18091` at `/tmp/mp91` with **`HOME=/tmp/mpqa-home`** so the user's real client configs were never touched. Seeded: cursor (entry present, stale port + secret header), windsurf (config, no entry), vscode (malformed JSON), gemini (user-authored entry with `user:pass@…?apikey=` URL), claude-code/codex/opencode (absent).

| # | Check | Result | Evidence |
|---|-------|--------|----------|
| 1 | Menu item | PASS — "Connect Client…" directly under "Add Server…" | menu listing |
| 2 | List states | PASS — Config present / No config found / bridge note for Claude Desktop; no TCC prompts on open | `shots/02-list.png` |
| 2b | No content reads on open | PASS (log-verified: aggregate is stat-only; detail+preview fire only on selection) | core log |
| 3 | SC-001 stopwatch | PASS — menu → connected in ~25 s, no browser | timed run |
| 4/5 | Preview (replace) | PASS — pending entry, path, **structural summary** (`Replacing existing entry "mcpproxy"`, Type, Endpoint `…:9999/mcp`, `Headers: X-API-Key`) — **no secret values**, backup statement, Advanced collapsed | `shots/04-preview-replace.png` |
| 7 | Credential notice | N/A this run — dev core had `require_mcp_auth` off, so the pending entry embedded no credential (correctly no notice) | — |
| 8 | Connect + refresh | PASS — file written, `mcp.json.bak.20260801-062951` created, green banner, row → Connected without reopening | `shots/07-connected.png` |
| 9 | Undo (session) | PASS — Undo appeared post-connect; restored the file **byte-exactly** (md5 `5e6ed766…` before == after) incl. the secret header | md5 diff |
| 9d | Non-create-capable absent | PASS — OpenCode shows the core refusal verbatim in red; **Connect control absent** (Close only) | `shots/08d-opencode.png` |
| 10 | Disconnect | PASS — confirmation names entry AND file before anything happens; Cancel wrote nothing (md5 unchanged) | `shots/09-disconnect.png` |
| 11 | Malformed config | PASS — row "Config unreadable", path shown, **Connect control absent** | `shots/10-malformed.png` |
| 14 | VoiceOver | WAIVED — user actively at the machine; AX identifiers unit-tested | — |

**Waived / not run:** 6 (create-case preview — covered by 9d's sibling path and unit tests), 9b (created-file undo — unit-tested), 9c (live conflict re-preview — unit-tested incl. masked-credential drift), 12 (core-down waiting state — unit-tested 2 s poll), 13 (dashboard routing — unit-tested `DashboardConnectControl` → shared presentation path).

**Observations (non-blocking):**
- The malformed detail pane shows the path and withholds Connect, but gives no inline "why". Row label carries it. Possible polish.
- `~/.config/opencode/opencode.json` and `~/.claude.json` **are** found correctly against a real home — verified separately (`exists=true` for claude-code, claude-desktop, cursor, codex, gemini, opencode). The "No config found" rows in these shots are an artifact of the deliberate scratch `HOME`, not a detection bug.

## Results (template)

**Status: NOT RUN.**

No live run has happened. The automated gates (T026) are green — linter,
`go test ./internal/...`, `swift test` (585 XCTest cases), `make swagger-verify`
— and the model-level guarantees they cover are asserted by unit tests against
synthesized API JSON, but every check in the table above requires the built app
driven by a human against a running core and none of them has been executed.
`verification/shots/` does not exist.

Automated coverage does NOT substitute for these checks. In particular, checks
2b (no TCC prompt on list), 3 (SC-001 stopwatch), 9/9b (live undo round-trip —
the live half of SC-003, see `baseline.md`), 9c (live conflict/re-preview) and
14 (VoiceOver) have no unit-test equivalent.

- Date / macOS version / app build: —
- SC-001 time: —
- Checks passed: 0 / 18 (none attempted)
- Waived: none
- Notes: run per the Setup section above before the feature is declared
  verified; record results by replacing this block.
