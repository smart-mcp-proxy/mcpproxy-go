# Manual Verification Protocol — Native Connect Client Form (Spec 091)

Run on the built app (`scripts/build-swift-app.sh`) against a dev core
(isolated data dir + config, high port). Capture screenshots into
`specs/091-connect-client-form/verification/shots/`.

## Setup

1. Seed client configs in a scratch HOME (or point the core's client registry
   paths at scratch copies): one client with an mcpproxy entry (connected),
   one with a config but no entry, one with no config file, one with a
   deliberately malformed config (invalid JSON).
2. Start the core; start the app connected over the local socket.

## Checks

| # | Check | Expect | Screenshot |
|---|-------|--------|------------|
| 1 | Menu item | "Connect Client…" next to "Add Server…" | `01-menu.png` |
| 2 | List states | config present / no config found / unsupported-disabled rows | `02-list.png` |
| 2b | No content reads on open | run once against a REAL client location under App-Data TCC (e.g. actual Claude Desktop container): opening the list triggers no TCC prompt; alternatively verify via core logs that zero config-content reads occur on list | `02b-tcc.png` or log excerpt |
| 3 | SC-001 stopwatch | menu → connected client < 30 s, no browser | note time below |
| 4 | Preview (add) | entry text + path + timestamped-backup statement, before any Connect control | `03-preview-add.png` |
| 5 | Preview (replace) | existing entry AND replacement both visible | `04-preview-replace.png` |
| 6 | Preview (create) | "file will be created… Undo removes it" statement | `05-preview-create.png` |
| 7 | Credential notice | shown when entry embeds the admin credential | `06-credential.png` |
| 8 | Connect + refresh | row flips to connected without reopening | `07-connected.png` |
| 9 | Undo (session) | Undo visible after connect; restores config byte-exact (diff the file); gone after form reopen | `08-undo.png` |
| 9b | Undo (created file) | for the no-config client: connect creates the file, Undo removes it (verify file absent afterwards) | `08b-undo-create.png` |
| 9c | Conflict precondition | edit the entry externally between preview and Connect: core rejects with conflict, form re-previews | `08c-conflict.png` |
| 10 | Disconnect | confirmation names file + entry; entry removed after | `09-disconnect.png` |
| 11 | Malformed config | "config unreadable" state, Connect disabled | `10-malformed.png` |
| 12 | Core down | waiting state; auto-populates when core starts (≤ 2 s poll) | `11-waiting.png` |
| 13 | Legacy path | dashboard connect control routes into this form (no preview-less native connect remains) | `12-dashboard.png` |
| 14 | VoiceOver | ⌘F5: list rows, preview text, and buttons announced; identifiers stable | note below |

## Results

- Date / macOS version / app build:
- SC-001 time:
- Checks passed:
- Waived:
- Notes:
