import XCTest
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

    private static func activity(id: String, type: String, status: String = "success") throws -> ActivityEntry {
        let json = """
        {"id":"\(id)","type":"\(type)","status":"\(status)","timestamp":"2026-07-29T11:00:00Z"}
        """
        // swiftlint:disable:next force_unwrapping
        return try JSONDecoder().decode(ActivityEntry.self, from: json.data(using: .utf8)!)
    }

    private static func session(id: String, status: String) throws -> APIClient.MCPSession {
        let json = """
        {"id":"\(id)","client_name":"Claude Code","status":"\(status)","tool_call_count":3}
        """
        // swiftlint:disable:next force_unwrapping
        return try JSONDecoder().decode(APIClient.MCPSession.self, from: json.data(using: .utf8)!)
    }
}
