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
        let firstRowTitle = rows[4].title
        XCTAssertEqual(firstRowTitle, "github:create_issue — 30s")

        var guardState = MenuRebuildGuard()
        XCTAssertEqual(guardState.decide(refreshing: section, from: state, now: Self.later), .rebuild)
        XCTAssertEqual(rows[4].title, firstRowTitle,
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
        XCTAssertEqual(rows[4].title, "github:create_issue — 5m",
                       "the installed row itself must be rewritten, not a fresh copy of it")
        XCTAssertFalse(guardState.isDirty)
    }

    func testOpenMenuDefersAStructuralChangeUntilClose() {
        let state = GlanceFixtures.connectedState()
        let section = makeSection()
        let rows = section.items(for: state, now: GlanceFixtures.now)

        let summaryBefore = rows[0].title
        XCTAssertEqual(summaryBefore, "12 calls in the last 24h · 1 active")

        var guardState = MenuRebuildGuard()
        guardState.menuWillOpen()

        // A third call arrives: the Recent list grows by a row, and the header
        // count moves with it. The header is set deliberately: refusing an
        // update has to change *nothing*, and a header that alone kept moving
        // would describe a set of rows that is not the one below it.
        state.usageTimeline = [UsageBucket(start: GlanceFixtures.now, calls: 99, errors: 0, totalRespBytes: 0)]
        state.glanceActivity.insert(
            GlanceFixtures.entry(id: "c", server: "slack", tool: "post_message",
                                 timestamp: "2027-01-15T07:59:50Z", session: "sess-a"),
            at: 0
        )

        XCTAssertEqual(guardState.decide(refreshing: section, from: state, now: Self.later),
                       .deferUntilClose)
        XCTAssertEqual(rows[4].title, "github:create_issue — 30s",
                       "a deferred rebuild must leave the on-screen rows exactly as they were")
        XCTAssertEqual(rows[0].title, summaryBefore,
                       "'99 calls' over the old three rows is the half-update the defer exists to prevent")
        XCTAssertTrue(guardState.menuDidClose(), "the suppressed rebuild is owed on close")
    }

    // MARK: - Line counts are structural, and the check is atomic (spec 090 FR-023)

    /// A reason appearing on a row that had none turns a one-line row into a
    /// two-line one. That resizes the menu under the cursor, so it is structural
    /// and must wait for close, exactly like a row-count change.
    ///
    /// The row that changes is deliberately the LAST one: the check has to be a
    /// preflight over every row, not a per-row bail-out. A loop that refused
    /// mid-way would already have rewritten the summary and the earlier rows,
    /// leaving a half-updated menu on screen — the very thing deferring exists
    /// to prevent.
    func testALaterRowsLineCountChangeIsRefusedWithoutTouchingEarlierRows() {
        let state = GlanceFixtures.connectedState()
        let section = makeSection()
        section.supportsRowSubtitles = true
        let rows = section.items(for: state, now: GlanceFixtures.now)

        let summaryBefore = rows[0].title
        let firstRowBefore = rows[4].title
        XCTAssertEqual(firstRowBefore, "github:create_issue — 30s")

        // Only the SECOND row changes shape; the first row's own text would
        // still move, because `later` is five minutes on.
        state.usageTimeline = [UsageBucket(start: GlanceFixtures.now, calls: 99, errors: 0, totalRespBytes: 0)]
        state.glanceActivity[1] = GlanceFixtures.entry(
            id: "b", server: "jira", tool: "get_issue",
            timestamp: "2027-01-15T07:58:00Z", session: nil,
            reason: "Verify the failed transition did not change the ticket")

        XCTAssertFalse(section.updateInPlace(for: state, now: Self.later),
                       "a row gaining a second line resizes the menu — that waits for close")
        XCTAssertEqual(rows[0].title, summaryBefore,
                       "the summary is written before the rows; a refusal must not leave it ahead of them")
        XCTAssertEqual(rows[4].title, firstRowBefore,
                       "an earlier row must not be rewritten by an update that then refuses")
        XCTAssertEqual(rows[5].title, "jira:get_issue — 2m")
    }

    /// The mirror case: a reason disappearing shrinks the row, and is refused
    /// the same way.
    func testARowLosingItsReasonIsAlsoStructural() {
        let state = GlanceFixtures.connectedState()
        state.glanceActivity[0] = GlanceFixtures.entry(
            id: "a", server: "github", tool: "create_issue",
            timestamp: "2027-01-15T07:59:30Z", session: "sess-a",
            reason: "Open the follow-up ticket")
        let section = makeSection()
        section.supportsRowSubtitles = true
        let rows = section.items(for: state, now: GlanceFixtures.now)

        state.glanceActivity[0] = GlanceFixtures.entry(
            id: "a", server: "github", tool: "create_issue",
            timestamp: "2027-01-15T07:59:30Z", session: "sess-a")

        XCTAssertFalse(section.updateInPlace(for: state, now: Self.later))
        XCTAssertEqual(rows[4].title, "github:create_issue — 30s",
                       "the refused update must leave the row exactly as it was")
    }

    /// …and the guard must not freeze the menu: a reason whose TEXT changes
    /// keeps the same line count, so it is an ordinary in-place rewrite.
    func testAChangedReasonOnATwoLineRowStillUpdatesInPlace() {
        let state = GlanceFixtures.connectedState()
        state.glanceActivity[0] = GlanceFixtures.entry(
            id: "a", server: "github", tool: "create_issue",
            timestamp: "2027-01-15T07:59:30Z", session: "sess-a",
            reason: "Open the follow-up ticket")
        let section = makeSection()
        section.supportsRowSubtitles = true
        let rows = section.items(for: state, now: GlanceFixtures.now)

        state.glanceActivity[0] = GlanceFixtures.entry(
            id: "a", server: "github", tool: "create_issue",
            timestamp: "2027-01-15T07:59:30Z", session: "sess-a",
            reason: "Open the follow-up ticket for the reporter")

        XCTAssertTrue(section.updateInPlace(for: state, now: Self.later))
        XCTAssertEqual(rows[4].title, "github:create_issue — 5m")
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
