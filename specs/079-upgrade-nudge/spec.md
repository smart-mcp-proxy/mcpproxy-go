# Feature Specification: Upgrade Awareness & Guided Update

**Feature Branch**: `chore/roadmap-polish-specs`
**Created**: 2026-07-02
**Status**: Draft
**Input**: User description: "Most active installs run far-behind versions and never learn a newer one exists. Turn the existing background update check into a universal, non-intrusive upgrade nudge that reaches every surface (CLI status/doctor, a log line, a dismissible Web UI banner, both trays), tells each user how far behind they are, and — where the install channel can be identified — shows the exact one-line command to update. Never block, never modal, respect a config opt-out and the existing stable/prerelease channel semantics, and stay silent in offline/CI/ephemeral contexts."

## Overview

Corrected telemetry (2026-07-02) shows that of the active installs in the last 14 days, roughly **60% run a pre-v0.40 build** (released ~March), while the latest stable (v0.46.0, released 2026-06-25) has under 19%, and a sticky older cohort nearly ties it. Three in five active users therefore receive none of the shipped improvements — scanner v2, profiles, security fixes — and any breaking config change silently strands them.

MCPProxy already has most of the *plumbing* for update awareness, but it is split across **two divergent check paths** that were built independently:

- A newer, centralized background checker `internal/updatecheck/` (`checker.go`, `github.go`, `types.go`) polls GitHub `releases/latest` every 4 hours (`DefaultCheckInterval`, `checker.go`), compares versions with `golang.org/x/mod/semver`, and exposes the result on `GET /api/v1/info` as an `update` object (`available`, `latest_version`, `release_url`, `checked_at`, `is_prerelease`, `check_error`; `types.go` `VersionInfo`/`InfoResponseUpdate`, `internal/contracts/types.go` `UpdateInfo`). It is wired through `internal/runtime/runtime.go` and `internal/server/server.go` (`GetVersionInfo()`/`RefreshVersionInfo()`). `mcpproxy doctor` renders it (`cmd/mcpproxy/doctor_cmd.go` ~L290-300), and the Web UI store `frontend/src/stores/system.ts` exposes `updateAvailable` + a manual `checkForUpdates()` toast, surfaced as a sidebar indicator in `frontend/src/components/SidebarNav.vue` (~L60, L488).
- An older, independent self-updater inside the Go tray, `internal/tray/tray.go` `checkForUpdates()` (L1039), makes its **own** separate `releases/latest` call (L1103), honors `MCPPROXY_UPDATE_NOTIFY_ONLY` (L1053), detects Homebrew to suppress self-update (`isHomebrewInstallation()`, L1176), and downloads/replaces the binary. The Swift tray adds a third path, `native/macos/MCPProxy/MCPProxy/Services/UpdateService.swift`, which is a **Sparkle stub**: it uses `SPUStandardUpdaterController` when the Sparkle SPM dependency is linked and otherwise falls back to its own GitHub API check (L5-6, L18, L54-57, L130-134).

What is missing is **reach, consistency, and a single source of truth**. The nudge does not appear where far-behind users actually look: `mcpproxy status` (the common command) omits it while `doctor` shows it; there is no startup log line; the Web UI has an easy-to-miss sidebar dot rather than a dismissible banner; and there is no config-file control (only environment variables — `MCPPROXY_DISABLE_AUTO_UPDATE`, `MCPPROXY_UPDATE_NOTIFY_ONLY`, `MCPPROXY_ALLOW_PRERELEASE_UPDATES`). The two Go check paths can disagree (the tray's own check vs `internal/updatecheck`), and there is no notion of an install channel model. Critically, for the large headless / server / Docker / Linux-tarball population — the cohort most likely to be far behind — there is **no actionable "here is how to update" guidance** at all, because the binary never identifies its install channel beyond the Homebrew check used only to *suppress* self-update.

This feature makes upgrade awareness **universal, consistent, non-intrusive, and actionable**, building on the existing `updatecheck` foundation rather than replacing it: it surfaces the same result on every surface with a clear "N releases / M weeks behind" framing, adds a dismissible per-version Web UI banner and a startup log line, adds config-file control with documented opt-out and channel selection, and — where the install channel can be identified — shows the exact one-line update command for that channel. It never blocks, never opens a modal, and stays quiet offline and in CI/ephemeral contexts.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Universal, non-intrusive upgrade awareness (Priority: P1)

A user running a months-old build interacts with MCPProxy the way they normally do — running `mcpproxy status`, glancing at the startup log, opening the Web UI, or clicking the tray. From wherever they already look, they learn, without any interruption, that a newer stable version exists and how far behind they are ("v0.46.0 available — you run v0.38.1, 8 releases / ~14 weeks behind"). Nothing blocks, nothing pops a modal, and the message can be dismissed and does not nag repeatedly for the same version.

**Why this priority**: This is the core of the feature and the MVP. The single biggest lever on the far-behind population is making the existence of a newer release *impossible to miss* on the surfaces they already use, without being annoying enough to be disabled. The backend check already exists; the value is consistent, universal, calm surfacing.

**Independent Test**: With the checker returning a newer stable than the running build, confirm the "vX available, you run vY, N behind" message appears consistently and non-intrusively in `mcpproxy status`, `mcpproxy doctor`, a single startup log line, the Web UI (as a dismissible banner), and both trays — and that dismissing the Web UI banner suppresses it for that version but not for the next one.

**Acceptance Scenarios**:

1. **Given** a newer stable release than the running version, **When** the user runs `mcpproxy status`, **Then** the output includes the current version, the latest version, and a human-readable "N releases / M weeks behind" delta.
2. **Given** a newer stable release, **When** the core starts, **Then** exactly one informational log line announces the available update (version, delta, release URL); when already current, no such line is emitted.
3. **Given** a newer stable release, **When** the user opens the Web UI, **Then** a dismissible, non-modal banner shows the available version, the delta, and a link to the release notes.
4. **Given** the user dismisses the Web UI banner for version vX, **When** they reload the Web UI and vX is still the latest, **Then** the banner stays dismissed; **When** a newer vY later becomes latest, **Then** the banner reappears for vY.
5. **Given** a newer stable release, **When** the user opens either tray, **Then** the update availability is indicated consistently with the other surfaces (same version and framing), and no surface blocks interaction while a check is pending or fails.
6. **Given** the running version equals the latest stable, **When** any surface renders, **Then** no upgrade nudge appears (and status/doctor may state "up to date").

---

### User Story 2 - The right update command for my install channel (Priority: P2)

A user who has been told an update exists wants to know *exactly how to get it*. A Homebrew user is shown `brew upgrade …`; a Docker user is shown the `docker pull …` tag to bump; a Linux `.deb`/`.rpm` user is shown the apt/dnf command; a `go install` user is shown the module path. Where the channel cannot be reliably identified (e.g. a bare tarball extraction), they get a safe, generic instruction pointing at the releases page and download docs rather than a command that might be wrong for their setup.

**Why this priority**: Awareness without an action is a dead end for the headless/server/Docker/Linux population that cannot self-update from a tray. Turning "you are behind" into a copy-pasteable next step is what actually moves those users forward. It builds directly on US1 and is only useful once awareness lands.

**Independent Test**: For each identifiable channel (brew, dmg/desktop, deb/rpm, docker, go-install) confirm the surfaced guidance is the correct one-line action for that channel and deep-links the release notes; for an unidentifiable channel confirm the generic releases-page instruction is shown and no channel-specific command is emitted.

**Acceptance Scenarios**:

1. **Given** an install identified as a given channel, **When** the guided-update guidance is shown, **Then** it presents the one-line action appropriate to that channel (e.g. package-manager upgrade command, Docker pull, or `go install …@latest`).
2. **Given** an install whose channel cannot be reliably determined, **When** guidance is shown, **Then** it presents a generic "download the latest release" instruction linking the releases page and download docs, and does not present a channel-specific command that could be incorrect.
3. **Given** any guided-update guidance, **When** it is shown, **Then** it deep-links the release notes for the latest version.
4. **Given** a package-manager-owned install (brew/deb/rpm/docker), **When** guidance is shown, **Then** it never instructs the user to run an in-app self-update that would conflict with the package manager.

---

### User Story 3 - Operator control and environment-appropriate quiet (Priority: P3)

An operator running MCPProxy in an air-gapped datacenter, a CI pipeline, or an ephemeral container wants the update behavior under their control from the config file — able to disable checks entirely, or opt into the prerelease channel — and expects the tool to stay silent when it obviously should (no network, non-interactive CI, ephemeral runs) rather than emitting noise or failing.

**Why this priority**: Control and good defaults are what keep the nudge from becoming something operators disable out of annoyance, but they matter only once the awareness and guidance (US1/US2) exist. The existing environment-variable switches remain, so this is additive hardening rather than a prerequisite.

**Independent Test**: Toggle the config knobs (disabled, prerelease channel) and confirm behavior changes accordingly and hot-reloads; run the check offline, in a simulated CI/non-interactive context, and as a prerelease build, and confirm the tool degrades to quiet correctness (no crash, no misleading "downgrade" nudge, no UI nag) in each case.

**Acceptance Scenarios**:

1. **Given** `update_check.enabled=false` in config, **When** the core runs, **Then** no update check is performed and no nudge appears on any surface; **When** the value is changed and the config hot-reloads, **Then** the new setting takes effect without a restart.
2. **Given** `update_check.channel` set to the prerelease channel, **When** a newer prerelease exists, **Then** it is offered consistently with the documented prerelease semantics; **Given** the default stable channel, **Then** prereleases are never offered as upgrades.
3. **Given** no network access, **When** the check runs, **Then** it fails silently with the error captured for diagnostics and never blocks startup or any surface, and no nudge is shown.
4. **Given** a running prerelease build newer than the latest stable, **When** awareness is computed, **Then** the user is NOT nudged to "upgrade" to an older stable version (no downgrade nudge).
5. **Given** a non-interactive / CI / ephemeral context, **When** surfaces render, **Then** UI nudges (banner, tray, repeated log nagging) are suppressed while machine-readable fields (status/doctor/`/api/v1/info`) still report the facts.
6. **Given** the existing environment-variable switches, **When** they are set, **Then** they continue to work and interoperate with the config knobs with a documented precedence.

---

### Edge Cases

- **Air-gapped install**: the GitHub check fails on every attempt → the failure is recorded (`check_error`), never surfaced as an alarming state, and the tool behaves exactly as an up-to-date install for gating purposes (no blocking, no repeated error toasts).
- **Downgrade / pinned older**: the latest stable is *older* than the running build (e.g. a prerelease, or a locally built newer version) → no "upgrade" nudge is shown; the delta is never negative.
- **Prerelease user on stable channel**: a user running an `-rc`/`-next` build must not be told to "upgrade" to the matching or older stable; prerelease-vs-stable comparison must not treat a stable tag as newer than the prerelease it precedes.
- **GitHub API rate limiting / transient 5xx**: unauthenticated GitHub API calls are rate-limited → checks are rate-limited (at most a daily check) and back off on failure; a rate-limit or transient error is treated as "unknown", never as "up to date" and never as a hard error.
- **CI / ephemeral / non-interactive**: environment kind is not interactive → UI nudges are suppressed; the tool must not emit a per-run nag that would spam CI logs.
- **Version string is `development`/unversioned**: a locally built binary with no release version → no nudge (cannot meaningfully compare), and status/doctor say so rather than showing a bogus delta.
- **Channel mis-identification risk**: install-channel heuristics are ambiguous (e.g. a tarball copied into `/usr/local/bin`) → the system must prefer a generic instruction over emitting a possibly-wrong channel command.
- **Dismissal across versions**: a banner dismissed for vX must not hide the nudge for a later vY; dismissal is per-version, not permanent.
- **Docker `:latest` users**: users pinned to a moving `:latest` tag may already be current at runtime even if the running image build string looks old → guidance for Docker points at pulling the tag rather than implying they are stranded when they are not.
- **Reaching the existing far-behind cohort**: builds that predate this feature cannot show its nudge; the forward-looking metric (share of actives on latest-or-previous stable) is what this feature moves, and package-manager/Docker-tag/docs channels are the only paths that reach pre-feature installs (acknowledged, see Out of Scope).

## Requirements *(mandatory)*

### Functional Requirements

**Awareness (build on the existing checker)**

- **FR-001**: The system MUST reuse the `internal/updatecheck` background checker and its `GET /api/v1/info` `update` result as the single source of truth for update state; this feature MUST NOT introduce a second, divergent check pipeline.
- **FR-001a**: The Go tray's independent update check (`internal/tray/tray.go` `checkForUpdates()`, which today makes its own separate `releases/latest` call) MUST converge on the `internal/updatecheck` result so the tray cannot report a different availability/version than the other surfaces; the tray's self-update *action* mechanics may remain, but the *check* MUST be the shared one. Eliminating this divergence is in scope; expanding self-update mechanics is not (see Out of Scope).
- **FR-002**: The system MUST surface upgrade awareness consistently on all of: `mcpproxy status`, `mcpproxy doctor`, a startup log line, the Web UI, and both trays — showing the current version, the latest version, and a human-readable "N releases / M weeks behind" delta.
- **FR-003**: `mcpproxy status` MUST include the update availability and delta (it currently shows only the running version).
- **FR-004**: The core MUST emit exactly one informational log line per process start when (and only when) an update is available, including the latest version and release URL; it MUST NOT repeatedly log the same availability on a timer.
- **FR-005**: The Web UI MUST present a dismissible, non-modal banner when an update is available; dismissal MUST be persisted per-version so it suppresses the current version but reappears for a newer one.
- **FR-006**: No surface MAY block interaction, open a modal, or degrade functionality because a check is pending, failed, or reports an available update.
- **FR-007**: The check MUST run without requiring telemetry consent and MUST hit only an anonymous, static/GitHub endpoint (no user identifier sent); update checking MUST be independent of the telemetry opt-in state.

**Guided update (channel-aware)**

- **FR-008**: The system MUST attempt to identify the install channel (at least: Homebrew, macOS desktop/DMG, Linux `.deb`/`.rpm`, Docker, `go install`, and generic tarball) using reliable signals, preferring a build-time channel marker where available and falling back to path/environment heuristics.
- **FR-009**: When the channel is identified, the system MUST present the correct one-line update action for that channel; when it cannot be reliably identified, it MUST present a generic "download the latest release" instruction and MUST NOT emit a channel-specific command that could be wrong.
- **FR-010**: Guided-update guidance MUST deep-link the release notes for the latest version.
- **FR-011**: For package-manager-owned installs (brew/deb/rpm/docker), the system MUST NOT offer an in-app self-update that would conflict with the package manager (preserving today's Homebrew self-update suppression).

**Configuration & channel semantics**

- **FR-012**: The system MUST provide config-file control under an `update_check` group with at least `enabled` (default true) and `channel` (default stable; prerelease opt-in), hot-reloaded like other config.
- **FR-013**: The `channel` setting MUST respect the existing stable-vs-prerelease release semantics: on the stable channel, prereleases (`-rc`/`-next`) MUST never be offered as upgrades; the prerelease channel offers them consistently with the documented prerelease rules.
- **FR-014**: The existing environment-variable switches (`MCPPROXY_DISABLE_AUTO_UPDATE`, `MCPPROXY_ALLOW_PRERELEASE_UPDATES`, and the notify-only switch) MUST continue to function, with a documented precedence relative to the new config keys.
- **FR-015**: The opt-out MUST be documented, and when disabled the system MUST perform no network check and show no nudge on any surface.

**Correctness & quiet defaults**

- **FR-016**: The system MUST NOT show an "upgrade" nudge when the latest available version is not strictly newer than the running version (no downgrade nudge, no negative delta), including for prerelease builds newer than the latest stable.
- **FR-017**: The system MUST NOT show a nudge for an unversioned/`development` build and MUST clearly report that state rather than a fabricated delta.
- **FR-018**: The update check MUST be rate-limited (at most a daily check) and MUST back off on failure, treating rate-limit/transient errors as "unknown" rather than "up to date".
- **FR-019**: In non-interactive / CI / ephemeral contexts, UI nudges (Web UI banner, tray indicator, repeated logging) MUST be suppressed while machine-readable fields (status/doctor/`/api/v1/info`) still report the facts.
- **FR-020**: A failed check (offline, rate-limited, error) MUST never block startup or any surface and MUST never surface as an alarming/error state to the end user; the error is retained for diagnostics only.

**Compatibility**

- **FR-021**: The `GET /api/v1/info` `update` contract MUST remain backward compatible; the payload MAY add fields (e.g. delta, channel, guided command) but MUST NOT remove or repurpose existing ones (`available`, `latest_version`, `release_url`, `checked_at`, `is_prerelease`, `check_error`).
- **FR-022**: All new behavior MUST be covered by automated tests, including tests that inject a mocked release result (newer stable, equal, older/prerelease, error/offline) without hitting the network, and a test that a dismissed banner version reappears for a newer version.

### Key Entities *(include if feature involves data)*

- **Update state**: The result of a check — current version, latest version, availability, release URL, checked-at, is-prerelease, and check-error — extended with a human-readable release/time delta. (Extends the existing `VersionInfo` / `/api/v1/info` `update` object; not a new pipeline.)
- **Install channel**: The identified distribution channel of the running binary (homebrew, desktop/dmg, deb, rpm, docker, go-install, tarball, or unknown) plus the corresponding one-line update action or generic fallback instruction.
- **Update-check config**: The `update_check` group — `enabled` and `channel` — plus documented precedence with the existing environment-variable switches.
- **Banner dismissal**: A per-version record (client-side) that a user dismissed the Web UI nudge for a specific latest version.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: The share of last-14-day active installs running the latest-or-previous stable release (currently ~23%) increases past 50% within two release cycles after this ships.
- **SC-002**: With a newer stable available, 100% of the surfaces — `mcpproxy status`, `mcpproxy doctor`, startup log, Web UI banner, and both trays — show the same available version and consistent "behind" framing, verified by test.
- **SC-003**: A Web UI banner dismissed for version vX stays dismissed for vX and reappears for a later vY, verified by test.
- **SC-004**: For each identifiable install channel, the guided command shown is the correct one-line action for that channel (0 wrong-channel commands), and unidentifiable channels always fall back to the generic instruction, verified by test.
- **SC-005**: With update checking disabled via config, zero network checks are made and zero nudges appear on any surface, verified by test.
- **SC-006**: In offline, rate-limited, downgrade, prerelease, and `development`-build cases, no misleading or alarming nudge is shown and no surface blocks, verified by test for each case.
- **SC-007**: In a non-interactive/CI/ephemeral context, no UI nudge or per-run log nag is emitted while machine-readable fields still report update state, verified by test.
- **SC-008**: The `GET /api/v1/info` `update` contract retains all existing fields and its current consumers (doctor, Web UI store) continue to pass their tests.

## Assumptions

- The existing `internal/updatecheck` background checker, its semver comparison, and the `/api/v1/info` `update` contract are sound and are the intended foundation; this feature extends surfacing and configuration rather than re-implementing the check.
- Hitting the anonymous GitHub `releases/latest` API (and `releases` for the prerelease channel) is an acceptable, privacy-preserving check that requires no user identity and no telemetry consent; a static redirect endpoint is an acceptable alternative if GitHub rate limits prove problematic.
- A build-time channel marker (injected at release/packaging time, analogous to how the version is stamped) is the most reliable channel signal; path/environment heuristics (Homebrew Cellar prefix, deb/rpm-owned paths, `/.dockerenv`, missing release version for `go install`) are an acceptable best-effort fallback where the marker is absent.
- "N releases behind" can be derived from the tag sequence between the running and latest versions, and "M weeks behind" from release publish dates; where the exact release count is unavailable, a version-delta and date-delta approximation is acceptable.
- The far-behind cohort that predates this feature cannot be reached by an in-app nudge; the metric this feature moves is forward-looking (share behind at each future release), and package-manager auto-upgrade, Docker tag moves, and docs are the mechanisms that reach pre-feature installs.
- Environment kind (interactive vs CI/ephemeral) is already detectable from existing environment/launch signals used elsewhere in the codebase.

## Out of Scope

- Expanding binary self-update to new channels or building one-click self-update beyond what the desktop trays already do; self-update remains a desktop-tray concern and is explicitly not added for package-manager-owned or headless installs. (The convergence of the tray's *check* onto `internal/updatecheck` is in scope per FR-001a; the tray's download/replace/restart *mechanics* are not.)
- Linking or completing the Swift **Sparkle** integration (`UpdateService.swift` is a stub that falls back to a GitHub check when Sparkle is not linked); wiring up Sparkle's full appcast-based update UX is a separate initiative. This feature only requires the Swift tray's *awareness* to be consistent with the shared check.
- Any server-side "your version is end-of-life" broadcast or forced-update mechanism; awareness stays local and advisory.
- Automatically running package-manager upgrade commands on the user's behalf (guidance is shown, never executed).
- Rescuing the existing pre-feature far-behind cohort through the in-app nudge (impossible by construction); only forward-looking behavior is in scope.
- Redesigning the release/packaging pipeline beyond adding a build-time channel marker.
- Changing the tray self-update download/replace/restart mechanics that already ship.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #[issue-number]` — links the commit to the issue without auto-closing.
- ❌ **Do NOT use**: `Fixes #`, `Closes #`, `Resolves #` — these auto-close issues on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

### Example Commit Message
```
feat(update): universal upgrade nudge + channel-aware guided update

Related #[issue-number]

Surface the existing background update check consistently across
mcpproxy status/doctor, a startup log line, a dismissible per-version
Web UI banner, and both trays, with a "N releases / M weeks behind"
delta. Add channel-aware guided update commands and an update_check
config group. Never blocks; quiet offline and in CI/ephemeral contexts.

## Changes
- status: show update availability + delta (parity with doctor)
- Web UI: dismissible per-version update banner
- startup log line when an update is available
- install-channel detection + per-channel one-line update guidance
- update_check.{enabled,channel} config with env-var precedence

## Testing
- Mocked release results (newer/equal/older/prerelease/error)
- Per-version banner dismissal, channel guidance, disabled opt-out,
  CI/ephemeral suppression
```
