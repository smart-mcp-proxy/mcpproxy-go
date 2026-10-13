import XCTest
import AppKit
@testable import MCPProxy

final class MenuRebuildGuardTests: XCTestCase {

    func testClosedMenuAlwaysRebuilds() {
        var guardState = MenuRebuildGuard()
        XCTAssertEqual(guardState.decide(structureChanged: false), .rebuild)
        XCTAssertEqual(guardState.decide(structureChanged: true), .rebuild)
        XCTAssertFalse(guardState.isDirty)
    }

    func testOpenMenuWithSameStructureUpdatesInPlace() {
        var guardState = MenuRebuildGuard()
        guardState.menuWillOpen()
        XCTAssertEqual(guardState.decide(structureChanged: false), .updateInPlace)
        XCTAssertEqual(guardState.decide(structureChanged: false), .updateInPlace)
        XCTAssertFalse(guardState.isDirty, "In-place updates must not owe a rebuild")
        XCTAssertFalse(guardState.menuDidClose(), "No rebuild is owed after in-place updates only")
    }

    func testStructuralChangeWhileOpenIsDeferredAndRunsOnceOnClose() {
        var guardState = MenuRebuildGuard()
        guardState.menuWillOpen()
        XCTAssertEqual(guardState.decide(structureChanged: true), .deferUntilClose)
        XCTAssertEqual(guardState.decide(structureChanged: true), .deferUntilClose)
        XCTAssertTrue(guardState.isDirty)

        XCTAssertTrue(guardState.menuDidClose(), "The deferred rebuild must run on close")
        XCTAssertFalse(guardState.isMenuOpen)
        XCTAssertFalse(guardState.menuDidClose(), "The deferred rebuild runs exactly once")
    }

    /// The stale flag can only come from a `menuDidClose` that never arrived —
    /// after a close that did arrive the flag is already clear, so reopening
    /// through the normal path proves nothing. Model the dropped close.
    func testReopeningClearsAStaleDirtyFlag() {
        var guardState = MenuRebuildGuard()
        guardState.menuWillOpen()
        _ = guardState.decide(structureChanged: true)
        XCTAssertTrue(guardState.isDirty)

        // No menuDidClose in between.
        guardState.menuWillOpen()
        XCTAssertFalse(guardState.isDirty,
                       "a dirty flag carried into a fresh open would defer a rebuild that is no longer owed")
        XCTAssertFalse(guardState.menuDidClose(),
                       "and would then fire that stale rebuild on the next close")
    }
}
