# PR 3 verification: server detail, review and skills

Checked on the PR head, 2026-11-25, macOS arm64. Isolated instances only (`scripts/run-web-smoke.sh` on `127.0.0.1:18080`, `scripts/run-skills-tab-sweep.sh` on free ports, both with scratch data dirs and throwaway keys); `~/.mcpproxy` and port 8080 untouched.

| Check | Command | Result |
|---|---|---|
| Frontend unit tests | `cd frontend && npx vitest run` | 3265 passed |
| Type check | `cd frontend && npx vue-tsc --noEmit` | clean |
| Build | `npm run build`; `go build ./cmd/mcpproxy`; `go build -tags server ./cmd/mcpproxy` | ok |
| Playwright sweeps with fixture upstream | `MCPPROXY_FIXTURE_PATH=<mcpfixture> ./scripts/run-web-smoke.sh` | 183 passed, 0 failed, 3 skipped (need a client or a skill fixture) |
| Skills sweep (Spec 125 scenarios + the new 390 px test) | `MCPPROXY_BINARY_PATH=./mcpproxy ./scripts/run-skills-tab-sweep.sh` | 12 passed |

## What the new tests cover

- `tests/unit/review-screen-fetch-primary.spec.ts` (SC-009, FR-032): not captured, the only primary button is the fetch attempt and the approve button is secondary and opens a confirmation that names the server; captured, the counted approve is primary and no fetch button shows; a failed attempt, a disabled server and a zero-tool result are reported on `review-fetch-outcome`, never replace the screen with the error panel, never approve; the branch follows the reloaded `definitions_captured` only.
- `tests/unit/server-detail-pr3.spec.ts`: pill label and one-line reason, primary action from `health.actions[0]` (none when empty; approve opens the review part and never approves), Actions menu keeps every action, facts on tools, logs, config and review tabs, the full untruncated diagnostic and raw error with focus return, five tabs and no Security tab, `?tab=review` and `?tab=security` land on their part, `?tool=` keeps its precedence, Scan Now still starts a scan, FR-035 summary lines reveal and focus a held tool even when a filter hid it, and open the scan part for a failed scan.
- `tests/unit/skills-utils.spec.ts`: `skillStateTone` (FR-037) and `skillRowAction` (FR-036).
- `e2e/web-ui-sweep/visual-a11y-sweep.spec.ts`: server detail on all five tab URLs, both themes: AA contrast; no sideways scroll at 1440, 820 and 390 px; every body button at least 44 px at 390 px; Scans link, Enter, focus on the scan part; Full diagnostic from the keyboard, Escape returns focus.
- `e2e/web-ui-sweep/skills-tab.spec.ts`: at 390 px the skills table has one button per row, six facts per row, no sideways scroll, 44 px buttons in the table and the open card.

## Changed existing tests (element moved, same function reached)

- `security-tab-ungate.spec.ts`, `server-detail-quarantine-tools-empty.spec.ts`, `server-detail-tab-url.spec.ts`, `web-ui-sweep.spec.ts`: `security-tab` became `review-tab` ("Review and scans"). `security-tab-dot` is unchanged.
- `server-detail-health-status-colour.spec.ts`: the header badge is the status pill (tone `review`, text "Needs review"); the summary is the one-line reason.
- `skills-panel.spec.ts`: the row text assertions read the table row (`skill-tr-<path>`); `skill-row-<path>` is the row's action button.

## Fix found by the sweep

The Trust mode selector note (`opacity-60`, 4.43:1) on the Configuration tab failed AA in the corporate theme; it is now `opacity-75`.

## Not covered

- Pre-existing solid-colour chips in `ScanHistory` ("completed") and the hold-evidence badge in dark are not restyled here.
- A skill row's "Unblock" opens the card where the guarded Unblock button is; there is no write from the table.
- The server edition build was compiled; the detail page is personal-edition only, so no server-edition sweep was run.

## Cross-model reviews

Reviewer `github-copilot/gpt-6.2-astra` (high), diffed against the merge-base `98dd89738`, three chunks:

- Detail tabs: 10 findings. Fixed: direct `?tab=security` focus before the server loaded; Retry scan racing a second status read (the report is loaded only on tab entry, as the Security tab click did); Edit URL primary action now opens the endpoint editor in place; Actions menu clipped at 390 px; Review link did not move focus; scan part did not refresh on Scans; quarantined servers with scan warnings got no summary line; "Next step" focus target; the Health tile repeated the summary. Not changed: query-only in-page navigation not re-read (`?tab=` writes the URL but is never read back; present before this PR).
- Review screen: 4 findings, all fixed (failure plus captured both reported; trusted-server fetch failure keeps the error panel with Retry; newest attempt only reports; a stale held-tool link leaves the operator's filters alone).
- Skills: 1 finding, fixed (a long unbroken word could widen the stacked table at 390 px).

Reviewer `github-copilot/gpt-6.2-sol` (high), approval paths only: neither the header primary action (approve opens the review part) nor the skills table row action (including Unblock) writes anything. Two observations, not changed: the review screen now stays mounted while the scan part is shown, so it keeps its review-change listeners (before, returning from Security remounted it); a tenant could already reach the review screen through `?tab=review` before this PR, and the backend answers 403.

## Visual review (screenshots)

Seeded isolated instance (`127.0.0.1:18850`, scratch data dir): the skills fixture with three pending skills, a trusted server with a held tool, a quarantined server with captured definitions (`fx-ok`), a quarantined server without (`fx-quarantined`), a disabled quarantined server and a broken one. Playwright full-page captures at 1440 light, 1440 dark and 390 are in `verification/pr3-screenshots/` (`<page>-light|dark|m.png`): tools, review, security, logs, config, skills, skillcard, quar-captured, quar-notcaptured.

Fixed after looking (follow-up request from the design session):
- The four stat cards are gone (audit A3). Each card's facts moved into the facts row (inventory row P3-07): tool count and disabled count, admin state with "set by you" / "awaiting your review", transport with the endpoint once (the subtitle line is removed); the Health word and tone are the header pill. The cards had no click target or tooltip.
- `Review | Scans` is a quiet segmented anchor nav with a 1 px `--border` border; both anchors and the `?tab=` landing are unchanged.
- The scan result banner and the other solid yellow blocks on these pages (quarantine banner, Tool Quarantine, "definitions not captured", held-change) use the soft status tones with a 1 px border; AA passes in both themes. The quarantine banner no longer squeezes its text into a narrow column at 390 px.
- Disable All is an outline button. It has no confirmation today and none was added (R2).
- The facts row is a bordered card on a 1/2/3 column grid; the Skills table narrows to skill, state and action beside an open card (the other three facts are in the card), and is full width otherwise.
- Admin state "Enabled" no longer renders magenta in dark; the scan and review parts no longer show a browser focus outline after an anchor jump.

Left to the PR 1 session (shared styles, per the request): the 1 px outline buttons and chips (the heavy black outline buttons such as Full diagnostic and Actions are the current shared `btn-outline`), the sidebar active item. Merge `claude/134-webui-pr1-shell-f30c09` when it reports.

## After merging the PR 1 shared-style fixes (`ad6a819ac`)

`claude/134-webui-pr1-shell-f30c09` is merged in; my uses of the border colour are `--ds-border` (the token was renamed because daisyUI 5 reads `--border` as a width). Screenshots retaken from the same seeded instance and replaced in `pr3-screenshots/`. Fixed after the second look: the banner buttons ("Review & approve", "Review all tools") were solid yellow, now outline (the header Review is the page's one primary); the hold-evidence verdict chip in dark was invisible (neutral outline badge), now plain text colour. Dialog confirm buttons stay `btn-warning` (they confirm a risky action).

Seen, not in this PR: `ScanHistory` status chips ("completed") are solid saturated green with poor contrast, on the review screen and the Review queue (belongs to the Review queue page work); the review screen's tier and verdict chips use `badge` styles of their own (Review page).

## Error-state review (second visual review)

Seeded states added: Error (`errsrv`, exits before initialize), Needs review (`fx-quarantined`), Connecting (`slowsrv`) and Disabled (`offsrv`); screenshots `state-error|needsreview|connecting|disabled-light|dark|m.png` in `pr3-screenshots/` (after only; no "before" set was kept).

- The diagnostic panel (`ErrorPanel.vue`) uses the soft tones: soft background, 1 px tone border, body text in the base colour, heading and severity tag in the tone, the error code plain monospace. Error uses error tones, warning and the reconnecting state review tones, info busy tones.
- The reason beside the pill is `serverReason()` from PR 2 (`utils/serverReason.ts`, `tests/unit/server-reason.spec.ts`), copied unchanged from `claude/134-webui-pr2-servers-011f39` together with the two files it needs, `utils/serversList.ts` and `tests/unit/servers-list.spec.ts` (unchanged). For a quarantined server, whose connection fault is shown as by design, the reason is the core's summary. The raw error stays in the Full diagnostic.
- The launch line (or URL) is a full-width monospace row with Copy under the facts; Transport reads "stdio, not connected".
- "How to fix this" replaces the fix-steps label; the activity link reads "Last 24 h: N calls, M errors" (the same `activity/summary` the Servers list reads) and "Calls in Activity" until loaded.
- At 390 px the diagnostic panel no longer overflows ("Hide steps" and the docs URL wrap); the breadcrumb's last item is no longer clipped.
