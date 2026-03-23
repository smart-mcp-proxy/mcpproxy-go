# Feature Specification: MCP Accessibility Testing Server (Spec C)

**Feature Branch**: `038-mcp-accessibility-server`
**Created**: 2026-03-23
**Status**: Draft
**Input**: MCP server exposing macOS Accessibility API (AXUIElement) as tools for automated UI testing of MCPProxy tray app and future main window.

## Assumptions

1. **Target app**: MCPProxy.app (the Swift tray app from Spec 037). The server finds it by bundle identifier `com.smartmcpproxy.mcpproxy` or `com.smartmcpproxy.mcpproxy.dev`.
2. **Transport**: MCP stdio (stdin/stdout JSON-RPC). Claude Code and other MCP clients connect via stdio, same as any MCP server.
3. **Language**: Swift. Direct access to macOS Accessibility API without bridging.
4. **Permissions**: The server binary (or its parent terminal) needs Accessibility permission in System Settings > Privacy > Accessibility. The server checks on startup and provides clear instructions if missing.
5. **Scope**: Menu bar testing first (tray icon, NSMenu items, submenus). Window inspection deferred to when Spec B adds windows.
6. **Platform**: macOS 13+ (same as tray app). Uses ApplicationServices/HIServices framework.
7. **Binary name**: `mcpproxy-ui-test` — ships as a standalone binary, not inside the .app bundle.
8. **Configuration**: The server takes the target app's bundle ID as a CLI argument or defaults to `com.smartmcpproxy.mcpproxy`.
9. **No state mutation**: The server only reads UI state and triggers actions. It does not modify Accessibility attributes or inject synthetic events beyond clicking menu items.
10. **MCP protocol**: Implements MCP tools only (no resources, no prompts). Uses a lightweight JSON-RPC stdio implementation in Swift.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Verify Tray Menu Structure (Priority: P1)

A developer makes changes to the tray app menu and wants to verify the menu shows the correct items without duplicates. They run `mcpproxy-ui-test` as an MCP server and call `list_menu_items` to get the full menu tree.

**Why this priority**: This is the core testing use case — validating that the tray menu renders correctly after code changes.

**Independent Test**: Start the tray app, connect via MCP, call `list_menu_items`, verify the response contains each server exactly once.

**Acceptance Scenarios**:

1. **Given** MCPProxy.app is running with 23 servers, **When** `list_menu_items` is called, **Then** the response contains a JSON tree of all menu items with titles, enabled state, and submenu structure — each server appears exactly once.
2. **Given** MCPProxy.app is running, **When** `list_menu_items` is called with `path: "Servers"`, **Then** only the Servers submenu items are returned.
3. **Given** MCPProxy.app is not running, **When** `list_menu_items` is called, **Then** the response returns an error: "MCPProxy.app is not running".

---

### User Story 2 - Click Menu Items (Priority: P1)

A developer wants to test that menu actions work — clicking "Disable" on a server, toggling "Run at Startup", clicking "Quit". They call `click_menu_item` with the item path.

**Why this priority**: Without action triggering, the testing server is read-only. Actions complete the feedback loop.

**Independent Test**: Call `click_menu_item` with path "Servers > tavily > Disable", verify the server state changes via mcpproxy API.

**Acceptance Scenarios**:

1. **Given** a server "tavily" is enabled, **When** `click_menu_item` is called with path `["Servers", "tavily", "Disable"]`, **Then** the menu item is clicked and the server becomes disabled.
2. **Given** the tray menu has "Open Web UI", **When** `click_menu_item` is called with path `["Open Web UI"]`, **Then** the action is triggered.
3. **Given** an invalid path, **When** `click_menu_item` is called, **Then** an error is returned listing available items at the point where navigation failed.

---

### User Story 3 - Read Tray Status (Priority: P2)

A developer wants to check the tray icon state, the header text (version, server counts), and error messages without opening the menu. They call `read_status_bar` to get the tray item's current state.

**Why this priority**: Quick health check without needing to parse the full menu tree.

**Independent Test**: Call `read_status_bar`, verify it returns the app name, version, and connection summary.

**Acceptance Scenarios**:

1. **Given** MCPProxy.app is running and connected, **When** `read_status_bar` is called, **Then** the response includes the status item title/tooltip and icon description.
2. **Given** MCPProxy.app is in an error state, **When** `read_status_bar` is called, **Then** the response includes the error message visible in the menu header.

---

### User Story 4 - Check Accessibility Permission (Priority: P3)

A developer starts `mcpproxy-ui-test` for the first time. The server checks if it has Accessibility permission and guides the user if not.

**Why this priority**: Usability — without this, users get cryptic AX errors.

**Acceptance Scenarios**:

1. **Given** the terminal has Accessibility permission, **When** the server starts, **Then** it initializes normally and lists tools.
2. **Given** the terminal lacks Accessibility permission, **When** the server starts, **Then** it outputs an error with instructions to grant permission in System Settings.

---

### Edge Cases

- What if the menu is currently open? The server waits briefly and retries.
- What if the app has multiple status items? The server finds the one belonging to the target bundle ID.
- What if a menu item title contains special characters or emoji? Titles are returned as-is (UTF-8 strings).
- What if the server is asked to click a disabled menu item? Returns an error indicating the item is disabled.
- What if the target app's menu is very large (50+ items)? The server handles it within the 2-second timeout.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The server MUST implement MCP protocol over stdio (JSON-RPC 2.0, stdin/stdout).
- **FR-002**: The server MUST expose a `list_menu_items` tool that returns the complete menu tree of the target app's status bar menu.
- **FR-003**: The server MUST expose a `click_menu_item` tool that navigates a menu path and triggers the action on the target item.
- **FR-004**: The server MUST expose a `read_status_bar` tool that returns the status item's title, tooltip, and icon accessibility description.
- **FR-005**: The server MUST expose a `check_accessibility` tool that verifies Accessibility API permission and returns the status.
- **FR-006**: The server MUST expose a `list_running_apps` tool that lists running applications with their bundle IDs.
- **FR-007**: The server MUST accept a `--bundle-id` CLI argument to specify the target application (default: `com.smartmcpproxy.mcpproxy`).
- **FR-008**: The server MUST return structured JSON responses with menu item titles, enabled/disabled state, checked state, submenu presence, and keyboard shortcuts.
- **FR-009**: The server MUST handle the case where the target app is not running with a clear error message.
- **FR-010**: The server MUST check for Accessibility permission on startup and provide instructions if missing.
- **FR-011**: The server MUST work with any macOS application's menu bar, making it a general-purpose UI testing tool.
- **FR-012**: The server MUST support macOS 13 Ventura and later.

### Key Entities

- **StatusBarItem**: A menu bar item belonging to an application. Attributes: app name, bundle ID, title, icon description.
- **MenuItem**: A single entry in a menu. Attributes: title, enabled, checked, has submenu, keyboard shortcut, index.
- **MenuTree**: Hierarchical structure of menu items with nested submenus.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: `list_menu_items` returns the complete menu tree within 2 seconds for a menu with 30+ items.
- **SC-002**: `click_menu_item` triggers the action within 1 second of the call.
- **SC-003**: The server correctly identifies all menu items in the MCPProxy tray (no duplicates, no missing items).
- **SC-004**: Claude Code can use the server to run an end-to-end test: list items → verify → click action → verify state change.
- **SC-005**: The binary compiles and runs on macOS 13+ with Swift 5.9+.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- Use: `Related #[issue-number]`
- Do NOT use: `Fixes #`, `Closes #`, `Resolves #`
