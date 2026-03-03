# Feature Specification: Status Command

**Feature Branch**: `027-status-command`
**Created**: 2026-03-02
**Status**: Draft
**Input**: User description: "Add mcpproxy status CLI command with API key display, Web UI URL, and key reset"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Quick Status Check (Priority: P1)

A user wants to quickly see if MCPProxy is running, what address it's listening on, and get the Web UI URL to open in a browser. They run `mcpproxy status` and see a summary of the proxy state with a masked API key and the full Web UI URL.

**Why this priority**: This is the primary use case - users need a single command to understand the current state of their MCPProxy instance and access the Web UI.

**Independent Test**: Can be fully tested by running `mcpproxy status` with and without a running daemon and verifying the output contains state, listen address, masked API key, and Web UI URL.

**Acceptance Scenarios**:

1. **Given** MCPProxy daemon is running, **When** user runs `mcpproxy status`, **Then** output shows state "Running", actual listen address, uptime, masked API key (first4+****+last4), Web UI URL with embedded API key, server counts, and socket path.
2. **Given** MCPProxy daemon is NOT running, **When** user runs `mcpproxy status`, **Then** output shows state "Not running", configured listen address with "(configured)" suffix, masked API key, Web UI URL, and config file path.
3. **Given** MCPProxy daemon is running, **When** user runs `mcpproxy status -o json`, **Then** output is valid JSON containing all status fields with the API key masked.

---

### User Story 2 - Copy API Key for Scripting (Priority: P1)

A user needs the full API key to configure another tool or script that communicates with MCPProxy over HTTP. They run `mcpproxy status --show-key` to reveal the full key, or pipe it to clipboard.

**Why this priority**: Equally critical as status check - without this, users must manually dig through config files to find their API key.

**Independent Test**: Can be tested by running `mcpproxy status --show-key` and verifying the full 64-character hex key appears unmasked in output.

**Acceptance Scenarios**:

1. **Given** a configured API key exists, **When** user runs `mcpproxy status --show-key`, **Then** the full unmasked API key is displayed in the output.
2. **Given** a configured API key exists, **When** user runs `mcpproxy status --show-key -o json`, **Then** JSON output contains the full unmasked API key.

---

### User Story 3 - Open Web UI Quickly (Priority: P2)

A user wants to open the MCPProxy Web UI in their browser with a single command. They run `open $(mcpproxy status --web-url)` to get just the URL and pipe it to the system browser opener.

**Why this priority**: Convenience feature that builds on P1 status output - makes the URL independently extractable for scripting.

**Independent Test**: Can be tested by running `mcpproxy status --web-url` and verifying it outputs ONLY the URL (no other text) with the embedded API key.

**Acceptance Scenarios**:

1. **Given** MCPProxy config exists, **When** user runs `mcpproxy status --web-url`, **Then** output contains only the Web UI URL with embedded API key and no other text.
2. **Given** MCPProxy daemon is running on a custom port, **When** user runs `mcpproxy status --web-url`, **Then** the URL reflects the actual listen address.
3. **Given** MCPProxy daemon is not running, **When** user runs `mcpproxy status --web-url`, **Then** the URL reflects the configured listen address.

---

### User Story 4 - Reset Compromised API Key (Priority: P3)

A user suspects their API key was leaked or simply wants to rotate it. They run `mcpproxy status --reset-key` to generate a new key, which is saved to config and displayed. A warning explains that HTTP clients using the old key will be disconnected.

**Why this priority**: Security hygiene feature - less frequent than viewing, but important when needed.

**Independent Test**: Can be tested by running `mcpproxy status --reset-key`, verifying a new key is generated, saved to config file, and the warning message is displayed.

**Acceptance Scenarios**:

1. **Given** an existing API key, **When** user runs `mcpproxy status --reset-key`, **Then** a new API key is generated, saved to config, the new key is displayed in full, and a warning about HTTP client disconnection is shown.
2. **Given** MCPProxy daemon is running, **When** user resets the key, **Then** the daemon picks up the new key via config hot-reload without requiring restart.
3. **Given** a socket-connected client (tray app), **When** the API key is reset, **Then** the socket client continues to work unaffected.

---

### Edge Cases

- What happens when the config file doesn't exist yet? Status command should auto-create defaults (using existing `config.Load()` behavior) and display the auto-generated API key.
- What happens when `MCPPROXY_API_KEY` environment variable is set? The env var takes precedence; `--reset-key` should warn that the config file key was reset but the env var still overrides it.
- What happens when `--show-key` and `--reset-key` are used together? Reset takes priority: generate new key, display it in full (show-key is implicit with reset).
- What happens when `--web-url` and `--reset-key` are used together? Reset first, then output the new Web UI URL with the new key.
- What happens when the listen address starts with `:` (e.g., `:8080`)? Prefix with `127.0.0.1` for the URL, matching existing `buildWebUIURL()` behavior.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST provide a `mcpproxy status` command that displays proxy state, listen address, masked API key, Web UI URL, and socket path.
- **FR-002**: System MUST detect whether the daemon is running (via socket availability) and switch between live data and config-only modes.
- **FR-003**: In daemon mode, system MUST display runtime data: state "Running", actual listen address, uptime, connected/quarantined server counts, and socket path.
- **FR-004**: In config-only mode, system MUST display static data: state "Not running", configured listen address with "(configured)" suffix, and config file path.
- **FR-005**: System MUST mask the API key by default showing first 4 characters + `****` + last 4 characters.
- **FR-006**: System MUST support `--show-key` flag to display the full unmasked API key.
- **FR-007**: System MUST support `--web-url` flag to output ONLY the Web UI URL (with embedded API key) for piping to other commands.
- **FR-008**: System MUST support `--reset-key` flag to generate a new cryptographic API key, save it to the config file, and display it with a warning about HTTP client disconnection.
- **FR-009**: System MUST support standard output formats (table, JSON, YAML) via `-o` flag, consistent with other CLI commands.
- **FR-010**: When `--reset-key` is used and `MCPPROXY_API_KEY` env var is set, system MUST warn that the env var still overrides the config file key.
- **FR-011**: `--web-url` output MUST contain only the URL string with no additional formatting, labels, or newlines beyond the trailing newline.

### Key Entities

- **StatusInfo**: Represents the collected status data - state (running/not running), listen address, API key (masked or full), Web UI URL, uptime, server counts, socket path, config path.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Users can determine MCPProxy running state and access the Web UI URL in a single command execution.
- **SC-002**: Users can retrieve their full API key for scripting without manually reading config files.
- **SC-003**: Users can open the Web UI in their browser with `open $(mcpproxy status --web-url)` in one step.
- **SC-004**: Users can rotate their API key and understand the impact on connected clients within a single command.
- **SC-005**: All output formats (table, JSON, YAML) produce consistent, parseable results.

## Assumptions

- The existing `/api/v1/status` endpoint returns sufficient data for daemon mode (server counts, listen address). If uptime is not currently returned, it will be added.
- The `cliclient.Client` can query the status endpoint via socket connection.
- Config hot-reload (file watcher) already handles API key changes in the running daemon.
- The `--reset-key` flag does not require confirmation prompt (per brainstorming decision: "Reset + warn" approach).

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
feat(cli): add status command with API key display and web URL

Related #[issue-number]

Add `mcpproxy status` command providing unified view of proxy state
including masked API key, Web UI URL, and key reset capability.

## Changes
- Add status_cmd.go with daemon/config dual-mode operation
- Add --show-key, --web-url, --reset-key flags
- Add status-command.md documentation for Docusaurus site
- Register status command in sidebars.js

## Testing
- Unit tests for masking, URL construction, reset logic
- E2E tests for daemon and config-only modes
```
