# Feature Specification: Tray Auto-Update Failure UX & Telemetry

**Feature Branch**: `095-update-failure-ux`
**Created**: 2026-08-13
**Status**: Approved (Codex cross-review, 4 rounds, VERDICT: APPROVED 2026-08-13)
**Input**: Tray auto-update failure UX and telemetry: friendlier Sparkle failure dialog with Try Again / Download from Website actions, plus privacy-safe fixed-enum update-failure telemetry delivered tray-to-core and surfaced via the daily heartbeat. macOS tray + Go core seam; verified locally incl. Sparkle test rig.

## Context & Evidence

On 2026-08-12 a real incident made both gaps visible at once:

1. A GitHub-edge network condition made the auto-update enclosure download fail repeatedly for a real installation (three attempts over ~75 minutes, all failing the same way). The stock updater alert — "Update Error! An error occurred while downloading the update. Please try again later." with a single **Cancel Update** button — was a dead end: no retry affordance, no alternative path to the new version. The user's only recovery was manual investigation.
2. We could not answer "are other users hitting this?" because no update-failure signal exists anywhere in telemetry. The daily heartbeat carries version, startup outcome, and error-category counters — but nothing about update check/download/install failures. The only available proxy (release-asset download counts) showed single-digit auto-update traffic, i.e. we would not notice even a 100% failure rate.

Both capabilities in this spec are the smallest fixes for those two gaps: give the stuck user a way forward, and give the project a way to measure how often users get stuck.

## Definitions

- **Update session**: one update attempt run by the updater framework, from check initiation to a terminal outcome. Each session has two properties:
  - **origin**: `user-initiated` (started from the menu / Try Again) or `scheduled` (background timer); fixed at session start.
  - **UI latch**: whether the updater presented any update UI during this session. The latch is one-way mutable during the session (unset → set when UI is first presented, never back), and its value is captured at occurrence time; the visibility decision (FR-002) evaluates that captured value, whether or not a window is still on screen.
- **Terminal failure (an "occurrence")**: exactly one terminal, non-cancellation, non-"no update found" outcome of one update session, as delivered by the updater framework's session-abort callback. Multiple callbacks describing the same session's failure are one occurrence. A user-selected retry starts a new session and can produce a new occurrence.
- **Eligible failures**: only terminal failures of updater-framework update sessions. Tray-synthesized advisory messages (the "core still running" postpone notice and the on-quit tripwire) are not update-session failures: they keep their existing journal-only path and are never classified, dialogued, or counted by this feature.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Recover from a failed update (Priority: P1)

A user triggers "Check for Updates…" (or an already-visible scheduled update proceeds) and the update fails — the feed can't be fetched, the download is refused, or installation can't complete. Instead of the stock dead-end alert, the tray shows a short, friendly explanation of what failed and offers direct recovery: try the update again, or download the installer from the website.

**Why this priority**: This is the user-facing incident fix. A failed update with no recovery path strands users on old versions and erodes trust in the updater; both recovery actions already exist in the product (re-check, browser download) but are unreachable from the failure moment.

**Independent Test**: Point a locally built tray at a feed whose enclosure URL is unreachable (local Sparkle test rig), run "Check for Updates…", and verify the new dialog appears with working "Try Again" and "Download from Website" actions.

**Acceptance Scenarios**:

1. **Given** an update download fails during a user-initiated session, **When** the occurrence surfaces, **Then** the tray shows exactly one dialog with a plain-language message specific to the download stage and exactly three choices: **Try Again**, **Download from Website**, and **Cancel** — and the stock "Update Error!" alert does not appear.
2. **Given** the failure dialog is shown, **When** the user chooses **Try Again**, **Then** exactly one new user-initiated update session is queued and starts (with its normal progress UI) only after the updater delivers the failed session's terminal-completion signal; a queued retry is discarded if the app quits first.
3. **Given** the failure dialog is shown, **When** the user chooses **Download from Website**, **Then** the default browser opens the highest-precedence valid download URL (per FR-005) and the dialog closes.
4. **Given** the failure dialog is shown, **When** the user chooses **Cancel**, **Then** the dialog closes and nothing else happens (parity with today's stock alert).
5. **Given** a scheduled session whose UI latch is unset fails, **When** the occurrence happens, **Then** no dialog appears while the occurrence is still recorded per User Story 2.
6. **Given** an update fails at a non-download stage (feed fetch or install), **When** the dialog appears, **Then** its message reflects that stage in plain language rather than a generic error.

---

### User Story 2 - Measure update failures in telemetry (Priority: P2)

The project maintainer can see, from received telemetry payloads, how many update-failure occurrences installations reported in their last daily window, broken down by failure stage (feed/appcast, download, install, other) — without ever receiving error text, URLs, or any other free-form content from users' machines.

**Why this priority**: Without this signal the team cannot distinguish "one developer's flaky network" from "the update path is broken for a cohort of users," and cannot verify that mitigations work. It depends on nothing in User Story 1 but shares the failure-classification seam.

**Independent Test**: With a running core, simulate a failure report for each stage, then inspect the local telemetry payload preview and confirm per-stage counts appear; confirm the payload contains no free text about the failure; confirm nothing is recorded when telemetry is off.

**Acceptance Scenarios**:

1. **Given** an eligible occurrence in the tray, **When** it is recorded, **Then** exactly one recording request reaches the core carrying only a stage value from the closed set (`appcast|download|install|other`) — no error message, URL, or version string.
2. **Given** recorded occurrences, **When** the daily telemetry heartbeat is next assembled, **Then** the per-stage occurrence counts appear in the heartbeat's existing error-code counter field (with its existing window semantics), and the existing privacy scanner accepts the payload.
3. **Given** telemetry is inactive at event time (config opt-out, environment opt-out, CI, or dev build), **When** a recording request arrives, **Then** the core acknowledges without persisting anything — an occurrence during opt-out can never become transmissible later, including after re-enabling.
4. **Given** the core is not reachable when an occurrence happens, **When** the tray attempts to record it, **Then** the dialog experience is unaffected and the tray makes exactly one bounded attempt (finite timeout, off the UI path, no retry).
5. **Given** a recorded occurrence and a core restart before the next heartbeat, **When** the heartbeat is assembled, **Then** the count is still present (recording is persisted, not memory-only).
6. **Given** a recording request with a stage outside the closed set, or with unexpected extra fields, **When** the core receives it, **Then** the request is rejected and nothing is recorded.

---

### Edge Cases

- **Repeated failures across sessions**: each occurrence increments its stage counter; dialogs appear only per the FR-002 visibility matrix, so silent background sessions never spam dialogs.
- **Try Again fails again**: a new session, a new occurrence, a new dialog; no special loop-breaking beyond what exists today.
- **No download URL known** (GitHub-releases check has nothing usable and the feed item carried no release link): "Download from Website" falls back to the fixed project releases page — the button is never hidden or disabled.
- **Failure while the core is stopping/restarting**: recording is best-effort; a lost record is acceptable, a blocked or broken dialog is not.
- **Both update brains report different versions**: the dialog concerns the failed operation only; URL selection follows FR-005's version-match rule and does not otherwise re-resolve version precedence (owned by the existing update-menu logic).
- **Old core / new tray**: the recording endpoint may not exist; any non-2xx outcome of the single attempt (404, 405, timeout, connection refused) is treated identically — one local log line, nothing user-visible (FR-016).
- **Clock changes / delayed heartbeats**: window behavior is whatever the existing local diagnostics counting machinery already does; this feature adds code values, not new window semantics.

## Requirements *(mandatory)*

### Functional Requirements

**Failure dialog**

- **FR-001**: For every eligible occurrence that the visibility matrix (FR-002) marks visible, the tray MUST present exactly one failure dialog offering exactly: **Try Again** (default action), **Download from Website**, and **Cancel** — and MUST suppress the stock error alert. At most one dialog per occurrence.
- **FR-002**: Dialog visibility MUST follow this matrix, evaluated on the failed session's origin and its UI-latch value as captured at occurrence time (see Definitions):

  | Session origin | UI latch | Dialog |
  |---|---|---|
  | user-initiated | (any) | show |
  | scheduled | set (UI was presented at any point in the session) | show |
  | scheduled | unset | suppress (record only) |

  Cancellation and "no update found" outcomes are not occurrences and never show the dialog (FR-008).
- **FR-003**: The dialog's primary message MUST be a stage-specific plain-language sentence (feed/availability check, download, installation, or a generic fallback for `other`). Secondary informative text MAY show the operating system's error description for supportability; this text is displayed locally only and is never transmitted, logged to telemetry, or included in the recording request.
- **FR-004**: **Try Again** MUST queue exactly one new user-initiated update session, started only after the updater delivers the failed session's terminal-completion signal (the update-cycle-finished callback). If that signal has already arrived when the user clicks, the retry starts immediately; a queued retry is discarded on app termination; clicking Try Again in a later dialog never stacks retries. An automated test MUST prove the retry invocation orders after the terminal-completion signal.
- **FR-005**: **Download from Website** MUST open the highest-precedence candidate from: (1) the architecture-specific installer URL from the GitHub-releases check, only when its version equals the failed session's offered version — or when the failed session has no offered version (e.g. feed-stage failure), in which case the GitHub result from the same check cycle qualifies; (2) the offered update's release-page link from the feed; (3) the fixed project releases page. Every candidate MUST be an absolute HTTPS URL; an invalid candidate or a failed browser-open falls through to the next; (3) is always valid by construction. The action MUST always be present and enabled.
- **FR-006**: All other updater UI (found-update prompt, progress, release notes, gentle reminders) MUST remain stock and unchanged.

**Failure classification**

- **FR-007**: Every eligible occurrence MUST be classified into exactly one stage of the closed set `appcast | download | install | other` by a pure, unit-testable function whose input is the failure's structured identity: the delegate callback provenance (e.g. download-specific failure callback vs. generic session abort), the error's domain and code, and the domains/codes of its underlying-error chain — never message text. Normative mapping:

  | Evidence (in precedence order) | Stage |
  |---|---|
  | download-specific failure callback fired for the session | `download` |
  | updater-framework code for appcast fetch/parse failure | `appcast` |
  | updater-framework code for enclosure download failure | `download` |
  | updater-framework code for extraction, signature/validation, staging, installation, or relaunch failure | `install` |
  | anything else (including unrecognized domains/codes) | `other` |

- **FR-008**: Recognized cancellation and "no update found" outcomes MUST be excluded before classification; they are never occurrences, never dialogs, never recorded. Tray-synthesized advisory messages (Definitions: eligible failures) are likewise excluded.

**Telemetry recording**

- **FR-009**: For every eligible occurrence the tray MUST make exactly one recording request to the core over the existing local management seam. The request body MUST be exactly `{"stage": "<appcast|download|install|other>"}` — no other fields. Transport attribution already present on every tray request (client-name header) remains transport-level and is not persisted or transmitted as part of this feature.
- **FR-010**: Recording MUST be bounded fire-and-forget: executed off the UI path, one attempt per occurrence, finite request timeout, no retry, at most one in-flight recording request per occurrence. On any failure the tray MUST log exactly one local line containing at most the stage and a numeric/status classification — never response bodies, URLs, or raw error descriptions.
- **FR-011**: The core MUST validate strictly: reject unknown stage values and requests with unexpected extra fields, recording nothing on rejection. When telemetry is active, success MUST be acknowledged only after the increment is durably persisted; the FR-013 telemetry-inactive no-op is the sole exemption and returns the same success shape (the tray cannot and need not distinguish the two).
- **FR-012**: Accepted occurrences MUST be persisted as per-stage counters through the existing local diagnostics counting machinery (four new fixed error-code values, one per stage), inheriting its existing persistence, windowing, pruning, and heartbeat surfacing semantics unchanged. The heartbeat values are occurrence counts; "how many installations were affected" is derived downstream as the number of payloads with a nonzero stage count, never by summing occurrences.
- **FR-013**: Telemetry gates MUST apply at event time: when the core's telemetry is inactive (config opt-out, environment opt-outs, CI, dev build), a recording request is acknowledged as a no-op and nothing is persisted — occurrences during opt-out can never become transmissible later, including after re-enabling. Transmission-time gates remain in force unchanged.
- **FR-014**: The transmitted representation MUST reuse the heartbeat's existing error-code counter field and therefore its existing shape validation; the only schema-adjacent change is adding the four fixed code values to the accepted catalog. The existing outbound privacy scanner MUST accept payloads carrying the new codes and MUST continue to reject free-text shapes.

**Contract & compatibility**

- **FR-015**: The recording operation MUST be documented in the API contract artifacts the repo already generates and verifies, and MUST follow the existing auth model of the management seam (local socket callers admitted as today; TCP callers require the API key). The response carries no content the tray depends on.
- **FR-016**: Version skew MUST be inert in both directions: an older tray simply never calls the endpoint; a newer tray against an older core evaluates its single attempt by HTTP status alone — any 2xx is success (response bodies are never read), and every non-success mode (404, 405, connection refused, timeout, any non-2xx) is treated identically per FR-010's single log line — no fallback endpoint, no capability probing, no user-visible effect, no change to core-health presentation.

### Key Entities

- **Update failure stage**: closed enumeration `appcast | download | install | other` — the only failure information that ever leaves the tray.
- **Occurrence**: one terminal non-cancellation outcome of one update session (see Definitions); unit of both dialog display and counting.
- **Failure record**: per-stage persisted counters owned by the core inside the existing diagnostics-counter machinery (four fixed code values), surfaced by the existing heartbeat field.
- **Failure dialog**: one modal tray dialog per visible occurrence; three fixed actions; stage-specific message.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: From the failure dialog, each recovery action completes in one click: **Try Again** queues exactly one new update attempt, and **Download from Website** invokes the browser-open on candidates in FR-005's precedence order with fall-through on invalid candidates or failed open attempts — verified by tests asserting the invocation order, not the OS-controlled outcome. (**Cancel** intentionally triggers neither.)
- **SC-002**: In the live local rig with a deliberately unreachable enclosure URL, the new dialog (not the stock alert) appears on a user-initiated check, and both recovery actions work — verified by the recorded manual protocol with screenshots.
- **SC-003**: For every simulated failure stage and callback combination in the classification table, the recorded stage matches in 100% of automated test cases; cancellations, "no update found", and tray-synthesized advisories record nothing.
- **SC-004**: After a simulated occurrence, the per-stage count is visible in the local telemetry payload preview (non-consuming) within one heartbeat assembly, and the payload passes the privacy scanner with zero free-text failure content.
- **SC-005**: With telemetry inactive by any existing gate at event time, nothing is persisted and nothing is ever transmitted for that occurrence — including after telemetry is re-enabled (disable → record → re-enable test).
- **SC-006**: Observable updater behavior outside terminal-failure handling is unchanged: all existing update-service, menu-state, and feed-URL suites pass without modification to their assertions.

## Assumptions

- The visibility matrix in FR-002 matches the stock alert's factual semantics (user-initiated always alerts; scheduled alerts only when update UI was presented, as a latched property); we change the alert's content and affordances, not its visibility policy.
- Per-stage counts via the existing diagnostics-counter machinery (existing window semantics) are sufficient granularity; no per-event timestamps, no immediate beacons, no OS/version breakdown beyond what the heartbeat already carries.
- The stage taxonomy is stable; refinements (e.g. distinguishing signature-validation from disk-space failures) are future schema work.
- Counting "Download from Website" clicks is deliberately omitted to keep the payload minimal.
- The telemetry ingest (separate repository) stores payloads wholesale and tolerates unknown code values, so the counts are present in received raw payloads as soon as this ships; queryable dashboards for them are follow-up work in that repository. This feature's outcome is the producer side: privacy-safe counts delivered in the heartbeat.

## Out of Scope

- Windows/Linux tray (`cmd/mcpproxy-tray`) update-failure UX or telemetry.
- Automatic silent retry/backoff of failed downloads (mitigation #2 from the incident review — separate feature).
- Serving update artifacts from an alternative host (mitigation #3 — separate feature).
- Telemetry ingest worker / dashboard changes (separate repository).
- Any change to when the updater shows update UI, check schedules, or the update-menu version-precedence logic (Spec 092).

## Commit Message Conventions *(mandatory)*

- Reference this feature as `Related #<issue>` (never `Fixes`/`Closes` — issues close on release, not merge).
- No AI co-authorship or attribution lines in commits or PR descriptions.
- Example: `feat(tray): friendlier update-failure dialog + failure telemetry (spec 095)`
