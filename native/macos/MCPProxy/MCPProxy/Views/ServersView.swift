// ServersView.swift
// MCPProxy
//
// Uses ScrollView + LazyVStack instead of List to avoid SwiftUI's
// List duplication bug with @ObservedObject / @Published arrays.
//
// Reads apiClient from appState so the view never needs to be recreated
// when the client becomes available after core startup.

import SwiftUI

// MARK: - Servers View

struct ServersView: View {
    @ObservedObject var appState: AppState

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

                Button {
                    // Force refresh from API
                    Task {
                        if let client = appState.apiClient {
                            let servers = try? await client.servers()
                            if let servers {
                                await appState.updateServers(servers)
                            }
                        }
                    }
                } label: {
                    Image(systemName: "arrow.clockwise")
                }
                .buttonStyle(.borderless)
            }
            .padding()

            Divider()

            if appState.servers.isEmpty {
                VStack(spacing: 12) {
                    Image(systemName: "server.rack")
                        .font(.system(size: 48))
                        .foregroundStyle(.tertiary)
                    Text("No servers configured")
                        .font(.title3)
                        .foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                // Use List with .id() keyed on server count to force full rebuild.
                // ScrollView+LazyVStack had layout issues in NavigationSplitView detail.
                // The .id() prevents the List duplication bug by forcing a complete
                // teardown and rebuild when the server set changes.
                List {
                    ForEach(appState.servers) { server in
                        ServerRow(server: server, appState: appState)
                    }
                }
                .id("servers-\(appState.servers.count)-\(appState.servers.first?.id ?? "")")
            }
        }
    }
}

// MARK: - Server Row

struct ServerRow: View {
    let server: ServerStatus
    @ObservedObject var appState: AppState
    @State private var isPerformingAction = false

    private var apiClient: APIClient? { appState.apiClient }

    var body: some View {
        HStack(spacing: 12) {
            Circle()
                .fill(healthColor)
                .frame(width: 10, height: 10)

            VStack(alignment: .leading, spacing: 2) {
                Text(server.name)
                    .font(.headline)
                HStack(spacing: 4) {
                    Text(server.health?.summary ?? statusText)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    if server.toolCount > 0 {
                        Text("(\(server.toolCount) tools)")
                            .font(.caption)
                            .foregroundStyle(.tertiary)
                    }
                }
            }

            Spacer()

            Text(server.protocol)
                .font(.caption2)
                .padding(.horizontal, 6)
                .padding(.vertical, 2)
                .background(.quaternary)
                .cornerRadius(3)

            if server.toolCount > 0 {
                Text("\(server.toolCount) tools")
                    .font(.caption)
                    .padding(.horizontal, 8)
                    .padding(.vertical, 2)
                    .background(.quaternary)
                    .cornerRadius(4)
            }

            if isPerformingAction {
                ProgressView().controlSize(.small)
            } else if !server.enabled {
                Button("Enable") {
                    performAction { try await apiClient?.enableServer(server.id) }
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
            } else {
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
                    Divider()
                    Button("View Logs") {
                        let home = FileManager.default.homeDirectoryForCurrentUser
                        let logFile = home.appendingPathComponent("Library/Logs/mcpproxy/server-\(server.name).log")
                        if FileManager.default.fileExists(atPath: logFile.path) {
                            NSWorkspace.shared.open(logFile)
                        } else {
                            NSWorkspace.shared.open(home.appendingPathComponent("Library/Logs/mcpproxy"))
                        }
                    }
                } label: {
                    Image(systemName: "ellipsis.circle")
                }
                .menuStyle(.borderlessButton)
                .fixedSize()
            }
        }
        .padding(.vertical, 8)
    }

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
            try? await Task.sleep(nanoseconds: 300_000_000)
            await MainActor.run { isPerformingAction = false }
        }
    }
}
