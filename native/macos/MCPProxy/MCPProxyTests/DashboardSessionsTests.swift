import XCTest
@testable import MCPProxy

/// The dashboard's Recent Sessions table (spec 090 follow-up).
///
/// The table asks the same question the tray's Clients section does — when was
/// this client last heard from — so it has to answer it the same way. These
/// pin the shared rule at the dashboard's own seam, because the two used to
/// disagree: the tray fell back to `start_time` and the table did not.
final class DashboardSessionsTests: XCTestCase {

    static let now = GlanceFormatting.parseTimestamp("2027-01-15T08:00:00Z")!

    // MARK: - The zero-value sentinel

    /// The core serialises an absent legacy `LastActivity` as the Go zero time.
    /// It parses, so a naive `lastActivity ?? startTime` never reaches the
    /// fallback and the row reads as being from the year 1 — "739000d ago".
    func testAZeroValueLastActivityFallsBackToTheStartTime() {
        let session = Self.session(id: "s1", name: "Claude Code",
                                   start: "2027-01-15T07:40:00Z",
                                   lastActivity: "0001-01-01T00:00:00Z")

        XCTAssertEqual(DashboardSessions.relativeTime(for: session, now: Self.now), "20m ago")
    }

    /// …and the same sentinel must not drag the row to the bottom of a table
    /// that is sorted by recency.
    func testAZeroValueLastActivityDoesNotSinkTheRow() {
        let rows = DashboardSessions.rows(from: [
            Self.session(id: "quiet", name: "Cursor",
                         start: "2027-01-15T06:00:00Z",
                         lastActivity: "2027-01-15T06:30:00Z"),
            Self.session(id: "sentinel", name: "Claude Code",
                         start: "2027-01-15T07:40:00Z",
                         lastActivity: "0001-01-01T00:00:00Z")
        ])

        XCTAssertEqual(rows.map(\.id), ["sentinel", "quiet"],
                       "07:40 is more recent than 06:30 whatever the sentinel says")
    }

    /// The dedupe picks a client's most recent session; a sentinel must not win
    /// that comparison either.
    func testTheDedupePrefersTheRealTimestampOverASentinel() {
        let rows = DashboardSessions.rows(from: [
            Self.session(id: "real", name: "Claude Code", calls: 2,
                         start: "2027-01-15T07:00:00Z",
                         lastActivity: "2027-01-15T07:50:00Z"),
            Self.session(id: "sentinel", name: "Claude Code", calls: 2,
                         start: "2027-01-15T07:10:00Z",
                         lastActivity: "0001-01-01T00:00:00Z")
        ])

        XCTAssertEqual(rows.map(\.id), ["real"])
    }

    func testASessionWithNoUsableTimestampReadsAsUnknownRatherThanAncient() {
        let session = Self.session(id: "none", name: "Cursor", start: nil, lastActivity: nil)

        XCTAssertEqual(DashboardSessions.relativeTime(for: session, now: Self.now), "-")
    }

    /// It still appears in the table — an unknown age is a reason to say so,
    /// not a reason to hide a client the user connected.
    func testASessionWithNoUsableTimestampIsStillListed() {
        let rows = DashboardSessions.rows(from: [
            Self.session(id: "none", name: "Cursor", start: nil, lastActivity: nil),
            Self.session(id: "recent", name: "Claude Code", lastActivity: "2027-01-15T07:55:00Z")
        ])

        XCTAssertEqual(rows.map(\.id), ["recent", "none"], "unknown sorts last, but sorts")
    }

    // MARK: - Age buckets

    func testTheAgeBucketsReadAsTheTableAlwaysRendered() {
        let cases: [(String, String)] = [
            ("2027-01-15T07:59:30Z", "just now"),
            ("2027-01-15T07:30:00Z", "30m ago"),
            ("2027-01-15T05:00:00Z", "3h ago"),
            ("2027-01-13T08:00:00Z", "2d ago")
        ]

        for (stamp, expected) in cases {
            let session = Self.session(id: stamp, name: "Claude Code", lastActivity: stamp)
            XCTAssertEqual(DashboardSessions.relativeTime(for: session, now: Self.now), expected)
        }
    }

    // MARK: - Dedupe and ordering (unchanged by the fix)

    func testAnActiveSessionWinsOverAClosedOneWithMoreCalls() {
        let rows = DashboardSessions.rows(from: [
            Self.session(id: "closed", name: "Claude Code", status: "closed", calls: 99,
                         lastActivity: "2027-01-15T07:59:00Z"),
            Self.session(id: "open", name: "Claude Code", status: "active", calls: 1,
                         lastActivity: "2027-01-15T07:00:00Z")
        ])

        XCTAssertEqual(rows.map(\.id), ["open"])
    }

    func testRowsWithIdenticalKeysAreOrderedBySessionIDSoTheTableIsStable() {
        let rows = DashboardSessions.rows(from: [
            Self.session(id: "b", name: "two", calls: 3, lastActivity: "2027-01-15T07:59:00Z"),
            Self.session(id: "a", name: "one", calls: 3, lastActivity: "2027-01-15T07:59:00Z")
        ])

        XCTAssertEqual(rows.map(\.id), ["a", "b"])
    }

    // MARK: - Helpers

    private static func session(
        id: String,
        name: String?,
        status: String = "active",
        calls: Int = 3,
        start: String? = "2027-01-15T07:00:00Z",
        lastActivity: String?
    ) -> APIClient.MCPSession {
        var json: [String: Any] = [
            "id": id,
            "status": status,
            "tool_call_count": calls
        ]
        if let name { json["client_name"] = name }
        if let start { json["start_time"] = start }
        if let lastActivity { json["last_activity"] = lastActivity }
        let data = try! JSONSerialization.data(withJSONObject: json)
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(APIClient.MCPSession.self, from: data)
    }
}
