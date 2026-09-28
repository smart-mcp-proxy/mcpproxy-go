import Foundation

// MARK: - Catalog (Spec 109 FR-060/061)
//
// macOS-tray mirror of `GET /api/v1/catalog/search` — a source-agnostic,
// ranked view over every enabled registry ("catalog source"), fanned out in
// parallel server-side (`registries.SearchAll`). This is a distinct DTO from
// `RepositoryServer` (RegistryModels.swift): the existing
// `GET /api/v1/registries/{id}/servers` and MCP `search_servers` keep
// serving that shape unchanged (contracts/rest-api.md#catalog).

/// A source's own popularity signal — stars for a GitHub-hosted server,
/// install/download counts otherwise. Either field may be absent.
struct CatalogPopularity: Codable, Equatable {
    let stars: Int?
    let installs: Int?
}

/// The install target: either a remote URL or a local command + args, never
/// both.
struct CatalogInstall: Codable, Equatable {
    let url: String?
    let command: String?
    let args: [String]?
}

/// One required input (FR-061/065). `secretLike` already folds in the D13
/// name heuristic server-side, so the toggle default needs no client-side
/// re-derivation.
struct CatalogInput: Codable, Equatable {
    let name: String
    let description: String?
    let secretLike: Bool

    enum CodingKeys: String, CodingKey {
        case name, description
        case secretLike = "secret_like"
    }
}

/// One `GET /api/v1/catalog/search` result. Mirrors `registries.CatalogResult`.
struct CatalogResult: Codable, Identifiable, Equatable {
    let source: String
    let id: String
    let title: String
    let publisher: String?
    let verified: Bool
    let official: Bool
    let popularity: CatalogPopularity?
    let description: String
    let transport: String
    let install: CatalogInstall
    let requiredInputs: [CatalogInput]?
    let sourceCodeURL: String?
    let added: Bool

    enum CodingKeys: String, CodingKey {
        case source, id, title, publisher, verified, official, popularity, description, transport, install, added
        case requiredInputs = "required_inputs"
        case sourceCodeURL = "source_code_url"
    }
}

/// A catalog source that failed or timed out. Mirrors `registries.SourceError`.
struct CatalogSourceError: Codable, Equatable {
    let source: String
    let reason: String
}

/// The empty-query landing sections (FR-060), each capped at 12 server-side.
struct CatalogSections: Codable, Equatable {
    let official: [CatalogResult]
    let popular: [CatalogResult]
}

/// Response of `GET /api/v1/catalog/search`. Mirrors the REST DTO
/// (contracts/rest-api.md#catalog): `sections` is `null` for a non-empty
/// query.
struct CatalogSearchResponse: Codable {
    let query: String
    let results: [CatalogResult]
    let sections: CatalogSections?
    let unavailable: [CatalogSourceError]?
}

// MARK: - Secret refs (Spec 109 FR-065)
//
// `GET /api/v1/secrets/refs` — every keyring/env secret reference currently
// configured, values always masked. Used by the Add Server sheet's secret
// toggle (Catalog + Paste tabs) as the "taken names" set so two fields that
// would compute the same `SecretRefName` collide into `-2`, `-3`, … instead
// of silently overwriting one another (D28), mirroring the Web UI's
// `resolveSecretFields` (frontend/src/composables/useSecretFields.ts).

/// One entry of `GET /api/v1/secrets/refs`. Mirrors the masked shape written
/// by `handleGetSecretRefs` (internal/httpapi/server.go): `type`, `name`,
/// `original` — never the actual value.
struct SecretRefEntry: Codable, Equatable {
    let type: String
    let name: String
    let original: String?
}

/// Response wrapper for `GET /api/v1/secrets/refs`.
struct SecretRefsResponse: Codable {
    let refs: [SecretRefEntry]
    let count: Int?
}

// MARK: - Import preview (Spec 109 FR-064), content-based
//
// `POST /api/v1/servers/import/json?preview=true` — the Paste tab's "detect
// before add" step. Distinct from `ImportResponse` (API/Models.swift), which
// models the simpler path-based `POST /api/v1/servers/import/path` summary
// the Import tab uses; this mirrors `httpapi.ImportedServerResponse`'s FR-064
// preview enrichment (`summary`, `tags`, `env`, `headers`) that the path-based
// response never carries.

/// One env-var or header field the import preview detected. Mirrors
/// `httpapi.EnvFieldPreview` / `HeaderFieldPreview`: `secret_like` already
/// folds in the D13 name heuristic, so the toggle default needs no
/// client-side re-derivation.
struct ImportPreviewField: Codable, Equatable {
    let name: String
    let secretLike: Bool
    let emptyOrPlaceholder: Bool

    enum CodingKeys: String, CodingKey {
        case name
        case secretLike = "secret_like"
        case emptyOrPlaceholder = "empty_or_placeholder"
    }
}

/// One detected server in an import preview. Mirrors
/// `httpapi.ImportedServerResponse` (only the fields the Paste tab needs).
struct ImportPreviewServer: Codable, Equatable {
    let name: String
    let `protocol`: String
    let url: String?
    let command: String?
    let args: [String]?
    let summary: String?
    let tags: [String]?
    let env: [ImportPreviewField]?
    let headers: [ImportPreviewField]?
}

/// Response of `POST /api/v1/servers/import/json` (preview or real). Mirrors
/// `httpapi.ImportResponse`; only `format` and `imported` are decoded here —
/// the Paste tab only ever previews (`imported.first`), never calls this
/// endpoint to perform the real add (it posts the resolved config to
/// `POST /api/v1/servers` instead, same as the Manual tab).
struct ImportPreviewResponse: Codable {
    let format: String?
    let imported: [ImportPreviewServer]
}
