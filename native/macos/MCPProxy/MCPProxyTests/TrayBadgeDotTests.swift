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

    /// Every opaque pixel the view painted, plus their bounding box, found by
    /// scanning the rendered bitmap.
    ///
    /// Scanning rather than sampling one computed point: the earlier version of
    /// this test guessed at the bitmap's y-flip and read a transparent pixel,
    /// which looks exactly like "the view drew nothing". Finding the marks the
    /// view actually made asserts position, colour and restraint at once, and
    /// keeps working if the geometry is retuned.
    private func paintedMarks(of view: TrayBadgeDotView) -> (box: NSRect, opaque: Int, sample: NSColor)? {
        guard let rep = view.bitmapImageRepForCachingDisplay(in: view.bounds) else { return nil }
        view.cacheDisplay(in: view.bounds, to: rep)

        var minX = Int.max, minY = Int.max, maxX = Int.min, maxY = Int.min
        var count = 0
        var sample: NSColor?
        for y in 0..<rep.pixelsHigh {
            for x in 0..<rep.pixelsWide {
                guard let c = rep.colorAt(x: x, y: y), c.alphaComponent > 0.5 else { continue }
                count += 1
                minX = min(minX, x); maxX = max(maxX, x)
                minY = min(minY, y); maxY = max(maxY, y)
                if sample == nil || c.alphaComponent > 0.95 { sample = c }
            }
        }
        guard count > 0, let sample else { return nil }
        // The rep is in DEVICE pixels (2x on retina); report POINTS so the
        // assertions can be written against the same units as badgeDotFrame.
        let scale = CGFloat(rep.pixelsWide) / view.bounds.width
        let box = NSRect(x: CGFloat(minX) / scale,
                         y: CGFloat(minY) / scale,
                         width: CGFloat(maxX - minX + 1) / scale,
                         height: CGFloat(maxY - minY + 1) / scale)
        return (box, Int(CGFloat(count) / (scale * scale)), sample)
    }

    func testAnErrorDrawsARedDot() {
        let view = TrayBadgeDotView(frame: NSRect(origin: .zero, size: Self.buttonSize))
        view.fillColor = .systemRed

        guard let marks = paintedMarks(of: view) else { return XCTFail("nothing was drawn") }
        guard let colour = marks.sample.usingColorSpace(.sRGB) else { return XCTFail("no colour space") }

        XCTAssertGreaterThan(colour.redComponent, 0.5, "the dot is not red")
        XCTAssertGreaterThan(colour.redComponent, colour.greenComponent)
        XCTAssertGreaterThan(colour.redComponent, colour.blueComponent)
    }

    func testAWarningDrawsAnAmberDotNotARedOne() {
        let view = TrayBadgeDotView(frame: NSRect(origin: .zero, size: Self.buttonSize))
        view.fillColor = .systemOrange

        guard let marks = paintedMarks(of: view) else { return XCTFail("nothing was drawn") }
        guard let colour = marks.sample.usingColorSpace(.sRGB) else { return XCTFail("no colour space") }

        XCTAssertGreaterThan(colour.greenComponent, 0.25,
                             "amber must be distinguishable from red at a glance")
        XCTAssertGreaterThan(colour.redComponent, colour.blueComponent)
    }

    /// What is painted must be the small corner dot — not a wash over the whole
    /// icon, which is the way "make the overlay fill the button" goes wrong.
    func testOnlyTheCornerDotIsPainted() {
        let view = TrayBadgeDotView(frame: NSRect(origin: .zero, size: Self.buttonSize))
        view.fillColor = .systemRed

        guard let marks = paintedMarks(of: view) else { return XCTFail("nothing was drawn") }
        let expected = TrayStatusIcon.badgeDotFrame(inButtonSize: Self.buttonSize)

        XCTAssertLessThanOrEqual(marks.box.width, expected.width + 1,
                                 "painted wider than the dot — the overlay is washing the icon")
        XCTAssertLessThanOrEqual(marks.box.height, expected.height + 1,
                                 "painted taller than the dot")

        let buttonArea = Self.buttonSize.width * Self.buttonSize.height
        XCTAssertLessThan(Double(marks.opaque), Double(buttonArea) * 0.15,
                          "the badge must cover a small fraction of the button, got \(marks.opaque)px")

        // Right-hand half, lower half: the corner the badge belongs in.
        XCTAssertGreaterThan(marks.box.midX, Self.buttonSize.width / 2, "dot is not on the right")
        XCTAssertGreaterThan(marks.box.midY, Self.buttonSize.height / 2,
                             "dot is not low (bitmap y grows downward, so low = large y)")
    }

    // MARK: - It survives a resize

    /// The regression cross-model review (gpt-5.6-sol) found: the badge used to
    /// be a 6x6 subview pinned with [.minXMargin, .maxYMargin], which anchors to
    /// the BUTTON's corner. The icon is CENTRED, so when the button shrinks by N
    /// the right-pinned dot moves left by N while the icon's right edge moves
    /// left by only N/2 — the badge drifts off the glyph. On top of that,
    /// NSStatusItem resizes asynchronously, so the bounds read right after
    /// clearing attributedTitle were the old, wider ones.
    ///
    /// The view now fills the button and derives the dot from its live bounds,
    /// so the badge is correct at EVERY width, not just the one it was born at.
    func testTheDotTracksTheIconWhenTheButtonResizes() {
        let view = TrayBadgeDotView(frame: NSRect(x: 0, y: 0, width: 48, height: 22))
        view.fillColor = .systemRed

        for width in [48.0, 36.0, 30.0, 24.0] as [CGFloat] {
            view.frame = NSRect(x: 0, y: 0, width: width, height: 22)
            let dot = TrayStatusIcon.badgeDotFrame(inButtonSize: view.bounds.size)
            let iconRight = (width - TrayStatusIcon.statusIconSide) / 2 + TrayStatusIcon.statusIconSide
            XCTAssertEqual(dot.maxX, iconRight, accuracy: 0.01,
                           "badge drifted off the icon at button width \(width)")
        }
    }

    /// A resize must actually re-render the dot at the new position — the
    /// point of computing it in `draw(_:)`. Asserting the rendered output
    /// rather than the `needsDisplay` flag, which a layer-backed view does not
    /// report reliably. (`needsDisplayOnBoundsChange` is a CALayer property and
    /// does not exist on NSView at all.)
    func testResizingRedrawsTheDotInItsNewPlace() {
        let view = TrayBadgeDotView(frame: NSRect(x: 0, y: 0, width: 48, height: 22))
        view.fillColor = .systemRed
        guard let wide = paintedMarks(of: view) else { return XCTFail("nothing drawn at 48pt") }

        view.setFrameSize(NSSize(width: 28, height: 22))
        guard let narrow = paintedMarks(of: view) else { return XCTFail("nothing drawn at 28pt") }

        XCTAssertNotEqual(wide.box.midX, narrow.box.midX,
                          "the dot did not move when the button shrank")
        let expected = TrayStatusIcon.badgeDotFrame(inButtonSize: NSSize(width: 28, height: 22))
        XCTAssertEqual(narrow.box.midX, expected.midX, accuracy: 1.5,
                       "after the resize the dot is not where the icon's corner now is")
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
