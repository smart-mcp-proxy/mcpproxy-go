# PR 2 verification: Servers table

Checked on the PR head, 2026-11-25, macOS arm64, an isolated instance (own config and data dir under a scratch directory, `HOME` in a scratch directory, listen `127.0.0.1:18734` and `:18735`, throwaway API key; `~/.mcpproxy` and port 8080 untouched).

| Check | Command | Result |
|---|---|---|
| Frontend unit tests | `cd frontend && npx vitest run` | 3256 passed (362 files) |
| Type check | `cd frontend && npx vue-tsc --noEmit` | clean |
| Build | `make frontend-build`; `go build -o mcpproxy ./cmd/mcpproxy`; `go build -tags server ./cmd/mcpproxy` | ok |
| Release gate Go tests | `go test ./cmd/release-gate/` | ok (the Web UI row is derived from the status pill) |
| Playwright sweeps, with fixture upstream | `MCPPROXY_BASE_URL=http://127.0.0.1:18734 MCPPROXY_FIXTURE_PATH=<mcpfixture> HOME=<scratch> ./scripts/run-web-smoke.sh` | 179 passed, 3 skipped, 0 failed |
| Playwright sweeps, no fixture | same on `:18735` without `MCPPROXY_FIXTURE_PATH` | 109 passed, 73 skipped (need the fixture), 0 failed |

One earlier full run showed `shell-pr1.spec.ts` "Tab through the header never opens the palette ... Ctrl+K" failing once (`Control+k` did not open the palette); it passed on the three later full runs and alone. It is a PR 1 spec and was not touched here.

## SC-001 and FR-026 (measured)

- **SC-001**, 34 servers (28 usable, 6 enabled and not usable: `error`, `sign_in_required`, `needs_secret`, `error`, `needs_review`, `connecting`), 1440x900, "All" chip: **14 whole rows** above the page footer, all 6 unusable servers among them, as the first 6 rows (today the card grid shows 6 cards). Row height 40 px. Screenshot: [pr2-servers-light-1440.png](pr2-servers-light-1440.png), [pr2-servers-dark-1440.png](pr2-servers-dark-1440.png).
- **FR-026**, 300 servers: first row visible **264 to 293 ms** after navigation on this machine (limit 2 s on the CI runner); a scripted scroll of the page's scroll container (`<main>`) collects exactly the 300 fixture ids. The list is not windowed (300 rows render in well under the limit), so nothing was windowed.
- 390x844: rows stack (name and status, reason, tools and last call, action and more), no sideways scroll, every visible body control and every opened menu item and dialog button at least 44 px. Screenshot: [pr2-servers-dark-390.png](pr2-servers-dark-390.png).

## Guards added or changed

- `frontend/tests/unit/server-reason.spec.ts`, `servers-list.spec.ts`: the reason order (FR-022: classified code or known pattern, diagnostic message, summary, root cause, status label; a classified code beats free text; no invented words) and the total order, chips, legacy `status`, bare-visit defaults (FR-023, FR-024).
- `servers-page.spec.ts`, `server-row.spec.ts`, `server-facts.spec.ts`: chips and counts, More filters, URL writes, totals, search, Refresh, Scan All, states, the shared clock, the row, the delete dialog, the facts on server detail.
- Ported, not deleted: every `ServerCard` spec now mounts `ServerRow` (menu, primary action, OAuth sign-in, status chip, review and error lines, approve navigation); the shield, scan verdict and held-tool specs mount `ServerFacts`; `servers-profile-scope` and `profiles-unhide` use the View as control.
- `e2e/web-ui-sweep/servers-pr2.spec.ts` (added to `scripts/run-web-smoke.sh`): SC-001, FR-026, rows, keyboard path to a menu item, filters, phone layout and targets, the phone menu and delete dialog, light and dark at 1440 and 390. `navigation-consistency.spec.ts` card-height tests became row-height and action-cell tests; `profiles-unhide.spec.ts` and `surface-snapshot.mjs` follow the new markup.
- `cmd/release-gate`: the Web UI row expectation follows the status pill (tone and Spec 109 label when `health.status` is present, legacy chip otherwise).
- Contrast: `visual-a11y-sweep.spec.ts` walks `/servers` in both themes and passed.

## Cross-model review (opencode `gpt-6.2-astra`, high), 4 rounds, diff against the PR 1 tip

Round 1 found: pattern order in the reason, unusable-but-connected wording and sorting, the 768 to 1200 px column budget, the full diagnostic missing the code, tooltip-only explanations, "online" in the totals line meaning transport-connected, the memo missing `trust_mode`. Round 2: lost test coverage (clock, shield name, facts on detail), the release gate and snapshot out of step, the 300-server scroll test not scrolling `<main>`, legacy admin-state colours, hints focus, 40 px and 24 px controls. Round 3: delete dialog focus, stale "Connected" reason after Disable, 44 px targets inside dialogs and hints, unknown status layout, fix steps and docs link in the full diagnostic, three inventory rows. Round 4: delete dialog focus on failure, fix-step links, the profile select disabled while profiles load, two inventory notes. Every finding was checked against the code before fixing; all were fixed except the items listed below. Round 4 ended with no open finding that changes a function's reach.

Left as is (recorded, not a function loss): going from `?status=connected` to `?status=bogus` inside the mounted page now shows All (the old page kept the stale local filter); a hover tooltip still names the scan-disabled reason, and the reason is also printed in the menu.

## Not verified

- No visual check of the server edition (`-tags server`) pages; they are not touched.
- The macOS app and tray are not touched.
- CI on the runner (the 2 s bound was measured locally).
