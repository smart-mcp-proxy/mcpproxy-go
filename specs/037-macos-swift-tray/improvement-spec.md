# Improvement Specification: macOS Swift Tray App

**Feature Branch**: `037-macos-swift-tray`
**Created**: 2026-03-24
**Status**: Draft

## Problem Statement

The macOS tray app (native/macos/MCPProxy) provides basic server monitoring via the tray menu and a main window with four sidebar tabs (Servers, Activity Log, Secrets, Configuration). However, the ServersView is read-only with no click interactions -- users cannot drill into a server to see its tools, logs, or configuration. There is no way to add or import servers from the tray app. Tool quarantine approval requires opening the Web UI. Server actions (enable/disable/restart) only exist in the tray menu submenu, which is hard to discover. After performing actions, there is no visual feedback. Tool search across servers is absent.

These gaps force users to switch to the Web UI or CLI for common workflows. The tray app should be the primary management interface for personal edition users on macOS.

## Current State

### Tray App Endpoints Currently Used

| Endpoint | Usage |
|----------|-------|
| `GET /ready` | Readiness probe during core startup |
| `GET /api/v1/info` | Version, endpoints, update info |
| `GET /api/v1/servers` | Server list for tray menu and ServersView |
| `POST /api/v1/servers/{id}/enable` | Enable server (tray menu) |
| `POST /api/v1/servers/{id}/disable` | Disable server (tray menu, via enable with `enabled=false`) |
| `POST /api/v1/servers/{id}/restart` | Restart server (tray menu) |
| `POST /api/v1/servers/{id}/login` | OAuth login (tray menu) |
| `GET /api/v1/activity` | Recent activity for tray menu and ActivityView |
| `GET /api/v1/activity/summary` | Activity summary stats |
| `GET /api/v1/secrets/config` | Secrets tab |
| `GET /events` | SSE for real-time updates |

### Endpoints Available but NOT Used by Tray

| Endpoint | Relevance |
|----------|-----------|
| `GET /api/v1/servers/{id}/tools` | **P1** -- Server detail tools tab |
| `GET /api/v1/servers/{id}/logs` | **P1** -- Server detail logs tab |
| `POST /api/v1/servers` | **P1** -- Add server manually |
| `POST /api/v1/servers/import/path` | **P1** -- Import from local config file |
| `GET /api/v1/servers/import/paths` | **P1** -- Discover canonical config paths (Claude Desktop, etc.) |
| `POST /api/v1/servers/{id}/tools/approve` | **P1** -- Approve quarantined tools |
| `POST /api/v1/servers/{id}/unquarantine` | Already used via `approveTools()` in APIClient |
| `POST /api/v1/servers/{id}/quarantine` | Already wired in APIClient |
| `DELETE /api/v1/servers/{id}` | **P2** -- Remove server |
| `GET /api/v1/index/search` | **P2** -- Tool search across servers |
| `POST /api/v1/servers/{id}/discover-tools` | P3 -- Force tool refresh |
| `GET /api/v1/servers/{id}/tool-calls` | P3 -- Per-server tool call history |
| `GET /api/v1/diagnostics` | P3 -- Server diagnostics |

### Current Swift Files

| File | Purpose |
|------|---------|
| `Views/ServersView.swift` | NSTableView of servers (read-only, no click actions) |
| `Views/MainWindow.swift` | NavigationSplitView with 4 sidebar items |
| `Views/ActivityView.swift` | Activity log viewer |
| `Views/SecretsView.swift` | Secrets/keyring management |
| `Views/ConfigView.swift` | Configuration viewer |
| `API/APIClient.swift` | REST client (actor, async/await, Unix socket transport) |
| `API/Models.swift` | ServerStatus, HealthStatus, ActivityEntry, etc. |
| `Menu/TrayMenu.swift` | Tray dropdown with servers, activity, quarantine sections |

---

## User Stories & Acceptance Criteria

### User Story 1 -- Server Detail View (Priority: P1)

As a user, I want to click a server in the main window to see its tools, logs, and configuration, so I can inspect and manage servers without switching to the Web UI.

**Why P1**: The ServersView is the main content area but is entirely read-only. Users cannot get any detail about a server beyond name, protocol badge, and health dot.

**Acceptance Scenarios**:

1. **Given** the Servers tab is active, **When** I double-click a server row in the NSTableView, **Then** a detail view opens showing the server's name, protocol, URL/command, health status (level + summary + detail), and three tabs: Tools, Logs, Config.
2. **Given** the server detail Tools tab is active for a connected server, **When** the tools are loaded, **Then** I see a list of tools with name, description (truncated to 2 lines), and annotation badges (read-only, write, destructive) matching the Web UI's `AnnotationBadges` component. Clicking a tool opens a popover/sheet with the full description and JSON input schema.
3. **Given** the server detail Logs tab is active, **When** the tab appears, **Then** the last 100 log lines are loaded from `GET /api/v1/servers/{id}/logs?tail=100` and displayed in a monospaced font scroll view. A refresh button reloads the logs.
4. **Given** the server detail Config tab is active, **Then** I see read-only fields for: name, protocol, URL (if HTTP), command + args (if stdio), working directory, enabled toggle, quarantined badge.
5. **Given** the server detail is open, **When** I click a back button or press Escape, **Then** I return to the server list.

**New API Calls**:
- `GET /api/v1/servers/{id}/tools` -- fetch tools for a server
- `GET /api/v1/servers/{id}/logs?tail=100` -- fetch server log lines

**New Swift Models**:
```swift
struct ServerTool: Codable, Identifiable {
    var id: String { name }
    let name: String
    let description: String?
    let serverName: String?
    let annotations: ToolAnnotation?
    let schema: [String: AnyCodable]? // input_schema
}

struct ToolAnnotation: Codable {
    let readOnlyHint: Bool?
    let destructiveHint: Bool?
    let idempotentHint: Bool?
    let openWorldHint: Bool?
    let title: String?
}

struct ServerLogsResponse: Codable {
    let serverName: String
    let lines: [String]
    let count: Int
}
```

**New Swift Views**:
- `ServerDetailView.swift` -- container with header + tabs
- `ServerToolsTab.swift` -- tool list with annotation badges
- `ServerLogsTab.swift` -- monospaced log viewer
- `ServerConfigTab.swift` -- read-only config display

**Implementation Notes**:
- Navigation: Use `@State private var selectedServer: ServerStatus?` in ServersView. When set, push/present a ServerDetailView. On macOS, use `NavigationStack` or a simple conditional overlay within the detail pane.
- The NSTableView Coordinator needs a `tableViewSelectionDidChange` or double-click action delegate method.
- Tool annotation badges should use SF Symbols: `eye` (read-only), `pencil` (write), `trash` (destructive).

---

### User Story 2 -- Add/Import Server Dialog (Priority: P1)

As a user, I want to add a new MCP server or import existing configurations from Claude Desktop directly from the tray app, so I do not have to manually edit JSON files.

**Why P1**: Adding servers is the most fundamental setup action. Currently requires editing `mcp_config.json` by hand or using the Web UI.

**Acceptance Scenarios**:

1. **Given** the main window is open, **When** I click an "Add Server..." button (toolbar or below the server list), **Then** a sheet/dialog appears with two tabs: "Manual" and "Import".
2. **Given** the Manual tab is active, **When** I fill in: name (required), protocol (stdio/http radio), command or URL (required based on protocol), optional args (one per line), optional env vars (KEY=VALUE per line), optional working directory, and click "Add", **Then** the server is created via `POST /api/v1/servers` and appears in the server list.
3. **Given** the Import tab is active, **When** the tab loads, **Then** canonical config paths are fetched from `GET /api/v1/servers/import/paths` and displayed with existence status (green checkmark / gray "Not found"). Each found config has an "Import" button.
4. **Given** a canonical config path exists (e.g., Claude Desktop), **When** I click "Import", **Then** `POST /api/v1/servers/import/path` is called with that path. A summary shows: N imported, N skipped (already exist), N failed. The server list refreshes.
5. **Given** the Add Server sheet is open from the tray menu via "Add Server...", **Then** the same sheet behavior applies (opens the main window if needed, then shows the sheet).

**New API Calls**:
- `POST /api/v1/servers` -- add a single server
- `GET /api/v1/servers/import/paths` -- list canonical config paths with existence check
- `POST /api/v1/servers/import/path` -- import from a filesystem path

**New Swift Models**:
```swift
struct CanonicalConfigPath: Codable, Identifiable {
    var id: String { path }
    let name: String
    let path: String
    let format: String
    let exists: Bool
    let description: String?
    let os: String?
}

struct CanonicalConfigPathsResponse: Codable {
    let os: String
    let paths: [CanonicalConfigPath]
}

struct AddServerRequest: Codable {
    let name: String
    let `protocol`: String
    let url: String?
    let command: String?
    let args: [String]?
    let env: [String: String]?
    let workingDir: String?
    let enabled: Bool
    let quarantined: Bool
}

struct ImportFromPathRequest: Codable {
    let path: String
    let format: String?
}

struct ImportResponse: Codable {
    let summary: ImportSummary
    let imported: [ImportedServer]?
    let skipped: [SkippedServer]?
    let failed: [FailedServer]?
}

struct ImportSummary: Codable {
    let total: Int
    let imported: Int
    let skipped: Int
    let failed: Int
}
```

**New Swift Views**:
- `AddServerSheet.swift` -- sheet with Manual/Import tab picker
- `ManualServerForm.swift` -- form fields for manual server creation
- `ImportServerForm.swift` -- canonical path list with import buttons

**Implementation Notes**:
- The "Add Server..." button should also appear in the tray menu (actionsSection) as a menu item.
- Validation: name must be non-empty and not contain spaces. Protocol selection shows/hides URL vs command fields. For stdio, command is required. For http, URL is required.
- After successful add/import, trigger a server list refresh (the SSE `servers.changed` event should also handle this automatically).

---

### User Story 3 -- Tool Quarantine Approval (Priority: P1)

As a user, I want to approve quarantined tools directly in the tray app without opening the Web UI, so I can unblock my AI agents quickly.

**Why P1**: Quarantine blocks tool execution. If a user adds a new server, all its tools are pending approval. The tray app currently only shows quarantined server count and an "Approve" button that unquarantines the entire server -- it does not handle tool-level quarantine (Spec 032).

**Acceptance Scenarios**:

1. **Given** a server has tools with `pending` or `changed` quarantine status, **When** I view the server detail view, **Then** the Tools tab shows a prominent "N tool(s) need approval" banner at the top, matching the Web UI pattern.
2. **Given** the quarantine banner is visible, **When** I click "Approve All", **Then** `POST /api/v1/servers/{id}/tools/approve` is called (with no body to approve all). The banner disappears and tools become available.
3. **Given** a quarantined tool with `changed` status, **When** I view it in the tools list, **Then** it shows a "Changed" badge in orange/red. Clicking it shows the tool name, new description, and an individual "Approve" button.
4. **Given** the tray menu shows a "Needs Attention" section for a server with `action: "approve"`, **When** I click it, **Then** the main window opens to the server detail view with the Tools tab active, showing the quarantine banner.
5. **Given** tools are approved, **When** the SSE `servers.changed` event fires, **Then** the quarantine counts in the tray menu and server list update automatically.

**New API Calls**:
- `POST /api/v1/servers/{id}/tools/approve` -- approve all pending/changed tools (already wired in APIClient as `approveTools()`)
- `POST /api/v1/servers/{id}/tools/approve` with body `{"tools": ["tool1", "tool2"]}` -- approve specific tools

**New Swift Models**:
The `ServerTool` model from User Story 1 covers this. Add a quarantine status field:
```swift
struct ToolApprovalInfo: Codable {
    let toolName: String
    let status: String  // "pending", "changed", "approved"
    let description: String?
    let previousDescription: String?
    let currentDescription: String?
}
```

**Implementation Notes**:
- The `GET /api/v1/servers/{id}/tools` response already includes quarantine metadata per tool when quarantine is enabled. Parse the `quarantine` field from each tool entry.
- The existing `appState.quarantinedToolsCount` and `serversNeedingAttention` already feed the tray menu. The improvement is in the action taken when clicking -- navigate to server detail instead of just calling `unquarantineServer`.
- For the tray menu "approve" action, update `handleServerAction` in TrayMenu.swift to open the main window at the correct server + Tools tab.

---

### User Story 4 -- Server Actions in Main Window (Priority: P2)

As a user, I want to enable, disable, restart, or log in to a server directly from the main window server list, so I do not have to use the tray menu submenu.

**Why P2**: The tray menu submenus work but are hard to discover. Main window context menus are the standard macOS interaction pattern.

**Acceptance Scenarios**:

1. **Given** the Servers tab is active, **When** I right-click a server row, **Then** a context menu appears with: Enable/Disable (toggle based on current state), Restart, Log In (if `health.action == "login"`), View in Web UI.
2. **Given** the Servers tab is active, **When** I double-click a server row, **Then** the server detail view opens (User Story 1).
3. **Given** the server detail view is open, **When** I look at the header area, **Then** I see action buttons: an Enable/Disable toggle, a Restart button, and a Login button (conditionally shown when `health.action == "login"`).

**Implementation Notes**:
- Add `NSTableViewDelegate.tableView(_:rowActionsForRow:edge:)` for swipe actions (optional, macOS 11+).
- Add a right-click handler via `menu(for:)` on the NSTableView.
- The Coordinator already has access to `apiClient`, so calling enable/disable/restart/login is straightforward.
- For double-click: set `tableView.doubleAction` to a selector on the Coordinator that sets `selectedServer`.

---

### User Story 5 -- Action Feedback (Priority: P2)

As a user, after I trigger an action (enable, disable, restart, login, approve), I want to see brief visual feedback confirming the action succeeded or failed.

**Why P2**: Currently actions fire silently. Users have no way to know if an action worked without checking server status.

**Acceptance Scenarios**:

1. **Given** I click "Enable" on a disabled server, **When** the API call completes successfully, **Then** a brief in-app notification appears (e.g., toast or status bar message): "github-server enabled". The server's health dot updates.
2. **Given** I click "Restart" on a server, **When** the API call fails, **Then** a brief error notification appears: "Failed to restart github-server: HTTP 500: internal error".
3. **Given** I approve quarantined tools, **When** the approval completes, **Then** a notification appears: "3 tools approved for github-server".
4. **Given** any action is in progress, **Then** the button shows a loading spinner (ProgressView) and is disabled to prevent double-clicks.

**Implementation Notes**:
- Use a lightweight toast/banner system. Options:
  - A `@Published var toastMessage: String?` on AppState with auto-dismiss after 3 seconds.
  - macOS `NSUserNotificationCenter` or `UNUserNotificationCenter` for system notifications (heavier, but works when main window is closed).
- For in-app toasts: overlay a `Text` with background at the bottom of MainWindow that slides in/out.
- For tray menu actions: use `NotificationService` (already exists) to post a macOS notification.
- Loading state: wrap API calls with `@State private var isActionLoading = false`.

---

### User Story 6 -- Tool Search (Priority: P2)

As a user, I want to search across all tools from all servers in the main window, so I can quickly find a tool without knowing which server provides it.

**Why P2**: Valuable for power users with many servers. The BM25 search infrastructure already exists in the backend.

**Acceptance Scenarios**:

1. **Given** the main window is open, **When** I click the "Search" sidebar item, **Then** a search view appears with a text field.
2. **Given** the Search tab is active, **When** I type a query and press Enter (or after a 500ms debounce), **Then** `GET /api/v1/index/search?q=<query>&limit=20` is called and results are displayed as a list of cards showing: tool name, server name (badge), description (2-line truncated), relevance score.
3. **Given** search results are displayed, **When** I click a result, **Then** the server detail view opens with the Tools tab active, scrolled to that tool.
4. **Given** search results are empty, **Then** a "No tools found" message is shown with a suggestion to check server connections.

**New API Calls**:
- `GET /api/v1/index/search?q=<query>&limit=<limit>` -- BM25 tool search

**New Swift Models**:
```swift
struct SearchResult: Codable, Identifiable {
    var id: String { "\(tool.serverName ?? ""):\(tool.name)" }
    let score: Double
    let matches: Int?
    let snippet: String?
    let tool: SearchTool
}

struct SearchTool: Codable {
    let name: String
    let description: String?
    let serverName: String?
    let annotations: ToolAnnotation?

    enum CodingKeys: String, CodingKey {
        case name, description, annotations
        case serverName = "server_name"
    }
}

struct SearchToolsResponse: Codable {
    let query: String
    let results: [SearchResult]
    let total: Int
    let took: String?
}
```

**New Swift Views**:
- `SearchView.swift` -- search input + results list

**Implementation Notes**:
- Add `case search = "Search"` to `SidebarItem` enum in MainWindow.swift with icon `magnifyingglass`.
- Debounce search input by 500ms to avoid excessive API calls while typing.
- Results should use a similar card layout to the ServersView table but optimized for search results.

---

## API Client Extensions

The following methods should be added to `APIClient.swift`:

```swift
// MARK: - Server Detail

/// Fetch tools for a specific server.
func serverTools(_ id: String) async throws -> [ServerTool] {
    let response: ServerToolsResponse = try await fetchWrapped(
        path: "/api/v1/servers/\(id)/tools"
    )
    return response.tools
}

/// Fetch log lines for a specific server.
func serverLogs(_ id: String, tail: Int = 100) async throws -> [String] {
    let response: ServerLogsResponse = try await fetchWrapped(
        path: "/api/v1/servers/\(id)/logs?tail=\(tail)"
    )
    return response.lines
}

// MARK: - Add / Import

/// Add a new server via POST /api/v1/servers.
func addServer(_ request: AddServerRequest) async throws {
    let body = try JSONEncoder().encode(request)
    try await postAction(path: "/api/v1/servers", bodyData: body)
}

/// Fetch canonical config paths for import.
func importPaths() async throws -> [CanonicalConfigPath] {
    let response: CanonicalConfigPathsResponse = try await fetchWrapped(
        path: "/api/v1/servers/import/paths"
    )
    return response.paths
}

/// Import servers from a filesystem path.
func importFromPath(_ path: String, format: String? = nil) async throws -> ImportResponse {
    var body: [String: Any] = ["path": path]
    if let format { body["format"] = format }
    let data = try await postRaw(path: "/api/v1/servers/import/path", body: body)
    return try JSONDecoder().decode(
        APIResponse<ImportResponse>.self, from: data
    ).data!
}

// MARK: - Tool Search

/// Search tools across all servers.
func searchTools(query: String, limit: Int = 20) async throws -> SearchToolsResponse {
    let encoded = query.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? query
    return try await fetchWrapped(
        path: "/api/v1/index/search?q=\(encoded)&limit=\(limit)"
    )
}

// MARK: - Tool Quarantine

/// Approve specific tools for a server.
func approveSpecificTools(_ id: String, tools: [String]) async throws {
    let body: [String: Any] = ["tools": tools]
    try await postAction(path: "/api/v1/servers/\(id)/tools/approve", body: body)
}
```

## File Changes Summary

### New Files

| File | Purpose |
|------|---------|
| `Views/ServerDetailView.swift` | Server detail container with header and tab bar |
| `Views/ServerToolsTab.swift` | Tool list with annotation badges and quarantine banner |
| `Views/ServerLogsTab.swift` | Monospaced log viewer with refresh |
| `Views/ServerConfigTab.swift` | Read-only configuration display |
| `Views/AddServerSheet.swift` | Add/Import server dialog |
| `Views/ManualServerForm.swift` | Manual server creation form |
| `Views/ImportServerForm.swift` | Canonical path discovery and import |
| `Views/SearchView.swift` | BM25 tool search interface |

### Modified Files

| File | Changes |
|------|---------|
| `API/APIClient.swift` | Add `serverTools()`, `serverLogs()`, `addServer()`, `importPaths()`, `importFromPath()`, `searchTools()`, `approveSpecificTools()` |
| `API/Models.swift` | Add `ServerTool`, `ToolAnnotation`, `ServerLogsResponse`, `CanonicalConfigPath`, `AddServerRequest`, `ImportResponse`, `SearchResult`, `SearchToolsResponse` |
| `Views/ServersView.swift` | Add double-click handler, right-click context menu, `@State selectedServer` for navigation to detail view |
| `Views/MainWindow.swift` | Add `case search` to SidebarItem enum, wire SearchView in the detail switch, add "Add Server..." toolbar button |
| `Menu/TrayMenu.swift` | Add "Add Server..." menu item in actionsSection. Update `handleServerAction` for `approve` to open main window at server detail. |

## Non-Goals

- **Editing server configuration in-place** -- the tray app shows config read-only. Editing requires the Web UI or direct file editing. This avoids complexity around config file writes and concurrent modification.
- **Server removal from tray** -- delete is destructive and rare. Keep it in Web UI only for now.
- **Real-time log streaming** -- the Logs tab fetches a snapshot. True `--follow` would require WebSocket or SSE per-server, which is out of scope.
- **Tool execution from tray** -- the tray is for monitoring and management, not for calling tools.
- **Agent token management** -- already has its own TokensView in the main window.

## Dependencies

- All backend API endpoints referenced above already exist and are documented in the OAS (`oas/swagger.yaml`).
- No backend changes are required for this spec. All work is in the Swift tray app.
- The Web UI implementations in `ServerDetail.vue`, `AddServerModal.vue`, and `Search.vue` serve as feature reference for parity.
