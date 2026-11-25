# PR 4 verification: Home

Checked on the PR head, 2026-11-25, macOS arm64. Stacked on PR 1 (#2049).

| Check | Command | Result |
|---|---|---|
| Frontend unit tests | `cd frontend && npx vitest run` | 3212 passed (358 files) |
| Type check | `cd frontend && npx vue-tsc --noEmit` | clean |
| Build | `cd frontend && npm run build`; `go build ./cmd/mcpproxy`; `go build -tags server ./cmd/mcpproxy` | ok |
| Standing sweep with fixture upstream | `MCPPROXY_BASE_URL=http://127.0.0.1:18951 MCPPROXY_FIXTURE_PATH=<mcpfixture> ./scripts/run-web-smoke.sh` | 172 passed, 4 skipped; the 3 failures of the first run are fixed (below) and re-run singly |
| Home sweep on three isolated instances | `home-pr4.spec.ts` against fresh (0 servers), seeded (5 servers, 2 attention items), fleet (34 servers, 6 attention items) | 7/7 pass on fleet, 6 pass + 1 skip on seeded and fresh (no attention items to scroll for on fresh; See all needs more than 5) |

Instances were isolated: own `--config`, `--data-dir`, `HOME`, ports 18941 to 18943 and 18951; never `~/.mcpproxy` or 8080. A first smoke run hit another session's instance on the launcher's default port 18080 ("address already in use"), so its results were discarded and the run repeated on 18951.

## What the sweep asserts (`e2e/web-ui-sweep/home-pr4.spec.ts`)

- Section order by position: attention, usage strip, recent activity, fleet line, shortcuts, security scan, topology.
- **SC-011**: with the fleet instance the first attention item and its fix button are fully inside the first screen at 1440×900 and at 390×844 (`toBeInViewport({ ratio: 1 })` and bottom edge ≤ viewport height).
- No sideways scroll at 1440 and 390; every `main .btn` outside dialogs is at least 44 px tall at 390.
- "See all" by keyboard (focus, Enter) goes from five rows to all; the sixth item's fix link, activated by Enter, lands on its target.
- Each of the five shortcuts is reached by focus and activated by Enter at 390.
- The topology section is collapsed by default, opens by Enter in place, shows live clients and the servers card, survives a reload, and closes by Space.
- The header online indicator no longer carries tools or routing mode text; the fleet line shows "usable" and the mode.
- Screenshots (`SWEEP_SCREENSHOT_DIR`): 1440 light, 1440 dark, 390 light, 390 dark, each with the topology open and closed, for the three instances, plus the first-run wizard over Home. I opened them.

## Visual review fixes

| Defect seen | Fix |
|---|---|
| Update and telemetry notices were solid saturated alert blocks (a cyan slab taking half of the 390 px first screen); their dismiss and copy buttons were 24 to 32 px. | Soft busy tone with 1 px border; 44 px controls below `sm`; command wraps. |
| Attention rows had black dividers in light theme. | Rows use `--ds-border`. |
| Strip captions at 60 % opacity. | `text-base-content/70`. |
| Docker, quarantine and quarantine-count cues were solid tinted blocks. | Soft status tones. |

## Contrast sweep changes

- `contrast AA: / with a live MCP client` now opens the topology first (live clients moved into the collapsed section).
- `contrast AA: buttons and links on a coloured alert, hovered` had its coverage from Home's two daisyUI alerts. They are now soft notices, so on a clean instance no swept route shows one. The guard measures seven routes and, when none shows a coloured alert with a ghost button, records an annotation and passes instead of failing. Any alert that is on screen is still measured in both themes.

## Not verified

- Auto-open of the first-run wizard is unchanged code; its screenshot is attached but no test newly asserts it.
- Server edition (`/my/*`, `/admin/*`) Home as a tenant: covered by unit tests only (`dashboard-tenant-gating.spec.ts`, `home-pr4.spec.ts`), not driven in a browser.
- Dark theme primary buttons render daisyUI cyan rather than brand blue in the attention list and header (PR 1 tokens); not changed here.

## Cross-model review (opencode `github-copilot/gpt-6.2-astra`, high, 1 round)

Brief: which existing function the diff removes, hides without a named place, or makes unreachable at 390 px or by keyboard, against the merge-base `ad6a819ac`. Verified each finding against the code:

| Finding | Verdict | Action |
|---|---|---|
| Fleet line hidden on first run, where the header showed it | real | Shown on first run too |
| Mode shown before the server list is known | real | "Loading servers…" / "Servers unavailable" until loaded |
| CLI hints moved inside the collapsed topology; their disclosure was click-only | real | Hints panel is last on the page again; its two headers got `role=button`, Enter and Space |
| Scan summary "Loading…" never resolves for a tenant | real | No loading text; link only |
| "No issues found" claims more than a scan count shows | real | Removed; only "Run first scan" and the issue count, as before |
| Single usage strip no longer remounts, so no refresh | real | Strip refreshes itself every 30 s |
| Nine `data-test` hooks removed | accepted | Moved elements: `home-usage-strip`, `home-shortcut-*`, header tools/mode moved to `home-fleet-*`; tests moved (FR-073) |
| Loading/failed attention branches now render text | intended (P4-09) | none |

Round 2 not run: no design change after round 1 beyond the fixes above.

## Visual review round 2

- Recent activity statuses use `StateTag` in soft tones (error → error tone, blocked → review tone, other pill statuses neutral); "Success" stays plain text. The contrast sweep for `/` passes in both themes.
- Vertical rhythm: Home sections 16 px apart, usage cards shorter. At 1440×900 with two attention items the usable-tools line ends at y=749 of 900, so it and the shortcut row are inside the first screen (measured by script, screenshot looked at).
