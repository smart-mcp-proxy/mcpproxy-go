import Foundation

// MARK: - API Client Errors

/// Errors specific to the MCPProxy REST API client.
enum APIClientError: Error, LocalizedError {
    case notReady
    case httpError(statusCode: Int, message: String)
    case decodingError(underlying: Error)
    case noData
    case invalidURL(String)

    var errorDescription: String? {
        switch self {
        case .notReady:
            return "Core is not ready"
        case .httpError(let statusCode, let message):
            return "HTTP \(statusCode): \(message)"
        case .decodingError(let underlying):
            return "Decoding error: \(underlying.localizedDescription)"
        case .noData:
            return "No data in response"
        case .invalidURL(let url):
            return "Invalid URL: \(url)"
        }
    }
}

// MARK: - API Client

/// Async/await REST API client for the mcpproxy core server.
///
/// Uses Unix domain socket transport when available, falling back to TCP.
/// All methods throw `APIClientError` on failure.
actor APIClient {
    private let session: URLSession
    private let baseURL: String
    private let apiKey: String?

    /// Create an API client.
    ///
    /// - Parameters:
    ///   - socketPath: Path to the Unix socket, or `nil` to use the default.
    ///     Pass an empty string to force TCP-only mode.
    ///   - baseURL: TCP base URL. Used as fallback or when socket is unavailable.
    ///   - apiKey: Optional API key for authentication.
    init(socketPath: String? = nil, baseURL: String = "http://127.0.0.1:8080", apiKey: String? = nil) {
        self.baseURL = baseURL
        self.apiKey = apiKey

        // Unix socket is the default and preferred transport.
        // Only fall back to TCP if explicitly requested (empty socketPath string).
        // The SocketURLProtocol checks socket availability per-request,
        // so it's safe to register even before the socket file exists.
        if let path = socketPath, path.isEmpty {
            // Explicitly requested TCP-only
            self.session = SocketTransport.makeTCPSession()
        } else {
            // Always use socket-backed session — SocketURLProtocol falls through
            // to standard networking if the socket file doesn't exist yet.
            self.session = SocketTransport.makeURLSession(socketPath: socketPath)
        }
    }

    /// Create an API client with an explicit URLSession (for testing).
    init(session: URLSession, baseURL: String = "http://127.0.0.1:8080", apiKey: String? = nil) {
        self.session = session
        self.baseURL = baseURL
        self.apiKey = apiKey
    }

    // MARK: - Health

    /// Check if the core is ready to accept requests.
    /// Returns `true` if `/healthz/ready` returns 200.
    func ready() async throws -> Bool {
        let (_, response) = try await performRequest(path: "/ready", method: "GET")
        _ = response // suppress unused warning
        return true
    }

    /// Fetch the full status snapshot from `GET /api/v1/status`.
    func status() async throws -> StatusResponse {
        return try await fetchWrapped(path: "/api/v1/status")
    }

    /// Fetch server info from `GET /api/v1/info`.
    func info() async throws -> InfoResponse {
        return try await fetchWrapped(path: "/api/v1/info")
    }

    // MARK: - Docker & Diagnostics

    /// Docker status response from `GET /api/v1/docker/status`.
    struct DockerStatusResponse: Codable {
        let dockerAvailable: Bool
        let recoveryMode: Bool?
        enum CodingKeys: String, CodingKey {
            case dockerAvailable = "docker_available"
            case recoveryMode = "recovery_mode"
        }
    }

    /// Diagnostics response from `GET /api/v1/diagnostics`.
    struct DiagnosticsResponse: Codable {
        let dockerStatus: DockerStatusInfo?
        let quarantineEnabled: Bool?
        struct DockerStatusInfo: Codable {
            let available: Bool
        }
        enum CodingKeys: String, CodingKey {
            case dockerStatus = "docker_status"
            case quarantineEnabled = "quarantine_enabled"
        }
    }

    /// Fetch Docker availability from `GET /api/v1/docker/status`.
    func dockerStatus() async throws -> Bool {
        let response: DockerStatusResponse = try await fetchWrapped(path: "/api/v1/docker/status")
        return response.dockerAvailable
    }

    /// Fetch diagnostics (includes Docker + quarantine status).
    func diagnostics() async throws -> DiagnosticsResponse {
        return try await fetchWrapped(path: "/api/v1/diagnostics")
    }

    // MARK: - Servers

    /// List all upstream servers from `GET /api/v1/servers`.
    func servers() async throws -> [ServerStatus] {
        let response: ServersListResponse = try await fetchWrapped(path: "/api/v1/servers")
        return response.servers
    }

    /// Enable a server via `POST /api/v1/servers/{id}/enable`.
    func enableServer(_ id: String) async throws {
        try await postAction(path: "/api/v1/servers/\(id)/enable")
    }

    /// Disable a server via `POST /api/v1/servers/{id}/disable`.
    func disableServer(_ id: String) async throws {
        try await postAction(path: "/api/v1/servers/\(id)/disable")
    }

    /// Restart a server via `POST /api/v1/servers/{id}/restart`.
    func restartServer(_ id: String) async throws {
        try await postAction(path: "/api/v1/servers/\(id)/restart")
    }

    /// Trigger OAuth login for a server via `POST /api/v1/servers/{id}/login`.
    func loginServer(_ id: String) async throws {
        try await postAction(path: "/api/v1/servers/\(id)/login")
    }

    /// Quarantine a server via `POST /api/v1/servers/{id}/quarantine`.
    func quarantineServer(_ id: String) async throws {
        try await postAction(path: "/api/v1/servers/\(id)/quarantine")
    }

    /// Unquarantine a server via `POST /api/v1/servers/{id}/unquarantine`.
    func unquarantineServer(_ id: String) async throws {
        try await postAction(path: "/api/v1/servers/\(id)/unquarantine")
    }

    /// Approve all pending/changed tools for a server via `POST /api/v1/servers/{id}/tools/approve`.
    func approveTools(_ id: String) async throws {
        try await postAction(path: "/api/v1/servers/\(id)/tools/approve", body: ["approve_all": true])
    }

    /// Delete a server via `DELETE /api/v1/servers/{id}`.
    func deleteServer(_ id: String) async throws {
        try await deleteAction(path: "/api/v1/servers/\(id)")
    }

    /// Update a server via `PATCH /api/v1/servers/{name}`.
    func updateServer(_ name: String, updates: [String: Any]) async throws {
        let bodyData = try JSONSerialization.data(withJSONObject: updates)
        let (data, response) = try await performRequest(path: "/api/v1/servers/\(name)", method: "PATCH", body: bodyData)
        if let errorResponse = try? JSONDecoder().decode(APIErrorResponse.self, from: data),
           !errorResponse.success, let message = errorResponse.error {
            throw APIClientError.httpError(statusCode: response.statusCode, message: message)
        }
    }

    /// Store a value in the OS keyring under `name` and return the
    /// `${keyring:name}` reference string. Kept for callers that have
    /// the plaintext on the client side; the Headers / Environment
    /// Variables "Convert to secret" flow uses the atomic
    /// `convertConfigToSecret` below instead.
    func storeSecret(name: String, value: String) async throws -> String {
        _ = try await postAction(
            path: "/api/v1/secrets",
            body: ["name": name, "value": value, "type": "keyring"]
        )
        return "${keyring:\(name)}"
    }

    /// Atomically move a header / env value out of `mcp_config.json` and
    /// into the OS keyring. The backend reads the real value from the
    /// loaded config (so the client never has to possess it — useful
    /// when the API redacts what we see), stores it in keyring under
    /// `secretName`, and rewrites the config field with the
    /// `${keyring:<name>}` reference.
    func convertConfigToSecret(serverName: String, scope: String, key: String, secretName: String) async throws {
        _ = try await postAction(
            path: "/api/v1/servers/\(serverName)/config-to-secret",
            body: ["scope": scope, "key": key, "secret_name": secretName]
        )
    }

    // MARK: - Connect (Client Registration)

    /// Client status model returned by `GET /api/v1/connect`.
    struct ClientStatus: Codable, Identifiable {
        var id: String { clientId }
        let clientId: String
        let name: String
        let configPath: String
        let exists: Bool
        let connected: Bool
        let supported: Bool
        let reason: String?

        enum CodingKeys: String, CodingKey {
            case clientId = "id"
            case name
            case configPath = "config_path"
            case exists, connected, supported, reason
        }
    }

    /// Result of a connect/disconnect action.
    struct ConnectResult: Codable {
        let success: Bool
        let client: String?
        let configPath: String?
        let backupPath: String?
        let serverName: String?
        let action: String?
        let message: String?

        enum CodingKeys: String, CodingKey {
            case success, client, action, message
            case configPath = "config_path"
            case backupPath = "backup_path"
            case serverName = "server_name"
        }
    }

    /// Response wrapper for the client list endpoint.
    struct ClientListResponse: Codable {
        let clients: [ClientStatus]
    }

    /// Fetch all AI client statuses from `GET /api/v1/connect`.
    func connectClients() async throws -> [ClientStatus] {
        let data = try await fetchRaw(path: "/api/v1/connect")
        let decoder = JSONDecoder()
        // Try wrapped: {"success": true, "data": {"clients": [...]}}
        if let wrapper = try? decoder.decode(APIResponse<ClientListResponse>.self, from: data),
           let payload = wrapper.data {
            return payload.clients
        }
        // Try wrapped with direct array: {"success": true, "data": [...]}
        if let wrapper = try? decoder.decode(APIResponse<[ClientStatus]>.self, from: data),
           let payload = wrapper.data {
            return payload
        }
        // Try direct decode
        if let direct = try? decoder.decode(ClientListResponse.self, from: data) {
            return direct.clients
        }
        if let direct = try? decoder.decode([ClientStatus].self, from: data) {
            return direct
        }
        return []
    }

    /// Connect MCPProxy to a client via `POST /api/v1/connect/{clientId}`.
    func connectToClient(_ clientId: String) async throws -> ConnectResult {
        let data = try await postRaw(path: "/api/v1/connect/\(clientId)")
        let decoder = JSONDecoder()
        if let wrapper = try? decoder.decode(APIResponse<ConnectResult>.self, from: data),
           let payload = wrapper.data {
            return payload
        }
        return try decoder.decode(ConnectResult.self, from: data)
    }

    /// Disconnect MCPProxy from a client via `DELETE /api/v1/connect/{clientId}`.
    func disconnectFromClient(_ clientId: String) async throws -> ConnectResult {
        let data = try await deleteRaw(path: "/api/v1/connect/\(clientId)")
        let decoder = JSONDecoder()
        if let wrapper = try? decoder.decode(APIResponse<ConnectResult>.self, from: data),
           let payload = wrapper.data {
            return payload
        }
        return try decoder.decode(ConnectResult.self, from: data)
    }

    // MARK: - Sessions

    /// MCP session model from `GET /api/v1/sessions`.
    struct MCPSession: Codable, Identifiable {
        var id: String
        let clientName: String?
        let clientVersion: String?
        let status: String
        let hasRoots: Bool?
        let hasSampling: Bool?
        let toolCallCount: Int?
        let totalTokens: Int?
        let startTime: String?
        let lastActive: String?

        enum CodingKeys: String, CodingKey {
            case id
            case clientName = "client_name"
            case clientVersion = "client_version"
            case status
            case hasRoots = "has_roots"
            case hasSampling = "has_sampling"
            case toolCallCount = "tool_call_count"
            case totalTokens = "total_tokens"
            case startTime = "start_time"
            case lastActive = "last_active"
        }
    }

    /// Response wrapper for the sessions list endpoint.
    struct SessionsResponse: Codable {
        let sessions: [MCPSession]
        let total: Int?
        let limit: Int?
    }

    /// Fetch recent MCP sessions from `GET /api/v1/sessions`.
    func sessions(limit: Int = 5) async throws -> [MCPSession] {
        let response: SessionsResponse = try await fetchWrapped(path: "/api/v1/sessions?limit=\(limit)")
        return response.sessions
    }

    // MARK: - Activity

    /// Fetch recent activity entries from `GET /api/v1/activity`.
    func recentActivity(limit: Int = 50) async throws -> [ActivityEntry] {
        let response: ActivityListResponse = try await fetchWrapped(path: "/api/v1/activity?limit=\(limit)")
        return response.activities
    }

    /// Fetch the activity summary from `GET /api/v1/activity/summary`.
    func activitySummary() async throws -> ActivitySummary {
        return try await fetchWrapped(path: "/api/v1/activity/summary")
    }

    /// Fetch activity entries that contain sensitive data detections.
    func sensitiveDataCheck() async throws -> [ActivityEntry] {
        let response: ActivityListResponse = try await fetchWrapped(
            path: "/api/v1/activity?sensitive_data=true&limit=100"
        )
        return response.activities
    }

    // MARK: - Server Detail

    /// Fetch tools for a specific server from `GET /api/v1/servers/{id}/tools`.
    func serverTools(_ id: String) async throws -> [ServerTool] {
        let data = try await fetchRaw(path: "/api/v1/servers/\(id)/tools")
        let decoder = JSONDecoder()
        // Try wrapped response first
        if let wrapper = try? decoder.decode(APIResponse<ServerToolsResponse>.self, from: data),
           let payload = wrapper.data {
            return payload.tools
        }
        // Try direct decode
        if let direct = try? decoder.decode(ServerToolsResponse.self, from: data) {
            return direct.tools
        }
        // Try {"data": {"tools": [...]}} shape
        if let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
           let dataObj = json["data"] as? [String: Any],
           let toolsArray = dataObj["tools"] as? [[String: Any]] {
            let toolsData = try JSONSerialization.data(withJSONObject: toolsArray)
            return try decoder.decode([ServerTool].self, from: toolsData)
        }
        return []
    }

    /// Fetch log lines for a specific server from `GET /api/v1/servers/{id}/logs`.
    /// Handles both structured `logs` (objects) and plain `lines` (strings) response formats.
    func serverLogs(_ id: String, tail: Int = 100) async throws -> [String] {
        let data = try await fetchRaw(path: "/api/v1/servers/\(id)/logs?tail=\(tail)")
        let decoder = JSONDecoder()
        if let wrapper = try? decoder.decode(APIResponse<ServerLogsResponse>.self, from: data),
           let payload = wrapper.data {
            return payload.displayLines
        }
        if let direct = try? decoder.decode(ServerLogsResponse.self, from: data) {
            return direct.displayLines
        }
        return []
    }

    // MARK: - Add / Import Servers

    /// Add a new server via `POST /api/v1/servers`.
    func addServer(_ config: [String: Any]) async throws {
        try await postAction(path: "/api/v1/servers", body: config)
    }

    /// Fetch canonical config paths for import from `GET /api/v1/servers/import/paths`.
    func importPaths() async throws -> [CanonicalConfigPath] {
        let data = try await fetchRaw(path: "/api/v1/servers/import/paths")
        let decoder = JSONDecoder()
        if let wrapper = try? decoder.decode(APIResponse<CanonicalConfigPathsResponse>.self, from: data),
           let payload = wrapper.data {
            return payload.paths
        }
        if let direct = try? decoder.decode(CanonicalConfigPathsResponse.self, from: data) {
            return direct.paths
        }
        return []
    }

    /// Import servers from a filesystem path via `POST /api/v1/servers/import/path`.
    func importFromPath(_ path: String, format: String? = nil) async throws -> ImportResponse {
        var body: [String: Any] = ["path": path]
        if let format { body["format"] = format }
        let data = try await postRaw(path: "/api/v1/servers/import/path", body: body)
        let decoder = JSONDecoder()

        // Try the standard API envelope: {"success": true, "data": {...}}
        if let wrapper = try? decoder.decode(APIResponse<ImportResponse>.self, from: data),
           let payload = wrapper.data {
            return payload
        }

        // Check for an API error envelope: {"success": false, "error": "..."}
        if let errorResp = try? decoder.decode(APIErrorResponse.self, from: data),
           !errorResp.success, let message = errorResp.error {
            throw APIClientError.httpError(statusCode: 400, message: message)
        }

        // Fallback: try to decode the full body as ImportResponse directly.
        // If this also fails, surface the raw body so the caller can show something useful.
        do {
            return try decoder.decode(ImportResponse.self, from: data)
        } catch {
            let preview = String(data: data.prefix(200), encoding: .utf8) ?? "binary"
            throw APIClientError.decodingError(
                underlying: NSError(domain: "ImportDecode", code: -1,
                                    userInfo: [NSLocalizedDescriptionKey: "Cannot decode import response: \(preview)"])
            )
        }
    }

    // MARK: - Tool Search

    /// Search tools across all servers via `GET /api/v1/tools`.
    func searchTools(query: String, limit: Int = 20) async throws -> [SearchResult] {
        let encoded = query.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? query
        let data = try await fetchRaw(path: "/api/v1/tools?q=\(encoded)&limit=\(limit)")
        let decoder = JSONDecoder()
        if let wrapper = try? decoder.decode(APIResponse<SearchToolsResponse>.self, from: data),
           let payload = wrapper.data {
            return payload.results ?? []
        }
        if let direct = try? decoder.decode(SearchToolsResponse.self, from: data) {
            return direct.results ?? []
        }
        return []
    }

    // MARK: - Tool Quarantine

    /// Fetch tool diff (old vs new description/schema) for a pending/changed tool.
    /// Returns a dictionary with keys like "old_description", "new_description",
    /// "old_schema", "new_schema", "status".
    func toolDiff(server: String, tool: String) async throws -> [String: Any] {
        let encodedTool = tool.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? tool
        let data = try await fetchRaw(path: "/api/v1/servers/\(server)/tools/\(encodedTool)/diff")
        // Try standard envelope first
        if let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any] {
            if let payload = json["data"] as? [String: Any] {
                return payload
            }
            return json
        }
        return [:]
    }

    /// Approve specific tools for a server via `POST /api/v1/servers/{id}/tools/approve`.
    func approveSpecificTools(_ id: String, tools: [String]) async throws {
        let body: [String: Any] = ["tools": tools]
        try await postAction(path: "/api/v1/servers/\(id)/tools/approve", body: body)
    }

    // MARK: - Generic Endpoints (for views that need raw data access)

    /// Fetch raw response data from a GET endpoint.
    /// Used by views that handle their own decoding (e.g., TokensView).
    func fetchRaw(path: String) async throws -> Data {
        let (data, _) = try await performRequest(path: path, method: "GET")
        return data
    }

    /// Execute a POST action and return the raw response data.
    /// Used by views that need to inspect the full response (e.g., token creation).
    @discardableResult
    func postRaw(path: String, body: [String: Any]? = nil) async throws -> Data {
        let bodyData: Data?
        if let body {
            bodyData = try JSONSerialization.data(withJSONObject: body)
        } else {
            bodyData = nil
        }
        let (data, _) = try await performRequest(path: path, method: "POST", body: bodyData)
        return data
    }

    /// Execute a DELETE action.
    /// Used by views that need to delete resources (e.g., token revocation).
    func deleteAction(path: String) async throws {
        let (data, response) = try await performRequest(path: path, method: "DELETE")
        if let errorResponse = try? JSONDecoder().decode(APIErrorResponse.self, from: data),
           !errorResponse.success, let message = errorResponse.error {
            throw APIClientError.httpError(statusCode: response.statusCode, message: message)
        }
    }

    /// Execute a DELETE action and return the raw response data.
    /// Used by views that need to inspect the full response (e.g., disconnect result).
    func deleteRaw(path: String) async throws -> Data {
        let (data, response) = try await performRequest(path: path, method: "DELETE")
        if let errorResponse = try? JSONDecoder().decode(APIErrorResponse.self, from: data),
           !errorResponse.success, let message = errorResponse.error {
            throw APIClientError.httpError(statusCode: response.statusCode, message: message)
        }
        return data
    }

    // MARK: - Configuration (Spec 060)

    /// Fetch the full server configuration as a JSON dictionary.
    /// GET /api/v1/config → { success, data: { config: {...} } }.
    func getConfig() async throws -> [String: Any] {
        let (data, response) = try await performRequest(path: "/api/v1/config", method: "GET")
        guard let root = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw APIClientError.httpError(statusCode: response.statusCode, message: "Malformed config response")
        }
        if let inner = root["data"] as? [String: Any], let cfg = inner["config"] as? [String: Any] {
            return cfg
        }
        // Some builds may return the config object directly.
        if let cfg = root["config"] as? [String: Any] { return cfg }
        throw APIClientError.httpError(statusCode: response.statusCode, message: "Config not found in response")
    }

    /// Apply a partial config update (only the changed fields) via the
    /// deep-merge PATCH endpoint, so unrelated settings and redacted secrets are
    /// never clobbered. Returns the apply-result dictionary (success,
    /// applied_immediately, requires_restart, restart_reason, changed_fields,
    /// validation_errors).
    @discardableResult
    func patchConfig(_ partial: [String: Any]) async throws -> [String: Any] {
        let bodyData = try JSONSerialization.data(withJSONObject: partial)
        let (data, response) = try await performRequest(path: "/api/v1/config", method: "PATCH", body: bodyData)
        let root = (try? JSONSerialization.jsonObject(with: data) as? [String: Any]) ?? [:]
        if let success = root["success"] as? Bool, !success {
            let msg = (root["error"] as? String) ?? "Failed to apply configuration"
            throw APIClientError.httpError(statusCode: response.statusCode, message: msg)
        }
        return (root["data"] as? [String: Any]) ?? [:]
    }

    // MARK: - Private Helpers

    /// Fetch a resource wrapped in the standard `APIResponse` envelope.
    private func fetchWrapped<T: Decodable>(path: String) async throws -> T {
        let (data, _) = try await performRequest(path: path, method: "GET")
        let decoder = JSONDecoder()
        do {
            let wrapper = try decoder.decode(APIResponse<T>.self, from: data)
            if wrapper.success, let payload = wrapper.data {
                return payload
            }
            throw APIClientError.httpError(statusCode: 200, message: wrapper.error ?? "Unknown error")
        } catch let error as APIClientError {
            throw error
        } catch {
            // Try decoding directly without the wrapper (some endpoints don't wrap)
            do {
                return try decoder.decode(T.self, from: data)
            } catch {
                throw APIClientError.decodingError(underlying: error)
            }
        }
    }

    /// Execute a POST action that returns a success/error wrapper.
    @discardableResult
    func postAction(path: String, body: [String: Any]? = nil) async throws -> Data {
        let bodyData: Data?
        if let body {
            bodyData = try JSONSerialization.data(withJSONObject: body)
        } else {
            bodyData = nil
        }
        let (data, response) = try await performRequest(path: path, method: "POST", body: bodyData)

        // Check for API-level errors in the response body
        if let errorResponse = try? JSONDecoder().decode(APIErrorResponse.self, from: data),
           !errorResponse.success, let message = errorResponse.error {
            throw APIClientError.httpError(statusCode: response.statusCode, message: message)
        }

        return data
    }

    /// Low-level request execution WITHOUT HTTP status validation. Returns the
    /// raw body and response for any status. Callers that need to inspect error
    /// bodies (e.g. the registry add-source flow, which reads a stable `code`)
    /// use this directly; most callers use `performRequest`, which validates.
    private func rawRequest(
        path: String,
        method: String,
        body: Data? = nil
    ) async throws -> (Data, HTTPURLResponse) {
        guard let url = URL(string: baseURL + path) else {
            throw APIClientError.invalidURL(baseURL + path)
        }

        var request = URLRequest(url: url)
        request.httpMethod = method
        request.setValue("application/json", forHTTPHeaderField: "Accept")

        // Spec 042: telemetry surface header so the daemon can attribute
        // requests to the macOS tray for the surface_requests counter.
        let trayVersion = Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "dev"
        request.setValue("tray/\(trayVersion)", forHTTPHeaderField: "X-MCPProxy-Client")

        if let body {
            request.httpBody = body
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }

        // Attach API key if configured
        if let apiKey, !apiKey.isEmpty {
            request.setValue(apiKey, forHTTPHeaderField: "X-API-Key")
        }

        let (data, urlResponse) = try await session.data(for: request)

        guard let httpResponse = urlResponse as? HTTPURLResponse else {
            throw APIClientError.noData
        }

        return (data, httpResponse)
    }

    /// Low-level request execution with HTTP status validation.
    private func performRequest(
        path: String,
        method: String,
        body: Data? = nil
    ) async throws -> (Data, HTTPURLResponse) {
        let (data, httpResponse) = try await rawRequest(path: path, method: method, body: body)

        // 2xx is success; for readiness we also treat the response as-is
        guard (200...299).contains(httpResponse.statusCode) else {
            // Try to extract error message from body
            var message = HTTPURLResponse.localizedString(forStatusCode: httpResponse.statusCode)
            if let errorBody = try? JSONDecoder().decode(APIErrorResponse.self, from: data),
               let apiError = errorBody.error {
                message = apiError
            }
            throw APIClientError.httpError(statusCode: httpResponse.statusCode, message: message)
        }

        return (data, httpResponse)
    }

    // MARK: - Registries (MCP-866 / MCP-902)

    /// List configured registries from `GET /api/v1/registries`, each tagged
    /// with provenance/trust so the UI can flag official vs custom sources.
    func registries() async throws -> [Registry] {
        let response: GetRegistriesResponse = try await fetchWrapped(path: "/api/v1/registries")
        return response.registries
    }

    /// Add a user-supplied registry source via `POST /api/v1/registries`. The
    /// server always tags an added source custom/unverified (provenance is NOT
    /// part of the request), so every server later discovered through it lands
    /// quarantined. Returns a structured result carrying the stable error
    /// `code` instead of throwing, mirroring the Web UI's `addRegistrySource`.
    func addRegistrySource(
        url: String,
        protocol proto: String? = nil,
        id: String? = nil,
        name: String? = nil
    ) async -> AddRegistrySourceResult {
        var body: [String: Any] = ["url": url]
        if let proto, !proto.isEmpty { body["protocol"] = proto }
        if let id, !id.isEmpty { body["id"] = id }
        if let name, !name.isEmpty { body["name"] = name }

        do {
            let bodyData = try JSONSerialization.data(withJSONObject: body)
            let (data, response) = try await rawRequest(path: "/api/v1/registries", method: "POST", body: bodyData)
            let decoder = JSONDecoder()

            if (200...299).contains(response.statusCode),
               let wrapper = try? decoder.decode(APIResponse<AddRegistrySourceData>.self, from: data),
               wrapper.success {
                return .ok(wrapper.data?.registry)
            }

            let errBody = try? decoder.decode(RegistryAddErrorBody.self, from: data)
            return .failure(
                code: errBody?.code,
                error: errBody?.error ?? "HTTP \(response.statusCode): \(HTTPURLResponse.localizedString(forStatusCode: response.statusCode))"
            )
        } catch {
            return .failure(code: nil, error: error.localizedDescription)
        }
    }

    /// Search a single registry's servers via
    /// `GET /api/v1/registries/{id}/servers?q=&limit=`. Throws on transport/HTTP
    /// errors; a 200 with an `unavailable` marker (e.g. key required) is a
    /// normal, non-throwing result that the browse view surfaces per-registry.
    func searchRegistryServers(registryID: String, query: String, limit: Int = 20) async throws -> SearchRegistryServersResponse {
        var params: [String] = ["limit=\(limit)"]
        if !query.isEmpty { params.insert("q=\(query.uriComponentEncoded)", at: 0) }
        let path = "/api/v1/registries/\(registryID.uriComponentEncoded)/servers?\(params.joined(separator: "&"))"
        return try await fetchWrapped(path: path)
    }

    /// Add a server discovered through a registry via
    /// `POST /api/v1/registries/{id}/servers/{serverId}/add`. Returns a
    /// structured result (does not throw) carrying `missingInputs` when the
    /// server needs env values the caller hasn't supplied yet.
    func addServerFromRegistry(registryID: String, serverID: String, env: [String: String]? = nil) async -> AddServerResult {
        var body: [String: Any] = [:]
        if let env, !env.isEmpty { body["env"] = env }
        let path = "/api/v1/registries/\(registryID.uriComponentEncoded)/servers/\(serverID.uriComponentEncoded)/add"
        do {
            let bodyData = try JSONSerialization.data(withJSONObject: body)
            let (data, response) = try await rawRequest(path: path, method: "POST", body: bodyData)
            if (200...299).contains(response.statusCode) { return .ok() }
            let err = try? JSONDecoder().decode(RegistryAddServerErrorBody.self, from: data)
            return .failure(
                message: err?.message ?? "HTTP \(response.statusCode): \(HTTPURLResponse.localizedString(forStatusCode: response.statusCode))",
                missingInputs: err?.missingInputs
            )
        } catch {
            return .failure(message: error.localizedDescription)
        }
    }
}
