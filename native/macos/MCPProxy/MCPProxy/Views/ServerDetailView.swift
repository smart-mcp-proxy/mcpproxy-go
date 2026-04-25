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
    // Per-server Docker isolation overrides.
    @State private var editIsolationImage = ""
    @State private var editIsolationNetworkMode = ""
    @State private var editIsolationExtraArgs = ""
    @State private var editIsolationWorkingDir = ""
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
                            ToolRow(tool: tool, serverName: server.name, apiClient: apiClient) {
                                Task { await loadTools() }
                            }
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

                    // Docker isolation overrides (only relevant for stdio servers).
                    // The global "Docker Isolation" toggle in the General section
                    // enables isolation for this server; these fields customize how
                    // that isolation is provisioned.
                    if server.protocol == "stdio" {
                        configSection(title: "Docker Isolation Overrides") {
                            if isEditing {
                                let defaults = server.isolationDefaults
                                let imagePlaceholder = isolationPlaceholder(
                                    for: defaults?.image,
                                    fallback: "Default image (resolved from runtime)"
                                )
                                let networkPlaceholder = isolationPlaceholder(
                                    for: defaults?.networkMode,
                                    fallback: "bridge | none | host"
                                )
                                let extraArgsPlaceholder: String = {
                                    if let extra = defaults?.extraArgs, !extra.isEmpty {
                                        return "Default: \(extra.joined(separator: " "))"
                                    }
                                    return "-v\n/Users/you/data:/data:rw"
                                }()
                                let workdirPlaceholder = isolationPlaceholder(
                                    for: defaults?.workingDir,
                                    fallback: "/vault (Docker default applies when empty)"
                                )

                                isolationOverrideRow(
                                    label: "Image",
                                    text: $editIsolationImage,
                                    placeholder: imagePlaceholder,
                                    defaultValue: defaults?.image
                                )
                                isolationOverrideRow(
                                    label: "Network Mode",
                                    text: $editIsolationNetworkMode,
                                    placeholder: networkPlaceholder,
                                    defaultValue: defaults?.networkMode
                                )
                                isolationOverrideRow(
                                    label: "Extra docker args (one per line)",
                                    text: $editIsolationExtraArgs,
                                    placeholder: extraArgsPlaceholder,
                                    defaultValue: (defaults?.extraArgs ?? []).joined(separator: " "),
                                    multiline: true
                                )
                                isolationOverrideRow(
                                    label: "Container Working Dir",
                                    text: $editIsolationWorkingDir,
                                    placeholder: workdirPlaceholder,
                                    defaultValue: defaults?.workingDir
                                )
                            } else if let iso = server.isolation {
                                if let img = iso.image, !img.isEmpty {
                                    configRow(label: "Image", value: img)
                                }
                                if let nm = iso.networkMode, !nm.isEmpty {
                                    configRow(label: "Network Mode", value: nm)
                                }
                                if let extra = iso.extraArgs, !extra.isEmpty {
                                    configRow(label: "Extra Args", value: extra.joined(separator: " "))
                                }
                                if let wd = iso.workingDir, !wd.isEmpty {
                                    configRow(label: "Container Working Dir", value: wd)
                                }
                                if (iso.image ?? "").isEmpty && (iso.networkMode ?? "").isEmpty
                                    && (iso.extraArgs?.isEmpty ?? true) && (iso.workingDir ?? "").isEmpty {
                                    configRow(label: "Overrides", value: "None (inherits global)")
                                }
                            } else {
                                configRow(label: "Overrides", value: "None (inherits global)")
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

    /// Format a placeholder string for an isolation override field. When
    /// the backend reports a resolved default we surface it explicitly so
    /// the user knows what an empty field will resolve to.
    private func isolationPlaceholder(for value: String?, fallback: String) -> String {
        if let v = value, !v.isEmpty {
            return "Default: \(v)"
        }
        return fallback
    }

    /// Renders one Docker isolation override field with a per-field clear
    /// button and a "(using default)" caption. Each field is independently
    /// clearable: tapping the clear button blanks the local edit binding,
    /// and on Save the empty string is sent to the backend, which clears
    /// the override under the existing PATCH semantics.
    @ViewBuilder
    private func isolationOverrideRow(
        label: String,
        text: Binding<String>,
        placeholder: String,
        defaultValue: String?,
        multiline: Bool = false
    ) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(alignment: .top) {
                Text(label)
                    .font(.scaled(.subheadline, scale: fontScale))
                    .foregroundStyle(.secondary)
                    .frame(width: 140, alignment: .trailing)
                if multiline {
                    ZStack(alignment: .topTrailing) {
                        TextEditor(text: text)
                            .font(.scaledMonospaced(.subheadline, scale: fontScale))
                            .frame(height: 60)
                            .border(Color(nsColor: .separatorColor), width: 1)
                        if !text.wrappedValue.isEmpty {
                            Button(action: { text.wrappedValue = "" }) {
                                Image(systemName: "xmark.circle.fill")
                                    .foregroundStyle(.secondary)
                            }
                            .buttonStyle(.plain)
                            .help("Clear override (use default)")
                            .padding(4)
                        }
                    }
                } else {
                    HStack(spacing: 4) {
                        TextField(placeholder, text: text)
                            .font(.scaledMonospaced(.subheadline, scale: fontScale))
                            .textFieldStyle(.roundedBorder)
                        if !text.wrappedValue.isEmpty {
                            Button(action: { text.wrappedValue = "" }) {
                                Image(systemName: "xmark.circle.fill")
                                    .foregroundStyle(.secondary)
                            }
                            .buttonStyle(.plain)
                            .help("Clear override (use default)")
                        }
                    }
                }
                Spacer()
            }
            // Caption beneath the field communicates which resolved default
            // would apply when the field is empty. Subtle but discoverable.
            if text.wrappedValue.isEmpty {
                let caption: String = {
                    if let d = defaultValue, !d.isEmpty {
                        return "Using default: \(d)"
                    }
                    return "Using default (no override set)"
                }()
                Text(caption)
                    .font(.scaled(.caption2, scale: fontScale))
                    .foregroundStyle(.tertiary)
                    .padding(.leading, 148)
            }
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
        editDockerIsolation = server.isolation?.enabled ?? false
        editSkipQuarantine = false  // read from config if available
        let iso = server.isolation
        editIsolationImage = iso?.image ?? ""
        editIsolationNetworkMode = iso?.networkMode ?? ""
        editIsolationExtraArgs = (iso?.extraArgs ?? []).joined(separator: "\n")
        editIsolationWorkingDir = iso?.workingDir ?? ""
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

        // Docker isolation (stdio only). Send any field that changed —
        // we always include `enabled` because the handler tracks the
        // boolean explicitly, and sending just the sub-fields without
        // it would leave the isolation off on a server that never had
        // it enabled before.
        if server.protocol == "stdio" {
            let existing = server.isolation
            let newExtra = editIsolationExtraArgs
                .components(separatedBy: "\n")
                .map { $0.trimmingCharacters(in: .whitespaces) }
                .filter { !$0.isEmpty }
            let newImage = editIsolationImage.trimmingCharacters(in: .whitespaces)
            let newNetwork = editIsolationNetworkMode.trimmingCharacters(in: .whitespaces)
            let newWorkDir = editIsolationWorkingDir.trimmingCharacters(in: .whitespaces)

            var iso: [String: Any] = [:]
            var isoChanged = false

            if editDockerIsolation != (existing?.enabled ?? false) {
                iso["enabled"] = editDockerIsolation
                isoChanged = true
            }
            if newImage != (existing?.image ?? "") {
                iso["image"] = newImage
                isoChanged = true
            }
            if newNetwork != (existing?.networkMode ?? "") {
                iso["network_mode"] = newNetwork
                isoChanged = true
            }
            if newExtra != (existing?.extraArgs ?? []) {
                iso["extra_args"] = newExtra
                isoChanged = true
            }
            if newWorkDir != (existing?.workingDir ?? "") {
                iso["working_dir"] = newWorkDir
                isoChanged = true
            }

            if isoChanged {
                // Always include `enabled` alongside any sub-field change so
                // the backend applies the full intended isolation state.
                if iso["enabled"] == nil {
                    iso["enabled"] = editDockerIsolation
                }
                updates["isolation"] = iso
            }
        }

        if updates.isEmpty {
            isEditing = false
            isSavingEdit = false
            return
        }

        do {
            try await client.updateServer(server.name, updates: updates)
            isEditing = false
            actionMessage = "Server configuration updated. Restart may be required."
            // Pull the updated config back so the Config tab shows the newly
            // saved values instead of the pre-edit snapshot.
            await refreshServer()
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
    var onApproved: (() -> Void)? = nil
    @Environment(\.fontScale) var fontScale

    @State private var isExpanded = false
    @State private var diffData: [String: Any]? = nil
    @State private var isLoadingDiff = false
    @State private var isApprovingTool = false
    @State private var approveSuccess = false

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
                    HStack(spacing: 8) {
                        Circle()
                            .fill(approvalStatusColor(status))
                            .frame(width: 8, height: 8)
                            .accessibilityLabel("Approval: \(status)")
                        Text(approvalStatusLabel(status))
                            .font(.scaled(.subheadline, scale: fontScale))
                            .foregroundStyle(.primary)

                        if needsApproval {
                            if approveSuccess {
                                HStack(spacing: 3) {
                                    Image(systemName: "checkmark.circle.fill")
                                        .foregroundStyle(.green)
                                    Text("Approved")
                                        .font(.scaled(.caption, scale: fontScale))
                                        .foregroundStyle(.green)
                                }
                            } else if isApprovingTool {
                                ProgressView()
                                    .controlSize(.small)
                            } else {
                                Button {
                                    approveTool()
                                } label: {
                                    Label("Approve", systemImage: "checkmark.shield")
                                }
                                .buttonStyle(.borderedProminent)
                                .tint(.orange)
                                .controlSize(.small)
                            }
                        }
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
                        diffBeforeAfter(previous: oldDesc, current: newDesc)
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
                        diffBeforeAfter(previous: oldSchema, current: newSchema)
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
    private func diffBeforeAfter(previous: String, current: String) -> some View {
        let parts = computeWordDiff(previous, current)
        VStack(alignment: .leading, spacing: 6) {
            diffBox(
                label: "BEFORE (APPROVED)",
                attributed: renderDiffSide(parts, keep: .removed),
                accent: .red,
                isEmpty: previous.isEmpty
            )
            diffBox(
                label: "AFTER (CURRENT)",
                attributed: renderDiffSide(parts, keep: .added),
                accent: .green,
                isEmpty: current.isEmpty
            )
        }
    }

    @ViewBuilder
    private func diffBox(label: String, attributed: AttributedString, accent: Color, isEmpty: Bool) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            Text(label)
                .font(.system(size: 9 * fontScale, weight: .semibold))
                .tracking(0.5)
                .foregroundStyle(.secondary)
            if isEmpty {
                Text("(empty)")
                    .font(.scaledMonospaced(.caption, scale: fontScale))
                    .foregroundStyle(.tertiary)
                    .padding(6)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(accent.opacity(0.04))
                    .overlay(
                        RoundedRectangle(cornerRadius: 4)
                            .strokeBorder(accent.opacity(0.25), lineWidth: 1)
                    )
                    .cornerRadius(4)
            } else {
                Text(attributed)
                    .font(.scaledMonospaced(.caption, scale: fontScale))
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(6)
                    .background(accent.opacity(0.04))
                    .overlay(
                        RoundedRectangle(cornerRadius: 4)
                            .strokeBorder(accent.opacity(0.25), lineWidth: 1)
                    )
                    .cornerRadius(4)
            }
        }
    }

    /// Build AttributedString for one side of the diff. `keep` is the change-type
    /// (removed or added) that belongs in this side; same-type parts render plain
    /// in both sides; the opposite change-type is dropped.
    private func renderDiffSide(_ parts: [ToolDiffPart], keep: ToolDiffPart.Kind) -> AttributedString {
        var result = AttributedString()
        for part in parts {
            switch part.kind {
            case .same:
                result += AttributedString(part.text)
            case .added, .removed:
                if part.kind != keep { continue }
                var span = AttributedString(part.text)
                let accent: Color = part.kind == .removed ? .red : .green
                span.backgroundColor = accent.opacity(0.25)
                span.foregroundColor = accent
                result += span
            }
        }
        return result
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

    private func approveTool() {
        guard let client = apiClient else { return }
        isApprovingTool = true
        Task {
            do {
                try await client.approveSpecificTools(serverName, tools: [tool.name])
                await MainActor.run {
                    isApprovingTool = false
                    approveSuccess = true
                    onApproved?()
                }
            } catch {
                await MainActor.run {
                    isApprovingTool = false
                    NSLog("[ToolRow] approveTool FAILED for %@: %@", tool.name, error.localizedDescription)
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

// MARK: - Tool Description Word Diff

struct ToolDiffPart {
    enum Kind { case same, added, removed }
    let kind: Kind
    let text: String
}

/// Split a string into tokens, preserving runs of whitespace as their own tokens
/// (matches the web UI's `split(/(\s+)/)` so the LCS behaves identically).
private func splitPreservingWhitespace(_ s: String) -> [String] {
    var tokens: [String] = []
    var buffer = ""
    var bufferIsWhitespace: Bool? = nil
    for ch in s {
        let chIsWhitespace = ch.isWhitespace
        if bufferIsWhitespace == nil {
            bufferIsWhitespace = chIsWhitespace
            buffer.append(ch)
        } else if bufferIsWhitespace == chIsWhitespace {
            buffer.append(ch)
        } else {
            tokens.append(buffer)
            buffer = String(ch)
            bufferIsWhitespace = chIsWhitespace
        }
    }
    if !buffer.isEmpty { tokens.append(buffer) }
    return tokens
}

/// Generic LCS diff over `Element` sequences (words or characters).
private func lcsDiff<Element: Equatable>(_ oldElems: [Element], _ newElems: [Element]) -> [(kind: ToolDiffPart.Kind, elem: Element)] {
    let m = oldElems.count
    let n = newElems.count
    if m == 0 && n == 0 { return [] }
    if m == 0 { return newElems.map { (.added, $0) } }
    if n == 0 { return oldElems.map { (.removed, $0) } }

    var dp = Array(repeating: Array(repeating: 0, count: n + 1), count: m + 1)
    for i in 1...m {
        for j in 1...n {
            if oldElems[i - 1] == newElems[j - 1] {
                dp[i][j] = dp[i - 1][j - 1] + 1
            } else {
                dp[i][j] = max(dp[i - 1][j], dp[i][j - 1])
            }
        }
    }

    var out: [(ToolDiffPart.Kind, Element)] = []
    var i = m
    var j = n
    while i > 0 || j > 0 {
        if i > 0 && j > 0 && oldElems[i - 1] == newElems[j - 1] {
            out.append((.same, oldElems[i - 1]))
            i -= 1
            j -= 1
        } else if j > 0 && (i == 0 || dp[i][j - 1] >= dp[i - 1][j]) {
            out.append((.added, newElems[j - 1]))
            j -= 1
        } else {
            out.append((.removed, oldElems[i - 1]))
            i -= 1
        }
    }
    out.reverse()
    return out
}

/// Character-level diff between two strings. Falls back to the raw
/// `removed → added` pair when either side exceeds `maxChars` to avoid
/// quadratic blowup on very long runs.
private func characterLevelDiff(_ oldText: String, _ newText: String, maxChars: Int = 1500) -> [ToolDiffPart] {
    if oldText.count > maxChars || newText.count > maxChars {
        return [
            ToolDiffPart(kind: .removed, text: oldText),
            ToolDiffPart(kind: .added, text: newText),
        ]
    }
    let oldChars = Array(oldText)
    let newChars = Array(newText)
    return lcsDiff(oldChars, newChars).map { ToolDiffPart(kind: $0.kind, text: String($0.elem)) }
}

/// Merge consecutive parts of the same kind to minimize attribute spans.
private func mergeSameKind(_ parts: [ToolDiffPart]) -> [ToolDiffPart] {
    var merged: [ToolDiffPart] = []
    for part in parts {
        if let last = merged.last, last.kind == part.kind {
            merged[merged.count - 1] = ToolDiffPart(kind: last.kind, text: last.text + part.text)
        } else {
            merged.append(part)
        }
    }
    return merged
}

/// Word-level diff with character-level refinement inside adjacent
/// (removed, added) pairs. This keeps whole-token highlights for large
/// docstring expansions while narrowing substring changes like
/// `"1 April"` → `"8 April"` down to just the `1`/`8` characters.
func computeWordDiff(_ oldText: String, _ newText: String) -> [ToolDiffPart] {
    let oldTokens = splitPreservingWhitespace(oldText)
    let newTokens = splitPreservingWhitespace(newText)
    let wordDiff = lcsDiff(oldTokens, newTokens).map { ToolDiffPart(kind: $0.kind, text: $0.elem) }

    // First merge consecutive same-kind parts, then refine adjacent
    // (removed, added) runs into character-level diffs.
    let merged = mergeSameKind(wordDiff)
    var refined: [ToolDiffPart] = []
    var idx = 0
    while idx < merged.count {
        let current = merged[idx]
        if idx + 1 < merged.count {
            let next = merged[idx + 1]
            if (current.kind == .removed && next.kind == .added) ||
               (current.kind == .added && next.kind == .removed) {
                let removedText = current.kind == .removed ? current.text : next.text
                let addedText = current.kind == .added ? current.text : next.text
                refined.append(contentsOf: characterLevelDiff(removedText, addedText))
                idx += 2
                continue
            }
        }
        refined.append(current)
        idx += 1
    }

    return mergeSameKind(refined)
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
