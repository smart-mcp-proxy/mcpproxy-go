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

        // Prefer socket transport when available
        if let path = socketPath, path.isEmpty {
            // Explicitly requested TCP-only
            self.session = SocketTransport.makeTCPSession()
        } else if SocketTransport.isSocketAvailable(path: socketPath) {
            self.session = SocketTransport.makeURLSession(socketPath: socketPath)
        } else {
            self.session = SocketTransport.makeTCPSession()
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
        let (_, response) = try await performRequest(path: "/healthz/ready", method: "GET")
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

    // MARK: - Servers

    /// List all upstream servers from `GET /api/v1/servers`.
    func servers() async throws -> [ServerStatus] {
        let response: ServersListResponse = try await fetchWrapped(path: "/api/v1/servers")
        return response.servers
    }

    /// Enable a server via `POST /api/v1/servers/{id}/enable`.
    func enableServer(_ id: String) async throws {
        let body: [String: Any] = ["enabled": true]
        try await postAction(path: "/api/v1/servers/\(id)/enable", body: body)
    }

    /// Disable a server via `POST /api/v1/servers/{id}/enable` with enabled=false.
    func disableServer(_ id: String) async throws {
        let body: [String: Any] = ["enabled": false]
        try await postAction(path: "/api/v1/servers/\(id)/enable", body: body)
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
        try await postAction(path: "/api/v1/servers/\(id)/tools/approve")
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
    private func postAction(path: String, body: [String: Any]? = nil) async throws -> Data {
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

    /// Low-level request execution with HTTP status validation.
    private func performRequest(
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
}
