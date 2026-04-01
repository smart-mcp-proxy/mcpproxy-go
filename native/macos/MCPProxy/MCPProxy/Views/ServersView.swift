// ServersView.swift
// MCPProxy
//
// APPROACH: Use AppKit NSTableView via NSViewRepresentable to display servers.
// NSTableView has zero duplication issues and proper view recycling.
// Docker Desktop-style multi-column table with Status, Name, Type, Status text, Tools, Token Size, Actions.

import SwiftUI
import AppKit

// MARK: - Servers View (SwiftUI shell with AppKit table)

struct ServersView: View {
    @ObservedObject var appState: AppState
    @Environment(\.fontScale) var fontScale

    @State private var servers: [ServerStatus] = []
    @State private var isLoading = false
    @State private var loadTask: Task<Void, Never>?
    @State private var selectedServer: ServerStatus?
    @State private var showAddServer = false
    @State private var addServerInitialTab: AddServerTab = .manual

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            if let server = selectedServer {
                ServerDetailView(
                    server: server,
                    appState: appState,
                    onDismiss: { selectedServer = nil }
                )
            } else {
                serverListView
            }
        }
        .sheet(isPresented: $showAddServer) {
            AddServerView(appState: appState, isPresented: $showAddServer, initialTab: addServerInitialTab)
                .id(addServerInitialTab)
        }
    }

    @ViewBuilder
    private var serverListView: some View {
        VStack(alignment: .leading, spacing: 0) {
            // Header
            HStack {
                Text("Servers")
                    .font(.scaled(.title2, scale: fontScale).bold())
                Spacer()
                Text("\(appState.connectedCount)/\(appState.totalServers) connected")
                    .foregroundStyle(.secondary)
                Text("\u{00B7}")
                    .foregroundStyle(.secondary)
                Text("\(appState.totalTools) tools")
                    .foregroundStyle(.secondary)

                Button {
                    addServerInitialTab = .importConfig
                    showAddServer = true
                } label: {
                    Image(systemName: "plus")
                }
                .buttonStyle(.borderless)
                .help("Add Server...")

                if isLoading {
                    ProgressView().controlSize(.small)
                } else {
                    Button {
                        triggerLoad()
                    } label: {
                        Image(systemName: "arrow.clockwise")
                    }
                    .buttonStyle(.borderless)
                    .accessibilityIdentifier("servers-refresh")
                }
            }
            .padding()
            .accessibilityIdentifier("servers-header")

            // Prominent "Add Server" button bar
            HStack {
                Button {
                    addServerInitialTab = .importConfig
                    showAddServer = true
                } label: {
                    Label("Add Server", systemImage: "plus.circle.fill")
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
            }
            .padding(.horizontal)
            .padding(.vertical, 8)

            Divider()

            if servers.isEmpty && !isLoading {
                if appState.coreState != .connected {
                    // Core is not running — explain why servers list is empty
                    VStack(spacing: 16) {
                        Spacer()
                        Image(systemName: appState.isStopped ? "stop.circle.fill" : "server.rack")
                            .font(.system(size: 48 * fontScale))
                            .foregroundStyle(.tertiary)
                        Text(appState.isStopped ? "MCPProxy Core is Stopped" : "MCPProxy Core is Not Running")
                            .font(.scaled(.title3, scale: fontScale))
                            .foregroundStyle(.secondary)
                        Text("Start the core to see your servers")
                            .font(.scaled(.body, scale: fontScale))
                            .foregroundStyle(.tertiary)
                        Spacer()
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                } else {
                    // Empty state when no servers
                    VStack(spacing: 16) {
                        Spacer()
                        Image(systemName: "server.rack")
                            .font(.system(size: 48 * fontScale))
                            .foregroundStyle(.secondary)
                        Text("No Servers Configured")
                            .font(.scaled(.headline, scale: fontScale))
                        Text("Add your first MCP server or import from an existing AI tool configuration.")
                            .font(.scaled(.subheadline, scale: fontScale))
                            .foregroundStyle(.secondary)
                            .multilineTextAlignment(.center)
                            .frame(maxWidth: 300)
                        HStack(spacing: 12) {
                            Button {
                                addServerInitialTab = .manual
                                showAddServer = true
                            } label: {
                                Label("Add Server", systemImage: "plus.circle.fill")
                            }
                            .buttonStyle(.borderedProminent)
                            Button {
                                addServerInitialTab = .importConfig
                                showAddServer = true
                            } label: {
                                Label("Import", systemImage: "square.and.arrow.down")
                            }
                            .buttonStyle(.bordered)
                        }
                        Spacer()
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }

            // AppKit NSTableView -- Docker Desktop-style multi-column
            ServerTableView(
                servers: $servers,
                apiClient: appState.apiClient,
                fontScale: fontScale,
                onDoubleClick: { server in
                    selectedServer = server
                },
                onServersChanged: {
                    triggerLoad()
                }
            )
            .accessibilityIdentifier("servers-list")
        }
        .onAppear {
            triggerLoad()
        }
        .onChange(of: appState.serversVersion) { _ in
            triggerLoad()
        }
        .onReceive(NotificationCenter.default.publisher(for: .showAddServer)) { notification in
            if let tab = notification.object as? AddServerTab {
                addServerInitialTab = tab
            } else {
                addServerInitialTab = .manual
            }
            showAddServer = true
        }
        .onReceive(NotificationCenter.default.publisher(for: .showServerDetail)) { notification in
            guard let serverName = notification.object as? String else { return }
            // Find the server by name in the current list or appState
            if let server = servers.first(where: { $0.name == serverName })
                ?? appState.servers.first(where: { $0.name == serverName }) {
                selectedServer = server
            }
        }
    }

    private func triggerLoad() {
        loadTask?.cancel()
        loadTask = Task {
            guard let client = appState.apiClient else {
                servers = appState.servers
                return
            }
            isLoading = true
            do {
                servers = try await client.servers()
            } catch {
                servers = appState.servers
            }
            isLoading = false
        }
    }
}

// MARK: - Column Identifiers

enum ServerColumn: String, CaseIterable {
    case status = "status"
    case name = "name"
    case type = "type"
    case state = "state"
    case tools = "tools"
    case pending = "pending"
    case tokenSize = "tokenSize"
    case actions = "actions"

    var identifier: NSUserInterfaceItemIdentifier {
        NSUserInterfaceItemIdentifier(rawValue)
    }

    var title: String {
        switch self {
        case .status: return ""
        case .name: return "Name"
        case .type: return "Type"
        case .state: return "Status"
        case .tools: return "Tools"
        case .pending: return "Pending"
        case .tokenSize: return "Tokens"
        case .actions: return "Actions"
        }
    }

    var minWidth: CGFloat {
        switch self {
        case .status: return 30
        case .name: return 120
        case .type: return 50
        case .state: return 80
        case .tools: return 45
        case .pending: return 60
        case .tokenSize: return 60
        case .actions: return 120
        }
    }

    var maxWidth: CGFloat {
        switch self {
        case .status: return 30
        case .name: return 400
        case .type: return 60
        case .state: return 160
        case .tools: return 50
        case .pending: return 80
        case .tokenSize: return 80
        case .actions: return 120
        }
    }

    var resizingMask: NSTableColumn.ResizingOptions {
        switch self {
        case .status, .tools, .pending, .actions: return .userResizingMask
        case .name: return [.userResizingMask, .autoresizingMask]
        default: return .userResizingMask
        }
    }
}

// MARK: - AppKit NSTableView wrapper

struct ServerTableView: NSViewRepresentable {
    @Binding var servers: [ServerStatus]
    let apiClient: APIClient?
    var fontScale: CGFloat = 1.0
    var onDoubleClick: ((ServerStatus) -> Void)?
    var onServersChanged: (() -> Void)?

    func makeNSView(context: Context) -> NSScrollView {
        let scrollView = NSScrollView()
        scrollView.hasVerticalScroller = true
        scrollView.hasHorizontalScroller = false
        scrollView.autohidesScrollers = true

        let tableView = NSTableView()
        tableView.style = .fullWidth
        tableView.rowHeight = 28
        tableView.usesAlternatingRowBackgroundColors = true
        tableView.intercellSpacing = NSSize(width: 12, height: 0)
        tableView.columnAutoresizingStyle = .lastColumnOnlyAutoresizingStyle

        // Create columns with sort descriptors
        for col in ServerColumn.allCases {
            let column = NSTableColumn(identifier: col.identifier)
            column.title = col.title
            column.minWidth = col.minWidth
            column.maxWidth = col.maxWidth
            column.width = col.minWidth
            column.resizingMask = col.resizingMask
            // Add sort descriptor for sortable columns (not status dot or actions)
            if col != .status && col != .actions {
                column.sortDescriptorPrototype = NSSortDescriptor(key: col.rawValue, ascending: true)
            }
            tableView.addTableColumn(column)
        }

        // Enable header
        tableView.headerView = NSTableHeaderView()

        // Set default sort by name ascending
        if let nameCol = tableView.tableColumns.first(where: { $0.identifier.rawValue == ServerColumn.name.rawValue }),
           let descriptor = nameCol.sortDescriptorPrototype {
            tableView.sortDescriptors = [descriptor]
        }

        tableView.delegate = context.coordinator
        tableView.dataSource = context.coordinator

        // Double-click handler
        tableView.doubleAction = #selector(Coordinator.tableViewDoubleClicked(_:))
        tableView.target = context.coordinator

        // Right-click context menu
        let menu = NSMenu()
        menu.delegate = context.coordinator
        tableView.menu = menu

        scrollView.documentView = tableView
        context.coordinator.tableView = tableView
        return scrollView
    }

    func updateNSView(_ nsView: NSScrollView, context: Context) {
        context.coordinator.servers = servers
        context.coordinator.apiClient = apiClient
        context.coordinator.fontScale = fontScale
        context.coordinator.onDoubleClick = onDoubleClick
        context.coordinator.onServersChanged = onServersChanged
        context.coordinator.tableView?.reloadData()
    }

    func makeCoordinator() -> Coordinator {
        Coordinator()
    }

    // MARK: - Coordinator

    class Coordinator: NSObject, NSTableViewDataSource, NSTableViewDelegate, NSMenuDelegate {
        var servers: [ServerStatus] = []
        var apiClient: APIClient?
        var fontScale: CGFloat = 1.0
        var onDoubleClick: ((ServerStatus) -> Void)?
        var onServersChanged: (() -> Void)?
        weak var tableView: NSTableView?

        // Sort state
        var sortColumn: ServerColumn = .name
        var sortAscending: Bool = true

        /// Servers sorted by current sort column and direction.
        var sortedServers: [ServerStatus] {
            servers.sorted { a, b in
                let result: Bool
                switch sortColumn {
                case .status:
                    result = a.name.localizedCaseInsensitiveCompare(b.name) == .orderedAscending
                case .name:
                    result = a.name.localizedCaseInsensitiveCompare(b.name) == .orderedAscending
                case .type:
                    result = a.protocol.localizedCaseInsensitiveCompare(b.protocol) == .orderedAscending
                case .state:
                    result = stateOrder(a) < stateOrder(b)
                case .tools:
                    result = a.toolCount < b.toolCount
                case .pending:
                    result = a.pendingApprovalCount < b.pendingApprovalCount
                case .tokenSize:
                    result = (a.toolListTokenSize ?? 0) < (b.toolListTokenSize ?? 0)
                case .actions:
                    result = a.name.localizedCaseInsensitiveCompare(b.name) == .orderedAscending
                }
                return sortAscending ? result : !result
            }
        }

        /// Numeric ordering for server state (for stable sort).
        private func stateOrder(_ server: ServerStatus) -> Int {
            if server.quarantined { return 3 }
            if !server.enabled { return 4 }
            if server.connected { return 0 }
            if let health = server.health, health.level == "unhealthy" { return 2 }
            return 1 // disconnected
        }

        // MARK: - Sort Descriptors Changed

        func tableView(_ tableView: NSTableView, sortDescriptorsDidChange oldDescriptors: [NSSortDescriptor]) {
            guard let descriptor = tableView.sortDescriptors.first,
                  let key = descriptor.key,
                  let col = ServerColumn(rawValue: key) else { return }
            sortColumn = col
            sortAscending = descriptor.ascending
            tableView.reloadData()
        }

        // MARK: - Double Click

        @objc func tableViewDoubleClicked(_ sender: NSTableView) {
            let row = sender.clickedRow
            let sorted = sortedServers
            guard row >= 0, row < sorted.count else { return }
            onDoubleClick?(sorted[row])
        }

        // MARK: - Action Button Handlers

        @objc func infoButtonClicked(_ sender: NSButton) {
            let row = sender.tag
            let sorted = sortedServers
            guard row >= 0, row < sorted.count else { return }
            onDoubleClick?(sorted[row])
        }

        @objc func toggleEnabledClicked(_ sender: NSButton) {
            let row = sender.tag
            let sorted = sortedServers
            guard row >= 0, row < sorted.count else { return }
            let server = sorted[row]
            Task {
                if server.enabled {
                    try? await apiClient?.disableServer(server.id)
                } else {
                    try? await apiClient?.enableServer(server.id)
                }
                await MainActor.run { onServersChanged?() }
            }
        }

        @objc func restartButtonClicked(_ sender: NSButton) {
            let row = sender.tag
            let sorted = sortedServers
            guard row >= 0, row < sorted.count else { return }
            let server = sorted[row]
            Task {
                try? await apiClient?.restartServer(server.id)
                await MainActor.run { onServersChanged?() }
            }
        }

        @objc func deleteButtonClicked(_ sender: NSButton) {
            let row = sender.tag
            let sorted = sortedServers
            guard row >= 0, row < sorted.count else { return }
            let server = sorted[row]

            // Show confirmation alert
            let alert = NSAlert()
            alert.messageText = "Delete Server"
            alert.informativeText = "Are you sure you want to delete \"\(server.name)\"? This action cannot be undone."
            alert.alertStyle = .warning
            alert.addButton(withTitle: "Delete")
            alert.addButton(withTitle: "Cancel")

            // Style the Delete button as destructive
            alert.buttons[0].hasDestructiveAction = true

            let response = alert.runModal()
            if response == .alertFirstButtonReturn {
                Task {
                    try? await apiClient?.deleteServer(server.id)
                    await MainActor.run { onServersChanged?() }
                }
            }
        }

        // MARK: - Right-Click Context Menu

        func menuNeedsUpdate(_ menu: NSMenu) {
            menu.removeAllItems()
            guard let tableView else { return }
            let row = tableView.clickedRow
            let sorted = sortedServers
            guard row >= 0, row < sorted.count else { return }
            let server = sorted[row]

            // Enable/Disable (stdio servers use Stop/Start terminology)
            if server.enabled {
                let disableLabel = server.protocol == "stdio" ? "Stop" : "Disable"
                let disable = NSMenuItem(title: disableLabel, action: #selector(ctxDisableServer(_:)), keyEquivalent: "")
                disable.target = self
                disable.representedObject = server
                menu.addItem(disable)
            } else {
                let enableLabel = server.protocol == "stdio" ? "Start" : "Enable"
                let enable = NSMenuItem(title: enableLabel, action: #selector(ctxEnableServer(_:)), keyEquivalent: "")
                enable.target = self
                enable.representedObject = server
                menu.addItem(enable)
            }

            // Restart
            let restart = NSMenuItem(title: "Restart", action: #selector(ctxRestartServer(_:)), keyEquivalent: "")
            restart.target = self
            restart.representedObject = server
            menu.addItem(restart)

            // Log In (if auth needed)
            if server.health?.action == "login" {
                menu.addItem(.separator())
                let login = NSMenuItem(title: "Log In", action: #selector(ctxLoginServer(_:)), keyEquivalent: "")
                login.target = self
                login.representedObject = server
                login.image = NSImage(systemSymbolName: "person.badge.key", accessibilityDescription: "login")
                menu.addItem(login)
            }

            // Approve Tools (if quarantined)
            if server.pendingApprovalCount > 0 {
                menu.addItem(.separator())
                let approve = NSMenuItem(title: "Approve All Tools", action: #selector(ctxApproveTools(_:)), keyEquivalent: "")
                approve.target = self
                approve.representedObject = server
                approve.image = NSImage(systemSymbolName: "checkmark.shield", accessibilityDescription: "approve")
                menu.addItem(approve)
            }

            menu.addItem(.separator())

            // View Details
            let details = NSMenuItem(title: "View Details", action: #selector(ctxViewDetails(_:)), keyEquivalent: "")
            details.target = self
            details.representedObject = server
            menu.addItem(details)

            // View Logs
            let logs = NSMenuItem(title: "View Logs", action: #selector(ctxViewLogs(_:)), keyEquivalent: "")
            logs.target = self
            logs.representedObject = server
            menu.addItem(logs)

            menu.addItem(.separator())

            // Delete
            let delete = NSMenuItem(title: "Delete Server", action: #selector(ctxDeleteServer(_:)), keyEquivalent: "")
            delete.target = self
            delete.representedObject = server
            delete.image = NSImage(systemSymbolName: "trash", accessibilityDescription: "delete")
            menu.addItem(delete)
        }

        @objc private func ctxEnableServer(_ sender: NSMenuItem) {
            guard let server = sender.representedObject as? ServerStatus else { return }
            Task {
                try? await apiClient?.enableServer(server.id)
                await MainActor.run { onServersChanged?() }
            }
        }

        @objc private func ctxDisableServer(_ sender: NSMenuItem) {
            guard let server = sender.representedObject as? ServerStatus else { return }
            Task {
                try? await apiClient?.disableServer(server.id)
                await MainActor.run { onServersChanged?() }
            }
        }

        @objc private func ctxRestartServer(_ sender: NSMenuItem) {
            guard let server = sender.representedObject as? ServerStatus else { return }
            Task {
                try? await apiClient?.restartServer(server.id)
                await MainActor.run { onServersChanged?() }
            }
        }

        @objc private func ctxLoginServer(_ sender: NSMenuItem) {
            guard let server = sender.representedObject as? ServerStatus else { return }
            Task { try? await apiClient?.loginServer(server.id) }
        }

        @objc private func ctxApproveTools(_ sender: NSMenuItem) {
            guard let server = sender.representedObject as? ServerStatus else { return }
            Task {
                try? await apiClient?.approveTools(server.id)
                await MainActor.run { onServersChanged?() }
            }
        }

        @objc private func ctxViewDetails(_ sender: NSMenuItem) {
            guard let server = sender.representedObject as? ServerStatus else { return }
            onDoubleClick?(server)
        }

        @objc private func ctxViewLogs(_ sender: NSMenuItem) {
            guard let server = sender.representedObject as? ServerStatus else { return }
            let home = FileManager.default.homeDirectoryForCurrentUser
            let logFile = home.appendingPathComponent("Library/Logs/mcpproxy/server-\(server.name).log")
            if FileManager.default.fileExists(atPath: logFile.path) {
                NSWorkspace.shared.open(logFile)
            }
        }

        @objc private func ctxDeleteServer(_ sender: NSMenuItem) {
            guard let server = sender.representedObject as? ServerStatus else { return }
            let alert = NSAlert()
            alert.messageText = "Delete Server"
            alert.informativeText = "Are you sure you want to delete \"\(server.name)\"? This action cannot be undone."
            alert.alertStyle = .warning
            alert.addButton(withTitle: "Delete")
            alert.addButton(withTitle: "Cancel")
            alert.buttons[0].hasDestructiveAction = true

            let response = alert.runModal()
            if response == .alertFirstButtonReturn {
                Task {
                    try? await apiClient?.deleteServer(server.id)
                    await MainActor.run { onServersChanged?() }
                }
            }
        }

        // MARK: - Data Source

        func numberOfRows(in tableView: NSTableView) -> Int {
            sortedServers.count
        }

        // MARK: - Delegate

        func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
            let sorted = sortedServers
            guard row < sorted.count, let colId = tableColumn?.identifier else { return nil }
            let server = sorted[row]

            guard let column = ServerColumn(rawValue: colId.rawValue) else { return nil }

            switch column {
            case .status:
                return makeStatusDotCell(server: server, tableView: tableView)
            case .name:
                return makeNameCell(server: server, tableView: tableView)
            case .type:
                return makeTypeCell(server: server, tableView: tableView)
            case .state:
                return makeStateCell(server: server, tableView: tableView)
            case .tools:
                return makeToolsCell(server: server, tableView: tableView)
            case .pending:
                return makePendingCell(server: server, tableView: tableView)
            case .tokenSize:
                return makeTokenSizeCell(server: server, tableView: tableView)
            case .actions:
                return makeActionsCell(server: server, row: row, tableView: tableView)
            }
        }

        // MARK: - Cell Factories

        private func makeStatusDotCell(server: ServerStatus, tableView: NSTableView) -> NSView {
            let cellId = NSUserInterfaceItemIdentifier("StatusDotCell")
            let cell = reuseOrCreate(tableView: tableView, identifier: cellId)

            let dot = NSView()
            dot.wantsLayer = true
            dot.layer?.cornerRadius = 5
            dot.layer?.backgroundColor = healthColor(for: server).cgColor
            dot.translatesAutoresizingMaskIntoConstraints = false
            dot.setAccessibilityLabel("Health: \(server.health?.level ?? (server.connected ? "connected" : "disconnected"))")
            cell.addSubview(dot)
            NSLayoutConstraint.activate([
                dot.widthAnchor.constraint(equalToConstant: 10),
                dot.heightAnchor.constraint(equalToConstant: 10),
                dot.centerXAnchor.constraint(equalTo: cell.centerXAnchor),
                dot.centerYAnchor.constraint(equalTo: cell.centerYAnchor)
            ])
            return cell
        }

        private func makeNameCell(server: ServerStatus, tableView: NSTableView) -> NSView {
            let cellId = NSUserInterfaceItemIdentifier("NameCell")
            let cell = reuseOrCreate(tableView: tableView, identifier: cellId)

            let label = NSTextField(labelWithString: server.name)
            label.font = .systemFont(ofSize: NSFont.systemFontSize * fontScale, weight: .semibold)
            label.lineBreakMode = .byTruncatingTail
            label.translatesAutoresizingMaskIntoConstraints = false
            cell.addSubview(label)
            NSLayoutConstraint.activate([
                label.leadingAnchor.constraint(equalTo: cell.leadingAnchor, constant: 4),
                label.trailingAnchor.constraint(equalTo: cell.trailingAnchor, constant: -4),
                label.centerYAnchor.constraint(equalTo: cell.centerYAnchor)
            ])
            return cell
        }

        private func makeTypeCell(server: ServerStatus, tableView: NSTableView) -> NSView {
            let cellId = NSUserInterfaceItemIdentifier("TypeCell")
            let cell = reuseOrCreate(tableView: tableView, identifier: cellId)

            let label = NSTextField(labelWithString: server.protocol)
            label.font = .systemFont(ofSize: NSFont.smallSystemFontSize * fontScale)
            label.textColor = .secondaryLabelColor
            label.lineBreakMode = .byTruncatingTail
            label.translatesAutoresizingMaskIntoConstraints = false
            cell.addSubview(label)
            NSLayoutConstraint.activate([
                label.leadingAnchor.constraint(equalTo: cell.leadingAnchor, constant: 4),
                label.trailingAnchor.constraint(equalTo: cell.trailingAnchor, constant: -4),
                label.centerYAnchor.constraint(equalTo: cell.centerYAnchor)
            ])
            return cell
        }

        private func makeStateCell(server: ServerStatus, tableView: NSTableView) -> NSView {
            let cellId = NSUserInterfaceItemIdentifier("StateCell")
            let cell = reuseOrCreate(tableView: tableView, identifier: cellId)

            let statusText: String
            let statusColor: NSColor
            if server.quarantined {
                statusText = "Quarantined"
                statusColor = .systemOrange
            } else if !server.enabled {
                statusText = "Disabled"
                statusColor = .systemGray
            } else if server.connected {
                statusText = "Connected"
                statusColor = .systemGreen
            } else if let health = server.health {
                statusText = health.summary
                statusColor = health.level == "unhealthy" ? .systemRed : .secondaryLabelColor
            } else {
                statusText = "Disconnected"
                statusColor = .systemGray
            }

            let label = NSTextField(labelWithString: statusText)
            label.font = .systemFont(ofSize: NSFont.smallSystemFontSize * fontScale)
            label.textColor = statusColor
            label.lineBreakMode = .byTruncatingTail
            label.translatesAutoresizingMaskIntoConstraints = false
            cell.addSubview(label)
            cell.toolTip = server.health?.detail ?? server.health?.summary ?? ""
            NSLayoutConstraint.activate([
                label.leadingAnchor.constraint(equalTo: cell.leadingAnchor, constant: 4),
                label.trailingAnchor.constraint(equalTo: cell.trailingAnchor, constant: -4),
                label.centerYAnchor.constraint(equalTo: cell.centerYAnchor)
            ])
            return cell
        }

        private func makeToolsCell(server: ServerStatus, tableView: NSTableView) -> NSView {
            let cellId = NSUserInterfaceItemIdentifier("ToolsCell")
            let cell = reuseOrCreate(tableView: tableView, identifier: cellId)

            let text = server.toolCount > 0 ? "\(server.toolCount)" : "-"
            let label = NSTextField(labelWithString: text)
            label.font = .monospacedDigitSystemFont(ofSize: NSFont.smallSystemFontSize * fontScale, weight: .regular)
            label.textColor = .secondaryLabelColor
            label.alignment = .right
            label.translatesAutoresizingMaskIntoConstraints = false
            cell.addSubview(label)
            NSLayoutConstraint.activate([
                label.leadingAnchor.constraint(equalTo: cell.leadingAnchor, constant: 4),
                label.trailingAnchor.constraint(equalTo: cell.trailingAnchor, constant: -4),
                label.centerYAnchor.constraint(equalTo: cell.centerYAnchor)
            ])
            return cell
        }

        private func makePendingCell(server: ServerStatus, tableView: NSTableView) -> NSView {
            let cellId = NSUserInterfaceItemIdentifier("PendingCell")
            let cell = reuseOrCreate(tableView: tableView, identifier: cellId)

            let count = server.pendingApprovalCount
            let text = count > 0 ? "\(count)" : "-"
            let label = NSTextField(labelWithString: text)
            label.font = .monospacedDigitSystemFont(ofSize: NSFont.smallSystemFontSize * fontScale, weight: .regular)
            label.textColor = count > 0 ? .systemOrange : .secondaryLabelColor
            label.alignment = .right
            label.translatesAutoresizingMaskIntoConstraints = false
            cell.addSubview(label)
            if count > 0 {
                cell.toolTip = "\(count) tool(s) awaiting approval"
            }
            NSLayoutConstraint.activate([
                label.leadingAnchor.constraint(equalTo: cell.leadingAnchor, constant: 4),
                label.trailingAnchor.constraint(equalTo: cell.trailingAnchor, constant: -4),
                label.centerYAnchor.constraint(equalTo: cell.centerYAnchor)
            ])
            return cell
        }

        private func makeTokenSizeCell(server: ServerStatus, tableView: NSTableView) -> NSView {
            let cellId = NSUserInterfaceItemIdentifier("TokenSizeCell")
            let cell = reuseOrCreate(tableView: tableView, identifier: cellId)

            let text: String
            if let size = server.toolListTokenSize, size > 0 {
                text = formatTokenSize(size)
            } else {
                text = "-"
            }
            let label = NSTextField(labelWithString: text)
            label.font = .monospacedDigitSystemFont(ofSize: NSFont.smallSystemFontSize * fontScale, weight: .regular)
            label.textColor = .secondaryLabelColor
            label.alignment = .right
            label.translatesAutoresizingMaskIntoConstraints = false
            cell.addSubview(label)
            NSLayoutConstraint.activate([
                label.leadingAnchor.constraint(equalTo: cell.leadingAnchor, constant: 4),
                label.trailingAnchor.constraint(equalTo: cell.trailingAnchor, constant: -4),
                label.centerYAnchor.constraint(equalTo: cell.centerYAnchor)
            ])
            return cell
        }

        private func makeActionsCell(server: ServerStatus, row: Int, tableView: NSTableView) -> NSView {
            let cellId = NSUserInterfaceItemIdentifier("ActionsCell")
            let cell = reuseOrCreate(tableView: tableView, identifier: cellId)

            let stack = NSStackView()
            stack.orientation = .horizontal
            stack.spacing = 2
            stack.alignment = .centerY
            stack.translatesAutoresizingMaskIntoConstraints = false

            // Play/Stop toggle button
            let toggleLabel = server.protocol == "stdio"
                ? (server.enabled ? "Stop" : "Start")
                : (server.enabled ? "Disable" : "Enable")
            let toggleButton = makeIconButton(
                symbolName: server.enabled ? "stop.fill" : "play.fill",
                accessibilityLabel: toggleLabel,
                action: #selector(toggleEnabledClicked(_:)),
                tag: row
            )
            toggleButton.contentTintColor = server.enabled ? .systemGray : .systemGreen
            stack.addArrangedSubview(toggleButton)

            // Restart button
            let restartButton = makeIconButton(
                symbolName: "arrow.clockwise",
                accessibilityLabel: "Restart",
                action: #selector(restartButtonClicked(_:)),
                tag: row
            )
            stack.addArrangedSubview(restartButton)

            // Info button (opens detail)
            let infoButton = makeIconButton(
                symbolName: "info.circle",
                accessibilityLabel: "Details",
                action: #selector(infoButtonClicked(_:)),
                tag: row
            )
            stack.addArrangedSubview(infoButton)

            // Delete button
            let deleteButton = makeIconButton(
                symbolName: "trash",
                accessibilityLabel: "Delete",
                action: #selector(deleteButtonClicked(_:)),
                tag: row
            )
            deleteButton.contentTintColor = .systemRed
            stack.addArrangedSubview(deleteButton)

            cell.addSubview(stack)
            NSLayoutConstraint.activate([
                stack.leadingAnchor.constraint(equalTo: cell.leadingAnchor, constant: 2),
                stack.trailingAnchor.constraint(lessThanOrEqualTo: cell.trailingAnchor, constant: -2),
                stack.centerYAnchor.constraint(equalTo: cell.centerYAnchor)
            ])
            return cell
        }

        // MARK: - Helpers

        private func reuseOrCreate(tableView: NSTableView, identifier: NSUserInterfaceItemIdentifier) -> NSTableCellView {
            if let reused = tableView.makeView(withIdentifier: identifier, owner: nil) as? NSTableCellView {
                reused.subviews.forEach { $0.removeFromSuperview() }
                return reused
            }
            let cell = NSTableCellView()
            cell.identifier = identifier
            return cell
        }

        private func makeIconButton(symbolName: String, accessibilityLabel: String, action: Selector, tag: Int) -> NSButton {
            let button = NSButton(frame: NSRect(x: 0, y: 0, width: 24, height: 24))
            button.bezelStyle = .accessoryBarAction
            button.image = NSImage(systemSymbolName: symbolName, accessibilityDescription: accessibilityLabel)
            button.imagePosition = .imageOnly
            button.isBordered = false
            button.target = self
            button.action = action
            button.tag = tag
            button.translatesAutoresizingMaskIntoConstraints = false
            NSLayoutConstraint.activate([
                button.widthAnchor.constraint(equalToConstant: 24),
                button.heightAnchor.constraint(equalToConstant: 24)
            ])
            return button
        }

        private func formatTokenSize(_ size: Int) -> String {
            if size >= 1_000_000 {
                return String(format: "%.1fM", Double(size) / 1_000_000.0)
            } else if size >= 1_000 {
                return String(format: "%.1fK", Double(size) / 1_000.0)
            }
            return "\(size)"
        }

        private func healthColor(for server: ServerStatus) -> NSColor {
            server.statusNSColor
        }
    }
}
