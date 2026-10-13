import AppKit
import SwiftUI
import XCTest
@testable import MCPProxy

/// Spec 108-k (K13/K20): a closed Settings window must not keep a live
/// SettingsView that could consume the next window's pending route.
@MainActor
final class SettingsWindowRetirementTests: XCTestCase {
    func testRetiringAWindowDropsItsSwiftUIContent() {
        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 100, height: 100),
            styleMask: [.titled, .closable], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.contentView = NSHostingView(rootView: Text("settings"))
        XCTAssertNotNil(window.contentView)
        SettingsWindowRetirement.retire(window)
        XCTAssertNil(window.contentView)
    }
}
