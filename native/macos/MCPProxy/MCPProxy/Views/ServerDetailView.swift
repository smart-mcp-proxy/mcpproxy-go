// ServerDetailView.swift
// MCPProxy
//
// Shows server detail with three tabs: Tools, Logs, Config.
// Opened by double-clicking a server row in ServersView.

import SwiftUI

// MARK: - Tab Enum

enum ServerDetailTab: String, CaseIterable {
    case tools = "Tools"
    case logs = "Logs"
    case config = "Config"

    var icon: String {
        switch self {
        case .tools: return "wrench.and.screwdriver"
        case .logs: return "doc.text"
        case .config: return "gearshape"
        }
    }
}

// MARK: - Server Detail View

struct ServerDetailView: View {
    let initialServer: ServerStatus
    @ObservedObject var appState: AppState
    let onDismiss: () -> Void
    @Environment(\.fontScale) var fontScale

    @State private var server: ServerStatus
    @State private var selectedTab: ServerDetailTab = .tools
    @State private var tools: [ServerTool] = []
    @State private var logLines: [String] = []
    @State private var isLoadingTools = false
    @State private var isLoadingLogs = false
    @State private var isApproving = false
    @State private var actionMessage: String?

    init(server: ServerStatus, appState: AppState, onDismiss: @escaping () -> Void) {
        self.initialServer = server
        self.appState = appState
        self.onDismiss = onDismiss
        self._server = State(initialValue: server)
    }

    // Edit mode state for Config tab
    @State private var isEditing = false
    @State private var editURL = ""
    @State private var editCommand = ""
    @State private var editArgs = ""
    @State private var editWorkingDir = ""
    @State private var editEnvVars = ""
    @State private var editEnabled = true
    @State private var editQuarantined = false
    @State private var editDockerIsolation = false
    @State private var editSkipQuarantine = false
    @State private var isSavingEdit = false
    @State private var editError: String?

    // Logs auto-refresh timer
    @State private var logRefreshTimer: Timer?

    private var apiClient: APIClient? { appState.apiClient }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            serverHeader
            Divider()
            tabBar
            Divider()

            if let msg = actionMessage {
                actionBanner(msg)
            }

            switch selectedTab {
            case .tools: toolsTab
            case .logs: logsTab
            case .config: configTab
            }
        }
    }

    // MARK: - Header

    @ViewBuilder
    private var serverHeader: some View {
        HStack(spacing: 12) {
            Button {
                onDismiss()
            } label: {
                Image(systemName: "chevron.left")
                    .font(.title3)
            }
            .buttonStyle(.borderless)
            .help("Back to server list")

            // Health dot
            Circle()
                .fill(server.statusColor)
                .frame(width: 12, height: 12)
                .accessibilityLabel("Server health: \(server.health?.level ?? "unknown")")

            VStack(alignment: .leading, spacing: 2) {
                Text(server.name)
                    .font(.scaled(.title2, scale: fontScale).bold())
                Text(server.health?.summary ?? statusText)
                    .font(.scaled(.subheadline, scale: fontScale))
                    .foregroundStyle(.secondary)
            }

            Spacer()

            // Action buttons
            if server.health?.action == "login" {
                Button("Log In") {
                    Task { await performAction { try await apiClient?.loginServer(server.id) }
                        actionMessage = "Login initiated for \(server.name)"
                    }
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
            }

            if server.quarantined {
                Button {
                    Task {
                        do {
                            try await apiClient?.approveTools(server.id)
                            try await apiClient?.unquarantineServer(server.id)
                            actionMessage = "Server approved and activated"
                            await refreshServer()
                        } catch {
                            actionMessage = "Failed to approve: \(error.localizedDescription)"
                        }
                    }
                } label: {
                    Label("Approve Server", systemImage: "checkmark.shield")
                }
                .buttonStyle(.borderedProminent)
                .tint(.green)
                .controlSize(.small)
            } else {
                // Allow re-quarantining an approved server
                Button {
                    Task {
                        do {
                            try await apiClient?.postAction(path: "/api/v1/servers/\(server.id)/quarantine")
                            actionMessage = "Server quarantined"
                            await refreshServer()
                        } catch {
                            actionMessage = "Failed to quarantine: \(error.localizedDescription)"
                        }
                    }
                } label: {
                    Label("Quarantine", systemImage: "shield.lefthalf.filled")
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
            }

            if server.enabled {
                let disableLabel = server.protocol == "stdio" ? "Stop" : "Disable"
                let disabledMsg = server.protocol == "stdio" ? "stopped" : "disabled"
                Button(disableLabel) {
                    Task {
                        await performAction { try await apiClient?.disableServer(server.id) }
                        actionMessage = "\(server.name) \(disabledMsg)"
                        await refreshServer()
                    }
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
            } else {
                let enableLabel = server.protocol == "stdio" ? "Start" : "Enable"
                let enabledMsg = server.protocol == "stdio" ? "started" : "enabled"
                Button(enableLabel) {
                    Task { await performAction { try await apiClient?.enableServer(server.id) }
                        actionMessage = "\(server.name) \(enabledMsg)"
                    }
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
            }

            Button {
                Task { await performAction { try await apiClient?.restartServer(server.id) }
                    actionMessage = "\(server.name) restarting..."
                }
            } label: {
                Image(systemName: "arrow.clockwise")
            }
            .buttonStyle(.bordered)
            .controlSize(.small)
            .help("Restart server")
        }
        .padding()
    }

    // MARK: - Tab Bar

    @ViewBuilder
    private var tabBar: some View {
        HStack(spacing: 0) {
            ForEach(ServerDetailTab.allCases, id: \.self) { tab in
                Button {
                    selectedTab = tab
                } label: {
                    HStack(spacing: 4) {
                        Image(systemName: tab.icon)
                        Text(tab.rawValue)
                        if tab == .tools && pendingApprovalCount > 0 {
                            Text("\(pendingApprovalCount)")
                                .font(.scaled(.caption2, scale: fontScale).bold())
                                .foregroundStyle(.white)
                                .padding(.horizontal, 5)
                                .padding(.vertical, 1)
                                .background(.orange)
                                .clipShape(Capsule())
                        }
                    }
                    .frame(minWidth: 80)
                    .padding(.horizontal, 16)
                    .padding(.vertical, 8)
                    .background(
                        selectedTab == tab
                            ? Color.accentColor.opacity(0.15)
                            : Color.clear
                    )
                    .overlay(
                        RoundedRectangle(cornerRadius: 8)
                            .stroke(selectedTab == tab ? Color.accentColor.opacity(0.3) : Color.clear, lineWidth: 1)
                    )
                    .cornerRadius(8)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
            }
            Spacer()
        }
        .padding(.horizontal)
        .padding(.vertical, 4)
    }

    // MARK: - Tools Tab

    @ViewBuilder
    private var toolsTab: some View {
        VStack(alignment: .leading, spacing: 0) {
            // Quarantine approval banner
            if pendingApprovalCount > 0 {
                quarantineBanner
            }

            if isLoadingTools {
                ProgressView("Loading tools...")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if tools.isEmpty {
                VStack(spacing: 12) {
                    Image(systemName: "wrench.and.screwdriver")
                        .font(.system(size: 40 * fontScale))
                        .foregroundStyle(.tertiary)
                    Text("No tools available")
                        .font(.scaled(.title3, scale: fontScale))
                        .foregroundStyle(.secondary)
                    Text(server.connected ? "This server has no tools" : "Connect the server to see tools")
                        .font(.scaled(.caption, scale: fontScale))
                        .foregroundStyle(.tertiary)
                    Button("Reload") {
                        Task { await loadTools() }
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ScrollView {
                    VStack(alignment: .leading, spacing: 1) {
                        ForEach(tools) { tool in
                            ToolRow(tool: tool, serverName: server.name, apiClient: apiClient)
                        }
                    }
                    .padding()
                }
            }
        }
        .task { await loadTools() }
    }

    @ViewBuilder
    private var quarantineBanner: some View {
        HStack {
            Image(systemName: "shield.lefthalf.filled")
                .foregroundStyle(.orange)
            Text("\(pendingApprovalCount) tool(s) need approval")
                .font(.scaled(.subheadline, scale: fontScale).bold())
            Spacer()
            if isApproving {
                ProgressView()
                    .controlSize(.small)
            } else {
                Button("Approve All") {
                    Task {
                        isApproving = true
                        defer { isApproving = false }
                        do {
                            try await apiClient?.approveTools(server.id)
                            actionMessage = "All tools approved for \(server.name)"
                            await loadTools()
                        } catch {
                            actionMessage = "Failed to approve: \(error.localizedDescription)"
                        }
                    }
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
                .tint(.orange)
            }
        }
        .padding()
        .background(Color.orange.opacity(0.1))
    }

    // MARK: - Logs Tab

    @ViewBuilder
    private var logsTab: some View {
        VStack(alignment: .leading, spacing: 0) {
            lastErrorBanner
            HStack {
                Text("Server Logs")
                    .font(.scaled(.subheadline, scale: fontScale).bold())
                Text("\(logLines.count) lines")
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.secondary)
                Spacer()
                if isLoadingLogs {
                    ProgressView().controlSize(.small)
                }
                Button {
                    Task { await loadLogs() }
                } label: {
                    Image(systemName: "arrow.clockwise")
                }
                .buttonStyle(.borderless)
                .help("Refresh logs")

                Button("Open Log File") {
                    let home = FileManager.default.homeDirectoryForCurrentUser
                    let logFile = home.appendingPathComponent("Library/Logs/mcpproxy/server-\(server.name).log")
                    if FileManager.default.fileExists(atPath: logFile.path) {
                        NSWorkspace.shared.open(logFile)
                    }
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
            }
            .padding()

            Divider()

            if logLines.isEmpty && !isLoadingLogs {
                VStack(spacing: 12) {
                    Image(systemName: "doc.text")
                        .font(.system(size: 40 * fontScale))
                        .foregroundStyle(.tertiary)
                    Text("No log entries")
                        .font(.scaled(.title3, scale: fontScale))
                        .foregroundStyle(.secondary)
                    Text("Logs will appear when the server produces output")
                        .font(.scaled(.caption, scale: fontScale))
                        .foregroundStyle(.tertiary)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ScrollViewReader { proxy in
                    ScrollView(.vertical) {
                        VStack(alignment: .leading, spacing: 0) {
                            ForEach(Array(logLines.enumerated()), id: \.offset) { idx, line in
                                logLineView(line)
                                    .fixedSize(horizontal: false, vertical: true)
                                    .id(idx)
                            }
                        }
                        .padding(8)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .background(Color(nsColor: .textBackgroundColor))
                    .onChange(of: logLines.count) { _ in
                        // Auto-scroll to bottom on new lines
                        if let last = logLines.indices.last {
                            proxy.scrollTo(last, anchor: .bottom)
                        }
                    }
                }
            }
        }
        .task { await loadLogs() }
        .onAppear { startLogRefresh() }
        .onDisappear { stopLogRefresh() }
    }

    /// Banner showing last error at top of Logs tab.
    @ViewBuilder
    private var lastErrorBanner: some View {
        if let lastError = server.lastError, !lastError.isEmpty {
            HStack(spacing: 8) {
                Image(systemName: "exclamationmark.triangle.fill")
                    .foregroundStyle(.red)
                VStack(alignment: .leading, spacing: 2) {
                    Text("Last Error")
                        .font(.scaled(.caption, scale: fontScale).bold())
                        .foregroundStyle(.red)
                    Text(lastError)
                        .font(.scaledMonospaced(.caption, scale: fontScale))
                        .foregroundStyle(.red.opacity(0.8))
                        .textSelection(.enabled)
                }
                Spacer()
            }
            .padding(8)
            .background(Color.red.opacity(0.1))
            .cornerRadius(6)
            .padding(.horizontal)
            .padding(.top, 4)
        }
    }

    /// Render a single log line with color-coded level and word wrapping.
    @ViewBuilder
    private func logLineView(_ line: String) -> some View {
        let levelColor = logLevelColor(line)
        Text(line)
            .font(.scaledMonospaced(.caption, scale: fontScale))
            .foregroundStyle(levelColor)
            .textSelection(.enabled)
            .lineLimit(nil)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.vertical, 1)
    }

    private func logLevelColor(_ line: String) -> Color {
        if line.contains("[ERROR]") || line.contains("| ERROR |") {
            return .red
        } else if line.contains("[WARN]") || line.contains("| WARN |") {
            return .orange
        } else if line.contains("[DEBUG]") || line.contains("| DEBUG |") {
            return .gray
        }
        return .primary
    }

    // MARK: - Config Tab

    @ViewBuilder
    private var configTab: some View {
        VStack(spacing: 0) {
            // Edit/Save/Cancel toolbar
            HStack {
                Spacer()
                if isEditing {
                    if isSavingEdit {
                        ProgressView().controlSize(.small)
                        Text("Saving...")
                            .font(.scaled(.caption, scale: fontScale))
                    }
                    Button("Cancel") {
                        isEditing = false
                        editError = nil
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                    Button("Save") {
                        Task { await saveEdits() }
                    }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.small)
                    .disabled(isSavingEdit)
                } else {
                    Button {
                        startEditing()
                    } label: {
                        Label("Edit", systemImage: "pencil")
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                }
            }
            .padding(.horizontal)
            .padding(.vertical, 8)

            if let err = editError {
                HStack {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundStyle(.red)
                    Text(err)
                        .font(.scaled(.caption, scale: fontScale))
                        .foregroundStyle(.red)
                    Spacer()
                    Button("Dismiss") { editError = nil }
                        .buttonStyle(.borderless)
                        .font(.scaled(.caption, scale: fontScale))
                }
                .padding(.horizontal)
                .padding(.vertical, 4)
                .background(Color.red.opacity(0.1))
            }

            Divider()

            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    configSection(title: "General") {
                        configRow(label: "Name", value: server.name)
                        configRow(label: "Protocol", value: server.protocol)
                        if isEditing {
                            configToggleRow(label: "Enabled", isOn: $editEnabled,
                                hint: "When disabled, the server will not connect or be available to AI agents.")
                            configToggleRow(label: "Quarantined", isOn: $editQuarantined,
                                hint: "New tools must be reviewed and approved before AI agents can use them. Protects against tool poisoning attacks.")
                            configToggleRow(label: "Docker Isolation", isOn: $editDockerIsolation,
                                hint: "Runs the server in an isolated Docker container. Prevents access to your filesystem, network, and other system resources. Recommended for untrusted servers.")
                            configToggleRow(label: "Skip Quarantine", isOn: $editSkipQuarantine)
                        } else {
                            configRow(label: "Enabled", value: server.enabled ? "Yes" : "No")
                            if server.quarantined {
                                configRow(label: "Quarantined", value: "Yes")
                            }
                        }
                    }

                    if server.protocol == "http" || server.protocol == "sse" || server.protocol == "streamable-http" {
                        configSection(title: "Connection") {
                            if isEditing {
                                configEditRow(label: "URL", text: $editURL, placeholder: "https://api.example.com/mcp")
                            } else {
                                configRow(label: "URL", value: server.url ?? "N/A")
                            }
                        }
                    }

                    if server.protocol == "stdio" {
                        configSection(title: "Process") {
                            if isEditing {
                                configEditRow(label: "Command", text: $editCommand, placeholder: "e.g. npx, uvx")
                                configEditRow(label: "Args (one per line)", text: $editArgs, placeholder: "arg1\narg2", multiline: true)
                                configEditRow(label: "Working Dir", text: $editWorkingDir, placeholder: "/path/to/project")
                            } else {
                                configRow(label: "Command", value: server.command ?? server.name)
                                if let args = server.args, !args.isEmpty {
                                    configRow(label: "Args", value: args.joined(separator: " "))
                                }
                                if let wd = server.workingDir, !wd.isEmpty {
                                    configRow(label: "Working Dir", value: wd)
                                }
                            }
                        }
                    }

                    if isEditing {
                        configSection(title: "Environment Variables") {
                            configEditRow(label: "KEY=VALUE per line", text: $editEnvVars, placeholder: "API_KEY=abc123\nDEBUG=true", multiline: true)
                        }
                    }

                    configSection(title: "Status") {
                        configRow(label: "Connected", value: server.connected ? "Yes" : "No")
                        if let connectedAt = server.connectedAt {
                            configRow(label: "Connected At", value: connectedAt)
                        }
                        if let reconnectCount = server.reconnectCount, reconnectCount > 0 {
                            configRow(label: "Reconnect Count", value: "\(reconnectCount)")
                        }
                        configRow(label: "Tool Count", value: "\(server.toolCount)")
                        if let tokenSize = server.toolListTokenSize {
                            configRow(label: "Token Size", value: "\(tokenSize)")
                        }
                        if let lastError = server.lastError {
                            configRow(label: "Last Error", value: lastError)
                        }
                    }

                    if let health = server.health {
                        configSection(title: "Health") {
                            configRow(label: "Level", value: health.level)
                            configRow(label: "Admin State", value: health.adminState)
                            configRow(label: "Summary", value: health.summary)
                            if let detail = health.detail, !detail.isEmpty {
                                configRow(label: "Detail", value: detail)
                            }
                            if let action = health.action, !action.isEmpty {
                                configRow(label: "Action", value: action)
                            }
                        }
                    }
                }
                .padding()
            }
        }
    }

    @ViewBuilder
    private func configSection(title: String, @ViewBuilder content: () -> some View) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(title)
                .font(.scaled(.headline, scale: fontScale))
            content()
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(nsColor: .controlBackgroundColor))
        .cornerRadius(8)
    }

    @ViewBuilder
    private func configRow(label: String, value: String) -> some View {
        HStack(alignment: .top) {
            Text(label)
                .font(.scaled(.subheadline, scale: fontScale))
                .foregroundStyle(.secondary)
                .frame(width: 140, alignment: .trailing)
            Text(value)
                .font(.scaledMonospaced(.subheadline, scale: fontScale))
                .textSelection(.enabled)
            Spacer()
        }
    }

    @ViewBuilder
    private func configEditRow(label: String, text: Binding<String>, placeholder: String, multiline: Bool = false) -> some View {
        HStack(alignment: .top) {
            Text(label)
                .font(.scaled(.subheadline, scale: fontScale))
                .foregroundStyle(.secondary)
                .frame(width: 140, alignment: .trailing)
            if multiline {
                TextEditor(text: text)
                    .font(.scaledMonospaced(.subheadline, scale: fontScale))
                    .frame(height: 60)
                    .border(Color(nsColor: .separatorColor), width: 1)
            } else {
                TextField(placeholder, text: text)
                    .font(.scaledMonospaced(.subheadline, scale: fontScale))
                    .textFieldStyle(.roundedBorder)
            }
            Spacer()
        }
    }

    @ViewBuilder
    private func configToggleRow(label: String, isOn: Binding<Bool>, hint: String? = nil) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack {
                Text(label)
                    .font(.scaled(.subheadline, scale: fontScale))
                    .foregroundStyle(.secondary)
                    .frame(width: 140, alignment: .trailing)
                Toggle("", isOn: isOn)
                    .labelsHidden()
                Spacer()
            }
            if let hint = hint {
                Text(hint)
                    .font(.scaled(.caption2, scale: fontScale))
                    .foregroundStyle(.tertiary)
                    .padding(.leading, 148)
            }
        }
    }

    // MARK: - Action Banner

    @ViewBuilder
    private func actionBanner(_ message: String) -> some View {
        HStack {
            Text(message)
                .font(.scaled(.caption, scale: fontScale))
                .foregroundStyle(.secondary)
            Spacer()
            Button("Dismiss") { actionMessage = nil }
                .buttonStyle(.borderless)
                .font(.scaled(.caption, scale: fontScale))
        }
        .padding(.horizontal)
        .padding(.vertical, 6)
        .background(Color.accentColor.opacity(0.1))
    }

    // MARK: - Computed

    private var statusText: String {
        if !server.enabled { return "Disabled" }
        return server.connected ? "Connected" : "Disconnected"
    }

    private var pendingApprovalCount: Int {
        let fromModel = server.pendingApprovalCount
        if fromModel > 0 { return fromModel }
        return tools.filter { $0.approvalStatus == "pending" || $0.approvalStatus == "changed" }.count
    }

    // MARK: - Data Loading

    private func loadTools() async {
        guard let client = apiClient else {
            NSLog("[ServerDetail] loadTools: no apiClient")
            return
        }
        isLoadingTools = true
        defer { isLoadingTools = false }
        do {
            tools = try await client.serverTools(server.name)
            NSLog("[ServerDetail] loadTools: loaded %d tools for %@", tools.count, server.name)
        } catch {
            NSLog("[ServerDetail] loadTools FAILED for %@: %@", server.name, error.localizedDescription)
        }
    }

    private func loadLogs() async {
        guard let client = apiClient else {
            // Fall back to reading the log file directly
            loadLogsFromFile()
            return
        }
        isLoadingLogs = true
        defer { isLoadingLogs = false }
        do {
            logLines = try await client.serverLogs(server.name, tail: 100)
        } catch {
            // Fall back to reading the log file directly
            loadLogsFromFile()
        }
    }

    private func loadLogsFromFile() {
        let home = FileManager.default.homeDirectoryForCurrentUser
        let logFile = home.appendingPathComponent("Library/Logs/mcpproxy/server-\(server.name).log")
        guard let data = try? Data(contentsOf: logFile),
              let text = String(data: data, encoding: .utf8) else {
            logLines = []
            return
        }
        let allLines = text.components(separatedBy: "\n")
        logLines = Array(allLines.suffix(100))
    }

    private func performAction(_ action: () async throws -> Void) async {
        do {
            try await action()
        } catch {
            actionMessage = "Error: \(error.localizedDescription)"
        }
    }

    /// Refresh server status from API to update the view after mutations.
    private func refreshServer() async {
        guard let client = apiClient else { return }
        do {
            let servers = try await client.servers()
            if let updated = servers.first(where: { $0.name == server.name }) {
                server = updated
            }
        } catch {
            // Silently fail — view keeps showing stale data
        }
    }

    // MARK: - Edit Mode

    private func startEditing() {
        editURL = server.url ?? ""
        editCommand = server.command ?? ""
        editArgs = (server.args ?? []).joined(separator: "\n")
        editWorkingDir = server.workingDir ?? ""
        editEnvVars = "" // env vars not in ServerStatus model, would need config API
        editEnabled = server.enabled
        editQuarantined = server.quarantined
        editDockerIsolation = false // read from config if available
        editSkipQuarantine = false  // read from config if available
        editError = nil
        isEditing = true
    }

    private func saveEdits() async {
        guard let client = apiClient else { return }
        isSavingEdit = true
        editError = nil

        var updates: [String: Any] = [:]

        // String fields — only include if changed
        if server.protocol == "stdio" {
            let cmd = editCommand.trimmingCharacters(in: .whitespaces)
            if cmd.isEmpty {
                editError = "Command is required for stdio servers"
                isSavingEdit = false
                return
            }
            if cmd != (server.command ?? "") { updates["command"] = cmd }
            let args = editArgs.components(separatedBy: "\n").map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
            updates["args"] = args
        } else {
            let url = editURL.trimmingCharacters(in: .whitespaces)
            if url.isEmpty {
                editError = "URL is required for HTTP servers"
                isSavingEdit = false
                return
            }
            if url != (server.url ?? "") { updates["url"] = url }
        }

        let wd = editWorkingDir.trimmingCharacters(in: .whitespaces)
        if !wd.isEmpty { updates["working_dir"] = wd }

        // Parse env vars
        if !editEnvVars.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            var env: [String: String] = [:]
            for line in editEnvVars.components(separatedBy: "\n") {
                let trimmed = line.trimmingCharacters(in: .whitespaces)
                if trimmed.isEmpty { continue }
                let parts = trimmed.components(separatedBy: "=")
                if parts.count >= 2 {
                    env[parts[0].trimmingCharacters(in: .whitespaces)] = parts.dropFirst().joined(separator: "=")
                }
            }
            if !env.isEmpty { updates["env"] = env }
        }

        // Boolean toggles
        if editEnabled != server.enabled { updates["enabled"] = editEnabled }
        if editQuarantined != server.quarantined { updates["quarantined"] = editQuarantined }

        if updates.isEmpty {
            isEditing = false
            isSavingEdit = false
            return
        }

        do {
            try await client.updateServer(server.name, updates: updates)
            isEditing = false
            actionMessage = "Server configuration updated. Restart may be required."
        } catch {
            editError = "Failed to save: \(error.localizedDescription)"
        }

        isSavingEdit = false
    }

    // MARK: - Log Auto-Refresh

    private func startLogRefresh() {
        logRefreshTimer?.invalidate()
        logRefreshTimer = Timer.scheduledTimer(withTimeInterval: 3.0, repeats: true) { _ in
            Task { await loadLogs() }
        }
    }

    private func stopLogRefresh() {
        logRefreshTimer?.invalidate()
        logRefreshTimer = nil
    }
}

// MARK: - Tool Row (Expandable Disclosure)

struct ToolRow: View {
    let tool: ServerTool
    var serverName: String = ""
    var apiClient: APIClient? = nil
    @Environment(\.fontScale) var fontScale

    @State private var isExpanded = false
    @State private var diffData: [String: Any]? = nil
    @State private var isLoadingDiff = false

    private var needsApproval: Bool {
        guard let status = tool.approvalStatus else { return false }
        return status == "pending" || status == "changed"
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            // Collapsed header -- always visible, clickable to expand
            Button {
                withAnimation(.easeInOut(duration: 0.2)) {
                    isExpanded.toggle()
                }
                if isExpanded && needsApproval && diffData == nil {
                    loadDiff()
                }
            } label: {
                HStack(spacing: 6) {
                    Image(systemName: isExpanded ? "chevron.down" : "chevron.right")
                        .font(.scaled(.caption2, scale: fontScale).weight(.semibold))
                        .foregroundStyle(.secondary)
                        .frame(width: 12)

                    Text(tool.name)
                        .font(.scaledMonospaced(.body, scale: fontScale).weight(.semibold))
                        .foregroundStyle(.primary)

                    annotationBadgesCollapsed

                    if tool.approvalStatus == "pending" {
                        Text("NEW")
                            .font(.system(size: 9 * fontScale, weight: .bold))
                            .foregroundStyle(.white)
                            .padding(.horizontal, 5)
                            .padding(.vertical, 1)
                            .background(.blue)
                            .cornerRadius(3)
                    }
                    if tool.approvalStatus == "changed" {
                        Text("CHANGED")
                            .font(.system(size: 9 * fontScale, weight: .bold))
                            .foregroundStyle(.white)
                            .padding(.horizontal, 5)
                            .padding(.vertical, 1)
                            .background(.orange)
                            .cornerRadius(3)
                    }

                    Spacer()
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .padding(.vertical, 6)
            .padding(.horizontal, 8)

            // One-line description preview (always visible)
            if let desc = tool.description, !desc.isEmpty, !isExpanded {
                Text(desc)
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .padding(.leading, 26)
                    .padding(.trailing, 8)
                    .padding(.bottom, hasCollapsedAnnotations ? 2 : 6)
            }

            // Annotation hints below description (collapsed state only)
            if !isExpanded, let annotations = tool.annotations, hasAnyAnnotation(annotations) {
                HStack(spacing: 4) {
                    if annotations.readOnlyHint == true {
                        annotationHintBadge("read-only", color: .secondary)
                    }
                    if annotations.destructiveHint == true {
                        annotationHintBadge("destructive", color: .red)
                    }
                    if annotations.idempotentHint == true {
                        annotationHintBadge("idempotent", color: .secondary)
                    }
                    if annotations.openWorldHint == true {
                        annotationHintBadge("open-world", color: .orange)
                    }
                }
                .padding(.leading, 26)
                .padding(.trailing, 8)
                .padding(.bottom, 6)
            }

            // Expanded detail section
            if isExpanded {
                expandedContent
                    .padding(.leading, 26)
                    .padding(.trailing, 8)
                    .padding(.bottom, 8)
            }
        }
        .background(Color(nsColor: .controlBackgroundColor))
        .cornerRadius(8)
    }

    // MARK: - Expanded Content

    @ViewBuilder
    private var expandedContent: some View {
        VStack(alignment: .leading, spacing: 10) {
            // Full description
            if let desc = tool.description, !desc.isEmpty {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Description")
                        .font(.scaled(.caption, scale: fontScale).bold())
                        .foregroundStyle(.secondary)
                    Text(desc)
                        .font(.scaled(.subheadline, scale: fontScale))
                        .foregroundStyle(.primary)
                        .textSelection(.enabled)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }

            // Annotations section
            if let annotations = tool.annotations, hasAnyAnnotation(annotations) {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Annotations")
                        .font(.scaled(.caption, scale: fontScale).bold())
                        .foregroundStyle(.secondary)
                    annotationBadgesExpanded(annotations)
                }
            }

            // Approval Status section
            if let status = tool.approvalStatus {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Approval Status")
                        .font(.scaled(.caption, scale: fontScale).bold())
                        .foregroundStyle(.secondary)
                    HStack(spacing: 6) {
                        Circle()
                            .fill(approvalStatusColor(status))
                            .frame(width: 8, height: 8)
                            .accessibilityLabel("Approval: \(status)")
                        Text(approvalStatusLabel(status))
                            .font(.scaled(.subheadline, scale: fontScale))
                            .foregroundStyle(.primary)
                    }
                }
            }

            // Diff section for quarantined tools
            if needsApproval {
                diffSection
            }
        }
    }

    // MARK: - Annotation Helpers

    private func hasAnyAnnotation(_ a: ToolAnnotation) -> Bool {
        a.readOnlyHint == true || a.destructiveHint == true ||
        a.idempotentHint == true || a.openWorldHint == true ||
        (a.title != nil && !a.title!.isEmpty)
    }

    /// Compact inline badges for collapsed state.
    @ViewBuilder
    private var annotationBadgesCollapsed: some View {
        if let annotations = tool.annotations {
            if annotations.readOnlyHint == true {
                badge(text: "read-only", color: .green, icon: "eye")
            }
            if annotations.destructiveHint == true {
                badge(text: "destructive", color: .red, icon: "trash")
            }
            if annotations.idempotentHint == true {
                badge(text: "idempotent", color: .blue, icon: "arrow.2.squarepath")
            }
            if annotations.openWorldHint == true {
                badge(text: "open-world", color: .orange, icon: "globe")
            }
        }
    }

    /// Full annotation display for expanded state.
    @ViewBuilder
    private func annotationBadgesExpanded(_ annotations: ToolAnnotation) -> some View {
        let items: [(String, Color, String, Bool?)] = [
            ("Read-Only", .green, "eye", annotations.readOnlyHint),
            ("Destructive", .red, "trash", annotations.destructiveHint),
            ("Idempotent", .blue, "arrow.2.squarepath", annotations.idempotentHint),
            ("Open World", .orange, "globe", annotations.openWorldHint),
        ]
        FlowLayout(spacing: 4) {
            if let title = annotations.title, !title.isEmpty {
                badge(text: title, color: .purple, icon: "tag")
            }
            ForEach(items, id: \.0) { label, color, icon, value in
                if value == true {
                    badge(text: label, color: color, icon: icon)
                }
            }
        }
    }

    // MARK: - Approval Status Helpers

    private func approvalStatusColor(_ status: String) -> Color {
        switch status {
        case "approved": return .green
        case "pending": return .orange
        case "changed": return .red
        default: return .gray
        }
    }

    private func approvalStatusLabel(_ status: String) -> String {
        switch status {
        case "approved": return "Approved"
        case "pending": return "Pending Approval"
        case "changed": return "Changed (needs re-approval)"
        default: return status.capitalized
        }
    }

    // MARK: - Diff Section

    @ViewBuilder
    private var diffSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            Divider()

            if isLoadingDiff {
                HStack {
                    ProgressView()
                        .controlSize(.small)
                    Text("Loading changes...")
                        .font(.scaled(.caption, scale: fontScale))
                        .foregroundStyle(.secondary)
                }
                .padding(.vertical, 4)
            } else if let diff = diffData {
                // Description diff
                let oldDesc = diff["previous_description"] as? String
                    ?? diff["old_description"] as? String
                    ?? ""
                let newDesc = diff["current_description"] as? String
                    ?? diff["new_description"] as? String
                    ?? ""

                if !oldDesc.isEmpty || !newDesc.isEmpty {
                    Text("Description Changes")
                        .font(.scaled(.caption, scale: fontScale).bold())
                        .foregroundStyle(.secondary)

                    if oldDesc != newDesc {
                        if !oldDesc.isEmpty {
                            diffLine(text: oldDesc, isOld: true)
                        }
                        if !newDesc.isEmpty {
                            diffLine(text: newDesc, isOld: false)
                        }
                    } else {
                        Text("No description changes")
                            .font(.scaled(.caption, scale: fontScale))
                            .foregroundStyle(.tertiary)
                    }
                }

                // Schema diff
                let oldSchema = diff["previous_schema"] as? String
                    ?? diff["old_schema"] as? String
                    ?? (diff["old_input_schema"] as? String)
                    ?? ""
                let newSchema = diff["current_schema"] as? String
                    ?? diff["new_schema"] as? String
                    ?? (diff["new_input_schema"] as? String)
                    ?? ""

                if !oldSchema.isEmpty || !newSchema.isEmpty {
                    Text("Schema Changes")
                        .font(.scaled(.caption, scale: fontScale).bold())
                        .foregroundStyle(.secondary)
                        .padding(.top, 4)

                    if oldSchema != newSchema {
                        if !oldSchema.isEmpty {
                            diffLine(text: oldSchema, isOld: true)
                        }
                        if !newSchema.isEmpty {
                            diffLine(text: newSchema, isOld: false)
                        }
                    } else {
                        Text("No schema changes")
                            .font(.scaled(.caption, scale: fontScale))
                            .foregroundStyle(.tertiary)
                    }
                }

                if oldDesc.isEmpty && newDesc.isEmpty && oldSchema.isEmpty && newSchema.isEmpty {
                    HStack(spacing: 6) {
                        Image(systemName: "info.circle")
                            .foregroundStyle(.orange)
                        Text("New tool pending approval")
                            .font(.scaled(.caption, scale: fontScale))
                            .foregroundStyle(.secondary)
                    }
                }
            } else {
                Text("Could not load diff data")
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.secondary)
            }
        }
    }

    @ViewBuilder
    private func diffLine(text: String, isOld: Bool) -> some View {
        HStack(alignment: .top, spacing: 4) {
            Image(systemName: isOld ? "minus.circle.fill" : "plus.circle.fill")
                .font(.scaled(.caption2, scale: fontScale))
                .foregroundStyle(isOld ? .red : .green)
            Text(text)
                .font(.scaledMonospaced(.caption, scale: fontScale))
                .foregroundStyle(isOld ? .secondary : .primary)
                .lineLimit(isOld ? 6 : nil)
        }
        .padding(4)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background((isOld ? Color.red : Color.green).opacity(0.08))
        .cornerRadius(4)
    }

    private func loadDiff() {
        guard let client = apiClient else {
            diffData = [:]
            return
        }
        isLoadingDiff = true
        Task {
            do {
                let result = try await client.toolDiff(server: serverName, tool: tool.name)
                await MainActor.run {
                    diffData = result
                    isLoadingDiff = false
                }
            } catch {
                await MainActor.run {
                    diffData = [:]
                    isLoadingDiff = false
                }
            }
        }
    }

    // MARK: - Collapsed Annotations Helper

    private var hasCollapsedAnnotations: Bool {
        guard let annotations = tool.annotations else { return false }
        return hasAnyAnnotation(annotations)
    }

    /// Compact text-only annotation hint badge for collapsed tool rows.
    @ViewBuilder
    private func annotationHintBadge(_ text: String, color: Color) -> some View {
        Text(text)
            .font(.system(size: 9 * fontScale))
            .foregroundStyle(color)
            .padding(.horizontal, 4)
            .padding(.vertical, 1)
            .background(color.opacity(0.1))
            .cornerRadius(3)
    }

    // MARK: - Badge

    @ViewBuilder
    private func badge(text: String, color: Color, icon: String) -> some View {
        HStack(spacing: 2) {
            Image(systemName: icon)
                .font(.system(size: 8 * fontScale))
            Text(text)
                .font(.scaled(.caption2, scale: fontScale))
        }
        .foregroundStyle(color)
        .padding(.horizontal, 5)
        .padding(.vertical, 2)
        .background(color.opacity(0.1))
        .cornerRadius(4)
    }
}

// MARK: - Flow Layout (for wrapping annotation badges)

/// A simple horizontal flow layout that wraps items to the next line.
struct FlowLayout: Layout {
    var spacing: CGFloat = 4

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let result = layout(in: proposal.width ?? .infinity, subviews: subviews)
        return result.size
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        let result = layout(in: bounds.width, subviews: subviews)
        for (index, position) in result.positions.enumerated() {
            subviews[index].place(
                at: CGPoint(x: bounds.minX + position.x, y: bounds.minY + position.y),
                proposal: ProposedViewSize(subviews[index].sizeThatFits(.unspecified))
            )
        }
    }

    private struct LayoutResult {
        var positions: [CGPoint]
        var size: CGSize
    }

    private func layout(in maxWidth: CGFloat, subviews: Subviews) -> LayoutResult {
        var positions: [CGPoint] = []
        var x: CGFloat = 0
        var y: CGFloat = 0
        var rowHeight: CGFloat = 0
        var maxX: CGFloat = 0

        for subview in subviews {
            let size = subview.sizeThatFits(.unspecified)
            if x + size.width > maxWidth, x > 0 {
                x = 0
                y += rowHeight + spacing
                rowHeight = 0
            }
            positions.append(CGPoint(x: x, y: y))
            rowHeight = max(rowHeight, size.height)
            x += size.width + spacing
            maxX = max(maxX, x - spacing)
        }

        return LayoutResult(
            positions: positions,
            size: CGSize(width: maxX, height: y + rowHeight)
        )
    }
}
