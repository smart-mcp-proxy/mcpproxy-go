// TraySecondaryActionTests.swift
// MCPProxyTests
//
// Review round 2 (109-e medium finding): the tray submenu and the Servers
// row each carry three always-present tail rows — Enable/Disable, Restart,
// View Logs — laid out UNDER the Spec 109 FR-014 primary item without ever
// checking whether the primary already performs one of them. A disabled
// server (primary = Enable), a restart-needing server (primary = Restart)
// and a token-refresh-pending server (primary = View logs) each ended up
// showing the identical command twice in the same menu/row.
//
// `TraySecondaryPresentation.items(for:)` is the one pure function both
// surfaces now read to decide which of the three tail rows to render — these
// tests pin its decisions directly, without a live NSMenu or NSTableView.

import XCTest
@testable import MCPProxy

final class TraySecondaryActionTests: XCTestCase {

    // MARK: - No primary action: everything applicable shows

    func testHealthyEnabledServerOffersDisableAndRestartAndViewLogs() {
        let server = Self.server(enabled: true, health: nil)
        let items = TraySecondaryPresentation.items(for: server)
        XCTAssertEqual(items, [.toggleEnabled(enable: false), .restart, .viewLogs])
    }

    func testDisabledServerWithNoPrimaryOffersEnableAndRestartAndViewLogs() {
        // A disabled server the health calculator has not flagged with an
        // "enable" action (e.g. old core, or a future health vocabulary
        // change) must still be enable-able from the tail.
        let server = Self.server(enabled: false, health: nil)
        let items = TraySecondaryPresentation.items(for: server)
        XCTAssertEqual(items, [.toggleEnabled(enable: true), .restart, .viewLogs])
    }

    // MARK: - The duplicate this round fixes

    func testEnablePrimaryDropsTheEchoedEnableTailRow() {
        let server = Self.server(enabled: false, health: ("degraded", "Disabled", "enable"))
        let items = TraySecondaryPresentation.items(for: server)
        XCTAssertFalse(items.contains(.toggleEnabled(enable: true)),
                       "Enable must not appear twice: once as the primary, once in the tail")
        XCTAssertEqual(items, [.restart, .viewLogs], "Restart and View Logs are untouched by an Enable primary")
    }

    func testRestartPrimaryDropsTheEchoedRestartTailRow() {
        let server = Self.server(enabled: true, health: ("unhealthy", "failed to connect", "restart"))
        let items = TraySecondaryPresentation.items(for: server)
        XCTAssertFalse(items.contains(.restart),
                       "Restart must not appear twice: once as the primary, once in the tail")
        XCTAssertEqual(items, [.toggleEnabled(enable: false), .viewLogs])
    }

    func testViewLogsPrimaryDropsTheEchoedViewLogsTailRow() {
        let server = Self.server(enabled: true, health: ("degraded", "Token refresh pending", "view_logs"))
        let items = TraySecondaryPresentation.items(for: server)
        XCTAssertFalse(items.contains(.viewLogs),
                       "View Logs must not appear twice: once as the primary, once in the tail")
        XCTAssertEqual(items, [.toggleEnabled(enable: false), .restart])
    }

    // MARK: - A primary that is neither Enable, Restart nor View Logs changes nothing

    func testLoginPrimaryLeavesAllThreeTailRowsInPlace() {
        let server = Self.server(enabled: true, health: ("degraded", "Sign-in required", "login"))
        let items = TraySecondaryPresentation.items(for: server)
        XCTAssertEqual(items, [.toggleEnabled(enable: false), .restart, .viewLogs])
    }

    func testApprovePrimaryLeavesAllThreeTailRowsInPlace() {
        let server = Self.server(enabled: true, quarantined: true,
                                 health: ("healthy", "Quarantined for review", "approve"))
        let items = TraySecondaryPresentation.items(for: server)
        XCTAssertEqual(items, [.toggleEnabled(enable: false), .restart, .viewLogs])
    }

    // MARK: - Disable is never the primary's echo

    func testEnabledServerAlwaysOffersDisableRegardlessOfPrimary() {
        for action in ["login", "restart", "approve", "set_secret", "configure", "edit_url", "view_logs"] {
            let server = Self.server(enabled: true, health: ("degraded", "", action))
            XCTAssertTrue(TraySecondaryPresentation.items(for: server).contains(.toggleEnabled(enable: false)),
                         "\(action): Disable is never produced as a primary action, so it must never be suppressed")
        }
    }

    // MARK: - Helpers

    /// Same JSON-decode fixture pattern as TrayPrimaryItemTests, so a future
    /// `ServerStatus` field addition cannot silently break either file.
    private static func server(enabled: Bool,
                                quarantined: Bool = false,
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
            "id": "srv", "name": "srv", "protocol": "http", "enabled": \(enabled),
            "connected": \(health == nil && enabled), "quarantined": \(quarantined), "tool_count": 1
            \(healthJSON)
        }
        """.data(using: .utf8)!
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(ServerStatus.self, from: json)
    }
}
