// ServersView.swift
// MCPProxy
//
// Uses ScrollView + LazyVStack instead of List to avoid SwiftUI's
// List duplication bug with @ObservedObject / @Published arrays.

import SwiftUI

// MARK: - Servers View

struct ServersView: View {
    @ObservedObject var appState: AppState
    let apiClient: APIClient?

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
                // ScrollView + LazyVStack — no duplication bugs unlike List
                ScrollView {
                    LazyVStack(spacing: 0) {
                        ForEach(appState.servers) { server in
                            ServerRow(server: server, apiClient: apiClient)
                            Divider().padding(.leading, 34)
                        }
                    }
                    .padding(.horizontal)
                }
            }
        }
    }
}

// MARK: - Server Row

struct ServerRow: View {
    let server: ServerStatus
    let apiClient: APIClient?
    @State private var isPerformingAction = false

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
