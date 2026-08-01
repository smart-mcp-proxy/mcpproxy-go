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

**Status: RUN 2026-08-01 06:06–06:20** (macOS 26 / Darwin 25.5.0, branch build at `d6eaa29e9`+socket-override, isolated dev core `:18090` at /tmp/mp90, seeded: 19× reasoned burst, transport-error record, quarantine-blocked record, 3 client sessions aged overnight). Automated gates in [gates.md](gates.md).

- Date / macOS version / app build: 2026-08-01, macOS 26, dev build launched with the `MCPPROXY_SOCKET_PATH` override against the scratch core
- Checks passed: **1** (order: summary → Activity (24h) → Recent → Clients, `shots/01-order.png`); **2** (grouped `×20`, `×9→×10`, `×2` rows, `shots/02-grouped.png`); **3** (reason subtitles on every reasoned row incl. live-SSE rows); **4** (success rows icon-free); **5** (failed row: red ⊗ + error clause in title); **6** (blocked row: orange ⚠ triangle, shape-distinct, block reason as subtitle); **7** (tooltip = full label + full reason + full error, `shots/06-tooltip.png`); **8/9/10** (tri-state in one frame: active = filled green no age, idle = half-fill "idle 11m", seen = hollow "seen 8h", `shots/07-clients.png`); **11** (summary "9 calls this hour · 1 active · 1 idle" — counts by state, seen excluded, empty states omitted); **14** (menu held open during live traffic: top row updated IN PLACE ×9→×10, age 1s, subtitle swapped, zero structural change, `shots/08-stability-a.png`; the full 35 s hold was cut short by the user's own click in another window — expected NSMenu dismissal, not a defect)
- Waived: **11b** (6+ clients summary universe — unit-covered by GlancePresenceTests, not seeded live); **12** (macOS 13 fallback — no macOS 13 machine; availability-gated path unit-tested); **13** (full VoiceOver walk — waived while the user was actively at the machine; AX labels unit-tested, keyboard row-highlight observed live in `shots/06-tooltip.png`)
- Notes:
  - After overnight App Nap under the lock screen, the first menu open rendered pre-sleep data without the "not updating" marker; one 30 s poll after wake fully caught up. The marker requires 3 consecutive FAILED polls by design; napped polls aren't failures. Acceptable; possible future refinement.
  - Pre-existing core observations (not spec-090 regressions, for follow-up): tool-result `isError:true` responses record as `status=success` (only transport-level failures record `error`); intent-variant conflicts are not blocked in a default config (the export's "Intent rejected" blocks came from policy config this scratch core lacks) — quarantine blocks were used to exercise the blocked path.
