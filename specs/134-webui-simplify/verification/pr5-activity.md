# PR 5 verification: Activity calls view

Checked on the PR head, 2026-11-25, macOS arm64. Isolated instance (own config, data dir, HOME, port 19041, stopped by pid) with 2 fixture servers and 62 seeded calls through `/mcp`: repeated runs (x14, x12, x10, x8, x4, x3), 4 failed calls, 2 blocked attempts, one call with sensitive data. Screenshots in `pr5-shots/` (opened and checked).

| Check | Command | Result |
|---|---|---|
| Frontend unit tests | `cd frontend && npx vitest run` | 3225 passed |
| Type check | `npx vue-tsc --noEmit` | clean |
| Build | `npm run build`; `go build ./cmd/mcpproxy`; `go build -tags server ./cmd/mcpproxy` | ok |
| Standing sweep with fixture | `MCPPROXY_BASE_URL=http://127.0.0.1:19061 MCPPROXY_FIXTURE_PATH=<mcpfixture> ./scripts/run-web-smoke.sh` | 179 passed, 0 failed, 2 skipped |
| New Activity sweep | `activity-pr5.spec.ts` | 9 passed |
| Release-gate spec list guard | `go test ./cmd/release-gate/` | ok |

## SC-006 (Activity part)

`activity-pr5.spec.ts` "1280 px": headers are exactly Time, Tool, Result, Client, Duration, Open details; the table wrapper has `scrollWidth - clientWidth <= 0`, the page has no sideways scroll, and no cell clips its text (a long `beta-long-server-name:echo` wraps instead of being cut). Screenshot `calls-1280-light.png`.

## What is verified, by requirement

- FR-045: five data columns and one action column (calls only; System and All keep their columns, test `View=all`); tool as `server:tool`; fallbacks (`unattributed`, dash for events, type label for tool-less non-call rows, action text, preflight and credential summaries); quiet success with sr-only "Success"; Error, Blocked and raw statuses keep their label; a raw 45-character status cannot widen the table (fixed layout, truncated pill with title).
- FR-046: Sort control, ten options, five keys, keyboard (`selectOption` on the focused select); grouping disabled outside time order; unit tests check full server order ascending and descending.
- FR-047: popover opens over the results (first row y unchanged, tested at 1440); results card top is identical with no filter, one chip and an empty result; Escape closes it and returns focus; outside click and Tab-out dismiss it; at 390 px six long filters overflow into "All N filters", every one removable; body buttons all >= 44 px; only the chip row scrolls sideways.
- FR-048: Group repeats on by default, stored off respected, run summary (span, duration range, reasons cue), expansion of all members by id, off outside time order.
- FR-049: row detail holds Type, Details, Server link, Attribution (always, `unattributed` and dash fallbacks included), Sensitive data with the detection count, Intent, Why. Focus enters the panel on open (retries until the drawer is visible) and returns to the row button on close.
- Sessions view keeps the panel, the disabled-filter chips, and now also the Filters button with its popover (the token setter was reachable there before). `/sessions` redirect still resolves.
- Contrast: `visual-a11y-sweep` `/activity` (both themes) passes; the run-span text no longer uses `opacity-70`.
- 390 px: no sideways scroll (document and table wrapper), full-width detail panel (`detail-390-light.png`).

## Cross-model review (opencode `gpt-6.2-astra`, high), three chunks against merge-base `ad6a819ac`

Round 1 (table and detail), round 1 (filters), round 1 (grouping and sort). Every finding was checked against the code first. Fixed: long raw status pushing the action off at 390 px (fixed layout); lost numeric detection count (shown in the row mark and the detail); metadata-only `code_execution` parent lost its name; keyboard path to the links that moved into the detail (focus now enters the panel, returns on close); Sessions lost the Token setter; overflow list hidden under the popover; Tab leaving the popover; Escape closing the popover under the access explainer; outside click overwriting the stored choice; overflow Escape and focus after removal; "No filters applied" beside a scope chip; quarantine batch summary had no title; the reasons cue now says how to reach the reasons; weakened intent test restored; vacuous sort and expansion assertions replaced. Not changed: the chip row scrolls sideways at 390 px (the lead's rule allows it); a run's duration range is hidden at 390 px with the Duration column (also true before; the detail shows the lead's duration).
Round 2: not run (all chunks answered; fixes verified by tests and the sweep).

## Not verified

- Server edition pages (`/my`, `/admin`) and a tenant principal: the tenant path is unchanged code (export hidden, `GET /user/activity`), covered by the existing tenant unit tests only.
- macOS app and tray: untouched.
- 1440 dark popover and 1440 light screenshots were checked; 1280 dark was not.
- One earlier full-suite vitest run showed a single failure in `activity-table-scanning` that passed on 3 reruns and on later full runs; not reproduced.
