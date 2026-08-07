# Feature Specification: One-Click Auto-Updater (macOS) + Channel-Aware `mcpproxy update` CLI

**Feature Branch**: `092-auto-updater`
**Created**: 2026-08-07
**Status**: Draft
**Input**: User description: "One-click auto-updater for macOS + channel-aware mcpproxy update CLI (fixes #957). Phase 0 fixes the stale-process bug for every upgrade path (version-mismatch supersede in tray, bundle-replacement detection, postinstall quit-before-launch). Phase 1 finishes the half-plumbed Sparkle 2 integration (one-click download, verify, bundle swap, relaunch; EdDSA keys, CI appcast, notarized stapled .app zip enclosure). Phase 2 adds mcpproxy update CLI branching on existing install-channel detection (self-update only on tarball/unknown-writable channels, cosign-verified; package-manager guidance elsewhere; DMG delegates to tray). Decision report: docs/research/auto-updater-issue-957-2026-08-07.html"

> Related: issue #957 ("old version App still after upgrade"). Decision analysis with option comparison and verification appendix: `docs/research/auto-updater-issue-957-2026-08-07.html` (Option A chosen).

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Upgrading never leaves the old version running (Priority: P1)

A macOS user upgrades MCPProxy by any means — dragging a new DMG into Applications, running the PKG installer, or any future automated path. After the upgrade, the running menu-bar app and the background core service are the new version (or the user is offered a one-click relaunch into it). The old version never silently keeps serving.

**Why this priority**: This is the reported bug in #957. Every other story builds on top of it; it must hold even for users who never adopt the one-click updater. It ships independently in the next release.

**Independent Test**: Install an older release, start it, install a newer release over it (drag-install and PKG separately), and verify the running app/core end up on the new version without manually killing processes.

**Acceptance Scenarios**:

1. **Given** an old-version tray and its managed core are running, **When** the user drag-installs a newer app bundle over the old one, **Then** the tray detects the on-disk version change and offers "MCPProxy was updated to vY — Relaunch", and accepting stops the old core and relaunches into the new version.
2. **Given** an old-version core is running that identifies itself as tray-launched (durable launch provenance — regardless of whether the tray instance that started it still exists), **When** a newer tray starts and attaches to it, **Then** the tray detects that the running core is older than its bundled core, stops it, and respawns the bundled (new) core automatically.
3. **Given** an old-version core is running that identifies as user/externally launched (e.g. started from a terminal), **When** a newer tray attaches, **Then** the tray does NOT kill it automatically but surfaces a clear "Old core vX still running — restart into vY" action; activating it is explicit user consent to stop that core, and if no safe stop mechanism is available the tray presents instructions instead.
4. **Given** the old app is running, **When** the user runs the PKG installer for a newer version, **Then** installation completes with the old instance quit first and the new version launched — not the stale instance brought to the foreground.
5. **Given** versions already match, **When** the tray performs its checks, **Then** no restart prompt or process churn occurs.

---

### User Story 2 - One-click update from the menu bar (Priority: P2)

A macOS user (DMG/PKG install) sees "Update 0.54.1 — ready to restart?" in the MCPProxy menu when a new version is available. One click downloads the update, verifies its authenticity, replaces the installed app, and relaunches — tray and core come back on the new version. No browser, no DMG dragging, no installer steps.

**Why this priority**: The requested UX and the reason users stay up to date. Depends on release-infrastructure changes (update feed, verified update archive), so it ships after the P1 bug fix.

**Independent Test**: Install a notarized older release that includes the updater, publish a newer release, click the menu item, and observe download → verify → swap → relaunch complete without any manual step; app and core report the new version.

**Acceptance Scenarios**:

1. **Given** a newer version exists in the update feed, **When** the user opens the menu, **Then** an "Update X.Y.Z — ready to restart?" item is visible (gentle nudge — no interrupting popups).
2. **Given** the user clicks the update item, **When** the update runs, **Then** the download is verified for both feed authenticity and OS code-signature validity before anything is replaced, the managed core is stopped gracefully before the swap, and the app relaunches on the new version with the core restarted.
3. **Given** the update completed, **When** the user checks versions (menu, `mcpproxy --version`, API), **Then** app and core both report the new version, and existing configuration, tokens, and quarantine state are untouched.
4. **Given** the app is running from a read-only or translocated location (e.g. launched from the DMG itself), **When** an update is attempted, **Then** the user gets a clear explanation and a fallback (e.g. move to Applications / download link) instead of a silent no-op.
5. **Given** verification of the downloaded update fails (tampered or corrupt), **When** the update runs, **Then** nothing is replaced, the running version keeps working, and the user sees an actionable error.
6. **Given** the user is on the release-candidate channel (per `docs/prerelease-builds.md`), **When** updates are offered, **Then** RC builds are offered only to RC-channel users; stable users see stable releases only.
7. **Given** automatic update checks are disabled (existing kill switch env/config), **When** the tray runs, **Then** no update nudges appear and no feed checks occur.

---

### User Story 3 - `mcpproxy update` does the right thing per install channel (Priority: P3)

A CLI user runs `mcpproxy update`. The command knows how mcpproxy was installed (Homebrew, deb/rpm, Docker, DMG, tarball, go-install …) and either performs a safe verified self-update (standalone-binary channels), prints the exact package-manager command to run, or points at the tray updater — never corrupting a package manager's bookkeeping and never touching the installed app bundle.

**Why this priority**: Completes the story for headless/CLI users; independent of the macOS GUI work and lower risk. Mirrors the behavior of best-in-class CLIs (uv, deno).

**Independent Test**: Run `mcpproxy update` on each channel fixture and verify: self-replace happens only on tarball/unknown-writable installs, with integrity verification; all other channels get correct guidance and a zero-side-effect exit.

**Acceptance Scenarios**:

1. **Given** a Homebrew/deb/rpm/go-install install, **When** the user runs `mcpproxy update`, **Then** the command prints the appropriate upgrade command (e.g. `brew upgrade mcpproxy`) and exits without modifying anything.
2. **Given** a Docker or Windows-installer install, **When** the user runs `mcpproxy update`, **Then** channel-appropriate guidance is printed and nothing is modified.
3. **Given** a macOS DMG install, **When** the user runs `mcpproxy update`, **Then** the command directs the user to the tray's updater (and never modifies the app bundle or any staged copy of it).
4. **Given** a tarball install in a user-writable location, **When** the user runs `mcpproxy update`, **Then** the new binary is downloaded, its integrity verified against the release's signed checksums, and swapped in atomically; the old binary is recoverable until success is confirmed.
5. **Given** the target binary location is not writable (e.g. root-owned), **When** the user runs `mcpproxy update`, **Then** the command fails with an explicit message naming the path and owner and suggesting options — it never escalates privileges itself.
6. **Given** the available version is the same or older than the running one, **When** the user runs `mcpproxy update`, **Then** the command reports "already up to date" and refuses to downgrade unless `--force` is passed.
7. **Given** any channel, **When** the user runs `mcpproxy update --check` (or `mcpproxy update` with no newer version), **Then** the command reports current/latest versions and the detected channel without side effects.

---

### Edge Cases

- Update clicked while the core is mid-request: managed core must be stopped gracefully (bounded wait) before the swap; in-flight tool calls fail visibly rather than hanging forever.
- Old core cannot be superseded because a different user/session owns it: surface the situation instead of fighting over the single-instance locks (config.db flock, TCP port).
- Network failure mid-download: no partial state; retry is safe; the running version is unaffected.
- Update feed unreachable: menu behaves as "no update available"; existing daily GitHub check remains the fallback surface; the two mechanisms must not double-nudge.
- Both per-arch artifacts exist: the correct architecture is selected on Apple Silicon and Intel.
- User declines the relaunch prompt: they can keep working on the old version; the prompt remains available, not nagging (respects existing nudge-suppression rules, e.g. CI environments).
- Legacy staged core copy (`~/Library/Application Support/mcpproxy/bin/mcpproxy` from the old Go tray) is stale: it must not shadow the new bundled core after an update.
- `mcpproxy update` run inside the app bundle context (binary resolved from `MCPProxy.app/Contents/...`): treated as DMG channel; never self-replaces in place.

## Requirements *(mandatory)*

### Functional Requirements

**Stale-version supersede (P1 — fixes #957)**

- **FR-001**: The tray MUST compare the running core's version with its bundled core's version at attach time and whenever a version report is received, and, for cores with tray launch provenance that are older, stop and respawn the bundled core automatically.
- **FR-001a**: Core launch provenance ("launched by a tray" vs. "launched by the user/other") MUST be durable and queryable across tray restarts — today ownership is only in-memory in the launching tray, and every pre-existing core is classified as external, which would defeat FR-001 for the exact tray-upgrade scenario this feature targets. The core already receives a launched-by marker at spawn; it MUST report it so any newer tray can recognize (and supersede) a core an older tray started.
- **FR-002**: For user/externally launched cores that are older than the bundled core, the tray MUST surface a restart action that requires explicit user activation (consent) rather than acting automatically. When no safe stop mechanism exists for that core, the action MUST present instructions instead of failing silently; the tray MUST NOT kill a user-launched process without that explicit activation.
- **FR-003**: The tray MUST detect that the on-disk app bundle version differs from the running app's version (drag-install upgrade) and offer a one-click relaunch that stops the managed core, launches the new bundle, and exits the old instance.
- **FR-004**: The macOS package installer's post-install step MUST quit any running MCPProxy instance (politely, with a bounded wait and forced fallback) before launching the newly installed version.
- **FR-005**: Supersede checks MUST be idempotent and silent when versions already match (no restart loops, no spurious prompts). A downgrade (running > bundled) MUST NOT trigger automatic supersede.
- **FR-006**: All version comparisons driving supersede and update decisions MUST follow SemVer 2.0 precedence, including numeric prerelease identifiers (rc.10 > rc.2 — the existing tray comparison sorts these lexicographically and MUST NOT be reused as-is), tolerate a leading "v" and build metadata, and treat malformed or missing versions as "no supersede" with a logged reason.

**One-click updater (P2)**

- **FR-010**: The menu MUST present available updates as a gentle, non-interrupting menu item of the form "Update X.Y.Z — ready to restart?"; activating it MUST complete download, verification, installed-app replacement, and relaunch without further user steps.
- **FR-011**: Updates MUST be verified with two independent mechanisms before installation: a cryptographic signature on each downloaded update artifact (enclosure-level, verified against a public key pinned in the shipped app — the feed XML itself is not what carries the signature) and OS code-signature validation of the replacement app that checks it matches the expected bundle identity/signing identity, not merely "some valid signature". A failure of either MUST abort with no changes and an actionable error.
- **FR-012**: The updater MUST stop the tray-managed core gracefully before the app bundle is replaced, and the relaunched app MUST start the new core. The P1 supersede check MUST remain active permanently as the safety net for update paths the updater does not control (install-on-quit, manual installs).
- **FR-013**: The update feed MUST be generated automatically by the release pipeline for every stable release, hosted at a stable HTTPS URL, and offer per-architecture (or universal) artifacts that pass macOS Gatekeeper (notarized and stapled) and preserve the app's code-signature integrity (symlink-preserving archive).
- **FR-014**: Release-candidate builds MUST be offered only to users on the RC channel, consistent with the existing prerelease opt-in mechanism; stable users MUST never be offered RCs. Because RC builds are produced by a separate prerelease pipeline that today publishes neither update-feed entries nor signed checksum manifests, that pipeline MUST gain equivalent artifact/feed/manifest generation (channel-tagged entries in the feed, RC artifacts covered by signed checksums) — RC support is in scope for the release-infrastructure work, not an afterthought on the stable pipeline.
- **FR-015**: The updater MUST honor the existing update-check kill switches (config setting and environment variable) and the existing nudge-suppression rules (e.g. CI). The effective policy (updates enabled/disabled, selected channel, nudges suppressed) MUST be an explicit, hot-reloadable contract visible to the tray — not inferred from missing data — and MUST govern every tray-side check, including any independent periodic check the tray performs today. User-initiated "Check for Updates" remains available even when automatic checks are disabled.
- **FR-016**: When the app cannot be updated in place (translocated, read-only volume, insufficient permissions), the updater MUST tell the user why and offer a fallback path rather than failing silently.
- **FR-017**: Exactly one source of truth MUST own the update menu item at any time: when the feed-based updater is available, it owns the one-click item and the legacy release check MUST NOT surface a competing nudge for the same or lower version; when only the legacy check has a result (feed unreachable, or it advertises a version absent from the feed), the item MUST present as browser-download guidance, never as a one-click action it cannot perform. Equal versions from both sources deduplicate to a single item.
- **FR-018**: The Homebrew cask MUST be marked as self-updating so the package manager does not fight the built-in updater.

**Channel-aware CLI (P3)**

- **FR-020**: `mcpproxy update` MUST branch on the already-detected install channel: package-manager channels (Homebrew, deb, rpm, go-install) get the exact upgrade command printed; Docker and Windows-installer get guidance; DMG gets a pointer to the tray updater. Self-update MUST require a **positively identified self-managed install** (tarball channel). The `unknown` channel MUST remain guidance-only — writability of the target does not establish ownership (ambiguous installs include AUR, MacPorts/Nix-like layouts, and manually installed packages), and a positive tarball marker MUST exist for self-update to ever activate (today the detector defines the tarball channel but never returns it, so positive identification — e.g. build-time channel stamping of tarball artifacts — is a prerequisite, not an assumption). An explicit `--self` style override MAY allow a user to assert self-managed ownership on `unknown`, with a clear warning.
- **FR-021**: CLI self-update MUST verify artifact integrity against the release's signed checksum manifest (signature verified offline against the project's release identity) before replacement, and MUST replace the binary atomically (write-new-in-target-directory + rename, never in-place). The previous binary MUST be retained until success is confirmed — success meaning the new binary executes and reports the expected version — and restored on failure; the spec's atomicity promise includes preserving file permissions/mode, following (not replacing) a symlinked target's destination, and defined behavior when a running core still executes from the old binary (it keeps running; the swap affects the next start).
- **FR-022**: CLI self-update MUST refuse to install a version equal to or older than the running one unless the user passes both an explicit target version option and `--force` (a bare "update to latest" has no downgrade to force), MUST never escalate privileges automatically, and MUST never modify files inside the installed app bundle or its staged copies.
- **FR-023**: `mcpproxy update` MUST support a check-only mode reporting current version, latest version, and detected channel, with no side effects, honoring the existing prerelease-channel selection.
- **FR-024**: After the one-click updater ships, the DMG channel's guidance text (status/doctor/Web UI surfaces) MUST direct users to the tray updater instead of "download the latest DMG".

**Cleanup**

- **FR-030**: The plan MUST identify which execution paths (if any) still resolve the legacy staged core copy ahead of the bundled core — the current tray prefers the bundled core, so the staged copy only matters for legacy-tray or non-bundle flows — and neutralize only those paths (refresh or remove the staged copy with clear ownership rules), never deleting a binary the user may manage themselves without that analysis.

### Key Entities

- **Install channel**: the detected provenance of the running binary (dmg, homebrew, deb, rpm, docker, go-install, windows-installer, tarball, unknown); determines which update behavior applies.
- **Update feed (appcast)**: the machine-readable list of published versions, per-architecture artifacts, and their signatures that the in-app updater consumes; generated by the release pipeline; stable URL is permanent infrastructure.
- **Update artifact (enclosure)**: a notarized, stapled, symlink-preserving archive of the app bundle attached to each release, listed in the signed checksum manifest.
- **Version pair (running vs. available)**: running core version, running app version, on-disk bundle version, bundled core version — the comparisons that drive supersede and update decisions.
- **Core ownership**: whether the core process is tray-managed (safe to restart automatically) or externally attached (restart requires user consent).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: After upgrading by any supported path (drag-install, PKG, one-click), for tray-launched cores and where the user accepts the offered relaunch: the running app and core report the new version within 60 seconds — with at most one user click — in 100% of QA upgrade scenarios. Where the user declines, the offer remains available without nagging; for externally launched cores, the consent action (or instructions) is present in 100% of scenarios.
- **SC-002**: Zero scenarios remain in which an older core keeps serving requests indefinitely after an upgrade without a visible prompt (the #957 report class is eliminated).
- **SC-003**: A user on a DMG install can go from "update available" to "running the new version" via a single menu click, with no browser, Finder, or installer interaction.
- **SC-004**: A tampered or corrupt update artifact is never installed (verification failure aborts with no change) in 100% of tamper-test cases.
- **SC-005**: `mcpproxy update` never modifies a package-manager-owned install; on self-managed installs it updates atomically or leaves the previous binary working — no scenario ends with a broken binary.
- **SC-006**: Update checks and nudges respect the existing kill switches and CI suppression in 100% of cases; stable-channel users are never offered RC builds.
- **SC-007**: Support burden: new-release adoption requires no manual process-killing instructions in issues/support (no recurrence of #957-style reports for releases shipped after Phase 0).

## Assumptions

- The chosen approach is Option A from the decision report: complete the existing in-app update framework integration rather than building a custom downloader/swapper, with the P1 supersede fix shipping first and remaining permanently as the safety net.
- The update feed URL will be decided by the maintainer before Phase 1 ships (the app bundles already reference `https://mcpproxy.app/appcast.xml`, which argues for honoring that URL); until decided, Phase 1 CI work can proceed against a placeholder.
- One-click update is limited to tray-managed cores in v1; externally-attached cores get the non-destructive prompt (open decision #2 in the report). Graceful drain of in-flight requests beyond the existing bounded stop is out of scope for v1.
- Windows and Linux GUI one-click update are out of scope; Windows/Linux users are served by the existing installer/package channels and the P3 CLI where applicable.
- Signing-key material (feed private key) lives in CI secrets; key rotation procedures are documented but rotation tooling is out of scope.
- The release pipeline's existing signed checksum manifest (checksums.txt + cosign bundle) is the trust root for CLI self-update; no new registry or update server is introduced.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #957` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #957`, `Closes #957`, `Resolves #957` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat(tray): supersede stale core after upgrade

Related #957

Detect version mismatch between running core and bundled core on attach
and restart tray-managed cores into the bundled version.

## Changes
- Version comparison at attach time and on version reports
- Non-destructive restart prompt for externally-attached cores

## Testing
- Fixture-driven XCTest for the supersede state machine
- Manual old-DMG -> new-DMG upgrade QA
```
