import XCTest
import AppKit
@testable import MCPProxy

/// Click routing for glance rows: every clickable row must carry BOTH a target
/// and an action, and the session id it hands the Web UI link must survive the
/// trip through `representedObject`.
@MainActor
final class GlanceRowRoutingTests: XCTestCase {

    static let now = GlanceFormatting.parseTimestamp("2027-01-15T08:00:00Z")!

    private final class ClickSpy: NSObject {
        /// One entry per click, holding the session id the row handed over.
        var clicks: [String?] = []
        @objc func openGlanceRow(_ sender: NSMenuItem) {
            clicks.append(sender.representedObject as? String)
        }
    }

    /// A menu item with an action but no target routes up the responder chain
    /// and is dropped — in a status-bar menu the row simply does nothing, with
    /// no error anywhere. Pin both halves of the pair, and prove the pair
    /// actually dispatches rather than only comparing fields.
    func testEveryClickableRowCarriesBothTargetAndAction() {
        let spy = ClickSpy()
        let section = GlanceSection(target: spy, action: #selector(ClickSpy.openGlanceRow(_:)))
        let items = section.items(for: Self.busyState(), now: Self.now)

        // A submenu item is not a glance click row: assigning `submenu`
        // makes AppKit install its own `submenuAction:` and open the submenu
        // itself, so the histogram row has an action but no target by design.
        let clickable = items.filter { $0.action != nil && $0.submenu == nil }
        XCTAssertEqual(clickable.count, 4,
                       "two activity rows, Open Activity…, and one client row")

        for item in clickable {
            XCTAssertEqual(item.action, #selector(ClickSpy.openGlanceRow(_:)))
            XCTAssertTrue(item.target === spy,
                          "a nil target makes the row silently do nothing")
        }

        for item in clickable {
            let sent = NSApplication.shared.sendAction(item.action!, to: item.target, from: item)
            XCTAssertTrue(sent, "row '\(item.title)' did not dispatch")
        }
        XCTAssertEqual(spy.clicks, ["sess-a", nil, nil, "sess-a"],
                       "each row hands over its own session id")
    }

    /// Two producers make `representedObject` nil: a record the core never
    /// attributed to a session, and the "Open Activity…" row itself. Both mean
    /// the same thing downstream — open the unfiltered log.
    func testNilRepresentedObjectOpensTheUnfilteredLog() throws {
        let state = Self.busyState()
        state.glanceActivity = [
            Self.entry(id: "a", server: "github", tool: "create_issue",
                       timestamp: "2027-01-15T07:59:30Z", session: nil)
        ]
        let spy = ClickSpy()
        let section = GlanceSection(target: spy, action: #selector(ClickSpy.openGlanceRow(_:)))
        let items = section.items(for: state, now: Self.now)

        let unattributedRow = try XCTUnwrap(items.first { $0.title.hasPrefix("github:create_issue") })
        let openActivityRow = try XCTUnwrap(items.first { $0.title == "Open Activity…" })

        XCTAssertNil(unattributedRow.representedObject,
                     "a record with no session_id must not carry a stale id")
        XCTAssertNil(openActivityRow.representedObject,
                     "the Open Activity… row is deliberately unfiltered")

        for row in [unattributedRow, openActivityRow] {
            XCTAssertEqual(
                activityURLString(baseURL: "http://127.0.0.1:8080",
                                  apiKey: "",
                                  sessionID: row.representedObject as? String),
                "http://127.0.0.1:8080/ui/activity"
            )
        }
    }

    // MARK: - Fixtures

    private static func busyState() -> AppState {
        let state = AppState()
        state.coreState = .connected
        state.callsThisHour = 12
        state.glanceActivity = [
            entry(id: "a", server: "github", tool: "create_issue",
                  timestamp: "2027-01-15T07:59:30Z", session: "sess-a"),
            entry(id: "b", server: "jira", tool: "get_issue",
                  timestamp: "2027-01-15T07:58:00Z", session: nil)
        ]
        state.glanceSessions = [
            session(id: "sess-a", name: "Claude Code", calls: 8,
                    lastActivity: "2027-01-15T07:59:00Z")
        ]
        return state
    }

    private static func entry(
        id: String,
        server: String,
        tool: String,
        timestamp: String,
        session: String?
    ) -> ActivityEntry {
        var json: [String: Any] = [
            "id": id,
            "type": "tool_call",
            "status": "success",
            "timestamp": timestamp,
            "request_id": "req-\(id)",
            "server_name": server,
            "tool_name": tool
        ]
        if let session { json["session_id"] = session }
        let data = try! JSONSerialization.data(withJSONObject: json)
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(ActivityEntry.self, from: data)
    }

    private static func session(
        id: String,
        name: String,
        calls: Int,
        lastActivity: String
    ) -> APIClient.MCPSession {
        let json: [String: Any] = [
            "id": id,
            "status": "active",
            "client_name": name,
            "tool_call_count": calls,
            "last_activity": lastActivity
        ]
        let data = try! JSONSerialization.data(withJSONObject: json)
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(APIClient.MCPSession.self, from: data)
    }
}
