# Feature Specification: Update Check Enhancement & Version Display

**Feature Branch**: `001-update-version-display`
**Created**: 2025-12-15
**Status**: Draft
**Input**: User description: "Tray app update check and version display: analyze current Check for updates functionality on macOS/Windows, implement proper UX for update notifications, consider auto-update or GitHub releases redirect, and add version number visibility in tray menu and WebUI"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Version Always Visible (Priority: P1)

A user wants to quickly verify which version of MCPProxy they are running across all interfaces - tray menu, Web Control Panel, and CLI - without additional navigation or commands.

**Why this priority**: Version visibility is essential for support requests, bug reports, and self-service troubleshooting. This is the most fundamental usability requirement that enables all other scenarios.

**Independent Test**: Can be fully tested by opening tray menu, Web Control Panel, or running `mcpproxy doctor` and verifying the version number is immediately visible.

**Acceptance Scenarios**:

1. **Given** the tray application is running, **When** the user opens the tray menu, **Then** they see the current version number (e.g., "MCPProxy v1.2.3") displayed as a menu item
2. **Given** the Web Control Panel is open, **When** the user views the interface, **Then** the server version is visible without requiring navigation (e.g., in header, footer, or sidebar)
3. **Given** the user runs `mcpproxy doctor`, **When** the command outputs results, **Then** the current version is displayed in the output
4. **Given** a development/debug build, **When** the user views the version anywhere, **Then** it shows "development" or the appropriate build identifier

---

### User Story 2 - Background Update Detection by Core (Priority: P1)

The MCPProxy core server automatically checks for new GitHub releases in the background and exposes this information via REST API, so all clients (tray, WebUI, CLI) can display update availability consistently.

**Why this priority**: Centralizing update checks in core ensures consistency across all interfaces and reduces redundant GitHub API calls. This is the architectural foundation for update notifications.

**Independent Test**: Can be fully tested by running the core server with a known older version, waiting for background check (or triggering via startup), and querying the REST API endpoint to verify update info is available.

**Acceptance Scenarios**:

1. **Given** the core server starts, **When** initialization completes, **Then** it performs an initial check for new GitHub releases
2. **Given** the core server is running, **When** 4 hours have elapsed since last check, **Then** it automatically checks GitHub for new releases
3. **Given** a newer version exists on GitHub, **When** the REST API version endpoint is queried, **Then** it returns both current version and latest available version with release URL
4. **Given** the user is running the latest version, **When** the REST API version endpoint is queried, **Then** it indicates no update is available
5. **Given** the GitHub API is unreachable, **When** a background check fails, **Then** the last known state is preserved and error is logged (not surfaced to user)

---

### User Story 3 - Update Available Menu Item in Tray (Priority: P2)

When the core detects a new version is available, a "New version available" menu item appears in the tray menu. The old "Check for Updates..." menu item is removed entirely.

**Why this priority**: Proactive notification in tray is the most visible way to inform desktop users about updates, reducing the need for manual checks.

**Independent Test**: Can be fully tested by running tray with a core that has detected an update, opening the tray menu, and verifying the "New version available" item appears and "Check for Updates..." is absent.

**Acceptance Scenarios**:

1. **Given** no update is available, **When** the user opens the tray menu, **Then** no update-related menu item is shown (no "Check for Updates...")
2. **Given** an update is available (v1.3.0), **When** the user opens the tray menu, **Then** they see "New version available (v1.3.0)" menu item
3. **Given** "New version available" is shown, **When** the user clicks it, **Then** their default browser opens to the GitHub releases page for that version
4. **Given** Homebrew installation is detected, **When** update is available and user clicks menu item, **Then** a message suggests using `brew upgrade mcpproxy` instead of opening browser

---

### User Story 4 - Update Notification in Web Control Panel (Priority: P2)

When an update is available, the Web Control Panel displays a non-intrusive notification banner or indicator so users managing MCPProxy remotely are informed.

**Why this priority**: Web users need the same update visibility as tray users, especially important for headless/server deployments where tray is not available.

**Independent Test**: Can be fully tested by opening Web Control Panel connected to a core that has detected an update, and verifying the update notification is visible.

**Acceptance Scenarios**:

1. **Given** no update is available, **When** the user views the Web Control Panel, **Then** no update notification is displayed
2. **Given** an update is available (v1.3.0), **When** the user views the Web Control Panel, **Then** they see a notification banner (e.g., "Update available: v1.3.0")
3. **Given** update notification is displayed, **When** the user clicks on it or an associated link, **Then** they are directed to the GitHub releases page
4. **Given** update notification is displayed, **When** the user dismisses it, **Then** it remains dismissed for that session but reappears on next visit

---

### User Story 5 - Update Info in CLI Doctor Command (Priority: P2)

When running `mcpproxy doctor`, the output includes the current version and indicates if a newer version is available, helping CLI users stay informed.

**Why this priority**: CLI users running diagnostics should see update availability as part of system health, making doctor the natural place for this information.

**Independent Test**: Can be fully tested by running `mcpproxy doctor` when an update is available and verifying the output includes update information.

**Acceptance Scenarios**:

1. **Given** no update is available, **When** the user runs `mcpproxy doctor`, **Then** output shows "Version: v1.2.3 (latest)"
2. **Given** an update is available (v1.3.0), **When** the user runs `mcpproxy doctor`, **Then** output shows "Version: v1.2.3 (update available: v1.3.0)" with GitHub release URL
3. **Given** update check has not completed or failed, **When** the user runs `mcpproxy doctor`, **Then** output shows "Version: v1.2.3" without update status

---

### User Story 6 - Download New Version via GitHub Releases (Priority: P3)

When a user decides to update, they can easily access the GitHub releases page to download the appropriate version for their platform.

**Why this priority**: While the system doesn't auto-update, providing a direct path to downloads reduces friction for users who want to update.

**Independent Test**: Can be fully tested by clicking the update notification/menu item and verifying the correct GitHub releases page opens.

**Acceptance Scenarios**:

1. **Given** an update notification is shown (any interface), **When** the user clicks the action to download, **Then** their default browser opens to `https://github.com/smart-mcp-proxy/mcpproxy-go/releases/tag/vX.Y.Z`
2. **Given** the user is on macOS with Homebrew installation detected, **When** they interact with update notification, **Then** they see guidance to use `brew upgrade mcpproxy`
3. **Given** the user is on Windows, **When** they access the releases page, **Then** they can find Windows-specific download assets

---

### Edge Cases

- **GitHub API rate limit exceeded**: Silent failure - log error at debug level, preserve last known state, wait for next scheduled check (4 hours). No user-facing error.
- **Development/snapshot builds**: Version comparison skipped for non-semver versions (e.g., "development"); no update notifications shown.
- **Core loses internet connectivity**: Same as rate limit - silent failure, preserve last known state, retry on next scheduled check.
- **Tray cannot connect to core**: Tray shows version from its own build info; update status unavailable until core connection restored.
- **Prerelease versions**: Respect `MCPPROXY_ALLOW_PRERELEASE_UPDATES` env var; when false (default), only compare against stable releases.
- **GitHub release missing platform assets**: Show update notification anyway; user will see available assets on releases page.

## Requirements *(mandatory)*

### Functional Requirements

**Version Display:**
- **FR-001**: Tray menu MUST display the current version number as a visible menu item (e.g., "MCPProxy v1.2.3")
- **FR-002**: Web Control Panel MUST display the current version in a persistent, visible location
- **FR-003**: `mcpproxy doctor` command MUST include the current version in its output

**Background Update Detection (Core):**
- **FR-004**: Core server MUST check for new GitHub releases on startup
- **FR-005**: Core server MUST check for new GitHub releases every 4 hours while running
- **FR-006**: Core server MUST expose a REST API endpoint that returns current version and latest available version information
- **FR-007**: Core server MUST cache the latest version info and serve it to all clients

**Update Notifications:**
- **FR-008**: Tray menu MUST show "New version available (vX.Y.Z)" menu item when update is detected
- **FR-009**: Tray menu MUST NOT show any update-related menu item when running latest version (remove "Check for Updates...")
- **FR-010**: Web Control Panel MUST display a notification when update is available
- **FR-011**: `mcpproxy doctor` MUST indicate update availability with latest version and download URL

**User Actions:**
- **FR-012**: Clicking update menu item/notification MUST open GitHub releases page in default browser
- **FR-013**: System MUST show appropriate guidance for package manager installations (Homebrew: suggest `brew upgrade`)

**Configuration & Compatibility:**
- **FR-014**: System MUST respect `MCPPROXY_DISABLE_AUTO_UPDATE` to disable background checks entirely
- **FR-015**: System MUST respect `MCPPROXY_ALLOW_PRERELEASE_UPDATES` for prerelease version comparison
- **FR-016**: System MUST work consistently on both macOS and Windows platforms
- **FR-017**: System MUST handle GitHub API failures gracefully without impacting core functionality

**Documentation & Testing:**
- **FR-018**: Feature MUST be documented in `docs/` directory for publication on the docs site
- **FR-019**: Documentation MUST cover: version display locations, update notification behavior, environment variable configuration, and troubleshooting
- **FR-020**: All functional requirements MUST have corresponding automated tests (unit and/or integration)
- **FR-021**: Background update checker MUST have unit tests covering: startup check, periodic check, GitHub API success/failure scenarios
- **FR-022**: REST API version endpoint MUST be covered by API E2E tests

### Key Entities *(include if feature involves data)*

- **Version Info**: Current version string, latest available version string (if different), release URL, check timestamp, is_update_available flag. Stored in-memory only; refreshed on startup and every 4 hours.
- **REST API Endpoint Response**: `GET /api/v1/version` or included in `GET /api/v1/info` - returns version info entity

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Version number is visible in tray menu on first open without additional clicks
- **SC-002**: Version information is visible on Web Control Panel without navigation
- **SC-003**: Version is displayed in `mcpproxy doctor` output
- **SC-004**: Update availability is detected within 5 minutes of core startup
- **SC-005**: Users can navigate to GitHub releases page within 1 click from update notification
- **SC-006**: Feature works identically on macOS and Windows (functionality parity)
- **SC-007**: Background update checks do not impact core server performance or responsiveness
- **SC-008**: Documentation published on docs site covering all user-facing behavior
- **SC-009**: All new code has automated test coverage; no untested code paths for core functionality
- **SC-010**: API E2E tests pass for version endpoint

## Clarifications

### Session 2025-12-15

- Q: Should version cache persist across restarts or be in-memory only? → A: In-memory only (fresh check on each startup, lost on restart)
- Q: How should the system handle GitHub API rate limiting? → A: Silent failure - log error, wait for next scheduled check (4 hours)
- Q: Documentation and testing requirements? → A: Mandatory - document in docs/ for docs site; cover all features with automated tests (unit + E2E)

## Assumptions

- The existing REST API infrastructure can be extended with version/update information
- GitHub API for releases is reliably accessible (with graceful degradation on failure)
- The version string is available at runtime via build flags in core
- Tray app can read version/update info from core via existing API client connection
- The 4-hour check interval is appropriate (not too frequent for API limits, not too infrequent for user needs)

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- **Use**: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- **Do NOT use**: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- **Do NOT include**: "Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat: [brief description of change]

Related #[issue-number]

[Detailed description of what was changed and why]

## Changes
- [Bulleted list of key changes]
- [Each change on a new line]

## Testing
- [Test results summary]
- [Key test scenarios covered]
```
