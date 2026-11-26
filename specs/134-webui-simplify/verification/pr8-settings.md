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

## Follow-up round (after the design session's request)

- Fixed with a test first: (a) a repeated `?focus=` link on General (the first visit now writes `tab=` into the URL); (b) reopening the wizard on Verify now shows "checking" again (per-open request token, a stale negative is reset on open).
- (c) The unrendered profile field error key is the code on main: `ProfileEditor.vue:504` on `origin/main` (`else if (refused.field) fieldErrors[refused.field] = refused.message`) with the same template keys. Not changed (R2).
- The wizard footer tip is cut off on main too: the same `text-xs opacity-50 truncate` element at `OnboardingWizard.vue:669` on `origin/main`. Not changed.
- Unit tests: 3245 passed, type check clean. Full sweep on the final build: 173 passed, 4 skipped, 0 failed; `settings-pr8.spec.ts` against an instance with a profile: 5 passed.
- Screenshots were not re-taken for this round: no layout change, only behaviour, focus and ARIA.

## After merging origin/main (PR 1 landed)

Merged `origin/main` (no rebase); conflicts were in the files both sides touched (docs spec lists, `main.css`, hints panel, wizard, Settings header, smoke script, inventory), resolved by keeping this PR's additions on top of main's. `go test ./cmd/release-gate/...` and the Spec 109 traceability/parity tests in `internal/httpapi` pass (the spec-list check sees `settings-pr8.spec.ts` in the launcher, `release-gate.md` and `web-ui-verification.md`; no file was renamed). Unit tests 3245 passed, type check clean, `make build` ok, full sweep 173 passed, 4 skipped, 0 failed.

## Reviews

- **Sol (`github-copilot/gpt-6.2-sol --variant high`, code correctness): 20 rounds, verdict NOT CLEAN at the cap.** Rounds 1-19 produced real fixes (stale `?tab=` and `?focus=` handling, bare and unknown tab and focus links, late edition discovery, roving tabindex and wrapping arrows, ignoring modified keys, focus hand-off, tabpanel roles, per-open status token, "checking" state, live region, field-error focus and aria-invalid). Round 12 returned no verdict (reviewer tried to read node_modules, UNREVIEWED) and was re-run. Round 20's single finding is unfixed: navigating in place to `/settings?focus=<unknown key>` falls back to General but then writes `tab=general` into the URL, where a direct load leaves `tab` absent (cosmetic). Not fixed, because round 21 is not allowed.
  - Declined, with reason: aria-controls on the selected wizard tab only (an earlier round asked for exactly that, as one panel is mounted); phone select re-selecting the shown option cannot cancel a pending teams link (a native select emits no change); verify state under direct or code-execution routing (the `retrieve_tools`-only pending rule is on main, `OnboardingWizard.vue:830`, with a test, and the copy is hedged); focus lost when resizing across the breakpoint (every responsive swap); the phone select shows the first section while a teams focus link waits for edition status (transient); a late field error applied after the same field was edited in flight (apply-on-refusal is on main).
- **Astra (`github-copilot/gpt-6.2-astra --variant high`, function-loss question), run once on the final diff: no finding of a removed, hidden or unreachable function, but 3 behaviour findings, so not a literal "NO FINDINGS".** All three exist on main and are not changed: a query-only `?tab=teams` link before edition status arrives (the retry is mount-only on main); a recorded upstream success hidden when the onboarding state itself cannot be read (store default); an earlier negative kept when a later status poll fails (`fetchActivation` "keep prior value" on main). An earlier Astra run (before fixes) found 5 issues, 4 fixed.

## Round 21 (one extra round approved by the maintainer past the cap)

Fixed with a test first: navigating in place to `/settings?focus=<unknown key>` now falls back to General without writing `tab=general` (matching a direct load). Unit tests 3245 passed, type check clean, `settings-pr8.spec.ts` and the Settings case of `web-ui-sweep.spec.ts` passed on a rebuilt isolated instance. One Sol run on the final diff (`github-copilot/gpt-6.2-sol --variant high`); the first attempt never received its brief (shell quoting) and was discarded as UNREVIEWED, the second returned two findings, reported here unfixed:

1. `Settings.vue` `focusField`: `/settings?tab=teams&focus=routing_mode` before edition status arrives selects General and cancels the Teams retry, but the URL keeps `tab=teams` (the tab is only written when the query has none). Assessment: real, cosmetic, introduced by this diff (on main the default Security section made the activeTab watcher rewrite the URL).
2. `ProfileEditor.vue` `revealRefusal`: the document-wide `.text-error` fallback can find another profile editor's error if a slow save fails after the user left the editor and opened a different profile with its own field error, and focus sits on `<body>`. Assessment: real, narrow, introduced by this diff (the fallback is new).

Verdict: not clean (round 21, final).
