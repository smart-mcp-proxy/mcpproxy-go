import XCTest
import AppKit
@testable import MCPProxy

/// The exact policy `AppController.rebuildMenu()` applies before it touches the
/// menu: while the status-bar menu is on screen the glance rows are rewritten in
/// place, and anything structural waits for `menuDidClose`.
///
/// This matters more than an edge case. Task 6 made `MCPSession` Equatable so a
/// client row can show a live call count, which means `glanceSessions`
/// republishes on nearly every 30 s poll for any active session (`lastActivity`
/// moves between polls), and `glanceActivity` republishes on every reconcile.
/// Without this policy the menu would restructure under the cursor during
/// ordinary use.
@MainActor
final class GlanceMenuPolicyTests: XCTestCase {

    private final class ClickStub: NSObject {
        @objc func openGlanceRow(_ sender: NSMenuItem) {}
    }

    private let clickStub = ClickStub()

    private func makeSection() -> GlanceSection {
        GlanceSection(target: clickStub, action: #selector(ClickStub.openGlanceRow(_:)))
    }

    /// Row titles carry the relative age, so a later clock is enough to prove
    /// whether a row was rewritten.
    private static let later = GlanceFormatting.parseTimestamp("2027-01-15T08:05:00Z")!

    func testClosedMenuRebuildsAndLeavesRowsUntouched() {
        let state = GlanceFixtures.connectedState()
        let section = makeSection()
        let rows = section.items(for: state, now: GlanceFixtures.now)
        let firstRowTitle = rows[3].title
        XCTAssertEqual(firstRowTitle, "github:create_issue — 30s")

        var guardState = MenuRebuildGuard()
        XCTAssertEqual(guardState.decide(refreshing: section, from: state, now: Self.later), .rebuild)
        XCTAssertEqual(rows[3].title, firstRowTitle,
                       "a closed menu is rebuilt wholesale — the section must not be touched first")
    }

    func testOpenMenuRewritesRowsInPlace() {
        let state = GlanceFixtures.connectedState()
        let section = makeSection()
        let rows = section.items(for: state, now: GlanceFixtures.now)

        var guardState = MenuRebuildGuard()
        guardState.menuWillOpen()
        XCTAssertEqual(guardState.decide(refreshing: section, from: state, now: Self.later),
                       .updateInPlace)
        XCTAssertEqual(rows[3].title, "github:create_issue — 5m",
                       "the installed row itself must be rewritten, not a fresh copy of it")
        XCTAssertFalse(guardState.isDirty)
    }

    func testOpenMenuDefersAStructuralChangeUntilClose() {
        let state = GlanceFixtures.connectedState()
        let section = makeSection()
        let rows = section.items(for: state, now: GlanceFixtures.now)

        var guardState = MenuRebuildGuard()
        guardState.menuWillOpen()

        // A third call arrives: the Recent list grows by a row.
        state.glanceActivity.insert(
            GlanceFixtures.entry(id: "c", server: "slack", tool: "post_message",
                                 timestamp: "2027-01-15T07:59:50Z", session: "sess-a"),
            at: 0
        )

        XCTAssertEqual(guardState.decide(refreshing: section, from: state, now: Self.later),
                       .deferUntilClose)
        XCTAssertEqual(rows[3].title, "github:create_issue — 30s",
                       "a deferred rebuild must leave the on-screen rows exactly as they were")
        XCTAssertTrue(guardState.menuDidClose(), "the suppressed rebuild is owed on close")
    }

    /// The core going away is structural too — the whole block disappears — so
    /// it must not happen under the cursor either.
    func testCoreDisconnectWhileOpenIsDeferred() {
        let state = GlanceFixtures.connectedState()
        let section = makeSection()
        _ = section.items(for: state, now: GlanceFixtures.now)

        var guardState = MenuRebuildGuard()
        guardState.menuWillOpen()
        state.coreState = .idle

        XCTAssertEqual(guardState.decide(refreshing: section, from: state, now: Self.later),
                       .deferUntilClose)
        XCTAssertTrue(guardState.menuDidClose())
    }
}
