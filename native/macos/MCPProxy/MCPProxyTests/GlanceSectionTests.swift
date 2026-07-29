import XCTest
import AppKit
@testable import MCPProxy

@MainActor
final class GlanceSectionTests: XCTestCase {

    /// Fixed clock so relative ages are deterministic.
    static let now = GlanceFormatting.parseTimestamp("2027-01-15T08:00:00Z")!

    // MARK: - Visibility

    func testBlockIsHiddenWhenCoreIsNotConnected() {
        let state = Self.busyState()
        state.coreState = .idle
        let section = Self.makeSection()
        XCTAssertEqual(section.items(for: state, now: Self.now).count, 0)
    }

    func testBlockIsHiddenWhenUserStoppedTheCore() {
        let state = Self.busyState()
        state.isStopped = true
        let section = Self.makeSection()
        XCTAssertEqual(section.items(for: state, now: Self.now).count, 0)
    }

    // MARK: - Header

    func testHeaderShowsCallsThisHourAndClientCount() {
        let section = Self.makeSection()
        let items = section.items(for: Self.busyState(), now: Self.now)
        XCTAssertEqual(items.first?.title, "12 calls this hour · 1 client")
        XCTAssertFalse(items[0].isEnabled, "the header is a muted, non-clickable line")
    }

    func testHeaderOmitsCallCountUntilUsageLoads() {
        let state = Self.busyState()
        state.callsThisHour = nil
        let section = Self.makeSection()
        XCTAssertEqual(section.items(for: state, now: Self.now).first?.title, "1 client")
    }

    // MARK: - Recent section

    func testRecentSectionRendersQualifyingRows() {
        let section = Self.makeSection()
        let titles = section.items(for: Self.busyState(), now: Self.now).map {
            $0.isSeparatorItem ? "—" : $0.title
        }
        XCTAssertEqual(Array(titles.prefix(6)), [
            "12 calls this hour · 1 client",
            "—",
            "Recent",
            "github:create_issue — 30s",
            "jira:get_issue · auth failed — 2m",
            "Open Activity…"
        ])
    }

    func testActivityRowCarriesFullIdentity() {
        let section = Self.makeSection()
        let failed = section.items(for: Self.busyState(), now: Self.now)[4]
        XCTAssertEqual(failed.title, "jira:get_issue · auth failed — 2m")
        XCTAssertEqual(failed.representedObject as? String, "sess-b")
        XCTAssertEqual(failed.image?.accessibilityDescription, "failed")
        XCTAssertEqual(failed.toolTip, "jira:get_issue\nauth failed: token expired. retry after refresh")
        XCTAssertEqual(failed.accessibilityLabel(), "jira:get_issue, failed: auth failed, 2m ago")
        XCTAssertNotNil(failed.action)
    }

    func testOpenActivityRowHasNoSessionPayload() {
        let section = Self.makeSection()
        let items = section.items(for: Self.busyState(), now: Self.now)
        XCTAssertEqual(items[5].title, "Open Activity…")
        XCTAssertNil(items[5].representedObject)
        XCTAssertNotNil(items[5].action)
    }

    func testNoActivityShowsOneMutedRow() {
        let state = Self.busyState()
        state.glanceActivity = []
        let section = Self.makeSection()
        let row = section.items(for: state, now: Self.now)[3]
        XCTAssertEqual(row.title, "No tool calls yet")
        XCTAssertFalse(row.isEnabled)
    }

    func testFirstClauseKeepsOnlyTheLeadingClause() {
        XCTAssertEqual(GlanceSection.firstClause(of: "auth failed: token expired"), "auth failed")
        XCTAssertEqual(GlanceSection.firstClause(of: "dial tcp 127.0.0.1"), "dial tcp 127")
        XCTAssertEqual(GlanceSection.firstClause(of: "  boom  "), "boom")
        XCTAssertNil(GlanceSection.firstClause(of: "   "))
        XCTAssertNil(GlanceSection.firstClause(of: nil))
    }

    // MARK: - Clients section and histogram

    func testClientRowCarriesSessionIdentity() {
        let section = Self.makeSection()
        let client = section.items(for: Self.busyState(), now: Self.now)[8]
        XCTAssertEqual(client.title, "Claude Code — 8 calls · 1m")
        XCTAssertEqual(client.representedObject as? String, "sess-a")
        XCTAssertEqual(client.toolTip, "Claude Code 2.1.0")
        XCTAssertEqual(client.accessibilityLabel(), "Claude Code, 8 calls, last active 1m ago")
    }

    func testNoClientsShowsOneMutedRow() {
        let state = Self.busyState()
        state.glanceSessions = []
        let section = Self.makeSection()
        let row = section.items(for: state, now: Self.now)[8]
        XCTAssertEqual(row.title, "No connected clients")
        XCTAssertFalse(row.isEnabled)
    }

    func testHistogramSubmenuShowsLoadingUntilUsageArrives() {
        let section = Self.makeSection()
        let histogram = section.items(for: Self.busyState(), now: Self.now)[10]
        XCTAssertEqual(histogram.title, "Activity (24h)")
        XCTAssertEqual(histogram.submenu?.item(at: 0)?.title, "Loading…")
    }

    func testHistogramSubmenuUsesInjectedViewWhenAvailable() {
        let state = Self.busyState()
        state.usageTimeline = [UsageBucket(start: Self.now, calls: 12, errors: 1, totalRespBytes: 0)]
        let section = Self.makeSection()
        section.histogramViewBuilder = { buckets in
            let view = NSView(frame: NSRect(x: 0, y: 0, width: 240, height: 90))
            view.setAccessibilityLabel("\(buckets.count) buckets")
            return view
        }
        let chart = section.items(for: state, now: Self.now)[10].submenu?.item(at: 0)
        XCTAssertNotNil(chart?.view)
        XCTAssertEqual(chart?.view?.accessibilityLabel(), "1 buckets")
    }

    func testHistogramSubmenuFallsBackToTextWithoutABuilder() {
        let state = Self.busyState()
        state.usageTimeline = [UsageBucket(start: Self.now, calls: 12, errors: 1, totalRespBytes: 0)]
        let section = Self.makeSection()
        let items = section.items(for: state, now: Self.now)
        XCTAssertEqual(items[10].submenu?.item(at: 0)?.title, "12 calls · 1 error (24h)")
    }

    func testBlockLayoutOrder() {
        let section = Self.makeSection()
        let items = section.items(for: Self.busyState(), now: Self.now)
        let titles = items.map { $0.isSeparatorItem ? "—" : $0.title }
        XCTAssertEqual(titles, [
            "12 calls this hour · 1 client",
            "—",
            "Recent",
            "github:create_issue — 30s",
            "jira:get_issue · auth failed — 2m",
            "Open Activity…",
            "—",
            "Clients",
            "Claude Code — 8 calls · 1m",
            "—",
            "Activity (24h)",
            "—"
        ])
    }

    // MARK: - In-place updates

    func testUpdateInPlaceRewritesTheEntireRowIdentity() {
        let state = Self.busyState()
        let section = Self.makeSection()
        let items = section.items(for: state, now: Self.now)
        let row = items[3]

        state.glanceActivity = [
            Self.entry(id: "c", server: "obsidian", tool: "search_notes",
                       timestamp: "2027-01-15T07:59:55Z", session: "sess-c"),
            Self.entry(id: "b", server: "jira", tool: "get_issue", status: "error",
                       error: "auth failed: token expired. retry after refresh",
                       timestamp: "2027-01-15T07:58:00Z", session: "sess-b")
        ]
        state.callsThisHour = 13

        XCTAssertTrue(section.updateInPlace(for: state, now: Self.now))
        XCTAssertEqual(items[0].title, "13 calls this hour · 1 client")
        XCTAssertEqual(row.title, "obsidian:search_notes — 5s")
        XCTAssertEqual(row.representedObject as? String, "sess-c",
                       "the click payload must follow the title, or the row opens the previous record's session")
        XCTAssertEqual(row.image?.accessibilityDescription, "succeeded")
        XCTAssertEqual(row.toolTip, "obsidian:search_notes")
        XCTAssertEqual(row.accessibilityLabel(), "obsidian:search_notes, succeeded, 5s ago")
    }

    func testUpdateInPlaceRefusesStructuralChange() {
        let state = Self.busyState()
        let section = Self.makeSection()
        let items = section.items(for: state, now: Self.now)

        state.glanceActivity = [state.glanceActivity[0]]

        XCTAssertFalse(section.updateInPlace(for: state, now: Self.now),
                       "a row-count change must defer a rebuild, not mutate an open menu")
        XCTAssertEqual(items[3].title, "github:create_issue — 30s", "rows must be left untouched")
    }

    func testUpdateInPlaceRefusesWhenHistogramLoadednessFlips() {
        let state = Self.busyState()
        let section = Self.makeSection()
        _ = section.items(for: state, now: Self.now)

        state.usageTimeline = [UsageBucket(start: Self.now, calls: 12, errors: 1, totalRespBytes: 0)]

        XCTAssertFalse(section.updateInPlace(for: state, now: Self.now))
    }

    func testUpdateInPlaceBeforeFirstBuildReportsStructural() {
        XCTAssertFalse(Self.makeSection().updateInPlace(for: Self.busyState(), now: Self.now))
    }

    // MARK: - Status is carried by shape AND colour

    func testStatusIsEncodedByShapeAndColourNotColourAlone() {
        let stamp = "2027-01-15T07:59:30Z"
        let succeeded = Self.entry(id: "s", server: "a", tool: "t", timestamp: stamp)
        let failed = Self.entry(id: "f", server: "a", tool: "t", status: "error", timestamp: stamp)
        let pending = Self.entry(id: "p", server: "a", tool: "t", status: "running", timestamp: stamp)

        let shapes = [succeeded, failed, pending].map(GlanceFormatting.statusSymbolName(for:))
        XCTAssertEqual(Set(shapes).count, 3, "shape alone must separate the three outcomes")

        XCTAssertNotEqual(GlanceSection.statusTint(for: succeeded), GlanceSection.statusTint(for: failed))
        XCTAssertNotEqual(GlanceSection.statusTint(for: failed), GlanceSection.statusTint(for: pending))
        XCTAssertNotEqual(GlanceSection.statusTint(for: succeeded), GlanceSection.statusTint(for: pending))

        XCTAssertEqual(GlanceSection.outcomeDescription(for: pending), "in progress",
                       "a call still running must not be announced as failed")
    }

    func testStatusIconKeepsItsTintInTheMenu() {
        let section = Self.makeSection()
        let items = section.items(for: Self.busyState(), now: Self.now)
        XCTAssertEqual(items[3].image?.isTemplate, false,
                       "a template image is recoloured by the menu, which would drop the status tint")
        XCTAssertEqual(items[4].image?.isTemplate, false)
    }

    // MARK: - Helpers

    private final class ClickStub: NSObject {
        @objc func openGlanceRow(_ sender: NSMenuItem) {}
    }

    private static let clickStub = ClickStub()

    private static func makeSection() -> GlanceSection {
        GlanceSection(target: clickStub, action: #selector(ClickStub.openGlanceRow(_:)))
    }

    /// A connected core with two qualifying calls and one active client.
    private static func busyState() -> AppState {
        let state = AppState()
        // coreState first: its didSet clears the glance feeds on any non-connected state.
        state.coreState = .connected
        state.callsThisHour = 12
        state.glanceActivity = [
            entry(id: "a", server: "github", tool: "create_issue",
                  timestamp: "2027-01-15T07:59:30Z", session: "sess-a"),
            entry(id: "b", server: "jira", tool: "get_issue", status: "error",
                  error: "auth failed: token expired. retry after refresh",
                  timestamp: "2027-01-15T07:58:00Z", session: "sess-b")
        ]
        state.glanceSessions = [
            session(id: "sess-a", name: "Claude Code", version: "2.1.0",
                    calls: 8, lastActivity: "2027-01-15T07:59:00Z")
        ]
        return state
    }

    private static func entry(
        id: String,
        type: String = "tool_call",
        server: String? = nil,
        tool: String? = nil,
        status: String = "success",
        error: String? = nil,
        timestamp: String,
        session: String? = nil
    ) -> ActivityEntry {
        var json: [String: Any] = [
            "id": id,
            "type": type,
            "status": status,
            "timestamp": timestamp,
            "request_id": "req-\(id)"
        ]
        if let server { json["server_name"] = server }
        if let tool { json["tool_name"] = tool }
        if let error { json["error_message"] = error }
        if let session { json["session_id"] = session }
        let data = try! JSONSerialization.data(withJSONObject: json)
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(ActivityEntry.self, from: data)
    }

    private static func session(
        id: String,
        name: String,
        version: String,
        calls: Int,
        lastActivity: String
    ) -> APIClient.MCPSession {
        let json: [String: Any] = [
            "id": id,
            "status": "active",
            "client_name": name,
            "client_version": version,
            "tool_call_count": calls,
            "last_activity": lastActivity
        ]
        let data = try! JSONSerialization.data(withJSONObject: json)
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(APIClient.MCPSession.self, from: data)
    }
}
