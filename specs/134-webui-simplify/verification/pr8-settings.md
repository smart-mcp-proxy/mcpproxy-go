# PR 8 verification: Settings, profile editor action row, setup wizard

Checked on the PR head, 2026-11-26, macOS arm64, isolated instances on 127.0.0.1:19347 (sweep launcher) and 127.0.0.1:19348 (screenshots), own config and data dir.

| Check | Command | Result |
|---|---|---|
| Frontend unit tests | `cd frontend && npx vitest run` | all passed (360 files) |
| Type check | `cd frontend && npx vue-tsc --noEmit` | clean |
| Build | `make build` | ok |
| Playwright sweeps, with fixture upstream | `MCPPROXY_BASE_URL=http://127.0.0.1:19347 MCPPROXY_FIXTURE_PATH=<mcpfixture> ./scripts/run-web-smoke.sh` | first run 171 passed, 2 failed (bugs in the new `settings-pr8.spec.ts`: a hidden-panel locator and a disabled-button hit test, fixed); after the review fixes: 173 passed, 4 skipped, 0 failed |
| `settings-pr8.spec.ts` after the fix, against the rebuilt instance (it has a profile, so the profile editor test runs) | `playwright test settings-pr8.spec.ts` | 5 passed |

No Go file changed, so the golangci-lint commands were not run.

New guards: `settings-pr8-layout.spec.ts` (FR-050 default, labels, order, select, FR-051 help, FR-052 save row, review fixes), `profile-editor-action-row.spec.ts` (FR-052), `onboarding-wizard-pr8.spec.ts` (FR-053), `e2e/web-ui-sweep/settings-pr8.spec.ts`. Existing specs updated where behaviour the spec names changed (default section, wizard link, verify unknown row, hints/refusal assertions); none removed.

## Screenshots (`pr8-shots/`)

Settings (general, security, catalog, advanced, raw), profile editor and wizard (clients, servers, verify) at 1440 and 390 px, light and dark, each with a `-bottom` view scrolled to the end (Settings, profile editor). Document sideways overflow measured on every one: 0. I opened the general, security, profile editor, advanced and wizard verify shots.

Defects found by looking and fixed: solid yellow and red "restart"/"sensitive" badges (now soft tones); the Save row, checked at the end of the page, sits below the last field and nothing paints over it at 1440 and 390 px.

Seen, not changed: the wizard footer tip is cut off at 390 px (wizard footer, not in FR-053).

## Reviews

- `github-copilot/gpt-6.2-astra --variant high`, design question (what is removed, hidden or unreachable): 5 findings, 4 fixed (profile editor field refusal now brought into view; hints toggle keeps focus; re-picking General cancels a pending `?tab=`; Advanced help names the new section labels), 1 not changed (Verify says "waiting" when the onboarding state itself cannot be read: old behaviour of the store default, not this change).
- `github-copilot/gpt-6.2-sol --variant high`, code correctness, round 1: 6 findings; 5 fixed (stale `?tab=` rewritten on re-pick; bare `/settings` falls back to General; section list is a roving-tabindex list with Up/Down/Home/End; wizard `tabpanel` roles; arrows move from the focused wizard tab; a "checking" verify state until the status call settles), 1 (a field error under a key the profile form never renders) is old behaviour, not changed.
- Sol round 2, on those fixes: 5 findings; 2 fixed (fallback to General also after a `?focus=` link; `aria-controls` only on the selected wizard tab), 3 not changed and listed as known edges: a repeat of the same `?focus=` link on an already-selected General is a duplicate navigation (old behaviour of the router); reopening the wizard on Verify after a failed status read shows "not tracked" instead of "checking" until the next poll; an unrendered profile field error key (old). Verdict: not clean at round 2, stopped there with these edges recorded (cap is 20 rounds).
- Astra was run once, before the fixes; it was not re-run on the final diff.
