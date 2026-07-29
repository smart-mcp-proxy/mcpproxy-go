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
