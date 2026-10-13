# Auto-Updater Rollout & First-Test Plan

**Feature**: 092-auto-updater · **Created**: 2026-08-08 · **Status**: Active

The governing risk: **release N ships a broken updater, and N's users can never
be reached over the air again.** Every decision below is shaped by that
asymmetry — detection failures are recoverable nuisances, but only because we
keep independent fallback channels alive; a broken *install* path would be
worse than no updater at all.

## Why "stuck forever" is already structurally impossible

Three update channels exist and fail independently. The Sparkle feed is the
*best* one, not the *only* one:

| Channel | Detects via | Survives a broken… |
|---|---|---|
| Sparkle one-click | signed appcast at mcpproxy.app | — (the thing under test) |
| Legacy nudge → browser download | GitHub Releases API, polled by core (24h) **and** tray — no appcast, no EdDSA involved | broken feed, wrong key, dead site |
| Manual DMG over-install | user action | everything above |

Two invariants make the fallbacks real, both already tested on this branch:

1. **Feed empty/404/unverifiable → the GitHub-check result renders as a
   browser-download menu item** (FR-017 state machine; feed-404 tests). A user
   whose feed never works still gets nudged — through a channel with no shared
   failure mode.
2. **Manual DMG over-install now works cleanly** (Phase 0: bundle-replacement
   detection + stale-core supersede + postinstall quit — the #957 fix). The
   escape hatch is no longer booby-trapped.

So "fails to detect new version" degrades to "user updates the old way, like
every release before this one." The plan's job is to make sure we *notice*
quickly and never regress the fallbacks.

## Failure modes & mitigations

### Detection (user stays on old version — annoying, recoverable)

| # | Failure | Mitigation in place | Residual action |
|---|---|---|---|
| D1 | Appcast not served (site PR unmerged, CF outage, stale deploy) | GitHub-check fallback nudges; site workflow verifies live URLs after deploy (below) | Merge website PR before release N |
| D2 | Wrong/placeholder `SUPublicEDKey` in shipped app, or wrong private key in CI | Sparkle refuses items *silently* → fallback nudge still fires; RC dress rehearsal (below) catches it before stable | Never rotate the key casually |
| D3 | Feed malformed / missing `edSignature` | CI refuses to publish such a feed (generate + site workflows both grep for the signature); fallback | — |
| D4 | Arch mix-up (arm feed serving amd enclosure) | Per-arch feeds generated from per-arch enclosure dirs; RC rehearsal runs on arm64 at minimum | Verify amd64 once via the rehearsal checklist |
| D5 | Update policy stuck restrictive (core never reports policy) | `awaitingCore` default is deliberate; `legacyDefault` stamps once a core actually answered; manual "Check for Updates" always allowed | — |
| D6 | SemVer comparison bug hides an update | SemVer 2.0 suite (rc.2/rc.10, build metadata, malformed) | — |
| D7 | Stable user offered an RC (or vice versa) | Channel-tagged beta feeds at separate URLs; policy-gated | Negative check in rehearsal |

### Install (worse class — must never strand a broken app)

| # | Failure | Mitigation |
|---|---|---|
| I1 | Crash mid-swap | Sparkle's install is atomic; CLI path is sentinel-journaled + advisory-locked with `.old` rollback and post-swap `--version` proof |
| I2 | Old core survives the update | Phase 0 supersede runs permanently at attach + on every version report — cleans up after *any* install path, including Sparkle's |
| I3 | Updated app killed by Gatekeeper (not stapled) | Enclosure is the notarized+stapled zip; **rehearsal must run on a real signed build, not a dev build** (Sparkle's own documented requirement) |
| I4 | App translocated / read-only volume | Surfaced error + browser fallback (FR-016), not a silent no-op |
| I5 | Install-on-quit replacing bundle under a live core | Path disabled by construction (`automaticallyDownloadsUpdates` forced false; tripwire logs if ever re-enabled) |

### Systemic (the "broken N" scenario)

| # | Failure | Mitigation |
|---|---|---|
| S1 | Release N's updater is broken; N+1 can't reach users OTA | Fallback channels (above); **minimize the N→N+1 gap** — see sequencing; release notes for N state the manual path |
| S2 | A *bad* update is being offered (worse than none) | Feed pull = `git revert` on the website repo (feeds live in `public/` under version control) — offering stops within one Pages deploy, ~1 min; users stay on current version |
| S3 | Site serves stale feeds silently | Post-deploy live verification in the site workflow (hash-compare against the release assets) |
| S4 | Adoption is broken and nobody notices | Telemetry heartbeats carry version — watch the version mix on the dashboard; success = N→N+1 migration visibly faster than the historical manual-update curve |

## The test plan

### Stage 0 — local rehearsal (no releases; can run today on this branch)

Build the rig once, keep it as the pre-release smoke test:

1. Build two app bundles locally (`build-swift-app.sh` at fake versions
   `v0.99.0-test` → `v0.99.1-test`), signed with the dev identity, **test**
   EdDSA keypair (never the production key).
2. `generate_appcast` over the v0.99.1 zip; serve feed + zip from
   `python3 -m http.server`; point the installed v0.99.0 at it
   (`defaults write com.smartmcpproxy.mcpproxy SUFeedURL http://localhost:8000/appcast.xml`
   — any non-`appcast.xml`-default URL is honored verbatim).
3. **Green path**: menu shows "Update 0.99.1 — ready to restart?" → click →
   download → core stopped (verify by pid) → bundle swapped → relaunch → app
   *and* core report 0.99.1; old core did not survive.
4. **Negative paths**, same rig:
   - tamper one byte of the zip → Sparkle refuses, current version keeps running;
   - serve a feed signed with a *different* key → refused;
   - kill the HTTP server → menu falls back to the browser-download item;
   - `MCPPROXY_DISABLE_AUTO_UPDATE=1` → no nudge, manual check still works.

### Stage 1 — RC dress rehearsal (the real "first test", full production pipeline)

The RC channel is the staging environment: `prerelease.yml` now has full parity
(beta feeds, checksums, cosign, site dispatch). **No stable user is exposed.**

1. Merge PR #958 and website PR #4. Cut `vNEXT-rc.1` per the prerelease flow.
2. Verify the pipeline did its job with no hands:
   `appcast-beta-*.xml` on the release **and** live at mcpproxy.app (site
   workflow green incl. live verification), enclosure downloads, stapler
   validates it.
3. Install rc.1 from the DMG on the laptop (manual — this is everyone's last
   manual install), opt into the RC channel.
4. Cut `vNEXT-rc.2` (trivial diff). Wait for the scheduled check or use
   "Check for Updates": **one click must land rc.2** with the full
   stop-core → swap → relaunch → supersede chain on a real notarized build.
5. Negative: a stable-channel install must not see rc.2; `mcpproxy update
   --check` on the RC channel reports it; on a brew install prints the brew
   line.
6. Only a clean rc.1→rc.2 one-click unlocks the stable release.

### Stage 2 — stable N (auto-updater release)

Cut stable per the usual process (tag main; update site links). Release notes
say explicitly: *this release installs manually; from the next release, updates
are one click* — and name the fallback (menu → browser) in case the feed is
ever unreachable.

### Stage 3 — N+1 canary: the concurrency release (minimize the gap)

Ship 093 (request concurrency) as `v(N+1)` **within days, not weeks** — it is
merged, Codex-clean, and deliberately independent, so it is the ideal
first-OTA payload:

- If the updater works: users get a real feature via one click, and the
  dashboard shows the migration curve.
- If the updater is broken: the exposure window is days, the population is one
  release deep, and every user still has two working fallback channels.

Watch after shipping N+1: version mix in telemetry heartbeats (expect N to
drain measurably faster than historical releases), GitHub issues mentioning
updates, and the site workflow staying green.

## Standing rules distilled from this plan

- The GitHub-check fallback and the Phase 0 supersede are **permanent safety
  rails** — never removed as "redundant with Sparkle".
- The production EdDSA key never rotates casually (shipped apps pin it; a
  rotation orphans every install back to manual).
- Feeds are pulled (git revert on the site), never edited by hand — an edited
  body fails signature verification anyway.
- Every future release is implicitly an updater test: N's pipeline publishes
  the feed that N−1's users consume. A failed `sparkle-appcast` job or a red
  site workflow is a release blocker, not a warning.
