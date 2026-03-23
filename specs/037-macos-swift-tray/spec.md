# Feature Specification: Native macOS Swift Tray App (Spec A)

**Feature Branch**: `037-macos-swift-tray`
**Created**: 2026-03-23
**Status**: Draft
**Input**: User description: "Native macOS Swift tray app for MCPProxy (Spec A): SwiftUI MenuBarExtra + AppKit escape hatches, core process management, system notifications, Sparkle auto-update, symlink, tray icon badges. macOS 13+ Ventura. Bundle ID com.smartmcpproxy.mcpproxy."

## Assumptions

The following decisions were made autonomously where the original requirements were ambiguous:

1. **Scope boundary**: This spec covers only the tray menu, core process management, notifications, and auto-update (Spec A). The main app window with full views (Spec B) and automated testing framework (Spec C) are separate future specs.
2. **Appcast hosting**: The Sparkle appcast XML will be hosted at `https://mcpproxy.app/appcast.xml` (existing domain). GitHub Releases provides the DMG download URLs.
3. **Symlink authorization**: Creating `/usr/local/bin/mcpproxy` symlink requires elevated privileges. The app will prompt via macOS authorization dialog on first launch. If denied, the app continues without the symlink and shows a non-blocking hint in the menu.
4. **Notification permission**: The app requests notification permission on first launch. All features work without notifications — they degrade gracefully to menu-only indicators.
5. **Core binary resolution**: The app always prefers the bundled binary at `Contents/Resources/bin/mcpproxy`. External binaries on PATH are not used for launching.
6. **Existing Go tray deprecation**: The existing Go tray app (`cmd/mcpproxy-tray/`) continues to work for Windows. macOS users migrate to the Swift app. No cross-compilation of the Swift app.
7. **Menu bar icon**: Uses a template image (monochrome, adapts to light/dark) with a composited colored dot for health status. No dock icon (LSUIElement=true).
8. **OAuth flow**: When a server requires OAuth login, the tray triggers the flow by calling the REST API endpoint which opens the default browser. The tray app itself does not render any OAuth UI.
9. **Minimum macOS version**: 13.0 Ventura. This enables MenuBarExtra, SMAppService, and modern SwiftUI features.
10. **Auto-update applies to entire .app bundle**: Both the Swift tray binary and the bundled Go core binary are updated together as a single artifact.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Launch and Monitor MCPProxy (Priority: P1)

A developer installs MCPProxy.app and launches it. The app appears in the menu bar with a status icon. It automatically starts the MCPProxy core service in the background and connects via Unix socket. The tray icon shows a green dot when all servers are connected and healthy. The menu displays the version, connection count (e.g., "19/23 servers"), and tool count.

**Why this priority**: This is the foundational user journey. Without a working tray that launches and monitors the core, nothing else functions.

**Independent Test**: Launch the app, verify the tray icon appears, verify the core process starts, verify the menu shows live server/tool counts from the running instance.

**Acceptance Scenarios**:

1. **Given** MCPProxy.app is not running, **When** user launches it from /Applications, **Then** the tray icon appears in the menu bar within 2 seconds, the core process starts automatically, and after the core is ready the menu shows version and server status.
2. **Given** the app is running and connected, **When** user clicks the tray icon, **Then** a menu appears showing: version line, connection summary, server list, recent activity, and action items.
3. **Given** the core is already running externally (e.g., `mcpproxy serve` from terminal), **When** user launches the tray app, **Then** it detects the existing instance via socket, attaches to it without launching a second process, and shows status normally.
4. **Given** the core crashes unexpectedly, **When** the tray detects process exit, **Then** it shows an error state in the tray icon (red dot) and menu, and attempts automatic restart up to 3 times.

---

### User Story 2 - Control Upstream Servers (Priority: P1)

A developer wants to quickly check which servers are connected and take action on servers that need attention — enable, disable, restart, or initiate OAuth login — all from the tray menu without opening a browser.

**Why this priority**: Server management is MCPProxy's core function. Quick tray access to server status and one-click actions is the primary value proposition over the Web UI.

**Independent Test**: With MCPProxy running, open the tray menu, verify all servers appear with correct status indicators, perform enable/disable/restart actions from the submenu.

**Acceptance Scenarios**:

1. **Given** MCPProxy is connected with multiple upstream servers, **When** user opens the tray menu, **Then** the "Servers" submenu shows each server with a colored health dot (green=healthy, yellow=degraded, red=unhealthy), name, and tool count.
2. **Given** a server requires OAuth authentication, **When** user clicks "Login" next to that server in the attention section, **Then** the default browser opens to the OAuth flow for that server.
3. **Given** a server is enabled, **When** user selects "Disable" from its submenu, **Then** the server is disabled via API and the menu updates to show the new state within 2 seconds.
4. **Given** servers need attention (auth required, connection failed), **When** user opens the menu, **Then** an "N servers need attention" section appears at the top with one-click action buttons for each.

---

### User Story 3 - Receive Security Notifications (Priority: P2)

A developer is working and MCPProxy detects sensitive data (API keys, credentials) in a tool call's input or output. The tray app sends a native macOS notification alerting the user. Similarly, when new or changed tools are detected (quarantine), the user is notified so they can review and approve.

**Why this priority**: Security alerts are critical for user trust. Without proactive notifications, sensitive data exposure or tool poisoning attacks could go unnoticed.

**Independent Test**: Trigger a sensitive data detection via a tool call, verify a macOS notification appears. Trigger a tool quarantine change, verify a notification appears with an action to review.

**Acceptance Scenarios**:

1. **Given** MCPProxy detects sensitive data in a tool call, **When** the activity event arrives via SSE, **Then** a macOS notification appears with the server name, tool name, and detection category (e.g., "API key detected in github:search_code response").
2. **Given** a server's tool descriptions change (rug pull detection), **When** the quarantine event arrives, **Then** a notification appears: "[N] tools pending approval on [server]" with a "Review" action button.
3. **Given** the user has disabled notifications in macOS System Settings, **When** a security event occurs, **Then** the tray menu still shows the alert section (menu-based indicators work without notification permission).
4. **Given** multiple sensitive data detections occur within 5 minutes for the same server, **When** processing events, **Then** only one notification is sent (rate-limited), but the menu shows all findings.

---

### User Story 4 - Auto-Update MCPProxy (Priority: P2)

A developer is using MCPProxy and a new version is released on GitHub. The tray app checks for updates periodically and shows an update notification. The user can approve the update, which downloads, installs, and relaunches the app — including the updated core binary.

**Why this priority**: Keeping MCPProxy current is important for security patches and new features. Without auto-update, users must manually download DMGs.

**Independent Test**: Configure a test appcast with a newer version, verify the app detects it, verify the update flow completes and the app relaunches with the new version.

**Acceptance Scenarios**:

1. **Given** a new version is available on GitHub Releases, **When** the app performs its periodic check (every 4 hours), **Then** a macOS notification appears: "MCPProxy [version] available" and the menu shows "Update Available: v[X.Y.Z]".
2. **Given** the user clicks "Install & Relaunch" (or approves from menu), **When** the update downloads and verifies, **Then** the core process is gracefully stopped, the .app bundle is replaced, and the app relaunches with the new version.
3. **Given** user clicks "Check for Updates..." manually, **When** Sparkle checks the appcast, **Then** a dialog shows either "Update available" with release notes or "You're up to date."
4. **Given** the update signature verification fails, **When** Sparkle detects the mismatch, **Then** the update is rejected and the user sees an error message. The current version continues running unaffected.

---

### User Story 5 - First Launch Setup (Priority: P3)

A new user installs MCPProxy.app for the first time. On first launch, the app creates a symlink at `/usr/local/bin/mcpproxy` pointing to the bundled binary (with authorization prompt), requests notification permissions, and registers as a login item if the user enables "Run at Startup."

**Why this priority**: First-launch experience sets user expectations. The symlink is important for CLI users but is a one-time setup. Lower priority because the app is fully functional without it.

**Independent Test**: Delete the symlink and any login item registration, launch the app, verify the authorization prompt appears for the symlink, verify notification permission is requested.

**Acceptance Scenarios**:

1. **Given** first launch and `/usr/local/bin/mcpproxy` does not exist, **When** the app starts, **Then** it prompts the user for admin authorization to create the symlink. If approved, the symlink is created. If denied, the app continues normally and shows a hint in the menu.
2. **Given** the user toggles "Run at Startup" in the menu, **When** the toggle is enabled, **Then** the app registers as a login item using the system service. When toggled off, it unregisters.
3. **Given** an older version's symlink exists at `/usr/local/bin/mcpproxy`, **When** the app launches, **Then** it detects the stale symlink and offers to update it to point to the current bundled binary.

---

### User Story 6 - View Recent Activity (Priority: P3)

A developer wants a quick glance at what MCPProxy has been doing without opening the Web UI. The tray menu shows the last few activity entries and highlights any issues.

**Why this priority**: Activity visibility is useful but not critical — the Web UI provides a full activity log. The tray provides a convenience preview.

**Independent Test**: Perform some tool calls via MCP, open the tray menu, verify recent activity entries appear with correct tool names, servers, durations, and status indicators.

**Acceptance Scenarios**:

1. **Given** MCPProxy has processed tool calls, **When** user opens the tray menu, **Then** the "Recent Activity" section shows the last 3 entries with: status icon, server:tool name, and duration.
2. **Given** a tool call was blocked (quarantined), **When** it appears in recent activity, **Then** it shows a warning icon and "blocked" status.
3. **Given** sensitive data was detected in the last hour, **When** user opens the menu, **Then** a "Sensitive Data Detected" alert section appears with finding count and a link to view details.

---

### Edge Cases

- What happens when the Unix socket file exists but the core process is dead (stale socket)?
  - The app attempts to connect, fails, removes the stale socket file, and launches a new core process.
- What happens when port 8080 is already occupied by another application?
  - The core exits with code 2. The tray shows "Port conflict" in the menu with options: retry, use an available port, or edit configuration.
- What happens when the database file is locked by another mcpproxy instance?
  - The core exits with code 3. The tray shows "Database locked" with instructions to stop the other instance.
- What happens when the user force-quits the tray app (e.g., Activity Monitor)?
  - The core process continues running as an orphan. Next tray launch detects it via socket and attaches.
- What happens during an auto-update if the core is actively handling MCP requests?
  - The update waits for the user to approve "Install & Relaunch." The core is sent SIGTERM with a 10-second grace period for in-flight requests before SIGKILL.
- What happens when there is no internet connection during update check?
  - Sparkle silently fails the check and retries at the next interval. No error shown to the user.
- What happens when the /usr/local/bin directory does not exist?
  - The app skips the symlink creation and logs a warning. No error shown to the user.
- What happens when SSE connection drops and reconnects?
  - The app triggers a full state refresh (servers, activity, status) on reconnect to catch any missed events.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The app MUST appear as a menu bar item with a monochrome template icon that adapts to light and dark menu bar themes.
- **FR-002**: The app MUST launch the bundled MCPProxy core binary (`Contents/Resources/bin/mcpproxy serve`) as a child process on startup if no existing instance is detected.
- **FR-003**: The app MUST communicate with the core service via Unix socket (`~/.mcpproxy/mcpproxy.sock`) without requiring an API key.
- **FR-004**: The app MUST subscribe to the core's Server-Sent Events stream (`/events`) for real-time status updates.
- **FR-005**: The tray icon MUST display a colored badge dot indicating overall health: green (all healthy), yellow (degraded/warnings), red (errors/attention needed), no dot (disconnected/launching).
- **FR-006**: The tray menu MUST display the MCPProxy version, connection summary (N/M servers connected), and total tool count.
- **FR-007**: The tray menu MUST show an "attention needed" section listing servers that require action (OAuth login, restart, enable), with one-click action buttons.
- **FR-008**: The tray menu MUST provide a "Servers" submenu listing all configured servers with health indicators, tool counts, and per-server action submenus (enable/disable, restart, view logs, login).
- **FR-009**: The tray menu MUST show a "quarantine" section when tools are pending approval, displaying the count per server.
- **FR-010**: The tray menu MUST display the 3 most recent activity entries with status icon, server:tool name, and duration.
- **FR-011**: The app MUST send native macOS notifications for: sensitive data detection, tool quarantine changes, OAuth token expiration warnings, core crashes, and update availability.
- **FR-012**: Notifications MUST include actionable buttons (e.g., "Review Tools", "Re-authenticate", "Restart", "Install & Relaunch") that trigger the appropriate action.
- **FR-013**: Notifications MUST be rate-limited: maximum one notification per server per event type per 5 minutes.
- **FR-014**: The app MUST integrate Sparkle 2.x for auto-update, checking an EdDSA-signed appcast for new versions.
- **FR-015**: The auto-update flow MUST: gracefully stop the core process, replace the .app bundle (including the bundled core binary), update the `/usr/local/bin/mcpproxy` symlink, and relaunch.
- **FR-016**: The app MUST provide a "Check for Updates..." menu item for manual update checks.
- **FR-017**: The app MUST provide a "Run at Startup" toggle that registers/unregisters the app as a login item.
- **FR-018**: On first launch, the app MUST attempt to create a symlink at `/usr/local/bin/mcpproxy` pointing to the bundled core binary, requesting admin authorization.
- **FR-019**: The app MUST handle core process failures by parsing exit codes (2=port conflict, 3=DB locked, 4=config error, 5=permission error) and displaying appropriate error messages and remediation actions in the menu.
- **FR-020**: The app MUST attempt automatic core restart up to 3 times with 2-second backoff on unexpected failures.
- **FR-021**: The app MUST detect and attach to an already-running core instance (external mode) without launching a second process.
- **FR-022**: On quit, the app MUST send SIGTERM to the core process, wait up to 10 seconds, then SIGKILL if the process has not exited.
- **FR-023**: The tray menu MUST include "Open Web UI" (opens browser to localhost), "Add Server..." (opens main window — Spec B placeholder), and "Quit MCPProxy" items.
- **FR-024**: The app MUST support macOS 13 Ventura and later.
- **FR-025**: The app MUST be signed with Developer ID Application certificate and notarized for distribution outside the Mac App Store.

### Key Entities

- **Core Process**: The `mcpproxy serve` child process managed by the tray. Attributes: PID, state (launching/running/stopped/crashed), ownership mode (tray-managed/external-attached), exit code.
- **Server Status**: An upstream MCP server's current state. Attributes: name, health level (healthy/degraded/unhealthy), admin state (enabled/disabled/quarantined), tool count, pending action (login/restart/enable/approve).
- **Activity Entry**: A recent tool call or event. Attributes: type, server name, tool name, status (success/error/blocked), duration, timestamp, sensitive data flag.
- **Notification Event**: A user-facing alert triggered by a security or system event. Attributes: event type, server name, message, priority, timestamp, action.
- **App State**: Root observable state driving the menu. Attributes: core state, servers list, recent activity, sensitive data alerts, quarantined tools count, update availability, auto-start enabled.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: The tray app launches and shows a functional menu within 3 seconds on a MacBook Air M1 with macOS 13.
- **SC-002**: The tray app starts the core service and reaches a connected state within 15 seconds (excluding server connection time).
- **SC-003**: Server status changes (enable/disable/restart) initiated from the tray menu are reflected in the menu within 2 seconds.
- **SC-004**: Notifications for sensitive data detection appear within 5 seconds of the event occurring.
- **SC-005**: The auto-update flow (check, download, install, relaunch) completes within 60 seconds for a typical DMG size (~30MB).
- **SC-006**: The tray app consumes less than 50MB of memory during steady-state operation.
- **SC-007**: The `/usr/local/bin/mcpproxy` symlink is functional after first-launch setup, allowing `mcpproxy serve` from any terminal.
- **SC-008**: 100% of core process exit codes (2, 3, 4, 5) result in user-visible error messages with remediation actions — no silent failures.
- **SC-009**: The tray menu accurately reflects the state of all configured servers within 5 seconds of any change, verified via SSE event delivery.
- **SC-010**: The app binary is successfully signed and notarized, passing `spctl --assess --type execute` verification.

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
feat(macos-tray): add core process management with socket transport

Related #XXX

Implements CoreProcessManager actor that launches mcpproxy serve,
monitors via Unix socket, handles exit codes, and manages lifecycle.

## Changes
- Add CoreProcessManager.swift with launch/monitor/shutdown
- Add SocketTransport.swift for Unix socket HTTP
- Add CoreState.swift state machine (6 states)
- Add unit tests for state transitions and exit code parsing

## Testing
- All state transition tests pass
- Socket connection verified against running mcpproxy instance
```
