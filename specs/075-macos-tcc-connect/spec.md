# Feature Specification: macOS TCC-safe Connect wizard & App-Data denial diagnostics

**Feature Branch**: `075-macos-tcc-connect`
**Created**: 2026-06-17
**Status**: Draft
**Input**: User description: "macOS TCC-safe Connect wizard and App-Data denial diagnostics — make Connect status determine 'installed' via os.Stat metadata only, defer config content-reads to explicit per-client action, surface EPERM/TCC denials with actionable remediation, and add a `mcpproxy doctor` check for persisted macOS App-Data TCC denial."

## Context (why this exists)

On macOS 14+ (Sonoma / Sequoia / Tahoe), the "App Data" privacy permission (`kTCCServiceSystemPolicyAppData`) shows the dialog **"<app> wants to access data from other apps"** when an app *reads the content* of another app's data. It gates content reads (`open`+read), **not** metadata operations (`stat`/`access`).

Today, the Connect feature — which links MCP clients (Claude Desktop, Cursor, VS Code, Codex, Gemini, OpenCode, etc.) to mcpproxy — reads the **full contents** of every installed client's config file every time the Connect status is requested, just to decide whether each client is already connected. On macOS this fires the privacy prompt for apps the user never asked to touch. If the user clicks **"Don't Allow"**, the decision is remembered by the OS: every future read fails silently, Connect shows everything as "not connected," and connect/disconnect actions fail without a clear reason.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Viewing Connect status without a privacy storm (Priority: P1)

A macOS user opens the Connect page (or the status is polled). They want to see which MCP clients are installed and which are connected to mcpproxy, **without** macOS popping a "wants to access data from other apps" prompt for every client they have installed.

**Why this priority**: This is the root cause of the reported problem and affects every macOS user with multiple MCP clients installed. It is the difference between Connect being usable out-of-the-box and Connect triggering a wall of privacy prompts (and silent breakage after a denial).

**Independent Test**: Load Connect status on a machine with several client configs present and confirm that determining the "installed" state performs only metadata checks (no content reads), so no privacy prompt is raised by simply viewing status.

**Acceptance Scenarios**:

1. **Given** several MCP client config files exist on disk, **When** the user requests overall Connect status, **Then** each client is reported as installed/not-installed using only file-existence (metadata) checks and no client config content is read.
2. **Given** the user has not acted on any specific client, **When** Connect status is computed, **Then** no client config file is opened for reading.
3. **Given** a client is installed, **When** the user explicitly requests that one client's detailed status (or connects/disconnects it), **Then** that single client's config is read at that moment, scoped to the user's action.

---

### User Story 2 - Clear, actionable message when macOS blocks access (Priority: P2)

A user (who previously denied the prompt, or whose OS silently blocks the background process) tries to connect or refresh a specific client. The access is blocked by macOS. They want to understand *why* and *exactly how to fix it*, instead of a silent "not connected" or a confusing generic failure.

**Why this priority**: Once access is denied, the feature is broken in a way that is invisible and undiagnosable to the user. Turning a silent failure into an actionable message is high value but only matters after Story 1 reduces how often the denial happens.

**Independent Test**: Simulate a permission-denied error on a client config read/write and verify the surfaced status/error names the privacy cause and gives the precise remediation steps.

**Acceptance Scenarios**:

1. **Given** reading a client config is blocked by a macOS permission denial, **When** that client's status is computed or a connect/disconnect is attempted, **Then** the result is reported as a distinct "blocked by macOS privacy" state — not "not connected" and not a generic error.
2. **Given** a privacy-denied result, **When** the message is shown, **Then** it states the cause ("macOS blocked access to <client>'s data") and the remediation (enable mcpproxy under System Settings → Privacy & Security → App Data, or the exact `tccutil reset` command).
3. **Given** a config file is simply absent, **When** status is computed, **Then** it is reported as "not installed" and is clearly distinguished from a privacy denial.
4. **Given** a config file exists but is malformed, **When** it is read on an explicit action, **Then** it is reported as "unreadable/malformed" and clearly distinguished from a privacy denial.

---

### User Story 3 - Doctor flags a persisted privacy denial (Priority: P3)

A user whose Connect feature mysteriously shows nothing runs the health-check command. They want it to tell them, in one line, that a macOS privacy denial is the cause and how to reset it.

**Why this priority**: A diagnostic convenience that turns a confusing state into a one-command fix. Valuable, but the in-feature message (Story 2) already covers the primary path.

**Independent Test**: Run the health check on macOS in a state representing a persisted App-Data denial and confirm it reports the issue with the remediation command; run it on a non-macOS platform and confirm it is a no-op.

**Acceptance Scenarios**:

1. **Given** the platform is macOS and a persisted App-Data denial affecting Connect is present, **When** the health check runs, **Then** it reports a warning naming the privacy denial and the exact remediation command.
2. **Given** the platform is not macOS, **When** the health check runs, **Then** the App-Data check is skipped (no-op) and does not appear as a failure.
3. **Given** macOS with no relevant denial, **When** the health check runs, **Then** the App-Data check passes (or is silent) and does not produce a false warning.

---

### Edge Cases

- A client config path is a symlink into a protected location (e.g. a sandbox container): existence is still determined by metadata only; content is only read on explicit action.
- The same status request covers a mix of: installed+readable, installed+denied, installed+malformed, and absent clients — each must be reported in its correct distinct state.
- The background (non-GUI) process cannot raise a prompt and is silently denied: the denied state must still be detectable and surfaced (no infinite silent failure).
- Connect/disconnect *writes* (not just reads) are blocked by the denial: the write failure must surface the same actionable privacy message.
- A denial that the user later resets externally: subsequent explicit actions must work again without restarting mcpproxy.
- Non-macOS platforms: behavior is unchanged; existence + on-demand reads remain functionally equivalent and no privacy-specific messaging appears.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Connect overall status MUST determine each client's "installed" state using file-existence/metadata checks only, and MUST NOT read any client config file's contents.
- **FR-002**: Reading a client's config contents (to detect whether mcpproxy is already configured in it) MUST occur only in response to an explicit, per-client user action (connect, disconnect, or an explicit single-client detailed-status request).
- **FR-003**: The system MUST classify a client-config access into distinct outcomes: accessible, absent (not installed), permission-denied (privacy), and malformed/unreadable.
- **FR-004**: When a client-config read or write fails due to a permission denial, the system MUST surface a distinct "blocked by macOS privacy" outcome rather than reporting "not connected" or a generic error.
- **FR-005**: The privacy-denied message MUST name the cause and provide remediation: enabling mcpproxy under System Settings → Privacy & Security → App Data, and the exact reset command including the relevant bundle identifier(s).
- **FR-006**: The existing Connect REST API surface (overall status, per-client connect, per-client disconnect) MUST remain backward compatible; the status payload MAY add fields but MUST NOT remove or repurpose existing ones.
- **FR-007**: The health-check command MUST include a macOS-only check that detects a persisted App-Data privacy denial affecting Connect and reports it with the remediation command.
- **FR-008**: The health-check App-Data check MUST be a no-op on non-macOS platforms and MUST NOT produce false warnings when no relevant denial exists.
- **FR-009**: Behavior on Linux and Windows MUST be functionally preserved (existence via metadata, content read on explicit action); no privacy-specific prompts or messaging apply there.
- **FR-010**: The Docker binary discovery probe MUST remain unchanged (it already uses metadata-only checks and is out of scope).
- **FR-011**: The privacy-denied classification MUST be derived from the operating system's permission-denied signal for a file access (not from string-matching arbitrary error text).
- **FR-012**: All new behavior MUST be covered by automated tests, including a test that injects a permission-denied error for a config access without requiring a real OS denial.

### Key Entities *(include if feature involves data)*

- **Client status**: Per MCP client — identity/name, installed (exists) flag, connected flag, and an access-state describing whether its config was readable, absent, privacy-denied, or malformed, plus any remediation hint.
- **Access outcome**: The classification of an attempt to read/write a client config — one of accessible, absent, permission-denied, malformed.
- **Doctor check result**: A health-check finding for the macOS App-Data denial — pass/warn, human-readable summary, and remediation command.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Viewing overall Connect status reads **zero** client config file contents (100% of installed/not-installed determinations come from metadata), verified by test.
- **SC-002**: On macOS, simply viewing Connect status raises **no** "access data from other apps" prompts for clients the user has not explicitly acted on.
- **SC-003**: 100% of permission-denied config accesses are reported as the distinct privacy state with remediation text, not as "not connected" or a generic error.
- **SC-004**: The four access outcomes (accessible, absent, privacy-denied, malformed) are each reported correctly and distinctly in tests covering all four.
- **SC-005**: The health-check App-Data check correctly reports denial-present (warn) and denial-absent (pass) on macOS and is a no-op on other platforms, verified by test.
- **SC-006**: The existing Connect REST endpoints continue to pass their current contract/integration tests with no breaking changes.

## Assumptions

- "Installed" for a client is adequately determined by the presence of its known config file path(s); no content read is required to know a client exists.
- "Connected" detection requires reading config content and is therefore acceptable to defer to an explicit per-client action; overall status may show "connected: unknown / needs check" for installed-but-not-yet-read clients until the user acts, rather than eagerly reading.
- The relevant bundle identifiers for the remediation command are mcpproxy's release and development identifiers; the message will include the applicable one(s).
- Permission denials surface as the OS "operation not permitted / permission denied" error class on a file open/read or write; this is the signal used for classification.
- The health check's detection of a *persisted* denial is best-effort (it may infer from a representative blocked access) and must never produce a false positive when access is fine.

## Out of Scope

- Changes to the Docker well-known-path probe (already metadata-only and TCC-safe).
- Changes to app signing, notarization, or entitlements.
- Automating the granting of Full Disk Access or any privacy permission.
- Changing how clients other than the existing supported set are discovered.

## Commit Message Conventions *(mandatory)*

### Issue References
- ✅ **Use**: `Related #696` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #`, `Closes #`, `Resolves #`

### Co-Authorship
- ❌ **Do NOT include** `Co-Authored-By: Claude` or "Generated with Claude Code" trailers.

### Example Commit Message
```
feat(connect): stat-only status + on-demand reads + TCC denial surfacing

Related #696

Connect status now determines installed-state via metadata only and reads
client config contents only on explicit per-client action, eliminating the
macOS "access data from other apps" prompt storm. Permission denials are
classified distinctly and surfaced with remediation.

## Changes
- Connect status: existence via stat; content read deferred to explicit action
- Access-outcome classification (accessible/absent/denied/malformed)
- EPERM/TCC-aware error surfacing with remediation
- doctor: macOS App-Data denial check (no-op elsewhere)

## Testing
- Unit tests incl. injected permission-denied path
- Existing Connect REST contract tests pass
```
