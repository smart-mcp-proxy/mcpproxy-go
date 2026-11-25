# PR 1 verification: visual system and shell

Checked on the PR head, 2026-11-25, macOS arm64.

| Check | Command | Result |
|---|---|---|
| Frontend unit tests | `cd frontend && npx vitest run` | 3187 passed |
| Type check | `cd frontend && npx vue-tsc --noEmit` | clean |
| Build | `cd frontend && npm run build`; `go build ./cmd/mcpproxy`; `go build -tags server ./cmd/mcpproxy` | ok |
| Playwright sweeps, with fixture upstream | `MCPPROXY_BASE_URL=http://127.0.0.1:18647 MCPPROXY_FIXTURE_PATH=<mcpfixture> ./scripts/run-web-smoke.sh` | 168 passed, 0 failed |
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
