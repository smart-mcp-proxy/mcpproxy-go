import XCTest
@testable import MCPProxy

final class ModelsTests: XCTestCase {

    // MARK: - Helpers

    private func decode<T: Decodable>(_ type: T.Type, from jsonString: String) throws -> T {
        let data = jsonString.data(using: .utf8)!
        return try JSONDecoder().decode(T.self, from: data)
    }

    // MARK: - HealthStatus

    func testDecodeHealthStatus() throws {
        let json = """
        {
            "level": "healthy",
            "admin_state": "enabled",
            "summary": "Connected and operational"
        }
        """
        let health = try decode(HealthStatus.self, from: json)
        XCTAssertEqual(health.level, "healthy")
        XCTAssertEqual(health.adminState, "enabled")
        XCTAssertEqual(health.summary, "Connected and operational")
        XCTAssertNil(health.detail)
        XCTAssertNil(health.action)
    }

    func testDecodeHealthStatusWithAllFields() throws {
        let json = """
        {
            "level": "degraded",
            "admin_state": "enabled",
            "summary": "OAuth token expiring soon",
            "detail": "Token expires in 2 hours",
            "action": "login"
        }
        """
        let health = try decode(HealthStatus.self, from: json)
        XCTAssertEqual(health.level, "degraded")
        XCTAssertEqual(health.detail, "Token expires in 2 hours")
        XCTAssertEqual(health.action, "login")
    }

    func testHealthLevelParsing() throws {
        let healthyJSON = """
        {"level": "healthy", "admin_state": "enabled", "summary": "OK"}
        """
        let healthy = try decode(HealthStatus.self, from: healthyJSON)
        XCTAssertEqual(healthy.healthLevel, .healthy)

        let degradedJSON = """
        {"level": "degraded", "admin_state": "enabled", "summary": "Warning"}
        """
        let degraded = try decode(HealthStatus.self, from: degradedJSON)
        XCTAssertEqual(degraded.healthLevel, .degraded)

        let unhealthyJSON = """
        {"level": "unhealthy", "admin_state": "disabled", "summary": "Down"}
        """
        let unhealthy = try decode(HealthStatus.self, from: unhealthyJSON)
        XCTAssertEqual(unhealthy.healthLevel, .unhealthy)
    }

    func testHealthLevelUnknownValueFallsBackToUnhealthy() throws {
        let json = """
        {"level": "critical", "admin_state": "enabled", "summary": "Unknown level"}
        """
        let health = try decode(HealthStatus.self, from: json)
        XCTAssertEqual(health.healthLevel, .unhealthy)
    }

    func testAdminStateParsing() throws {
        let json = """
        {"level": "healthy", "admin_state": "quarantined", "summary": "Pending approval"}
        """
        let health = try decode(HealthStatus.self, from: json)
        XCTAssertEqual(health.adminStateEnum, .quarantined)
    }

    func testAdminStateUnknownValueFallsBackToEnabled() throws {
        let json = """
        {"level": "healthy", "admin_state": "suspended", "summary": "Unknown state"}
        """
        let health = try decode(HealthStatus.self, from: json)
        XCTAssertEqual(health.adminStateEnum, .enabled)
    }

    func testHealthActionParsing() throws {
        let json = """
        {"level": "unhealthy", "admin_state": "enabled", "summary": "Failed", "action": "restart"}
        """
        let health = try decode(HealthStatus.self, from: json)
        XCTAssertEqual(health.healthAction, .restart)
    }

    func testHealthActionViewLogs() throws {
        let json = """
        {"level": "unhealthy", "admin_state": "enabled", "summary": "Failed", "action": "view_logs"}
        """
        let health = try decode(HealthStatus.self, from: json)
        XCTAssertEqual(health.healthAction, .viewLogs)
    }

    func testHealthActionNilWhenEmpty() throws {
        let json = """
        {"level": "healthy", "admin_state": "enabled", "summary": "OK", "action": ""}
        """
        let health = try decode(HealthStatus.self, from: json)
        XCTAssertNil(health.healthAction)
    }

    func testHealthActionNilWhenMissing() throws {
        let json = """
        {"level": "healthy", "admin_state": "enabled", "summary": "OK"}
        """
        let health = try decode(HealthStatus.self, from: json)
        XCTAssertNil(health.healthAction)
    }

    func testHealthActionNilWhenUnrecognized() throws {
        let json = """
        {"level": "healthy", "admin_state": "enabled", "summary": "OK", "action": "self_destruct"}
        """
        let health = try decode(HealthStatus.self, from: json)
        XCTAssertNil(health.healthAction)
    }

    // MARK: - HealthLevel Enum

    func testHealthLevelSFSymbolNames() {
        XCTAssertEqual(HealthLevel.healthy.sfSymbolName, "checkmark.circle.fill")
        XCTAssertEqual(HealthLevel.degraded.sfSymbolName, "exclamationmark.triangle.fill")
        XCTAssertEqual(HealthLevel.unhealthy.sfSymbolName, "xmark.circle.fill")
    }

    func testHealthLevelColorNames() {
        XCTAssertEqual(HealthLevel.healthy.colorName, "green")
        XCTAssertEqual(HealthLevel.degraded.colorName, "orange")
        XCTAssertEqual(HealthLevel.unhealthy.colorName, "red")
    }

    // MARK: - HealthAction Enum

    func testHealthActionLabels() {
        XCTAssertEqual(HealthAction.login.label, "Sign in")
        XCTAssertEqual(HealthAction.restart.label, "Restart")
        XCTAssertEqual(HealthAction.enable.label, "Enable")
        XCTAssertEqual(HealthAction.approve.label, "Approve")
        XCTAssertEqual(HealthAction.viewLogs.label, "View Logs")
        XCTAssertEqual(HealthAction.setSecret.label, "Set Secret")
        XCTAssertEqual(HealthAction.configure.label, "Configure")
    }

    // MARK: - ServerStatus

    func testDecodeFullServerStatus() throws {
        let json = """
        {
            "id": "github-server",
            "name": "github-server",
            "url": "https://api.github.com/mcp",
            "protocol": "http",
            "enabled": true,
            "connected": true,
            "quarantined": false,
            "tool_count": 12,
            "health": {
                "level": "healthy",
                "admin_state": "enabled",
                "summary": "Connected and operational"
            }
        }
        """
        let server = try decode(ServerStatus.self, from: json)
        XCTAssertEqual(server.id, "github-server")
        XCTAssertEqual(server.name, "github-server")
        XCTAssertEqual(server.url, "https://api.github.com/mcp")
        XCTAssertEqual(server.protocol, "http")
        XCTAssertTrue(server.enabled)
        XCTAssertTrue(server.connected)
        XCTAssertFalse(server.quarantined)
        XCTAssertEqual(server.toolCount, 12)
        XCTAssertNotNil(server.health)
        XCTAssertEqual(server.health?.level, "healthy")
        XCTAssertEqual(server.health?.adminState, "enabled")
        XCTAssertEqual(server.health?.summary, "Connected and operational")
    }

    func testDecodeServerStatusWithStdioProtocol() throws {
        let json = """
        {
            "id": "ast-grep",
            "name": "ast-grep",
            "command": "npx",
            "args": ["ast-grep-mcp"],
            "protocol": "stdio",
            "enabled": true,
            "connected": false,
            "quarantined": false,
            "tool_count": 3,
            "status": "disconnected",
            "last_error": "process exited unexpectedly"
        }
        """
        let server = try decode(ServerStatus.self, from: json)
        XCTAssertEqual(server.id, "ast-grep")
        XCTAssertEqual(server.command, "npx")
        XCTAssertEqual(server.args, ["ast-grep-mcp"])
        XCTAssertEqual(server.protocol, "stdio")
        XCTAssertFalse(server.connected)
        XCTAssertEqual(server.status, "disconnected")
        XCTAssertEqual(server.lastError, "process exited unexpectedly")
        XCTAssertNil(server.url)
    }

    func testDecodeServerStatusMissingOptionalFields() throws {
        let json = """
        {
            "id": "minimal",
            "name": "minimal",
            "protocol": "http",
            "enabled": false,
            "connected": false,
            "quarantined": false,
            "tool_count": 0
        }
        """
        let server = try decode(ServerStatus.self, from: json)
        XCTAssertEqual(server.id, "minimal")
        XCTAssertNil(server.url)
        XCTAssertNil(server.command)
        XCTAssertNil(server.args)
        XCTAssertNil(server.connecting)
        XCTAssertNil(server.status)
        XCTAssertNil(server.lastError)
        XCTAssertNil(server.connectedAt)
        XCTAssertNil(server.lastReconnectAt)
        XCTAssertNil(server.reconnectCount)
        XCTAssertNil(server.toolListTokenSize)
        XCTAssertNil(server.authenticated)
        XCTAssertNil(server.oauthStatus)
        XCTAssertNil(server.tokenExpiresAt)
        XCTAssertNil(server.userLoggedOut)
        XCTAssertNil(server.health)
        XCTAssertNil(server.quarantine)
        XCTAssertNil(server.error)
    }

    func testServerStatusPendingApprovalCountWithQuarantine() throws {
        let json = """
        {
            "id": "test",
            "name": "test",
            "protocol": "http",
            "enabled": true,
            "connected": true,
            "quarantined": true,
            "tool_count": 10,
            "quarantine": {
                "pending_count": 3,
                "changed_count": 2
            }
        }
        """
        let server = try decode(ServerStatus.self, from: json)
        XCTAssertEqual(server.pendingApprovalCount, 5)
    }

    func testServerStatusPendingApprovalCountWithoutQuarantine() throws {
        let json = """
        {
            "id": "test",
            "name": "test",
            "protocol": "http",
            "enabled": true,
            "connected": true,
            "quarantined": false,
            "tool_count": 5
        }
        """
        let server = try decode(ServerStatus.self, from: json)
        XCTAssertEqual(server.pendingApprovalCount, 0)
    }

    func testServerStatusIdentifiable() throws {
        let json = """
        {
            "id": "my-server",
            "name": "my-server",
            "protocol": "http",
            "enabled": true,
            "connected": false,
            "quarantined": false,
            "tool_count": 0
        }
        """
        let server = try decode(ServerStatus.self, from: json)
        XCTAssertEqual(server.id, "my-server")
    }

    func testDecodeServerStatusWithOAuthFields() throws {
        let json = """
        {
            "id": "oauth-server",
            "name": "oauth-server",
            "url": "https://api.example.com/mcp",
            "protocol": "http",
            "enabled": true,
            "connected": true,
            "quarantined": false,
            "tool_count": 8,
            "authenticated": true,
            "oauth_status": "authenticated",
            "token_expires_at": "2026-03-24T10:00:00Z",
            "user_logged_out": false,
            "connected_at": "2026-03-23T08:00:00Z",
            "reconnect_count": 2,
            "tool_list_token_size": 45000
        }
        """
        let server = try decode(ServerStatus.self, from: json)
        XCTAssertEqual(server.authenticated, true)
        XCTAssertEqual(server.oauthStatus, "authenticated")
        XCTAssertEqual(server.tokenExpiresAt, "2026-03-24T10:00:00Z")
        XCTAssertEqual(server.userLoggedOut, false)
        XCTAssertEqual(server.connectedAt, "2026-03-23T08:00:00Z")
        XCTAssertEqual(server.reconnectCount, 2)
        XCTAssertEqual(server.toolListTokenSize, 45000)
    }

    // MARK: - OAuthStatus

    func testDecodeOAuthStatus() throws {
        let json = """
        {
            "status": "authenticated",
            "token_expires_at": "2026-03-24T10:00:00Z",
            "has_refresh_token": true,
            "user_logged_out": false
        }
        """
        let oauth = try decode(OAuthStatus.self, from: json)
        XCTAssertEqual(oauth.status, "authenticated")
        XCTAssertEqual(oauth.tokenExpiresAt, "2026-03-24T10:00:00Z")
        XCTAssertEqual(oauth.hasRefreshToken, true)
        XCTAssertEqual(oauth.userLoggedOut, false)
    }

    func testDecodeOAuthStatusMinimal() throws {
        let json = """
        {
            "status": "not_configured"
        }
        """
        let oauth = try decode(OAuthStatus.self, from: json)
        XCTAssertEqual(oauth.status, "not_configured")
        XCTAssertNil(oauth.tokenExpiresAt)
        XCTAssertNil(oauth.hasRefreshToken)
        XCTAssertNil(oauth.userLoggedOut)
    }

    // MARK: - QuarantineStats

    func testDecodeQuarantineStats() throws {
        let json = """
        {
            "pending_count": 5,
            "changed_count": 2
        }
        """
        let stats = try decode(QuarantineStats.self, from: json)
        XCTAssertEqual(stats.pendingCount, 5)
        XCTAssertEqual(stats.changedCount, 2)
        XCTAssertEqual(stats.totalPending, 7)
    }

    func testQuarantineStatsTotalPendingZero() throws {
        let json = """
        {
            "pending_count": 0,
            "changed_count": 0
        }
        """
        let stats = try decode(QuarantineStats.self, from: json)
        XCTAssertEqual(stats.totalPending, 0)
    }

    // MARK: - ActivityEntry

    func testDecodeActivityEntry() throws {
        let json = """
        {
            "id": "act-001",
            "type": "tool_call",
            "source": "mcp",
            "server_name": "github",
            "tool_name": "create_issue",
            "status": "success",
            "duration_ms": 245,
            "timestamp": "2026-03-23T12:00:00Z",
            "session_id": "sess-abc",
            "request_id": "req-xyz"
        }
        """
        let entry = try decode(ActivityEntry.self, from: json)
        XCTAssertEqual(entry.id, "act-001")
        XCTAssertEqual(entry.type, "tool_call")
        XCTAssertEqual(entry.source, "mcp")
        XCTAssertEqual(entry.serverName, "github")
        XCTAssertEqual(entry.toolName, "create_issue")
        XCTAssertEqual(entry.status, "success")
        XCTAssertEqual(entry.durationMs, 245)
        XCTAssertEqual(entry.timestamp, "2026-03-23T12:00:00Z")
        XCTAssertEqual(entry.sessionId, "sess-abc")
        XCTAssertEqual(entry.requestId, "req-xyz")
    }

    func testDecodeActivityEntryMinimal() throws {
        let json = """
        {
            "id": "act-002",
            "type": "connection",
            "status": "error",
            "timestamp": "2026-03-23T12:00:00Z"
        }
        """
        let entry = try decode(ActivityEntry.self, from: json)
        XCTAssertEqual(entry.id, "act-002")
        XCTAssertEqual(entry.type, "connection")
        XCTAssertEqual(entry.status, "error")
        XCTAssertNil(entry.source)
        XCTAssertNil(entry.serverName)
        XCTAssertNil(entry.toolName)
        XCTAssertNil(entry.errorMessage)
        XCTAssertNil(entry.durationMs)
        XCTAssertNil(entry.sessionId)
        XCTAssertNil(entry.requestId)
        XCTAssertNil(entry.hasSensitiveData)
        XCTAssertNil(entry.detectionTypes)
        XCTAssertNil(entry.maxSeverity)
    }

    func testDecodeActivityEntryWithSensitiveData() throws {
        let json = """
        {
            "id": "act-003",
            "type": "tool_call",
            "status": "success",
            "timestamp": "2026-03-23T12:00:00Z",
            "has_sensitive_data": true,
            "detection_types": ["aws_access_key", "high_entropy"],
            "max_severity": "critical"
        }
        """
        let entry = try decode(ActivityEntry.self, from: json)
        XCTAssertEqual(entry.hasSensitiveData, true)
        XCTAssertEqual(entry.detectionTypes, ["aws_access_key", "high_entropy"])
        XCTAssertEqual(entry.maxSeverity, "critical")
    }

    func testActivityEntryEqualityByID() throws {
        let json1 = """
        {"id": "same-id", "type": "tool_call", "status": "success", "timestamp": "2026-03-23T12:00:00Z"}
        """
        let json2 = """
        {"id": "same-id", "type": "connection", "status": "error", "timestamp": "2026-03-23T13:00:00Z"}
        """
        let entry1 = try decode(ActivityEntry.self, from: json1)
        let entry2 = try decode(ActivityEntry.self, from: json2)
        XCTAssertEqual(entry1, entry2, "ActivityEntry equality is based on id only")
    }

    func testActivityEntryInequalityByID() throws {
        let json1 = """
        {"id": "id-1", "type": "tool_call", "status": "success", "timestamp": "2026-03-23T12:00:00Z"}
        """
        let json2 = """
        {"id": "id-2", "type": "tool_call", "status": "success", "timestamp": "2026-03-23T12:00:00Z"}
        """
        let entry1 = try decode(ActivityEntry.self, from: json1)
        let entry2 = try decode(ActivityEntry.self, from: json2)
        XCTAssertNotEqual(entry1, entry2)
    }

    // MARK: - ActivityListResponse

    func testDecodeActivityListResponse() throws {
        let json = """
        {
            "activities": [
                {
                    "id": "act-001",
                    "type": "tool_call",
                    "status": "success",
                    "timestamp": "2026-03-23T12:00:00Z"
                }
            ],
            "total": 100,
            "limit": 50,
            "offset": 0
        }
        """
        let response = try decode(ActivityListResponse.self, from: json)
        XCTAssertEqual(response.activities.count, 1)
        XCTAssertEqual(response.total, 100)
        XCTAssertEqual(response.limit, 50)
        XCTAssertEqual(response.offset, 0)
    }

    // MARK: - ActivitySummary

    func testDecodeActivitySummary() throws {
        let json = """
        {
            "period": "24h",
            "total_count": 150,
            "success_count": 140,
            "error_count": 8,
            "blocked_count": 2,
            "top_servers": [
                {"name": "github", "count": 80}
            ],
            "top_tools": [
                {"server": "github", "tool": "create_issue", "count": 45}
            ],
            "start_time": "2026-03-22T12:00:00Z",
            "end_time": "2026-03-23T12:00:00Z"
        }
        """
        let summary = try decode(ActivitySummary.self, from: json)
        XCTAssertEqual(summary.period, "24h")
        XCTAssertEqual(summary.totalCount, 150)
        XCTAssertEqual(summary.successCount, 140)
        XCTAssertEqual(summary.errorCount, 8)
        XCTAssertEqual(summary.blockedCount, 2)
        XCTAssertEqual(summary.topServers?.count, 1)
        XCTAssertEqual(summary.topServers?.first?.name, "github")
        XCTAssertEqual(summary.topServers?.first?.count, 80)
        XCTAssertEqual(summary.topTools?.count, 1)
        XCTAssertEqual(summary.topTools?.first?.server, "github")
        XCTAssertEqual(summary.topTools?.first?.tool, "create_issue")
        XCTAssertEqual(summary.topTools?.first?.count, 45)
        XCTAssertEqual(summary.startTime, "2026-03-22T12:00:00Z")
        XCTAssertEqual(summary.endTime, "2026-03-23T12:00:00Z")
    }

    func testDecodeActivitySummaryMinimal() throws {
        let json = """
        {
            "period": "1h",
            "total_count": 0,
            "success_count": 0,
            "error_count": 0,
            "blocked_count": 0,
            "start_time": "2026-03-23T11:00:00Z",
            "end_time": "2026-03-23T12:00:00Z"
        }
        """
        let summary = try decode(ActivitySummary.self, from: json)
        XCTAssertEqual(summary.totalCount, 0)
        XCTAssertNil(summary.topServers)
        XCTAssertNil(summary.topTools)
    }

    // MARK: - StatusResponse

    func testDecodeStatusResponse() throws {
        let json = """
        {
            "running": true,
            "edition": "personal",
            "listen_addr": "127.0.0.1:8080",
            "routing_mode": "bm25",
            "upstream_stats": {
                "total_servers": 5,
                "connected_servers": 4,
                "quarantined_servers": 1,
                "total_tools": 42
            },
            "timestamp": 1711180800
        }
        """
        let status = try decode(StatusResponse.self, from: json)
        XCTAssertTrue(status.running)
        XCTAssertEqual(status.edition, "personal")
        XCTAssertEqual(status.listenAddr, "127.0.0.1:8080")
        XCTAssertEqual(status.routingMode, "bm25")
        XCTAssertNotNil(status.upstreamStats)
        XCTAssertEqual(status.upstreamStats?.totalServers, 5)
        XCTAssertEqual(status.upstreamStats?.connectedServers, 4)
        XCTAssertEqual(status.upstreamStats?.quarantinedServers, 1)
        XCTAssertEqual(status.upstreamStats?.totalTools, 42)
        XCTAssertEqual(status.timestamp, 1711180800)
    }

    func testDecodeStatusResponseMinimal() throws {
        let json = """
        {
            "running": false
        }
        """
        let status = try decode(StatusResponse.self, from: json)
        XCTAssertFalse(status.running)
        XCTAssertNil(status.edition)
        XCTAssertNil(status.listenAddr)
        XCTAssertNil(status.routingMode)
        XCTAssertNil(status.upstreamStats)
        XCTAssertNil(status.timestamp)
    }

    // MARK: - UpstreamStats

    func testDecodeUpstreamStatsWithTokenMetrics() throws {
        let json = """
        {
            "total_servers": 3,
            "connected_servers": 2,
            "quarantined_servers": 0,
            "total_tools": 25,
            "docker_containers": 1,
            "token_metrics": {
                "total_server_tool_list_size": 120000,
                "average_query_result_size": 5000,
                "saved_tokens": 115000,
                "saved_tokens_percentage": 95.83,
                "per_server_tool_list_sizes": {
                    "github": 80000,
                    "gitlab": 40000
                }
            }
        }
        """
        let stats = try decode(UpstreamStats.self, from: json)
        XCTAssertEqual(stats.totalServers, 3)
        XCTAssertEqual(stats.connectedServers, 2)
        XCTAssertEqual(stats.quarantinedServers, 0)
        XCTAssertEqual(stats.totalTools, 25)
        XCTAssertEqual(stats.dockerContainers, 1)
        XCTAssertNotNil(stats.tokenMetrics)
        XCTAssertEqual(stats.tokenMetrics?.totalServerToolListSize, 120000)
        XCTAssertEqual(stats.tokenMetrics?.averageQueryResultSize, 5000)
        XCTAssertEqual(stats.tokenMetrics?.savedTokens, 115000)
        XCTAssertEqual(try XCTUnwrap(stats.tokenMetrics?.savedTokensPercentage), 95.83, accuracy: 0.01)
        XCTAssertEqual(stats.tokenMetrics?.perServerToolListSizes?["github"], 80000)
        XCTAssertEqual(stats.tokenMetrics?.perServerToolListSizes?["gitlab"], 40000)
    }

    func testDecodeUpstreamStatsMinimal() throws {
        let json = """
        {
            "total_servers": 0,
            "connected_servers": 0,
            "quarantined_servers": 0,
            "total_tools": 0
        }
        """
        let stats = try decode(UpstreamStats.self, from: json)
        XCTAssertEqual(stats.totalServers, 0)
        XCTAssertNil(stats.dockerContainers)
        XCTAssertNil(stats.tokenMetrics)
    }

    // MARK: - InfoResponse

    func testDecodeInfoResponse() throws {
        let json = """
        {
            "version": "v0.21.0",
            "web_ui_url": "http://127.0.0.1:8080/ui/",
            "listen_addr": "127.0.0.1:8080",
            "endpoints": {
                "http": "http://127.0.0.1:8080",
                "socket": "~/.mcpproxy/mcpproxy.sock"
            }
        }
        """
        let info = try decode(InfoResponse.self, from: json)
        XCTAssertEqual(info.version, "v0.21.0")
        XCTAssertEqual(info.webUiUrl, "http://127.0.0.1:8080/ui/")
        XCTAssertEqual(info.listenAddr, "127.0.0.1:8080")
        XCTAssertEqual(info.endpoints.http, "http://127.0.0.1:8080")
        XCTAssertEqual(info.endpoints.socket, "~/.mcpproxy/mcpproxy.sock")
        XCTAssertNil(info.update)
        // Spec 092: a core from before FR-001a/FR-002 sends neither field, and
        // an OLD core is exactly the one the supersede check must reason about
        // — so their absence must not fail decoding.
        XCTAssertNil(info.launchedBy)
        XCTAssertNil(info.pid)
    }

    /// Spec 092 FR-001a/FR-002: durable provenance and the core's pid.
    func testDecodeInfoResponseWithLaunchProvenanceAndPID() throws {
        let json = """
        {
            "version": "v0.54.0",
            "web_ui_url": "http://127.0.0.1:8080/ui/",
            "listen_addr": "127.0.0.1:8080",
            "endpoints": { "http": "http://127.0.0.1:8080", "socket": "s" },
            "launched_by": "tray",
            "pid": 4711
        }
        """
        let info = try decode(InfoResponse.self, from: json)
        XCTAssertEqual(info.launchedBy, "tray")
        XCTAssertEqual(info.pid, 4711)
    }

    func testDecodeInfoResponseWithUpdateAvailable() throws {
        let json = """
        {
            "version": "v0.20.0",
            "web_ui_url": "http://127.0.0.1:8080/ui/",
            "listen_addr": "127.0.0.1:8080",
            "endpoints": {
                "http": "http://127.0.0.1:8080",
                "socket": "~/.mcpproxy/mcpproxy.sock"
            },
            "update": {
                "available": true,
                "latest_version": "v0.21.0",
                "release_url": "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/tag/v0.21.0",
                "checked_at": "2026-03-23T12:00:00Z",
                "is_prerelease": false
            }
        }
        """
        let info = try decode(InfoResponse.self, from: json)
        XCTAssertNotNil(info.update)
        XCTAssertEqual(info.update?.available, true)
        XCTAssertEqual(info.update?.latestVersion, "v0.21.0")
        XCTAssertEqual(info.update?.releaseUrl, "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/tag/v0.21.0")
        XCTAssertEqual(info.update?.checkedAt, "2026-03-23T12:00:00Z")
        XCTAssertEqual(info.update?.isPrerelease, false)
        XCTAssertNil(info.update?.checkError)
    }

    func testDecodeInfoResponseWithUpdateCheckError() throws {
        let json = """
        {
            "version": "v0.20.0",
            "web_ui_url": "http://127.0.0.1:8080/ui/",
            "listen_addr": "127.0.0.1:8080",
            "endpoints": {
                "http": "http://127.0.0.1:8080",
                "socket": ""
            },
            "update": {
                "available": false,
                "check_error": "network timeout"
            }
        }
        """
        let info = try decode(InfoResponse.self, from: json)
        XCTAssertNotNil(info.update)
        XCTAssertFalse(info.update!.available)
        XCTAssertEqual(info.update?.checkError, "network timeout")
        XCTAssertNil(info.update?.latestVersion)
    }

    // MARK: - SSEEvent

    func testSSEEventDecodeTypedPayload() throws {
        let event = SSEEvent(
            event: "status",
            data: "{\"running\":true,\"listen_addr\":\"127.0.0.1:8080\"}",
            retry: nil,
            id: nil
        )
        let update = try event.decode(StatusUpdate.self)
        XCTAssertTrue(update.running)
        XCTAssertEqual(update.listenAddr, "127.0.0.1:8080")
    }

    func testSSEEventDecodePayloadAsDictionary() throws {
        let event = SSEEvent(
            event: "servers.changed",
            data: "{\"reason\":\"reconnected\",\"server\":\"github\"}",
            retry: nil,
            id: "42"
        )
        let payload = try event.decodePayload()
        XCTAssertEqual(payload["reason"] as? String, "reconnected")
        XCTAssertEqual(payload["server"] as? String, "github")
    }

    func testSSEEventDecodeInvalidDataThrows() {
        let event = SSEEvent(event: "test", data: "", retry: nil, id: nil)
        XCTAssertThrowsError(try event.decodePayload()) { error in
            XCTAssertTrue(error is SSEError)
        }
    }

    func testSSEEventEquality() {
        let a = SSEEvent(event: "status", data: "{}", retry: 5000, id: "1")
        let b = SSEEvent(event: "status", data: "{}", retry: 5000, id: "1")
        XCTAssertEqual(a, b)
    }

    // MARK: - SSEError

    func testSSEErrorDescriptions() {
        XCTAssertNotNil(SSEError.invalidData.errorDescription)
        XCTAssertNotNil(SSEError.connectionLost.errorDescription)
        XCTAssertNotNil(SSEError.invalidURL.errorDescription)
    }

    // MARK: - StatusUpdate

    func testDecodeStatusUpdate() throws {
        let json = """
        {
            "running": true,
            "listen_addr": "127.0.0.1:8080",
            "timestamp": 1711180800,
            "upstream_stats": {
                "total_servers": 2,
                "connected_servers": 2,
                "quarantined_servers": 0,
                "total_tools": 15
            }
        }
        """
        let update = try decode(StatusUpdate.self, from: json)
        XCTAssertTrue(update.running)
        XCTAssertEqual(update.listenAddr, "127.0.0.1:8080")
        XCTAssertEqual(update.timestamp, 1711180800)
        XCTAssertNotNil(update.upstreamStats)
        XCTAssertEqual(update.upstreamStats?.totalServers, 2)
    }

    func testDecodeStatusUpdateMinimal() throws {
        let json = """
        {
            "running": false
        }
        """
        let update = try decode(StatusUpdate.self, from: json)
        XCTAssertFalse(update.running)
        XCTAssertNil(update.listenAddr)
        XCTAssertNil(update.timestamp)
        XCTAssertNil(update.upstreamStats)
    }

    // MARK: - APIResponse

    func testDecodeAPIResponseSuccess() throws {
        let json = """
        {
            "success": true,
            "data": {"running": true},
            "request_id": "req-123"
        }
        """
        let response = try decode(APIResponse<StatusResponse>.self, from: json)
        XCTAssertTrue(response.success)
        XCTAssertNotNil(response.data)
        XCTAssertTrue(response.data!.running)
        XCTAssertEqual(response.requestId, "req-123")
        XCTAssertNil(response.error)
    }

    func testDecodeAPIResponseError() throws {
        let json = """
        {
            "success": false,
            "error": "Server not found",
            "request_id": "req-456"
        }
        """
        let response = try decode(APIErrorResponse.self, from: json)
        XCTAssertFalse(response.success)
        XCTAssertEqual(response.error, "Server not found")
        XCTAssertEqual(response.requestId, "req-456")
    }

    // MARK: - ServersListResponse

    func testDecodeServersListResponse() throws {
        let json = """
        {
            "servers": [
                {
                    "id": "server-1",
                    "name": "server-1",
                    "protocol": "http",
                    "enabled": true,
                    "connected": true,
                    "quarantined": false,
                    "tool_count": 5
                },
                {
                    "id": "server-2",
                    "name": "server-2",
                    "protocol": "stdio",
                    "enabled": false,
                    "connected": false,
                    "quarantined": false,
                    "tool_count": 0
                }
            ]
        }
        """
        let response = try decode(ServersListResponse.self, from: json)
        XCTAssertEqual(response.servers.count, 2)
        XCTAssertEqual(response.servers[0].id, "server-1")
        XCTAssertEqual(response.servers[1].id, "server-2")
    }

    // MARK: - ServerActionResponse

    func testDecodeServerActionResponse() throws {
        let json = """
        {
            "message": "Server restarted",
            "server_name": "github"
        }
        """
        let response = try decode(ServerActionResponse.self, from: json)
        XCTAssertEqual(response.message, "Server restarted")
        XCTAssertEqual(response.serverName, "github")
    }

    func testDecodeServerActionResponseMinimal() throws {
        let json = """
        {
            "message": "OK"
        }
        """
        let response = try decode(ServerActionResponse.self, from: json)
        XCTAssertEqual(response.message, "OK")
        XCTAssertNil(response.serverName)
    }

    // MARK: - AdminState Enum

    func testAdminStateCaseIterable() {
        let allCases = AdminState.allCases
        XCTAssertEqual(allCases.count, 3)
        XCTAssertTrue(allCases.contains(.enabled))
        XCTAssertTrue(allCases.contains(.disabled))
        XCTAssertTrue(allCases.contains(.quarantined))
    }

    // MARK: - HealthAction Enum

    func testHealthActionCaseIterable() {
        let allCases = HealthAction.allCases
        XCTAssertEqual(allCases.count, 7)
    }

    func testHealthActionRawValues() {
        XCTAssertEqual(HealthAction.viewLogs.rawValue, "view_logs")
        XCTAssertEqual(HealthAction.setSecret.rawValue, "set_secret")
        XCTAssertEqual(HealthAction.login.rawValue, "login")
        XCTAssertEqual(HealthAction.restart.rawValue, "restart")
        XCTAssertEqual(HealthAction.enable.rawValue, "enable")
        XCTAssertEqual(HealthAction.approve.rawValue, "approve")
        XCTAssertEqual(HealthAction.configure.rawValue, "configure")
    }

    // MARK: - ActivityEntry glance accessors (spec 090, data-model.md)

    /// Builds an entry from a JSON dictionary so the accessors are exercised
    /// against decoded `JSONValue` metadata, exactly as the poll delivers it.
    private func entry(
        id: String = "e",
        type: String,
        status: String,
        metadata: [String: Any]? = nil,
        extra: [String: Any] = [:]
    ) throws -> ActivityEntry {
        var json: [String: Any] = [
            "id": id,
            "type": type,
            "status": status,
            "timestamp": "2026-03-23T12:00:00Z"
        ]
        if let metadata { json["metadata"] = metadata }
        for (key, value) in extra { json[key] = value }
        let data = try JSONSerialization.data(withJSONObject: json)
        return try JSONDecoder().decode(ActivityEntry.self, from: data)
    }

    func testIntentReasonComesFromTheIntentObject() throws {
        let call = try entry(type: "tool_call", status: "success",
                             metadata: ["intent": ["reason": "Verify the transition"]])
        XCTAssertEqual(call.intentReason, "Verify the transition")
        XCTAssertEqual(call.reason, "Verify the transition")
    }

    func testIntentReasonIsNilWhenAbsentOrEmpty() throws {
        XCTAssertNil(try entry(type: "tool_call", status: "success").intentReason)
        XCTAssertNil(try entry(type: "tool_call", status: "success",
                               metadata: ["intent": ["operation_type": "read"]]).intentReason)
        XCTAssertNil(try entry(type: "tool_call", status: "success",
                               metadata: ["intent": ["reason": ""]]).intentReason)
    }

    func testBlockReasonComesFromTheTopLevelReasonKey() throws {
        let blocked = try entry(type: "policy_decision", status: "blocked",
                                metadata: ["decision": "blocked",
                                           "reason": "Intent rejected: tool variant conflicts"])
        XCTAssertEqual(blocked.blockReason, "Intent rejected: tool variant conflicts")
    }

    func testBlockReasonIsNilWhenAbsentOrEmpty() throws {
        XCTAssertNil(try entry(type: "policy_decision", status: "blocked").blockReason)
        XCTAssertNil(try entry(type: "policy_decision", status: "blocked",
                               metadata: ["decision": "blocked", "reason": ""]).blockReason)
    }

    /// The pipeline-facing accessor: a policy row explains itself with the
    /// policy's reason, a call row with the caller's declared intent.
    func testReasonPrefersTheBlockReasonOnPolicyRecords() throws {
        let blocked = try entry(type: "policy_decision", status: "blocked",
                                metadata: ["decision": "blocked",
                                           "reason": "Quarantined server",
                                           "intent": ["reason": "Fetch the ticket"]])
        XCTAssertEqual(blocked.reason, "Quarantined server",
                       "a blocked row must explain the block, not the caller's plan")

        let call = try entry(type: "tool_call", status: "error",
                             metadata: ["reason": "unrelated", "intent": ["reason": "Fetch the ticket"]])
        XCTAssertEqual(call.reason, "Fetch the ticket")
    }

    func testReasonIsNilWhenNeitherSourceHasOne() throws {
        XCTAssertNil(try entry(type: "tool_call", status: "success").reason)
        XCTAssertNil(try entry(type: "policy_decision", status: "blocked").reason)
    }

    func testOutcomeClassSeparatesBlocksFromCalls() throws {
        for decision in ["blocked", "block"] {
            let record = try entry(type: "policy_decision", status: decision,
                                   metadata: ["decision": decision, "reason": "nope"])
            XCTAssertEqual(record.outcomeClass, .blocked, "decision \(decision) is a block")
        }

        XCTAssertEqual(try entry(type: "tool_call", status: "success").outcomeClass, .call)
        XCTAssertEqual(try entry(type: "tool_call", status: "error").outcomeClass, .call)
        XCTAssertEqual(try entry(type: "internal_tool_call", status: "error").outcomeClass, .call)
    }

    /// Warnings and redactions are policy decisions that did NOT stop the call,
    /// so they are not blocks (and, per FR-012, never become rows).
    func testNonBlockingPolicyDecisionsAreNotBlocks() throws {
        for decision in ["warning", "redacted", "allowed"] {
            let record = try entry(type: "policy_decision", status: decision,
                                   metadata: ["decision": decision, "reason": "fyi"])
            XCTAssertEqual(record.outcomeClass, .call, "decision \(decision) is not a block")
        }
    }

    /// Legacy/lossy records carry no `metadata.decision` — the persisted record's
    /// own status is the decision (`activity_service.go` writes `Status: decision`),
    /// so the class must fall back to it rather than silently declassifying a block.
    func testOutcomeClassFallsBackToStatusWhenDecisionMetadataIsAbsent() throws {
        XCTAssertEqual(try entry(type: "policy_decision", status: "blocked").outcomeClass, .blocked)
        XCTAssertEqual(try entry(type: "policy_decision", status: "block").outcomeClass, .blocked)
        XCTAssertEqual(try entry(type: "policy_decision", status: "warning").outcomeClass, .call)
    }

    /// The second half of the group key (FR-004): three coarse classes, so a
    /// success and a failure at the same tool can never share a run.
    func testStatusClassCoarsensTheStatusIntoThreeGroups() throws {
        XCTAssertEqual(try entry(type: "tool_call", status: "success").statusClass, .success)
        XCTAssertEqual(try entry(type: "tool_call", status: "error").statusClass, .failure)
        XCTAssertEqual(try entry(type: "tool_call", status: "running").statusClass, .pending)
        XCTAssertEqual(try entry(type: "tool_call", status: "pending").statusClass, .pending)
        // Everything the core may ever write that is neither of the two known
        // terminal statuses lands in one bucket, so an unrecognised status
        // never fragments a run record by record.
        XCTAssertEqual(try entry(type: "policy_decision", status: "blocked").statusClass, .pending)
        XCTAssertEqual(try entry(type: "tool_call", status: "something-new").statusClass, .pending)
    }

    /// A `parent_id` on the wire is the correlation id of the `code_execution`
    /// call that made this sub-call. It must survive decoding so the tray can
    /// route a child row back to its parent.
    func testActivityEntryDecodesParentID() throws {
        let child = try entry(type: "tool_call", status: "success",
                              extra: ["parent_id": "1770000000-abc-code_execution"])
        XCTAssertEqual(child.parentId, "1770000000-abc-code_execution")
        XCTAssertNil(try entry(type: "tool_call", status: "success").parentId,
                     "a record with no parent decodes with none")
    }
}
