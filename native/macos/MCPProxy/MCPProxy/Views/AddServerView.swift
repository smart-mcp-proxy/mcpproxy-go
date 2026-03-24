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

    @State private var selectedTab: AddServerTab = .importConfig

    private var apiClient: APIClient? { appState.apiClient }

    var body: some View {
        VStack(spacing: 0) {
            // Header
            HStack {
                Text("Add Server")
                    .font(.title2.bold())
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
        .frame(width: 520, height: 480)
    }
}

// MARK: - Import Server Form

struct ImportServerForm: View {
    @ObservedObject var appState: AppState
    let onDone: () -> Void

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
                        .font(.system(size: 40))
                        .foregroundStyle(.tertiary)
                    Text("No config files found")
                        .font(.title3)
                        .foregroundStyle(.secondary)
                    Text("Try adding a server manually instead")
                        .font(.caption)
                        .foregroundStyle(.tertiary)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                if let msg = resultMessage {
                    HStack {
                        Image(systemName: "checkmark.circle.fill")
                            .foregroundStyle(.green)
                        Text(msg)
                            .font(.caption)
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
                            .font(.caption)
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
                    .font(.subheadline.bold())
                Text(config.path)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                if let desc = config.description, !desc.isEmpty {
                    Text(desc)
                        .font(.caption2)
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
                    .font(.caption)
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
        guard let client = apiClient else { return }
        importingPath = config.path
        resultMessage = nil
        errorMessage = nil
        defer { importingPath = nil }
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
    }
}

// MARK: - Manual Server Form

struct ManualServerForm: View {
    @ObservedObject var appState: AppState
    let onDone: () -> Void

    @State private var name = ""
    @State private var selectedProtocol = "stdio"
    @State private var url = ""
    @State private var command = ""
    @State private var argsText = ""
    @State private var envText = ""
    @State private var workingDir = ""
    @State private var isSubmitting = false
    @State private var errorMessage: String?

    private var apiClient: APIClient? { appState.apiClient }
    private let protocols = ["stdio", "http", "sse"]

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                if let err = errorMessage {
                    HStack {
                        Image(systemName: "exclamationmark.triangle.fill")
                            .foregroundStyle(.red)
                        Text(err)
                            .font(.caption)
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
                }

                // Protocol
                formField(label: "Protocol") {
                    Picker("", selection: $selectedProtocol) {
                        ForEach(protocols, id: \.self) { proto in
                            Text(proto).tag(proto)
                        }
                    }
                    .pickerStyle(.segmented)
                }

                // URL (for http/sse)
                if selectedProtocol == "http" || selectedProtocol == "sse" {
                    formField(label: "URL (required)") {
                        TextField("https://api.example.com/mcp", text: $url)
                            .textFieldStyle(.roundedBorder)
                    }
                }

                // Command (for stdio)
                if selectedProtocol == "stdio" {
                    formField(label: "Command (required)") {
                        TextField("e.g. npx, uvx, node", text: $command)
                            .textFieldStyle(.roundedBorder)
                    }

                    formField(label: "Arguments (one per line)") {
                        TextEditor(text: $argsText)
                            .font(.system(.body, design: .monospaced))
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
                        .font(.system(.body, design: .monospaced))
                        .frame(height: 60)
                        .border(Color.gray.opacity(0.3), width: 1)
                }

                // Submit
                HStack {
                    Spacer()
                    if isSubmitting {
                        ProgressView()
                            .controlSize(.small)
                    }
                    Button("Add Server") {
                        Task { await submitServer() }
                    }
                    .buttonStyle(.borderedProminent)
                    .disabled(!isValid || isSubmitting)
                }
            }
            .padding()
        }
    }

    @ViewBuilder
    private func formField(label: String, @ViewBuilder content: () -> some View) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(label)
                .font(.subheadline)
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
        isSubmitting = true
        defer { isSubmitting = false }

        var config: [String: Any] = [
            "name": name.trimmingCharacters(in: .whitespaces),
            "protocol": selectedProtocol,
            "enabled": true,
        ]

        if selectedProtocol == "http" || selectedProtocol == "sse" {
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

        do {
            try await client.addServer(config)
            onDone()
        } catch {
            errorMessage = "Failed to add server: \(error.localizedDescription)"
        }
    }
}
