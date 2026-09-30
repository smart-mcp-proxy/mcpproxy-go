import XCTest
@testable import MCPProxy

final class APIClientClientsTests: XCTestCase {
    override func setUp() {
        super.setUp()
        ConnectStubURLProtocol.reset()
    }

    override func tearDown() {
        ConnectStubURLProtocol.reset()
        super.tearDown()
    }

    func testClientsRequestsPresenceListAndDecodesConnectionEvidence() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope("""
        {"clients":[{"id":"claude-code","display_name":"Claude Code","kind":"supported",
        "icon":"claude-code","state":"connected_seen","installed":true,"connected":true,
        "last_seen":"2026-09-29T09:00:00Z","active_sessions":2,"calls_24h":17,
        "display_path":"~/.claude.json","reload_hint":"Restart Claude Code"}]}
        """)

        let rows = try await ConnectStubURLProtocol.makeClient().clients()

        XCTAssertEqual(ConnectStubURLProtocol.recorded.last?.url, "http://127.0.0.1:8080/api/v1/clients")
        XCTAssertEqual(rows.count, 1)
        XCTAssertEqual(rows[0].stateLabel, "Connected")
        XCTAssertEqual(rows[0].calls24h, 17)
        XCTAssertEqual(rows[0].effectiveDisplayPath, "~/.claude.json")
    }

    func testClientDetailRequestsEncodedIDAndDecodesRecentSessions() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope("""
        {"id":"other:my client","display_name":"My Client","kind":"other","state":"other",
        "installed":false,"connected":false,"active_sessions":1,"calls_24h":1,
        "sessions":[{"id":"transport-1","work_session_id":"ws-1","started_at":"2026-09-29T08:00:00Z","last_activity":"2026-09-29T09:00:00Z"}]}
        """)

        let row = try await ConnectStubURLProtocol.makeClient().clientPresence("other:my client")

        XCTAssertEqual(ConnectStubURLProtocol.recorded.last?.url, "http://127.0.0.1:8080/api/v1/clients/other%3Amy%20client")
        XCTAssertEqual(row.sessions?.first?.displayID, "ws-1")
        XCTAssertEqual(row.stateLabel, "Observed")
    }

    func testRoutingDecodesPathsAndRequestsRoutingContract() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope("""
        {"routing_mode":"retrieve_tools","description":"Search first","endpoints":{"default":"/mcp","direct":"/mcp/all","code_execution":"/mcp/code","retrieve_tools":"/mcp/call"},"available_modes":["retrieve_tools","direct","code_execution"],"pending_routing_mode":"direct","restart_required":true,"code_execution_enabled":false}
        """)

        let routing = try await ConnectStubURLProtocol.makeClient().routing()

        XCTAssertEqual(ConnectStubURLProtocol.recorded.last?.url, "http://127.0.0.1:8080/api/v1/routing")
        XCTAssertEqual(routing.servedModeLabel, "Retrieve tools")
        XCTAssertEqual(routing.pendingRoutingMode, "direct")
        XCTAssertEqual(routing.endpoints.rows.map(\.path), ["/mcp", "/mcp/all", "/mcp/code", "/mcp/call"])
    }

    func testEndpointURLBuildsCopyableAbsoluteClientEndpoint() async throws {
        let client = ConnectStubURLProtocol.makeClient()

        let url = await client.endpointURL("/mcp")

        XCTAssertEqual(url, "http://127.0.0.1:8080/mcp")
    }

    func testEndpointURLUsesObservedCustomListenAddress() {
        XCTAssertEqual(
            APIClient.endpointURL("/mcp", baseURL: "http://127.0.0.1:18765"),
            "http://127.0.0.1:18765/mcp"
        )
    }

    func testRoutingModePatchUsesTheConfigMergeEndpoint() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope(
            #"{"requires_restart":true,"changed_fields":["routing_mode"]}"#
        )

        _ = try await ConnectStubURLProtocol.makeClient().patchConfig(["routing_mode": "direct"])

        let request = try XCTUnwrap(ConnectStubURLProtocol.recorded.last)
        XCTAssertEqual(request.url, "http://127.0.0.1:8080/api/v1/config")
        XCTAssertEqual(request.method, "PATCH")
        XCTAssertEqual(request.json["routing_mode"] as? String, "direct")
    }
}
