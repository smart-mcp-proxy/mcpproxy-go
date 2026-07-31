import XCTest
@testable import MCPProxy

final class GlanceSelectionCollapseTests: XCTestCase {

    // MARK: - Rule 4

    func testPairedFailureCollapsesToTheUpstreamRecord() {
        let entries = [
            GlanceSelectionTests.entry(id: "wrapper", type: "internal_tool_call", tool: "call_tool_read",
                                       status: "error", requestId: "req-1"),
            GlanceSelectionTests.entry(id: "upstream", type: "tool_call", server: "jira", tool: "get_issue",
                                       status: "error", requestId: "req-1")
        ]
        let rows = GlanceSelection.activityRows(from: entries)
        XCTAssertEqual(rows.count, 1)
        XCTAssertEqual(rows[0].newest.id, "upstream")
        XCTAssertEqual(rows[0].newest.serverName, "jira")
    }

    func testPreDispatchWrapperFailureWithNoPairStillRenders() {
        let entries = [
            GlanceSelectionTests.entry(id: "wrapper", type: "internal_tool_call", tool: "call_tool_read",
                                       status: "error", requestId: "req-2")
        ]
        let rows = GlanceSelection.activityRows(from: entries)
        XCTAssertEqual(rows.map(\.newest.id), ["wrapper"])
    }

    /// Rule 4 is about request identity, and two records without one are two
    /// distinct calls — they are never merged into a single record. Rule 5
    /// (grouping) then legitimately renders them as one ×2 row, so the
    /// assertion is on the records inside the run, not on the row count.
    func testRecordsWithoutRequestIDsAreNeverCollapsed() {
        let entries = [
            GlanceSelectionTests.entry(id: "a", type: "tool_call", server: "s", tool: "t"),
            GlanceSelectionTests.entry(id: "b", type: "tool_call", server: "s", tool: "t")
        ]
        let runs = GlanceSelection.activityRows(from: entries)
        XCTAssertEqual(runs.count, 1, "consecutive calls to one tool are one row…")
        XCTAssertEqual(runs[0].records.map(\.id), ["a", "b"], "…but both records survive into it")
        XCTAssertEqual(runs[0].count, 2)
    }

    func testCollapsedRowKeepsTheGroupsRecencyPosition() {
        let entries = [
            GlanceSelectionTests.entry(id: "newest", type: "tool_call", server: "a", tool: "t", requestId: "r-9"),
            GlanceSelectionTests.entry(id: "wrapper", type: "internal_tool_call", tool: "call_tool_read",
                                       status: "error", requestId: "r-8"),
            GlanceSelectionTests.entry(id: "upstream", type: "tool_call", server: "b", tool: "t",
                                       status: "error", requestId: "r-8")
        ]
        XCTAssertEqual(GlanceSelection.activityRows(from: entries).map(\.newest.id), ["newest", "upstream"])
    }

    // MARK: - Capping over a realistic page

    func testFiveRowsAreSelectedFromAFiftyRecordPageFullOfNoise() {
        var page: [ActivityEntry] = []
        // 40 management-built-in calls arrive first — rule 1 must drop them all.
        for i in 0..<40 {
            page.append(GlanceSelectionTests.entry(
                id: "mgmt-\(i)", type: "internal_tool_call",
                tool: i.isMultiple(of: 2) ? "upstream_servers" : "quarantine_security"))
        }
        // 4 successful wrappers — rule 3 drops them.
        for i in 0..<4 {
            page.append(GlanceSelectionTests.entry(
                id: "wrap-\(i)", type: "internal_tool_call", tool: "call_tool_read"))
        }
        // 6 real calls — only the first five become rows.
        for i in 0..<6 {
            page.append(GlanceSelectionTests.entry(
                id: "call-\(i)", type: "tool_call", server: "srv", tool: "tool\(i)"))
        }
        XCTAssertEqual(page.count, 50)

        let rows = GlanceSelection.activityRows(from: page)
        XCTAssertEqual(rows.map(\.newest.id), ["call-0", "call-1", "call-2", "call-3", "call-4"])
    }

    /// Depth, not just filtering. The client requests ONE page and applies
    /// rules 1-3 afterwards, so the rows the user sees are only as deep as that
    /// page: a burst of proxy-management calls at the head of the log pushes
    /// real calls off it, and the menu says "No tool calls yet" while the calls
    /// sit just below the fold.
    ///
    /// 90 management calls is not a contrived number — `upstream_servers` and
    /// `quarantine_security` are unconditionally dropped by rule 1, and an
    /// agent walking a server list makes them in bursts.
    func testFiveRowsSurviveABurstOfManagementCallsAtTheHeadOfTheLog() {
        var log: [ActivityEntry] = []   // newest first, as the endpoint returns it
        for i in 0..<90 {
            log.append(GlanceSelectionTests.entry(
                id: "mgmt-\(i)", type: "internal_tool_call",
                tool: i.isMultiple(of: 2) ? "upstream_servers" : "quarantine_security"))
        }
        for i in 0..<6 {
            log.append(GlanceSelectionTests.entry(
                id: "call-\(i)", type: "tool_call", server: "srv", tool: "tool\(i)"))
        }

        let page = Array(log.prefix(AppState.glanceActivityPageSize))
        let rows = GlanceSelection.activityRows(from: page)

        XCTAssertEqual(rows.map(\.newest.id), ["call-0", "call-1", "call-2", "call-3", "call-4"])
    }

    /// Five qualifying RECORDS are not five rows. A failed call emits a wrapper
    /// and an upstream record under one request id, and rule 4 collapses the
    /// pair — so the depth the rows need is measured in request groups, which is
    /// what the guarantee has to be stated in. The depth tests above use unique
    /// request ids and would never notice.
    func testFiveQualifyingRecordsSharingRequestIDsProduceFewerRows() {
        var page: [ActivityEntry] = []
        for i in 0..<3 {
            page.append(GlanceSelectionTests.entry(
                id: "wrapper-\(i)", type: "internal_tool_call", tool: "call_tool_read",
                status: "error", requestId: "req-\(i)"))
            page.append(GlanceSelectionTests.entry(
                id: "upstream-\(i)", type: "tool_call", server: "srv", tool: "tool\(i)",
                status: "error", requestId: "req-\(i)"))
        }

        let rows = GlanceSelection.activityRows(from: page)

        XCTAssertEqual(page.count, 6, "six qualifying records…")
        XCTAssertEqual(rows.map(\.newest.id), ["upstream-0", "upstream-1", "upstream-2"],
                       "…but three request groups, so three rows")
    }

    /// The honest residual, pinned so nobody has to rediscover it: the feed is
    /// exactly one page deep. Noise deeper than the page hides real calls, and
    /// the endpoint clamps `limit` at 100, so this is the floor of what a single
    /// request can promise — paging past it is possible (`offset` works
    /// end-to-end) but each request re-walks the whole activity bucket, which
    /// is the expensive part.
    func testRowsGoNoDeeperThanOnePage() {
        var log: [ActivityEntry] = []
        for i in 0..<(AppState.glanceActivityPageSize + 1) {
            log.append(GlanceSelectionTests.entry(
                id: "mgmt-\(i)", type: "internal_tool_call", tool: "upstream_servers"))
        }
        log.append(GlanceSelectionTests.entry(id: "call-0", type: "tool_call", server: "srv", tool: "t"))

        let page = Array(log.prefix(AppState.glanceActivityPageSize))

        XCTAssertTrue(GlanceSelection.activityRows(from: page).isEmpty,
                      "a call below the page is not shown; that is the documented limit")
    }

    func testFewerThanFiveQualifyingRecordsYieldsWhatThereIs() {
        let rows = GlanceSelection.activityRows(from: [
            GlanceSelectionTests.entry(id: "call-0", type: "tool_call", server: "srv", tool: "t")
        ])
        XCTAssertEqual(rows.count, 1)
    }

    func testEmptyInputYieldsNoRows() {
        XCTAssertTrue(GlanceSelection.activityRows(from: []).isEmpty)
    }

    // MARK: - Clients

    func testActiveClientsFiltersClosedSessionsAndCapsAtFive() {
        var sessions = [GlanceSelectionTests.session(id: "closed-1", status: "closed")]
        for i in 0..<7 {
            sessions.append(GlanceSelectionTests.session(id: "active-\(i)", status: "active"))
        }
        sessions.append(GlanceSelectionTests.session(id: "closed-2", status: "closed"))

        let clients = GlanceSelection.activeClients(from: sessions)
        XCTAssertEqual(clients.map(\.id), ["active-0", "active-1", "active-2", "active-3", "active-4"])
    }

    func testActiveClientsIsEmptyWhenEverySessionIsClosed() {
        let sessions = [
            GlanceSelectionTests.session(id: "a", status: "closed"),
            GlanceSelectionTests.session(id: "b", status: "closed")
        ]
        XCTAssertTrue(GlanceSelection.activeClients(from: sessions).isEmpty)
    }
}
