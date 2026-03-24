// TokensView.swift
// MCPProxy
//
// Displays agent tokens with name, creation date, permissions, and
// server restrictions. Supports creating and revoking tokens.
// Uses the /api/v1/tokens REST API endpoints.
//
// Reads apiClient from appState instead of taking it as a parameter.

import SwiftUI

// MARK: - Token Model

/// Represents an agent token as returned by the tokens API.
/// Minimal model for display purposes -- the full token secret is only
/// shown once at creation time.
struct AgentToken: Codable, Identifiable, Equatable {
    let id: String
    let name: String
    let createdAt: String
    let lastUsedAt: String?
    let servers: [String]?
    let permissions: [String]?
    let expiresAt: String?

    enum CodingKeys: String, CodingKey {
        case id, name, servers, permissions
        case createdAt = "created_at"
        case lastUsedAt = "last_used_at"
        case expiresAt = "expires_at"
    }
}

/// Response wrapper for the tokens list endpoint.
struct TokensListResponse: Codable {
    let tokens: [AgentToken]
}

// MARK: - Tokens View

struct TokensView: View {
    @ObservedObject var appState: AppState
    @State private var tokens: [AgentToken] = []
    @State private var isLoading = false
    @State private var errorMessage: String?
    @State private var showCreateSheet = false
    @State private var selectedTokenID: String?

    private var apiClient: APIClient? { appState.apiClient }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            // Header
            HStack {
                Text("Agent Tokens")
                    .font(.title2.bold())
                Spacer()
                if isLoading {
                    ProgressView()
                        .controlSize(.small)
                }
                Button {
                    Task { await loadTokens() }
                } label: {
                    Image(systemName: "arrow.clockwise")
                }
                .buttonStyle(.borderless)
                .help("Refresh tokens")

                Button("Create Token") {
                    showCreateSheet = true
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
            }
            .padding()

            Divider()

            if let error = errorMessage {
                errorBanner(error)
            }

            if tokens.isEmpty && !isLoading {
                emptyState
            } else {
                tokenList
            }
        }
        .task { await loadTokens() }
        .sheet(isPresented: $showCreateSheet) {
            CreateTokenSheet(appState: appState) { _ in
                Task { await loadTokens() }
            }
        }
    }

    // MARK: - Subviews

    @ViewBuilder
    private var emptyState: some View {
        VStack(spacing: 12) {
            Image(systemName: "person.badge.key")
                .font(.system(size: 48))
                .foregroundStyle(.tertiary)
            Text("No agent tokens")
                .font(.title3)
                .foregroundStyle(.secondary)
            Text("Create tokens to allow AI agents to authenticate with MCPProxy")
                .font(.caption)
                .foregroundStyle(.tertiary)
                .multilineTextAlignment(.center)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    @ViewBuilder
    private var tokenList: some View {
        List(tokens, selection: $selectedTokenID) { token in
            TokenRow(token: token, onRevoke: {
                Task { await revokeToken(token.name) }
            })
            .tag(token.id)
        }
    }

    @ViewBuilder
    private func errorBanner(_ message: String) -> some View {
        HStack {
            Image(systemName: "exclamationmark.triangle.fill")
                .foregroundStyle(.orange)
            Text(message)
                .font(.caption)
                .foregroundStyle(.secondary)
            Spacer()
            Button("Dismiss") { errorMessage = nil }
                .buttonStyle(.borderless)
                .font(.caption)
        }
        .padding(.horizontal)
        .padding(.vertical, 6)
        .background(Color.orange.opacity(0.1))
    }

    // MARK: - Data Loading

    private func loadTokens() async {
        guard let client = apiClient else {
            errorMessage = "Not connected to MCPProxy core"
            return
        }
        isLoading = true
        errorMessage = nil
        defer { isLoading = false }

        do {
            let data = try await client.fetchRaw(path: "/api/v1/tokens")
            let decoder = JSONDecoder()
            // Try wrapped response first
            if let wrapped = try? decoder.decode(APIResponse<TokensListResponse>.self, from: data),
               wrapped.success, let payload = wrapped.data {
                tokens = payload.tokens
            } else if let direct = try? decoder.decode(TokensListResponse.self, from: data) {
                tokens = direct.tokens
            } else {
                tokens = []
            }
        } catch {
            errorMessage = "Failed to load tokens: \(error.localizedDescription)"
        }
    }

    private func revokeToken(_ name: String) async {
        guard let client = apiClient else { return }
        do {
            try await client.deleteAction(path: "/api/v1/tokens/\(name)")
            await loadTokens()
        } catch {
            errorMessage = "Failed to revoke token: \(error.localizedDescription)"
        }
    }
}

// MARK: - Token Row

struct TokenRow: View {
    let token: AgentToken
    let onRevoke: () -> Void

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: "key.fill")
                .foregroundStyle(.blue)
                .frame(width: 20)

            VStack(alignment: .leading, spacing: 2) {
                Text(token.name)
                    .font(.headline)

                HStack(spacing: 8) {
                    Text("Created: \(formattedDate(token.createdAt))")
                        .font(.caption)
                        .foregroundStyle(.secondary)

                    if let lastUsed = token.lastUsedAt {
                        Text("Last used: \(formattedDate(lastUsed))")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }

                // Permissions and servers
                HStack(spacing: 8) {
                    if let permissions = token.permissions, !permissions.isEmpty {
                        Text(permissions.joined(separator: ", "))
                            .font(.caption2)
                            .foregroundStyle(.blue)
                            .padding(.horizontal, 6)
                            .padding(.vertical, 1)
                            .background(.blue.opacity(0.1))
                            .cornerRadius(3)
                    }

                    if let servers = token.servers, !servers.isEmpty {
                        Text(servers.joined(separator: ", "))
                            .font(.caption2)
                            .foregroundStyle(.purple)
                            .padding(.horizontal, 6)
                            .padding(.vertical, 1)
                            .background(.purple.opacity(0.1))
                            .cornerRadius(3)
                    }
                }
            }

            Spacer()

            // Expiry indicator
            if let expires = token.expiresAt {
                Text("Expires: \(formattedDate(expires))")
                    .font(.caption)
                    .foregroundStyle(.tertiary)
            }

            Button(role: .destructive) {
                onRevoke()
            } label: {
                Image(systemName: "trash")
            }
            .buttonStyle(.borderless)
            .help("Revoke this token")
        }
        .padding(.vertical, 4)
    }

    private func formattedDate(_ isoString: String) -> String {
        let isoFormatter = ISO8601DateFormatter()
        isoFormatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        var date = isoFormatter.date(from: isoString)
        if date == nil {
            isoFormatter.formatOptions = [.withInternetDateTime]
            date = isoFormatter.date(from: isoString)
        }
        guard let d = date else { return isoString }
        let displayFormatter = DateFormatter()
        displayFormatter.dateStyle = .medium
        displayFormatter.timeStyle = .short
        return displayFormatter.string(from: d)
    }
}

// MARK: - Create Token Sheet

struct CreateTokenSheet: View {
    @ObservedObject var appState: AppState
    let onCreated: (String) -> Void

    private var apiClient: APIClient? { appState.apiClient }

    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var serversText = ""
    @State private var permissionsText = "read,write"
    @State private var isCreating = false
    @State private var createdTokenSecret: String?
    @State private var errorMessage: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Create Agent Token")
                .font(.title2.bold())

            if let secret = createdTokenSecret {
                // Show the created token secret (one-time display)
                tokenCreatedView(secret: secret)
            } else {
                tokenForm
            }
        }
        .padding(24)
        .frame(width: 450)
    }

    @ViewBuilder
    private var tokenForm: some View {
        VStack(alignment: .leading, spacing: 12) {
            VStack(alignment: .leading, spacing: 4) {
                Text("Token Name")
                    .font(.subheadline.bold())
                TextField("e.g., deploy-bot", text: $name)
                    .textFieldStyle(.roundedBorder)
            }

            VStack(alignment: .leading, spacing: 4) {
                Text("Servers (comma-separated, empty for all)")
                    .font(.subheadline.bold())
                TextField("e.g., github,gitlab", text: $serversText)
                    .textFieldStyle(.roundedBorder)
            }

            VStack(alignment: .leading, spacing: 4) {
                Text("Permissions (comma-separated)")
                    .font(.subheadline.bold())
                TextField("e.g., read,write", text: $permissionsText)
                    .textFieldStyle(.roundedBorder)
            }

            if let error = errorMessage {
                Text(error)
                    .font(.caption)
                    .foregroundStyle(.red)
            }

            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)

                Button("Create") {
                    Task { await createToken() }
                }
                .buttonStyle(.borderedProminent)
                .disabled(name.trimmingCharacters(in: .whitespaces).isEmpty || isCreating)
                .keyboardShortcut(.defaultAction)
            }
        }
    }

    @ViewBuilder
    private func tokenCreatedView(secret: String) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            Label("Token created successfully", systemImage: "checkmark.circle.fill")
                .foregroundStyle(.green)
                .font(.headline)

            Text("Copy this token now. It will not be shown again.")
                .font(.subheadline)
                .foregroundStyle(.secondary)

            HStack {
                Text(secret)
                    .font(.system(.body, design: .monospaced))
                    .textSelection(.enabled)
                    .padding(8)
                    .background(.quaternary)
                    .cornerRadius(6)

                Button {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(secret, forType: .string)
                } label: {
                    Image(systemName: "doc.on.doc")
                }
                .buttonStyle(.borderless)
                .help("Copy to clipboard")
            }

            HStack {
                Spacer()
                Button("Done") {
                    onCreated(name)
                    dismiss()
                }
                .buttonStyle(.borderedProminent)
                .keyboardShortcut(.defaultAction)
            }
        }
    }

    // MARK: - API Call

    private func createToken() async {
        guard let client = apiClient else {
            errorMessage = "Not connected to MCPProxy core"
            return
        }
        isCreating = true
        errorMessage = nil
        defer { isCreating = false }

        let servers = serversText
            .split(separator: ",")
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }

        let permissions = permissionsText
            .split(separator: ",")
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }

        var body: [String: Any] = ["name": name.trimmingCharacters(in: .whitespaces)]
        if !servers.isEmpty { body["servers"] = servers }
        if !permissions.isEmpty { body["permissions"] = permissions }

        do {
            let data = try await client.postRaw(path: "/api/v1/tokens", body: body)
            // Extract the token secret from the response
            if let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let dataObj = json["data"] as? [String: Any],
               let token = dataObj["token"] as? String {
                createdTokenSecret = token
            } else if let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                      let token = json["token"] as? String {
                createdTokenSecret = token
            } else {
                errorMessage = "Token created but could not read the secret from response"
            }
        } catch {
            errorMessage = "Failed to create token: \(error.localizedDescription)"
        }
    }
}
