# Feature Specification: Scanner Simplification — Deterministic Default, Opt-In Deep Scan

**Feature Branch**: `077-scanner-simplification`
**Created**: 2026-06-30
**Status**: Draft
**Input**: User description: "Make the deterministic offline detect engine the reliable default scanner (zero Docker), demote the heavy Docker scanners + source-code extraction to an opt-in deep scan that never blocks or degrades the baseline, and produce a single unified report."

## Overview

MCPProxy's security scanning has accreted three overlapping layers: a deterministic offline detection engine (Spec 076), a set of duplicate legacy phrase/secret rules layered on top of it, and six third-party scanners that run in Docker containers. For most users this is unreliable and confusing: Docker is required but frequently absent, source-code extraction from MCP servers fails intermittently, findings from different scanners never merge into one report, and a scan that cannot run every scanner reports a confusing "degraded" verdict accompanied by a storm of notifications.

This feature makes the **deterministic offline engine the always-on default** that works with zero external dependencies, demotes everything heavy to an **opt-in "deep scan"** that can never block or worsen the baseline verdict, and presents **a single unified report** regardless of how many scanners ran.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Reliable offline scan with no Docker (Priority: P1)

A user installs MCPProxy on a machine that does not have Docker. They add an MCP server. The proxy scans the server's tools and returns a clear, deterministic verdict (clean / warning / dangerous) for every server — with no setup, no containers, and no "degraded" or "scan failed" noise.

**Why this priority**: This is the core of the feature and the MVP. The majority of users have no Docker; today they get degraded/failed scans. A trustworthy zero-dependency baseline is the single most important outcome.

**Independent Test**: On a host with Docker uninstalled, add several MCP servers (including one with a poisoned tool description) and confirm every server receives a deterministic verdict, the poisoned one is flagged dangerous, and no scanner-failure or degraded state appears.

**Acceptance Scenarios**:

1. **Given** a host without Docker, **When** a server's tools are scanned, **Then** the scan completes with a definitive `clean`/`warning`/`dangerous` status and reports no failed or degraded scanners.
2. **Given** the same tool set scanned twice, **When** results are compared, **Then** the verdict and findings are identical (deterministic).
3. **Given** a tool whose description contains a high-confidence injection phrase (e.g. an instruction to ignore prior instructions and exfiltrate data), **When** it is scanned, **Then** the tool is flagged `dangerous` and approval is blocked.
4. **Given** a benign tool whose description merely mentions instructions in a non-malicious way, **When** it is scanned, **Then** it is NOT blocked (no false-positive hard finding).

---

### User Story 2 - One unified, readable report (Priority: P2)

A user opens a server's security report and sees a single consolidated list of findings with clear severities, no matter how many scanners contributed. When two scanners independently flag the same issue, that agreement is reflected as higher confidence rather than as duplicate entries.

**Why this priority**: Users currently cannot make sense of fragmented, per-scanner output. A single merged report is what makes scan results actionable.

**Independent Test**: Run a scan with both the baseline engine and (separately) an enabled deep scan; confirm the report shows one deduplicated finding list, that a finding flagged by two sources appears once with elevated confidence, and that every finding carries a clear severity.

**Acceptance Scenarios**:

1. **Given** findings produced by multiple scanners, **When** the report is assembled, **Then** findings referring to the same issue at the same location appear exactly once.
2. **Given** the same issue flagged independently by two scanners, **When** the report is assembled, **Then** the merged finding's confidence is higher than either source alone.
3. **Given** any finding in the report, **When** a user views it, **Then** it shows a clear severity and is attributable to its source(s).

---

### User Story 3 - Opt-in deep scan that never hurts the baseline (Priority: P2)

A power user with Docker enables "deep scan" to get source-level analysis. The extra findings appear in the same report. Later, Docker becomes unavailable or source extraction fails — the baseline verdict is unchanged, and the only signal is a quiet "deep scan unavailable" note, never a degraded verdict or a notification storm.

**Why this priority**: Deep analysis must remain available for advanced users without re-introducing the fragility that motivated this work. Isolating it from the baseline is what makes the baseline trustworthy.

**Independent Test**: Enable deep scan and confirm source-level findings merge into the report; then disable Docker mid-use and confirm the baseline verdict is identical to the deep-scan-off baseline and only an informational "deep scan unavailable" indicator is shown.

**Acceptance Scenarios**:

1. **Given** deep scan is disabled (default), **When** a server is scanned, **Then** only the baseline engine runs and no Docker is invoked.
2. **Given** deep scan is enabled and Docker is available, **When** a server is scanned, **Then** deep findings merge into the same unified report.
3. **Given** deep scan is enabled but Docker is absent or a deep scanner fails, **When** a server is scanned, **Then** the baseline verdict is unaffected and the failure is surfaced as an informational "deep scan unavailable" note (not `degraded`/`failed`).
4. **Given** a deep scanner's source extraction fails for one server, **When** the scan completes, **Then** the baseline still produces a deterministic verdict for that server.

---

### User Story 4 - Quiet, trustworthy notifications (Priority: P3)

A user reconnecting many servers, or re-scanning, receives at most one settled result notification per server instead of a flood of per-scanner progress messages.

**Why this priority**: The notification storm (tracked as MCP-2207) erodes trust and buries real signal. Fixing it is high-value polish but depends on the status model from US1–US3.

**Independent Test**: Trigger a reconnect storm across multiple servers and confirm each server emits a single settled scan result rather than repeated start/progress/complete events.

**Acceptance Scenarios**:

1. **Given** multiple servers reconnecting in quick succession, **When** scans run, **Then** each server produces a single settled scan notification.
2. **Given** a scan in progress, **When** it completes, **Then** the user sees one terminal result rather than separate per-scanner lifecycle messages.

---

### Edge Cases

- A remote MCP server has no extractable source code → deep scan reports nothing scannable for that server; baseline still produces a verdict from tool definitions.
- A configuration file uses the old `scanner_fetch_package_source` / `scanner_disable_no_new_privileges` keys → they are migrated to the new deep-scan settings with identical effect; no manual edit required.
- A configuration file references the removed `auto_scan_quarantined` key → it is ignored without error.
- All deep scanners are enabled but Docker is missing → baseline verdict is normal; deep scan shows as unavailable.
- A previously legacy-blocked phrase is no longer in the curated high-confidence set → it surfaces as a review-only warning rather than a hard block (documented posture change).

## Requirements *(mandatory)*

### Functional Requirements

**Baseline scanner (default)**
- **FR-001**: The system MUST run a deterministic, offline baseline scanner for every server's tools that requires no Docker, network, or external process.
- **FR-002**: The baseline scanner MUST be the only in-process detection engine; the duplicate legacy phrase rules and the duplicate legacy embedded-secret path MUST be removed without loss of detection coverage.
- **FR-003**: The baseline MUST produce identical results for identical inputs (determinism), verifiable across repeated runs.
- **FR-004**: The system MUST preserve today's protective posture by treating a curated, high-confidence set of injection/exfiltration phrases as blocking (hard tier), while broader, lower-confidence phrasing remains review-only (soft tier).
- **FR-005**: The baseline MUST NOT produce a false-positive blocking finding on benign tool descriptions that merely resemble injection phrasing.

**Deep scan (opt-in)**
- **FR-006**: Heavy scanners (the Docker-based scanners and source-code extraction) MUST be opt-in and disabled by default.
- **FR-007**: A deep scan failure (Docker absent, extraction failure, scanner error, sandbox isolation) MUST NOT change the baseline verdict.
- **FR-008**: Deep scan availability MUST be reported as a distinct, informational dimension separate from the baseline verdict; the system MUST NOT downgrade an otherwise-clean baseline to "degraded" because a deep scanner did not run.
- **FR-009**: When enabled and available, deep scan findings MUST merge into the same unified report as baseline findings.

**Unified report**
- **FR-010**: The system MUST present a single report per server that consolidates findings from all scanners that ran.
- **FR-011**: Findings referring to the same issue at the same location MUST be deduplicated into one entry.
- **FR-012**: When multiple independent scanners agree on a finding, the merged finding's confidence MUST be increased to reflect that consensus.
- **FR-013**: Every finding MUST carry a clear, user-readable severity and be attributable to its contributing source(s).

**Status, notifications, configuration**
- **FR-014**: The server scan verdict (`clean`/`warning`/`dangerous`) MUST be derived solely from baseline findings.
- **FR-015**: The system MUST emit at most one settled scan result notification per server per scan, including during reconnect storms.
- **FR-016**: The system MUST remove the unused `auto_scan_quarantined` configuration key and ignore it if present in existing configs.
- **FR-017**: The system MUST provide a single deep-scan configuration group (default off) that subsumes the previous package-source-fetch and privilege-hardening settings, and MUST migrate the old keys to it transparently on load.
- **FR-018**: All bundled Docker scanners MUST default to disabled; the deterministic in-process scanner MUST default to enabled.

**Unchanged surfaces**
- **FR-019**: The tool-level quarantine state machine (hash-based pending/changed/approved gating) MUST remain unchanged; this feature changes how tools are scanned, not how quarantine enforces approvals.
- **FR-020**: The existing quarantine/security management surfaces (the security CLI commands and the quarantine management tool) MUST continue to function and MUST read from the unified report.
- **FR-021**: The approval gate MUST block on baseline `dangerous` findings only; deep-scan findings inform but do not gate approval.

### Key Entities

- **Scan Report**: The single per-server result. Carries the overall verdict (clean/warning/dangerous), a consolidated finding list, and a separate deep-scan availability descriptor.
- **Finding**: One detected issue. Has a rule identity, a location, a severity, a confidence, and one or more contributing sources.
- **Deep-Scan Descriptor**: Informational status of the opt-in layer: whether it is enabled, whether it ran, whether it was available, and which scanners (if any) failed.
- **Baseline Engine**: The deterministic, offline detection engine that produces the verdict.
- **Deep Scanner**: An opt-in heavy scanner (Docker-based or source-extracting) that contributes enrichment findings only.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: On a host without Docker, 100% of added servers receive a deterministic verdict and 0% report a "degraded" or "scanner failed" state.
- **SC-002**: Repeated scans of the same tool set produce identical verdicts and findings 100% of the time.
- **SC-003**: Detection recall for the curated hard-tier checks is ≥ 0.90 and the hard-negative false-positive rate is ≤ 0.05, measured against the evaluation corpus, with no regression versus the pre-change blocking behavior on the shared corpus.
- **SC-004**: After removing the duplicate legacy rules, no previously-detected attack in the evaluation corpus goes undetected (zero coverage loss).
- **SC-005**: A scan that includes a failed or unavailable deep scanner yields a baseline verdict identical to the same scan with deep scan disabled, in 100% of cases.
- **SC-006**: A reconnect storm across N servers produces at most N settled scan notifications (one per server), eliminating the per-scanner notification flood.
- **SC-007**: Existing configurations using the deprecated keys load without manual changes and behave identically after migration.
- **SC-008**: When two scanners agree on a finding, the unified report shows one entry with higher confidence than either source reports alone.

## Assumptions

- The Spec 076 deterministic detection engine already provides coverage equivalent to (or better than) the legacy phrase and embedded-secret rules being removed; the curated hard-tier phrase set closes the one behavioral gap (legacy rules were blocking, detect's equivalents were review-only).
- Most MCP servers scanned in practice are remote or otherwise have no extractable source, so the baseline (tool-definition) scan is the meaningful signal for the majority of users; deep scan is genuinely supplementary.
- The existing unified-report data contract (normalized finding + summary + risk-score with consensus weighting) is sufficient as the single report format and does not need a redesign.
- Demoting the Docker scanners to opt-in is acceptable security posture for the default experience, because the deterministic baseline covers tool-poisoning, shadowing, hidden-unicode, decoded-payload, secret, and curated injection-phrase classes offline.

## Out of Scope

- Removing or rewriting the Docker scanner plugins (they are retained, just opt-in).
- Changing the tool-level quarantine state machine or its hashing/approval logic.
- Redesigning the registry / add-server flow (separate initiative).
- Adding new third-party scanners or new detection categories beyond the curated hard-tier phrase check.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #`, `Closes #`, `Resolves #` - These auto-close issues on merge

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

### Example Commit Message
```
feat(security): deterministic baseline scanner default; deep scan opt-in

Related #[issue-number]

Make the offline detect engine the always-on default and demote Docker
scanners + source extraction to an opt-in deep scan that never blocks or
degrades the baseline verdict. Unify all findings into a single report.

## Changes
- Remove duplicate legacy tpaRules + legacy embedded-secret path
- Add curated hard-tier phrase_injection check
- Separate deep-scan availability from baseline verdict
- Migrate deprecated config keys into security.deep_scan

## Testing
- Offline determinism, no-coverage-loss, deep-scan-isolation, config migration
```
