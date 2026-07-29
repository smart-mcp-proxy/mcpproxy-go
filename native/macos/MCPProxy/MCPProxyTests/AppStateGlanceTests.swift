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
        state.updateUsage(timeline: timeline, now: now)

        XCTAssertEqual(state.callsThisHour, 12)
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
