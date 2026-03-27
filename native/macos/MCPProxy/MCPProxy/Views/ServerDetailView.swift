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
    let server: ServerStatus
    @ObservedObject var appState: AppState
    let onDismiss: () -> Void

    @State private var selectedTab: ServerDetailTab = .tools
    @State private var tools: [ServerTool] = []
    @State private var logLines: [String] = []
    @State private var isLoadingTools = false
    @State private var isLoadingLogs = false
    @State private var isApproving = false
    @State private var actionMessage: String?

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
                .fill(healthColor)
                .frame(width: 12, height: 12)

            VStack(alignment: .leading, spacing: 2) {
                Text(server.name)
                    .font(.title2.bold())
                Text(server.health?.summary ?? statusText)
                    .font(.subheadline)
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

            if server.enabled {
                Button("Disable") {
                    Task { await performAction { try await apiClient?.disableServer(server.id) }
                        actionMessage = "\(server.name) disabled"
                    }
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
            } else {
                Button("Enable") {
                    Task { await performAction { try await apiClient?.enableServer(server.id) }
                        actionMessage = "\(server.name) enabled"
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
                                .font(.caption2.bold())
                                .foregroundStyle(.white)
                                .padding(.horizontal, 5)
                                .padding(.vertical, 1)
                                .background(.orange)
                                .clipShape(Capsule())
                        }
                    }
                    .padding(.horizontal, 16)
                    .padding(.vertical, 8)
                    .background(selectedTab == tab ? Color.accentColor.opacity(0.1) : .clear)
                    .cornerRadius(6)
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
                        .font(.system(size: 40))
                        .foregroundStyle(.tertiary)
                    Text("No tools available")
                        .font(.title3)
                        .foregroundStyle(.secondary)
                    Text(server.connected ? "This server has no tools" : "Connect the server to see tools")
                        .font(.caption)
                        .foregroundStyle(.tertiary)
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
                .font(.subheadline.bold())
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
            HStack {
                Text("Server Logs")
                    .font(.subheadline.bold())
                Text("\(logLines.count) lines")
                    .font(.caption)
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
                        .font(.system(size: 40))
                        .foregroundStyle(.tertiary)
                    Text("No log entries")
                        .font(.title3)
                        .foregroundStyle(.secondary)
                    Text("Logs will appear when the server produces output")
                        .font(.caption)
                        .foregroundStyle(.tertiary)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ScrollViewReader { proxy in
                    ScrollView([.horizontal, .vertical]) {
                        VStack(alignment: .leading, spacing: 0) {
                            ForEach(Array(logLines.enumerated()), id: \.offset) { idx, line in
                                logLineView(line)
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
    }

    /// Render a single log line with color-coded level.
    @ViewBuilder
    private func logLineView(_ line: String) -> some View {
        let levelColor = logLevelColor(line)
        Text(line)
            .font(.system(size: 11, design: .monospaced))
            .foregroundStyle(levelColor)
            .textSelection(.enabled)
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
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                configSection(title: "General") {
                    configRow(label: "Name", value: server.name)
                    configRow(label: "Protocol", value: server.protocol)
                    configRow(label: "Enabled", value: server.enabled ? "Yes" : "No")
                    if server.quarantined {
                        configRow(label: "Quarantined", value: "Yes")
                    }
                }

                if server.protocol == "http" || server.protocol == "sse" {
                    configSection(title: "Connection") {
                        configRow(label: "URL", value: server.url ?? "N/A")
                    }
                }

                if server.protocol == "stdio" {
                    configSection(title: "Process") {
                        configRow(label: "Command", value: server.command ?? "N/A")
                        if let args = server.args, !args.isEmpty {
                            configRow(label: "Args", value: args.joined(separator: " "))
                        }
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

    @ViewBuilder
    private func configSection(title: String, @ViewBuilder content: () -> some View) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(title)
                .font(.headline)
            content()
        }
        .padding()
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.quaternary.opacity(0.5))
        .cornerRadius(8)
    }

    @ViewBuilder
    private func configRow(label: String, value: String) -> some View {
        HStack(alignment: .top) {
            Text(label)
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .frame(width: 120, alignment: .trailing)
            Text(value)
                .font(.system(.subheadline, design: .monospaced))
                .textSelection(.enabled)
            Spacer()
        }
    }

    // MARK: - Action Banner

    @ViewBuilder
    private func actionBanner(_ message: String) -> some View {
        HStack {
            Text(message)
                .font(.caption)
                .foregroundStyle(.secondary)
            Spacer()
            Button("Dismiss") { actionMessage = nil }
                .buttonStyle(.borderless)
                .font(.caption)
        }
        .padding(.horizontal)
        .padding(.vertical, 6)
        .background(Color.accentColor.opacity(0.1))
    }

    // MARK: - Computed

    private var healthColor: Color {
        if server.quarantined { return .orange }
        if !server.enabled { return .gray }
        switch server.health?.level {
        case "healthy": return .green
        case "degraded": return .yellow
        case "unhealthy": return .red
        default: return server.connected ? .green : .gray
        }
    }

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
        guard let client = apiClient else { return }
        isLoadingTools = true
        defer { isLoadingTools = false }
        do {
            tools = try await client.serverTools(server.name)
        } catch {
            // Silently fail -- tools just won't display
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
}

// MARK: - Tool Row

struct ToolRow: View {
    let tool: ServerTool
    var serverName: String = ""
    var apiClient: APIClient? = nil

    @State private var isExpanded = false
    @State private var diffData: [String: Any]? = nil
    @State private var isLoadingDiff = false

    private var needsApproval: Bool {
        guard let status = tool.approvalStatus else { return false }
        return status == "pending" || status == "changed"
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(tool.name)
                    .font(.system(size: 13, weight: .semibold, design: .monospaced))

                annotationBadges

                if let status = tool.approvalStatus, status != "approved" {
                    Text(status.capitalized)
                        .font(.caption2.bold())
                        .foregroundStyle(.white)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 2)
                        .background(status == "changed" ? Color.red : .orange)
                        .clipShape(Capsule())
                }

                Spacer()

                if needsApproval {
                    Button {
                        if isExpanded {
                            isExpanded = false
                        } else {
                            isExpanded = true
                            if diffData == nil {
                                loadDiff()
                            }
                        }
                    } label: {
                        HStack(spacing: 4) {
                            Image(systemName: isExpanded ? "chevron.down" : "chevron.right")
                                .font(.system(size: 10))
                            Text("View Changes")
                                .font(.caption)
                        }
                    }
                    .buttonStyle(.borderless)
                    .foregroundStyle(.orange)
                }
            }

            if let desc = tool.description, !desc.isEmpty {
                Text(desc)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }

            // Expanded diff section
            if isExpanded && needsApproval {
                diffSection
            }
        }
        .padding(.vertical, 6)
        .padding(.horizontal, 8)
        .background(Color(nsColor: .controlBackgroundColor))
        .cornerRadius(6)
    }

    @ViewBuilder
    private var diffSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            Divider()

            if isLoadingDiff {
                HStack {
                    ProgressView()
                        .controlSize(.small)
                    Text("Loading changes...")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                .padding(.vertical, 4)
            } else if let diff = diffData {
                // Description diff
                // API returns "previous_description"/"current_description" (not "old"/"new")
                let oldDesc = diff["previous_description"] as? String
                    ?? diff["old_description"] as? String
                    ?? ""
                let newDesc = diff["current_description"] as? String
                    ?? diff["new_description"] as? String
                    ?? ""

                if !oldDesc.isEmpty || !newDesc.isEmpty {
                    Text("Description")
                        .font(.caption.bold())
                        .foregroundStyle(.secondary)

                    if oldDesc != newDesc {
                        if !oldDesc.isEmpty {
                            HStack(alignment: .top, spacing: 4) {
                                Image(systemName: "minus.circle.fill")
                                    .font(.system(size: 10))
                                    .foregroundStyle(.red)
                                Text(oldDesc)
                                    .font(.system(size: 11, design: .monospaced))
                                    .foregroundStyle(.secondary)
                            }
                            .padding(4)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .background(Color.red.opacity(0.08))
                            .cornerRadius(4)
                        }
                        if !newDesc.isEmpty {
                            HStack(alignment: .top, spacing: 4) {
                                Image(systemName: "plus.circle.fill")
                                    .font(.system(size: 10))
                                    .foregroundStyle(.green)
                                Text(newDesc)
                                    .font(.system(size: 11, design: .monospaced))
                            }
                            .padding(4)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .background(Color.green.opacity(0.08))
                            .cornerRadius(4)
                        }
                    } else {
                        Text("No description changes")
                            .font(.caption)
                            .foregroundStyle(.tertiary)
                    }
                }

                // Schema diff
                // API returns "previous_schema"/"current_schema" (not "old"/"new")
                let oldSchema = diff["previous_schema"] as? String
                    ?? diff["old_schema"] as? String
                    ?? (diff["old_input_schema"] as? String)
                    ?? ""
                let newSchema = diff["current_schema"] as? String
                    ?? diff["new_schema"] as? String
                    ?? (diff["new_input_schema"] as? String)
                    ?? ""

                if !oldSchema.isEmpty || !newSchema.isEmpty {
                    Text("Schema")
                        .font(.caption.bold())
                        .foregroundStyle(.secondary)
                        .padding(.top, 4)

                    if oldSchema != newSchema {
                        if !oldSchema.isEmpty {
                            HStack(alignment: .top, spacing: 4) {
                                Image(systemName: "minus.circle.fill")
                                    .font(.system(size: 10))
                                    .foregroundStyle(.red)
                                Text(oldSchema)
                                    .font(.system(size: 10, design: .monospaced))
                                    .foregroundStyle(.secondary)
                                    .lineLimit(6)
                            }
                            .padding(4)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .background(Color.red.opacity(0.08))
                            .cornerRadius(4)
                        }
                        if !newSchema.isEmpty {
                            HStack(alignment: .top, spacing: 4) {
                                Image(systemName: "plus.circle.fill")
                                    .font(.system(size: 10))
                                    .foregroundStyle(.green)
                                Text(newSchema)
                                    .font(.system(size: 10, design: .monospaced))
                                    .lineLimit(6)
                            }
                            .padding(4)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .background(Color.green.opacity(0.08))
                            .cornerRadius(4)
                        }
                    } else {
                        Text("No schema changes")
                            .font(.caption)
                            .foregroundStyle(.tertiary)
                    }
                }

                // If no diff data came back at all
                if oldDesc.isEmpty && newDesc.isEmpty && oldSchema.isEmpty && newSchema.isEmpty {
                    HStack(spacing: 6) {
                        Image(systemName: "info.circle")
                            .foregroundStyle(.orange)
                        Text("New tool pending approval")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
            } else {
                Text("Could not load diff data")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
        .padding(.top, 4)
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

    @ViewBuilder
    private var annotationBadges: some View {
        if let annotations = tool.annotations {
            if annotations.readOnlyHint == true {
                badge(text: "read", color: .green, icon: "eye")
            }
            if annotations.destructiveHint == true {
                badge(text: "destructive", color: .red, icon: "trash")
            } else if annotations.readOnlyHint != true {
                badge(text: "write", color: .orange, icon: "pencil")
            }
        }
    }

    @ViewBuilder
    private func badge(text: String, color: Color, icon: String) -> some View {
        HStack(spacing: 2) {
            Image(systemName: icon)
                .font(.system(size: 8))
            Text(text)
                .font(.caption2)
        }
        .foregroundStyle(color)
        .padding(.horizontal, 5)
        .padding(.vertical, 2)
        .background(color.opacity(0.1))
        .cornerRadius(4)
    }
}
