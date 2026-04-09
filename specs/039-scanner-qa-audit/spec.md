# Feature Specification: Security Scanner QA Audit & Fix

**Feature Branch**: `feat/039-security-scanner-plugins`
**Created**: 2026-04-06
**Status**: Draft
**Input**: QA audit of MCPProxy security scanner feature across all server types, with bug fixes and HTML report

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Scan Any Server Type (Priority: P1)

As a security-conscious developer, I want to scan any MCP server connected to my proxy — whether it's a local stdio process, a Docker-isolated container, or a remote HTTP endpoint — and get clear, actionable security findings.

**Why this priority**: Core value proposition. If scanning doesn't work reliably across all server types, the feature is broken.

**Independent Test**: Trigger scan on each server type (HTTP, streamable-http, stdio local, stdio Docker, quarantined) and verify results appear without page reload.

**Acceptance Scenarios**:

1. **Given** an HTTP server (context7), **When** I trigger a scan, **Then** I see progress, then findings about tool descriptions within 60 seconds
2. **Given** a stdio Docker server (perplexity), **When** I trigger a scan, **Then** source code is extracted from Docker and scanned by all applicable scanners
3. **Given** a local stdio server (demo-filesystem), **When** I trigger a scan, **Then** I see appropriate warnings about no Docker isolation
4. **Given** a quarantined server (malicious-demo), **When** I trigger a scan, **Then** the system auto-connects the server temporarily for scanning
5. **Given** a disconnected server, **When** I trigger a scan, **Then** I see a clear error message explaining the server must be connected

---

### User Story 2 - Understand Scan Results (Priority: P1)

As a developer reviewing scan results, I want findings displayed with clear severity, evidence, and context so I can make informed trust decisions.

**Why this priority**: Results must be understandable and actionable, otherwise the scan feature provides no value.

**Independent Test**: View scan report for a server with findings and verify all information is accessible.

**Acceptance Scenarios**:

1. **Given** a completed scan with findings, **When** I view the security tab, **Then** I see findings grouped by severity with risk scores
2. **Given** a finding about tool poisoning, **When** I view its details, **Then** I see the evidence text that triggered the alert
3. **Given** a scan with 0 findings, **When** I view the security tab, **Then** I see "No issues found" (not "Never scanned")
4. **Given** a server never scanned, **When** I view the security tab, **Then** I see a clear "Not yet scanned" state with a scan button

---

### User Story 3 - Track Scan Progress (Priority: P2)

As a developer, I want clear visual feedback during the scan process so I know it's working and how long to wait.

**Why this priority**: Without progress feedback, users will think the scan is broken and click repeatedly.

**Independent Test**: Start a scan and verify progress bar appears immediately and updates throughout.

**Acceptance Scenarios**:

1. **Given** I click "Start Scan", **When** scan is initializing, **Then** I see an indeterminate progress bar with "Initializing..." text
2. **Given** a scan is in progress, **When** scanners are running, **Then** I see per-scanner status (name, running/completed/failed)
3. **Given** a scan completes, **When** results are ready, **Then** results appear automatically without page reload

---

### User Story 4 - Correct Context Banners (Priority: P2)

As a developer, I want the scan context banner to accurately describe my server's security posture — Docker isolated, local process, or remote HTTP.

**Why this priority**: Incorrect banners (e.g., showing "No Docker Isolation" for HTTP servers) erode trust.

**Independent Test**: View security tab for each server type and verify banner content matches server reality.

**Acceptance Scenarios**:

1. **Given** an HTTP server, **When** I view its security tab, **Then** I see "HTTP Server - Tool description scanning only"
2. **Given** a Docker-isolated server, **When** I view its security tab, **Then** I see "Docker Isolated" with container info
3. **Given** a local stdio server without Docker, **When** I view its security tab, **Then** I see "No Docker Isolation" warning

---

### User Story 5 - Scanner Execution Logs (Priority: P3)

As a developer, I want to see which scanners ran, their status, duration, and any errors in the execution logs.

**Why this priority**: Helps debug scan issues and understand what was actually checked.

**Independent Test**: View scanner execution logs after a completed scan.

**Acceptance Scenarios**:

1. **Given** a completed scan, **When** I view execution logs, **Then** I see all scanners that ran with their status and duration
2. **Given** a scanner that failed, **When** I view execution logs, **Then** I see the error message and can understand what went wrong
3. **Given** an HTTP server scan, **When** I view execution logs, **Then** I only see scanners capable of analyzing tool descriptions (not filesystem scanners)

---

### Edge Cases

- What happens when Docker is not available on the host?
- What happens when a scanner Docker image fails to pull?
- What happens when scan is triggered but server disconnects mid-scan?
- What happens with concurrent scans on the same server?
- What happens with very large tool sets (50+ tools)?
- What if tools.json export fails?

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST correctly resolve scan source for all server types (HTTP, SSE, streamable-http, stdio local, stdio Docker)
- **FR-002**: System MUST show appropriate context banners matching the actual server type
- **FR-003**: System MUST display scan progress from start to completion without requiring page reload
- **FR-004**: System MUST display scan results immediately upon completion
- **FR-005**: System MUST clearly distinguish between "never scanned", "scanning", "no findings", and "has findings" states
- **FR-006**: System MUST skip irrelevant scan passes (e.g., Pass 2 supply chain audit for HTTP servers with no filesystem)
- **FR-007**: System MUST show scanner execution logs with correct scanner count, names, and statuses
- **FR-008**: System MUST handle scan errors gracefully with user-friendly messages
- **FR-009**: System MUST export tool definitions correctly for scanners that analyze tool descriptions
- **FR-010**: System MUST prevent duplicate concurrent scans on the same server

### Key Entities

- **ScanJob**: A scan execution with status, pass number, scanner statuses, findings
- **ScanReport**: Aggregated findings from a completed scan with severity/risk scores
- **ScanContext**: Server-specific scan configuration (source method, path, Docker isolation, tools exported)
- **ScannerPlugin**: A Docker-based scanner with capabilities, Docker image, and configuration

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: All 9 connected server types can be scanned without errors
- **SC-002**: Scan results display within 2 seconds of scan completion (no page reload needed)
- **SC-003**: Zero false "No Docker Isolation" warnings for HTTP/remote servers
- **SC-004**: Scanner execution logs show correct scanner count for every scan
- **SC-005**: All identified bugs are fixed and validated via API + UI testing
- **SC-006**: Comprehensive HTML report documents all findings, fixes, and validation results

## Assumptions

- Docker is available on the host machine for running scanner containers
- Scanner Docker images are pre-pulled or can be pulled on demand
- HTTP servers are scanned for tool description poisoning only (no filesystem access)
- Pass 2 (supply chain audit) is only relevant for servers with local filesystem access
- The `cisco-mcp-scanner` is the primary scanner for tool description analysis
- Risk scores from scanners are on a 0-100 scale

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- Use: `Related #[issue-number]` - Links the commit to the issue without auto-closing

### Example Commit Message
```
fix(039): [brief description of change]

Related #[issue-number]

[Detailed description of what was changed and why]

## Changes
- [Bulleted list of key changes]

## Testing
- [Test results summary]
```
