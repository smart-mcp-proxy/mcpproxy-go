import XCTest
@testable import MCPProxy

/// Spec 109-i (FR-050, FR-052): the native sidebar has the same names, groups
/// and order as the Web UI (Home, Connect, Protect, Monitor), and the toolbar
/// "+" menu offers Server / Client / Token. The ⌘1…⌘7 shortcut order follows
/// the visual order because both derive from `SidebarItem.allCases`.
@MainActor
final class NavigationStructureTests: XCTestCase {

    func testSidebarItemsFollowTheVisualOrder() {
        XCTAssertEqual(
            SidebarItem.allCases.map(\.rawValue),
            ["Home", "Clients", "Servers", "Tools", "Review Queue", "Secrets", "Activity"]
        )
    }

    func testSidebarLayoutGroupsMatchTheWebUI() {
        let layout = MainWindow.sidebarLayout
        XCTAssertEqual(layout.map(\.title), [nil, "Connect", "Protect", "Monitor"])
        XCTAssertEqual(layout[0].items, [.home])
        XCTAssertEqual(layout[1].items, [.clients, .servers, .tools])
        XCTAssertEqual(layout[2].items, [.review, .secrets])
        XCTAssertEqual(layout[3].items, [.activity])
        // The shortcut order (allCases) is the visual order.
        XCTAssertEqual(layout.flatMap(\.items), SidebarItem.allCases)
    }

    func testSidebarSectionTitles() {
        XCTAssertEqual(SidebarSection.allCases.map(\.title), ["Connect", "Protect", "Monitor"])
    }

    func testRetiredNamesAreGone() {
        let names = SidebarItem.allCases.map(\.rawValue)
        for gone in ["Dashboard", "Registries", "Agent Tokens", "Activity Log", "Sessions", "Security"] {
            XCTAssertFalse(names.contains(gone), "\(gone) must not be a sidebar item")
        }
    }

    func testAddMenuItemsAndDestinations() {
        XCTAssertEqual(AddMenuItem.allCases.map(\.rawValue), ["Server", "Client", "Token"])
        XCTAssertEqual(AddMenuItem.server.destination, .servers)
        XCTAssertEqual(AddMenuItem.client.destination, .clients)
        XCTAssertEqual(AddMenuItem.token.destination, .clients)
    }

    func testPendingAddActionIsConsumedOnlyByMatchingKinds() {
        let appState = AppState()
        appState.pendingAddAction = .server
        XCTAssertNil(appState.consumePendingAddAction(for: [.client, .token]))
        XCTAssertEqual(appState.pendingAddAction, .server, "a Clients view must not eat a Server action")
        XCTAssertEqual(appState.consumePendingAddAction(for: [.server]), .server)
        XCTAssertNil(appState.pendingAddAction)

        appState.pendingAddAction = .token
        XCTAssertEqual(appState.consumePendingAddAction(for: [.client, .token]), .token)
        XCTAssertNil(appState.consumePendingAddAction(for: [.client, .token]))
    }

    /// Source guard (the idiom ReviewQueueMenuTests uses): the window carries
    /// the toolbar "+" menu and the badges the contract names.
    func testMainWindowCarriesTheToolbarAddMenuAndBadges() throws {
        let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
        let window = try String(contentsOf: root.appendingPathComponent("MCPProxy/Views/MainWindow.swift"))
        XCTAssertTrue(window.contains(".toolbar"))
        XCTAssertTrue(window.contains("accessibilityIdentifier(\"toolbar-add-menu\")"))
        XCTAssertTrue(window.contains("appState.attention.count"), "Home badge is the attention count")
        XCTAssertTrue(window.contains("appState.reviewQueueCount"), "Review Queue badge")
    }
}
