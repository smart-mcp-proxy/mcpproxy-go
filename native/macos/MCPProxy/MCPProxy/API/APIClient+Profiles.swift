// APIClient+Profiles.swift
// MCPProxy
//
// Spec 108-k (K1, K2): the Profiles v3 REST surface. Every method here throws
// `APIClientError.service(status:body:)` on a non-2xx answer, carrying the full
// structured body (`code`, `field`, `used_by`, `bindings`, `fixes`, …) that the
// older `performRequest` path reduces to a message string. Method and path names
// follow contracts/rest-api.md and 108-f §2.
//
// Nothing here ever logs a body or a URL: several responses carry a one-time
// credential.

import Foundation

/// The binding intent of a connect (`profile`, `mode`, `keyless`).
///
/// `profile == nil` means "not specified": a fresh credential defaults to All
/// servers and a reconnect KEEPS its existing binding (never silently widens a
/// locked client). `profile == ""` is an explicit All servers.
struct ConnectBinding: Equatable {
    var profile: String?
    var mode: BindingMode?
    var keyless: Bool

    init(profile: String? = nil, mode: BindingMode? = nil, keyless: Bool = false) {
        self.profile = profile
        self.mode = mode
        self.keyless = keyless
    }

    /// Nothing specified: the legacy, parameter-free connect.
    static let unspecified = ConnectBinding()

    /// Query items for `GET /connect/{id}/preview`.
    var queryItems: [URLQueryItem] {
        var items: [URLQueryItem] = []
        if let profile { items.append(URLQueryItem(name: "profile", value: profile)) }
        if let mode { items.append(URLQueryItem(name: "mode", value: mode.wire)) }
        if keyless { items.append(URLQueryItem(name: "keyless", value: "true")) }
        return items
    }

    /// Body fields for `POST /connect/{id}`.
    var bodyFields: [String: Any] {
        var body: [String: Any] = [:]
        if let profile { body["profile"] = profile }
        if let mode { body["mode"] = mode.wire }
        if keyless { body["keyless"] = true }
        return body
    }
}

extension APIClient {

    // MARK: - Request plumbing

    /// Decode the structured body of a non-2xx answer.
    nonisolated static func serviceError(status: Int, data: Data) -> APIClientError {
        if let body = try? JSONDecoder().decode(ServiceErrorBody.self, from: data), !body.error.isEmpty {
            return .service(status: status, body: body)
        }
        return .service(
            status: status,
            body: ServiceErrorBody(error: HTTPURLResponse.localizedString(forStatusCode: status)))
    }

    /// One JSON request whose success body is the standard envelope.
    func serviceRequest<T: Decodable>(
        _ type: T.Type,
        path: String,
        method: String,
        body: Data? = nil,
        strictSocket: Bool = false
    ) async throws -> T {
        if strictSocket && transportKind != .unixSocket { throw APIClientError.socketRequired }
        let (data, response) = try await rawRequest(
            path: path, method: method, body: body, strictSocket: strictSocket)
        guard (200...299).contains(response.statusCode) else {
            throw Self.serviceError(status: response.statusCode, data: data)
        }
        let decoder = JSONDecoder()
        do {
            let wrapper = try decoder.decode(APIResponse<T>.self, from: data)
            if let payload = wrapper.data { return payload }
            throw APIClientError.decodingError(
                underlying: DecodingError.dataCorrupted(
                    .init(codingPath: [], debugDescription: wrapper.error ?? "Missing data")))
        } catch let error as APIClientError {
            throw error
        } catch {
            // A few routes answer a bare payload.
            if let bare = try? decoder.decode(T.self, from: data) { return bare }
            throw APIClientError.decodingError(underlying: error)
        }
    }

    private func jsonBody(_ object: [String: Any]) throws -> Data {
        try JSONSerialization.data(withJSONObject: object)
    }

    private func query(_ items: [(String, String?)]) -> String {
        let parts = items.compactMap { name, value -> String? in
            guard let value, !value.isEmpty else { return nil }
            return "\(name)=\(Self.escapeQueryValue(value))"
        }
        return parts.isEmpty ? "" : "?" + parts.joined(separator: "&")
    }

    // MARK: - Profiles

    /// `GET /api/v1/profiles` (the v3 list; `used_by` only for administrators).
    func profilesV3() async throws -> ProfilesListResponse {
        try await serviceRequest(ProfilesListResponse.self, path: "/api/v1/profiles", method: "GET")
    }

    /// `GET /api/v1/profiles/{name}`.
    func profile(_ name: String) async throws -> ProfileView {
        try await serviceRequest(
            ProfileView.self, path: "/api/v1/profiles/\(name.uriComponentEncoded)", method: "GET")
    }

    /// `POST /api/v1/profiles`.
    func createProfile(_ payload: ProfileConfigPayload) async throws -> ProfileWriteResponse {
        try await serviceRequest(
            ProfileWriteResponse.self, path: "/api/v1/profiles", method: "POST",
            body: try JSONEncoder().encode(payload))
    }

    /// `PUT /api/v1/profiles/{name}` — a full replace.
    func updateProfile(_ name: String, _ payload: ProfileConfigPayload) async throws -> ProfileWriteResponse {
        try await serviceRequest(
            ProfileWriteResponse.self, path: "/api/v1/profiles/\(name.uriComponentEncoded)",
            method: "PUT", body: try JSONEncoder().encode(payload))
    }

    /// `POST /api/v1/profiles/{name}/rename`.
    func renameProfile(_ name: String, newName: String) async throws -> ProfileRenameResponse {
        try await serviceRequest(
            ProfileRenameResponse.self,
            path: "/api/v1/profiles/\(name.uriComponentEncoded)/rename", method: "POST",
            body: try jsonBody(["new_name": newName]))
    }

    /// `DELETE /api/v1/profiles/{name}?reassign_to=&force=`.
    func deleteProfile(_ name: String, reassignTo: String? = nil, force: Bool = false) async throws -> ProfileDeleteResponse {
        let q = query([("reassign_to", reassignTo), ("force", force ? "true" : nil)])
        return try await serviceRequest(
            ProfileDeleteResponse.self, path: "/api/v1/profiles/\(name.uriComponentEncoded)\(q)",
            method: "DELETE")
    }

    /// `GET /api/v1/profiles/{name}/effective-tools?client=&server=&reason=`.
    func effectiveTools(
        _ name: String, client: String? = nil, server: String? = nil, reason: String? = nil
    ) async throws -> EffectiveToolsResponse {
        let q = query([("client", client), ("server", server), ("reason", reason)])
        return try await serviceRequest(
            EffectiveToolsResponse.self,
            path: "/api/v1/profiles/\(name.uriComponentEncoded)/effective-tools\(q)", method: "GET")
    }

    /// `POST /api/v1/profiles/try` with an UNSAVED draft; nothing is persisted.
    func tryProfile(draft: ProfileConfigPayload, query text: String, limit: Int? = nil) async throws -> TryResponse {
        var body: [String: Any] = ["profile": try draft.jsonObject(), "query": text]
        if let limit { body["limit"] = limit }
        return try await serviceRequest(
            TryResponse.self, path: "/api/v1/profiles/try", method: "POST", body: try jsonBody(body))
    }

    // MARK: - Clients

    /// `GET /api/v1/clients?profile=&client=` with the response-level warnings.
    func clientsV3(profile: String? = nil, client: String? = nil) async throws -> ClientsResponse {
        let q = query([("profile", profile), ("client", client)])
        return try await serviceRequest(ClientsResponse.self, path: "/api/v1/clients\(q)", method: "GET")
    }

    /// `PUT /api/v1/clients/{client}/binding`. `mode` is sent only when non-nil
    /// (omitted = keep the credential's current mode).
    func setBinding(_ clientId: String, profile: String, mode: BindingMode? = nil) async throws -> ClientBindingResponse {
        var body: [String: Any] = ["profile": profile]
        if let mode { body["mode"] = mode.wire }
        return try await serviceRequest(
            ClientBindingResponse.self,
            path: "/api/v1/clients/\(clientId.uriComponentEncoded)/binding", method: "PUT",
            body: try jsonBody(body))
    }

    /// `POST /api/v1/clients/bulk-assign`; `""` is All servers on either side.
    func bulkAssign(from: String, to: String, mode: BindingMode? = nil) async throws -> BulkAssignResponse {
        var body: [String: Any] = ["from_profile": from, "to_profile": to]
        if let mode { body["mode"] = mode.wire }
        return try await serviceRequest(
            BulkAssignResponse.self, path: "/api/v1/clients/bulk-assign", method: "POST",
            body: try jsonBody(body))
    }

    /// `POST /api/v1/clients` — a custom client. The response carries the full
    /// credential ONCE; the caller must not keep it anywhere but the sheet that
    /// shows it.
    func addCustomClient(
        id: String, displayName: String?, profile: String?, mode: BindingMode?, expiresIn: String?
    ) async throws -> CustomClientResponse {
        var body: [String: Any] = ["id": id]
        if let displayName, !displayName.isEmpty { body["display_name"] = displayName }
        if let profile, !profile.isEmpty { body["profile"] = profile }
        if let mode { body["mode"] = mode.wire }
        if let expiresIn, !expiresIn.isEmpty { body["expires_in"] = expiresIn }
        return try await serviceRequest(
            CustomClientResponse.self, path: "/api/v1/clients", method: "POST", body: try jsonBody(body))
    }

    /// `POST /api/v1/clients/{id}/rotate`. A supported client's rotation writes
    /// its config file, so it rides the private socket like every connect write.
    func rotateClient(_ id: String, preconditionToken: String? = nil) async throws -> RotateResponse {
        var body: [String: Any] = [:]
        if let preconditionToken, !preconditionToken.isEmpty { body["precondition_token"] = preconditionToken }
        return try await serviceRequest(
            RotateResponse.self, path: "/api/v1/clients/\(id.uriComponentEncoded)/rotate",
            method: "POST", body: try jsonBody(body), strictSocket: true)
    }

    /// `POST /api/v1/clients/{id}/rotate/finalize` (idempotent).
    func finalizeRotation(_ id: String) async throws -> FinalizeRotationResponse {
        try await serviceRequest(
            FinalizeRotationResponse.self,
            path: "/api/v1/clients/\(id.uriComponentEncoded)/rotate/finalize", method: "POST",
            body: try jsonBody([:]))
    }

    /// `DELETE /api/v1/clients/{id}?disconnect=true`.
    func forgetClient(_ id: String, disconnect: Bool) async throws -> ForgetClientResponse {
        let q = query([("disconnect", disconnect ? "true" : nil)])
        return try await serviceRequest(
            ForgetClientResponse.self, path: "/api/v1/clients/\(id.uriComponentEncoded)\(q)",
            method: "DELETE")
    }

    /// `POST /api/v1/clients/upgrade-admin-key-holders` without `apply`: the
    /// combined preview. Writes nothing.
    func previewAdminKeyUpgrade(profile: String? = nil, mode: BindingMode? = nil) async throws -> UpgradePreview {
        try await serviceRequest(
            UpgradePreview.self, path: "/api/v1/clients/upgrade-admin-key-holders", method: "POST",
            body: try jsonBody(upgradeBody(profile: profile, mode: mode, apply: false, token: nil)))
    }

    /// The same route with `apply: true` and the preview's combined token.
    func applyAdminKeyUpgrade(
        profile: String? = nil, mode: BindingMode? = nil, preconditionToken: String?
    ) async throws -> UpgradeResult {
        try await serviceRequest(
            UpgradeResult.self, path: "/api/v1/clients/upgrade-admin-key-holders", method: "POST",
            body: try jsonBody(upgradeBody(profile: profile, mode: mode, apply: true, token: preconditionToken)),
            strictSocket: true)
    }

    private func upgradeBody(profile: String?, mode: BindingMode?, apply: Bool, token: String?) -> [String: Any] {
        var body: [String: Any] = [:]
        if let profile { body["profile"] = profile }
        if let mode { body["mode"] = mode.wire }
        if apply { body["apply"] = true }
        if let token, !token.isEmpty { body["precondition_token"] = token }
        return body
    }

    // MARK: - Access explanation

    /// The one subject of an access explanation (exactly one is sent).
    enum ExplainSubjectQuery: Equatable {
        case client(String)
        case token(String)
        case profile(String)
        case anonymous

        var queryItem: (String, String) {
            switch self {
            case .client(let id): return ("client", id)
            case .token(let name): return ("token", name)
            case .profile(let name): return ("profile", name)
            case .anonymous: return ("anonymous", "true")
            }
        }
    }

    /// `GET /api/v1/access/explain?tool=<server:tool>&<subject>`.
    func explain(tool: String, subject: ExplainSubjectQuery) async throws -> AccessExplanation {
        let (name, value) = subject.queryItem
        let q = query([("tool", tool), (name, value)])
        return try await serviceRequest(AccessExplanation.self, path: "/api/v1/access/explain\(q)", method: "GET")
    }

    // MARK: - Scoped reads (view-as and filters)

    /// `GET /api/v1/tools` — the catalog, optionally "viewed as" a client or a
    /// profile (rows gain `access` and `profile_tier`; a non-admin view-as also
    /// returns `counts`).
    func viewAsTools(client: String? = nil, profile: String? = nil) async throws -> SearchToolsResponse {
        let q = query([("client", client), ("profile", profile)])
        return try await serviceRequest(SearchToolsResponse.self, path: "/api/v1/tools\(q)", method: "GET")
    }

    /// `GET /api/v1/servers?profile=`.
    func servers(profile: String?) async throws -> [ServerStatus] {
        let q = query([("profile", profile)])
        let response: ServersListResponse = try await serviceRequest(
            ServersListResponse.self, path: "/api/v1/servers\(q)", method: "GET")
        return response.servers
    }

    /// `GET /api/v1/tokens?profile=&token=` (`profile=-` is "unpinned").
    func tokens(profile: String? = nil, token: String? = nil) async throws -> [AgentToken] {
        let q = query([("profile", profile), ("token", token)])
        let response: TokensListResponse = try await serviceRequest(
            TokensListResponse.self, path: "/api/v1/tokens\(q)", method: "GET")
        return response.tokens
    }

    /// `POST /api/v1/tokens`. The 400 `field:"name"` of a reserved name surfaces
    /// as `.service` for inline display.
    func createToken(body: [String: Any]) async throws -> String {
        let (data, response) = try await rawRequest(
            path: "/api/v1/tokens", method: "POST", body: try jsonBody(body))
        guard (200...299).contains(response.statusCode) else {
            throw Self.serviceError(status: response.statusCode, data: data)
        }
        guard let root = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw APIClientError.noData
        }
        if let inner = root["data"] as? [String: Any], let secret = inner["token"] as? String { return secret }
        if let secret = root["token"] as? String { return secret }
        throw APIClientError.noData
    }

    /// A scope-aware GET (Spec 109-k `ScopeRequest`): the path plus the encoded
    /// query the filter maps to, with `extra` appended (e.g. `limit=20`).
    func fetchScoped<T: Decodable>(_ request: ScopeRequest, extra: String? = nil) async throws -> T {
        var parts: [String] = []
        if !request.queryString.isEmpty { parts.append(request.queryString) }
        if let extra { parts.append(extra) }
        let path = request.path + (parts.isEmpty ? "" : "?" + parts.joined(separator: "&"))
        return try await fetchWrapped(path: path)
    }

    // MARK: - Connect with a binding (T042m)

    /// `GET /api/v1/connect/{id}/preview?server_name=…&profile=&mode=&keyless=`.
    func connectPreview(
        _ clientId: String, serverName: String, binding: ConnectBinding
    ) async throws -> ConnectPreviewModel {
        var items = [URLQueryItem(name: "server_name", value: serverName)] + binding.queryItems
        items.sort { $0.name < $1.name }
        let q = items.map { "\($0.name)=\(Self.escapeQueryValue($0.value ?? ""))" }.joined(separator: "&")
        let path = "/api/v1/connect/\(clientId.uriComponentEncoded)/preview?\(q)"
        let (data, response) = try await rawRequest(path: path, method: "GET")
        guard (200...299).contains(response.statusCode) else {
            throw Self.serviceError(status: response.statusCode, data: data)
        }
        return try decodePayload(ConnectPreviewModel.self, from: data)
    }

    /// `POST /api/v1/connect/{id}` with `profile`/`mode`/`keyless`, bound to the
    /// preview's precondition token. A 409 discriminated by `action` stays the
    /// typed `.connectConflict`; every other refusal is `.service` (a guard's
    /// `bindings`/`fixes`, a `conflicting_token`).
    func connect(
        _ clientId: String,
        serverName: String,
        force: Bool,
        preconditionToken: String?,
        binding: ConnectBinding
    ) async throws -> ConnectResult {
        guard transportKind == .unixSocket else { throw APIClientError.socketRequired }
        var body: [String: Any] = ["server_name": serverName]
        if force { body["force"] = true }
        if let preconditionToken, !preconditionToken.isEmpty { body["precondition_token"] = preconditionToken }
        for (key, value) in binding.bodyFields { body[key] = value }
        let (data, response) = try await rawRequest(
            path: "/api/v1/connect/\(clientId.uriComponentEncoded)", method: "POST",
            body: try jsonBody(body), strictSocket: true)
        if (200...299).contains(response.statusCode) {
            return try decodePayload(ConnectResult.self, from: data)
        }
        if response.statusCode == 409 {
            let typed = (try? JSONDecoder().decode(APIResponse<ConnectResult>.self, from: data))?.data
                ?? (try? JSONDecoder().decode(ConnectResult.self, from: data))
            if let action = typed?.action, !action.isEmpty {
                throw APIClientError.connectConflict(
                    action: action,
                    message: typed?.message ?? "The client configuration changed.",
                    displayPath: typed?.displayPath,
                    reloadHint: typed?.reloadHint)
            }
        }
        throw Self.serviceError(status: response.statusCode, data: data)
    }

    private func decodePayload<T: Decodable>(_ type: T.Type, from data: Data) throws -> T {
        let decoder = JSONDecoder()
        if let wrapper = try? decoder.decode(APIResponse<T>.self, from: data), let payload = wrapper.data {
            return payload
        }
        do {
            return try decoder.decode(T.self, from: data)
        } catch {
            throw APIClientError.decodingError(underlying: error)
        }
    }
}
