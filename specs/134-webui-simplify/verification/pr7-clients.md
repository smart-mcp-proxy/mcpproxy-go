# PR 7 verification: Clients, tokens, Usage

Checked on the PR head, 2026-11-26, macOS arm64. Stacked on #2049 (PR 1).

| Check | Command | Result |
|---|---|---|
| Frontend unit tests | `cd frontend && npx vitest run` | 3233 passed (after merging main) |
| Type check | `cd frontend && npx vue-tsc --noEmit` | clean |
| Build | `cd frontend && npm run build`; `go build ./cmd/mcpproxy`; `go build -tags server ./cmd/mcpproxy` | ok |
| Playwright sweeps with fixture upstream, own HOME, port 19294 | `./scripts/run-web-smoke.sh` | 173 passed, 3 failed, 4 did not run (the 3 are on Activity, below) |
| PR 7 spec alone, after the last header fix | `clients-tokens-usage-pr7.spec.ts` | 6 passed |

The 3 failures are `profiles-scope.spec.ts` "layout at 1024px" (activity table overflows its card by 30 px) and `visual-a11y-sweep.spec.ts` "contrast AA: /activity" in both themes ("· over 1m" caption at 3.45:1). `Activity.vue` is not in this PR's diff (`git diff ad6a819ac HEAD -- frontend/src/views/Activity.vue` is empty); they belong to PR 5. "4 did not run" are the rest of the serial group after the first failure.

## After merging main (PR 1 landed), 2026-11-26

Red CI before the merge, all from PR 1's lists and fixed by main: Server Edition (`TestProfilesV3ParityMatrixResolves`, `TestSpec109Traceability_AcceptanceIndex`), Unit Tests and the Windows amd64 build (`TestWebUISweepSpecListIsComplete`: `shell-pr1.spec.ts` undocumented). All pass locally after the merge, also with `-tags server`. The macOS and Linux build jobs ran 2 h 36 min and failed with no test output (queue backlog), not from this change.

Full sweep on the merged head with the PR 7 spec renamed `zz-` (below): **179 passed, 0 failed**. The earlier `profiles-scope` and `/activity` failures were this PR's own: `profiles-scope.spec.ts` "layout at 1024px: activity table at 1024 must fit its card (overflow 27px, columns Time:160 Server:70 Scope:176 Details:146 Status:89)" and `visual-a11y-sweep.spec.ts` "contrast AA: /activity": `3.45:1 span.opacity-70 rgb(135 137 151) on rgb(255 255 255) "· over 1m"` (corporate), `3.03:1 ... rgb(99 109 123) on rgb(29 35 42)` (dark). Proof, same fixture, isolated instance each: `origin/main` 169 passed, 0 failed; this branch without the PR 7 spec 173 passed, 0 failed; this branch with it 171-173 passed with those failures. Cause: the spec's Usage test seeds attributed activity (a client, a blocked call) that stays in the activity log, and Playwright runs files alphabetically, so those rows reached the later Activity specs. Fix: the spec is now `clients-tokens-usage-pr7.spec.ts` and runs last (a comment in the file says why).

Ctrl+K ("Tab through the header never opens the palette ... Ctrl+K and /", `shell-pr1.spec.ts`), 10 repeats alone: this branch 7 passed, 3 failed; `origin/main` 6 passed, 4 failed. It is flaky on main as well and not touched by this PR.

Sol rounds used on this PR: 4 (round 3 and 4 found only races in rapid history navigation; fixed: confirmations bound to the named credential, rotate previews numbered per opening, the panel kept and a queued open applied while a rotation or revoke is in flight). Not fixable in the UI: `revoke API key` takes the client id only, so a key replaced between the last page read and the click is caught by a key-prefix check at click time but not atomically. A key-bound revoke would need a backend change (recorded here, not made).

## SC-006, tokens part (1280 px)

Measured by `clients-tokens-usage-pr7.spec.ts`: the tokens table has `scrollWidth - clientWidth = 0` (974 px of 974 px at 1280, 1134 of 1134 at 1440), the document has no horizontal scroll, no `td` or `th` is wider than its box, and Regenerate and Revoke (and Delete for a revoked or expired token) are visible in the row. Kind, Mode, Prefix, Last used and the raw profile id sit in the row disclosure that the token name opens (a button with `aria-expanded`).

## What the new specs guard

- `frontend/tests/unit/clients-manage-panel.spec.ts`: one row and one button per client; reach wording; fold only of positively undetected supported apps, and a focused one revealed; the four groups in order with links and sessions on top and a closed "Disconnect or remove"; config path copy; connect and disconnect hand-off with no write started; `?focus=` (Clients tab only, unknown id requests nothing), `?focus=&move=1`; Escape, backdrop and close button, focus return; Forget with its confirmation and a result that stays after the row leaves; Escape inside a nested dialog; a fresh panel per client; active-session count; row click.
- `clients-reach.spec.ts`, `usage-chart-colors.spec.ts` (bar colours at least 3:1 and axis text AA on the card in both themes; no colour literal or `opacity-50/60` in `components/usage`).
- `e2e/web-ui-sweep/clients-tokens-usage-pr7.spec.ts`: 1280 and 390 px, keyboard.
- `visual-a11y-sweep.spec.ts` now also walks `/usage` and `/clients?tab=tokens` for contrast, names and layout, and the Manage panel on `/clients`.

## Visual review (`pr7-shots/`)

Opened at 1280 and 1440 light, 1440 dark and 390 px. Fixed after looking: duplicated profile, lock and expiry facts in the panel's Key group; 10 px badges in the panel (now 12 px); credential and rotation badges; table cell borders showing as lines in the 390 px card layout; the fold line had no chevron; dark-mode contrast of the neutral state tag (3.48:1, now the "off" tone); at 390 px the Agent tokens header clipped "Create Token" and the stats cards clipped (now wraps and stacks); the Tokens row repeated "Pinned to <profile>" beside the Profile column. Not shot: the connect preview and the open fold line (their code is unchanged or covered by unit tests).

## Cross-model review

- Astra (`github-copilot/gpt-6.2-astra`, high), Clients, then Tokens and Usage: 14 findings on Clients (profile and lock facts of a client with no bindable credential, fold ignoring browser logins, focus trap with a closed `<details>`, focus return for a folded app and after Forget, Disconnect offered for any supported row, the session count, the revoke row identity, wording) and 3 on Tokens (raw profile id, no visible labels at 390 px, a wrong tooltip). Fixed except: reach says `All servers` for a credential that cannot be bound and `—` for a custom client with only a browser login, which is what the page printed before; a client whose config cannot be read can still sit in the fold (the roster has no access state; reach to it is one click and the connect list shows the denial); the credential label and rotation badge appear in the profile line and again under Key (kept: one is the state, the other the detail). Usage: none.
- Sol (`github-copilot/gpt-6.2-sol`, high), credential paths, 2 rounds: found a retargetable API-key confirmation and a retargetable custom-rotation confirmation after browser Back, a dropped one-time secret when the panel closed mid-rotation, a panel left open across a tab or scope change, and revoke rows that did not say which grant. All fixed (panel keyed per client; confirmations dropped when the credential set changes; Cancel disabled while a rotation is in flight; panel closes with its tab and on a scope change; revoke rows and dialog show approved, last used and redirect host; test per fix). Not changed: opening the connect list from the panel runs the list's own onboarding read, the same read the header Connect dialog runs.
