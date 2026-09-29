import AppKit
import SwiftUI
import XCTest
@testable import MCPProxy

final class FirstRunDialogSizingTests: XCTestCase {
    func testKeepsTheOriginalMinimumHeightForShortContent() {
        let size = firstRunDialogContentSize(fittingHeight: 180)
        XCTAssertEqual(size.width, firstRunDialogWidth)
        XCTAssertEqual(size.height, firstRunDialogMinHeight)
    }

    func testGrowsToFitTallContentInsteadOfClipping() {
        let size = firstRunDialogContentSize(fittingHeight: 431.2)
        XCTAssertEqual(size.height, 432, "a taller layout must not be clipped to 300")
    }

    /// The real dialog's own fitting height must fit inside the size the
    /// presenter computes from it.
    @MainActor
    func testTheRealDialogFitsItsComputedWindowSize() {
        let host = NSHostingController(
            rootView: FirstRunDialog(launchAtLogin: .constant(true), onContinue: {}))
        host.view.layoutSubtreeIfNeeded()
        let fitting = host.view.fittingSize.height
        XCTAssertGreaterThan(fitting, 0)
        let size = firstRunDialogContentSize(fittingHeight: fitting)
        XCTAssertGreaterThanOrEqual(size.height, fitting)
        XCTAssertEqual(size.width, firstRunDialogWidth)
    }
}
