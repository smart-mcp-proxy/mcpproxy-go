// PasteServerView.swift
// MCPProxy
//
// The Add Server sheet's Paste tab (Spec 109 FR-064/065): paste a URL, a
// command line, or a JSON/TOML config; preview-detect the server (never
// executes anything); fill in any detected env/header fields with a
// Value/Secret toggle; add. Mirrors the Web UI's `PasteServer.vue`.

import SwiftUI

struct PasteServerView: View {
    @ObservedObject var appState: AppState
    let onAdded: (String) -> Void

    @Environment(\.fontScale) var fontScale

    @State private var content = ""
    @State private var isLoading = false
    @State private var errorMessage: String?
    @State private var preview: ImportPreviewServer?
    @State private var format: String?
    @State private var fields: [SecretFieldInput] = []
    @State private var addError: String?
    @State private var adding = false
    @State private var previewTask: Task<Void, Never>?
    // The exact raw text that produced `preview` — captured separately from
    // `content` (which keeps changing as the user types) so Add always
    // re-parses the same input the preview was computed from, even if a
    // debounced re-preview for newer text hasn't landed yet.
    @State private var previewRawContent = ""

    private var apiClient: APIClient? { appState.apiClient }

    private var formatLabel: String {
        switch format {
        case "url": return "Remote URL"
        case "command": return "Command line"
        default: return format ?? "Config"
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("Paste a URL, a command line, or a JSON/TOML config")
                .font(.scaled(.subheadline, scale: fontScale))
                .foregroundStyle(.secondary)
                .padding(.horizontal)
                .padding(.top, 8)

            TextEditor(text: $content)
                .font(.scaledMonospaced(.body, scale: fontScale))
                .frame(height: 100)
                .border(Color.gray.opacity(0.3), width: 1)
                .padding(.horizontal)
                .padding(.top, 6)
                .onChange(of: content) { _ in schedulePreview() }
                .accessibilityIdentifier("paste-textarea")

            statusArea
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .accessibilityIdentifier("paste-server")
    }

    @ViewBuilder
    private var statusArea: some View {
        if isLoading {
            HStack(spacing: 8) {
                ProgressView().controlSize(.small)
                Text("Detecting…")
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.secondary)
            }
            .padding(.horizontal)
            .padding(.top, 8)
            .accessibilityIdentifier("paste-loading")
        } else if let err = errorMessage {
            Text(err)
                .font(.scaled(.caption, scale: fontScale))
                .foregroundStyle(.red)
                .padding(.horizontal)
                .padding(.top, 8)
                .accessibilityIdentifier("paste-error")
        } else if let preview {
            ScrollView {
                VStack(alignment: .leading, spacing: 10) {
                    HStack(spacing: 6) {
                        badge(formatLabel)
                        ForEach(preview.tags ?? [], id: \.self) { badge($0) }
                    }
                    if let summary = preview.summary, !summary.isEmpty {
                        Text(summary)
                            .font(.scaledMonospaced(.caption, scale: fontScale))
                            .foregroundStyle(.secondary)
                            .textSelection(.enabled)
                            .accessibilityIdentifier("paste-summary")
                    }

                    ForEach($fields) { $field in
                        SecretFieldToggleView(name: field.name, value: $field.value, mode: $field.mode)
                    }

                    if let addError {
                        Text(addError)
                            .font(.scaled(.caption, scale: fontScale))
                            .foregroundStyle(.red)
                            .accessibilityIdentifier("paste-add-error")
                    }

                    Button {
                        Task { await handleAdd() }
                    } label: {
                        if adding { ProgressView().controlSize(.small) } else { Text("Add to MCPProxy") }
                    }
                    .buttonStyle(.borderedProminent)
                    .disabled(adding)
                    .accessibilityIdentifier("paste-add-button")
                }
                .padding()
            }
        }
    }

    private func badge(_ text: String) -> some View {
        Text(text)
            .font(.scaled(.caption2, scale: fontScale))
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .overlay(Capsule().stroke(Color.secondary.opacity(0.4)))
    }

    // MARK: - Preview

    private func schedulePreview() {
        previewTask?.cancel()
        let raw = content.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !raw.isEmpty else {
            preview = nil
            errorMessage = nil
            return
        }
        previewTask = Task {
            try? await Task.sleep(nanoseconds: 400_000_000)
            if Task.isCancelled { return }
            await runPreview()
        }
    }

    private func runPreview() async {
        guard let client = apiClient else {
            errorMessage = "Not connected to MCPProxy core"
            return
        }
        let raw = content.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !raw.isEmpty else { return }
        isLoading = true
        errorMessage = nil
        defer { isLoading = false }
        do {
            let resp = try await client.previewImportContent(raw)
            guard let first = resp.imported.first else {
                errorMessage = "Could not detect a server from this input"
                preview = nil
                return
            }
            format = resp.format
            preview = first
            previewRawContent = raw
            fields = Self.buildFields(from: first)
        } catch {
            errorMessage = error.localizedDescription
            preview = nil
        }
    }

    /// Pure: turns an import preview's env/header fields into the toggle
    /// list, defaulting each to Secret when the backend's D13 heuristic (or
    /// an explicit registry flag) says the name looks like a credential.
    /// Extracted so it is unit-testable without a running app or API client.
    static func buildFields(from server: ImportPreviewServer) -> [SecretFieldInput] {
        let envFields = (server.env ?? []).map {
            SecretFieldInput(name: $0.name, kind: .env, value: "", mode: $0.secretLike ? .secret : .value)
        }
        let headerFields = (server.headers ?? []).map {
            SecretFieldInput(name: $0.name, kind: .header, value: "", mode: $0.secretLike ? .secret : .value)
        }
        return envFields + headerFields
    }

    // MARK: - Add

    private func handleAdd() async {
        guard let client = apiClient, let preview else { return }
        adding = true
        addError = nil
        // Populated only once SecretFieldResolver.resolve has returned
        // successfully, so the catch block below never double-rolls-back
        // refs it already rolled back itself on a write failure.
        var writtenRefs: [String] = []
        do {
            let resolved = try await SecretFieldResolver.resolve(client: client, serverName: preview.name, fields: fields)
            writtenRefs = resolved.writtenRefs

            // Apply against the ORIGINAL raw content, never against
            // `preview.url`/`.command`/`.args` — see `applyImportContent`'s
            // doc comment for why (F-A/F-D, review round 4).
            let applied = try await client.applyImportContent(
                previewRawContent,
                serverName: preview.name,
                envOverride: resolved.env,
                headerOverride: resolved.headers
            )
            guard !applied.imported.isEmpty else {
                throw APIClientError.httpError(statusCode: 400, message: "Failed to add server")
            }
            onAdded(preview.name)
        } catch {
            if !writtenRefs.isEmpty {
                await SecretFieldResolver.rollback(client: client, refs: writtenRefs)
            }
            addError = error.localizedDescription
        }
        adding = false
    }
}
