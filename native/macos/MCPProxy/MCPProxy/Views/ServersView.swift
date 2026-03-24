// ServersView.swift
// MCPProxy
//
// Displays all upstream MCP servers with their health status, tool counts,
// and action buttons for enable/disable/restart/login operations.

import SwiftUI

// MARK: - Servers View

struct ServersView: View {
    @ObservedObject var appState: AppState
    let apiClient: APIClient?

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            // Header with aggregate stats
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
            }
            .padding()

            Divider()

            if appState.servers.isEmpty {
                emptyState
            } else {
                List(appState.servers) { server in
                    ServerRow(server: server, apiClient: apiClient)
                }
            }
        }
    }

    @ViewBuilder
    private var emptyState: some View {
        VStack(spacing: 12) {
            Image(systemName: "server.rack")
                .font(.system(size: 48))
                .foregroundStyle(.tertiary)
            Text("No servers configured")
                .font(.title3)
                .foregroundStyle(.secondary)
            Text("Add servers in ~/.mcpproxy/mcp_config.json")
                .font(.caption)
                .foregroundStyle(.tertiary)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

// MARK: - Server Row

struct ServerRow: View {
    let server: ServerStatus
    let apiClient: APIClient?
    @State private var isPerformingAction = false

    var body: some View {
        HStack(spacing: 12) {
            // Health indicator dot
            Circle()
                .fill(healthColor)
                .frame(width: 10, height: 10)

            // Server name and status summary
            VStack(alignment: .leading, spacing: 2) {
                Text(server.name)
                    .font(.headline)
                Text(server.health?.summary ?? statusText)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Spacer()

            // Tool count badge
            if server.toolCount > 0 {
                Text("\(server.toolCount) tools")
                    .font(.caption)
                    .padding(.horizontal, 8)
                    .padding(.vertical, 2)
                    .background(.quaternary)
                    .cornerRadius(4)
            }

            // Quarantine badge
            if server.quarantined {
                Label("Quarantined", systemImage: "shield.lefthalf.filled")
                    .font(.caption)
                    .foregroundStyle(.orange)
            }

            // Pending approval badge
            if server.pendingApprovalCount > 0 {
                Text("\(server.pendingApprovalCount) pending")
                    .font(.caption)
                    .foregroundStyle(.orange)
            }

            // Action controls
            if isPerformingAction {
                ProgressView()
                    .controlSize(.small)
            } else if !server.enabled {
                Button("Enable") {
                    performAction { try await apiClient?.enableServer(server.id) }
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
            } else {
                serverActionsMenu
            }
        }
        .padding(.vertical, 4)
    }

    // MARK: - Actions Menu

    @ViewBuilder
    private var serverActionsMenu: some View {
        Menu {
            Button("Restart") {
                performAction { try await apiClient?.restartServer(server.id) }
            }
            Button("Disable") {
                performAction { try await apiClient?.disableServer(server.id) }
            }

            if server.health?.action == "login" {
                Divider()
                Button("Log In") {
                    performAction { try await apiClient?.loginServer(server.id) }
                }
            }

            if server.quarantined {
                Divider()
                Button("Unquarantine") {
                    performAction { try await apiClient?.unquarantineServer(server.id) }
                }
            }

            if server.pendingApprovalCount > 0 {
                Divider()
                Button("Approve Tools") {
                    performAction { try await apiClient?.approveTools(server.id) }
                }
            }

            Divider()
            Button("View Logs") {
                openLogsForServer(server.name)
            }
        } label: {
            Image(systemName: "ellipsis.circle")
        }
        .menuStyle(.borderlessButton)
        .fixedSize()
    }

    // MARK: - Helpers

    private var healthColor: Color {
        if server.quarantined { return .orange }
        if !server.enabled { return .gray }
        if let health = server.health {
            switch health.level {
            case "healthy": return .green
            case "degraded": return .yellow
            case "unhealthy": return .red
            default: break
            }
        }
        return server.connected ? .green : .gray
    }

    private var statusText: String {
        if !server.enabled { return "Disabled" }
        if server.quarantined { return "Quarantined" }
        if server.connecting == true { return "Connecting..." }
        return server.connected ? "Connected" : "Disconnected"
    }

    private func performAction(_ action: @escaping () async throws -> Void) {
        isPerformingAction = true
        Task {
            try? await action()
            // Brief delay so the spinner is visible
            try? await Task.sleep(nanoseconds: 300_000_000)
            await MainActor.run { isPerformingAction = false }
        }
    }

    private func openLogsForServer(_ name: String) {
        let home = FileManager.default.homeDirectoryForCurrentUser
        let logFile = home.appendingPathComponent("Library/Logs/mcpproxy/server-\(name).log")
        if FileManager.default.fileExists(atPath: logFile.path) {
            NSWorkspace.shared.open(logFile)
        } else {
            let logsDir = home.appendingPathComponent("Library/Logs/mcpproxy")
            NSWorkspace.shared.open(logsDir)
        }
    }
}
