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
