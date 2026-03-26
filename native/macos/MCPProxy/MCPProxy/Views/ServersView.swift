// ServersView.swift
// MCPProxy
//
// APPROACH: Use AppKit NSTableView via NSViewRepresentable to display servers.
// NSTableView has zero duplication issues and proper view recycling.
// SwiftUI List with @ObservedObject is fundamentally broken for this use case.

import SwiftUI
import AppKit

// MARK: - Servers View (SwiftUI shell with AppKit table)

struct ServersView: View {
    @ObservedObject var appState: AppState

    @State private var servers: [ServerStatus] = []
    @State private var isLoading = false
    @State private var loadTask: Task<Void, Never>?
    @State private var selectedServer: ServerStatus?
    @State private var showAddServer = false

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
            AddServerView(appState: appState, isPresented: $showAddServer)
        }
    }

    @ViewBuilder
    private var serverListView: some View {
        VStack(alignment: .leading, spacing: 0) {
            // Header
            HStack {
                Text("Servers")
                    .font(.title2.bold())
                Spacer()
                Text("\(appState.connectedCount)/\(appState.totalServers) connected")
                    .foregroundStyle(.secondary)
                Text("\u{00B7}")
                    .foregroundStyle(.secondary)
                Text("\(appState.totalTools) tools")
                    .foregroundStyle(.secondary)

                Button {
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
                // Empty state when no servers
                VStack(spacing: 16) {
                    Spacer()
                    Image(systemName: "server.rack")
                        .font(.system(size: 48))
                        .foregroundStyle(.tertiary)
                    Text("No Servers Configured")
                        .font(.title3)
                        .foregroundStyle(.secondary)
                    Text("Add your first MCP server to get started")
                        .font(.body)
                        .foregroundStyle(.tertiary)
                    Button {
                        showAddServer = true
                    } label: {
                        Label("Add Your First Server", systemImage: "plus.circle.fill")
                    }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.large)
                    Spacer()
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }

            // AppKit NSTableView -- no duplication bugs
            ServerTableView(
                servers: $servers,
                apiClient: appState.apiClient,
                onDoubleClick: { server in
                    selectedServer = server
                }
            )
            .accessibilityIdentifier("servers-list")
        }
        .onAppear {
            triggerLoad()
        }
        .onReceive(NotificationCenter.default.publisher(for: .showAddServer)) { _ in
            showAddServer = true
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

// MARK: - AppKit NSTableView wrapper

struct ServerTableView: NSViewRepresentable {
    @Binding var servers: [ServerStatus]
    let apiClient: APIClient?
    var onDoubleClick: ((ServerStatus) -> Void)?

    func makeNSView(context: Context) -> NSScrollView {
        let scrollView = NSScrollView()
        scrollView.hasVerticalScroller = true
        scrollView.autohidesScrollers = true

        let tableView = NSTableView()
        tableView.style = .fullWidth
        tableView.rowHeight = 52
        tableView.usesAlternatingRowBackgroundColors = true
        tableView.headerView = nil  // No header row

        let col = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("server"))
        col.title = "Server"
        tableView.addTableColumn(col)

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
        context.coordinator.onDoubleClick = onDoubleClick
        context.coordinator.tableView?.reloadData()
    }

    func makeCoordinator() -> Coordinator {
        Coordinator()
    }

    class Coordinator: NSObject, NSTableViewDataSource, NSTableViewDelegate, NSMenuDelegate {
        var servers: [ServerStatus] = []
        var apiClient: APIClient?
        var onDoubleClick: ((ServerStatus) -> Void)?
        weak var tableView: NSTableView?

        // MARK: - Double Click

        @objc func tableViewDoubleClicked(_ sender: NSTableView) {
            let row = sender.clickedRow
            guard row >= 0, row < servers.count else { return }
            onDoubleClick?(servers[row])
        }

        @objc func infoButtonClicked(_ sender: NSButton) {
            let row = sender.tag
            guard row >= 0, row < servers.count else { return }
            onDoubleClick?(servers[row])
        }

        // MARK: - Right-Click Context Menu

        func menuNeedsUpdate(_ menu: NSMenu) {
            menu.removeAllItems()
            guard let tableView else { return }
            let row = tableView.clickedRow
            guard row >= 0, row < servers.count else { return }
            let server = servers[row]

            // Enable/Disable
            if server.enabled {
                let disable = NSMenuItem(title: "Disable", action: #selector(disableServer(_:)), keyEquivalent: "")
                disable.target = self
                disable.representedObject = server
                menu.addItem(disable)
            } else {
                let enable = NSMenuItem(title: "Enable", action: #selector(enableServer(_:)), keyEquivalent: "")
                enable.target = self
                enable.representedObject = server
                menu.addItem(enable)
            }

            // Restart
            let restart = NSMenuItem(title: "Restart", action: #selector(restartServer(_:)), keyEquivalent: "")
            restart.target = self
            restart.representedObject = server
            menu.addItem(restart)

            // Log In (if auth needed)
            if server.health?.action == "login" {
                menu.addItem(.separator())
                let login = NSMenuItem(title: "Log In", action: #selector(loginServer(_:)), keyEquivalent: "")
                login.target = self
                login.representedObject = server
                login.image = NSImage(systemSymbolName: "person.badge.key", accessibilityDescription: "login")
                menu.addItem(login)
            }

            // Approve Tools (if quarantined)
            if server.pendingApprovalCount > 0 {
                menu.addItem(.separator())
                let approve = NSMenuItem(title: "Approve All Tools", action: #selector(approveTools(_:)), keyEquivalent: "")
                approve.target = self
                approve.representedObject = server
                approve.image = NSImage(systemSymbolName: "checkmark.shield", accessibilityDescription: "approve")
                menu.addItem(approve)
            }

            menu.addItem(.separator())

            // View Details
            let details = NSMenuItem(title: "View Details", action: #selector(viewDetails(_:)), keyEquivalent: "")
            details.target = self
            details.representedObject = server
            menu.addItem(details)

            // View Logs
            let logs = NSMenuItem(title: "View Logs", action: #selector(viewLogs(_:)), keyEquivalent: "")
            logs.target = self
            logs.representedObject = server
            menu.addItem(logs)
        }

        @objc private func enableServer(_ sender: NSMenuItem) {
            guard let server = sender.representedObject as? ServerStatus else { return }
            Task { try? await apiClient?.enableServer(server.id) }
        }

        @objc private func disableServer(_ sender: NSMenuItem) {
            guard let server = sender.representedObject as? ServerStatus else { return }
            Task { try? await apiClient?.disableServer(server.id) }
        }

        @objc private func restartServer(_ sender: NSMenuItem) {
            guard let server = sender.representedObject as? ServerStatus else { return }
            Task { try? await apiClient?.restartServer(server.id) }
        }

        @objc private func loginServer(_ sender: NSMenuItem) {
            guard let server = sender.representedObject as? ServerStatus else { return }
            Task { try? await apiClient?.loginServer(server.id) }
        }

        @objc private func approveTools(_ sender: NSMenuItem) {
            guard let server = sender.representedObject as? ServerStatus else { return }
            Task { try? await apiClient?.approveTools(server.id) }
        }

        @objc private func viewDetails(_ sender: NSMenuItem) {
            guard let server = sender.representedObject as? ServerStatus else { return }
            onDoubleClick?(server)
        }

        @objc private func viewLogs(_ sender: NSMenuItem) {
            guard let server = sender.representedObject as? ServerStatus else { return }
            let home = FileManager.default.homeDirectoryForCurrentUser
            let logFile = home.appendingPathComponent("Library/Logs/mcpproxy/server-\(server.name).log")
            if FileManager.default.fileExists(atPath: logFile.path) {
                NSWorkspace.shared.open(logFile)
            }
        }

        // MARK: - Data Source

        func numberOfRows(in tableView: NSTableView) -> Int {
            servers.count
        }

        func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
            guard row < servers.count else { return nil }
            let server = servers[row]

            let cellId = NSUserInterfaceItemIdentifier("ServerCell")
            let cell: NSTableCellView
            if let reused = tableView.makeView(withIdentifier: cellId, owner: nil) as? NSTableCellView {
                cell = reused
                // Clear old subviews
                cell.subviews.forEach { $0.removeFromSuperview() }
            } else {
                cell = NSTableCellView()
                cell.identifier = cellId
            }

            // Build the cell content
            let stack = NSStackView()
            stack.orientation = .horizontal
            stack.spacing = 10
            stack.alignment = .centerY
            stack.translatesAutoresizingMaskIntoConstraints = false

            // Health dot
            let dot = NSView()
            dot.wantsLayer = true
            dot.layer?.cornerRadius = 5
            dot.layer?.backgroundColor = healthColor(for: server).cgColor
            dot.translatesAutoresizingMaskIntoConstraints = false
            NSLayoutConstraint.activate([
                dot.widthAnchor.constraint(equalToConstant: 10),
                dot.heightAnchor.constraint(equalToConstant: 10)
            ])
            stack.addArrangedSubview(dot)

            // Name + status
            let nameStack = NSStackView()
            nameStack.orientation = .vertical
            nameStack.alignment = .leading
            nameStack.spacing = 2

            let nameLabel = NSTextField(labelWithString: server.name)
            nameLabel.font = .systemFont(ofSize: 13, weight: .semibold)
            nameStack.addArrangedSubview(nameLabel)

            let statusText = server.health?.summary ?? (server.connected ? "Connected" : server.enabled ? "Disconnected" : "Disabled")
            let toolsText = server.toolCount > 0 ? " (\(server.toolCount) tools)" : ""
            let statusLabel = NSTextField(labelWithString: "\(statusText)\(toolsText)")
            statusLabel.font = .systemFont(ofSize: 11)
            statusLabel.textColor = .secondaryLabelColor
            nameStack.addArrangedSubview(statusLabel)
            stack.addArrangedSubview(nameStack)

            // Spacer
            let spacer = NSView()
            spacer.setContentHuggingPriority(.defaultLow, for: .horizontal)
            stack.addArrangedSubview(spacer)

            // Quarantine badge
            if server.pendingApprovalCount > 0 {
                let qBadge = NSTextField(labelWithString: "\(server.pendingApprovalCount) pending")
                qBadge.font = .systemFont(ofSize: 10, weight: .medium)
                qBadge.textColor = .white
                qBadge.backgroundColor = .systemOrange
                qBadge.isBordered = false
                qBadge.drawsBackground = true
                qBadge.wantsLayer = true
                qBadge.layer?.cornerRadius = 3
                stack.addArrangedSubview(qBadge)
            }

            // Protocol badge
            let protoBadge = NSTextField(labelWithString: server.protocol)
            protoBadge.font = .systemFont(ofSize: 10)
            protoBadge.textColor = .secondaryLabelColor
            protoBadge.backgroundColor = NSColor.quaternaryLabelColor
            protoBadge.isBordered = false
            protoBadge.drawsBackground = true
            protoBadge.wantsLayer = true
            protoBadge.layer?.cornerRadius = 3
            stack.addArrangedSubview(protoBadge)

            // Tools count
            if server.toolCount > 0 {
                let toolsBadge = NSTextField(labelWithString: "\(server.toolCount) tools")
                toolsBadge.font = .systemFont(ofSize: 10)
                toolsBadge.textColor = .secondaryLabelColor
                toolsBadge.backgroundColor = NSColor.quaternaryLabelColor
                toolsBadge.isBordered = false
                toolsBadge.drawsBackground = true
                toolsBadge.wantsLayer = true
                toolsBadge.layer?.cornerRadius = 3
                stack.addArrangedSubview(toolsBadge)
            }

            // Info button for details
            let infoButton = NSButton(frame: NSRect(x: 0, y: 0, width: 24, height: 24))
            infoButton.bezelStyle = .circular
            infoButton.image = NSImage(systemSymbolName: "info.circle", accessibilityDescription: "Details")
            infoButton.isBordered = false
            infoButton.target = self
            infoButton.action = #selector(infoButtonClicked(_:))
            infoButton.tag = row
            stack.addArrangedSubview(infoButton)

            // Chevron indicator for double-click
            let chevron = NSTextField(labelWithString: "\u{203A}")
            chevron.font = .systemFont(ofSize: 16)
            chevron.textColor = .tertiaryLabelColor
            stack.addArrangedSubview(chevron)

            cell.addSubview(stack)
            NSLayoutConstraint.activate([
                stack.leadingAnchor.constraint(equalTo: cell.leadingAnchor, constant: 8),
                stack.trailingAnchor.constraint(equalTo: cell.trailingAnchor, constant: -8),
                stack.topAnchor.constraint(equalTo: cell.topAnchor, constant: 4),
                stack.bottomAnchor.constraint(equalTo: cell.bottomAnchor, constant: -4)
            ])

            cell.setAccessibilityIdentifier("server-row-\(server.id)")
            return cell
        }

        private func healthColor(for server: ServerStatus) -> NSColor {
            if server.quarantined { return .systemOrange }
            if !server.enabled { return .systemGray }
            if let health = server.health {
                switch health.level {
                case "healthy": return .systemGreen
                case "degraded": return .systemYellow
                case "unhealthy": return .systemRed
                default: break
                }
            }
            return server.connected ? .systemGreen : .systemGray
        }
    }
}
