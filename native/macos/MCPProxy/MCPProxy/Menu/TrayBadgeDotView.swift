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
///
/// Why the view fills the WHOLE button rather than being a 6x6 view positioned
/// at the corner: the dot is anchored to the centred 18pt ICON, not to the
/// button, and the button's width changes (it is `variableLength`, and the core
/// -state glyph comes and goes from `attributedTitle`). A small right-pinned
/// subview breaks that anchor twice over —
///
///  * `[.minXMargin, .maxYMargin]` pins to the BUTTON's bottom-right, so when
///    the button shrinks by N the dot moves left by N while the centred icon's
///    right edge moves left by only N/2; and
///  * `NSStatusItem` resizes asynchronously, so `button.bounds` read straight
///    after clearing `attributedTitle` is still the old, wider value — the dot
///    is laid out against an icon centre that no longer exists.
///
/// Filling the bounds and computing the dot inside `draw(_:)` from the CURRENT
/// bounds removes both failure modes: any resize just redraws in the right
/// place. (Cross-model review, gpt-5.6-sol.)
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

    /// The dot's position is derived from `bounds` at draw time, so a resize
    /// MUST repaint or the badge is left at the previous geometry.
    ///
    /// This is the NSView spelling of it. `needsDisplayOnBoundsChange` is a
    /// CALayer property and does not exist on NSView — reaching for it here
    /// simply fails to compile.
    override func setFrameSize(_ newSize: NSSize) {
        let changed = newSize != frame.size
        super.setFrameSize(newSize)
        if changed { needsDisplay = true }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("TrayBadgeDotView is created in code, never from a nib")
    }

    /// The view covers the whole button, so this is what keeps the status item
    /// clickable at all: without it the badge would swallow every click that
    /// opens the menu.
    override func hitTest(_ point: NSPoint) -> NSView? { nil }

    /// Decoration must never take focus or appear as a separate element to
    /// VoiceOver: the button already publishes the full state as its
    /// accessibility label (`TrayStatusIcon.accessibilityLabel`), and a second,
    /// unlabelled element beside it would just be noise.
    override func accessibilityIsIgnored() -> Bool { true }

    override func draw(_ dirtyRect: NSRect) {
        // Recomputed every draw from the CURRENT bounds — see the type comment.
        let dot = TrayStatusIcon.badgeDotFrame(inButtonSize: bounds.size)
        let rect = dot.insetBy(dx: 0.5, dy: 0.5)
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
