import Foundation

// MARK: - Health Enums

/// Server health level as reported by the backend health calculator.
enum HealthLevel: String, Codable, CaseIterable {
    case healthy
    case degraded
    case unhealthy

    /// SF Symbol name for visual indicator.
    var sfSymbolName: String {
        switch self {
        case .healthy:   return "checkmark.circle.fill"
        case .degraded:  return "exclamationmark.triangle.fill"
        case .unhealthy: return "xmark.circle.fill"
        }
    }

    /// Semantic color name for SwiftUI.
    var colorName: String {
        switch self {
        case .healthy:   return "green"
        case .degraded:  return "orange"
        case .unhealthy: return "red"
        }
    }
}

/// Administrative state of a server.
enum AdminState: String, Codable, CaseIterable {
    case enabled
    case disabled
    case quarantined
}

/// Suggested remediation action returned by the health calculator.
enum HealthAction: String, Codable, CaseIterable {
    case login
    case restart
    case enable
    case approve
    case viewLogs = "view_logs"
    case setSecret = "set_secret"
    case configure

    /// Human-readable button label.
    var label: String {
        switch self {
        case .login:      return "Log In"
        case .restart:    return "Restart"
        case .enable:     return "Enable"
        case .approve:    return "Approve"
        case .viewLogs:   return "View Logs"
        case .setSecret:  return "Set Secret"
        case .configure:  return "Configure"
        }
    }
}

// MARK: - Health Status

/// Unified health status for an upstream MCP server.
/// Matches the Go `contracts.HealthStatus` struct.
struct HealthStatus: Codable, Equatable {
    let level: String
    let adminState: String
    let summary: String
    let detail: String?
    let action: String?

    enum CodingKeys: String, CodingKey {
        case level
        case adminState = "admin_state"
        case summary
        case detail
        case action
    }

    /// Parsed health level enum, falling back to `.unhealthy` for unknown values.
    var healthLevel: HealthLevel {
        HealthLevel(rawValue: level) ?? .unhealthy
    }

    /// Parsed admin state enum, falling back to `.enabled` for unknown values.
    var adminStateEnum: AdminState {
        AdminState(rawValue: adminState) ?? .enabled
    }

    /// Parsed action enum, nil when action is empty or unrecognized.
    var healthAction: HealthAction? {
        guard let action, !action.isEmpty else { return nil }
        return HealthAction(rawValue: action)
    }
}

// MARK: - OAuth Status

/// OAuth authentication status for a server that uses OAuth.
struct OAuthStatus: Codable, Equatable {
    let status: String
    let tokenExpiresAt: String?
    let hasRefreshToken: Bool?
    let userLoggedOut: Bool?

    enum CodingKeys: String, CodingKey {
        case status
        case tokenExpiresAt = "token_expires_at"
        case hasRefreshToken = "has_refresh_token"
        case userLoggedOut = "user_logged_out"
    }
}

// MARK: - Quarantine Stats

/// Tool quarantine metrics for a server.
struct QuarantineStats: Codable, Equatable {
    let pendingCount: Int
    let changedCount: Int

    enum CodingKeys: String, CodingKey {
        case pendingCount = "pending_count"
        case changedCount = "changed_count"
    }

    var totalPending: Int {
        pendingCount + changedCount
    }
}

// MARK: - Server Status

/// Represents an upstream MCP server's configuration and runtime status.
/// Matches the Go `contracts.Server` struct serialized by `/api/v1/servers`.
struct ServerStatus: Codable, Identifiable, Equatable {
    let id: String
    let name: String
    let url: String?
    let command: String?
    let args: [String]?
    let `protocol`: String
    let enabled: Bool
    let connected: Bool
    let connecting: Bool?
    let quarantined: Bool
    let status: String?
    let lastError: String?
    let connectedAt: String?
    let lastReconnectAt: String?
    let reconnectCount: Int?
    let toolCount: Int
    let toolListTokenSize: Int?
    let authenticated: Bool?
    let oauthStatus: String?
    let tokenExpiresAt: String?
    let userLoggedOut: Bool?
    let health: HealthStatus?
    let quarantine: QuarantineStats?
    let error: String?

    enum CodingKeys: String, CodingKey {
        case id, name, url, command, args
        case `protocol` = "protocol"
        case enabled, connected, connecting, quarantined
        case status
        case lastError = "last_error"
        case connectedAt = "connected_at"
        case lastReconnectAt = "last_reconnect_at"
        case reconnectCount = "reconnect_count"
        case toolCount = "tool_count"
        case toolListTokenSize = "tool_list_token_size"
        case authenticated
        case oauthStatus = "oauth_status"
        case tokenExpiresAt = "token_expires_at"
        case userLoggedOut = "user_logged_out"
        case health
        case quarantine
        case error
    }

    /// Number of tools awaiting approval (pending + changed), or 0 if quarantine stats are absent.
    var pendingApprovalCount: Int {
        quarantine?.totalPending ?? 0
    }
}

// MARK: - Upstream Stats

/// Aggregated statistics about upstream servers, as returned by `GetUpstreamStats()`.
struct UpstreamStats: Codable, Equatable {
    let totalServers: Int
    let connectedServers: Int
    let quarantinedServers: Int
    let totalTools: Int
    let dockerContainers: Int?
    let tokenMetrics: TokenMetrics?

    enum CodingKeys: String, CodingKey {
        case totalServers = "total_servers"
        case connectedServers = "connected_servers"
        case quarantinedServers = "quarantined_servers"
        case totalTools = "total_tools"
        case dockerContainers = "docker_containers"
        case tokenMetrics = "token_metrics"
    }
}

/// Token usage and savings metrics.
struct TokenMetrics: Codable, Equatable {
    let totalServerToolListSize: Int
    let averageQueryResultSize: Int
    let savedTokens: Int
    let savedTokensPercentage: Double
    let perServerToolListSizes: [String: Int]?

    enum CodingKeys: String, CodingKey {
        case totalServerToolListSize = "total_server_tool_list_size"
        case averageQueryResultSize = "average_query_result_size"
        case savedTokens = "saved_tokens"
        case savedTokensPercentage = "saved_tokens_percentage"
        case perServerToolListSizes = "per_server_tool_list_sizes"
    }
}

// MARK: - Activity Models

/// A single activity record from the activity log.
/// Matches Go `contracts.ActivityRecord`.
struct ActivityEntry: Codable, Identifiable, Equatable {
    let id: String
    let type: String
    let source: String?
    let serverName: String?
    let toolName: String?
    let status: String
    let errorMessage: String?
    let durationMs: Int64?
    let timestamp: String
    let sessionId: String?
    let requestId: String?
    let hasSensitiveData: Bool?
    let detectionTypes: [String]?
    let maxSeverity: String?

    enum CodingKeys: String, CodingKey {
        case id, type, source, status, timestamp
        case serverName = "server_name"
        case toolName = "tool_name"
        case errorMessage = "error_message"
        case durationMs = "duration_ms"
        case sessionId = "session_id"
        case requestId = "request_id"
        case hasSensitiveData = "has_sensitive_data"
        case detectionTypes = "detection_types"
        case maxSeverity = "max_severity"
    }

    static func == (lhs: ActivityEntry, rhs: ActivityEntry) -> Bool {
        lhs.id == rhs.id
    }
}

/// Response wrapper for `GET /api/v1/activity`.
struct ActivityListResponse: Codable {
    let activities: [ActivityEntry]
    let total: Int
    let limit: Int
    let offset: Int
}

/// Top server entry within an activity summary.
struct ActivityTopServer: Codable, Equatable {
    let name: String
    let count: Int
}

/// Top tool entry within an activity summary.
struct ActivityTopTool: Codable, Equatable {
    let server: String
    let tool: String
    let count: Int
}

/// Summary statistics for a time period.
/// Matches Go `contracts.ActivitySummaryResponse`.
struct ActivitySummary: Codable, Equatable {
    let period: String
    let totalCount: Int
    let successCount: Int
    let errorCount: Int
    let blockedCount: Int
    let topServers: [ActivityTopServer]?
    let topTools: [ActivityTopTool]?
    let startTime: String
    let endTime: String

    enum CodingKeys: String, CodingKey {
        case period
        case totalCount = "total_count"
        case successCount = "success_count"
        case errorCount = "error_count"
        case blockedCount = "blocked_count"
        case topServers = "top_servers"
        case topTools = "top_tools"
        case startTime = "start_time"
        case endTime = "end_time"
    }
}

// MARK: - Status / Info Responses

/// Response for `GET /api/v1/status`.
/// The backend builds this as a dynamic map; we decode the known keys.
struct StatusResponse: Codable {
    let running: Bool
    let edition: String?
    let listenAddr: String?
    let routingMode: String?
    let upstreamStats: UpstreamStats?
    let timestamp: Int64?

    enum CodingKeys: String, CodingKey {
        case running
        case edition
        case listenAddr = "listen_addr"
        case routingMode = "routing_mode"
        case upstreamStats = "upstream_stats"
        case timestamp
    }
}

/// Available API endpoints.
struct InfoEndpoints: Codable, Equatable {
    let http: String
    let socket: String
}

/// Update availability information.
struct UpdateInfo: Codable, Equatable {
    let available: Bool
    let latestVersion: String?
    let releaseUrl: String?
    let checkedAt: String?
    let isPrerelease: Bool?
    let checkError: String?

    enum CodingKeys: String, CodingKey {
        case available
        case latestVersion = "latest_version"
        case releaseUrl = "release_url"
        case checkedAt = "checked_at"
        case isPrerelease = "is_prerelease"
        case checkError = "check_error"
    }
}

/// Response for `GET /api/v1/info`.
struct InfoResponse: Codable, Equatable {
    let version: String
    let webUiUrl: String
    let listenAddr: String
    let endpoints: InfoEndpoints
    let update: UpdateInfo?

    enum CodingKeys: String, CodingKey {
        case version
        case webUiUrl = "web_ui_url"
        case listenAddr = "listen_addr"
        case endpoints
        case update
    }
}

// MARK: - SSE Event

/// Parsed Server-Sent Event from the `/events` endpoint.
struct SSEEvent: Equatable {
    /// The SSE `event:` field (e.g. "status", "servers.changed", "ping").
    let event: String

    /// The raw JSON string from the `data:` field.
    let data: String

    /// Parsed retry interval in milliseconds from the `retry:` field, if present.
    let retry: Int?

    /// Unique identifier from the `id:` field, if present.
    let id: String?

    /// Convenience: decode the data payload into a Decodable type.
    func decode<T: Decodable>(_ type: T.Type) throws -> T {
        guard let jsonData = data.data(using: .utf8) else {
            throw SSEError.invalidData
        }
        return try JSONDecoder().decode(type, from: jsonData)
    }

    /// Convenience: decode the data payload as a JSON dictionary.
    func decodePayload() throws -> [String: Any] {
        guard let jsonData = data.data(using: .utf8) else {
            throw SSEError.invalidData
        }
        guard let dict = try JSONSerialization.jsonObject(with: jsonData) as? [String: Any] else {
            throw SSEError.invalidData
        }
        return dict
    }
}

/// SSE-specific errors.
enum SSEError: Error, LocalizedError {
    case invalidData
    case connectionLost
    case invalidURL

    var errorDescription: String? {
        switch self {
        case .invalidData:
            return "Failed to decode SSE data payload"
        case .connectionLost:
            return "SSE connection was lost"
        case .invalidURL:
            return "Invalid SSE endpoint URL"
        }
    }
}

// MARK: - Status Update (SSE status event payload)

/// Payload of an SSE `status` event.
/// Combines running state, upstream stats, and the full server status snapshot.
struct StatusUpdate: Codable {
    let running: Bool
    let listenAddr: String?
    let timestamp: Int64?
    let upstreamStats: UpstreamStats?

    enum CodingKeys: String, CodingKey {
        case running
        case listenAddr = "listen_addr"
        case timestamp
        case upstreamStats = "upstream_stats"
    }
}

// MARK: - API Wrapper

/// Standard API response envelope used by all REST endpoints.
/// `data` is decoded as a generic JSON value; callers unwrap to the expected type.
struct APIResponse<T: Decodable>: Decodable {
    let success: Bool
    let data: T?
    let error: String?
    let requestId: String?

    enum CodingKeys: String, CodingKey {
        case success
        case data
        case error
        case requestId = "request_id"
    }
}

/// Non-generic API error response for when we only need the error.
struct APIErrorResponse: Codable {
    let success: Bool
    let error: String?
    let requestId: String?

    enum CodingKeys: String, CodingKey {
        case success
        case error
        case requestId = "request_id"
    }
}

// MARK: - Servers List Response

/// Response wrapper for `GET /api/v1/servers`.
struct ServersListResponse: Codable {
    let servers: [ServerStatus]
}

// MARK: - Server Action Response

/// Response for server action endpoints (enable, disable, restart, etc.).
struct ServerActionResponse: Codable {
    let message: String
    let serverName: String?

    enum CodingKeys: String, CodingKey {
        case message
        case serverName = "server_name"
    }
}

// MARK: - Server Tools

/// Annotation hints for an MCP tool (read-only, destructive, etc.).
struct ToolAnnotation: Codable, Equatable {
    let readOnlyHint: Bool?
    let destructiveHint: Bool?
    let idempotentHint: Bool?
    let openWorldHint: Bool?
    let title: String?

    enum CodingKeys: String, CodingKey {
        case readOnlyHint = "readOnlyHint"
        case destructiveHint = "destructiveHint"
        case idempotentHint = "idempotentHint"
        case openWorldHint = "openWorldHint"
        case title
    }
}

/// A single tool exposed by an upstream MCP server.
struct ServerTool: Codable, Identifiable, Equatable {
    var id: String { name }
    let name: String
    let description: String?
    let serverName: String?
    let annotations: ToolAnnotation?
    let approvalStatus: String?

    enum CodingKeys: String, CodingKey {
        case name, description, annotations
        case serverName = "server_name"
        case approvalStatus = "approval_status"
    }

    static func == (lhs: ServerTool, rhs: ServerTool) -> Bool {
        lhs.name == rhs.name && lhs.serverName == rhs.serverName
    }
}

/// Response wrapper for `GET /api/v1/servers/{id}/tools`.
struct ServerToolsResponse: Codable {
    let tools: [ServerTool]
}

/// Response wrapper for `GET /api/v1/servers/{id}/logs`.
struct ServerLogsResponse: Codable {
    let serverName: String?
    let lines: [String]
    let count: Int?

    enum CodingKeys: String, CodingKey {
        case serverName = "server_name"
        case lines, count
    }
}

// MARK: - Import Config

/// A canonical config file path discovered by the backend.
struct CanonicalConfigPath: Codable, Identifiable {
    var id: String { path }
    let name: String
    let path: String
    let format: String?
    let exists: Bool
    let description: String?

    enum CodingKeys: String, CodingKey {
        case name, path, format, exists, description
    }
}

/// Response wrapper for `GET /api/v1/servers/import/paths`.
struct CanonicalConfigPathsResponse: Codable {
    let os: String?
    let paths: [CanonicalConfigPath]
}

/// Summary of an import operation.
struct ImportSummary: Codable {
    let total: Int?
    let imported: Int?
    let skipped: Int?
    let failed: Int?
}

/// Response wrapper for `POST /api/v1/servers/import/path`.
struct ImportResponse: Codable {
    let summary: ImportSummary?
    let message: String?
}

// MARK: - Tool Search

/// A tool returned in search results.
struct SearchTool: Codable {
    let name: String
    let description: String?
    let serverName: String?
    let annotations: ToolAnnotation?

    enum CodingKeys: String, CodingKey {
        case name, description, annotations
        case serverName = "server_name"
    }
}

/// A single search result with score.
struct SearchResult: Codable, Identifiable {
    var id: String { "\(tool.serverName ?? ""):\(tool.name)" }
    let score: Double
    let tool: SearchTool
}

/// Response wrapper for `GET /api/v1/tools` or `GET /api/v1/index/search`.
struct SearchToolsResponse: Codable {
    let query: String?
    let results: [SearchResult]?
    let tools: [SearchTool]?
    let total: Int?
}
