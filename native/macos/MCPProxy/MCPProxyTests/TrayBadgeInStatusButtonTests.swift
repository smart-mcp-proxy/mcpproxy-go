// TrayBadgeInStatusButtonTests.swift
// MCPProxyTests
//
// The badge, rendered inside a REAL NSStatusBarButton.
//
// Every other badge test renders TrayBadgeDotView standalone. That is exactly
// how a shipped bug hid: `NSStatusBarButton.isFlipped` is `true` (NSButton
// flips; a plain NSView does not), so geometry written for a bottom-left origin
// lands in the icon's TOP-right corner once the view is a child of the button —
// and a standalone render never sees it. This file closes that gap by putting
// the overlay where it actually lives and looking at the pixels.

import AppKit
import XCTest
@testable import MCPProxy

final class TrayBadgeInStatusButtonTests: XCTestCase {

    private var statusItem: NSStatusItem?

    override func tearDown() {
        if let item = statusItem { NSStatusBar.system.removeStatusItem(item) }
        statusItem = nil
        super.tearDown()
    }

    /// A real status item button.
    ///
    /// Throws `XCTSkip` — NOT a failure — when the environment has no status bar
    /// to attach one to. `swift test` runs on a `macos-latest` runner whose
    /// window-server state is not guaranteed; a headless agent returns a nil
    /// `button`, and turning that into a red build would punish CI for
    /// something this test cannot assert there. The assertions below are about
    /// AppKit geometry, which only means anything with a real button.
    private func makeStatusButton() throws -> NSStatusBarButton {
        let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        statusItem = item
        guard let button = item.button else {
            throw XCTSkip("no window server / status bar in this environment")
        }
        button.setFrameSize(NSSize(width: 36, height: 22))
        return button
    }

    /// The assumption the whole design rests on. If this ever changes, the
    /// badge silently moves corners — so assert it rather than trusting it.
    func testTheStatusButtonIsFlippedButTheOverlayIsNot() throws {
        let button = try makeStatusButton()
        XCTAssertTrue(button.isFlipped,
                      "NSStatusBarButton is expected to be flipped; the badge geometry depends on knowing that")

        let overlay = TrayBadgeDotView(frame: button.bounds)
        XCTAssertFalse(overlay.isFlipped,
                       "the overlay must stay bottom-left origin — badgeDotFrame is written for that space")
    }

    /// The regression itself: with the overlay installed the way the app
    /// installs it, the dot must render in the LOWER half of the button as the
    /// user sees it.
    ///
    /// Bitmap rows count from the TOP, so "visually low" means a LARGE row
    /// index. The shipped bug put the dot at rows 5-14 of 44; correct is ~29-38.
    func testTheDotRendersInTheVisualBottomRightOfTheButton() throws {
        let button = try makeStatusButton()

        let overlay = TrayBadgeDotView(frame: button.bounds)
        overlay.autoresizingMask = [.width, .height]
        overlay.fillColor = .systemRed
        button.addSubview(overlay)

        let rep = try XCTUnwrap(button.bitmapImageRepForCachingDisplay(in: button.bounds))
        button.cacheDisplay(in: button.bounds, to: rep)

        var minRow = Int.max, maxRow = Int.min, minCol = Int.max, maxCol = Int.min
        var found = false
        for row in 0..<rep.pixelsHigh {
            for col in 0..<rep.pixelsWide {
                guard let c = rep.colorAt(x: col, y: row)?.usingColorSpace(.sRGB) else { continue }
                // The badge red, distinguished from the monochrome template icon.
                guard c.alphaComponent > 0.5,
                      c.redComponent > 0.5,
                      c.redComponent > c.greenComponent + 0.2,
                      c.redComponent > c.blueComponent + 0.2 else { continue }
                found = true
                minRow = min(minRow, row); maxRow = max(maxRow, row)
                minCol = min(minCol, col); maxCol = max(maxCol, col)
            }
        }

        XCTAssertTrue(found, "the badge did not render inside the status button at all")

        let midRow = Double(minRow + maxRow) / 2
        let midCol = Double(minCol + maxCol) / 2
        XCTAssertGreaterThan(midRow, Double(rep.pixelsHigh) / 2,
                             "badge is in the TOP half — the flipped-coordinate bug is back "
                             + "(rows \(minRow)...\(maxRow) of \(rep.pixelsHigh))")
        XCTAssertGreaterThan(midCol, Double(rep.pixelsWide) / 2,
                             "badge is in the LEFT half (cols \(minCol)...\(maxCol) of \(rep.pixelsWide))")
    }

    /// The overlay covers the whole button, so the button must still be
    /// clickable through it — otherwise the status item stops opening its menu.
    func testTheButtonIsStillClickableThroughTheOverlay() throws {
        let button = try makeStatusButton()
        let overlay = TrayBadgeDotView(frame: button.bounds)
        overlay.autoresizingMask = [.width, .height]
        button.addSubview(overlay)

        // Dead centre, and the dot's own corner: neither may be captured.
        for point in [NSPoint(x: button.bounds.midX, y: button.bounds.midY),
                      NSPoint(x: button.bounds.maxX - 3, y: button.bounds.maxY - 3)] {
            let hit = button.hitTest(point)
            XCTAssertFalse(hit === overlay,
                           "the overlay captured the click at \(point) — the menu would not open")
        }
    }
}
