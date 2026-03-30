// AddServerView.swift
// MCPProxy
//
// Sheet dialog for adding a new server manually or importing from
// existing config files (Claude Desktop, Cursor, etc.).

import SwiftUI

// MARK: - Add Server Tab

enum AddServerTab: String, CaseIterable {
    case importConfig = "Import"
    case manual = "Manual"
}

// MARK: - Add Server View

struct AddServerView: View {
    @ObservedObject var appState: AppState
    @Binding var isPresented: Bool
    @Environment(\.fontScale) var fontScale

    @State private var selectedTab: AddServerTab

    private var apiClient: APIClient? { appState.apiClient }

    init(appState: AppState, isPresented: Binding<Bool>, initialTab: AddServerTab = .importConfig) {
        self.appState = appState
        self._isPresented = isPresented
        self._selectedTab = State(initialValue: initialTab)
    }

    var body: some View {
        VStack(spacing: 0) {
            // Header
            HStack {
                Text("Add Server")
                    .font(.scaled(.title2, scale: fontScale).bold())
                Spacer()
                Button {
                    isPresented = false
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .foregroundStyle(.secondary)
                }
                .buttonStyle(.borderless)
            }
            .padding()

            // Tab picker
            Picker("", selection: $selectedTab) {
                ForEach(AddServerTab.allCases, id: \.self) { tab in
                    Text(tab.rawValue).tag(tab)
                }
            }
            .pickerStyle(.segmented)
            .padding(.horizontal)
            .padding(.bottom, 8)

            Divider()

            switch selectedTab {
            case .importConfig:
                ImportServerForm(appState: appState, onDone: { isPresented = false })
            case .manual:
                ManualServerForm(appState: appState, onDone: { isPresented = false })
            }
        }
        .frame(width: 560, height: 560)
    }
}

// MARK: - Import Server Form

struct ImportServerForm: View {
    @ObservedObject var appState: AppState
    let onDone: () -> Void
    @Environment(\.fontScale) var fontScale

    @State private var configPaths: [CanonicalConfigPath] = []
    @State private var isLoading = false
    @State private var importingPath: String?
    @State private var resultMessage: String?
    @State private var errorMessage: String?

    private var apiClient: APIClient? { appState.apiClient }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            if isLoading && configPaths.isEmpty {
                ProgressView("Discovering config files...")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if configPaths.isEmpty {
                VStack(spacing: 12) {
                    Image(systemName: "doc.badge.gearshape")
                        .font(.system(size: 40 * fontScale))
                        .foregroundStyle(.tertiary)
                    Text("No config files found")
                        .font(.scaled(.title3, scale: fontScale))
                        .foregroundStyle(.secondary)
                    Text("Try adding a server manually instead")
                        .font(.scaled(.caption, scale: fontScale))
                        .foregroundStyle(.tertiary)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                if let msg = resultMessage {
                    HStack {
                        Image(systemName: "checkmark.circle.fill")
                            .foregroundStyle(.green)
                        Text(msg)
                            .font(.scaled(.caption, scale: fontScale))
                        Spacer()
                    }
                    .padding(.horizontal)
                    .padding(.vertical, 6)
                    .background(Color.green.opacity(0.1))
                }

                if let err = errorMessage {
                    HStack {
                        Image(systemName: "exclamationmark.triangle.fill")
                            .foregroundStyle(.red)
                        Text(err)
                            .font(.scaled(.caption, scale: fontScale))
                        Spacer()
                    }
                    .padding(.horizontal)
                    .padding(.vertical, 6)
                    .background(Color.red.opacity(0.1))
                }

                ScrollView {
                    VStack(alignment: .leading, spacing: 1) {
                        ForEach(configPaths) { config in
                            configPathRow(config)
                        }
                    }
                    .padding()
                }
            }
        }
        .task { await loadPaths() }
    }

    @ViewBuilder
    private func configPathRow(_ config: CanonicalConfigPath) -> some View {
        HStack {
            Image(systemName: config.exists ? "checkmark.circle.fill" : "circle")
                .foregroundStyle(config.exists ? .green : .gray)

            VStack(alignment: .leading, spacing: 2) {
                Text(config.name)
                    .font(.scaled(.subheadline, scale: fontScale).bold())
                Text(config.path)
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                if let desc = config.description, !desc.isEmpty {
                    Text(desc)
                        .font(.scaled(.caption2, scale: fontScale))
                        .foregroundStyle(.tertiary)
                }
            }

            Spacer()

            if config.exists {
                if importingPath == config.path {
                    ProgressView()
                        .controlSize(.small)
                } else {
                    Button("Import") {
                        Task { await importConfig(config) }
                    }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.small)
                }
            } else {
                Text("Not found")
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.tertiary)
            }
        }
        .padding(.vertical, 8)
        .padding(.horizontal, 10)
        .background(Color(nsColor: .controlBackgroundColor))
        .cornerRadius(6)
    }

    private func loadPaths() async {
        guard let client = apiClient else { return }
        isLoading = true
        defer { isLoading = false }
        do {
            configPaths = try await client.importPaths()
        } catch {
            errorMessage = "Failed to discover config paths: \(error.localizedDescription)"
        }
    }

    private func importConfig(_ config: CanonicalConfigPath) async {
        guard let client = apiClient else {
            errorMessage = "Not connected to MCPProxy core"
            return
        }
        importingPath = config.path
        resultMessage = nil
        errorMessage = nil
        do {
            let response = try await client.importFromPath(config.path, format: config.format)
            if let summary = response.summary {
                let imported = summary.imported ?? 0
                let skipped = summary.skipped ?? 0
                resultMessage = "\(imported) server(s) imported, \(skipped) skipped from \(config.name)"
            } else if let message = response.message {
                resultMessage = message
            } else {
                resultMessage = "Import completed from \(config.name)"
            }
        } catch {
            errorMessage = "Import failed: \(error.localizedDescription)"
        }
        importingPath = nil
    }
}

// MARK: - Manual Server Form

struct ManualServerForm: View {
    @ObservedObject var appState: AppState
    let onDone: () -> Void
    @Environment(\.fontScale) var fontScale

    enum FormField: Hashable {
        case name, url, command
    }

    enum SubmitPhase: Equatable {
        case idle
        case saving
        case connecting
        case success(toolCount: Int, quarantined: Bool)
        case failure(error: String)

        static func == (lhs: SubmitPhase, rhs: SubmitPhase) -> Bool {
            switch (lhs, rhs) {
            case (.idle, .idle), (.saving, .saving), (.connecting, .connecting):
                return true
            case (.success(let a1, let b1), .success(let a2, let b2)):
                return a1 == a2 && b1 == b2
            case (.failure(let a), .failure(let b)):
                return a == b
            default:
                return false
            }
        }
    }

    @FocusState private var focusedField: FormField?
    @State private var fieldTouched: Set<FormField> = []

    @State private var name = ""
    @State private var selectedProtocol = "stdio"
    @State private var url = ""
    @State private var command = ""
    @State private var argsText = ""
    @State private var envText = ""
    @State private var workingDir = ""
    @State private var isEnabled = true
    @State private var dockerIsolation = true
    @State private var quarantined = false
    @State private var submitPhase: SubmitPhase = .idle
    @State private var lastSavedServerName: String?  // Track what was saved so Retry can clean up
    @State private var errorMessage: String?

    private var apiClient: APIClient? { appState.apiClient }
    private let protocols = ["stdio", "http"]

    private var isSubmitActive: Bool {
        if case .idle = submitPhase { return false }
        return true
    }

    var body: some View {
        VStack(spacing: 0) {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    if let err = errorMessage {
                        HStack {
                            Image(systemName: "exclamationmark.triangle.fill")
                                .foregroundStyle(.red)
                            Text(err)
                                .font(.scaled(.caption, scale: fontScale))
                            Spacer()
                        }
                        .padding(.horizontal)
                        .padding(.vertical, 6)
                        .background(Color.red.opacity(0.1))
                        .cornerRadius(6)
                    }

                    // Name
                    formField(label: "Name (required)") {
                        TextField("e.g. github-server", text: $name)
                            .textFieldStyle(.roundedBorder)
                            .focused($focusedField, equals: .name)
                            .onChange(of: focusedField) { newValue in
                                if newValue != .name && focusedField != .name {
                                    fieldTouched.insert(.name)
                                }
                            }
                        if fieldTouched.contains(.name) && name.trimmingCharacters(in: .whitespaces).isEmpty {
                            Text("Server name is required")
                                .font(.caption)
                                .foregroundStyle(.red)
                        }
                    }

                    // Protocol
                    formField(label: "Protocol") {
                        Picker("", selection: $selectedProtocol) {
                            Text("Local Command (stdio)").tag("stdio")
                            Text("Remote URL (HTTP)").tag("http")
                        }
                        .pickerStyle(.segmented)
                    }

                    // URL (for http)
                    if selectedProtocol == "http" {
                        formField(label: "URL (required)") {
                            TextField("https://api.example.com/mcp", text: $url)
                                .textFieldStyle(.roundedBorder)
                                .focused($focusedField, equals: .url)
                                .onChange(of: focusedField) { newValue in
                                    if newValue != .url && focusedField != .url {
                                        fieldTouched.insert(.url)
                                    }
                                }
                            if fieldTouched.contains(.url) && url.trimmingCharacters(in: .whitespaces).isEmpty {
                                Text("URL is required")
                                    .font(.caption)
                                    .foregroundStyle(.red)
                            }
                        }
                    }

                    // Command (for stdio)
                    if selectedProtocol == "stdio" {
                        formField(label: "Command (required)") {
                            TextField("e.g. npx, uvx, node", text: $command)
                                .textFieldStyle(.roundedBorder)
                                .focused($focusedField, equals: .command)
                                .onChange(of: focusedField) { newValue in
                                    if newValue != .command && focusedField != .command {
                                        fieldTouched.insert(.command)
                                    }
                                }
                            if fieldTouched.contains(.command) && command.trimmingCharacters(in: .whitespaces).isEmpty {
                                Text("Command is required")
                                    .font(.caption)
                                    .foregroundStyle(.red)
                            }
                        }

                        formField(label: "Arguments (one per line)") {
                            TextEditor(text: $argsText)
                                .font(.scaledMonospaced(.body, scale: fontScale))
                                .frame(height: 60)
                                .border(Color.gray.opacity(0.3), width: 1)
                        }
                    }

                    // Working directory
                    formField(label: "Working Directory (optional)") {
                        TextField("/path/to/project", text: $workingDir)
                            .textFieldStyle(.roundedBorder)
                    }

                    // Env vars
                    formField(label: "Environment Variables (KEY=VALUE per line)") {
                        TextEditor(text: $envText)
                            .font(.scaledMonospaced(.body, scale: fontScale))
                            .frame(height: 60)
                            .border(Color.gray.opacity(0.3), width: 1)
                    }

                    // Options
                    formField(label: "Options") {
                        VStack(alignment: .leading, spacing: 10) {
                            VStack(alignment: .leading, spacing: 2) {
                                Toggle("Enabled", isOn: $isEnabled)
                                Text("When disabled, the server will not connect or be available to AI agents.")
                                    .font(.scaled(.caption2, scale: fontScale))
                                    .foregroundStyle(.tertiary)
                            }

                            VStack(alignment: .leading, spacing: 2) {
                                Toggle("Docker Isolation", isOn: $dockerIsolation)
                                Text("Runs the server in an isolated Docker container. Prevents access to your filesystem, network, and other system resources. Recommended for untrusted servers.")
                                    .font(.scaled(.caption2, scale: fontScale))
                                    .foregroundStyle(.tertiary)
                            }

                            VStack(alignment: .leading, spacing: 2) {
                                Toggle("Quarantined", isOn: $quarantined)
                                Text("New tools must be reviewed and approved before AI agents can use them. Protects against tool poisoning attacks.")
                                    .font(.scaled(.caption2, scale: fontScale))
                                    .foregroundStyle(.tertiary)
                            }
                        }
                    }
                }
                .padding()
            }

            Divider()

            // Pinned submit area
            VStack(spacing: 8) {
                // Connection test feedback
                switch submitPhase {
                case .saving:
                    HStack(spacing: 8) {
                        ProgressView().controlSize(.small)
                        Text("Saving configuration...")
                            .font(.scaled(.caption, scale: fontScale))
                            .foregroundStyle(.secondary)
                        Spacer()
                    }
                    .padding(.horizontal)

                case .connecting:
                    HStack(spacing: 8) {
                        ProgressView().controlSize(.small)
                        Text("Connecting to server...")
                            .font(.scaled(.caption, scale: fontScale))
                            .foregroundStyle(.secondary)
                        Spacer()
                    }
                    .padding(.horizontal)

                case .success(let toolCount, let quarantined):
                    HStack(spacing: 8) {
                        Image(systemName: quarantined ? "shield.lefthalf.filled" : "checkmark.circle.fill")
                            .foregroundStyle(quarantined ? .orange : .green)
                        Text(quarantined
                             ? "Server added — quarantined for security review"
                             : "Connected (\(toolCount) tools)")
                            .font(.scaled(.caption, scale: fontScale))
                            .foregroundStyle(quarantined ? .orange : .green)
                        Spacer()
                    }
                    .padding(.horizontal)

                case .failure(let error):
                    VStack(alignment: .leading, spacing: 6) {
                        HStack(alignment: .top, spacing: 8) {
                            Image(systemName: "exclamationmark.triangle.fill")
                                .foregroundStyle(.red)
                            Text(error)
                                .font(.scaled(.caption, scale: fontScale))
                                .foregroundStyle(.red)
                                .lineLimit(8)
                                .textSelection(.enabled)
                            Spacer()
                        }

                        HStack {
                            Spacer()
                            Button("Save Anyway") {
                                onDone()
                            }
                            .buttonStyle(.bordered)
                            .controlSize(.small)
                            Button("Retry") {
                                submitPhase = .idle
                                Task { await submitServer() }
                            }
                            .buttonStyle(.borderedProminent)
                            .controlSize(.small)
                        }
                    }
                    .padding(.horizontal)

                default:
                    EmptyView()
                }

                // Show the Add Server button for all phases except failure
                // (failure shows its own Save Anyway / Retry buttons above)
                if case .failure = submitPhase {
                    // failure shows its own buttons above
                } else {
                    HStack {
                        Spacer()
                        Button("Add Server") {
                            Task { await submitServer() }
                        }
                        .buttonStyle(.borderedProminent)
                        .disabled(!isValid || isSubmitActive)
                    }
                    .padding(.horizontal)
                }
            }
            .padding(.vertical, 12)
        }
    }

    @ViewBuilder
    private func formField(label: String, @ViewBuilder content: () -> some View) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(label)
                .font(.scaled(.subheadline, scale: fontScale))
                .foregroundStyle(.secondary)
            content()
        }
    }

    private var isValid: Bool {
        guard !name.trimmingCharacters(in: .whitespaces).isEmpty else { return false }
        if selectedProtocol == "stdio" {
            return !command.trimmingCharacters(in: .whitespaces).isEmpty
        } else {
            return !url.trimmingCharacters(in: .whitespaces).isEmpty
        }
    }

    private func submitServer() async {
        guard let client = apiClient else { return }
        errorMessage = nil
        submitPhase = .saving

        // If a previous attempt saved a server, delete it first so we can re-add
        // with potentially updated params (user may have changed name, command, etc.)
        if let oldName = lastSavedServerName {
            NSLog("[AddServer] Retry: deleting previously saved server '%@' before re-adding", oldName)
            try? await client.deleteAction(path: "/api/v1/servers/\(oldName)")
            lastSavedServerName = nil
        }

        var config: [String: Any] = [
            "name": name.trimmingCharacters(in: .whitespaces),
            "protocol": selectedProtocol,
            "enabled": isEnabled,
            "docker_isolation": dockerIsolation,
            "quarantined": quarantined,
        ]

        if selectedProtocol == "http" {
            config["url"] = url.trimmingCharacters(in: .whitespaces)
        } else {
            config["command"] = command.trimmingCharacters(in: .whitespaces)
            let args = argsText.components(separatedBy: "\n")
                .map { $0.trimmingCharacters(in: .whitespaces) }
                .filter { !$0.isEmpty }
            if !args.isEmpty {
                config["args"] = args
            }
        }

        let trimmedDir = workingDir.trimmingCharacters(in: .whitespaces)
        if !trimmedDir.isEmpty {
            config["working_dir"] = trimmedDir
        }

        // Parse env vars
        let envLines = envText.components(separatedBy: "\n")
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }
        if !envLines.isEmpty {
            var envDict: [String: String] = [:]
            for line in envLines {
                let parts = line.split(separator: "=", maxSplits: 1)
                if parts.count == 2 {
                    envDict[String(parts[0])] = String(parts[1])
                }
            }
            if !envDict.isEmpty {
                config["env"] = envDict
            }
        }

        let serverName = name.trimmingCharacters(in: .whitespaces)

        do {
            try await client.addServer(config)
            lastSavedServerName = serverName
        } catch {
            let errorDetail = error.localizedDescription

            // 409 "already exists" — only treat as success if WE saved it on a previous attempt.
            // Otherwise it's a name collision with an existing server the user didn't create here.
            if errorDetail.contains("already exists") || errorDetail.contains("409") {
                if lastSavedServerName == serverName {
                    NSLog("[AddServer] Server already exists (our previous save succeeded), checking status")
                    // Fall through to connection polling below
                } else {
                    // Name collision with an existing server — show error so user picks a different name
                    submitPhase = .failure(error: "Server '\(serverName)' already exists. Choose a different name.")
                    return
                }
            } else if errorDetail.contains("timed out") || errorDetail.contains("timeout") {
                // Socket POST timed out — retry via TCP
                NSLog("[AddServer] Socket POST timed out, retrying via TCP")
                do {
                    var apiKey: String?
                    if let info = try? await client.info() {
                        if let comps = URLComponents(string: info.webUiUrl),
                           let key = comps.queryItems?.first(where: { $0.name == "apikey" })?.value {
                            apiKey = key
                        }
                    }
                    let tcpClient = APIClient(socketPath: "", baseURL: "http://127.0.0.1:8080", apiKey: apiKey)
                    try await tcpClient.addServer(config)
                } catch let retryError {
                    let retryDetail = retryError.localizedDescription
                    // TCP also got 409 — server was saved, fall through
                    if retryDetail.contains("already exists") || retryDetail.contains("409") {
                        NSLog("[AddServer] TCP retry: already exists — checking status")
                    } else {
                        var msg = "Failed to add server: \(retryDetail)"
                        let logHint = await fetchServerLogHint(client: client, serverName: serverName)
                        if !logHint.isEmpty { msg += "\n\nServer log: \(logHint)" }
                        submitPhase = .failure(error: msg)
                        return
                    }
                }
            } else {
                // Actual error — show it, user can edit fields and retry
                var msg = errorDetail
                let logHint = await fetchServerLogHint(client: client, serverName: serverName)
                if !logHint.isEmpty { msg += "\n\nServer log: \(logHint)" }
                submitPhase = .failure(error: msg)
                return
            }
        }

        // Server saved successfully. Now poll for connection status.
        // States: connecting → connected | quarantined | error
        submitPhase = .connecting

        // Poll up to 3 times (every 3s) for the server to finish connecting
        for attempt in 1...3 {
            try? await Task.sleep(nanoseconds: 3_000_000_000)

            guard let servers = try? await client.servers(),
                  let server = servers.first(where: { $0.name == serverName }) else {
                // Server not in list yet — keep waiting
                continue
            }

            let status = server.status ?? ""
            let isConnecting = (server.connecting == true) || status == "connecting"

            if server.connected {
                submitPhase = .success(toolCount: server.toolCount, quarantined: server.quarantined)
                try? await Task.sleep(nanoseconds: 1_500_000_000)
                onDone()
                return
            } else if server.quarantined && !isConnecting {
                // Quarantined and done connecting (or waiting for exemption)
                submitPhase = .success(toolCount: server.toolCount, quarantined: true)
                try? await Task.sleep(nanoseconds: 1_500_000_000)
                onDone()
                return
            } else if isConnecting && attempt < 3 {
                // Still connecting — keep polling (don't show error yet)
                continue
            } else if let lastError = server.lastError, !lastError.isEmpty {
                // Has an actual error — show it
                var detail = lastError
                let logHint = await fetchServerLogHint(client: client, serverName: serverName)
                if !logHint.isEmpty { detail += "\n\nRecent log: \(logHint)" }
                submitPhase = .failure(error: detail)
                return
            }
        }

        // After 3 attempts (9s), server exists but still connecting — treat as success
        // The server list will show the real-time status
        submitPhase = .success(toolCount: 0, quarantined: true)
        try? await Task.sleep(nanoseconds: 1_500_000_000)
        onDone()
    }

    /// Fetch the last error line from server logs to show a helpful hint.
    private func fetchServerLogHint(client: APIClient, serverName: String) async -> String {
        do {
            let logs = try await client.serverLogs(serverName, tail: 10)
            // Find the last ERROR line
            let errorLines = logs.filter { $0.contains("ERROR") || $0.contains("error") || $0.contains("failed") || $0.contains("not found") }
            if let lastError = errorLines.last {
                // Trim to a reasonable length
                let trimmed = lastError.count > 200 ? String(lastError.prefix(200)) + "..." : lastError
                return trimmed
            }
        } catch {
            // Can't get logs — that's fine, just return empty
        }
        return ""
    }
}
