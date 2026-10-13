import XCTest
import AppKit
@testable import MCPProxy

/// Click routing for glance rows: every clickable row must carry BOTH a target
/// and an action, and the session id it hands the Web UI link must survive the
/// trip through `representedObject`.
@MainActor
final class GlanceRowRoutingTests: XCTestCase {

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
        let items = section.items(for: GlanceFixtures.connectedState(), now: GlanceFixtures.now)

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
    /// the same thing downstream — open the log with no session context.
    func testNilRepresentedObjectOpensTheUnfilteredLog() throws {
        let state = GlanceFixtures.connectedState()
        state.glanceActivity = [
            GlanceFixtures.entry(id: "a", server: "github", tool: "create_issue",
                                 timestamp: "2027-01-15T07:59:30Z", session: nil)
        ]
        let spy = ClickSpy()
        let section = GlanceSection(target: spy, action: #selector(ClickSpy.openGlanceRow(_:)))
        let items = section.items(for: state, now: GlanceFixtures.now)

        let unattributedRow = try XCTUnwrap(items.first { $0.title.hasPrefix("github:create_issue") })
        let openActivityRow = try XCTUnwrap(items.first { $0.title == "Open Activity…" })

        XCTAssertNil(unattributedRow.representedObject,
                     "a record with no session_id must not carry a stale id")
        XCTAssertNil(openActivityRow.representedObject,
                     "the Open Activity… row is deliberately unfiltered")
    }
}
