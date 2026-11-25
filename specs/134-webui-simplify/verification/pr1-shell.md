# PR 1 verification: visual system and shell

Checked on the PR head, 2026-11-25, macOS arm64.

| Check | Command | Result |
|---|---|---|
| Frontend unit tests | `cd frontend && npx vitest run` | 3198 passed |
| Type check | `cd frontend && npx vue-tsc --noEmit` | clean |
| Build | `cd frontend && npm run build`; `go build ./cmd/mcpproxy`; `go build -tags server ./cmd/mcpproxy` | ok |
| Playwright sweeps, with fixture upstream | `MCPPROXY_BASE_URL=http://127.0.0.1:18647 MCPPROXY_FIXTURE_PATH=<mcpfixture> ./scripts/run-web-smoke.sh` | 169 passed, 0 failed |
| Playwright sweeps, no fixture | same without `MCPPROXY_FIXTURE_PATH` | 96 passed, 73 skipped (need the fixture), 0 failed |

New guards:

- `frontend/tests/unit/status-pill.spec.ts`: the eight Spec 109 statuses print their label in their tone; unknown raw and neutral; missing renders nothing.
- `frontend/tests/unit/no-emoji-chrome.spec.ts`: no emoji in any `.vue`/`.ts` under `frontend/src` (comments excluded) and none in the activity icon maps.
- `frontend/tests/unit/design-tokens-contrast.spec.ts`: tokens exist in `corporate` and `dark`, radius 4 px, the same brand-blue primary in both, every status tone and brand text at AA on its soft background and on base-100/200/300; no text below 12 px.
- `frontend/tests/unit/route-links-resolve.spec.ts`: every `{ name: … }` navigation target is a registered route (FR-015).
- `frontend/tests/unit/{shell-header,skip-link-palette,sidebar-phone-overlay,app-footer,view-as}.spec.ts`.
- `e2e/web-ui-sweep/shell-pr1.spec.ts`: first Tab on Skip to content, Enter focuses main; Tab never opens the palette, its button, Ctrl+K and `/` do; phone menu button, Escape and focus return, inert when closed, focus stays in the open menu; footer disclosure at 390 px; Add server at 1440 and 390 px by keyboard; theme menu by keyboard; View as only in page filter rows; no sideways scroll at 390 px on nine routes; no shell link reaches the not-found page.
- `e2e/web-ui-sweep/visual-a11y-sweep.spec.ts`: the AA contrast and accessible-name sweep now also covers `/review` and `/secrets` (both themes, three widths).

Not covered here: `/usage` (pre-existing `opacity-50` captions below AA, PR 7) and `/settings` (pre-existing unnamed toggles, PR 8) were tried in the contrast/name sweep and fail on page-body text this PR does not touch.

## Visual review fixes (fleet of 13 servers: 8 online, 2 held for review, 2 broken, 1 disabled)

Screenshots in `pr1-shots/`, Servers page, light (`corporate`) and dark, 1440 px and 390 px, before and after. I opened every one of them.

| Defect (before) | After |
|---|---|
| Outline buttons, chips and badges had a heavy dark border and badges lost their padding. Cause: the colour token was named `--border`, which daisyUI 5 reads as the border *width*. | Token renamed `--ds-border` (a unit test fails if `--border` is ever redefined). Neutral outline buttons and chips: 1 px `--ds-border`, `base-100` background, `base-content` text, radius 4, in both themes. Count badges are pills again. |
| Active sidebar item was a solid black block; its count badge unreadable. | `brand-soft` background, `brand-text` label, semibold. The contrast test covers brand-text on brand-soft and both badge kinds on it. |
| 390 px: the header search button was covered by the online indicator. | Menu, logo, search, online, attention and both Add controls are 44 px tall, fully visible and do not overlap (`shell-pr1.spec.ts` checks bounding boxes and what paints on each centre). |
| 390 px: two near-identical "+" buttons. | Add server keeps "+" (accessible name "Add server"); the Add menu shows a caret (accessible name "Add"). |
| 390 px: the footer version wrapped and squeezed "Project links". | Summary shows the release (`v0.69.1`); the full build string is inside the disclosure. |

Seen but not in PR 1 scope: at 390 px the Servers page header's "Scan All" button sits partly past the right edge (page body, unchanged by this PR, PR 2).
