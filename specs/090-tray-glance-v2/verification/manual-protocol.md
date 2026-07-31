# Manual Visual Verification Protocol — Tray Glance v2 (Spec 090)

Automated tests assert model-level encodings only. This protocol verifies what
the user actually sees. Run it on the built app (`scripts/build-swift-app.sh`)
against a live core seeded with the reference fixture, on macOS 14.4+ (primary)
and macOS 13 (fallback rendering, if a machine is available — otherwise waive
and note it).

Capture every screenshot into `specs/090-tray-glance-v2/verification/shots/`
named as indicated. Use the mcpproxy-ui-test MCP (`screenshot_status_bar_menu`)
or a manual ⇧⌘4 window grab of the open menu.

## Setup

1. Start a dev core with an isolated data dir + config (high port 18xxx).
2. Replay traffic that produces, in order: a burst of ≥ 6 identical calls to
   one tool, single calls to two other tools, one failing call, one
   policy-blocked call (rug-pull/TPA harness or an intent-conflict call).
3. Seed sessions for all three presence states (simulate by editing seeded
   sessions' `last_activity` timestamps): one client active (< 5 min), one
   idle (5–30 min), one seen (> 30 min but < 24 h).
4. For check 11b, seed at least six distinct qualifying clients (distinct
   name+version, all with last activity < 30 min) so qualifying clients
   outnumber the five displayed rows.

## Checks

| # | Check | Expect | Screenshot |
|---|-------|--------|------------|
| 1 | Block order | summary → Activity (24h) → Recent → Clients | `01-order.png` |
| 2 | Grouped row | one row with "×N" suffix; other tools visible below | `02-grouped.png` |
| 3 | Reason subtitle (14.4+) | subdued second line under the label; ellipsis if long | `03-reason.png` |
| 4 | Success rows | NO status icon on successful rows | `03-reason.png` (same shot) |
| 5 | Failed row | red mark + first error clause on title line; reason still the subtitle | `04-failed.png` |
| 6 | Blocked row | warning mark with a different shape than the failure mark; block reason as subtitle | `05-blocked.png` |
| 7 | Tooltip | hover a truncated row: full label + full reason (+ full error) | `06-tooltip.png` |
| 8 | Clients: active | filled indicator, no age | `07-clients.png` |
| 9 | Clients: idle | different fill/shape + "idle · Xm" age | `07-clients.png` |
| 10 | Clients: seen | quietest indicator + age | `07-clients.png` |
| 11 | Summary counts | "N active · M idle" matches the seeded states; "seen" clients excluded from counts | `01-order.png` |
| 11b | Summary universe | with 6+ qualifying clients seeded, summary counts exceed the 5 displayed rows | `09-universe.png` |
| 12 | macOS 13 fallback | rows single-line; reason present in tooltip only | `08-macos13.png` or "WAIVED: no macOS 13 machine" |
| 13 | VoiceOver | ⌘F5, arrow through the five rows: each announces label, outcome, reason (when present), age | note results below |
| 14 | Open-menu stability | keep menu open ≥ 35 s during live traffic: text updates in place, row count and heights never change | note results below |

## Results

- Date / macOS version / app build:
- Checks passed:
- Waived:
- Notes:
