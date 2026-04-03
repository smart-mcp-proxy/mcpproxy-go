# Feature Specification: Security Scanner Plugin System

**Feature Branch**: `feat/039-security-scanner-plugins`
**Created**: 2026-04-03
**Status**: Draft
**Input**: User description: "MCPProxy becomes a universal MCP security gateway by integrating external security scanners as Docker-based plugins. Scanners analyze quarantined servers before approval."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Install and Configure a Security Scanner (Priority: P1)

A developer using MCPProxy wants to add security scanning to their quarantine workflow. They browse the scanner registry (via CLI, Web UI, or macOS tray), find a scanner like "mcp-scan", install it with one click (Docker image pull), configure any required API keys, and see the scanner appear as "ready" in their installed scanners list.

**Why this priority**: Without at least one scanner installed, no scanning can happen. This is the foundation of the entire feature.

**Independent Test**: Can be fully tested by running `mcpproxy security scanners` to browse, `mcpproxy security install mcp-scan` to install, and verifying the scanner shows as installed via the API and CLI.

**Acceptance Scenarios**:

1. **Given** MCPProxy is running and Docker is available, **When** a user lists available scanners, **Then** the bundled registry shows all known scanners with their install status, required configuration, and descriptions.
2. **Given** a scanner is listed as "available", **When** the user installs it, **Then** the Docker image is pulled, the scanner status changes to "installed", and an activity log entry is recorded.
3. **Given** a scanner requires API keys (e.g., `MCP_SCANNER_API_KEY`), **When** the user configures it, **Then** the keys are stored securely (encrypted in the database) and the scanner status changes to "configured".
4. **Given** a scanner is installed, **When** the user removes it, **Then** the Docker image is removed, all configuration is deleted, and the scanner returns to "available" status.
5. **Given** Docker is not available on the host, **When** the user attempts to install a scanner, **Then** a clear error message explains that Docker is required for scanner execution.

---

### User Story 2 - Scan a Quarantined Server (Priority: P1)

When a new MCP server is added and enters quarantine (per existing quarantine system), the user can trigger a security scan. All enabled scanners run in parallel against the quarantined server. The user sees real-time progress and a final aggregated report with findings grouped by severity (critical/high/medium/low) and a composite risk score.

**Why this priority**: This is the core value proposition — finding vulnerabilities before approving servers.

**Independent Test**: Can be tested by adding a new server (which auto-quarantines), triggering `mcpproxy security scan <server>`, and verifying scanner containers run and produce a SARIF report that is stored and viewable.

**Acceptance Scenarios**:

1. **Given** a quarantined server and at least one installed scanner, **When** the user triggers a scan, **Then** scanner containers are created and run in parallel with appropriate isolation.
2. **Given** a scan is in progress, **When** the user checks status, **Then** they see per-scanner progress (pending/running/completed/failed) via API, CLI, or SSE events.
3. **Given** all scanners have completed, **When** the user views the report, **Then** they see aggregated findings by severity, per-scanner breakdown, tool descriptions for manual review, and a composite risk score (0-100).
4. **Given** a scanner exceeds its configured timeout, **When** the timeout fires, **Then** the scanner container is killed, the scanner is marked as "failed" with a timeout error, and remaining scanners continue.
5. **Given** `auto_scan_quarantined` is enabled in config, **When** a new server enters quarantine, **Then** a scan is automatically triggered with all enabled scanners.
6. **Given** a scan is running, **When** the user cancels it, **Then** all running scanner containers are killed and the scan is marked as "cancelled".

---

### User Story 3 - Review and Approve/Reject After Scan (Priority: P1)

After a scan completes, the user reviews findings in any UI (Web, CLI, or macOS tray). They can approve the server (stores integrity baseline, unquarantines, indexes tools), reject it (deletes server config, snapshot, and reports), or request a rescan.

**Why this priority**: The approve/reject flow is what makes scanning actionable. Without it, scan results are informational only.

**Independent Test**: Can be tested by completing a scan, then approving via `mcpproxy security approve <server>` and verifying the server becomes active with tools indexed, or rejecting and verifying cleanup.

**Acceptance Scenarios**:

1. **Given** a completed scan with no critical findings, **When** the user approves the server, **Then** an integrity baseline is stored, the server is unquarantined, tools are indexed, and an activity log entry records the approval.
2. **Given** a completed scan with critical findings, **When** the user approves without `--force`, **Then** the approval is rejected with a message listing the critical findings. With `--force`, approval proceeds with a warning.
3. **Given** a completed scan, **When** the user rejects the server, **Then** the server configuration, snapshot image, and scan reports are all deleted.
4. **Given** a completed scan, **When** the user requests a rescan, **Then** all scanners re-run against the same server (useful after scanner updates or server fixes).
5. **Given** a server was approved but scanners have been updated, **When** the user triggers a rescan on an active server, **Then** the server is re-quarantined, scanners run, and the user must re-approve.

---

### User Story 4 - Runtime Integrity Verification (Priority: P2)

After a server is approved and running, MCPProxy continuously verifies that the server's container image has not been tampered with. On each restart, the image digest is compared against the approved baseline. Periodically, filesystem changes are checked against an allowlist. Any mismatch triggers automatic re-quarantine and user notification.

**Why this priority**: Prevents post-approval tampering but requires the scanning/approval flow to exist first.

**Independent Test**: Can be tested by approving a server, modifying its container image externally, restarting the server, and verifying it is automatically re-quarantined.

**Acceptance Scenarios**:

1. **Given** an approved server with an integrity baseline, **When** the server restarts, **Then** the image digest is verified against the baseline before allowing the server to become active.
2. **Given** the image digest has changed since approval, **When** the server restarts, **Then** it is automatically re-quarantined, a notification is sent, and an activity log entry records the integrity violation.
3. **Given** `integrity_check_interval` is configured (default 1h), **When** the interval fires, **Then** a filesystem diff is run against the allowlist and any unexpected changes trigger an alert.
4. **Given** an integrity violation has occurred, **When** the user views the integrity status, **Then** they see the type of violation (digest mismatch or filesystem change), the expected vs. actual values, and when it was detected.

---

### User Story 5 - Security Dashboard and Multi-UI (Priority: P2)

Users can view security status across all servers from any interface: Web UI shows a dedicated Security page with stats cards, scanner health, and recent scans; CLI provides `mcpproxy security overview`; macOS tray shows security alerts in the menu and a Security sidebar item in the main window. All UIs are powered by the same REST API and receive real-time updates via SSE.

**Why this priority**: Dashboard and multi-UI are important for usability but depend on the core scan/approve flow being functional.

**Independent Test**: Can be tested by running scans on multiple servers and verifying the dashboard shows correct aggregated stats via API, CLI table output, and web UI rendering.

**Acceptance Scenarios**:

1. **Given** scans have been run on multiple servers, **When** the user views the security overview, **Then** they see total scans, findings by severity, risk distribution, and scanner health across all servers.
2. **Given** a scan is in progress, **When** the user is on any UI, **Then** they receive real-time SSE events for scan progress, completion, and failures.
3. **Given** findings exist for servers, **When** the macOS tray menu is opened, **Then** a "Security" section shows servers needing review with finding counts.
4. **Given** the CLI is used, **When** `mcpproxy security report <server>` is run, **Then** the output supports table (default), JSON, YAML, and raw SARIF formats.

---

### User Story 6 - Custom Scanner Registration (Priority: P3)

Power users can register custom scanners not in the bundled registry. They provide a scanner manifest (Docker image, input types, command, env requirements) via the API, CLI, or Web UI "Add Custom Scanner" form.

**Why this priority**: Extensibility for enterprise/custom scanners, but most users will use bundled registry scanners.

**Independent Test**: Can be tested by creating a custom scanner manifest, registering it, installing it, and running a scan with it.

**Acceptance Scenarios**:

1. **Given** a user has a custom scanner Docker image, **When** they register it with a valid manifest, **Then** it appears in the scanner list alongside registry scanners.
2. **Given** a custom scanner manifest is missing required fields, **When** registration is attempted, **Then** validation errors clearly indicate which fields are missing.
3. **Given** a custom scanner is registered and installed, **When** a scan is triggered, **Then** the custom scanner runs alongside registry scanners with identical isolation and timeout handling.

---

### Edge Cases

- What happens when Docker daemon is not running? System reports clear error with instructions; scanner features are gracefully disabled.
- What happens when a scanner image pull fails (network error, auth required)? Installation is marked as "failed" with the error message; user can retry.
- What happens when all scanners fail during a scan? The scan job is marked as "failed"; user can still approve with `--force` but gets a prominent warning.
- What happens when a scan is triggered but no scanners are installed? Clear error message: "No scanners installed. Run `mcpproxy security install <scanner-id>` to get started."
- What happens when multiple scans are triggered for the same server? Only one scan per server at a time; subsequent requests return the existing job ID.
- What happens when the scanner registry file is corrupted? Falls back to the bundled default registry and logs a warning.
- What happens when scanner API keys are rotated? User reconfigures via `mcpproxy security configure <scanner-id>`; existing scan results are unaffected.
- What happens during MCPProxy upgrade with existing scan data? Data model migrations preserve existing baselines and reports.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST provide a scanner registry listing all known security scanners with their metadata (name, vendor, description, input types, license, configuration requirements).
- **FR-002**: System MUST support installing scanners by pulling their Docker images, with progress feedback.
- **FR-003**: System MUST securely store scanner configuration (API keys, secrets) in the existing encrypted database.
- **FR-004**: System MUST execute scanners in isolated Docker containers with configurable network access and resource limits.
- **FR-005**: System MUST support three scanner input types: source filesystem (read-only mount), MCP connection (isolated network), and container image reference.
- **FR-006**: System MUST read scanner results in SARIF format and normalize findings into an internal model with severity, category, title, description, location, and scanner source.
- **FR-007**: System MUST run multiple scanners in parallel for a single scan job, with independent failure handling per scanner.
- **FR-008**: System MUST enforce per-scanner timeouts and kill containers that exceed them.
- **FR-009**: System MUST aggregate findings from all scanners into a single report with a composite risk score (0-100).
- **FR-010**: System MUST support approve/reject/rescan workflow after scan completion.
- **FR-011**: System MUST store integrity baselines on approval (image digest, source hash, lockfile hash, tool hashes) and verify them on server restart.
- **FR-012**: System MUST automatically re-quarantine servers when integrity verification fails.
- **FR-013**: System MUST emit SSE events for scan lifecycle (started, progress, completed, failed) and integrity alerts.
- **FR-014**: System MUST provide REST API endpoints for all scanner management, scan operations, approval flow, and overview.
- **FR-015**: System MUST provide CLI commands for all scanner and scan operations with support for table, JSON, YAML, and SARIF output formats.
- **FR-016**: System MUST support automatic scanning of newly quarantined servers when configured.
- **FR-017**: System MUST allow custom scanner registration with manifest validation.
- **FR-018**: System MUST log all security operations (install, scan, approve, reject, integrity violations) to the activity log.
- **FR-019**: System MUST prevent approval of servers with critical findings unless explicitly forced.
- **FR-020**: System MUST allow only one concurrent scan per server.
- **FR-021**: System MUST integrate with the existing tool-level quarantine system (spec 032) — scanner findings complement, not replace, tool hash approval.

### Key Entities

- **Scanner**: A security scanning tool distributed as a Docker image, with metadata (name, vendor, inputs, configuration), installable from registry.
- **Scan Job**: A running or completed scan of a specific server by a specific scanner, with status tracking and timing.
- **Scan Report**: Aggregated results from all scanners for a server, containing normalized findings and a risk score.
- **Scan Finding**: Individual security issue found by a scanner, with severity, category, and location.
- **Integrity Baseline**: Per-server record of approved hashes (image, source, lockfile, tools) used for runtime verification.
- **Scanner Registry**: Collection of known scanner manifests, bundled with the application and optionally updatable.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Users can install a scanner and run their first scan within 5 minutes of setup (excluding Docker image download time).
- **SC-002**: Scan results are available within 2 minutes of scan completion (parsing + storage + report generation).
- **SC-003**: The approval workflow (view report + approve/reject) completes in under 30 seconds from user decision.
- **SC-004**: Runtime integrity checks detect image digest mismatches within 1 server restart cycle (no false negatives).
- **SC-005**: All security operations are visible across all three UIs (Web, CLI, macOS tray) powered by a single API.
- **SC-006**: Zero scanner operations can affect the host filesystem or other servers (complete container isolation).
- **SC-007**: 100% of scanner failures (timeouts, crashes, bad output) are gracefully handled without affecting other scanners or system stability.
- **SC-008**: Custom scanners work identically to registry scanners with no feature gaps.

## Assumptions

- Docker is available on the host (required for scanner execution; graceful degradation with clear error when absent).
- Scanners are distributed as Docker images (no host-level installation needed).
- SARIF is the universal output format; non-SARIF scanners use adapter shims (thin Docker wrappers) provided by the registry.
- Scanner API keys are stored encrypted in the existing BBolt database (same mechanism as existing secrets).
- The scanner registry ships as a bundled JSON file; remote updates are opt-in and user-controlled (default: static file).
- v1 targets stdio servers in Docker; HTTP/SSE servers are scanned via URL (no container snapshotting needed).
- Pre-configured servers in the JSON config file are not auto-scanned; users can trigger manual scans on them.
- Docker socket access for `container_image` input type is opt-in and requires explicit user confirmation due to security implications; default is to pass image name only without socket mount.
- "Dry-run" scan mode (scan without quarantine) is supported as `--dry-run` flag on the scan command for v1.
- Scan reports are exportable in JSON and SARIF formats for v1; PDF/HTML report generation is deferred to v2.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- Use: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- Do NOT use: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- Do NOT include: `Co-Authored-By: Claude <noreply@anthropic.com>`
- Do NOT include: "Generated with Claude Code"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat(039): [brief description of change]

Related #356

[Detailed description of what was changed and why]

## Changes
- [Bulleted list of key changes]
- [Each change on a new line]

## Testing
- [Test results summary]
- [Key test scenarios covered]
```
