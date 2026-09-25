// ServerRowDispatchTests.swift
// MCPProxy
//
// Review round 2 (109-e medium finding): every existing pin for "a
// quarantined row never approves directly" (ServerRowActionTests,
// TrayPrimaryItemTests) asserts only the PURE DATA the row and tray submenu
// are built from (`ServerRowPresentation.contextMenuActions`,
// `.primaryAction`) — never the real `@objc` handlers `ServersView.swift`
// actually wires a click to. A future rewiring of `ctxOpenReview` or
// `primaryActionClicked`'s `.open(.review)` case to call
// `apiClient.approveTools(id)` directly (that method already exists and is
// already called elsewhere, e.g. DashboardView.swift) would pass every test
// in this package unchanged.
//
// These tests build the REAL `ServerTableView.Coordinator`, run its REAL
// menu-construction (`menuNeedsUpdate`) and button handler
// (`primaryActionClicked`), and dispatch through `NSApplication.sendAction`
// exactly as a real click would — the same technique `GlanceRowRoutingTests`
// uses for the tray's glance rows — so a handler rewired to approve directly
// fails here even though the pure-function tests elsewhere stay green.

import XCTest
import AppKit
@testable import MCPProxy

@MainActor
final class ServerRowDispatchTests: XCTestCase {

    /// `NSTableView.clickedRow` is a read-only AppKit property normally set
    /// by a real mouse event; overriding it in a tiny subclass is the
    /// standard way to drive `menuNeedsUpdate(_:)` — which reads it — without
    /// a live click.
    private final class FakeClickTableView: NSTableView {
        var fakeClickedRow: Int = -1
        override var clickedRow: Int { fakeClickedRow }
    }

    private func makeCoordinator(servers: [ServerStatus]) -> (ServerTableView.Coordinator, FakeClickTableView) {
        let coordinator = ServerTableView.Coordinator()
        coordinator.servers = servers
        let tableView = FakeClickTableView()
        coordinator.tableView = tableView
        return (coordinator, tableView)
    }

    // MARK: - Context menu (`ctxOpenReview`)

    /// The row's REAL right-click menu for a quarantined server, dispatched
    /// through the REAL `ctxOpenReview` handler `menuNeedsUpdate` wires it to.
    func testQuarantinedRowContextMenuReviewClickOpensToolsTabNeverApproves() throws {
        let server = Self.server(quarantined: true, health: ("healthy", "Quarantined for review", "approve"))
        let (coordinator, tableView) = makeCoordinator(servers: [server])
        tableView.fakeClickedRow = 0

        var opened: (ServerStatus, ServerDetailTab)?
        coordinator.onOpenDetail = { opened = ($0, $1) }

        let menu = NSMenu()
        coordinator.menuNeedsUpdate(menu)
        let review = try XCTUnwrap(menu.items.first { $0.title == "Review" },
                                   "expected a Review row: \(menu.items.map(\.title))")
        XCTAssertNotNil(review.action)
        XCTAssertTrue(review.target === coordinator)

        let sent = NSApplication.shared.sendAction(review.action!, to: review.target, from: review)
        XCTAssertTrue(sent, "the Review row did not dispatch")
        XCTAssertEqual(opened?.0.name, server.name)
        XCTAssertEqual(opened?.1, .tools, "review must open the Tools tab, never approve directly")
    }

    /// FR-010: a server that is BOTH quarantined AND needs sign-in offers
    /// both rows in the SAME real menu — mirrors
    /// `TrayAuditMenuTests.testAQuarantinedServerThatAlsoNeedsLoginOffersBothSignInAndReview`,
    /// which pins only the tray side of this fixture.
    func testQuarantinedAndLoginRowContextMenuOffersBothRealRows() {
        let server = Self.server(quarantined: true, health: ("degraded", "Sign-in required", "login"))
        let (coordinator, tableView) = makeCoordinator(servers: [server])
        tableView.fakeClickedRow = 0

        let menu = NSMenu()
        coordinator.menuNeedsUpdate(menu)
        let titles = menu.items.map(\.title)
        XCTAssertTrue(titles.contains("Sign in"), "\(titles)")
        XCTAssertTrue(titles.contains("Review"), "\(titles)")
    }

    // MARK: - Primary icon button (`primaryActionClicked`)

    /// The row's REAL primary-button handler for a quarantined server (primary
    /// = Review) must open the Tools tab, never call approve/unquarantine.
    func testQuarantinedRowPrimaryButtonClickOpensToolsTabNeverApproves() {
        let server = Self.server(quarantined: true, health: ("healthy", "Quarantined for review", "approve"))
        let (coordinator, _) = makeCoordinator(servers: [server])

        var opened: (ServerStatus, ServerDetailTab)?
        coordinator.onOpenDetail = { opened = ($0, $1) }

        let button = NSButton()
        button.tag = 0
        coordinator.primaryActionClicked(button)

        XCTAssertEqual(opened?.0.name, server.name)
        XCTAssertEqual(opened?.1, .tools, "the primary button must open Tools, never approve directly")
    }

    /// The row's REAL primary-button handler for an in-place action (login)
    /// never opens a screen — it must not silently do nothing either.
    func testLoginPrimaryButtonClickNeverOpensAScreen() {
        let server = Self.server(quarantined: false, health: ("degraded", "Sign-in required", "login"))
        let (coordinator, _) = makeCoordinator(servers: [server])

        var openedCount = 0
        coordinator.onOpenDetail = { _, _ in openedCount += 1 }

        let button = NSButton()
        button.tag = 0
        coordinator.primaryActionClicked(button)

        XCTAssertEqual(openedCount, 0, "login executes in place; it must not navigate")
    }

    // MARK: - Helpers

    /// Same JSON-decode fixture pattern as TrayPrimaryItemTests/ServerRowActionTests.
    private static func server(quarantined: Bool = false,
                                health: (level: String, summary: String, action: String)?) -> ServerStatus {
        var healthJSON = ""
        if let health {
            healthJSON = """
            , "health": {"level": "\(health.level)", "admin_state": "enabled",
                          "summary": "\(health.summary)", "action": "\(health.action)"}
            """
        }
        let json = """
        {
            "id": "srv", "name": "srv", "protocol": "http", "enabled": true,
            "connected": \(health == nil), "quarantined": \(quarantined), "tool_count": 1
            \(healthJSON)
        }
        """.data(using: .utf8)!
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(ServerStatus.self, from: json)
    }
}
