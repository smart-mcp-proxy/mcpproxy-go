// TokenCreateModel.swift
// MCPProxy
//
// Spec 108-k (K16, FR-049): the logic of the Create Token sheet and of the
// token list rows. A token is now scoped by a PROFILE (pick one, choose an
// expiry); the old per-token server and permission fields live under a
// "Legacy scope (advanced)" disclosure that is enabled only when no profile is
// chosen. The body is `{name, profile, expires_in}` or the legacy
// `{name, allowed_servers, permissions, expires_in}`.

import Foundation

/// The narrow API surface of the sheet; `APIClient` conforms.
protocol TokenCreateSource: Sendable {
    func createToken(body: [String: Any]) async throws -> String
}

extension APIClient: TokenCreateSource {}

@MainActor
final class TokenCreateModel: ObservableObject {
    /// The profile Picker's sentinels.
    static let choose = ""
    static let legacy = "__legacy__"

    static let expiryChoices: [(label: String, value: String)] = [
        ("7 days", "7d"), ("30 days", "30d"), ("90 days", "90d"), ("365 days", "365d"),
    ]
    /// Matches the CLI default.
    static let defaultExpiry = "30d"

    @Published var name: String
    /// A profile name, `choose` (nothing yet) or `legacy`.
    @Published var profile: String
    @Published var expiresIn = TokenCreateModel.defaultExpiry
    @Published var legacyServers = ""
    @Published var legacyPermissions = "read,write"

    @Published private(set) var nameError: String?
    @Published private(set) var errorMessage: String?
    @Published private(set) var isCreating = false
    /// The token secret, shown once after creation.
    @Published private(set) var secret: String?

    private let source: TokenCreateSource

    init(source: TokenCreateSource, presetProfile: String? = nil, presetName: String = "") {
        self.source = source
        self.name = presetName
        self.profile = presetProfile ?? Self.choose
    }

    var isLegacy: Bool { profile == Self.legacy }

    /// Create needs a name and a choice: a profile, or legacy on purpose.
    var canCreate: Bool {
        !name.trimmingCharacters(in: .whitespaces).isEmpty && profile != Self.choose && !isCreating
    }

    /// The exact JSON body. A profile token carries NO `allowed_servers` or
    /// `permissions`: its scope comes from the profile.
    func requestBody() -> [String: Any] {
        var body: [String: Any] = ["name": name.trimmingCharacters(in: .whitespaces), "expires_in": expiresIn]
        if isLegacy {
            let servers = Self.list(legacyServers)
            let permissions = Self.list(legacyPermissions)
            if !servers.isEmpty { body["allowed_servers"] = servers }
            if !permissions.isEmpty { body["permissions"] = permissions }
        } else if profile != Self.choose {
            body["profile"] = profile
        }
        return body
    }

    private static func list(_ text: String) -> [String] {
        text.split(separator: ",").map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
    }

    func create() async {
        guard canCreate else { return }
        isCreating = true
        nameError = nil
        errorMessage = nil
        defer { isCreating = false }
        do {
            secret = try await source.createToken(body: requestBody())
        } catch {
            if case APIClientError.service(_, let body) = error {
                if body.field == "name" { nameError = body.error } else { errorMessage = body.error }
            } else {
                errorMessage = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
            }
        }
    }

    /// Wipe the secret when the sheet closes.
    func dismiss() { secret = nil }
}

// MARK: - Token rows

/// How one token row reads (K16): a client credential is managed under Clients
/// (no Revoke here); a legacy-scope token shows its servers and permissions
/// read-only with a hint to migrate.
struct TokenRowPresentation: Equatable {
    let isClientCredential: Bool
    let isLegacyScope: Bool
    /// `Client credential for cursor — manage in Clients` for a client row.
    let kindLabel: String
    let profileChip: String?
    let modeLabel: String?
    let canRevoke: Bool
    let migrateHint: String?

    static let migrateText = "Migrate to a profile"

    init(_ token: AgentToken) {
        isClientCredential = token.kind == "client"
        isLegacyScope = token.legacyScope == true && !isClientCredential
        if isClientCredential {
            kindLabel = "Client credential for \(token.clientId ?? token.name) — manage in Clients"
        } else {
            kindLabel = "Agent token"
        }
        profileChip = (token.profilePin?.isEmpty == false) ? token.profilePin : nil
        switch token.profileMode {
        case .some(.locked): modeLabel = "Locked"
        case .some(.switchable): modeLabel = "Switchable"
        default: modeLabel = nil
        }
        canRevoke = !isClientCredential
        migrateHint = isLegacyScope ? Self.migrateText : nil
    }
}

/// The profile filter of the tokens list as query values: "" All, "-" unpinned.
enum TokenProfileFilter {
    static let all = ""
    static let unpinned = "-"
}
