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

    /// The page is the endpoint's maximum, spelled out here rather than
    /// interpolated: `AppState.glanceActivityPageSize` is only meaningful
    /// because the server clamps `limit` to 100, and a test that reads the
    /// constant back would follow it silently wherever it went.
    func testGlanceActivityRequestsToolCallTypesAtTheServersMaximumPage() async throws {
        GlanceStubURLProtocol.responseBody = GlanceStubURLProtocol.envelope("""
        {"activities":[],"total":0,"limit":100,"offset":0}
        """)
        let client = GlanceStubURLProtocol.makeClient()

        _ = try await client.glanceActivity()

        XCTAssertEqual(
            GlanceStubURLProtocol.requestedURLs,
            ["http://127.0.0.1:8080/api/v1/activity?type=tool_call,internal_tool_call&limit=100"]
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
        // The two fields the whole feature branches on, and the two this test
        // used not to assert: `type` drives rules 1-3 and rowLabel's
        // `server:tool` decision, `status` drives the symbol, the tint, the
        // VoiceOver phrasing and the error clause. Decoded wrong, every one of
        // them is wrong, and no other test sees the wire format.
        XCTAssertEqual(entries.first?.type, "tool_call")
        XCTAssertEqual(entries.first?.status, "error")
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

    // MARK: - Non-2xx error path

    /// Unwrap an APIClientError.httpError, failing the test on any other outcome.
    private func expectHTTPError(
        _ body: @autoclosure () async throws -> Any,
        file: StaticString = #filePath,
        line: UInt = #line
    ) async -> (statusCode: Int, message: String)? {
        do {
            _ = try await body()
            XCTFail("expected the call to throw, but it returned normally", file: file, line: line)
            return nil
        } catch let error as APIClientError {
            guard case .httpError(let statusCode, let message) = error else {
                XCTFail("expected .httpError, got \(error)", file: file, line: line)
                return nil
            }
            return (statusCode, message)
        } catch {
            XCTFail("expected APIClientError, got \(error)", file: file, line: line)
            return nil
        }
    }

    /// The live core returns 400 for `?status=bogus`. The failure must surface as
    /// an httpError keyed off the status code — note the error body also carries
    /// `request_id`, which must not disturb the message extraction.
    func testActiveSessionsSurfaces400AsHTTPError() async throws {
        GlanceStubURLProtocol.statusCode = 400
        GlanceStubURLProtocol.responseBody = Data("""
        {"success":false,"error":"invalid status filter: bogus","request_id":"req-7"}
        """.utf8)
        let client = GlanceStubURLProtocol.makeClient()

        let failure = await expectHTTPError(try await client.activeSessions())

        XCTAssertEqual(failure?.statusCode, 400)
        XCTAssertEqual(failure?.message, "invalid status filter: bogus")
    }

    func testUsageAggregateSurfaces500AsHTTPError() async throws {
        GlanceStubURLProtocol.statusCode = 500
        GlanceStubURLProtocol.responseBody = Data("""
        {"success":false,"error":"usage snapshot unavailable","request_id":"req-8"}
        """.utf8)
        let client = GlanceStubURLProtocol.makeClient()

        let failure = await expectHTTPError(try await client.usageAggregate())

        XCTAssertEqual(failure?.statusCode, 500)
        XCTAssertEqual(failure?.message, "usage snapshot unavailable")
    }

    /// A non-2xx with a body that is not the JSON error envelope must still fail
    /// as an httpError, not as a decoding error.
    func testGlanceActivitySurfacesNonJSONErrorBodyAsHTTPError() async throws {
        GlanceStubURLProtocol.statusCode = 503
        GlanceStubURLProtocol.responseBody = Data("upstream unavailable".utf8)
        let client = GlanceStubURLProtocol.makeClient()

        let failure = await expectHTTPError(try await client.glanceActivity())

        XCTAssertEqual(failure?.statusCode, 503)
        XCTAssertEqual(failure?.message, HTTPURLResponse.localizedString(forStatusCode: 503))
    }

    // MARK: - Decoding-error fidelity

    /// Unwrap an APIClientError.decodingError, failing the test on any other outcome.
    private func expectDecodingError(
        _ body: @autoclosure () async throws -> Any,
        file: StaticString = #filePath,
        line: UInt = #line
    ) async -> String? {
        do {
            _ = try await body()
            XCTFail("expected the call to throw, but it returned normally", file: file, line: line)
            return nil
        } catch let error as APIClientError {
            guard case .decodingError(let underlying) = error else {
                XCTFail("expected .decodingError, got \(error)", file: file, line: line)
                return nil
            }
            return "\(underlying)"
        } catch {
            XCTFail("expected APIClientError, got \(error)", file: file, line: line)
            return nil
        }
    }

    /// A malformed field inside an enveloped `data` must surface the error that
    /// describes it. fetchWrapped's bare-decode fallback re-fails on the top-level
    /// shape, and reporting *that* second error hides the real cause entirely.
    func testEnvelopedBodyPreservesTheInnerDecodingError() async throws {
        GlanceStubURLProtocol.responseBody = GlanceStubURLProtocol.envelope("""
        {"window":"24h","token_source":"bytes","tokens_saved":0,
         "tokens_saved_percentage":0,"tools":[],"timeline":[
           {"start":"29/07/2026 13:00","calls":1,"errors":0,"total_resp_bytes":0}
         ]}
        """)
        let client = GlanceStubURLProtocol.makeClient()

        let description = await expectDecodingError(try await client.usageAggregate())

        XCTAssertEqual(
            description?.contains("Not an RFC 3339 timestamp: 29/07/2026 13:00"), true,
            "the timestamp error was masked; got: \(description ?? "nil")"
        )
    }

    /// No-regression guard for the other side of the fallback: when the body is
    /// genuinely NOT enveloped, the bare decode's error is the informative one and
    /// must still be what surfaces.
    func testUnwrappedBodyPreservesTheBareDecodingError() async throws {
        GlanceStubURLProtocol.responseBody = Data("""
        {"activities":"not-an-array","total":0,"limit":50,"offset":0}
        """.utf8)
        let client = GlanceStubURLProtocol.makeClient()

        let description = await expectDecodingError(try await client.glanceActivity())

        XCTAssertEqual(
            description?.contains("activities"), true,
            "the bare-decode error was masked; got: \(description ?? "nil")"
        )
    }

    /// The fallback itself must keep working: an unwrapped body that decodes
    /// cleanly is still accepted.
    func testUnwrappedBodyStillDecodesSuccessfully() async throws {
        GlanceStubURLProtocol.responseBody = Data("""
        {"activities":[
          {"id":"01K","type":"tool_call","status":"ok","timestamp":"2026-07-29T13:04:05Z",
           "server_name":"gh","tool_name":"list_prs"}
        ],"total":1,"limit":50,"offset":0}
        """.utf8)
        let client = GlanceStubURLProtocol.makeClient()

        let entries = try await client.glanceActivity()

        XCTAssertEqual(entries.count, 1)
        XCTAssertEqual(entries.first?.serverName, "gh")
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
