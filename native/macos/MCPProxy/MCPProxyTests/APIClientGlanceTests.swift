import XCTest
@testable import MCPProxy

/// Request-shape and decoding tests for the three tray-glance API calls.
final class APIClientGlanceTests: XCTestCase {

    override func setUp() {
        super.setUp()
        GlanceStubURLProtocol.reset()
    }

    override func tearDown() {
        GlanceStubURLProtocol.reset()
        super.tearDown()
    }

    // MARK: - Usage aggregate

    func testUsageAggregateRequestsWindowAndTop() async throws {
        GlanceStubURLProtocol.responseBody = GlanceStubURLProtocol.envelope("""
        {"window":"24h","token_source":"bytes","tokens_saved":184320,
         "tokens_saved_percentage":92.4,"tools":[],"timeline":[]}
        """)
        let client = GlanceStubURLProtocol.makeClient()

        _ = try await client.usageAggregate()

        XCTAssertEqual(
            GlanceStubURLProtocol.requestedURLs,
            ["http://127.0.0.1:8080/api/v1/activity/usage?window=24h&top=1"]
        )
    }

    func testUsageAggregateDecodesTimelineBuckets() async throws {
        GlanceStubURLProtocol.responseBody = GlanceStubURLProtocol.envelope("""
        {"window":"24h","token_source":"bytes","tokens_saved":0,
         "tokens_saved_percentage":0,"tools":[],"timeline":[
           {"start":"2026-07-29T13:00:00Z","calls":12,"errors":2,"total_resp_bytes":4096}
         ]}
        """)
        let client = GlanceStubURLProtocol.makeClient()

        let usage = try await client.usageAggregate()

        XCTAssertEqual(usage.window, "24h")
        XCTAssertEqual(usage.tokensSaved, 0)
        XCTAssertEqual(usage.timeline.count, 1)
        let bucket = try XCTUnwrap(usage.timeline.first)
        XCTAssertEqual(bucket.calls, 12)
        XCTAssertEqual(bucket.errors, 2)
        XCTAssertEqual(bucket.totalRespBytes, 4096)
        // 2026-07-29T13:00:00Z as seconds since the epoch.
        XCTAssertEqual(bucket.start.timeIntervalSince1970, 1785330000, accuracy: 0.5)
    }

    func testUsageBucketDecodesFractionalSecondTimestamps() throws {
        let json = Data("""
        {"start":"2026-07-29T13:00:00.123Z","calls":1,"errors":0,"total_resp_bytes":0}
        """.utf8)

        let bucket = try JSONDecoder().decode(UsageBucket.self, from: json)

        // Tolerance must stay well under the .123s being asserted, or the test
        // passes even when the fractional-seconds branch is dropped.
        XCTAssertEqual(bucket.start.timeIntervalSince1970, 1785330000.123, accuracy: 0.001)
    }

    // MARK: - Glance activity

    func testGlanceActivityRequestsToolCallTypesWithOversizedPage() async throws {
        GlanceStubURLProtocol.responseBody = GlanceStubURLProtocol.envelope("""
        {"activities":[],"total":0,"limit":50,"offset":0}
        """)
        let client = GlanceStubURLProtocol.makeClient()

        _ = try await client.glanceActivity()

        XCTAssertEqual(
            GlanceStubURLProtocol.requestedURLs,
            ["http://127.0.0.1:8080/api/v1/activity?type=tool_call,internal_tool_call&limit=50"]
        )
    }

    func testGlanceActivityDecodesEntries() async throws {
        GlanceStubURLProtocol.responseBody = GlanceStubURLProtocol.envelope("""
        {"activities":[
          {"id":"01J","type":"tool_call","status":"error","timestamp":"2026-07-29T13:04:05Z",
           "server_name":"jira","tool_name":"get_issue","error_message":"auth failed",
           "request_id":"req-1"}
        ],"total":1,"limit":50,"offset":0}
        """)
        let client = GlanceStubURLProtocol.makeClient()

        let entries = try await client.glanceActivity()

        XCTAssertEqual(entries.count, 1)
        XCTAssertEqual(entries.first?.serverName, "jira")
        XCTAssertEqual(entries.first?.toolName, "get_issue")
        XCTAssertEqual(entries.first?.errorMessage, "auth failed")
    }

    // MARK: - Active sessions

    func testActiveSessionsRequestsStatusActive() async throws {
        GlanceStubURLProtocol.responseBody = GlanceStubURLProtocol.envelope("""
        {"sessions":[],"total":0,"limit":25}
        """)
        let client = GlanceStubURLProtocol.makeClient()

        _ = try await client.activeSessions()

        XCTAssertEqual(
            GlanceStubURLProtocol.requestedURLs,
            ["http://127.0.0.1:8080/api/v1/sessions?status=active&limit=25"]
        )
    }

    /// Regression: the model decoded `last_active`, but the API emits
    /// `last_activity`, so every session's timestamp silently arrived as nil.
    func testMCPSessionDecodesLastActivity() throws {
        let json = Data("""
        {"id":"sess-1","client_name":"Claude Code","status":"active",
         "tool_call_count":8,"start_time":"2026-07-29T12:00:00Z",
         "last_activity":"2026-07-29T13:04:05Z"}
        """.utf8)

        let session = try JSONDecoder().decode(APIClient.MCPSession.self, from: json)

        XCTAssertEqual(session.lastActivity, "2026-07-29T13:04:05Z")
        XCTAssertEqual(session.clientName, "Claude Code")
        XCTAssertEqual(session.toolCallCount, 8)
    }

    // MARK: - Data-source seam

    func testAPIClientConformsToGlanceDataSource() async throws {
        GlanceStubURLProtocol.responseBody = GlanceStubURLProtocol.envelope("""
        {"sessions":[],"total":0,"limit":25}
        """)
        let source: any GlanceDataSource = GlanceStubURLProtocol.makeClient()

        _ = try await source.activeSessions(limit: 25)

        XCTAssertEqual(GlanceStubURLProtocol.requestedURLs.count, 1)
    }

    func testCountingStubSatisfiesTheProtocolAndIssuesNoRequests() async throws {
        let stub = CountingGlanceDataSource()
        let source: any GlanceDataSource = stub

        _ = try await source.usageAggregate(window: "24h", top: 1)
        _ = try await source.glanceActivity(limit: 50)
        _ = try await source.activeSessions(limit: 25)

        XCTAssertEqual(stub.totalCallCount, 3)
        XCTAssertTrue(GlanceStubURLProtocol.requestedURLs.isEmpty)
    }
}
