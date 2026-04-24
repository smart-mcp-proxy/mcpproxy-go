// TrayMenu.swift
// MCPProxy
//
// The MenuBarExtra content view. Renders the full tray menu with server list,
// recent activity, quarantine alerts, and management actions.

import SwiftUI

struct TrayMenu: View {
    @ObservedObject var appState: AppState
    @ObservedObject var updateService: UpdateService
    let onRestart: () -> Void
    let onQuit: () -> Void

    @State private var apiClient: APIClient?

    var body: some View {
        // MARK: - Header
        headerSection

        Divider()

        // MARK: - Spec 044 Fix Issues (classified diagnostics)
        if !appState.serversWithDiagnostic.isEmpty {
            fixIssuesSection
            Divider()
        }

        // MARK: - Attention / Quarantine
        if !appState.serversNeedingAttention.isEmpty {
            attentionSection
            Divider()
        }

        if appState.quarantinedToolsCount > 0 {
            quarantineSection
            Divider()
        }

        // MARK: - Servers
        if !appState.servers.isEmpty {
            serversSection
            Divider()
        }

        // MARK: - Recent Activity
        if !appState.recentActivity.isEmpty {
            activitySection
            Divider()
        }

        // MARK: - Sensitive Data
        if appState.sensitiveDataAlertCount > 0 {
            sensitiveDataSection
            Divider()
        }

        // MARK: - Actions
        actionsSection

        Divider()

        // MARK: - Settings
        settingsSection

        Divider()

        // MARK: - Quit
        Button("Quit MCPProxy") {
            onQuit()
        }
        .keyboardShortcut("q")
    }

    // MARK: - Header Section

    @ViewBuilder
    private var headerSection: some View {
        if appState.version.isEmpty {
            Text("MCPProxy")
                .font(.headline)
        } else {
            Text("MCPProxy v\(appState.version)")
                .font(.headline)
        }

        Text(appState.statusSummary)
            .font(.subheadline)
            .foregroundStyle(.secondary)

        // Show error detail and retry button when in an error state
        if case .error(let coreError) = appState.coreState {
            Text(coreError.remediationHint)
                .font(.caption2)
                .foregroundStyle(.red)

            if coreError.isRetryable {
                Button("Retry") {
                    onRestart()
                }
            }
        }
    }

    // MARK: - Fix Issues Section (Spec 044)

    /// Renders the "Fix issues" group, one entry per server that has a
    /// classified diagnostic with warn/error severity. Clicking opens the
    /// server detail page in the web UI where ErrorPanel renders the
    /// full fix_steps list.
    @ViewBuilder
    private var fixIssuesSection: some View {
        let affected = appState.serversWithDiagnostic
        Text("⚠ Fix issues (\(affected.count))")
            .font(.caption)
            .foregroundStyle(.orange)

        ForEach(affected) { server in
            Button {
                openWebUI(path: "servers/\(server.name)")
            } label: {
                HStack {
                    Image(systemName: severityIcon(for: server.diagnostic?.severity))
                        .foregroundStyle(severityColor(for: server.diagnostic?.severity))
                    VStack(alignment: .leading) {
                        Text(server.name)
                        Text(server.diagnostic?.code ?? "")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                }
            }
        }
    }

    // MARK: - Attention Section

    @ViewBuilder
    private var attentionSection: some View {
        Text("Needs Attention")
            .font(.caption)
            .foregroundStyle(.secondary)

        ForEach(appState.serversNeedingAttention) { server in
            Button {
                handleServerAction(server)
            } label: {
                HStack {
                    Image(systemName: actionIcon(for: server.health?.action ?? ""))
                    VStack(alignment: .leading) {
                        Text(server.name)
                        Text(server.health?.summary ?? "")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
            }
        }
    }

    // MARK: - Quarantine Section

    @ViewBuilder
    private var quarantineSection: some View {
        let quarantinedServers = appState.servers.filter { $0.quarantined }
        Label(
            "\(appState.quarantinedToolsCount) quarantined server\(appState.quarantinedToolsCount == 1 ? "" : "s")",
            systemImage: "shield.lefthalf.filled"
        )
        .foregroundStyle(.orange)

        ForEach(quarantinedServers) { server in
            Button("Approve \(server.name)") {
                Task {
                    try? await apiClient?.unquarantineServer(server.id)
                }
            }
        }
    }

    // MARK: - Servers Section

    @ViewBuilder
    private var serversSection: some View {
        Text("Servers")
            .font(.caption)
            .foregroundStyle(.secondary)

        ForEach(appState.servers) { server in
            Menu {
                serverSubmenu(for: server)
            } label: {
                HStack {
                    Circle()
                        .fill(serverStatusColor(for: server))
                        .frame(width: 8, height: 8)
                    Text(server.name)
                    Spacer()
                    if server.toolCount > 0 {
                        Text("\(server.toolCount) tools")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
            }
        }
    }

    /// Submenu for individual server actions.
    @ViewBuilder
    private func serverSubmenu(for server: ServerStatus) -> some View {
        // Status info
        let summary = server.health?.summary ?? (server.connected ? "Connected" : "Disconnected")
        Text(summary)
            .font(.caption)

        if let detail = server.health?.detail, !detail.isEmpty {
            Text(detail)
                .font(.caption2)
                .foregroundStyle(.secondary)
        }

        Divider()

        // Enable / Disable (stdio servers use Stop/Start terminology)
        if server.enabled {
            Button(server.protocol == "stdio" ? "Stop" : "Disable") {
                Task {
                    try? await apiClient?.disableServer(server.id)
                }
            }
        } else {
            Button(server.protocol == "stdio" ? "Start" : "Enable") {
                Task {
                    try? await apiClient?.enableServer(server.id)
                }
            }
        }

        // Restart
        Button("Restart") {
            Task {
                try? await apiClient?.restartServer(server.id)
            }
        }

        // OAuth Login (shown when action is "login")
        if server.health?.action == "login" {
            Button("Log In") {
                Task {
                    try? await apiClient?.loginServer(server.id)
                }
            }
        }

        // View Logs
        Button("View Logs") {
            openLogsForServer(server.name)
        }
    }

    // MARK: - Activity Section

    @ViewBuilder
    private var activitySection: some View {
        Text("Recent Activity")
            .font(.caption)
            .foregroundStyle(.secondary)

        ForEach(appState.recentActivity.prefix(5)) { entry in
            HStack {
                Image(systemName: activityIcon(for: entry))
                VStack(alignment: .leading) {
                    Text(activitySummaryText(for: entry))
                        .font(.caption)
                        .lineLimit(1)
                    Text(relativeTime(entry.timestamp))
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
            }
        }
    }

    // MARK: - Sensitive Data Section

    @ViewBuilder
    private var sensitiveDataSection: some View {
        Label(
            "\(appState.sensitiveDataAlertCount) sensitive data detection\(appState.sensitiveDataAlertCount == 1 ? "" : "s")",
            systemImage: "exclamationmark.triangle.fill"
        )
        .foregroundStyle(.red)

        Button("View in Web UI") {
            openWebUI(path: "activity?sensitive=true")
        }
    }

    // MARK: - Actions Section

    @ViewBuilder
    private var actionsSection: some View {
        Button("Open Web UI") {
            openWebUI()
        }

        Button("Open Config File") {
            openConfigFile()
        }

        Button("Open Logs Directory") {
            openLogsDirectory()
        }
    }

    // MARK: - Settings Section

    @ViewBuilder
    private var settingsSection: some View {
        Toggle("Run at Startup", isOn: Binding(
            get: { appState.autoStartEnabled },
            set: { newValue in
                do {
                    if newValue {
                        try AutoStartService.enable()
                    } else {
                        try AutoStartService.disable()
                    }
                    appState.autoStartEnabled = newValue
                } catch {
                    // Revert on failure; the toggle will snap back
                    appState.autoStartEnabled = !newValue
                }
            }
        ))

        Button("Check for Updates") {
            updateService.checkForUpdates()
        }
        .disabled(!updateService.canCheckForUpdates)

        if let available = appState.updateAvailable ?? updateService.latestVersion {
            Text("Update available: v\(available)")
                .font(.caption)
                .foregroundStyle(.blue)
        }
    }

    // MARK: - Helpers

    private func serverStatusColor(for server: ServerStatus) -> Color {
        if server.quarantined {
            return .orange
        }
        if let health = server.health {
            switch health.level {
            case "healthy":
                return .green
            case "degraded":
                return .yellow
            case "unhealthy":
                return .red
            default:
                return server.connected ? .green : .red
            }
        }
        return server.connected ? .green : .red
    }

    /// Spec 044 — map a diagnostic severity string to an SF Symbol name.
    private func severityIcon(for severity: String?) -> String {
        switch severity {
        case "error": return "xmark.octagon.fill"
        case "warn":  return "exclamationmark.triangle.fill"
        default:      return "info.circle"
        }
    }

    /// Spec 044 — map a diagnostic severity string to a colour.
    private func severityColor(for severity: String?) -> Color {
        switch severity {
        case "error": return .red
        case "warn":  return .orange
        default:      return .blue
        }
    }

    private func actionIcon(for action: String) -> String {
        switch action {
        case "login":
            return "person.badge.key"
        case "restart":
            return "arrow.clockwise"
        case "enable":
            return "power"
        case "approve":
            return "checkmark.shield"
        case "set_secret", "configure":
            return "gearshape"
        case "view_logs":
            return "doc.text"
        default:
            return "exclamationmark.circle"
        }
    }

    private func activityIcon(for entry: ActivityEntry) -> String {
        if entry.hasSensitiveData == true {
            return "exclamationmark.triangle.fill"
        }
        switch entry.status {
        case "error":
            return "xmark.circle"
        default:
            return "checkmark.circle"
        }
    }

    /// Build a one-line summary for an activity entry.
    private func activitySummaryText(for entry: ActivityEntry) -> String {
        var parts: [String] = []
        if let serverName = entry.serverName, !serverName.isEmpty {
            parts.append(serverName)
        }
        if let toolName = entry.toolName, !toolName.isEmpty {
            parts.append(toolName)
        }
        if parts.isEmpty {
            parts.append(entry.type)
        }
        return parts.joined(separator: ":")
    }

    /// Parse the ISO 8601 timestamp string and format as relative time.
    private func relativeTime(_ timestamp: String) -> String {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let date = formatter.date(from: timestamp) {
            let relative = RelativeDateTimeFormatter()
            relative.unitsStyle = .abbreviated
            return relative.localizedString(for: date, relativeTo: Date())
        }
        // Fallback: try without fractional seconds
        formatter.formatOptions = [.withInternetDateTime]
        if let date = formatter.date(from: timestamp) {
            let relative = RelativeDateTimeFormatter()
            relative.unitsStyle = .abbreviated
            return relative.localizedString(for: date, relativeTo: Date())
        }
        return timestamp
    }

    private func handleServerAction(_ server: ServerStatus) {
        guard let action = server.health?.action else { return }
        switch action {
        case "login":
            Task { try? await apiClient?.loginServer(server.id) }
        case "restart":
            Task { try? await apiClient?.restartServer(server.id) }
        case "enable":
            Task { try? await apiClient?.enableServer(server.id) }
        case "approve":
            openWebUI(path: "servers/\(server.name)")
        case "view_logs":
            openLogsForServer(server.name)
        default:
            openWebUI(path: "servers/\(server.name)")
        }
    }

    private func openWebUI(path: String = "") {
        let baseURLString = appState.webUIBaseURL
        if let url = URL(string: "\(baseURLString)/ui/\(path)") {
            NSWorkspace.shared.open(url)
        }
    }

    private func openConfigFile() {
        let homeDir = FileManager.default.homeDirectoryForCurrentUser
        let configPath = homeDir.appendingPathComponent(".mcpproxy/mcp_config.json")
        NSWorkspace.shared.open(configPath)
    }

    private func openLogsDirectory() {
        let homeDir = FileManager.default.homeDirectoryForCurrentUser
        let logsPath = homeDir.appendingPathComponent("Library/Logs/mcpproxy")
        NSWorkspace.shared.open(logsPath)
    }

    private func openLogsForServer(_ serverName: String) {
        let homeDir = FileManager.default.homeDirectoryForCurrentUser
        let logFile = homeDir.appendingPathComponent("Library/Logs/mcpproxy/\(serverName).log")
        if FileManager.default.fileExists(atPath: logFile.path) {
            NSWorkspace.shared.open(logFile)
        } else {
            openLogsDirectory()
        }
    }
}
