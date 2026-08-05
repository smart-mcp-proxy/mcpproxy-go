import XCTest
@testable import MCPProxy

/// The native tab-switch contract between the tray and the main window.
///
/// `showMainWindow(tab:)` has two paths — a fresh window seeds the sidebar
/// selection through `MainWindow(initialTab:)`, a live window receives a
/// `.switchToSidebarTab` notification — and the notification leg is stringly
/// typed (`SidebarItem` raw value as a String). These tests pin the decode
/// seam and the glance rows' destination so neither can silently rot.
@MainActor
final class MainWindowRoutingTests: XCTestCase {

    /// Every sidebar item survives the post-as-string / parse-on-receive
    /// round trip that `showMainWindow(tab:)` and the window's `onReceive`
    /// perform.
    func testEverySidebarItemRoundTripsThroughTheNotification() {
        for item in SidebarItem.allCases {
            let note = Notification(name: .switchToSidebarTab, object: item.rawValue)
            XCTAssertEqual(MainWindow.sidebarItem(from: note), item)
        }
    }

    /// A caller posting the SidebarItem itself (instead of its raw value) is
    /// dropped, not crashed on — the silent-drop is the documented contract.
    func testANonStringPayloadIsDropped() {
        let wrongType = Notification(name: .switchToSidebarTab, object: SidebarItem.activity)
        XCTAssertNil(MainWindow.sidebarItem(from: wrongType))

        let unknown = Notification(name: .switchToSidebarTab, object: "No Such Section")
        XCTAssertNil(MainWindow.sidebarItem(from: unknown))

        let empty = Notification(name: .switchToSidebarTab, object: nil)
        XCTAssertNil(MainWindow.sidebarItem(from: empty))
    }

    /// FR (menu QA 2026-08): a glance row or "Open Activity…" click must land
    /// on the native Activity section, not the Web UI. The destination is a
    /// constant precisely so this test exists without an app delegate.
    func testGlanceRowsRouteToTheNativeActivitySection() {
        XCTAssertEqual(AppController.glanceActivityDestination, .activity)
    }
}
