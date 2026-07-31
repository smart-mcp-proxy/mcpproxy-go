import XCTest
@testable import MCPProxy

/// Presence classification for the tray's Clients section (spec 090 US4).
///
/// The rules are pure and boundary-heavy, so they are pinned here rather than
/// through the menu: an off-by-one at 5:00 or 30:00 is invisible in a
/// screenshot and would silently reclassify every client in the section.
final class GlancePresenceTests: XCTestCase {

    static let now = GlanceFormatting.parseTimestamp("2027-01-15T08:00:00Z")!

    // MARK: - State boundaries (FR-017)

    func testActiveIsAnythingUnderFiveMinutes() {
        for age in [0.0, 1.0, 60.0, 299.0] {
            XCTAssertEqual(GlancePresence.state(forAge: age), .active,
                           "\(age)s since last activity must read as active")
        }
    }

    /// The boundaries are inclusive-idle on purpose: 5:00 is not "still active"
    /// and 30:00 — the session inactivity timeout — is the last moment the
    /// client can be called merely quiet.
    func testTheFiveAndThirtyMinuteBoundariesAreIdle() {
        XCTAssertEqual(GlancePresence.state(forAge: 300), .idle)
        XCTAssertEqual(GlancePresence.state(forAge: 1799), .idle)
        XCTAssertEqual(GlancePresence.state(forAge: 1800), .idle)
    }

    func testPastThirtyMinutesIsSeenUpToTheLookback() {
        XCTAssertEqual(GlancePresence.state(forAge: 1801), .seen)
        XCTAssertEqual(GlancePresence.state(forAge: 3 * 3600), .seen)
        XCTAssertEqual(GlancePresence.state(forAge: 24 * 3600), .seen)
    }

    func testBeyondTheLookbackIsNotAPresenceAtAll() {
        XCTAssertNil(GlancePresence.state(forAge: 24 * 3600 + 1))
        XCTAssertNil(GlancePresence.state(forAge: 7 * 24 * 3600))
    }

    // MARK: - Timestamp handling

    /// A session that never recorded activity is still a session that started,
    /// and its start time is the honest answer to "when was this client last
    /// seen".
    func testAMissingLastActivityFallsBackToTheStartTime() {
        let rows = GlancePresence.clients(
            from: [Self.session(id: "s1", name: "Claude Code",
                                start: "2027-01-15T07:40:00Z", lastActivity: nil)],
            now: Self.now)

        XCTAssertEqual(rows.map(\.state), [.idle], "20 minutes since it started is 20 minutes quiet")
        XCTAssertEqual(rows.first?.age, 20 * 60)
    }

    /// The Go API serialises an absent legacy `LastActivity` as the zero
    /// `time.Time` — "0001-01-01T00:00:00Z" — which parses perfectly well and
    /// would otherwise land two millennia outside the lookback, silently
    /// dropping the client instead of falling back to its start time.
    func testAZeroValueLastActivityIsTreatedAsMissing() {
        let rows = GlancePresence.clients(
            from: [Self.session(id: "s1", name: "Claude Code",
                                start: "2027-01-15T07:40:00Z",
                                lastActivity: "0001-01-01T00:00:00Z")],
            now: Self.now)

        XCTAssertEqual(rows.map(\.state), [.idle], "must classify from start_time, not the zero stamp")
        XCTAssertEqual(rows.first?.age, 20 * 60)
    }

    func testASessionWithNoParseableTimestampIsExcluded() {
        let rows = GlancePresence.clients(
            from: [
                Self.session(id: "bad", name: "Claude Code",
                             start: "yesterday", lastActivity: "not a timestamp"),
                Self.session(id: "none", name: "Cursor", start: nil, lastActivity: nil),
                Self.session(id: "ok", name: "Codex", lastActivity: "2027-01-15T07:59:00Z")
            ],
            now: Self.now)

        XCTAssertEqual(rows.map(\.session.id), ["ok"])
    }

    /// Clock skew (or a core whose clock ran ahead) yields a timestamp in the
    /// future. It must read as "just now", never as a negative age.
    func testAFutureTimestampClampsToZeroRatherThanGoingNegative() {
        let rows = GlancePresence.clients(
            from: [Self.session(id: "s1", name: "Claude Code",
                                lastActivity: "2027-01-15T08:05:00Z")],
            now: Self.now)

        XCTAssertEqual(rows.first?.age, 0)
        XCTAssertEqual(rows.first?.state, .active)
        XCTAssertEqual(GlanceFormatting.compactAge(rows.first?.age ?? -1), "0s")
    }

    // MARK: - Deduplication (FR-017)

    /// One client reconnecting is one client. It is classified by its most
    /// recent session, not by however many sockets it has opened today.
    func testSessionsOfOneClientCollapseToItsMostRecent() {
        let rows = GlancePresence.clients(
            from: [
                Self.session(id: "old", name: "Claude Code", version: "2.1.0",
                             lastActivity: "2027-01-15T05:00:00Z"),
                Self.session(id: "new", name: "Claude Code", version: "2.1.0",
                             lastActivity: "2027-01-15T07:58:00Z"),
                Self.session(id: "mid", name: "Claude Code", version: "2.1.0",
                             lastActivity: "2027-01-15T07:00:00Z")
            ],
            now: Self.now)

        XCTAssertEqual(rows.count, 1)
        XCTAssertEqual(rows.first?.session.id, "new")
        XCTAssertEqual(rows.first?.state, .active)
    }

    /// …but two versions of the same client are two things running (spec
    /// Assumptions), so the dedupe key carries the version.
    func testTwoVersionsOfOneClientAreTwoRows() {
        let rows = GlancePresence.clients(
            from: [
                Self.session(id: "a", name: "Claude Code", version: "2.1.0",
                             lastActivity: "2027-01-15T07:59:00Z"),
                Self.session(id: "b", name: "Claude Code", version: "2.2.0",
                             lastActivity: "2027-01-15T07:58:00Z")
            ],
            now: Self.now)

        XCTAssertEqual(rows.map(\.session.id), ["a", "b"])
    }

    func testAnUnnamedClientIsNamedRatherThanDropped() {
        let rows = GlancePresence.clients(
            from: [
                Self.session(id: "n", name: nil, lastActivity: "2027-01-15T07:59:00Z"),
                Self.session(id: "e", name: "", lastActivity: "2027-01-15T07:58:00Z")
            ],
            now: Self.now)

        XCTAssertEqual(rows.map(\.name), ["Unknown client"])
        XCTAssertEqual(rows.count, 1, "an empty name and a missing one are the same client")
    }

    // MARK: - Ordering and cap (FR-018)

    func testRowsAreOrderedByRecencyAndCappedAtFive() {
        let sessions = (0..<7).map { index in
            Self.session(id: "c\(index)", name: "client-\(index)",
                         lastActivity: "2027-01-15T07:5\(index):00Z")
        }

        let rows = GlancePresence.clients(from: sessions, now: Self.now)

        XCTAssertEqual(rows.map(\.session.id), ["c6", "c5", "c4", "c3", "c2"])
    }

    /// US4 scenario 7: recency of *activity* decides inclusion and order, never
    /// recency of session start — the long-lived session that is calling tools
    /// right now is the one the user cares about.
    func testALongLivedButActiveSessionOutranksNewerQuietOnes() {
        let rows = GlancePresence.clients(
            from: [
                Self.session(id: "fresh-but-quiet", name: "Cursor",
                             start: "2027-01-15T07:55:00Z",
                             lastActivity: "2027-01-15T07:56:00Z"),
                Self.session(id: "old-but-busy", name: "Claude Code",
                             start: "2027-01-15T06:00:00Z",
                             lastActivity: "2027-01-15T07:59:00Z")
            ],
            now: Self.now)

        XCTAssertEqual(rows.map(\.session.id), ["old-but-busy", "fresh-but-quiet"])
    }

    /// FR-017: status is not consulted. A closed session is exactly the case the
    /// section exists to show — the transport is stateless, so a client that
    /// worked ten minutes ago has no open session to be "active" in.
    func testAClosedSessionStillCounts() {
        let rows = GlancePresence.clients(
            from: [Self.session(id: "closed", name: "Claude Code", status: "closed",
                                lastActivity: "2027-01-15T07:50:00Z")],
            now: Self.now)

        XCTAssertEqual(rows.map(\.state), [.idle])
    }

    // MARK: - Summary counts (FR-019)

    /// The summary counts every qualifying client, not just the five that got a
    /// row — the section says "how many are around", and a cap is a display
    /// limit, not a fact about the world.
    func testTheSummaryCountsTheWholeDedupedSet() {
        var sessions = (0..<7).map { index in
            Self.session(id: "a\(index)", name: "active-\(index)",
                         lastActivity: "2027-01-15T07:59:00Z")
        }
        sessions.append(Self.session(id: "i1", name: "idle-1",
                                     lastActivity: "2027-01-15T07:40:00Z"))

        let all = GlancePresence.clients(from: sessions, now: Self.now, limit: Int.max)

        XCTAssertEqual(all.count, 8)
        XCTAssertEqual(GlancePresence.summaryText(for: all), "7 active · 1 idle")
    }

    func testTheSummaryOmitsAnEmptyState() {
        let rows = GlancePresence.clients(
            from: [Self.session(id: "a", name: "Claude Code",
                                lastActivity: "2027-01-15T07:59:00Z")],
            now: Self.now)

        XCTAssertEqual(GlancePresence.summaryText(for: rows), "1 active")
    }

    /// "Seen" clients keep their rows but stay out of the count: a client last
    /// heard from three hours ago is not part of "what is going on right now",
    /// and counting it as one would overstate the proxy's current use.
    func testSeenClientsAreVisibleAsRowsButNotCounted() {
        let rows = GlancePresence.clients(
            from: [Self.session(id: "s", name: "Claude Code",
                                lastActivity: "2027-01-15T05:00:00Z")],
            now: Self.now)

        XCTAssertEqual(rows.map(\.state), [.seen])
        XCTAssertNil(GlancePresence.summaryText(for: rows),
                     "a feed of only seen clients contributes no client segment")
    }

    func testTheSummaryOfNothingIsNothing() {
        XCTAssertNil(GlancePresence.summaryText(for: []))
    }

    func testSingularAndPluralAreBothWellFormed() {
        let rows = GlancePresence.clients(
            from: [
                Self.session(id: "a1", name: "one", lastActivity: "2027-01-15T07:59:00Z"),
                Self.session(id: "i1", name: "two", lastActivity: "2027-01-15T07:40:00Z"),
                Self.session(id: "i2", name: "three", lastActivity: "2027-01-15T07:41:00Z")
            ],
            now: Self.now)

        XCTAssertEqual(GlancePresence.summaryText(for: rows), "1 active · 2 idle")
    }

    // MARK: - Helpers

    private static func session(
        id: String,
        name: String?,
        version: String? = nil,
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
        if let version { json["client_version"] = version }
        if let start { json["start_time"] = start }
        if let lastActivity { json["last_activity"] = lastActivity }
        let data = try! JSONSerialization.data(withJSONObject: json)
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(APIClient.MCPSession.self, from: data)
    }
}
