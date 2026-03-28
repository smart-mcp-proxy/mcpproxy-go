// ConfigView.swift
// MCPProxy
//
// Displays and allows editing the MCPProxy configuration file
// (~/.mcpproxy/mcp_config.json). Shows the raw JSON with basic
// syntax highlighting and provides save/revert functionality.
//
// Reads apiClient from appState instead of taking it as a parameter.

import SwiftUI

// MARK: - Config View

struct ConfigView: View {
    @ObservedObject var appState: AppState
    @Environment(\.fontScale) var fontScale

    private var apiClient: APIClient? { appState.apiClient }

    @State private var configText = ""
    @State private var originalText = ""
    @State private var isLoading = false
    @State private var isSaving = false
    @State private var errorMessage: String?
    @State private var successMessage: String?
    @State private var isEditing = false

    private let configPath: URL = {
        let home = FileManager.default.homeDirectoryForCurrentUser
        return home.appendingPathComponent(".mcpproxy/mcp_config.json")
    }()

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            // Header
            configHeader

            Divider()

            if let error = errorMessage {
                errorBanner(error)
            }

            if let success = successMessage {
                successBanner(success)
            }

            // Config content
            if isLoading {
                ProgressView("Loading configuration...")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                configEditor
            }
        }
        .task { loadConfig() }
    }

    // MARK: - Header

    @ViewBuilder
    private var configHeader: some View {
        HStack {
            VStack(alignment: .leading, spacing: 2) {
                Text("Configuration")
                    .font(.scaled(.title2, scale: fontScale).bold())
                Text(configPath.path)
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
            }

            Spacer()

            if hasChanges {
                Text("Modified")
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.orange)
                    .padding(.horizontal, 8)
                    .padding(.vertical, 2)
                    .background(.orange.opacity(0.1))
                    .cornerRadius(4)
            }

            Button {
                loadConfig()
            } label: {
                Image(systemName: "arrow.clockwise")
            }
            .buttonStyle(.borderless)
            .help("Reload from disk")

            Button("Open in Editor") {
                NSWorkspace.shared.open(configPath)
            }
            .buttonStyle(.bordered)
            .controlSize(.small)

            if isEditing {
                Button("Revert") {
                    configText = originalText
                    clearMessages()
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
                .disabled(!hasChanges)

                Button("Save") {
                    saveConfig()
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
                .disabled(!hasChanges || isSaving)
            } else {
                Button("Edit") {
                    isEditing = true
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
            }
        }
        .padding()
    }

    // MARK: - Editor

    @ViewBuilder
    private var configEditor: some View {
        if isEditing {
            // Editable text editor
            TextEditor(text: $configText)
                .font(.scaledMonospaced(.body, scale: fontScale))
                .padding(8)
                .accessibilityIdentifier("config-editor")
                .onChange(of: configText) { _ in
                    clearMessages()
                }
        } else {
            // Read-only scrollable view with line numbers
            ScrollView([.horizontal, .vertical]) {
                HStack(alignment: .top, spacing: 0) {
                    // Line numbers
                    let lines = configText.components(separatedBy: "\n")
                    VStack(alignment: .trailing, spacing: 0) {
                        ForEach(Array(lines.enumerated()), id: \.offset) { index, _ in
                            Text("\(index + 1)")
                                .font(.scaledMonospaced(.caption, scale: fontScale))
                                .foregroundStyle(.tertiary)
                                .frame(minWidth: 30, alignment: .trailing)
                        }
                    }
                    .padding(.trailing, 8)
                    .padding(.leading, 8)

                    Divider()

                    // Config text
                    Text(configText)
                        .font(.scaledMonospaced(.body, scale: fontScale))
                        .textSelection(.enabled)
                        .padding(.leading, 8)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
                .padding(.vertical, 8)
            }
            .background(Color(nsColor: .textBackgroundColor))
            .accessibilityIdentifier("config-editor")
        }
    }

    // MARK: - Banners

    @ViewBuilder
    private func errorBanner(_ message: String) -> some View {
        HStack {
            Image(systemName: "exclamationmark.triangle.fill")
                .foregroundStyle(.red)
            Text(message)
                .font(.scaled(.caption, scale: fontScale))
                .foregroundStyle(.secondary)
            Spacer()
            Button("Dismiss") { errorMessage = nil }
                .buttonStyle(.borderless)
                .font(.scaled(.caption, scale: fontScale))
        }
        .padding(.horizontal)
        .padding(.vertical, 6)
        .background(Color.red.opacity(0.1))
    }

    @ViewBuilder
    private func successBanner(_ message: String) -> some View {
        HStack {
            Image(systemName: "checkmark.circle.fill")
                .foregroundStyle(.green)
            Text(message)
                .font(.scaled(.caption, scale: fontScale))
                .foregroundStyle(.secondary)
            Spacer()
            Button("Dismiss") { successMessage = nil }
                .buttonStyle(.borderless)
                .font(.scaled(.caption, scale: fontScale))
        }
        .padding(.horizontal)
        .padding(.vertical, 6)
        .background(Color.green.opacity(0.1))
    }

    // MARK: - Computed

    private var hasChanges: Bool {
        configText != originalText
    }

    // MARK: - File I/O

    private func loadConfig() {
        isLoading = true
        clearMessages()
        defer { isLoading = false }

        do {
            let data = try Data(contentsOf: configPath)
            let text = String(data: data, encoding: .utf8) ?? ""
            configText = text
            originalText = text
        } catch {
            errorMessage = "Failed to load config: \(error.localizedDescription)"
            configText = ""
            originalText = ""
        }
    }

    private func saveConfig() {
        clearMessages()

        // Validate JSON before saving
        guard let data = configText.data(using: .utf8) else {
            errorMessage = "Failed to encode text as UTF-8"
            return
        }

        do {
            _ = try JSONSerialization.jsonObject(with: data)
        } catch {
            errorMessage = "Invalid JSON: \(error.localizedDescription)"
            return
        }

        isSaving = true
        defer { isSaving = false }

        do {
            try data.write(to: configPath, options: .atomic)
            originalText = configText
            successMessage = "Configuration saved. MCPProxy will auto-reload."
            isEditing = false
        } catch {
            errorMessage = "Failed to save: \(error.localizedDescription)"
        }
    }

    private func clearMessages() {
        errorMessage = nil
        successMessage = nil
    }
}
