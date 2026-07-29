import XCTest
@testable import MCPProxy

/// The activity and sessions refreshes, on the failure path.
///
/// Both used to fetch through the concrete `APIClient` inside
/// `CoreProcessManager` and swallow their errors with a bare
/// `// Non-fatal` — so a permanently failing fetch rendered "No tool calls yet"
/// and "No connected clients", which is exactly what a genuinely idle proxy
/// renders. `usageError` exists to stop the third feed telling that same quiet
/// lie; these tests exist so the other two cannot tell it either.
@MainActor
final class GlanceRefreshFailureTests: XCTestCase {

    /// A data source that can fail per feed.
    private final class StubSource: GlanceDataSource {
        var activityError: Error?
        var sessionsError: Error?
        var entries: [ActivityEntry] = []
        var sessions: [APIClient.MCPSession] = []

        func usageAggregate(window: String, top: Int) async throws -> UsageAggregateResponse {
            UsageAggregateResponse(window: "24h", tokenSource: "bytes", tokensSaved: 0,
                                   tokensSavedPercentage: 0, timeline: [])
        }
        func glanceActivity(limit: Int) async throws -> [ActivityEntry] {
            if let activityError { throw activityError }
            return entries
        }
        func activeSessions(limit: Int) async throws -> [APIClient.MCPSession] {
            if let sessionsError { throw sessionsError }
            return sessions
        }
    }

    private func connectedState() -> AppState {
        let state = AppState()
        state.coreState = .connected
        return state
    }

    private static func entry(id: String) throws -> ActivityEntry {
        let json = """
        {"id":"\(id)","type":"tool_call","status":"success","timestamp":"2026-07-29T11:00:00Z",
         "server_name":"srv","tool_name":"t","request_id":"\(id)"}
        """
        // swiftlint:disable:next force_unwrapping
        return try JSONDecoder().decode(ActivityEntry.self, from: json.data(using: .utf8)!)
    }

    private static func session(id: String) throws -> APIClient.MCPSession {
        let json = """
        {"id":"\(id)","client_name":"Claude Code","status":"active","tool_call_count":3}
        """
        // swiftlint:disable:next force_unwrapping
        return try JSONDecoder().decode(APIClient.MCPSession.self, from: json.data(using: .utf8)!)
    }

    // MARK: - Failures reach state

    func testAFailedActivityRefreshIsRecorded() async {
        let state = connectedState()
        let source = StubSource()
        source.activityError = APIClientError.httpError(statusCode: 503, message: "core restarting")

        await state.refreshGlanceActivity(from: source)

        XCTAssertEqual(state.glanceFailureStreak[.activity], 1)
        XCTAssertEqual(state.glanceError, "HTTP 503: core restarting")
    }

    func testAFailedSessionsRefreshIsRecorded() async {
        let state = connectedState()
        let source = StubSource()
        source.sessionsError = APIClientError.notReady

        await state.refreshGlanceSessions(from: source)

        XCTAssertEqual(state.glanceFailureStreak[.sessions], 1)
        XCTAssertNotNil(state.glanceError)
    }

    /// Consecutive failures accumulate — one blip and a core that has been gone
    /// for minutes must be distinguishable, which is what the streak is for.
    func testConsecutiveFailuresAccumulate() async {
        let state = connectedState()
        let source = StubSource()
        source.activityError = APIClientError.noData

        await state.refreshGlanceActivity(from: source)
        await state.refreshGlanceActivity(from: source)
        await state.refreshGlanceActivity(from: source)

        XCTAssertEqual(state.glanceFailureStreak[.activity], 3)
    }

    /// …and a success resets that feed's streak, so a recovered core stops
    /// being reported as failing.
    func testASuccessfulRefreshResetsTheStreak() async throws {
        let state = connectedState()
        let source = StubSource()
        source.activityError = APIClientError.noData
        await state.refreshGlanceActivity(from: source)
        XCTAssertEqual(state.glanceFailureStreak[.activity], 1)

        source.activityError = nil
        source.entries = [try Self.entry(id: "a1")]
        await state.refreshGlanceActivity(from: source)

        XCTAssertEqual(state.glanceFailureStreak[.activity], 0)
        XCTAssertNil(state.glanceError)
        XCTAssertEqual(state.glanceActivity.map(\.id), ["a1"])
    }

    /// One feed's success must not clear another feed's failure: they fail
    /// independently, and a per-feed streak that any success reset would report
    /// a persistently failing feed as healthy every 30 seconds.
    func testOneFeedsSuccessDoesNotResetAnothersStreak() async throws {
        let state = connectedState()
        let source = StubSource()
        source.activityError = APIClientError.noData
        source.sessions = [try Self.session(id: "s1")]

        await state.refreshGlanceActivity(from: source)
        await state.refreshGlanceSessions(from: source)

        XCTAssertEqual(state.glanceFailureStreak[.activity], 1)
        XCTAssertEqual(state.glanceFailureStreak[.sessions], 0)
    }

    /// The same guard the three publish helpers carry: a fetch already past its
    /// `guard let apiClient` when the core goes away resolves into its catch
    /// block after `clearGlanceState()`, and a dead core's failure must not
    /// outlive it.
    func testAFailureIsIgnoredWhileDisconnected() async {
        let state = AppState()
        let source = StubSource()
        source.activityError = APIClientError.noData

        await state.refreshGlanceActivity(from: source)

        XCTAssertNil(state.glanceError)
        XCTAssertNil(state.glanceFailureStreak[.activity])
    }

    /// Disconnecting clears the recorded failure along with the feeds it
    /// describes — the next core starts from "loading", not from the previous
    /// core's error.
    func testClearGlanceStateClearsTheFailureRecord() async {
        let state = connectedState()
        let source = StubSource()
        source.activityError = APIClientError.noData
        await state.refreshGlanceActivity(from: source)

        state.coreState = .idle

        XCTAssertNil(state.glanceError)
        XCTAssertTrue(state.glanceFailureStreak.isEmpty)
    }

    // MARK: - The success path still publishes

    func testASuccessfulSessionsRefreshPublishesTheSessions() async throws {
        let state = connectedState()
        let source = StubSource()
        source.sessions = [try Self.session(id: "s1")]

        await state.refreshGlanceSessions(from: source)

        XCTAssertEqual(state.glanceSessions.map(\.id), ["s1"])
    }
}
