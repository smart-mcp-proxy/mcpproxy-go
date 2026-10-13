# Feature Specification: Release Qualification Gate — Automated QA Matrix That Blocks the Tag

**Feature Branch**: `081-release-qa-gate`
**Created**: 2026-07-06
**Status**: Draft
**Input**: User description: "Build a release-qualification gate that blocks a release tag until, automatically: a matrix of surfaces (MCP tools, REST API, CLI, Web UI, macOS app) crossed with server types (stdio, http, sse, docker, oauth) passes, plus invariants: every tool call appears in the activity log with a request id; token counters and analytics/telemetry counters move on calls; a new server lands in quarantine and the approval flow works end-to-end; all five server types connect, list tools, and survive reconnect; an in-place upgrade preserves config, data, and index; tray/Web UI/CLI agree with the core on server states; and the scanner eval gate stays green (recall ≥ 0.90, FP ≤ 5%). Assemble the existing suites (test-api-e2e.sh, race tests, Playwright sweep, mcpproxy-ui-test, scan-eval) instead of reinventing them. Priority: server-type matrix with logging/token invariants first, macOS smoke second."

## Overview

MCPProxy ships releases to a user base that mostly gives it exactly one chance: telemetry over the 60-day human cohort (n=**1082**, measured 2026-07-06) shows only **17.7%** of installs return for a second day, and **75.5%** of all-time installs (n=1459) have exactly one day of heartbeats ever. A regression that ships in a release is therefore not a bug a user reports — it is a user who silently never comes back (hypothesis H1, breakage-driven churn, is checked by looking for churn clusters within 7 days of a release). Yet release qualification today is manual and ad-hoc: the v0.47.0-rc.1 cut (2026-06-29) needed **5 successive CI-fix commits** on its final pre-tag PR (#782) to go green — and the RC line still ran to rc.3 — while v0.46.0 (2026-06-25) shipped through a notarization failure discovered only mid-pipeline. Nothing between "tag pushed" and "artifacts published" proves the product actually works.

A substantial amount of QA automation already exists — it is just not assembled into a gate, and none of it blocks a tag:

- `scripts/test-api-e2e.sh` — a bash API E2E harness that boots a built `./mcpproxy` binary on port 8081 against `test/e2e-config.template.json` and exercises the REST API with curl; run on push/PR by `.github/workflows/e2e-tests.yml` (which also builds the frontend and runs Go E2E suites). It covers **stdio and http** upstreams only.
- `go test -race ./internal/... ` — unit + race coverage via `.github/workflows/unit-tests.yml`.
- `.github/workflows/eval.yml` ("Eval (Spec 065 regression gate)") — the `security-d2` job runs `go run ./cmd/scan-eval --gate --min-recall 0.90 --max-fp 0.05` as a hard, offline, blocking check (Spec 076 US3), but only on path-filtered PRs (security/index/server/CLI paths) plus a nightly schedule — never on release tags.
- The Playwright Web UI sweep (`docs/development/web-ui-verification.md`) — **manually triggered only**; required by convention when touching `frontend/src/`, never run on a tag.
- `mcpproxy-ui-test` — an accessibility-driven MCP server that can read the macOS status bar, click menu items, and screenshot the tray; today it is used **interactively only**, and the macOS app has zero CI automation beyond `test-macos-build.yml` (build-only).
- `.github/workflows/release.yml` publishes on `v*` tags (stable; `v*-rc.*` goes through `prerelease.yml`) with **no qualification dependency** — a tag push goes straight to build-and-publish.

The gaps, stated honestly: there is no single release-gate workflow; the E2E matrix does not cover **sse, docker, or oauth** server types; the invariants that define "the product works" (activity log completeness, token/telemetry counters moving, quarantine + approval end-to-end, upgrade-in-place preserving `~/.mcpproxy` config + BBolt data + Bleve index, surface-state agreement) are checked nowhere; and the macOS app is never exercised in CI.

This feature builds a release qualification gate that is **automated, assembled, blocking, and honest**: one workflow, triggered by the release tag, that runs the existing suites unchanged, adds the missing server-type matrix and invariant checks, and makes artifact publication depend on the verdict — so a red gate means no release, mechanically. It reuses `test-api-e2e.sh`, the race tests, `scan-eval --gate`, the Playwright sweep, and `mcpproxy-ui-test` rather than reinventing them; every skipped or degraded cell is reported explicitly (no silent skips). It does NOT replace the per-PR CI workflows, does not test against live third-party upstreams (all matrix upstreams are local fixtures, including a mock OAuth IdP), and does not attempt full GUI-driving of the macOS app — the macOS job is a smoke test, initially advisory.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - One gate workflow: existing suites + full server-type matrix block the tag (Priority: P1)

A maintainer pushes a release tag (`v0.48.0` or `v0.48.0-rc.1`) and walks away. A single `release-qa-gate` workflow starts automatically, runs the existing suites (`scripts/test-api-e2e.sh`, `go test -race ./internal/...`, `scan-eval --gate`) plus a new server-type matrix — stdio, http, sse, docker, and oauth upstream fixtures, each proven to connect, list tools, serve a tool call, and survive a forced reconnect — and produces a single pass/fail verdict. The publish jobs in `release.yml` / `prerelease.yml` only start after the gate passes; if any matrix cell fails, the tag produces no artifacts and the maintainer gets a report naming the exact cell and its logs.

**Why this priority**: This is the MVP and the load-bearing mechanism — everything else in this spec is a check that plugs into this gate. Per the prior decision, the server-type matrix ships first because sse/docker/oauth are the three connection paths users hit that CI has never exercised, and connection breakage is the leading H1 churn suspect (`upstream_connect_timeout/refused`, `oauth_refresh_failed`, and `docker_pull/run_failed` are all among the 11 heartbeat error categories in `internal/telemetry/error_categories.go`).

**Independent Test**: Push a test tag on a branch where one matrix cell is deliberately broken (e.g. the sse fixture returns malformed frames) and verify the gate fails, names the cell, and no publish job runs; fix the fixture, re-push, and verify all five server types go green and publishing proceeds. Verify separately (negative case) that a plain push to `main` does not trigger the gate — it is tag-scoped.

**Acceptance Scenarios**:

1. **Given** a `v*` tag is pushed, **When** CI evaluates workflows, **Then** the release qualification gate runs automatically and every publish job is ordered strictly after a successful gate verdict.
2. **Given** the gate is running, **When** its suite jobs execute, **Then** they invoke the existing `scripts/test-api-e2e.sh`, `go test -race ./internal/...`, and `cmd/scan-eval --gate --min-recall 0.90 --max-fp 0.05` entry points (not reimplementations), and a failure in any one fails the gate.
3. **Given** the server-type matrix, **When** it runs, **Then** each of stdio, http, sse, docker, and oauth fixtures is started locally, and for each the gate verifies: connection reaches Ready, the fixture's tools are listed and indexed, at least one tool call round-trips successfully, and after the fixture process/container is killed and restarted the server reconnects and serves a call again.
4. **Given** any matrix cell fails after retries, **When** the gate concludes, **Then** the verdict is FAIL, the report identifies the failing cell (surface × server type × step) with logs, and no release artifact is published for that tag.
5. **Given** all gate jobs pass, **When** the gate concludes, **Then** a machine-readable report (per-cell status, durations, retry counts, skip reasons) is uploaded as a workflow artifact and the publish pipeline proceeds without human action.

---

### User Story 2 - Invariant checks: the product provably works, not just responds (Priority: P1)

A maintainer trusts the gate not because endpoints return 200, but because the gate asserts the invariants that define correct end-to-end behavior: every tool call made during the matrix run appears in the activity log with its request id; token usage counters and telemetry counters move when calls happen; a newly added server lands in quarantine and the approval flow releases it; and an in-place upgrade from the previous released version onto the candidate binary preserves config, BBolt data, and the Bleve index.

**Why this priority**: Shares P1 with US1 per the prior decision ("server-type matrix with logging/token invariants first"). A matrix that only checks liveness would still have passed several shipped regressions; the invariants are what convert "it starts" into "it works". These checks piggyback on the traffic US1 already generates, so they land in the same milestone at marginal cost.

**Independent Test**: Run the gate's invariant job against a candidate binary and verify each invariant is asserted with a real negative: drop an activity-log write (or point at an empty log) and confirm the request-id completeness check fails; freeze counters and confirm the counters-move check fails; pre-approve the fixture server and confirm the quarantine check fails because no quarantine transition was observed; corrupt the upgrade-in-place fixture's index and confirm the preservation check fails.

**Acceptance Scenarios**:

1. **Given** the matrix run has completed its tool calls, **When** the activity-log invariant runs, **Then** it confirms 100% of issued tool calls appear in the activity log, each correlated by the `X-Request-Id` the caller observed, and any missing or uncorrelated call fails the gate.
2. **Given** baseline counter snapshots taken before the matrix traffic, **When** the counters invariant runs, **Then** token usage counters and analytics/telemetry counters have strictly increased in line with the calls made; **When** any counter is flat despite traffic, **Then** the gate fails.
3. **Given** a fresh fixture server added mid-run via the management API, **When** the quarantine invariant runs, **Then** it observes the server enter quarantined state, its tools blocked from execution, the approval action succeed, and a post-approval tool call round-trip — the full Spec 032 flow, end-to-end.
4. **Given** a data directory (config + `config.db` + `index.bleve/`) produced by the previous released version, **When** the candidate binary starts against it in place, **Then** startup succeeds, all configured servers are retained, stored approvals/quarantine state are intact, and search over the existing index returns results — with zero destructive migration; **When** any of these is lost, **Then** the gate fails.
5. **Given** the scanner eval gate, **When** the release gate runs, **Then** `scan-eval --gate` green (recall ≥ 0.90, FP ≤ 5%) is required on the tag itself, regardless of which files the release changed.

---

### User Story 3 - Web UI sweep wired into the gate (Priority: P2)

A maintainer no longer remembers to run the Playwright sweep by hand before a release. The existing Web UI verification sweep (`docs/development/web-ui-verification.md`) runs as a gate job against the candidate binary's embedded frontend, exercising the core screens (servers list, server detail, quarantine review, settings) against the same fixture upstreams the matrix started — and a broken Web UI blocks the tag exactly like a broken API.

**Why this priority**: P2 because it hardens an existing, proven asset rather than closing a never-tested path; the Web UI already gets per-PR attention when `frontend/src/` changes. Its gate value is catching the embed/build-integration class of failure (stale `web/frontend/dist/`, API-key bootstrap, SSE wiring) that per-PR frontend CI does not see because it never runs against the release-built binary.

**Independent Test**: Run the gate with a candidate binary whose embedded frontend was deliberately built from a broken bundle (or omitted) and verify the Web UI job fails the gate with screenshots; run with a healthy build and verify the sweep passes and its report is attached to the gate artifact.

**Acceptance Scenarios**:

1. **Given** a candidate binary with the frontend embedded, **When** the gate's Web UI job runs, **Then** the Playwright sweep drives the served Web UI (not a dev server) through the core screens against live fixture upstreams, and any failed check fails the gate.
2. **Given** the sweep completes, **When** the gate report is assembled, **Then** the sweep's HTML report and failure screenshots are included as artifacts.
3. **Given** the Web UI displays server state, **When** the sweep inspects the servers list, **Then** the states shown match the states the matrix fixtures are actually in.

---

### User Story 4 - macOS app smoke on a macOS runner (Priority: P3)

A maintainer gets at least a heartbeat-level guarantee about the macOS app before it ships to the DMG pipeline: on a `macos` runner, the gate launches the tray app against a running core and uses the `mcpproxy-ui-test` accessibility primitives (list running apps, read status bar, list menu items, screenshot) to assert the tray is present, its menu renders the expected top-level items, and its displayed server state agrees with the core.

**Why this priority**: P3 per the prior decision ("macOS smoke second") and because it is the most operationally fragile job: macOS runners are slow and scarce, and `mcpproxy-ui-test` has only ever been driven interactively. It therefore starts **advisory** (reported, not blocking) with an explicit promotion criterion, so its flakiness cannot hold stable releases hostage while it matures.

**Independent Test**: On a macOS runner, run the smoke job against a healthy build and verify it reports the tray present with expected menu items; then run it against a build where the tray binary is absent and verify the job reports failure with a screenshot artifact — while (during the advisory phase) the overall gate verdict is unaffected but the report clearly flags the advisory failure.

**Acceptance Scenarios**:

1. **Given** a macOS runner with the candidate core and tray builds, **When** the smoke job runs, **Then** it starts the core headless, launches the tray, and asserts via accessibility APIs that the status bar item exists and the menu lists the expected top-level entries.
2. **Given** the tray is running, **When** the smoke reads the tray's displayed server states, **Then** they match `mcpproxy upstream list` output from the core.
3. **Given** the smoke job is in its advisory phase, **When** it fails, **Then** the gate verdict is unchanged but the gate report prominently records the advisory failure with artifacts; **When** the promotion criterion is met (see FR-021), **Then** the job becomes blocking.

---

### User Story 5 - Surface-state consistency check (Priority: P3)

A maintainer knows the surfaces do not lie to each other: at a defined checkpoint during the matrix run, the gate snapshots server state from the core's REST API, the CLI (`mcpproxy upstream list -o json`), and the Web UI (via the Playwright sweep), plus the tray where US4 runs, and asserts they agree on every server's name, admin state, and health level. Divergence — the class of bug where the tray shows "connected" while the core reports "degraded" — blocks the tag.

**Why this priority**: P3 because it depends on US1 (fixtures), US3 (Web UI reading), and partially US4 (tray reading), and because state-divergence bugs, while real (the tray is a stateless REST/SSE mirror by design and has drifted before), are lower-frequency than connection breakage. It is cheap once the other stories exist: it is one comparison over data the other jobs already collect.

**Independent Test**: With the matrix fixtures in a known mixed state (one connected, one quarantined, one deliberately stopped), collect the three (or four) surface snapshots and verify the consistency check passes; then stub one surface's snapshot to report a stale state and verify the check fails naming the surface, the server, and both conflicting values.

**Acceptance Scenarios**:

1. **Given** matrix fixtures in a mixed state, **When** the consistency checkpoint runs, **Then** REST, CLI, and Web UI snapshots agree on each server's identity, admin state (enabled/disabled/quarantined), and health level, using the unified `health` contract.
2. **Given** any two surfaces disagree after state-settling retries, **When** the check concludes, **Then** the gate fails with a diff naming the surface, server, field, and both values.
3. **Given** the macOS smoke job ran (US4), **When** its tray snapshot is available, **Then** it participates in the same comparison; **When** it did not run, **Then** the check proceeds over the remaining surfaces and records the tray as not-compared (never silently passed).

---

### Edge Cases

- **Flaky fixture or timing-dependent step**: any matrix cell MAY be retried at most 2 times; a pass-on-retry is recorded as `flaky` in the gate report (never hidden), and a cell that is `flaky` in 3 consecutive gate runs MUST be surfaced as a tracked defect → retries mask transient noise, never trends.
- **Docker unavailable or broken on the runner**: the docker matrix cell MUST fail the gate — not skip — because Docker isolation is a shipped feature; the failure message MUST distinguish "runner has no Docker" (infrastructure, fix the workflow) from "mcpproxy failed to use Docker" (product regression).
- **OAuth without a live IdP**: the oauth cell runs against a local mock IdP fixture implementing the OAuth 2.1 + PKCE flow the client already speaks (`internal/oauth/`); no test may depend on a third-party identity provider, and the mock MUST also exercise token refresh so `oauth_refresh_failed`-class regressions are caught.
- **RC vs stable tags**: the gate runs identically for `v*-rc.*` (before `prerelease.yml` publishes) and stable `v*` (before `release.yml` publishes); an RC that passed the gate does NOT exempt the later stable tag — the stable tag is re-qualified on its own commit.
- **Gate red for infrastructure reasons** (runner outage, GitHub cache failure): re-running the failed workflow on the same tag re-qualifies it; there is no force-publish path that bypasses the gate short of editing the workflow, and any such edit is visible in the tag's history.
- **Upgrade-in-place fixture drift**: the previous-version data directory fixture MUST be produced by actually running the previous released binary (downloaded by version pin), not by a hand-crafted snapshot, so schema drift in BBolt/Bleve is caught rather than assumed away.
- **Runtime budget exceeded**: the blocking portion of the gate targets ≤ 30 minutes wall clock; if a job hangs, per-job timeouts fail it explicitly rather than letting the tag sit in limbo.
- **macOS runner unavailable**: while advisory (US4), the smoke job's inability to schedule is recorded as `not-run` in the report and does not block; after promotion to blocking, a scheduling failure blocks like any other failure.
- **Existing per-PR workflows overlap**: `e2e-tests.yml`, `unit-tests.yml`, and `eval.yml` keep their current triggers untouched; the gate invokes the same underlying entry points on the tag, so a script fix benefits both without dual maintenance.

## Requirements *(mandatory)*

### Functional Requirements

**Gate workflow & blocking semantics**

- **FR-001**: The system MUST provide a single release qualification workflow that runs automatically on every release tag (`v*`, including `v*-rc.*`) and produces exactly one machine-readable pass/fail verdict for that tag's commit.
- **FR-001a**: The workflow MUST also be manually dispatchable against any ref (dry run) so maintainers can qualify a candidate before tagging; a dry run never publishes and never counts as qualification for a later tag.
- **FR-002**: Artifact-publishing jobs in `.github/workflows/release.yml` and `.github/workflows/prerelease.yml` MUST be ordered strictly after a successful gate verdict for the same commit; a failed or absent gate verdict MUST prevent all artifact publication for that tag.
- **FR-003**: The gate MUST invoke the existing entry points — `scripts/test-api-e2e.sh`, `go test -race ./internal/...`, `go test -tags server ./internal/serveredition/...`, and `cmd/scan-eval --gate --min-recall 0.90 --max-fp 0.05` (Spec 076 thresholds) — rather than duplicating their logic, and a failure in any of them MUST fail the gate.
- **FR-004**: The gate MUST upload a machine-readable report artifact enumerating every job and matrix cell with status (`pass|fail|flaky|skipped|not-run|advisory-fail`), duration, retry count, and — for anything other than `pass` — a reason; there MUST be no silent skips.
- **FR-005**: Each gate job MUST have an explicit timeout, and the blocking portion of the gate SHOULD complete within 30 minutes wall clock on standard runners.

**Server-type matrix**

- **FR-006**: The gate MUST run a server-type matrix covering exactly five upstream types — stdio, streamable-http, sse, docker-isolated stdio, and oauth-protected http — each backed by a local fixture started by the gate; no matrix cell MAY depend on network access to third-party services.
- **FR-007**: For each server type, the matrix MUST verify, in order: (a) the server reaches Ready state; (b) its tools are listed by the core and discoverable via `retrieve_tools`; (c) at least one tool call round-trips with a correct result through the MCP proxy; (d) after the fixture is forcibly terminated and restarted, the server reconnects and (c) succeeds again.
- **FR-008**: The oauth cell MUST use a gate-owned mock IdP implementing the authorization-code + PKCE flow and short-lived access tokens, such that the cell exercises both initial authorization and at least one token refresh during the run.
- **FR-009**: The docker cell MUST run the fixture under real Docker isolation on the runner; if Docker is unavailable, the cell MUST fail with an infrastructure-classified error (it MUST NOT skip or silently fall back to un-isolated execution).
- **FR-010**: Matrix cells MAY be retried at most twice on failure; any pass-on-retry MUST be reported as `flaky`, and the final verdict for a cell that never passes within the retry budget MUST be `fail`.

**Invariants**

- **FR-011**: The gate MUST assert that 100% of tool calls issued during the matrix run appear in the activity log, each correlated by the request id (`X-Request-Id`) observed by the caller; any missing or uncorrelated call MUST fail the gate.
- **FR-012**: The gate MUST snapshot token usage counters and analytics/telemetry counters before and after the matrix traffic and assert they increased consistently with the calls made; flat counters under traffic MUST fail the gate.
- **FR-013**: The gate MUST add a fresh fixture server via the management surface mid-run and assert the full Spec 032 flow: the server enters quarantine, its tools are blocked from execution while quarantined, the approval action succeeds, and a post-approval tool call round-trips.
- **FR-014**: The gate MUST perform an upgrade-in-place check: start the candidate binary against a data directory (config file, BBolt `config.db`, Bleve `index.bleve/`) produced by actually running the previous released version, and assert successful startup, retention of all configured servers and their quarantine/approval state, and working search over the pre-existing index.
- **FR-015**: The scanner eval gate (FR-003's `scan-eval` invocation) MUST run on the tag commit unconditionally — independent of which paths the release changed — so the recall/FP guarantee holds for every shipped release, not only when the path-filtered PR trigger happens to fire.

**Web UI, macOS, and surface consistency**

- **FR-016**: The gate MUST run the existing Playwright Web UI sweep against the Web UI served by the candidate binary (embedded frontend, not a dev server), with the matrix fixtures as its upstream data, and a sweep failure MUST fail the gate.
- **FR-017**: The Web UI job MUST attach the sweep's HTML report and failure screenshots to the gate report artifact.
- **FR-018**: The gate MUST include a surface-state consistency check that, at a checkpoint with fixtures in a known mixed state, snapshots server state from the REST API, the CLI (`mcpproxy upstream list -o json`), and the Web UI, and asserts agreement on server identity, admin state, and health level per the unified `health` contract; a post-retry divergence MUST fail the gate with a per-field diff.
- **FR-019**: The gate MUST include a macOS smoke job on a macOS runner that starts the candidate core, launches the tray app, and asserts via accessibility automation (the `mcpproxy-ui-test` primitives: running-app presence, status-bar item, menu items, screenshot) that the tray is present, renders its expected top-level menu, and displays server states matching the core.
- **FR-020**: The macOS smoke job MUST start in advisory mode: its failure is recorded (`advisory-fail`) in the gate report with artifacts but does not affect the blocking verdict.
- **FR-021**: The macOS smoke job MUST be promoted to blocking once it has passed on 3 consecutive release tags without a flaky or infrastructure failure; the promotion is a one-line workflow change and the criterion MUST be stated in the workflow file.
- **FR-022**: All new gate behavior MUST be covered by automated tests, including: fixture servers for all five types with kill/reconnect handling; the mock IdP's authorization and refresh paths; each invariant check's negative case (missing activity entry, flat counters, pre-approved server, corrupted/absent upgrade fixture data); the report generator's handling of `flaky`/`skipped`/`advisory-fail` statuses; and a workflow-level assertion (or audit test) that publish jobs depend on the gate verdict.

### Key Entities *(include if feature involves data)*

- **Gate run**: one execution of the qualification workflow bound to a single commit/tag; owns a verdict (`pass|fail`) and a report artifact.
- **Matrix cell**: the unit of qualification — a (surface × server type × step) tuple with status, duration, retry count, and logs; the report is a list of these.
- **Fixture upstream**: a gate-owned local MCP server of a given type (stdio script, http/sse server, docker image, oauth-protected server + mock IdP), start/kill-controllable so reconnect can be forced deterministically.
- **Invariant check**: a named assertion over observed system state (activity-log completeness, counter deltas, quarantine lifecycle, upgrade preservation, surface agreement) with an explicit negative case; extends nothing — it reads existing contracts (`/api/v1` responses, activity log entries, unified `health`).
- **Gate report**: the machine-readable artifact (per-cell statuses + reasons + attachments) that is the single source of truth for why a tag did or did not qualify.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Two consecutive releases (RC or stable) ship with **zero manual pre-release checklist items** performed by a human between tag push and artifact publication — down from the 100%-manual qualification baseline (2026-07-06).
- **SC-002**: Fix commits required during a release cut to get its qualifying CI green drop to **≤ 1** per release, from the **5** observed during the v0.47.0-rc.1 cut (2026-06-29).
- **SC-003**: CI coverage of upstream server types rises from **2 of 5** (stdio, http — baseline 2026-07-06) to **5 of 5**, with all five cells green on the qualifying run of the next release tag.
- **SC-004**: 100% of release tags publish artifacts only after a passing gate verdict on the same commit; 0 artifacts are ever published from a tag whose gate failed, verified by workflow-dependency test.
- **SC-005**: 100% of tool calls made during a gate run are correlated to activity-log entries by request id, and each invariant check demonstrably fails on its seeded negative case, verified by test.
- **SC-006**: 0 silent skips: every non-`pass` cell in any gate run carries an explicit status and reason in the report, including advisory macOS failures and not-compared surfaces, verified by test.
- **SC-007**: The blocking portion of the gate completes within 30 minutes wall clock on the qualifying runs of the next two release tags.
- **SC-008**: All existing per-PR workflows (`e2e-tests.yml`, `unit-tests.yml`, `eval.yml`, `sandbox-integration.yml`) continue to pass unchanged, and `scripts/test-api-e2e.sh` remains runnable standalone exactly as before — the gate consumes existing contracts without breaking them.

## Assumptions

- The gate runs on GitHub Actions; `ubuntu-latest` runners provide working Docker (proven by existing `sandbox-integration.yml` and E2E usage), and `macos` runners are available for the advisory smoke job even if slow or queued.
- Previous released binaries remain downloadable by version pin from GitHub Releases, so the upgrade-in-place fixture can always be generated from the real prior version.
- `test/e2e-config.template.json` and `scripts/test-api-e2e.sh` are stable extension points: adding fixture upstream definitions and invariant assertions there (or alongside them) is cheaper and safer than a parallel harness.
- The mock OAuth IdP only needs to cover the flows the client implements (`internal/oauth/`: authorization code + PKCE, refresh); conformance beyond what mcpproxy speaks is out of scope.
- `mcpproxy-ui-test`'s accessibility primitives work in a CI (non-interactive) macOS session; if a GUI session requirement surfaces, the advisory phase absorbs that discovery without blocking releases.
- Blocking a tag is acceptable release friction: the team prefers a delayed release over an unqualified one, per the churn evidence that shipped breakage is unrecoverable for most users.

## Out of Scope

- Replacing or re-triggering the per-PR CI workflows; they keep their current triggers and the gate only adds a tag-scoped assembly on top (FR-003 reuses their entry points; SC-008 guards the boundary).
- Windows tray/GUI automation — the Windows installer path stays qualified by build success only; a Windows smoke analogous to US4 is a follow-up once the macOS advisory job proves the pattern.
- Testing against live third-party upstreams (real GitHub MCP, real IdPs, registry servers): all matrix upstreams are local fixtures by design (FR-006); a scheduled "real world" canary is a separate concern.
- Performance/load qualification (latency budgets, soak tests): the gate proves functional correctness, not throughput.
- Notarization/signing verification (the v0.46.0 Apple-agreement class of failure): those failures occur inside the publish pipeline the gate deliberately sits before; validating Apple/SignPath outcomes is release.yml's job, not the gate's.
- Full macOS GUI regression coverage: US4 is intentionally a smoke test (presence, menu, state agreement); driving every tray flow via accessibility automation is out of scope until after FR-021 promotion.
- Auto-rollback or auto-yanking of a bad release already published: the gate prevents publication; it does not manage post-publication incident response.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #NNN` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #`, `Closes #`, `Resolves #`

### Co-Authorship
- ❌ **Do NOT include** `Co-Authored-By: Claude` or "Generated with Claude Code" trailers.

### Example Commit Message
```
feat(ci): release qualification gate blocking tags on QA matrix

Related #NNN

Add a tag-triggered release-qa-gate workflow that assembles the
existing suites (test-api-e2e.sh, go race tests, scan-eval gate,
Playwright sweep) with a new five-type upstream matrix
(stdio/http/sse/docker/oauth: connect, list, call, reconnect) and
invariant checks (activity-log request ids, counter deltas,
quarantine e2e, upgrade-in-place). Publish jobs now depend on the
gate verdict; macOS tray smoke runs advisory on a macos runner.

## Changes
- .github/workflows/release-qa-gate.yml: gate workflow + report artifact
- release.yml / prerelease.yml: publish jobs gated on verdict
- test fixtures: sse/docker/oauth upstreams + mock PKCE IdP
- invariant checks: activity log, counters, quarantine, upgrade
- macOS smoke job (advisory) via mcpproxy-ui-test primitives

## Testing
- fixture kill/reconnect and mock-IdP refresh paths
- negative cases for every invariant check
- report statuses: flaky, skipped, advisory-fail, not-run
- workflow-dependency audit: no publish without gate pass
```
