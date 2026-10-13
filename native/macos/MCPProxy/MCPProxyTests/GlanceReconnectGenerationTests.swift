import XCTest
@testable import MCPProxy

/// A glance fetch issued to one core must never publish into another.
///
/// `testLateFetchDoesNotRepopulateAfterDisconnect` pins the easy half of this:
/// a response that resolves while the tray is disconnected is dropped by the
/// `coreState == .connected` guard. The half that guard cannot see is a
/// response that resolves AFTER a reconnect has already restored `.connected`
/// — the predicate is true again, so the previous core's data publishes over
/// the new core's. The refresh loop reconnects in well under the 30s poll
/// interval, so the window is a whole poll cycle wide, not a few milliseconds.
@MainActor
final class GlanceReconnectGenerationTests: XCTestCase {

    /// A source whose fetch takes the core down and brings it back before it
    /// answers — the interleaving a `.connected`-only guard cannot detect.
    private final class ReconnectingSource: GlanceDataSource {
        var state: AppState?
        var entries: [ActivityEntry] = []
        var sessions: [APIClient.MCPSession] = []
        var timeline: [UsageBucket] = []
        /// Thrown instead of answering, to cover the catch blocks too.
        var failure: Error?

        private func reconnectThenAnswer() throws {
            if let state {
                state.coreState = .reconnecting(attempt: 1)
                state.coreState = .connected
            }
            if let failure { throw failure }
        }

        func usageAggregate(window: String, top: Int) async throws -> UsageAggregateResponse {
            try reconnectThenAnswer()
            return UsageAggregateResponse(window: "24h", tokenSource: "bytes", tokensSaved: 0,
                                          tokensSavedPercentage: 0, timeline: timeline)
        }
        func glanceActivity(limit: Int) async throws -> [ActivityEntry] {
            try reconnectThenAnswer()
            return entries
        }
        func recentSessions(limit: Int) async throws -> [APIClient.MCPSession] {
            try reconnectThenAnswer()
            return sessions
        }
    }

    private func connectedState() -> AppState {
        let state = AppState()
        state.coreState = .connected
        return state
    }

    private func source(for state: AppState) -> ReconnectingSource {
        let source = ReconnectingSource()
        source.state = state
        return source
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

    // MARK: - The generation itself

    func testEachStateChangeStartsANewConnectionGeneration() {
        let state = AppState()
        let first = state.connectionGeneration

        state.coreState = .connected

        XCTAssertNotEqual(state.connectionGeneration, first)
        XCTAssertTrue(state.isCurrentConnection(state.connectionGeneration))
        XCTAssertFalse(state.isCurrentConnection(first))
    }

    /// Re-assigning the same state is not a new connection: the refresh loop
    /// would otherwise throw away a perfectly good in-flight response every
    /// time something re-published an unchanged `coreState`.
    func testRepublishingTheSameStateKeepsTheGeneration() {
        let state = connectedState()
        let generation = state.connectionGeneration

        state.coreState = .connected

        XCTAssertEqual(state.connectionGeneration, generation)
    }

    /// A disconnected tray is nobody's current connection, whatever the number.
    func testNoGenerationIsCurrentWhileDisconnected() {
        let state = AppState()
        XCTAssertFalse(state.isCurrentConnection(state.connectionGeneration))
    }

    // MARK: - The three fetches

    func testAnActivityFetchFromThePreviousCoreDoesNotPublish() async throws {
        let state = connectedState()
        let source = source(for: state)
        source.entries = [try Self.entry(id: "from-the-dead-core")]

        await state.refreshGlanceActivity(from: source)

        XCTAssertTrue(state.glanceActivity.isEmpty,
                      "the previous core's rows published into the reconnected core")
    }

    func testASessionsFetchFromThePreviousCoreDoesNotPublish() async throws {
        let state = connectedState()
        let source = source(for: state)
        source.sessions = [try Self.session(id: "from-the-dead-core")]

        await state.refreshGlanceSessions(from: source)

        XCTAssertTrue(state.glanceSessions.isEmpty,
                      "the previous core's clients published into the reconnected core")
    }

    func testAUsageFetchFromThePreviousCoreDoesNotPublish() async {
        let state = connectedState()
        let source = source(for: state)
        source.timeline = [UsageBucket(start: Date(), calls: 99, errors: 0, totalRespBytes: 0)]

        await state.refreshUsage(from: source)

        XCTAssertNil(state.usageTimeline,
                     "the previous core's usage published into the reconnected core")
        XCTAssertNil(state.callsThisHour)
    }

    /// The failure paths need the same guard: a dead core's error recorded
    /// against the new connection would mark a healthy core as failing, and the
    /// streak is what decides whether the block admits it is stale.
    func testAFailureFromThePreviousCoreIsNotRecorded() async {
        let state = connectedState()
        let source = source(for: state)
        source.failure = APIClientError.notReady

        await state.refreshGlanceActivity(from: source)
        await state.refreshGlanceSessions(from: source)
        await state.refreshUsage(from: source)

        XCTAssertNil(state.glanceError)
        XCTAssertTrue(state.glanceFailureStreak.isEmpty)
        XCTAssertNil(state.usageError)
    }

    // MARK: - The SSE path

    /// An SSE row belongs to the stream that delivered it, and that stream
    /// belongs to one connection. The publish happens a MainActor hop after the
    /// event is read, and executors guarantee no ordering across that hop
    /// against the reconnect work — so an event from the previous core can
    /// resume after `.connected` has been restored and prepend a dead core's
    /// call to the new core's feed.
    ///
    /// The disconnected case was already covered; this is the one that needs a
    /// generation, because `.connected` is true again by the time it publishes.
    func testAnSSERowFromThePreviousCoreDoesNotPublish() throws {
        let state = connectedState()
        let generation = state.connectionGeneration

        // The core dies and comes back while the event is in flight.
        state.coreState = .reconnecting(attempt: 1)
        state.coreState = .connected

        state.prependGlanceActivity(try Self.entry(id: "from-the-dead-core"),
                                    generation: generation)

        XCTAssertTrue(state.glanceActivity.isEmpty,
                      "the previous core's SSE row published into the reconnected core")
    }

    /// Positive control for the same call: an event from the live connection
    /// still reaches the feed.
    ///
    /// Note what this pair does NOT cover, which is why `SSEStreamSessionTests`
    /// exists: both call the publish helper directly with a generation the test
    /// chose, so they pin the guard and say nothing about where production reads
    /// that generation. They pass unchanged against the version that reads it
    /// when the event arrives — the regression the guard was written for.
    func testAnSSERowFromTheLiveConnectionPublishes() throws {
        let state = connectedState()

        state.prependGlanceActivity(try Self.entry(id: "live"),
                                    generation: state.connectionGeneration)

        XCTAssertEqual(state.glanceActivity.map(\.id), ["live"])
    }

    /// Positive control: with no reconnection the very same fetches publish, so
    /// the tests above are pinning the reconnect, not a broken refresh path.
    func testTheSameFetchesPublishWhenTheConnectionHolds() async throws {
        let state = connectedState()
        let source = ReconnectingSource()   // no AppState, so it never reconnects
        source.entries = [try Self.entry(id: "a1")]
        source.sessions = [try Self.session(id: "s1")]

        await state.refreshGlanceActivity(from: source)
        await state.refreshGlanceSessions(from: source)

        XCTAssertEqual(state.glanceActivity.map(\.id), ["a1"])
        XCTAssertEqual(state.glanceSessions.map(\.id), ["s1"])
    }
}
