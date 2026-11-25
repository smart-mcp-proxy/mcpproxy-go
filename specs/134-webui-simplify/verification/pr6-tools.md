# PR 6 verification: Tools

Checked on the PR head, 2026-11-25, macOS arm64. Stacked on #2049 (PR 1); PR 1's parts are used as they are.

| Check | Command | Result |
|---|---|---|
| Frontend unit tests | `cd frontend && npx vitest run` | 3,253 passed (364 files) |
| Type check | `cd frontend && npx vue-tsc --noEmit` | clean |
| Build | `cd frontend && npm run build`; `go build ./cmd/mcpproxy`; `go build -tags server ./cmd/mcpproxy` | ok |
| Playwright sweeps, with fixture upstream | `MCPPROXY_FIXTURE_PATH=<mcpfixture> ./scripts/run-web-smoke.sh` | Full launcher run: 175 passed, 3 failed, 3 skipped; the 3 failures were mistakes in the new `tools-pr6.spec.ts` (a header locator, a focus-trap loop, a layout reset on reload), fixed, and the spec then passed 8 of 8 against a scratch core (`playwright test tools-pr6.spec.ts`). Every other spec passed. The spec had first been left out of the launcher list, so earlier runs did not include it; it is in now, with the docs the release-gate guard requires. |

## What the tests guard

Inventory section "Tools (PR 6)" in `function-inventory.md` (P6-01 to P6-31, plus rows 44 and 63 to 69) names the test of every row.

- `tests/unit/tools-exceptions.spec.ts`: every exception label (Writes, Destructive, Unannotated, New and Changed needs review, Disabled, Denied by config), none for an ordinary tool; section order not usable, usable, disabled, then name; sections come only from the rows given.
- `tools-grouped.spec.ts`: sections, header (server link, status pill, returned count, "n on this page"), no pill without health, collapse defaults and toggle by button, a search reopens a section the user closed, unique toggle ids, review links on the labels, Only exceptions, statistics and the Needs review link, partial warning, empty state, paging.
- `tools-layout.spec.ts`: grouped default, choice stored, Flat list keeps the nine columns and all seven sort keys in both directions.
- `tools-details.spec.ts`: Details shows description, server link, tier, state, usage and Calls link, last used or "Never used", approval with Held cue, Review link and hold evidence as text, annotations, input schema; "—" for no approval status; config-denied versus disabled; native dialog, Escape returns focus; a flat row and a sort header work from the keyboard.
- `tools-more-filters.spec.ts`: Status, Tier and Approval behind "More filters" with `status`, `tier`, `risk`, `approval` links; the panel opens by itself, also on a same-page link; Only exceptions is a shortcut that does not replace them.
- `tools-batch-grouped.spec.ts`: select-all selects the page including collapsed sections and says so; Approve reaches collapsed tools, one call per server; Reject and Approve stay disabled for an all-approved selection; Enable, Disable, Clear; "N tools selected on M servers (K on other pages)".
- `tools-view-as-grouped.spec.ts`: View as under grouped: select, banner, not-callable note, Details verdict and Why?, read-only selection, Callable filter under More filters with a chip and Clear Filters.
- Existing Tools specs that read the flat table now set the stored layout to flat (one line each); `tools-approval-badge-nowrap.spec.ts` also reads `ToolDetails.vue`, where the badge moved.
- `e2e/web-ui-sweep/tools-pr6.spec.ts` (grouped default and labels, Details by keyboard with Tab kept inside and Escape returning focus, Only exceptions, More filters, `?risk=` link, Flat list by keyboard and all sort keys, selection across a collapsed section, 390 px: no sideways scroll, 44 px buttons, stacked rows) and case 1c of `profiles-scope.spec.ts` (grouped View as: Details, Why?, real Escape twice). The older View as cases in `profiles-scope.spec.ts` set the flat layout and still run.

## Visual review (fleet of 34 servers: 27 online, 3 awaiting review, 4 disabled; tool list extended through a request route with all tiers, held and changed tools, 30 tools on one server)

Screenshots in `pr6-shots/` (light `corporate`, dark, and 390 px; `-m` is the phone). I opened every one of them.

| Defect found in review | Fix |
|---|---|
| Name chip filled its whole column (a wide grey block) | Chip is as wide as the name, the column keeps a fixed width |
| Layout switch drew "Grouped" as a solid primary button | Both are outline buttons; the pressed one takes the shared pressed style |
| 390 px: the four statistics scrolled sideways ("Needs revi") | Two columns on a phone |
| Batch bar scrolled out of view after selecting far down a long list | Sticky at the top of the page body |
| Flat table at 1440 px with the sidebar: description column took the free width and pushed Last Used out | Description column capped at 16 rem |
| Details dialog badges were solid saturated blocks | Soft tones (`StateTag`) |

## Not covered

- Server edition (`-tags server`) builds, but the Tools page was not opened in a server-edition core.
- Page order follows live server health, so a health change between two clicks on Next can reorder sections across a page boundary (the sections of one page always match the ordered list at the time it renders).
- Pre-existing, unchanged: in the flat table at phone width under an administrator View as, the Tier and Last Used columns are hidden below `lg`, so their sort keys are not reachable there; use the grouped list or a wider window.
