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
        XCTAssertEqual(rows[0].id, "upstream")
        XCTAssertEqual(rows[0].serverName, "jira")
    }

    func testPreDispatchWrapperFailureWithNoPairStillRenders() {
        let entries = [
            GlanceSelectionTests.entry(id: "wrapper", type: "internal_tool_call", tool: "call_tool_read",
                                       status: "error", requestId: "req-2")
        ]
        let rows = GlanceSelection.activityRows(from: entries)
        XCTAssertEqual(rows.map(\.id), ["wrapper"])
    }

    func testRecordsWithoutRequestIDsAreNeverCollapsed() {
        let entries = [
            GlanceSelectionTests.entry(id: "a", type: "tool_call", server: "s", tool: "t"),
            GlanceSelectionTests.entry(id: "b", type: "tool_call", server: "s", tool: "t")
        ]
        XCTAssertEqual(GlanceSelection.activityRows(from: entries).map(\.id), ["a", "b"])
    }

    func testCollapsedRowKeepsTheGroupsRecencyPosition() {
        let entries = [
            GlanceSelectionTests.entry(id: "newest", type: "tool_call", server: "a", tool: "t", requestId: "r-9"),
            GlanceSelectionTests.entry(id: "wrapper", type: "internal_tool_call", tool: "call_tool_read",
                                       status: "error", requestId: "r-8"),
            GlanceSelectionTests.entry(id: "upstream", type: "tool_call", server: "b", tool: "t",
                                       status: "error", requestId: "r-8")
        ]
        XCTAssertEqual(GlanceSelection.activityRows(from: entries).map(\.id), ["newest", "upstream"])
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
        XCTAssertEqual(rows.map(\.id), ["call-0", "call-1", "call-2", "call-3", "call-4"])
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
