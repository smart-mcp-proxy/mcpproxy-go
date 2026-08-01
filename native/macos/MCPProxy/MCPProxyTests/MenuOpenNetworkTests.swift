import XCTest
import AppKit
@testable import MCPProxy

/// Spec 048's invariant, driven through the real controller: opening the tray
/// menu performs no network request (spec 090 FR-022 / SC-006).
///
/// Why it needs a seam rather than a stub sitting next to the code: the earlier
/// version of this check constructed a `CountingGlanceDataSource`, never handed
/// it to anything, and asserted it had not been called. It could not fail. The
/// controller's data source is now injectable, and its tray menu can be hosted
/// by something other than a live `NSStatusItem`, so the menu-open sequence runs
/// here exactly as it runs in production — `menuWillOpen` → `rebuildMenu` →
/// (menu open, state changes) → `rebuildMenu` → `updateInPlace` → `menuDidClose`
/// — with every request counted at two levels: the injected data source, and the
/// URL loading system underneath the `APIClient` the controller would otherwise
/// reach for.
@MainActor
final class MenuOpenNetworkTests: XCTestCase {

    /// A tray-menu host that is not an `NSStatusItem`, so the real rebuild path
    /// runs without putting an icon in the menu bar of whoever is running tests.
    private final class TestMenuHost: TrayMenuHost {
        var menu: NSMenu?
    }

    override func setUp() {
        super.setUp()
        GlanceStubURLProtocol.reset()
    }

    override func tearDown() {
        GlanceStubURLProtocol.reset()
        super.tearDown()
    }

    func testTheMenuOpenSequenceIssuesNoRequests() throws {
        let source = CountingGlanceDataSource()
        let host = TestMenuHost()
        let controller = AppController(glanceDataSource: source, menuHost: host)

        let state = controller.appState
        // The client the controller would use if some path went around the seam.
        // Its requests are recorded by the stub URL protocol, so a fetch added to
        // menuWillOpen tomorrow fails this test whichever route it takes.
        state.apiClient = GlanceStubURLProtocol.makeClient()
        state.coreState = .connected
        state.callsThisHour = 12
        state.glanceActivity = [
            Self.entry(id: "a", server: "github", tool: "create_issue",
                       timestamp: "2027-01-15T07:59:30Z"),
            Self.entry(id: "b", server: "jira", tool: "get_issue",
                       timestamp: "2027-01-15T07:58:00Z")
        ]
        state.glanceSessions = [Self.session(id: "sess-a", lastActivity: "2027-01-15T07:59:00Z")]

        controller.rebuildMenu()
        let menu = try XCTUnwrap(host.menu, "the controller must install its tray menu on the host")
        let requestsBefore = source.totalCallCount

        // The real sequence.
        controller.menuWillOpen(menu)
        // A poll lands while the user is reading the menu: the in-place branch.
        state.callsThisHour = 13
        controller.rebuildMenu()
        controller.menuDidClose(menu)

        // A fetch kicked off from the menu path would be started in a Task, so
        // give the run loop long enough for one to land. Without this the test
        // could only catch a synchronous regression, and there is no synchronous
        // way to fetch — every route is async.
        //
        // This window is the only thing between an async regression and a
        // vacuous pass, so it errs long: a runner slow enough to schedule that
        // Task in more than a second would otherwise report green. It costs the
        // full second only when there is nothing to find — the loop leaves the
        // moment a request appears.
        let deadline = Date().addingTimeInterval(1.0)
        while Date() < deadline, source.totalCallCount == requestsBefore,
              GlanceStubURLProtocol.requestedURLs.isEmpty {
            RunLoop.main.run(mode: .default, before: Date().addingTimeInterval(0.02))
        }

        XCTAssertEqual(source.totalCallCount - requestsBefore, 0,
                       "opening the menu must read state already in memory, never fetch")
        XCTAssertEqual(GlanceStubURLProtocol.requestedURLs, [],
                       "no request may reach the network from the menu-open path, by any route")
    }

    /// The guard against a vacuous pass: the sequence above really did build the
    /// glance and really did rewrite it in place. Without this, deleting the
    /// glance block entirely would leave the zero-request assertion green.
    func testTheSequenceActuallyBuiltAndUpdatedTheGlance() throws {
        let source = CountingGlanceDataSource()
        let host = TestMenuHost()
        let controller = AppController(glanceDataSource: source, menuHost: host)

        let state = controller.appState
        state.coreState = .connected
        state.callsThisHour = 12
        state.glanceActivity = [
            Self.entry(id: "a", server: "github", tool: "create_issue",
                       timestamp: "2027-01-15T07:59:30Z")
        ]
        state.glanceSessions = [Self.session(id: "sess-a", lastActivity: "2027-01-15T07:59:00Z")]

        controller.rebuildMenu()
        let menu = try XCTUnwrap(host.menu)
        controller.menuWillOpen(menu)

        let titles = menu.items.map(\.title)
        XCTAssertTrue(titles.contains("Recent"), "the glance block was not built at all")
        XCTAssertTrue(titles.contains { $0.hasPrefix("github:create_issue") },
                      "the activity rows were not built")
        let summary = try XCTUnwrap(menu.items.first { $0.title.contains("calls this hour") })

        state.callsThisHour = 13
        controller.rebuildMenu()

        XCTAssertTrue(summary.title.hasPrefix("13 calls this hour"),
                      "the open menu's rows must be rewritten in place, not left at menu-open time")
        XCTAssertEqual(source.totalCallCount, 0)
    }

    // MARK: - Helpers

    private static func entry(id: String,
                              server: String,
                              tool: String,
                              timestamp: String) -> ActivityEntry {
        let json: [String: Any] = [
            "id": id,
            "type": "tool_call",
            "status": "success",
            "timestamp": timestamp,
            "request_id": "req-\(id)",
            "server_name": server,
            "tool_name": tool
        ]
        let data = try! JSONSerialization.data(withJSONObject: json)
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(ActivityEntry.self, from: data)
    }

    private static func session(id: String, lastActivity: String) -> APIClient.MCPSession {
        let json: [String: Any] = [
            "id": id,
            "status": "active",
            "client_name": "Claude Code",
            "tool_call_count": 8,
            "last_activity": lastActivity
        ]
        let data = try! JSONSerialization.data(withJSONObject: json)
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(APIClient.MCPSession.self, from: data)
    }
}
