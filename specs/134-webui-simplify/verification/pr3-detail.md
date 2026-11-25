# PR 3 verification: server detail, review and skills

Checked on the PR head, 2026-11-25, macOS arm64. Isolated instances only (`scripts/run-web-smoke.sh` on `127.0.0.1:18080`, `scripts/run-skills-tab-sweep.sh` on free ports, both with scratch data dirs and throwaway keys); `~/.mcpproxy` and port 8080 untouched.

| Check | Command | Result |
|---|---|---|
| Frontend unit tests | `cd frontend && npx vitest run` | 3211 passed |
| Type check | `cd frontend && npx vue-tsc --noEmit` | clean |
| Build | `npm run build`; `go build ./cmd/mcpproxy`; `go build -tags server ./cmd/mcpproxy` | ok |
| Playwright sweeps with fixture upstream | `MCPPROXY_FIXTURE_PATH=<mcpfixture> ./scripts/run-web-smoke.sh` | 182 passed, 0 failed, 3 skipped (need a client or a skill fixture) |
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

- No human visual review of screenshots beyond the sweeps' assertions (contrast, overflow, 44 px, keyboard); no screenshot attached.
- A skill row's "Unblock" opens the card where the guarded Unblock button is; there is no write from the table.
- The server edition build was compiled; the detail page is personal-edition only, so no server-edition sweep was run.

## Cross-model reviews

Reviewer `github-copilot/gpt-6.2-astra` (high), diffed against the merge-base `98dd89738`, three chunks:

- Detail tabs: 10 findings. Fixed: direct `?tab=security` focus before the server loaded; Retry scan racing a second status read (the report is loaded only on tab entry, as the Security tab click did); Edit URL primary action now opens the endpoint editor in place; Actions menu clipped at 390 px; Review link did not move focus; scan part did not refresh on Scans; quarantined servers with scan warnings got no summary line; "Next step" focus target; the Health tile repeated the summary. Not changed: query-only in-page navigation not re-read (`?tab=` writes the URL but is never read back; present before this PR).
- Review screen: 4 findings, all fixed (failure plus captured both reported; trusted-server fetch failure keeps the error panel with Retry; newest attempt only reports; a stale held-tool link leaves the operator's filters alone).
- Skills: 1 finding, fixed (a long unbroken word could widen the stacked table at 390 px).

Reviewer `github-copilot/gpt-6.2-sol` (high), approval paths only: neither the header primary action (approve opens the review part) nor the skills table row action (including Unblock) writes anything. Two observations, not changed: the review screen now stays mounted while the scan part is shown, so it keeps its review-change listeners (before, returning from Security remounted it); a tenant could already reach the review screen through `?tab=review` before this PR, and the backend answers 403.
