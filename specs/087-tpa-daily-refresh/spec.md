# Feature Specification: Daily Offline-First Refresh of the TPA Signature Bundle + mcpproxy Self-Update Awareness

**Feature Branch**: `087-tpa-daily-refresh`
**Created**: 2026-07-16
**Status**: Draft
**Input**: A once-per-day, non-blocking, offline-FIRST refresh that (a) updates the tpa-db signature bundle — bundled-with-binary default, manual file-drop override, and an OPTIONAL verified/signed network fetch, versioned against the tpa-db `SCHEMA_VERSION`, failing safe to last-known-good; and (b) checks for a new mcpproxy release and surfaces it, respecting the existing update-check/distribution mechanism (no auto-installer).

## User Scenarios & Testing *(mandatory)*

<!--
  The tpa-db pillar (ROADMAP.md:365-388) delivers a versioned, offline-first TPA
  signature database consumed by the deterministic detect engine (Spec 076/077).
  This feature is the "out-of-band refresh (offline-friendly)" task of that pillar.
  It sits on top of the Scanner Bundle Contract (tpa-db/specs/002-scanner-export/
  contracts/scanner-bundle.contract.md) and the existing update-check mechanism
  (Spec 079, internal/updatecheck). Each story below is an independently shippable
  slice: P1 needs zero network, P2 adds an opt-in verified fetch, P3 is pure
  surfacing of an already-existing capability.
-->

### User Story 1 - Fully-offline bundle refresh: bundled default + manual file-drop (Priority: P1)

An operator runs mcpproxy on an air-gapped or network-restricted host. The binary already ships with an embedded, known-good TPA signature bundle, so the offline scanner has current signatures on first launch with no network and no setup. Periodically the operator receives a newer `scanner-bundle.json` out of band (USB, internal artifact mirror, config-management push) and drops it into the mcpproxy data directory. Once per day — and on demand — mcpproxy notices the dropped file, validates it (version compatibility, every pattern compiles, and it passes the recall/false-positive gate), and either activates it or, if it fails any check, keeps the currently-active bundle and records why. The scanner never runs stale or broken signatures, and never regresses below the built-in detection bar.

**Why this priority**: This is the MVP and the whole point of "offline-first". It delivers a refreshable signature database with zero network dependency and zero new attack surface, and it is the foundation both other stories build on. Without it there is no bundle lifecycle at all. It is also the only story that is unconditionally on by default.

**Independent Test**: Build the binary with an embedded bundle; start with no network and assert the offline scanner loads the embedded bundle and flags a known TPA fixture. Drop a newer valid `scanner-bundle.json` into the data dir, trigger a refresh, and assert the new bundle becomes active and its new signature fires. Drop a bundle with an unsupported `bundle_version`, an uncompilable pattern, and (separately) one that regresses recall below the gate — assert each is rejected and the previously-active bundle stays active with a recorded reason. No network, no live upstream server required.

**Acceptance Scenarios**:

1. **Given** a freshly installed binary with no config and no network, **When** mcpproxy starts, **Then** the offline scanner is backed by the embedded default bundle (the one compiled into the binary at build time), reports its `bundle_version`, and produces the same findings the built-in checks plus embedded signatures define.
2. **Given** a `scanner-bundle.json` placed in the configured bundle path whose `bundle_version` major/minor the loader supports and all of whose regex rules compile under RE2, **When** the daily refresh (or a manual refresh) runs and the candidate passes the activation self-check gate, **Then** the candidate becomes the active bundle atomically, the change is logged with old→new `bundle_version`, and a new signature present only in the candidate now fires on a matching tool.
3. **Given** a dropped bundle whose `bundle_version` major/minor the loader does not recognize, **When** refresh runs, **Then** the loader refuses to load it, keeps the last-known-good active bundle, and records a "version unsupported" reason — it does NOT silently run partial or stale rules (Contract §4).
4. **Given** a dropped bundle in which one or more `pattern`s fail to compile under RE2, **When** refresh runs, **Then** each failure is logged and counted as a load error (Contract §5), the whole candidate is rejected as not-known-good, and the active bundle is unchanged.
5. **Given** a dropped bundle that compiles and version-matches but would drop recall below the gate threshold or push hard-negative false positives above it on the embedded self-check corpus, **When** refresh runs, **Then** the candidate is rejected, the active bundle is unchanged, and the regression is recorded (no silent regression).
6. **Given** the refresh runs, **When** a candidate is evaluated or activated, **Then** the discovery/scan hot path is never blocked: refresh runs off the request path and a scan in flight either uses the previous active bundle or the newly-activated one, never a half-loaded state.
7. **Given** a candidate bundle whose `rules[]` contain `engine: structural_diff` entries (`runtime: "stateful"`), **When** it is loaded for the offline tier, **Then** those rules are treated as not-runnable (skipped, not counted as clean coverage) per Contract §1.3/§5, and their presence does not fail the load.

---

### User Story 2 - Optional signed network fetch with verification and last-known-good fallback (Priority: P2)

An operator on a connected host wants signatures to stay current without manual file drops. They opt in to a network refresh source (a URL to a signed bundle artifact) and supply a trusted public key. Once per day mcpproxy fetches the candidate bundle, verifies its detached signature against the configured key, then runs it through the same version + compile + recall/FP gate as a manual drop before activating it. Any failure — network error, signature mismatch, tampered/truncated payload, version mismatch, gate regression — leaves the currently-active bundle in place. The feature is strictly opt-in and off by default; nothing about P1's offline behavior changes when it is disabled.

**Why this priority**: Automated freshness is valuable but must not weaken the security posture. A network fetch introduces egress and a supply-chain trust boundary, so it is opt-in, signature-verified, and gated by exactly the same activation self-check as the offline path. It is P2, not P1, because the product is fully functional and secure without it (P1 covers air-gapped and manual flows), and because it depends on P1's loader/validator/activation machinery.

**Independent Test**: Serve a correctly-signed candidate bundle from a local test server; enable the fetch with the matching public key; run a refresh and assert the candidate is verified, gated, and activated. Then serve (a) an unsigned bundle, (b) a bundle signed by a different key, (c) a byte-corrupted bundle, and (d) a 404/timeout; assert each is rejected, the active bundle is unchanged, and a reason is recorded. Assert that with the fetch disabled, no network request is ever made.

**Acceptance Scenarios**:

1. **Given** network refresh is disabled (default), **When** mcpproxy runs, **Then** no update request is ever issued to any bundle source and behavior is identical to User Story 1.
2. **Given** network refresh is enabled with a source URL and a trusted public key, **When** the daily fetch retrieves a bundle whose detached signature verifies against the configured key AND which passes the version + compile + recall/FP gate, **Then** it is activated as last-known-good and the previous bundle is retained as the rollback target.
3. **Given** a fetched bundle whose signature does not verify against the configured key (missing signature, wrong key, or altered bytes), **When** refresh runs, **Then** the candidate is discarded before any pattern is compiled or activated, the active bundle is unchanged, and a "signature verification failed" reason is recorded.
4. **Given** a network error (unreachable host, timeout, non-200, oversized response), **When** the fetch runs, **Then** the refresh fails closed to the active bundle, the failure is rate-limited in logs, and the next daily tick retries — startup and scanning are never blocked on the fetch.
5. **Given** a fetched, verified bundle that fails the activation self-check gate, **When** refresh runs, **Then** it is rejected exactly as a failing local drop would be (User Story 1, scenario 5) and the active bundle is retained.

---

### User Story 3 - Daily mcpproxy release-availability awareness (Priority: P3)

An operator wants to know when a newer mcpproxy release is available — including releases that ship updated built-in detections — without the proxy silently reaching out or, worse, auto-installing anything. mcpproxy checks for a newer release on a daily cadence using the existing update-check mechanism and surfaces the result (current version, latest version, whether an update is available, and the channel-appropriate upgrade command) on the surfaces that mechanism already feeds. It never downloads or installs a binary; it respects the existing opt-out.

**Why this priority**: The capability to check for and surface a new release already exists (Spec 079, `internal/updatecheck`); this story reuses it rather than reinventing it, and adds only alignment with the daily refresh cadence and co-presentation next to bundle-refresh status. It is P3 because it is the smallest-value, lowest-risk slice and is almost entirely surfacing of existing behavior. It explicitly must NOT introduce an auto-installer, because the repo has no self-install mechanism to respect — the existing mechanism surfaces guidance only.

**Independent Test**: With a build whose version is older than the latest published release, run the release check and assert the surfaced info reports an available update with the correct channel-appropriate upgrade command and release URL, and that no binary is downloaded or executed. With the update check disabled (config or `MCPPROXY_DISABLE_AUTO_UPDATE`), assert no network request is made and no update nudge appears.

**Acceptance Scenarios**:

1. **Given** update checking is enabled and the running version is older than the latest release on the resolved channel, **When** the daily release check runs, **Then** the surfaced status reports current version, latest version, "update available", the release URL, and the channel-appropriate upgrade command — and no binary is fetched or run.
2. **Given** update checking is disabled by config (`update_check.enabled=false`) or by `MCPPROXY_DISABLE_AUTO_UPDATE`, **When** the daily cycle runs, **Then** no release request is issued and no update nudge appears on any surface (parity with Spec 079 FR-015).
3. **Given** both a bundle refresh and a release check occur in the same daily cycle, **When** their results are surfaced, **Then** bundle status (active `bundle_version`, last refresh outcome, last-known-good) and release status appear as distinct, clearly-labeled items — a bundle update and a binary update are never conflated.

---

### Edge Cases

- **No embedded bundle available at build time**: the build MUST fail rather than ship a binary whose offline scanner has no signatures; a released binary always has a known-good embedded default.
- **Bundle path points at a directory, a zero-byte file, or malformed JSON**: treated as "no valid candidate"; the active (embedded or previously-activated) bundle is retained and a reason recorded — never a crash and never an empty rule set.
- **Dropped bundle is byte-identical to the active one**: refresh is a no-op (idempotent); no re-activation, no log spam, determinism preserved.
- **Candidate `schema_version`/`bundle_version` newer minor than the loader knows but same major**: forward-compatible per Contract §4 — unknown additional keys on rule objects are ignored, but an unknown major/minor the loader does not support is refused (fail-closed).
- **Two refresh triggers race** (daily tick coincides with a manual refresh): refresh is single-flight; concurrent triggers coalesce and only one activation happens per distinct candidate.
- **Activation self-check corpus would be vacuous** (no gated-malicious sample or no hard-negative sample available to the self-check): the self-check fails closed — the candidate is NOT activated, mirroring the eval gate's exit-4 vacuity guard.
- **Clock skew / suspended laptop**: the daily cadence is a best-effort interval, not a wall-clock guarantee; a missed tick simply runs at the next opportunity and never runs more than one activation per candidate.
- **Signed-fetch enabled but no public key configured**: the fetch refuses to run (fail-closed) rather than accept an unverified bundle; a clear configuration error is surfaced.
- **Hot-reload flips the bundle path or fetch source**: the running refresher re-applies the new settings without a restart; an in-flight refresh completes against the settings it started with.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The binary MUST embed a known-good default TPA signature bundle at build time so the offline scanner has current signatures with no network and no configuration on first launch; a release build with no embeddable bundle MUST fail to build.
- **FR-002**: The offline scanner MUST consume signatures only via the compiled bundle contract (`scanner-bundle.json` shape) and MUST NOT parse signature source YAML, matching the tpa-db Scanner Bundle Contract.
- **FR-003**: The system MUST resolve the active bundle from a precedence order: a validated operator-supplied bundle at the configured bundle path overrides the embedded default; the embedded default is the fallback when no valid external bundle is present.
- **FR-004**: The system MUST run a bundle refresh on a daily cadence (a configurable best-effort interval, default once per day) AND on demand, off the scan/discovery hot path, so a refresh never blocks startup, a tool call, or a scan.
- **FR-005**: Before activating any candidate bundle (embedded, dropped, or fetched), the loader MUST verify `bundle_version` compatibility and refuse a bundle whose major/minor it does not support, keeping the last-known-good active (Contract §4). It MUST NOT silently run stale or partial rules.
- **FR-006**: The loader MUST compile every `engine: regex` rule's `pattern` under Go's RE2 at load time; any pattern that fails to compile MUST be logged and counted as a load error, and the candidate MUST be rejected rather than partially loaded (Contract §5).
- **FR-007**: The loader MUST treat `engine: structural_diff` (`runtime: "stateful"`) rules as not-runnable in the offline tier — skipped, not counted as clean coverage, and never causing a load failure (Contract §1.3/§5). Whether stateful manifest-diff detection is wired to the existing integrity baseline is explicitly out of scope for this feature.
- **FR-008**: A candidate bundle MUST pass an activation self-check — a recall/false-positive gate over an embedded labeled corpus using the same pass/fail logic and thresholds as the CI eval gate (recall ≥ 0.90 over gated categories, hard-negative false-positive rate ≤ 0.05) with the candidate's rules layered onto the built-in checks — BEFORE it becomes active. A candidate that regresses below the built-in bar MUST be rejected (no silent regression).
- **FR-009**: On ANY validation failure (version, compile, signature, gate, or I/O), the system MUST fail safe to the currently-active bundle (last-known-good), record a machine-readable reason and timestamp, and continue operating; it MUST NOT fall back to an empty rule set.
- **FR-010**: Bundle activation MUST be atomic with respect to concurrent scans: a scan MUST see either the fully-previous or the fully-new rule set, never a partially-swapped state; refresh MUST be single-flight (concurrent triggers coalesce).
- **FR-011**: Refresh MUST be idempotent: a candidate byte-identical to the active bundle MUST NOT trigger re-activation or duplicate log output, preserving the contract's byte-identical determinism guarantee (Contract §3).
- **FR-012**: The optional network fetch MUST be disabled by default and strictly opt-in via configuration; when disabled, the system MUST issue no bundle-fetch network request at any time.
- **FR-013**: When the network fetch is enabled, the system MUST verify a detached cryptographic signature of the fetched bundle against an operator-configured trusted public key BEFORE compiling patterns or activating, and MUST reject any bundle that is unsigned, signed by a non-configured key, or altered. The signing/verification scheme (signature format, algorithm, key distribution) MUST be specified in the design; the constitution does not define one.
- **FR-014**: When the network fetch is enabled but no trusted public key is configured, the fetch MUST fail closed (refuse to activate any fetched bundle) rather than accept an unverified one.
- **FR-015**: The network fetch MUST bound its blast radius: a maximum response size, a request timeout, and log-rate-limiting on repeated failures; failures MUST fail closed to the active bundle and retry on the next daily tick.
- **FR-016**: All bundle-refresh behavior MUST be configuration-driven via `mcp_config.json` (bundle path, refresh enable/interval, fetch enable, source URL, trusted public key) with environment-variable override and hot-reload without restart, consistent with the existing security config block.
- **FR-017**: The system MUST surface bundle status — active `bundle_version`, source (embedded/file/fetch), last refresh timestamp and outcome, last-known-good version, and skipped/rejected reasons — on an existing observability surface (the info/status endpoint and logs), so operators can see whether signatures are current and why a candidate was rejected.
- **FR-018**: The daily cycle MUST check for a newer mcpproxy release using the existing update-check mechanism (`internal/updatecheck`, Spec 079) and MUST surface current version, latest version, update-available state, release URL, and the channel-appropriate upgrade command; it MUST NOT download, verify, or install any binary (no auto-installer) and MUST respect the existing opt-out (`update_check.enabled=false` / `MCPPROXY_DISABLE_AUTO_UPDATE`).
- **FR-019**: Bundle-update status and mcpproxy-release status MUST be surfaced as distinct, clearly-labeled items so a signature-database update is never conflated with a binary release update.
- **FR-020**: The bundle check MUST integrate with the deterministic detect engine as one additional check in the existing in-process scanner check set, emitting hard-tier signals for rule hits (fail-closed: any single rule hit denies auto-approve), so bundle hits drive the same quarantine gate as the built-in hard checks with no separate verdict plumbing.
- **FR-021**: The embedded self-check corpus and any redistributed bundle MUST carry provenance/license metadata sufficient to redistribute (matching the corpus provenance requirement of the eval foundation); community-contributed signatures without a redistributable license MUST NOT be embedded.
- **FR-022**: Existing scan entry points (CLI `security scan`, the REST scan endpoint, the `quarantine_security` MCP tool) MUST continue to function unchanged from the caller's perspective, now additionally informed by the active bundle's signatures.

### Key Entities *(include if feature involves data)*

- **Signature Bundle**: the compiled, versioned `scanner-bundle.json` — `bundle_version`, `schema_version`, `signature_count`, `rules[]`, `skipped[]` — that the offline scanner consumes. Immutable once loaded; identified by its version and content hash.
- **Bundle Rule**: one offline-runnable detector within a bundle (`id`, `detector`, `engine`, `target`, `confidence`, `level`, `category`, and `pattern` for regex / `rule`+`runtime` for structural_diff). Regex rules run against a tool surface (e.g. `tool_description`); structural_diff rules are not-runnable in the offline tier.
- **Bundle Source**: where a candidate bundle came from — `embedded` (compiled into the binary), `file` (operator drop at the configured path), or `fetch` (verified network download). Precedence: a validated `file`/`fetch` candidate overrides `embedded`.
- **Bundle Signature**: the detached cryptographic signature over a fetched bundle plus the operator-configured trusted public key used to verify it; the trust anchor for the network path.
- **Refresh Scheduler**: the background, best-effort daily ticker (mirroring the existing update checker's Start/ticker/hot-reload lifecycle) that triggers refresh + release-check off the hot path and coalesces concurrent triggers into a single-flight refresh.
- **Activation Self-Check**: the recall/false-positive gate run over an embedded labeled corpus, using the same thresholds and pass/fail logic as the CI eval gate, that a candidate must pass before it becomes active.
- **Active Bundle / Last-Known-Good**: the currently-serving bundle and the retained rollback target; every failed candidate leaves the last-known-good active.
- **Release Availability**: current version, latest version on the resolved channel, update-available flag, release URL, and channel-appropriate upgrade command — surfaced, never acted upon.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A freshly installed binary on a host with no network detects a known TPA fixture using the embedded default bundle on first launch, with zero configuration.
- **SC-002**: A newer valid bundle dropped at the configured path becomes active within one refresh cycle (and immediately on a manual refresh), and a signature present only in that bundle then fires — with no proxy restart.
- **SC-003**: 100% of invalid candidates (unsupported version, uncompilable pattern, failing recall/FP gate, unverified signature, network error) leave the previously-active bundle serving; in no case does the scanner fall back to an empty or partial rule set.
- **SC-004**: No candidate bundle that would drop recall below 0.90 over gated categories or raise hard-negative false-positive rate above 0.05 on the embedded self-check corpus is ever activated — measured by rejecting a deliberately-regressing fixture bundle.
- **SC-005**: With the network fetch disabled, no bundle-fetch network request is observed over a full day of operation; with it enabled, a bundle whose signature does not verify against the configured key is never activated.
- **SC-006**: A refresh (including fetch, verify, and self-check) never blocks a scan or tool call: p99 scan latency during an active refresh is indistinguishable from steady state.
- **SC-007**: When a newer release exists, the surfaced status reports it with the correct channel-appropriate upgrade command and release URL, and no binary is ever downloaded or executed by this feature.
- **SC-008**: The CI eval gate (`cmd/scan-eval --gate --min-recall 0.90 --max-fp 0.05`) continues to pass with the embedded default bundle's rules layered onto the built-in checks, proving the shipped default cannot regress the build.

## Assumptions

- The tpa-db Scanner Bundle Contract (`scanner-bundle.json`, `bundle_version`/`schema_version` from `SCHEMA_VERSION`) is stable and is the sole handoff format; mcpproxy never parses signature YAML.
- The existing deterministic detect engine, its `Check` interface, the two-tier (hard/soft) enforcement, the quarantine state machine, and the CI eval gate (`cmd/scan-eval`, recall ≥ 0.90 / hard-negative FP ≤ 0.05) are reused unchanged; the bundle plugs in as one additional hard-tier check.
- The existing update-check mechanism (Spec 079, `internal/updatecheck`) is the ONLY release-awareness mechanism; there is no binary self-installer in the repo and this feature does not add one — it surfaces upgrade guidance only.
- The bundle only ADDS signatures on top of the built-in checks; it never removes or overrides a built-in check, so the built-in detection bar is a floor the activation self-check enforces.
- "Daily" is a best-effort interval consistent with the existing background-checker pattern, not a wall-clock cron guarantee.
- The offline scanner operates on tool metadata surfaces reachable today (tool description/schema); `resource_content` and `server_manifest` targets, and stateful `structural_diff` detection, are out of scope for this feature (skipped per contract) and left to a later spec.

## Out of Scope

- Wiring stateful `structural_diff` / manifest-diff detection into the integrity baseline (the offline tier skips those rules).
- Adding `resource_content` / `server_manifest` tool surfaces to the scanner's data model.
- Any binary self-update/auto-installer for mcpproxy itself (surfacing an available release is the only release behavior).
- Authoring or expanding the seed TPA corpus/signatures themselves (that is the separate tpa-db seed-corpus task); this feature consumes and refreshes whatever bundle the corpus produces.
- Changing the recall/FP thresholds or the CI eval gate's logic; this feature reuses them verbatim.
- Preserving a bundle rule's declared `level` verbatim through the finding severity (severity remains derived from tier + summed confidence by the existing aggregator).

## Constitution Check *(note)*

This feature is governed by the mcpproxy constitution (v1.1.0). Principle IV (Security by Default) is central: the network fetch is opt-in, signature-verified, and fails closed; every refresh path fails safe to last-known-good; and no candidate can regress below the built-in detection bar (activation self-check). Principle III (Configuration-Driven Architecture) requires the bundle path, refresh/fetch toggles, source URL, and trusted key to live in `mcp_config.json` with env override and hot-reload — no hardcoded paths or URLs. Principle V (TDD) applies: the loader, verifier, and self-check gate are built test-first with valid/invalid fixture bundles, and the embedded default must keep the CI eval gate green. Principle I (Performance at Scale) requires refresh to stay off the scan hot path with atomic, single-flight activation. The constitution defines no signing requirement, so FR-013 specifies the signature/verification contract explicitly rather than relying on constitution text.

## Commit Message Conventions *(mandatory)*

Use `Related #[issue-number]` (never `Fixes`/`Closes`/`Resolves`, which auto-close on merge). Do NOT add `Co-Authored-By: Claude <noreply@anthropic.com>` or the "Generated with Claude Code" trailer. Follow the repository conventional-commit format (`feat(security): …`, `fix(security): …`, `test(security): …`, `docs: …`) with a Changes and Testing section in the body.
