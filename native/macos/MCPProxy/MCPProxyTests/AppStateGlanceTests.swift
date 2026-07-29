import XCTest
import Combine
@testable import MCPProxy

/// Tray Glance: the four `AppState` glance fields, the current-UTC-hour
/// derivation behind `callsThisHour`, the disconnect reset, and the guarantee
/// that the shared Dashboard/ActivityView feeds are not narrowed.
@MainActor
final class AppStateGlanceTests: XCTestCase {

    // MARK: - callsThisHour

    /// A sparse timeline whose newest bucket is three hours old must report
    /// zero calls this hour, NOT that bucket's count. Buckets are UTC-hour
    /// aligned and the endpoint omits hours with no activity, so "the most
    /// recent bucket" is a count from the past presented as if it were current.
    func testCallsThisHourIsZeroWhenCurrentHourBucketIsAbsent() throws {
        let now = Self.date("2026-07-29T14:05:00Z")
        let timeline = [
            Self.bucket("2026-07-29T10:00:00Z", calls: 7),
            Self.bucket("2026-07-29T11:00:00Z", calls: 12, errors: 2),
        ]

        let state = AppState()
        state.coreState = .connected
        state.updateUsage(timeline: timeline, now: now)

        XCTAssertEqual(state.callsThisHour, 0)
        XCTAssertEqual(state.usageTimeline?.count, 2)
    }

    /// When the current UTC hour does have a bucket, its `calls` is the headline
    /// number — regardless of where it sits in the array.
    func testCallsThisHourReadsTheCurrentHourBucket() throws {
        let now = Self.date("2026-07-29T11:42:31Z")
        let timeline = [
            Self.bucket("2026-07-29T11:00:00Z", calls: 12, errors: 2),
            Self.bucket("2026-07-29T10:00:00Z", calls: 7),
        ]

        let state = AppState()
        state.coreState = .connected
        state.updateUsage(timeline: timeline, now: now)

        XCTAssertEqual(state.callsThisHour, 12)
    }

    /// "Loaded, and the proxy was idle" must be expressible: an empty timeline
    /// yields 0, which is deliberately distinct from the nil loading state.
    func testEmptyTimelineYieldsZeroNotNil() {
        let state = AppState()
        XCTAssertNil(state.callsThisHour, "callsThisHour starts nil = not loaded yet")

        state.coreState = .connected
        state.updateUsage(timeline: [], now: Self.date("2026-07-29T11:42:00Z"))

        XCTAssertEqual(state.callsThisHour, 0)
        XCTAssertEqual(state.usageTimeline, [])
    }

    // MARK: - Disconnect reset

    /// The connect path flips to `.connected` before the first refresh
    /// completes, so glance state from a previous core must be cleared the
    /// moment the core state leaves `.connected`.
    func testGlanceStateClearedOnDisconnect() throws {
        let state = AppState()
        state.coreState = .connected
        state.updateGlanceActivity([try Self.activity(id: "a1", type: "tool_call")])
        state.updateGlanceSessions([try Self.session(id: "s1", status: "active")])
        state.updateUsage(
            timeline: [Self.bucket("2026-07-29T11:00:00Z", calls: 12)],
            now: Self.date("2026-07-29T11:10:00Z")
        )

        state.coreState = .idle

        XCTAssertTrue(state.glanceActivity.isEmpty)
        XCTAssertTrue(state.glanceSessions.isEmpty)
        XCTAssertNil(state.usageTimeline)
        XCTAssertNil(state.callsThisHour)
    }

    /// Every non-connected state clears, not just `.idle` — a reconnecting or
    /// errored core must not keep showing the old numbers either.
    func testGlanceStateClearedOnReconnectingAndError() throws {
        for target in [CoreState.reconnecting(attempt: 1), .error(.general("boom")), .shuttingDown] {
            let state = AppState()
            state.coreState = .connected
            state.updateGlanceActivity([try Self.activity(id: "a1", type: "tool_call")])
            state.callsThisHour = 9

            state.coreState = target

            XCTAssertTrue(state.glanceActivity.isEmpty, "\(target) should clear glanceActivity")
            XCTAssertNil(state.callsThisHour, "\(target) should clear callsThisHour")
        }
    }

    /// Staying connected must not wipe the feeds the refresh loop just filled.
    func testConnectedStateDoesNotClearGlanceState() throws {
        let state = AppState()
        state.coreState = .connected
        state.updateGlanceActivity([try Self.activity(id: "a1", type: "tool_call")])
        state.callsThisHour = 4

        state.coreState = .connected

        XCTAssertEqual(state.glanceActivity.map(\.id), ["a1"])
        XCTAssertEqual(state.callsThisHour, 4)
    }

    /// A glance fetch already past its `guard let apiClient` when the core leaves
    /// `.connected` resolves AFTER the reset and would otherwise write the dead
    /// core's data back over the cleared fields, silently undoing the one
    /// guarantee these fields exist to provide.
    ///
    /// The window is real, not theoretical: `CoreProcessManager.shutdown()`
    /// transitions to `.shuttingDown` (:204) before it cancels `refreshTask`
    /// (:210), and cancellation would not help anyway — a suspended
    /// `await apiClient.glanceActivity()` still resumes and still runs the
    /// `await appState.update…` that follows it.
    func testLateFetchDoesNotRepopulateAfterDisconnect() throws {
        let state = AppState()
        state.coreState = .connected
        state.updateGlanceActivity([try Self.activity(id: "a1", type: "tool_call")])
        state.updateGlanceSessions([try Self.session(id: "s1", status: "active")])
        state.updateUsage(
            timeline: [Self.bucket("2026-07-29T11:00:00Z", calls: 12)],
            now: Self.date("2026-07-29T11:10:00Z")
        )

        state.coreState = .shuttingDown

        // The three in-flight fetches resolve now, after the reset.
        state.updateGlanceActivity([try Self.activity(id: "a2", type: "tool_call")])
        state.updateGlanceSessions([try Self.session(id: "s2", status: "active")])
        state.updateUsage(
            timeline: [Self.bucket("2026-07-29T11:00:00Z", calls: 99)],
            now: Self.date("2026-07-29T11:10:00Z")
        )

        XCTAssertTrue(state.glanceActivity.isEmpty, "a late activity fetch must not repopulate")
        XCTAssertTrue(state.glanceSessions.isEmpty, "a late sessions fetch must not repopulate")
        XCTAssertNil(state.usageTimeline, "a late usage fetch must not repopulate")
        XCTAssertNil(state.callsThisHour, "a late usage fetch must not repopulate")
    }

    // MARK: - Reconciling poll

    /// The 30s poll exists to reconcile the SSE-fed optimistic list with the
    /// server's canonical records — including the asynchronously-computed
    /// sensitive-data flag and the final status, which arrive on a record whose
    /// id has NOT changed. `ActivityEntry`'s Equatable is id-only
    /// (API/Models.swift:570), so an id-only guard would drop those corrections
    /// forever and the row would lie until it scrolled off.
    func testGlanceActivityUpdatesWhenOnlyStatusChanges() throws {
        let state = AppState()
        state.coreState = .connected
        state.updateGlanceActivity([try Self.activity(id: "a1", type: "tool_call")])
        state.updateGlanceActivity([try Self.activity(id: "a1", type: "tool_call", status: "error")])

        XCTAssertEqual(state.glanceActivity.first?.status, "error")
    }

    // MARK: - Reconcile vs live SSE rows

    /// `AppState` is reached through `await`, and the poll suspends on the
    /// network for tens of milliseconds. An SSE event that lands inside that
    /// window prepends a row the in-flight response cannot know about, and a
    /// wholesale replace erases it — the user watches a call appear and then
    /// vanish for up to 30 seconds. The merge keeps it.
    func testAPollDoesNotEraseARowThatArrivedWhileItWasInFlight() throws {
        let state = AppState()
        state.coreState = .connected
        let page = [try Self.activity(id: "a1", type: "tool_call", request: "r-1",
                                      timestamp: "2026-07-29T11:00:00Z")]
        state.updateGlanceActivity(page)

        // An SSE event lands while the next poll is suspended…
        state.prependGlanceActivity(try Self.activity(id: "r-9:tool_call", type: "tool_call",
                                                      request: "r-9",
                                                      timestamp: "2026-07-29T11:00:30Z"))
        // …and the poll's response, which predates it, resolves now.
        state.updateGlanceActivity(page)

        XCTAssertEqual(state.glanceActivity.map(\.requestId), ["r-9", "r-1"])
    }

    /// Once the poll DOES carry the call, its canonical record replaces the
    /// optimistic row rather than joining it: both describe one call, and
    /// `requestId` is what says so — the storage id differs from the SSE row's
    /// provisional `<request_id>:<type>`.
    ///
    /// The SSE row is stamped a shade LATER than the record, which is the case
    /// that separates the two keys: an id-keyed merge finds no match, sees a row
    /// newer than the page, retains it, and the one call occupies two rows.
    func testThePolledRecordSupersedesTheLiveRowForTheSameCall() throws {
        let state = AppState()
        state.coreState = .connected
        state.prependGlanceActivity(try Self.activity(id: "r-9:tool_call", type: "tool_call",
                                                      request: "r-9",
                                                      timestamp: "2026-07-29T11:00:30.500Z"))

        state.updateGlanceActivity([
            try Self.activity(id: "01JQ-STORAGE-ULID", type: "tool_call", request: "r-9",
                              timestamp: "2026-07-29T11:00:30Z")
        ])

        XCTAssertEqual(state.glanceActivity.map(\.id), ["01JQ-STORAGE-ULID"],
                       "one call, one row — keyed on requestId, not on the churning id")
    }

    /// The merge must not turn the feed into an append-only log. A row the poll
    /// omits from WITHIN its own window — collapsed, pruned, filtered — is the
    /// poll's business, and only rows newer than everything the page carries
    /// are retained.
    func testTheMergeDropsAStaleRowInsideThePollsOwnWindow() throws {
        let state = AppState()
        state.coreState = .connected
        state.updateGlanceActivity([
            try Self.activity(id: "gone", type: "tool_call", request: "r-old",
                              timestamp: "2026-07-29T10:59:00Z")
        ])

        state.updateGlanceActivity([
            try Self.activity(id: "a1", type: "tool_call", request: "r-1",
                              timestamp: "2026-07-29T11:00:00Z")
        ])

        XCTAssertEqual(state.glanceActivity.map(\.id), ["a1"])
    }

    /// An empty page contradicts nothing. A poll that returns no records has
    /// nothing to say about a call the server had not recorded when it answered,
    /// so it must not erase the row the SSE event just put on screen — the same
    /// bug shape the merge was written to fix, in the one branch that used to
    /// fall through to a replace.
    func testAnEmptyPollDoesNotEraseALiveRow() throws {
        let state = AppState()
        state.coreState = .connected
        state.prependGlanceActivity(try Self.activity(id: "r-9:tool_call", type: "tool_call",
                                                      request: "r-9",
                                                      timestamp: "2026-07-29T11:00:30Z"))

        state.updateGlanceActivity([])

        XCTAssertEqual(state.glanceActivity.map(\.requestId), ["r-9"])
    }

    /// …but a page that HAS records and no usable timestamps still replaces.
    /// "Newer than everything the page carries" is unanswerable there, and a
    /// page full of records is the server's own account of the feed; only the
    /// genuinely empty page is the non-statement.
    func testAPageWithUnparsableTimestampsStillReplaces() throws {
        let state = AppState()
        state.coreState = .connected
        state.prependGlanceActivity(try Self.activity(id: "r-9:tool_call", type: "tool_call",
                                                      request: "r-9",
                                                      timestamp: "2026-07-29T11:00:30Z"))

        state.updateGlanceActivity([
            try Self.activity(id: "a1", type: "tool_call", request: "r-1", timestamp: "not a date")
        ])

        XCTAssertEqual(state.glanceActivity.map(\.id), ["a1"])
    }

    /// Retention is bounded: a burst of SSE rows plus a full page cannot grow
    /// the feed past the cap.
    func testTheMergedFeedStaysCapped() throws {
        let state = AppState()
        state.coreState = .connected
        for i in 0..<10 {
            state.prependGlanceActivity(try Self.activity(id: "live-\(i)", type: "tool_call",
                                                          request: "live-\(i)",
                                                          timestamp: "2026-07-29T12:00:00Z"))
        }
        let page = try (0..<AppState.glanceActivityCap).map {
            try Self.activity(id: "p\($0)", type: "tool_call", request: "p\($0)",
                              timestamp: "2026-07-29T11:00:00Z")
        }

        state.updateGlanceActivity(page)

        XCTAssertEqual(state.glanceActivity.count, AppState.glanceActivityCap)
        XCTAssertEqual(state.glanceActivity.first?.id, "live-9", "newest first is preserved")
    }

    // MARK: - Shared feeds are not narrowed

    /// Dashboard non-regression: `recentActivity` still carries non-tool-call
    /// records (security scans, OAuth events) and `recentSessions` still carries
    /// closed sessions after the glance feeds are populated alongside them.
    func testGlanceFeedsDoNotNarrowSharedFeeds() throws {
        let state = AppState()
        state.coreState = .connected

        state.updateActivity([
            try Self.activity(id: "a1", type: "tool_call"),
            try Self.activity(id: "a2", type: "security_scan"),
        ])
        state.recentSessions = [
            try Self.session(id: "s1", status: "active"),
            try Self.session(id: "s2", status: "closed"),
        ]

        state.updateGlanceActivity([try Self.activity(id: "a1", type: "tool_call")])
        state.updateGlanceSessions([try Self.session(id: "s1", status: "active")])

        XCTAssertEqual(state.recentActivity.map(\.type), ["tool_call", "security_scan"])
        XCTAssertEqual(state.recentSessions.map(\.status), ["active", "closed"])
        XCTAssertEqual(state.glanceActivity.map(\.id), ["a1"])
        XCTAssertEqual(state.glanceSessions.map(\.id), ["s1"])
    }

    // MARK: - Session republish guard

    /// The tray's Clients rows render a live per-session call count
    /// ("Claude Code — 8 calls · 1m"), so a session whose count moved has to
    /// reach the menu. Guarding on ids alone froze that number at whatever the
    /// first poll returned for as long as the session list's membership held.
    func testUpdateGlanceSessionsRepublishesWhenACallCountMoves() throws {
        let state = AppState()
        state.coreState = .connected
        state.updateGlanceSessions([try Self.session(id: "s1", status: "active", calls: 3)])

        state.updateGlanceSessions([try Self.session(id: "s1", status: "active", calls: 40)])

        XCTAssertEqual(state.glanceSessions.first?.toolCallCount, 40)
    }

    /// …but the guard must still exist: an identical poll every 30s must not
    /// publish, or the debounced `objectWillChange → rebuildMenu()` sink rebuilds
    /// the menu forever on an idle proxy.
    func testUpdateGlanceSessionsIgnoresAnIdenticalPoll() throws {
        let state = AppState()
        state.coreState = .connected
        state.updateGlanceSessions([try Self.session(id: "s1", status: "active", calls: 3)])

        var published = 0
        let sink = state.objectWillChange.sink { _ in published += 1 }
        state.updateGlanceSessions([try Self.session(id: "s1", status: "active", calls: 3)])
        sink.cancel()

        XCTAssertEqual(published, 0)
    }

    // MARK: - Helpers

    private static func date(_ iso: String) -> Date {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime]
        // swiftlint:disable:next force_unwrapping
        return formatter.date(from: iso)!
    }

    private static func bucket(_ iso: String, calls: Int, errors: Int = 0) -> UsageBucket {
        UsageBucket(start: date(iso), calls: calls, errors: errors, totalRespBytes: 0)
    }

    static func activity(id: String,
                         type: String,
                         status: String = "success",
                         request: String? = nil,
                         timestamp: String = "2026-07-29T11:00:00Z") throws -> ActivityEntry {
        let requestField = request.map { ",\"request_id\":\"\($0)\"" } ?? ""
        let json = """
        {"id":"\(id)","type":"\(type)","status":"\(status)","timestamp":"\(timestamp)"\(requestField)}
        """
        // swiftlint:disable:next force_unwrapping
        return try JSONDecoder().decode(ActivityEntry.self, from: json.data(using: .utf8)!)
    }

    private static func session(id: String, status: String, calls: Int = 3) throws -> APIClient.MCPSession {
        let json = """
        {"id":"\(id)","client_name":"Claude Code","status":"\(status)","tool_call_count":\(calls)}
        """
        // swiftlint:disable:next force_unwrapping
        return try JSONDecoder().decode(APIClient.MCPSession.self, from: json.data(using: .utf8)!)
    }
}
