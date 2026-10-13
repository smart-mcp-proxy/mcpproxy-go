# Feature Specification: Security Scanner Web UI + Trust-Mode Controls

**Feature Branch**: `088-scanner-trust-ui`
**Created**: 2026-07-28
**Status**: Draft
**Input**: User description: "Security scanner Web UI + trust-mode (auto-approve) controls — close the spec-086 FR-018/SC-006 Web UI debt: tri-mode trust selector, held-evidence surfacing with TPA ids, quarantine clarity, baseline scan visibility, SSE live updates, deprecated-hint hygiene."

## Overview

Spec 086 shipped a complete trust-tiered approval backend — a per-server trust mode (`auto` | `scan` | `manual`), an offline TPA signature scanner, and hold evidence (reason, verdict, matched TPA signature ids) attached to every scan-held tool change — across REST, CLI, and MCP surfaces. The Web UI consumes none of it. Today the CLI's own remediation text tells operators to "Approve in Web UI" while the Web UI cannot show why anything is held, cannot display or change the trust mode, and hides the scan surface entirely unless an optional deep scanner is enabled. This feature closes that gap: everything an operator can learn or do about scanner verdicts and approval trust via CLI/REST becomes visible and actionable in the Web UI.

This is spec 086's own unmet requirement: its FR-018 and SC-006 name the Web UI as a required surfacing target for matched TPA ids, and its US3 acceptance scenario 4 explicitly includes the Web UI path. No new backend capability is required — the read/write surface already exists.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Control a server's trust mode from the Web UI (Priority: P1)

An operator managing MCP servers wants to decide, per server, how much autonomy the proxy has when that server's tools change: trust it fully (auto), let the offline security scan vouch for changes (scan), or review every change personally (manual). Today this choice exists only as a config-file field or an API call; the Web UI still shows a stale binary "auto-approve" toggle that cannot express the scan mode at all. The operator opens a server's configuration page, sees the current trust mode (including when it was inherited from legacy settings or is invalid), reads a plain-language explanation of each mode, and switches modes with an appropriate warning when choosing the least-safe option.

**Why this priority**: Trust mode is the control knob of the whole spec-086 feature — without UI access, the flagship "scan" mode is effectively invisible to every user who doesn't hand-edit JSON. This is the highest-value, lowest-risk slice and is independently shippable.

**Independent Test**: On a server detail page, view the current trust mode, change it to each of the three values, and confirm the change persists and is reflected on re-load and in the server list — without touching the config file or CLI.

**Acceptance Scenarios**:

1. **Given** a server with no explicit trust mode configured, **When** the operator opens the server's configuration section, **Then** they see the effective mode ("manual") presented as the current selection, with an indication that it is the default.
2. **Given** a server detail page, **When** the operator selects a different trust mode and confirms, **Then** the change is saved, a confirmation is shown (including any restart-required notice the system reports), and the new mode appears on the server list and detail views.
3. **Given** the operator selects the "auto" mode, **When** the selection is made, **Then** the UI warns — before applying — that new versions of this server's tools will be trusted without any scan (rug-pull risk) and that, at add time, auto-mode servers are admitted without quarantine or scanning.
4. **Given** a server whose configured trust-mode value is unrecognized (e.g. "bogus" from a hand-edited config), **When** the operator views the configuration, **Then** the UI shows the raw configured value AND the effective fail-closed mode ("manual"), rather than hiding or silently rewriting it.
5. **Given** a server originally configured only with legacy approval flags (whose migrated trust mode the system now reports as the server's mode), **When** the operator views the trust-mode control, **Then** that reported mode is shown as the current selection — no legacy-provenance labeling is attempted — and saving any mode from the UI writes the modern trust-mode field only.
6. **Given** the add-server form in the Web UI, **When** the operator adds a new server, **Then** they choose the trust mode at add time ("manual" preselected as the safe default), and the server's initial quarantine state follows from the chosen mode — the form no longer offers an independent quarantine checkbox that could contradict it.

---

### User Story 2 - See why a tool is held, with TPA signature ids (Priority: P1)

An operator reviewing a held tool change (new tool pending, or an existing tool whose definition changed) needs to know **why** it is held before deciding to approve or block. Today the Web UI shows only a status badge and a text diff; the evidence — whether the scan found threats vs. couldn't complete, the verdict severity, and the matched TPA-YYYY-NNNN signature ids — is REST/CLI-only. The operator opens the tool quarantine panel or the change-diff dialog and immediately sees the hold reason in plain language, a verdict badge, and the matched signature ids (known-attack TPA ids listed before generic heuristic signals), with a path to the full scan report finding for the evidence.

**Why this priority**: This is the direct FR-018/SC-006 debt: an approval decision made without visible evidence is a rubber stamp. Ships independently of US1 — evidence display needs no trust-mode control.

**Independent Test**: With a server that has a scan-held tool change (e.g. a poisoned description matching a TPA signature), open the server's tools view and the tool's diff dialog; verify hold reason, verdict, and TPA ids are visible and that the scan report is reachable from the evidence, all without CLI access.

**Acceptance Scenarios**:

1. **Given** a tool held because the scan found threats, **When** the operator views the held-tools list, **Then** each held tool shows a "scan found threats" indication, the verdict severity, and its matched signature ids with TPA-YYYY-NNNN ids listed before heuristic signal ids.
2. **Given** a tool held because the scan could not complete (coverage failure), **When** the operator views the held-tools list, **Then** the tool shows a visually distinct "scan could not complete — held as a precaution" indication with no fabricated threat claim.
3. **Given** a held tool's change-diff dialog, **When** opened, **Then** the same hold evidence (reason, verdict, signature ids) is shown alongside the before/after diff.
4. **Given** a held tool whose received signal list exceeds the display cap, **When** rendered, **Then** TPA ids among the received signals are never truncated away in favor of heuristic ids (parity with the CLI HELD-column behavior), and any "+N more" refers only to received-but-collapsed signals.
5. **Given** hold evidence referencing TPA ids and an existing scan report for that server, **When** the operator follows the evidence link, **Then** they land on the server's most recent scan report, with the matching finding highlighted when its signature ids correspond to the hold evidence.
6. **Given** a tool approved after being held, **When** the tools view refreshes, **Then** no stale hold evidence remains on the approved tool.
7. **Given** tools whose approval records predate hold evidence, **When** listed, **Then** they render exactly as before (no empty evidence chrome).

---

### User Story 3 - Understand a quarantined server's state at a glance (Priority: P2)

An operator sees a server is quarantined and needs to distinguish four very different situations: (a) an automatic scan is still running (scan mode — its result determines the next step), (b) the latest scan verdict was non-clean so automatic approval is blocked (action: review findings), (c) the latest scan failed to complete (action: retry the scan or review manually — not a threat verdict), or (d) the server awaits manual approval. Today the banner shows the same generic "Security Quarantine — this server is quarantined and requires manual approval" copy in all cases. The improved banner states which situation applies — derived only from information the server data already carries (trust mode, quarantine flag, scan outcome summary) — and shows the scan outcome (verdict, risk score, finding counts) when one exists, so the operator knows whether to wait, investigate, or approve.

**Why this priority**: Removes the most confusing ambiguity of scan mode, but depends on scan mode being reachable (US1) to matter widely; the underlying approve/reject actions already exist in the UI.

**Independent Test**: Quarantine one server in each of the four situations and verify the banner copy and scan summary differ appropriately, without reading logs.

**Acceptance Scenarios**:

1. **Given** a quarantined scan-mode server whose scan is currently running, **When** the operator views the server, **Then** the quarantine banner indicates a security scan is in progress and that its result will determine the next step — neutral copy that neither promises automatic approval (the page cannot know whether this server is still eligible for it) nor demands immediate operator action.
2. **Given** a quarantined scan-mode server whose latest scan verdict is non-clean, **When** viewed, **Then** the banner indicates the scan verdict blocked automatic approval, and shows the verdict, risk score, and finding counts with a path to the report. (The copy speaks to the verdict blocking automatic approval — it does not claim to know whether this was the original admission scan or a later re-scan, since that distinction is not available to the page.)
3. **Given** a quarantined server whose latest scan **failed** (no verdict produced), **When** viewed, **Then** the banner presents this as "the scan could not complete" — never as a threat verdict — and offers retrying the scan alongside manual review.
4. **Given** any other quarantined server (manual mode, or scan mode with no scan activity), **When** viewed, **Then** the banner indicates it awaits manual review, and shows the latest scan summary if any scan has run.
5. **Given** a quarantined server with no scan report at all, **When** viewed, **Then** the banner offers running a scan as a suggested next step alongside approval.

---

### User Story 4 - Baseline scan is visible without any optional scanner (Priority: P2)

A fresh install runs the deterministic offline baseline scanner on every applicable flow — yet the Web UI hides the entire per-server Security tab and the Scan Now action unless at least one optional deep scanner is enabled, and enabling deep scan requires hand-editing a raw config key named only in an info box. The operator on a default install opens a server page, sees the Security tab, runs a baseline scan, and views its results; separately, deep scan can be switched on from the Settings page like any other setting.

**Why this priority**: Restores truth to the default experience (the scan surface exists because scanning exists), and unblocks US2/US3 evidence flows on default installs. Lower than the P1s because scan results are partially reachable elsewhere today.

**Independent Test**: On an install with zero deep scanners enabled, open a server's Security tab, run Scan Now, and see a completed baseline report; enable deep scan from Settings without editing files.

**Acceptance Scenarios**:

1. **Given** an install with no deep scanners enabled, **When** the operator opens a server detail page, **Then** the Security tab is present and Scan Now is available.
2. **Given** the same install, **When** a scan is run, **Then** baseline results (verdict, risk score, findings) display, with any skipped optional scanners indicated as skipped rather than failing the surface.
3. **Given** the Settings page security section, **When** the operator looks for deep scan, **Then** an on/off control for the deep-scan feature exists with an explanation of what it adds, replacing the "edit this config key" instruction.

---

### User Story 5 - The page stays current without manual refresh (Priority: P3)

An operator watching a server page while a scan runs — or while a teammate/agent approves tools via CLI or MCP — expects the page to reflect the new state without a manual reload. When a scan settles or approvals change, the visible scan summary, quarantine banner, and held-tools panel refresh themselves using the live event stream the application already maintains.

**Why this priority**: Quality-of-life polish on top of the other stories; nothing is unreachable without it (manual refresh works).

**Independent Test**: With a server page open, trigger a scan and an approval from the CLI; verify the page updates within a few seconds of each event without user interaction.

**Acceptance Scenarios**:

1. **Given** an open server page and a running scan, **When** the scan settles, **Then** the scan summary and quarantine banner update within a few seconds without reload.
2. **Given** an open server page, **When** a held tool is approved via CLI or MCP, **Then** the held-tools panel reflects the approval within a few seconds of the next state broadcast.
3. **Given** the live event stream is unavailable, **When** the operator uses the page, **Then** existing manual-refresh and polling behavior still works (no regression).

---

### Edge Cases

- Configured trust-mode value is unrecognized: show raw value + effective "manual"; saving from the UI replaces the invalid value (US1 scenario 4).
- Legacy-only configuration (auto-approve flag or deprecated skip-quarantine): the system already reports the migrated mode as the server's trust mode, indistinguishable from an explicitly set one — the UI displays it as-is and writes only the modern field on save.
- Switching a quarantined server to scan mode starts an automatic scan **only** when the server has never been scanned and has no prior approval baseline; previously scanned or previously approved servers deliberately do not re-enter automatic admission. The UI must reflect the "scan running" state when a scan does start, and must not imply one will start when it won't — a manual scan remains offered.
- Hold evidence where the verdict is clean but the reason is coverage failure: must not display as a threat (US2 scenario 2).
- Signal display works on the evidence list the page receives (the stored evidence is a capped review hint — a fixed-size prefix in detection order; the full finding set lives on the scan report): TPA ids are ordered first among the received signals and are never dropped by any display cap while heuristic ids remain shown; any "+N more" count refers only to received-but-collapsed signals, never claims to count beyond the received list.
- Evidence link when no scan report exists (e.g. hold evidence persisted, report pruned): degrade gracefully — show the signal ids and offer running a scan instead of a broken link.
- Trust-mode change reports a restart requirement: surface the notice; do not imply the new mode is fully active if the system says otherwise.
- Multiple rapid scan-settled events (scan storms are already debounced upstream): the page must not thrash; one refresh per settled event is sufficient.
- Older tool approval records without hold-evidence data: render as today (US2 scenario 7).
- A server named with special characters (slashes, spaces) must work in all new views and links (existing routing convention).

## Requirements *(mandatory)*

### Functional Requirements

**Trust-mode control (US1)**

- **FR-001**: The Web UI MUST display each server's trust mode on the server detail configuration view: the reported value as-is, plus the effective fail-closed mode ("manual") whenever the reported value is not one of the three recognized modes. (No legacy-provenance labeling: the system reports migrated legacy settings as the server's trust mode, indistinguishable from explicit ones.)
- **FR-002**: The Web UI MUST allow changing a server's trust mode among exactly three values — auto, scan, manual — with plain-language descriptions covering **both** behaviors each mode governs: tool changes (auto: trusted without scanning; scan: auto-approved only on a clean offline scan verdict, otherwise held; manual: every change held) and new-server admission (auto: admitted without quarantine or scan; scan: quarantined on add with a fail-closed automatic scan; manual: quarantined on add for human review).
- **FR-003**: Selecting the auto mode MUST present a warning — covering both unscanned tool changes (rug-pull risk) and unscanned admission — before the change is applied.
- **FR-004**: Saving a trust mode from the UI MUST write the modern trust-mode field only, and MUST surface any restart-required notice returned by the system.
- **FR-005**: The trust-mode control MUST replace the legacy binary auto-approve toggle on the server configuration view.
- **FR-006**: The add-server form MUST offer the trust-mode choice with manual preselected as the default, and MUST derive the new server's initial quarantine state from the chosen mode — replacing the form's independent quarantine checkbox so the two cannot contradict each other. Other add paths (config import, registry add, onboarding) are unchanged and keep their existing safe defaults.
- **FR-007**: The server list/table view MUST show each server's trust mode at a glance.

**Held-evidence surfacing (US2)**

- **FR-008**: The Web UI surfaces that list or examine held (pending or changed) tools — specifically the server detail tool-quarantine panel, the tool change-diff dialog, and the global Tools page — MUST display the hold reason, distinguishing "scan found threats" from "scan could not complete — held as a precaution" with distinct visual treatment. Each such surface MUST source its data from a payload that actually carries the hold evidence (the export payload the current approvals panel uses does not include it). Count-only surfaces (e.g. the Dashboard pending-approvals banner) remain count-only.
- **FR-009**: Held tools MUST display their verdict severity and the matched signature ids **as received**; within the received list, TPA-YYYY-NNNN signature ids MUST be ordered before heuristic signal ids and MUST never be dropped by a display cap while heuristic ids remain shown; any overflow indicator counts only received-but-collapsed signals.
- **FR-010**: The tool change-diff dialog MUST display the same hold evidence alongside the before/after comparison.
- **FR-011**: Hold evidence MUST offer a best-effort link to the server's most recent scan report when one exists (highlighting the matching finding when the signature ids correspond), and degrade gracefully (signal ids shown, scan offered) when no report exists. Hold evidence carries no report identifier, so exact finding-level correspondence is not guaranteed and MUST NOT be implied when absent.
- **FR-012**: Approved or otherwise released tools MUST NOT display stale hold evidence; records without hold-evidence data MUST render unchanged.

**Quarantine clarity (US3)**

- **FR-013**: The server quarantine banner MUST distinguish at minimum, using only information the server payload already carries (trust mode, quarantine flag, scan outcome summary): a scan is currently running (scan mode); the latest scan verdict blocked automatic approval (scan mode, non-clean verdict); the latest scan failed to complete (failed status — presented as an incomplete scan with a retry action, never as a threat verdict); awaiting manual review (all other quarantined cases). Banner copy MUST NOT claim admission-window provenance (original admission scan vs. later re-scan) and MUST NOT promise automatic approval — neither fact is exposed to the page.
- **FR-014**: When a scan outcome exists, the quarantine banner (or its immediate context) MUST show the verdict, risk score, and finding counts, with navigation to the full report.
- **FR-015**: A quarantined server with no scan history MUST be offered a scan as a suggested action alongside approval.

**Baseline scan visibility (US4)**

- **FR-016**: The per-server security view and its scan action MUST be available regardless of whether any optional deep scanner is enabled.
- **FR-017**: Scan results on a default install MUST present baseline findings normally, indicating skipped optional scanners as skipped (not as errors).
- **FR-018**: The Settings security section MUST include an on/off control for the deep-scan feature with an explanation of its effect, replacing instructions to hand-edit configuration.

**Live updates (US5)**

- **FR-019**: The server detail view MUST refresh its scan summary, quarantine banner, and held-tools panel in response to the application's existing live events — the scan-settled event AND the server-state-changed broadcasts that accompany approvals made via CLI/MCP — scoped to the displayed server, without a full page reload. (Reacting to scan-settled alone does not cover the approval flow.)
- **FR-020**: Loss of the live event stream MUST NOT regress existing manual-refresh or polling behavior.

**Hygiene**

- **FR-021**: All Web UI guidance text recommending the deprecated skip-quarantine setting MUST be replaced with trust-mode guidance.

### Key Entities

- **Trust mode**: A per-server choice (auto | scan | manual) governing how new-server admission and tool changes are approved. Has a raw configured value (possibly absent, legacy-derived, or invalid) and an effective value (always one of the three; fail-closed to manual).
- **Hold evidence**: The reason (scan findings vs. scan coverage), verdict severity, and list of matched signature ids attached to a held tool change. TPA-YYYY-NNNN ids identify known attack signatures; other ids are heuristic checks. The stored list is a capped review hint (a fixed-size prefix in detection order, no overflow count, no report identifier); the authoritative finding set lives on the scan report.
- **Scan outcome summary**: The per-server latest scan status (including running and failed states), verdict (clean | warnings | dangerous), risk score, and finding counts, already delivered with the server data.
- **Quarantine state**: A server-level held-for-review status whose banner presentation depends on trust mode and scan state.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator can view and change any server's trust mode entirely within the Web UI in under 30 seconds, with the change verifiable on the server list without file or CLI access.
- **SC-002**: For every scan-held tool change, the Web UI shows at least the hold reason, verdict, and matched signature ids — with at least one TPA-YYYY-NNNN id visible whenever one is present in the delivered hold evidence (closes spec 086 SC-006 for the Web UI surface).
- **SC-003**: An operator shown a quarantined server can correctly identify, from the banner alone, which situation applies: a scan is in progress, the scan verdict blocked automatic approval, the scan failed to complete, or manual review is awaited — verified for all four states in QA.
- **SC-004**: On a default install with zero optional scanners enabled, an operator can run a scan and view its findings entirely from the Web UI.
- **SC-005**: With a server page open, a scan completion or a CLI-made approval is reflected on the page within 5 seconds of the application's state broadcast, without user interaction.
- **SC-006**: No Web UI text recommends the deprecated skip-quarantine setting.
- **SC-007**: The security actions this feature builds on keep working: approving/rejecting a quarantined server, approving/blocking held tools (individually and in bulk), viewing scan history, and opening scan reports all succeed after the change (their presentation is intentionally updated, their outcomes are not). Verified by: (a) the existing frontend unit-test suite passing, updated where presentation intentionally changed, and (b) new tests covering the new behaviors — trust-mode display/save states (unset, invalid, legacy-derived, explicit), both hold reasons, capped signal lists, missing scan reports, live-event refresh and event-stream loss, and special-character server names.

## Assumptions

- The backend surface shipped by spec 086 (trust-mode read/write on the server API, hold-evidence fields on tool and diff payloads, inline scan summary on server data, debounced scan-settled live event) is complete and sufficient; this feature adds no new backend endpoints. If a minor payload field addition proves necessary, it follows the established generated-contract + API-docs regeneration process.
- The existing scan report view is the destination for evidence links; no new report view is built.
- A cross-server approvals inbox is explicitly out of scope (candidate follow-up feature); the existing Dashboard pending-approvals banner remains as-is.
- Spec 087 (TPA bundle daily refresh) surfaces bundle status on the status endpoint only; this feature does not display bundle version/freshness.
- The legacy auto-approve toggle's removal from the UI does not remove the legacy API/config fields; they remain for compatibility and are simply no longer written by the UI.
- Mapping of scan-state distinctions in the quarantine banner relies on data already available client-side (trust mode, scan summary status, quarantine flag); no new server-side state machine is exposed. In particular, whether a server is inside its original admission window (vs. re-quarantined after approval) is server-internal and NOT available to the page — all banner requirements are scoped to what the payload proves.
- Hold evidence is a capped, order-of-detection prefix with no overflow count and no scan-report/finding identifier; all display requirements operate on the delivered list, and report links are best-effort to the server's latest report. Strengthening the evidence payload (producer-side TPA-first ordering before the cap, an overflow count, or a report id) would be an additive backend improvement this spec does not require.
- The add-server API's independent quarantine override remains available at the API level; only the Web UI form stops exposing a contradictory control.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat(webui): [brief description of change]

Related #[issue-number]

[Detailed description of what was changed and why]

## Changes
- [Bulleted list of key changes]
- [Each change on a new line]

## Testing
- [Test results summary]
- [Key test scenarios covered]
```
