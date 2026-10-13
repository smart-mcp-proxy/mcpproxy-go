import XCTest
import AppKit
@testable import MCPProxy

/// The header must never contradict the rows under it (GH #934).
///
/// Two independent ways it could, both observed in live QA:
///
/// 1. The Clients header counts every client the proxy has seen while the list
///    shows at most five, so "11 clients" sat above five rows with nothing to
///    say the list was cut.
/// 2. The call count comes from the 30-second usage poll while the rows arrive
///    over SSE, so a call that already has a row is not in the number above it
///    for up to half a minute — "12 calls this hour" above a five-second-old
///    thirteenth call.
///
/// Both are cosmetic and both cost trust in the one block whose entire job is to
/// be glanced at, so they are pinned here rather than left to a screenshot.
@MainActor
final class GlanceHeaderConsistencyTests: XCTestCase {

    static let now = GlanceFormatting.parseTimestamp("2027-01-15T08:00:00Z")!

    private static let clickStub = ClickTarget()

    final class ClickTarget: NSObject {
        @objc func openGlanceRow(_ sender: NSMenuItem) {}
    }

    private static func makeSection() -> GlanceSection {
        GlanceSection(target: clickStub, action: #selector(ClickTarget.openGlanceRow(_:)))
    }

    // MARK: - The list says how much of itself it is showing

    /// Eleven clients, five rows: the block must admit the other six exist.
    /// Without the overflow row the header ("8 active · 3 idle") is simply a
    /// bigger number than the list, with nothing to explain the difference.
    func testTruncatedClientListDeclaresHowManyItLeftOut() {
        let state = Self.stateWithClients(11)
        let items = Self.makeSection().items(for: state, now: Self.now)
        let titles = items.map { $0.isSeparatorItem ? "—" : $0.title }

        XCTAssertEqual(titles.filter { $0.hasPrefix("client-") }.count, 5,
                       "precondition: the list itself is still capped at five rows")
        guard let overflow = items.first(where: { $0.title == "+6 more" }) else {
            return XCTFail("a truncated client list must carry a '+N more' row, got \(titles)")
        }
        XCTAssertFalse(overflow.isEnabled, "the overflow row is a muted admission, not an action")
        XCTAssertNil(overflow.action)
    }

    /// …and says nothing when there is nothing to admit. An always-present
    /// "+0 more" would be its own kind of noise.
    func testAFullyVisibleClientListHasNoOverflowRow() {
        let state = Self.stateWithClients(5)
        let titles = Self.makeSection().items(for: state, now: Self.now).map(\.title)
        XCTAssertFalse(titles.contains { $0.hasSuffix(" more") },
                       "five clients fit in five rows, got \(titles)")
    }

    /// The overflow count is live: a twelfth client appearing must not leave
    /// "+6 more" on screen under a header that now counts twelve.
    func testTheOverflowCountIsRewrittenInPlace() {
        let state = Self.stateWithClients(11)
        let section = Self.makeSection()
        _ = section.items(for: state, now: Self.now)

        state.glanceSessions = Self.sessions(12)
        XCTAssertTrue(section.updateInPlace(for: state, now: Self.now),
                      "one more client beyond the cap changes no row COUNT, so it is in-place")
        XCTAssertEqual(section.clientOverflowTitleForTesting, "+7 more")
    }

    /// Crossing the cap changes the number of rows, which resizes a menu the
    /// user may have open — so it waits for close like every other structural
    /// change (FR-023).
    func testGainingAnOverflowRowIsStructural() {
        let state = Self.stateWithClients(5)
        let section = Self.makeSection()
        _ = section.items(for: state, now: Self.now)

        state.glanceSessions = Self.sessions(6)
        XCTAssertFalse(section.updateInPlace(for: state, now: Self.now),
                       "the block just gained a row; that is a rebuild, not a rewrite")
    }

    // MARK: - The call count counts the rows it sits above

    /// A live SSE call lands in the rows immediately. The header must move with
    /// it instead of waiting for the next usage poll.
    func testALiveCallIsCountedInTheHeaderBeforeTheNextPoll() {
        let state = Self.connectedState()
        state.updateUsage(timeline: [Self.bucket(calls: 12)], now: Self.now,
                          polledAt: Self.now.addingTimeInterval(-1))
        XCTAssertEqual(Self.makeSection().items(for: state, now: Self.now).first?.title,
                       "12 calls in the last 24h · 1 active",
                       "precondition: the polled count is what the header shows")

        state.prependGlanceActivity(
            GlanceFixtures.entry(id: "live", server: "github", tool: "create_issue",
                                 timestamp: "2027-01-15T08:00:00Z", session: "sess-a"),
            generation: state.connectionGeneration
        )

        XCTAssertEqual(Self.makeSection().items(for: state, now: Self.now).first?.title,
                       "13 calls in the last 24h · 1 active",
                       "the row is on screen, so the number above it has to include it")
    }

    /// The live increment is not a second source of truth: the next poll's
    /// answer replaces it outright, so the two can never accumulate.
    func testTheNextPollSupersedesTheLiveIncrement() {
        let state = Self.connectedState()
        state.updateUsage(timeline: [Self.bucket(calls: 12)], now: Self.now,
                          polledAt: Self.now.addingTimeInterval(-1))
        state.prependGlanceActivity(
            GlanceFixtures.entry(id: "live", server: "github", tool: "create_issue",
                                 timestamp: "2027-01-15T08:00:00Z", session: "sess-a"),
            generation: state.connectionGeneration
        )
        XCTAssertEqual(state.glanceCallsThisHour(now: Self.now), 13)

        // The poll that has now seen the same call: issued after it happened.
        state.updateUsage(timeline: [Self.bucket(calls: 13)], now: Self.now, polledAt: Self.now)
        XCTAssertEqual(state.glanceCallsThisHour(now: Self.now), 13,
                       "the poll's count already includes the live call; adding it twice "
                       + "is the drift this fix exists to remove")
    }

    /// A poll is an `await` across the network, and SSE rows land on the same
    /// actor while it is in flight. A response that snapshotted the core BEFORE
    /// a live call happened cannot answer for that call, so it must not delete
    /// it: the header dropping from 13 back to 12 under a visible thirteenth row
    /// — and staying there for up to 30 seconds — is the same inconsistency
    /// #934 is about, arriving from the other direction.
    func testAPollAlreadyInFlightDoesNotEraseACallItCouldNotHaveSeen() {
        let state = Self.connectedState()
        state.updateUsage(timeline: [Self.bucket(calls: 12)], now: Self.now,
                          polledAt: Self.now.addingTimeInterval(-60))

        // The next poll is issued…
        let issuedAt = Self.now
        // …a call arrives five seconds later, while it is still in flight…
        state.prependGlanceActivity(
            GlanceFixtures.entry(id: "live", server: "github", tool: "create_issue",
                                 timestamp: "2027-01-15T08:00:05Z", session: "sess-a"),
            generation: state.connectionGeneration
        )
        XCTAssertEqual(state.glanceCallsThisHour(now: Self.now), 13)

        // …and the response, which counted 12, lands after it.
        state.updateUsage(timeline: [Self.bucket(calls: 12)], now: Self.now, polledAt: issuedAt)
        XCTAssertEqual(state.glanceCallsThisHour(now: Self.now), 13,
                       "that poll answered for 08:00:00; the call at 08:00:05 is still ours")
    }

    /// The mirror image. The aggregate counted a call, the response was
    /// processed, and the SSE event for that same call is delivered a few
    /// milliseconds later. Adding it on top of a total that already contains it
    /// makes the header overshoot the rows until the next poll.
    func testACallTheLastPollAlreadyCountedIsNotAddedAgainWhenItsEventArrivesLate() {
        let state = Self.connectedState()
        let calledAt = "2027-01-15T08:00:05Z"
        // Issued after the call happened, so its 13 includes it.
        state.updateUsage(timeline: [Self.bucket(calls: 13)], now: Self.now,
                          polledAt: GlanceFormatting.parseTimestamp("2027-01-15T08:00:10Z")!)

        state.prependGlanceActivity(
            GlanceFixtures.entry(id: "live", server: "github", tool: "create_issue",
                                 timestamp: calledAt, session: "sess-a"),
            generation: state.connectionGeneration
        )
        XCTAssertEqual(state.glanceCallsThisHour(now: Self.now), 13,
                       "the row is late, the call is not: the poll already counted it")
    }

    /// Only what the usage timeline itself counts. A blocked call never
    /// executed, but it IS a red row in the glance and the core's aggregate now
    /// counts it in the timeline (`UsageAggregate.applyBlocked`) — so the live
    /// increment must count it too, or the header lags the bars by one until
    /// the next poll.
    func testABlockedCallIsAddedToTheCallCount() {
        let state = Self.connectedState()
        state.updateUsage(timeline: [Self.bucket(calls: 12)], now: Self.now)
        state.prependGlanceActivity(
            Self.blockedEntry(timestamp: "2027-01-15T08:00:30Z"),
            generation: state.connectionGeneration
        )
        XCTAssertEqual(state.glanceCallsThisHour(now: Self.now), 13)
    }

    /// The live-increment rule mirrors `UsageAggregate.Apply` case by case:
    /// internal built-ins count except the `call_tool_*` variant echoes (they
    /// mirror a dispatch whose own `tool_call` record is also on the stream),
    /// and a `tool_call` shed by the concurrency limiter never executed.
    func testLiveIncrementMirrorsTheAggregatesTimelineRule() {
        XCTAssertFalse(AppState.countsTowardUsageTimeline(
            Self.typedEntry(type: "internal_tool_call", tool: "code_execution", status: "success")),
                       "a script that ran bars through its sub-calls; the wrapper would double it")
        XCTAssertTrue(AppState.countsTowardUsageTimeline(
            Self.typedEntry(type: "internal_tool_call", tool: "code_execution", status: "error")),
                      "a script that died before dispatch has no sub-calls to bar for it")
        XCTAssertTrue(AppState.countsTowardUsageTimeline(
            Self.typedEntry(type: "internal_tool_call", tool: "describe_tool", status: "success")))
        XCTAssertTrue(AppState.countsTowardUsageTimeline(
            Self.typedEntry(type: "internal_tool_call", tool: "retrieve_tools", status: "error")))
        XCTAssertFalse(AppState.countsTowardUsageTimeline(
            Self.typedEntry(type: "internal_tool_call", tool: "call_tool_read", status: "error")))
        XCTAssertFalse(AppState.countsTowardUsageTimeline(
            Self.typedEntry(type: "internal_tool_call", tool: "upstream_servers", status: "success")),
                       "management built-ins are never glance rows, so no bar")
        XCTAssertFalse(AppState.countsTowardUsageTimeline(
            Self.typedEntry(type: "internal_tool_call", tool: "upstream_servers", status: "error")),
                       "rule 1 hides management built-ins whatever their status")
        XCTAssertTrue(AppState.countsTowardUsageTimeline(
            Self.typedEntry(type: "internal_tool_call", tool: "search_servers", status: "error")),
                      "any non-management internal failure is a glance row")
        XCTAssertFalse(AppState.countsTowardUsageTimeline(
            Self.typedEntry(type: "tool_call", tool: "create_issue", status: "rejected")))
        XCTAssertTrue(AppState.countsTowardUsageTimeline(
            Self.typedEntry(type: "tool_call", tool: "create_issue", status: "error")))
    }

    /// A live call from the hour that just ended belongs to that hour's bucket,
    /// not to this one — and neither does the POLLED total it was added to.
    /// "12 calls this hour" for an hour in which nothing has happened yet is the
    /// same lie as the one the live increment fixes, told by the other half of
    /// the sum.
    func testNeitherHalfOfTheCountSurvivesAnHourRollover() {
        let state = Self.connectedState()
        state.updateUsage(timeline: [Self.bucket(calls: 12)], now: Self.now)
        state.prependGlanceActivity(
            GlanceFixtures.entry(id: "live", server: "github", tool: "create_issue",
                                 timestamp: "2027-01-15T08:00:30Z", session: "sess-a"),
            generation: state.connectionGeneration
        )
        XCTAssertEqual(state.glanceCallsThisHour(now: Self.now), 13,
                       "precondition: both halves count inside the polled hour")

        // …an hour later, with no poll in between.
        let nextHour = Self.now.addingTimeInterval(3600)
        XCTAssertEqual(state.glanceCallsThisHour(now: nextHour), 0,
                       "the poll answered for 07:00–08:00; nothing has answered for 08:00–09:00")
    }

    /// The base is stale only until it is stale in the same direction as the
    /// live calls: a call that arrives in the NEW hour is this hour's whole
    /// count, and the previous hour's polled total must not be added to it.
    func testAfterAnHourRolloverOnlyTheNewHoursLiveCallsCount() {
        let state = Self.connectedState()
        state.updateUsage(timeline: [Self.bucket(calls: 12)], now: Self.now)
        state.prependGlanceActivity(
            GlanceFixtures.entry(id: "next-hour", server: "github", tool: "create_issue",
                                 timestamp: "2027-01-15T09:00:10Z", session: "sess-a"),
            generation: state.connectionGeneration
        )
        XCTAssertEqual(state.glanceCallsThisHour(now: Self.now.addingTimeInterval(3600)), 1)
    }

    /// Nothing to add to. Until the first usage response the header has no call
    /// segment at all, and one live call must not invent "1 call this hour"
    /// over a proxy that has served hundreds.
    func testLiveCallsDoNotFabricateACountBeforeTheFirstPoll() {
        let state = Self.connectedState()
        state.callsThisHour = nil
        state.prependGlanceActivity(
            GlanceFixtures.entry(id: "live", server: "github", tool: "create_issue",
                                 timestamp: "2027-01-15T08:00:00Z", session: "sess-a"),
            generation: state.connectionGeneration
        )
        XCTAssertNil(state.glanceCallsThisHour(now: Self.now))
    }

    // MARK: - Fixtures

    private static func connectedState() -> AppState {
        let state = AppState()
        state.coreState = .connected
        state.glanceSessions = [
            GlanceFixtures.session(id: "sess-a", name: "Claude Code", calls: 8,
                                   lastActivity: "2027-01-15T07:59:00Z")
        ]
        return state
    }

    private static func stateWithClients(_ count: Int) -> AppState {
        let state = AppState()
        state.coreState = .connected
        state.glanceSessions = sessions(count)
        return state
    }

    /// `count` distinct clients, each a minute apart so the order is total.
    private static func sessions(_ count: Int) -> [APIClient.MCPSession] {
        (0..<count).map { index in
            GlanceFixtures.session(
                id: "sess-\(index)",
                name: "client-\(index)",
                calls: 1,
                lastActivity: UsageBucket.rfc3339String(
                    from: now.addingTimeInterval(-Double(index) * 60)
                )
            )
        }
    }

    private static func bucket(calls: Int) -> UsageBucket {
        UsageBucket(start: AppState.floorToHour(now), calls: calls, errors: 0, totalRespBytes: 0)
    }

    private static func typedEntry(type: String, tool: String, status: String) -> ActivityEntry {
        let json: [String: Any] = [
            "id": "typed-\(type)-\(tool)-\(status)",
            "type": type,
            "status": status,
            "timestamp": "2027-01-15T08:00:00Z",
            "request_id": "req-typed-\(tool)-\(status)",
            "server_name": "github",
            "tool_name": tool
        ]
        let data = try! JSONSerialization.data(withJSONObject: json)
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(ActivityEntry.self, from: data)
    }

    private static func blockedEntry(timestamp: String = "2027-01-15T08:00:00Z") -> ActivityEntry {
        let json: [String: Any] = [
            "id": "blocked-1",
            "type": ActivityEntry.policyDecisionType,
            "status": "blocked",
            "timestamp": timestamp,
            "request_id": "req-blocked-1",
            "server_name": "github",
            "tool_name": "create_issue"
        ]
        let data = try! JSONSerialization.data(withJSONObject: json)
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(ActivityEntry.self, from: data)
    }
}
