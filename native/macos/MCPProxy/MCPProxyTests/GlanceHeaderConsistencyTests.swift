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
        state.updateUsage(timeline: [Self.bucket(calls: 12)], now: Self.now)
        XCTAssertEqual(Self.makeSection().items(for: state, now: Self.now).first?.title,
                       "12 calls this hour · 1 active",
                       "precondition: the polled count is what the header shows")

        state.prependGlanceActivity(
            GlanceFixtures.entry(id: "live", server: "github", tool: "create_issue",
                                 timestamp: "2027-01-15T08:00:00Z", session: "sess-a"),
            generation: state.connectionGeneration
        )

        XCTAssertEqual(Self.makeSection().items(for: state, now: Self.now).first?.title,
                       "13 calls this hour · 1 active",
                       "the row is on screen, so the number above it has to include it")
    }

    /// The live increment is not a second source of truth: the next poll's
    /// answer replaces it outright, so the two can never accumulate.
    func testTheNextPollSupersedesTheLiveIncrement() {
        let state = Self.connectedState()
        state.updateUsage(timeline: [Self.bucket(calls: 12)], now: Self.now)
        state.prependGlanceActivity(
            GlanceFixtures.entry(id: "live", server: "github", tool: "create_issue",
                                 timestamp: "2027-01-15T08:00:00Z", session: "sess-a"),
            generation: state.connectionGeneration
        )
        XCTAssertEqual(state.glanceCallsThisHour(now: Self.now), 13)

        // The poll that has now seen the same call.
        state.updateUsage(timeline: [Self.bucket(calls: 13)], now: Self.now)
        XCTAssertEqual(state.glanceCallsThisHour(now: Self.now), 13,
                       "the poll's count already includes the live call; adding it twice "
                       + "is the drift this fix exists to remove")
    }

    /// Only what the usage timeline itself counts. A blocked call never
    /// executed — the core's aggregate excludes it (`UsageAggregate.Apply`) —
    /// so counting it here would make the header disagree with the poll that
    /// follows it, in the other direction.
    func testABlockedCallIsNotAddedToTheCallCount() {
        let state = Self.connectedState()
        state.updateUsage(timeline: [Self.bucket(calls: 12)], now: Self.now)
        state.prependGlanceActivity(Self.blockedEntry(), generation: state.connectionGeneration)
        XCTAssertEqual(state.glanceCallsThisHour(now: Self.now), 12)
    }

    /// A live call from the hour that just ended belongs to that hour's bucket,
    /// not to this one.
    func testALiveCallFromAnEarlierHourIsNotCountedInThisOne() {
        let state = Self.connectedState()
        state.updateUsage(timeline: [Self.bucket(calls: 12)], now: Self.now)
        state.prependGlanceActivity(
            GlanceFixtures.entry(id: "live", server: "github", tool: "create_issue",
                                 timestamp: "2027-01-15T08:00:00Z", session: "sess-a"),
            generation: state.connectionGeneration
        )
        // …an hour later, with no poll in between.
        let nextHour = Self.now.addingTimeInterval(3600)
        XCTAssertEqual(state.glanceCallsThisHour(now: nextHour), 12)
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

    private static func blockedEntry() -> ActivityEntry {
        let json: [String: Any] = [
            "id": "blocked-1",
            "type": ActivityEntry.policyDecisionType,
            "status": "blocked",
            "timestamp": "2027-01-15T08:00:00Z",
            "request_id": "req-blocked-1",
            "server_name": "github",
            "tool_name": "create_issue"
        ]
        let data = try! JSONSerialization.data(withJSONObject: json)
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(ActivityEntry.self, from: data)
    }
}
