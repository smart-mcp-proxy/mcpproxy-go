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
