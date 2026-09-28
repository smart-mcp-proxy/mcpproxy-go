// CrossSurfaceRenderedLabelParityTests.swift
// MCPProxy
//
// Review round 2 (109-e medium finding): `TrayPrimaryItemTests
// .testRowAndSubmenuPrimaryLabelsAreIdentical` compares
// `TrayPrimaryPresentation.primaryItem(for:).label` against
// `ServerRowPresentation.primaryAction(for:).label` — but the row's
// implementation IS that exact same call (`ServerRowPresentation
// .primaryAction` is a one-line passthrough to `TrayPrimaryPresentation
// .primaryItem`), so the two sides of that assertion are always the same
// string read twice. It cannot catch a rendered-string divergence like the
// tail row's "View logs" vs "View Logs" (see TraySecondaryActionTests /
// the round 2 macOS finding) if one crept into the PRIMARY item instead —
// and only 2 of the 8 `HealthStatus.actionLabels` values had their actual
// rendered menu-item title asserted anywhere (TrayAuditMenuTests' Review and
// Sign-in fixtures).
//
// This file builds the REAL tray submenu (`AppController.rebuildMenu`) and
// the REAL Servers-row actions cell (`ServerTableView.Coordinator
// .tableView(_:viewFor:row:)`) independently, for every labeled action, and
// compares the actual rendered strings each surface produced — not the pure
// function both happen to call.
import XCTest
import AppKit
@testable import MCPProxy

@MainActor
final class CrossSurfaceRenderedLabelParityTests: XCTestCase {

    private final class TestMenuHost: TrayMenuHost {
        var menu: NSMenu?
    }

    private func renderedTraySubmenuTitles(for server: ServerStatus) throws -> [String] {
        let host = TestMenuHost()
        let controller = AppController(glanceDataSource: CountingGlanceDataSource(), menuHost: host)
        controller.appState.coreState = .connected
        controller.appState.servers = [server]
        controller.rebuildMenu()

        let serversParent = try XCTUnwrap(
            (host.menu?.items ?? []).first { $0.title.hasPrefix("Servers (") })
        let submenu = try XCTUnwrap(serversParent.submenu)
        let row = try XCTUnwrap(submenu.items.first { $0.title == server.name },
                                "no row for \(server.name): \(submenu.items.map(\.title))")
        return try XCTUnwrap(row.submenu).items.map(\.title)
    }

    /// The row's REAL primary icon button, built by the same cell factory
    /// `tableView(_:viewFor:row:)` uses in production — its accessible
    /// tooltip is `primary.label`, set at the exact call site this test
    /// exercises (`ServersView.swift`'s `makeActionsCell`).
    private func renderedRowPrimaryButtonToolTip(for server: ServerStatus) -> String? {
        let coordinator = ServerTableView.Coordinator()
        coordinator.servers = [server]
        let tableView = NSTableView()
        let column = NSTableColumn(identifier: ServerColumn.actions.identifier)
        guard let cell = coordinator.tableView(tableView, viewFor: column, row: 0) else { return nil }
        let stack = cell.subviews.first { $0 is NSStackView } as? NSStackView
        let primaryButton = stack?.arrangedSubviews
            .compactMap { $0 as? NSButton }
            .first { $0.contentTintColor == .controlAccentColor }
        return primaryButton?.toolTip
    }

    /// Every action `HealthStatus.actionLabels` carries a label for — not
    /// just the 2 (Review, Sign in) the existing tray suite happened to
    /// exercise with a real rendered menu.
    func testEveryLabeledActionRendersTheIdenticalPrimaryStringOnBothRealSurfaces() throws {
        for (action, expectedLabel) in HealthStatus.actionLabels {
            let server = Self.server(health: ("healthy", "", action))

            let trayTitles = try renderedTraySubmenuTitles(for: server)
            XCTAssertTrue(trayTitles.contains(expectedLabel),
                          "\(action): tray submenu did not render '\(expectedLabel)': \(trayTitles)")

            let rowToolTip = renderedRowPrimaryButtonToolTip(for: server)
            XCTAssertEqual(rowToolTip, expectedLabel,
                           "\(action): the row's real primary button tooltip diverged from the tray's rendered label")
        }
    }

    // MARK: - Helpers

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
