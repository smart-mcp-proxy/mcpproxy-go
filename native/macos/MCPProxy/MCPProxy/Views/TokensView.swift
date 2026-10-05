// TokensView.swift
// MCPProxy
//
// Displays agent tokens with name, kind, profile, creation date and expiry.
// Supports creating and revoking tokens. Uses the /api/v1/tokens REST API.
//
// Spec 108-k (K16, FR-049): a token is scoped by a profile; legacy per-token
// server and permission scopes are shown read-only with a hint to migrate;
// client credentials are managed under Clients; the list filters by profile
// and by token name when the core advertises `features.scope_filters`.
//
// Reads apiClient from appState instead of taking it as a parameter.

import SwiftUI

// MARK: - Token Model

/// An agent token as `GET /api/v1/tokens` returns it (no secret: the full
/// secret is shown once at creation).
struct AgentToken: Codable, Identifiable, Equatable {
    let name: String
    let createdAt: String
    let lastUsedAt: String?
    /// The token's own server scope (`allowed_servers`); `["*"]` is unrestricted.
    let servers: [String]?
    let permissions: [String]?
    let expiresAt: String?
    let revoked: Bool?
    /// Spec 108: the pinned profile (`profile_pin`), empty for an unpinned token.
    let profilePin: String?
    /// `agent` (a regular token) or `client` (a per-client credential).
    let kind: String?
    let clientId: String?
    let profileMode: BindingMode?
    /// True when the token's scope is its own servers/permissions rather than a profile's.
    let legacyScope: Bool?

    var id: String { name }

    enum CodingKeys: String, CodingKey {
        case name, permissions, revoked, kind
        case createdAt = "created_at"
        case lastUsedAt = "last_used_at"
        case expiresAt = "expires_at"
        case servers = "allowed_servers"
        case profilePin = "profile_pin"
        case clientId = "client_id"
        case profileMode = "profile_mode"
        case legacyScope = "legacy_scope"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = try c.decode(String.self, forKey: .name)
        createdAt = try c.decodeIfPresent(String.self, forKey: .createdAt) ?? ""
        lastUsedAt = try c.decodeIfPresent(String.self, forKey: .lastUsedAt)
        servers = try c.decodeIfPresent([String].self, forKey: .servers)
        permissions = try c.decodeIfPresent([String].self, forKey: .permissions)
        expiresAt = try c.decodeIfPresent(String.self, forKey: .expiresAt)
        revoked = try c.decodeIfPresent(Bool.self, forKey: .revoked)
        profilePin = try c.decodeIfPresent(String.self, forKey: .profilePin)
        kind = try c.decodeIfPresent(String.self, forKey: .kind)
        clientId = try c.decodeIfPresent(String.self, forKey: .clientId)
        profileMode = try c.decodeIfPresent(BindingMode.self, forKey: .profileMode)
        legacyScope = try c.decodeIfPresent(Bool.self, forKey: .legacyScope)
    }
}

/// Response wrapper for the tokens list endpoint.
struct TokensListResponse: Codable {
    let tokens: [AgentToken]
}

// MARK: - Tokens View

struct TokensView: View {
    @ObservedObject var appState: AppState
    /// Optional one-shot request to open the create sheet (Spec 109-i toolbar
    /// "+ -> Token"). Reset to false once consumed. Nil for other call sites.
    var requestCreate: Binding<Bool>?
    /// The scope filter of this list (profile, token); owned by the Clients hub
    /// so a Profiles-card link can set it before this tab exists.
    @Binding var filter: ScopeFilter
    @Environment(\.fontScale) var fontScale
    @State private var tokens: [AgentToken] = []
    @State private var isLoading = false
    @State private var errorMessage: String?
    @State private var showCreateSheet = false
    @State private var selectedTokenID: String?
    @State private var migrating: AgentToken?
    @State private var reloadTask: Task<Void, Never>?
    /// Only the newest load (on the current connection) may publish.
    @State private var loadGeneration = 0

    private var apiClient: APIClient? { appState.apiClient }

    init(appState: AppState, requestCreate: Binding<Bool>? = nil, filter: Binding<ScopeFilter> = .constant(ScopeFilter())) {
        self.appState = appState
        self.requestCreate = requestCreate
        self._filter = filter
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            // Header
            HStack {
                Text("Agent Tokens")
                    .font(.scaled(.title2, scale: fontScale).bold())
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
                .accessibilityIdentifier("tokens-create")
            }
            .padding()

            if appState.scopeFiltersAvailable { filterBar }

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
        .onAppear { consumeCreateRequest() }
        .onChange(of: requestCreate?.wrappedValue ?? false) { _ in consumeCreateRequest() }
        .onChange(of: filter) { _ in scheduleReload() }
        .sheet(isPresented: $showCreateSheet) {
            CreateTokenSheet(appState: appState) { _ in
                Task { await loadTokens() }
            }
        }
        .sheet(item: $migrating) { token in
            CreateTokenSheet(appState: appState, presetName: "\(token.name)-profile") { _ in
                Task { await loadTokens() }
            }
        }
    }

    private func consumeCreateRequest() {
        guard requestCreate?.wrappedValue == true else { return }
        requestCreate?.wrappedValue = false
        showCreateSheet = true
    }

    private func scheduleReload() {
        reloadTask?.cancel()
        reloadTask = Task {
            await Task.yield()
            guard !Task.isCancelled else { return }
            await loadTokens()
        }
    }

    // MARK: - Filters

    /// Profile (All / Unpinned / each profile) and the exact token name; both
    /// are server-side (`GET /tokens?profile=&token=`).
    @ViewBuilder
    private var filterBar: some View {
        HStack(spacing: 10) {
            Picker("Profile", selection: Binding(
                get: { filter.profile ?? TokenProfileFilter.all },
                set: { filter.profile = $0.isEmpty ? nil : $0 })) {
                Text("All profiles").tag(TokenProfileFilter.all)
                Text("Unpinned").tag(TokenProfileFilter.unpinned)
                ForEach(appState.profiles) { Text($0.pickerTitle(in: appState.profiles)).tag($0.name) }
                if let current = filter.profile, current != TokenProfileFilter.unpinned,
                   !appState.profiles.contains(where: { $0.name == current }) {
                    Text(current).tag(current)
                }
            }
            .frame(width: 240)
            .accessibilityIdentifier("token-profile-picker")

            TextField("Token name", text: Binding(
                get: { filter.token ?? "" },
                set: { filter.token = $0.isEmpty ? nil : $0 }))
                .textFieldStyle(.roundedBorder).frame(width: 180)
                .accessibilityLabel("Filter by token name")
                .accessibilityIdentifier("token-name-filter")

            if let profile = filter.profile {
                chip(profile == TokenProfileFilter.unpinned ? "Unpinned" : "Profile: \(profile)") { filter.profile = nil }
            }
            if let token = filter.token {
                chip("Token: \(token)") { filter.token = nil }
            }
            Spacer()
        }
        .padding(.horizontal).padding(.bottom, 8)
    }

    private func chip(_ text: String, clear: @escaping () -> Void) -> some View {
        HStack(spacing: 4) {
            Text(text).font(.caption)
            Button(action: clear) { Image(systemName: "xmark.circle.fill") }
                .buttonStyle(.borderless)
                .accessibilityLabel("Clear filter \(text)")
        }
        .padding(.horizontal, 8).padding(.vertical, 3)
        .background(Color.accentColor.opacity(0.15)).clipShape(Capsule())
    }

    // MARK: - Subviews

    @ViewBuilder
    private var emptyState: some View {
        VStack(spacing: 12) {
            Image(systemName: "person.badge.key")
                .font(.system(size: 48 * fontScale))
                .foregroundStyle(.tertiary)
            Text("No agent tokens")
                .font(.scaled(.title3, scale: fontScale))
                .foregroundStyle(.secondary)
            Text("Create tokens to allow AI agents to authenticate with MCPProxy")
                .font(.scaled(.caption, scale: fontScale))
                .foregroundStyle(.tertiary)
                .multilineTextAlignment(.center)
            if appState.profiles.isEmpty {
                Text("Create a profile to scope tokens simply")
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.tertiary)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    @ViewBuilder
    private var tokenList: some View {
        List(tokens, selection: $selectedTokenID) { token in
            TokenRow(
                token: token,
                onRevoke: { Task { await revokeToken(token.name) } },
                onShowActivity: appState.scopeFiltersAvailable
                    ? { appState.openActivity(with: .forToken(token.name)) }
                    : nil,
                onMigrate: { migrating = token }
            )
            .tag(token.id)
        }
    }

    @ViewBuilder
    private func errorBanner(_ message: String) -> some View {
        HStack {
            Image(systemName: "exclamationmark.triangle.fill")
                .foregroundStyle(.orange)
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
        .background(Color.orange.opacity(0.1))
    }

    // MARK: - Data Loading

    private func loadTokens() async {
        guard let client = apiClient else {
            errorMessage = "Not connected to MCPProxy core"
            return
        }
        loadGeneration += 1
        let generation = loadGeneration
        let connection = appState.connectionGeneration
        isLoading = true
        errorMessage = nil
        defer { if generation == loadGeneration { isLoading = false } }

        // The scope parameters ride only when the core advertises them.
        let request = filter.restRequest(for: .tokens, scopeFiltersAvailable: appState.scopeFiltersAvailable)
        let profile = request?.query.first { $0.name == "profile" }?.value
        let token = request?.query.first { $0.name == "token" }?.value
        do {
            let fetched = try await client.tokens(profile: profile, token: token)
            guard generation == loadGeneration, appState.isCurrentConnection(connection) else { return }
            tokens = fetched
        } catch {
            guard generation == loadGeneration else { return }
            errorMessage = "Failed to load tokens: \(error.localizedDescription)"
        }
    }

    private func revokeToken(_ name: String) async {
        guard let client = apiClient else { return }
        do {
            try await client.deleteAction(path: "/api/v1/tokens/\(name.uriComponentEncoded)")
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
    /// Link-map "Token row → Activity" (Spec 109-k): nil hides it, which is
    /// the case until the core advertises `features.scope_filters`.
    var onShowActivity: (() -> Void)? = nil
    /// "Migrate to a profile" on a legacy-scope token (opens Create Token prefilled).
    var onMigrate: (() -> Void)? = nil
    @State private var showRevokeConfirmation = false
    @Environment(\.fontScale) var fontScale

    var body: some View {
        let presentation = TokenRowPresentation(token)
        HStack(spacing: 12) {
            Image(systemName: presentation.isClientCredential ? "person.badge.key.fill" : "key.fill")
                .foregroundStyle(presentation.isClientCredential ? Color.purple : Color.blue)
                .frame(width: 20)

            VStack(alignment: .leading, spacing: 2) {
                Text(token.name)
                    .font(.scaled(.headline, scale: fontScale))
                Text(presentation.kindLabel)
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.secondary)

                HStack(spacing: 8) {
                    Text("Created: \(formattedDate(token.createdAt))")
                        .font(.scaled(.caption, scale: fontScale))
                        .foregroundStyle(.secondary)

                    if let lastUsed = token.lastUsedAt {
                        Text("Last used: \(formattedDate(lastUsed))")
                            .font(.scaled(.caption, scale: fontScale))
                            .foregroundStyle(.secondary)
                    }
                }

                // Profile, mode and (legacy) scope
                HStack(spacing: 8) {
                    if presentation.isRevoked {
                        badge("Revoked", color: .red)
                            .accessibilityIdentifier("token-revoked-\(token.name)")
                    }
                    if let profile = presentation.profileChip {
                        badge("Profile: \(profile)", color: .green)
                            .accessibilityIdentifier("token-profile-chip-\(token.name)")
                    }
                    if let mode = presentation.modeLabel {
                        badge(mode, color: .gray)
                    }
                    if presentation.isLegacyScope {
                        badge("Legacy scope", color: .orange)
                        if let permissions = token.permissions, !permissions.isEmpty {
                            badge(permissions.joined(separator: ", "), color: .blue)
                        }
                        if let servers = token.servers, !servers.isEmpty {
                            badge(servers.joined(separator: ", "), color: .purple)
                        }
                        if let onMigrate, let hint = presentation.migrateHint {
                            Button(hint, action: onMigrate)
                                .buttonStyle(.link).font(.scaled(.caption2, scale: fontScale))
                                .accessibilityIdentifier("token-migrate-\(token.name)")
                        }
                    }
                }
            }

            Spacer()

            // Expiry indicator
            if let expires = token.expiresAt {
                Text("Expires: \(formattedDate(expires))")
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.tertiary)
            }

            if let onShowActivity {
                Button(action: onShowActivity) {
                    Image(systemName: "clock.arrow.circlepath")
                }
                .buttonStyle(.borderless)
                .help("Show this token's activity")
                .accessibilityIdentifier("token-row-activity-link")
            }

            if presentation.canRevoke {
                Button(role: .destructive) {
                    showRevokeConfirmation = true
                } label: {
                    Image(systemName: "trash")
                }
                .buttonStyle(.borderless)
                .help("Revoke this token")
                .accessibilityLabel("Revoke \(token.name)")
            }
        }
        .padding(.vertical, 4)
        .alert("Revoke Token", isPresented: $showRevokeConfirmation) {
            Button("Cancel", role: .cancel) { }
            Button("Revoke", role: .destructive) {
                onRevoke()
            }
        } message: {
            Text("Are you sure you want to revoke \"\(token.name)\"? This action cannot be undone.")
        }
        .accessibilityIdentifier("token-row-\(token.name)")
    }

    private func badge(_ text: String, color: Color) -> some View {
        Text(text)
            .font(.scaled(.caption2, scale: fontScale))
            .foregroundStyle(color)
            .padding(.horizontal, 6)
            .padding(.vertical, 1)
            .background(color.opacity(0.1))
            .cornerRadius(3)
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

    @Environment(\.dismiss) private var dismiss
    @Environment(\.fontScale) var fontScale
    @StateObject private var model: TokenCreateModel
    @State private var showLegacy = false

    init(appState: AppState, presetProfile: String? = nil, presetName: String = "", onCreated: @escaping (String) -> Void) {
        self.appState = appState
        self.onCreated = onCreated
        let source: TokenCreateSource = appState.apiClient ?? NoCoreTokenSource()
        _model = StateObject(wrappedValue: TokenCreateModel(source: source, presetProfile: presetProfile, presetName: presetName))
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Create Agent Token")
                .font(.scaled(.title2, scale: fontScale).bold())

            if let secret = model.secret {
                // Show the created token secret (one-time display)
                tokenCreatedView(secret: secret)
            } else {
                tokenForm
            }
        }
        .padding(24)
        .frame(width: 480)
        .onDisappear { model.dismiss() }
    }

    @ViewBuilder
    private var tokenForm: some View {
        VStack(alignment: .leading, spacing: 12) {
            VStack(alignment: .leading, spacing: 4) {
                Text("Token Name")
                    .font(.scaled(.subheadline, scale: fontScale).bold())
                TextField("e.g., deploy-bot", text: $model.name)
                    .textFieldStyle(.roundedBorder)
                    .accessibilityIdentifier("token-name")
                if let error = model.nameError {
                    Label(error, systemImage: "exclamationmark.circle.fill")
                        .font(.scaled(.caption, scale: fontScale)).foregroundStyle(.red)
                        .accessibilityIdentifier("token-name-error")
                }
            }

            VStack(alignment: .leading, spacing: 4) {
                Text("Profile")
                    .font(.scaled(.subheadline, scale: fontScale).bold())
                Picker("Profile", selection: $model.profile) {
                    Text("Choose…").tag(TokenCreateModel.choose)
                    ForEach(appState.profiles) { Text($0.pickerTitle(in: appState.profiles)).tag($0.name) }
                    Text("None — legacy scope").tag(TokenCreateModel.legacy)
                }
                .labelsHidden()
                .accessibilityLabel("Profile")
                .accessibilityIdentifier("token-profile-picker-create")
                if appState.profiles.isEmpty {
                    Text("Create a profile to scope tokens simply")
                        .font(.scaled(.caption, scale: fontScale)).foregroundStyle(.secondary)
                } else {
                    Text("The token can reach only what this profile allows.")
                        .font(.scaled(.caption, scale: fontScale)).foregroundStyle(.secondary)
                }
            }

            VStack(alignment: .leading, spacing: 4) {
                Text("Expires")
                    .font(.scaled(.subheadline, scale: fontScale).bold())
                Picker("Expires", selection: $model.expiresIn) {
                    ForEach(TokenCreateModel.expiryChoices, id: \.value) { Text($0.label).tag($0.value) }
                }
                .labelsHidden()
                .accessibilityLabel("Expiry")
                .accessibilityIdentifier("token-expiry")
            }

            DisclosureGroup("Legacy scope (advanced)", isExpanded: $showLegacy) {
                VStack(alignment: .leading, spacing: 8) {
                    if !model.isLegacy {
                        Text("Choose “None — legacy scope” as the profile to use these.")
                            .font(.scaled(.caption, scale: fontScale)).foregroundStyle(.secondary)
                    }
                    VStack(alignment: .leading, spacing: 4) {
                        Text("Servers (comma-separated, empty for all)")
                            .font(.scaled(.subheadline, scale: fontScale).bold())
                        TextField("e.g., github,gitlab", text: $model.legacyServers)
                            .textFieldStyle(.roundedBorder)
                    }
                    VStack(alignment: .leading, spacing: 4) {
                        Text("Permissions (comma-separated)")
                            .font(.scaled(.subheadline, scale: fontScale).bold())
                        TextField("e.g., read,write", text: $model.legacyPermissions)
                            .textFieldStyle(.roundedBorder)
                    }
                }
                .disabled(!model.isLegacy)
                .padding(.top, 4)
            }
            .onChange(of: model.profile) { newValue in
                if newValue == TokenCreateModel.legacy { showLegacy = true }
            }

            if let error = model.errorMessage {
                Text(error)
                    .font(.scaled(.caption, scale: fontScale))
                    .foregroundStyle(.red)
            }

            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)

                Button("Create") {
                    Task { await model.create() }
                }
                .buttonStyle(.borderedProminent)
                .disabled(!model.canCreate)
                .keyboardShortcut(.defaultAction)
                .accessibilityIdentifier("token-create-confirm")
            }
        }
    }

    @ViewBuilder
    private func tokenCreatedView(secret: String) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            Label("Token created successfully", systemImage: "checkmark.circle.fill")
                .foregroundStyle(.green)
                .font(.scaled(.headline, scale: fontScale))

            Text("Copy this token now. It will not be shown again.")
                .font(.scaled(.subheadline, scale: fontScale))
                .foregroundStyle(.secondary)

            HStack {
                Text(secret)
                    .font(.scaledMonospaced(.body, scale: fontScale))
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
                    onCreated(model.name)
                    model.dismiss()
                    dismiss()
                }
                .buttonStyle(.borderedProminent)
                .keyboardShortcut(.defaultAction)
            }
        }
    }
}

/// Stands in when no core connection exists yet.
private struct NoCoreTokenSource: TokenCreateSource {
    func createToken(body: [String: Any]) async throws -> String { throw APIClientError.notReady }
}
