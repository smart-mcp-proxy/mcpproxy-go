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

    var body: some View {
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

            Divider()

            // AppKit NSTableView — no duplication bugs
            ServerTableView(servers: $servers, apiClient: appState.apiClient)
                .accessibilityIdentifier("servers-list")
        }
        .onAppear {
            triggerLoad()
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

        scrollView.documentView = tableView
        context.coordinator.tableView = tableView
        return scrollView
    }

    func updateNSView(_ nsView: NSScrollView, context: Context) {
        context.coordinator.servers = servers
        context.coordinator.apiClient = apiClient
        context.coordinator.tableView?.reloadData()
    }

    func makeCoordinator() -> Coordinator {
        Coordinator()
    }

    class Coordinator: NSObject, NSTableViewDataSource, NSTableViewDelegate {
        var servers: [ServerStatus] = []
        var apiClient: APIClient?
        weak var tableView: NSTableView?

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
