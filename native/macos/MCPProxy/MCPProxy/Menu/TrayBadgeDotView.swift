// TrayBadgeDotView.swift
// MCPProxy
//
// The small coloured dot overlaid on the bottom-right corner of the menu-bar
// icon when a server carries a warn/error diagnostic.

import AppKit

/// A coloured badge dot drawn over the status item's template icon.
///
/// Why a subview and not pixels in `button.image`: an `NSStatusItem` image must
/// stay `isTemplate` so AppKit re-tints it for light/dark menu bars and inverts
/// it while the menu is open. Template rendering forces every pixel to the
/// menu-bar's own colour, so a red dot composited into the image comes back
/// black. A sibling view is drawn after the template pass and keeps its colour
/// in all three appearances.
final class TrayBadgeDotView: NSView {

    /// The dot's fill. Setting it redraws; setting it to the same value is a
    /// no-op so the status poll does not schedule redundant redraws.
    var fillColor: NSColor = .systemRed {
        didSet {
            guard fillColor != oldValue else { return }
            needsDisplay = true
        }
    }

    override init(frame frameRect: NSRect) {
        super.init(frame: frameRect)
        wantsLayer = true
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("TrayBadgeDotView is created in code, never from a nib")
    }

    /// The dot is decoration on a button. Without this it would swallow the
    /// clicks that open the menu wherever it overlaps the icon — a status item
    /// with a dead corner.
    override func hitTest(_ point: NSPoint) -> NSView? { nil }

    /// Decoration must never take focus or appear as a separate element to
    /// VoiceOver: the button already publishes the full state as its
    /// accessibility label (`TrayStatusIcon.accessibilityLabel`), and a second,
    /// unlabelled element beside it would just be noise.
    override func accessibilityIsIgnored() -> Bool { true }

    override func draw(_ dirtyRect: NSRect) {
        let rect = bounds.insetBy(dx: 0.5, dy: 0.5)
        guard rect.width > 0, rect.height > 0 else { return }

        let path = NSBezierPath(ovalIn: rect)
        fillColor.setFill()
        path.fill()

        // A darker rim of the fill's own hue, so the dot still reads as a
        // distinct shape where it overlaps the icon glyph. Deliberately NOT the
        // menu-bar background: that is translucent over the user's wallpaper
        // and has no colour this view can name.
        if let rim = fillColor.blended(withFraction: 0.35, of: .black) {
            rim.setStroke()
            path.lineWidth = 1
            path.stroke()
        }
    }
}
