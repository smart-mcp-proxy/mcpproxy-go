import Foundation

// MARK: - Registries (MCP-866 / MCP-902)
//
// macOS-tray mirror of the MCP-867 Web UI registry surface. Models the
// `GET /api/v1/registries` list (with provenance/trust) and the
// `POST /api/v1/registries` add-source flow (with stable error codes), plus the
// one-time third-party-registry warning acknowledgement.

/// Trust-tag constants mirroring `config.RegistryProvenance*` on the Go side
/// (and `REGISTRY_PROVENANCE_*` in the Web UI). Trust is derived server-side
/// from membership in the shipped default set — never self-asserted.
enum RegistryProvenance {
    static let official = "official/trusted"
    static let custom = "custom/unverified"
}

/// A registry as listed by `GET /api/v1/registries`. Mirrors `contracts.Registry`.
/// Unknown fields (`count`, `tags`) are intentionally ignored — the tray view
/// only needs identity, description, and provenance/trust.
struct Registry: Codable, Identifiable, Equatable {
    let id: String
    let name: String
    let description: String?
    let url: String?
    let serversURL: String?
    let `protocol`: String?
    /// "official/trusted" for built-in defaults, "custom/unverified" for
    /// user-added sources.
    let provenance: String?
    /// Convenience boolean mirror of `provenance == "official/trusted"`.
    let trusted: Bool?

    enum CodingKeys: String, CodingKey {
        case id, name, description, url
        case serversURL = "servers_url"
        case `protocol`
        case provenance, trusted
    }

    /// A registry is "custom/unverified" (third-party) when its provenance says
    /// so, or — defensively — when `trusted` is explicitly false. Anything else
    /// (including older payloads without the field) is treated as
    /// official/trusted. Mirrors the Web UI's `isCustomRegistry`.
    var isCustom: Bool {
        provenance == RegistryProvenance.custom || trusted == false
    }

    /// Resolve the full `Registry` that a search result's `registry` field
    /// refers to. That field may carry the registry id OR its display name (the
    /// backend search response uses the name — see `RepositoryServer.registry`),
    /// so match on id first, then fall back to a case-insensitive name match.
    /// Returns nil when nothing matches; the badge popup then shows the raw
    /// label only. Used by the macOS browse view (MCP-1050).
    static func lookup(_ nameOrID: String, in registries: [Registry]) -> Registry? {
        if let byID = registries.first(where: { $0.id == nameOrID }) { return byID }
        return registries.first { $0.name.caseInsensitiveCompare(nameOrID) == .orderedSame }
    }
}

/// Slim projection returned by `POST /api/v1/registries` (add-source).
/// Mirrors `contracts.RegistrySummary`.
struct RegistrySummary: Codable, Equatable {
    let id: String
    let name: String
    let url: String?
    let serversURL: String?
    let `protocol`: String?
    let provenance: String?
    let trusted: Bool?

    enum CodingKeys: String, CodingKey {
        case id, name, url
        case serversURL = "servers_url"
        case `protocol`
        case provenance, trusted
    }
}

/// Response wrapper for `GET /api/v1/registries`.
struct GetRegistriesResponse: Codable {
    let registries: [Registry]
    let total: Int?
}

/// `data` payload of a successful `POST /api/v1/registries`.
struct AddRegistrySourceData: Codable {
    let registry: RegistrySummary?
}

/// Structured error body of a failed `POST /api/v1/registries`, carrying the
/// stable cross-surface `code` (see `writeRegistryAddError` on the Go side).
struct RegistryAddErrorBody: Decodable {
    let error: String?
    let code: String?
}

/// Result of adding a *registry source*. Carries the stable error `code`
/// (`invalid_registry_url` | `registries_locked` | `registry_shadows_builtin` |
/// `duplicate_registry`) so the UI renders an actionable message instead of a
/// generic string. Mirrors the Web UI's `AddRegistrySourceResult`.
struct AddRegistrySourceResult: Equatable {
    let success: Bool
    let registry: RegistrySummary?
    let error: String?
    let code: String?

    static func ok(_ registry: RegistrySummary?) -> AddRegistrySourceResult {
        AddRegistrySourceResult(success: true, registry: registry, error: nil, code: nil)
    }

    static func failure(code: String?, error: String?) -> AddRegistrySourceResult {
        AddRegistrySourceResult(success: false, registry: nil, error: error, code: code)
    }

    /// Actionable message for this result, derived from its `code`.
    var userMessage: String {
        Self.message(code: code, fallback: error)
    }

    /// Map the backend's stable error code to an actionable message.
    /// Mirrors the Web UI's `addRegistryErrorMessage`.
    static func message(code: String?, fallback: String?) -> String {
        switch code {
        case "invalid_registry_url":
            return fallback ?? "That URL is not a valid HTTPS registry endpoint."
        case "registries_locked":
            return "Adding registries is locked by an administrator on this instance."
        case "registry_shadows_builtin":
            return "That id/host collides with a built-in registry. Try a different id."
        case "duplicate_registry":
            return "A registry with that id is already configured."
        default:
            return fallback ?? "Failed to add registry."
        }
    }
}

/// Persists the one-time acknowledgement of the third-party-registry warning
/// (MCP-867 parity). Backed by UserDefaults so the warning only shows until the
/// user acknowledges it once. `defaults` is injectable for testing.
struct ThirdPartyRegistryAck {
    /// Key mirrors the Web UI's localStorage key for cross-surface consistency.
    static let key = "mcpproxy-thirdparty-registry-ack"

    let defaults: UserDefaults

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
    }

    var hasAcknowledged: Bool {
        defaults.bool(forKey: Self.key)
    }

    func acknowledge() {
        defaults.set(true, forKey: Self.key)
    }
}

// MARK: - Server discovery / browse (macOS mirror of Web UI R1)
//
// Models `GET /api/v1/registries/{id}/servers` (per-registry search) and
// `POST /api/v1/registries/{id}/servers/{serverId}/add`. The browse view fans
// these out across several selected registries and merges the results.

/// A server returned by a registry search. Mirrors `contracts.RepositoryServer`
/// (only the fields the tray browse UI needs are decoded).
struct RepositoryServer: Codable, Identifiable, Equatable {
    let id: String
    let name: String
    let description: String?
    let url: String?
    let sourceCodeURL: String?
    let installCmd: String?
    let connectURL: String?
    /// Which registry this result came from (used for per-card attribution and
    /// as the registry id passed to the add endpoint).
    let registry: String?
    let requiredInputs: [RequiredInput]?

    enum CodingKeys: String, CodingKey {
        case id, name, description, url, registry
        case sourceCodeURL = "source_code_url"
        case installCmd = "install_cmd"
        case connectURL = "connect_url"
        case requiredInputs = "required_inputs"
    }

    struct RequiredInput: Codable, Equatable {
        let name: String
        let description: String?
        let secret: Bool?
    }

    /// Neutral transport label mirroring the Web UI's `serverTransport` (R2):
    /// remote / stdio:npm / stdio:python / stdio:docker / stdio.
    var transport: String {
        let cmd = (installCmd ?? "").trimmingCharacters(in: .whitespaces).lowercased()
        if !cmd.isEmpty {
            if cmd.hasPrefix("docker") { return "stdio:docker" }
            if cmd.hasPrefix("npx") || cmd.range(of: #"(^|\s)(npm|node)(\s|$)"#, options: .regularExpression) != nil { return "stdio:npm" }
            if cmd.hasPrefix("uvx") || cmd.hasPrefix("uv ") || cmd.range(of: #"(^|\s)(pipx?|python3?)(\s|$)"#, options: .regularExpression) != nil { return "stdio:python" }
            return "stdio"
        }
        if let url, !url.isEmpty { return "remote" }
        return "stdio"
    }
}

/// Per-registry "unavailable" marker (e.g. key required). Mirrors
/// `contracts.RegistryUnavailable`.
struct RegistryUnavailable: Codable, Equatable {
    let reason: String?
}

/// Response of `GET /api/v1/registries/{id}/servers`. Mirrors
/// `contracts.SearchRegistryServersResponse`.
struct SearchRegistryServersResponse: Codable {
    let registryID: String?
    let servers: [RepositoryServer]?
    let total: Int?
    let unavailable: RegistryUnavailable?

    enum CodingKeys: String, CodingKey {
        case registryID = "registry_id"
        case servers, total, unavailable
    }
}

/// Result of adding a server from a registry. Carries `missingInputs` when the
/// backend rejects with `missing_required_input` so the UI can tell the user
/// which env vars are needed (the full prompt flow is a follow-up).
struct AddServerResult: Equatable {
    let success: Bool
    let message: String?
    let missingInputs: [String]?

    static func ok() -> AddServerResult { AddServerResult(success: true, message: nil, missingInputs: nil) }
    static func failure(message: String?, missingInputs: [String]? = nil) -> AddServerResult {
        AddServerResult(success: false, message: message, missingInputs: missingInputs)
    }
}

/// Structured error body of a failed add-from-registry. Mirrors
/// `contracts.RegistryAddError`.
struct RegistryAddServerErrorBody: Decodable {
    let code: String?
    let message: String?
    let missingInputs: [String]?
    enum CodingKeys: String, CodingKey {
        case code, message
        case missingInputs = "missing_inputs"
    }
}

/// JS `encodeURIComponent` equivalent for safe path-segment encoding (the Web
/// UI uses encodeURIComponent on the registry id and server id).
extension String {
    var uriComponentEncoded: String {
        let allowed = CharacterSet(charactersIn:
            "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.!~*'()")
        return addingPercentEncoding(withAllowedCharacters: allowed) ?? self
    }
}
