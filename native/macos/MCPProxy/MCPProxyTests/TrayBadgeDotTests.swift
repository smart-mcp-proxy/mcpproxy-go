// TrayBadgeDotTests.swift
// MCPProxyTests
//
// The severity badge moved from a full-size "● " beside the icon to a small dot
// in the icon's bottom-right corner. These assert the geometry and the drawing,
// because the status item itself cannot be screenshotted without a Screen
// Recording grant — "it looked right once" is not a regression test.

import AppKit
import XCTest
@testable import MCPProxy

final class TrayBadgeDotTests: XCTestCase {

    /// A 36x22 button is the size AppKit gives a status item holding an 18pt
    /// icon and no title — the shape this actually ships in.
    private static let buttonSize = CGSize(width: 36, height: 22)

    // MARK: - Geometry

    func testTheDotSitsInTheBottomRightOfTheIconNotTheButton() {
        let frame = TrayStatusIcon.badgeDotFrame(inButtonSize: Self.buttonSize)
        let iconOriginX = (Self.buttonSize.width - TrayStatusIcon.statusIconSide) / 2
        let iconOriginY = (Self.buttonSize.height - TrayStatusIcon.statusIconSide) / 2

        XCTAssertEqual(frame.maxX, iconOriginX + TrayStatusIcon.statusIconSide, accuracy: 0.01,
                       "the dot's right edge must meet the ICON's right edge")
        XCTAssertEqual(frame.minY, iconOriginY, accuracy: 0.01,
                       "the dot's bottom must meet the icon's bottom")
    }

    /// The complaint was that the badge was too big. 6pt on an 18pt icon is a
    /// third of its width — a badge, not a second symbol.
    func testTheDotIsSmallRelativeToTheIcon() {
        let frame = TrayStatusIcon.badgeDotFrame(inButtonSize: Self.buttonSize)
        XCTAssertEqual(frame.width, frame.height, "the badge is a circle")
        XCTAssertLessThanOrEqual(frame.width, TrayStatusIcon.statusIconSide / 2.5,
                                 "a dot larger than this reads as a second icon")
        XCTAssertGreaterThanOrEqual(frame.width, 4, "below this it is invisible on a retina menu bar")
    }

    /// It must stay inside the button whatever width AppKit hands us, or it is
    /// clipped away on one side and silently invisible.
    func testTheDotStaysInsideTheButtonAtAnyWidth() {
        for width in [24.0, 30.0, 36.0, 48.0] as [CGFloat] {
            let size = CGSize(width: width, height: 22)
            let frame = TrayStatusIcon.badgeDotFrame(inButtonSize: size)
            XCTAssertGreaterThanOrEqual(frame.minX, 0, "clipped left at width \(width)")
            XCTAssertLessThanOrEqual(frame.maxX, width, "clipped right at width \(width)")
            XCTAssertGreaterThanOrEqual(frame.minY, 0, "clipped bottom at width \(width)")
            XCTAssertLessThanOrEqual(frame.maxY, size.height, "clipped top at width \(width)")
        }
    }

    // MARK: - Drawing

    /// Renders the view offscreen and reads the centre pixel back. This is the
    /// assertion a screenshot would have made.
    private func centrePixel(of view: TrayBadgeDotView) -> NSColor? {
        guard let rep = view.bitmapImageRepForCachingDisplay(in: view.bounds) else { return nil }
        view.cacheDisplay(in: view.bounds, to: rep)
        return rep.colorAt(x: Int(view.bounds.midX), y: Int(view.bounds.midY))
    }

    func testAnErrorDrawsARedDot() {
        let view = TrayBadgeDotView(frame: NSRect(x: 0, y: 0, width: 12, height: 12))
        view.fillColor = .systemRed

        guard let colour = centrePixel(of: view)?.usingColorSpace(.sRGB) else {
            return XCTFail("nothing was drawn")
        }
        XCTAssertGreaterThan(colour.redComponent, 0.5, "the dot is not red")
        XCTAssertGreaterThan(colour.redComponent, colour.greenComponent)
        XCTAssertGreaterThan(colour.redComponent, colour.blueComponent)
        XCTAssertGreaterThan(colour.alphaComponent, 0.9, "the dot must be opaque")
    }

    func testAWarningDrawsAnAmberDotNotARedOne() {
        let view = TrayBadgeDotView(frame: NSRect(x: 0, y: 0, width: 12, height: 12))
        view.fillColor = .systemOrange

        guard let colour = centrePixel(of: view)?.usingColorSpace(.sRGB) else {
            return XCTFail("nothing was drawn")
        }
        XCTAssertGreaterThan(colour.greenComponent, 0.25,
                             "amber must be distinguishable from red at a glance")
        XCTAssertGreaterThan(colour.redComponent, colour.blueComponent)
    }

    // MARK: - It is decoration, not a control

    /// The dot overlaps the icon. Without hit-test passthrough it would swallow
    /// the clicks that open the menu, leaving a dead corner on the status item.
    func testTheDotDoesNotSwallowClicks() {
        let view = TrayBadgeDotView(frame: NSRect(x: 0, y: 0, width: 12, height: 12))
        XCTAssertNil(view.hitTest(NSPoint(x: 6, y: 6)),
                     "a click on the badge must reach the button underneath")
    }

    /// The button already publishes the whole state as its accessibility label;
    /// a second unlabelled element beside it is noise for a VoiceOver user.
    func testTheDotIsInvisibleToAccessibility() {
        let view = TrayBadgeDotView(frame: NSRect(x: 0, y: 0, width: 12, height: 12))
        XCTAssertTrue(view.accessibilityIsIgnored())
    }
}
