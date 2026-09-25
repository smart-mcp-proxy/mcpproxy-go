// TrayPrimaryItemTests.swift
// MCPProxyTests
//
// Spec 109 FR-014 (PR 109-e, T069a): `TrayPrimaryPresentation.primaryItem(for:)`
// is the ONE pure function both the macOS Servers row and the tray server
// submenu read for `actions[0]` — table-tested over every value here so the
// two surfaces cannot drift, and so the mapping can never regress into a
// missing item or a direct one-click approve.

import XCTest
@testable import MCPProxy

final class TrayPrimaryItemTests: XCTestCase {

    // MARK: - Executed in place

    func testLoginRestartEnableExecuteInPlace() {
        let login = try! XCTUnwrap(TrayPrimaryPresentation.primaryItem(for: "login"))
        XCTAssertEqual(login.label, "Sign in")
        XCTAssertEqual(login.kind, .execute(.login))

        let restart = try! XCTUnwrap(TrayPrimaryPresentation.primaryItem(for: "restart"))
        XCTAssertEqual(restart.label, "Restart")
        XCTAssertEqual(restart.kind, .execute(.restart))

        let enable = try! XCTUnwrap(TrayPrimaryPresentation.primaryItem(for: "enable"))
        XCTAssertEqual(enable.label, "Enable")
        XCTAssertEqual(enable.kind, .execute(.enable))
    }

    // MARK: - Opens the screen that performs it

    func testApproveOpensReviewNeverExecutesDirectly() {
        let item = try! XCTUnwrap(TrayPrimaryPresentation.primaryItem(for: "approve"))
        XCTAssertEqual(item.label, "Review")
        XCTAssertEqual(item.kind, .open(.review))
        // FR-005: never a one-click approve.
        XCTAssertNotEqual(item.kind, .execute(.approve))
    }

    func testSetSecretOpensConfig() {
        let item = try! XCTUnwrap(TrayPrimaryPresentation.primaryItem(for: "set_secret"))
        XCTAssertEqual(item.label, "Add secret")
        XCTAssertEqual(item.kind, .open(.config))
    }

    func testConfigureOpensConfig() {
        let item = try! XCTUnwrap(TrayPrimaryPresentation.primaryItem(for: "configure"))
        XCTAssertEqual(item.label, "Fix config")
        XCTAssertEqual(item.kind, .open(.config))
    }

    func testEditURLOpensConfig() {
        let item = try! XCTUnwrap(TrayPrimaryPresentation.primaryItem(for: "edit_url"))
        XCTAssertEqual(item.label, "Edit URL")
        XCTAssertEqual(item.kind, .open(.config))
    }

    func testViewLogsOpensLogs() {
        let item = try! XCTUnwrap(TrayPrimaryPresentation.primaryItem(for: "view_logs"))
        XCTAssertEqual(item.label, "View logs")
        XCTAssertEqual(item.kind, .open(.logs))
    }

    // MARK: - No value yields a missing item or a direct approve

    func testEveryLabeledActionResolvesToAnItem() {
        for (action, label) in HealthStatus.actionLabels {
            let item = TrayPrimaryPresentation.primaryItem(for: action)
            XCTAssertNotNil(item, "\(action) has a label but no primary item — FR-014 forbids a missing item")
            XCTAssertEqual(item?.label, label, "\(action): label must come from the shared table")
            XCTAssertNotEqual(item?.kind, .execute(.approve), "\(action): approve must never execute in place")
        }
    }

    func testEmptyOrUnknownActionYieldsNoItem() {
        XCTAssertNil(TrayPrimaryPresentation.primaryItem(for: ""))
        XCTAssertNil(TrayPrimaryPresentation.primaryItem(for: nil))
        XCTAssertNil(TrayPrimaryPresentation.primaryItem(for: "some_future_action"))
    }

    // MARK: - The Servers row and the tray submenu say the same thing

    func testRowAndSubmenuPrimaryLabelsAreIdentical() {
        for action in HealthStatus.actionLabels.keys {
            let server = Self.server(health: ("healthy", "", action))
            let trayLabel = TrayPrimaryPresentation.primaryItem(for: server)?.label
            let rowLabel = ServerRowPresentation.primaryAction(for: server)?.label
            XCTAssertEqual(trayLabel, rowLabel, "\(action): tray and row must show the identical primary label")
        }
    }

    // MARK: - Old-core / quarantine fallback

    func testQuarantinedServerWithNoHealthFallsBackToApprove() {
        let server = Self.server(quarantined: true, health: nil)
        let item = TrayPrimaryPresentation.primaryItem(for: server)
        XCTAssertEqual(item?.kind, .open(.review))
    }

    func testHealthActionTakesPrecedenceOverQuarantineFallback() {
        // A quarantined server that ALSO needs sign-in shows Sign in first
        // (actions priority order), not Review — matches health.ActionPriority.
        let server = Self.server(quarantined: true, health: ("degraded", "", "login"))
        let item = TrayPrimaryPresentation.primaryItem(for: server)
        XCTAssertEqual(item?.kind, .execute(.login))
    }

    func testNonQuarantinedServerWithNoHealthHasNoPrimaryItem() {
        let server = Self.server(quarantined: false, health: nil)
        XCTAssertNil(TrayPrimaryPresentation.primaryItem(for: server))
    }

    // MARK: - Helpers

    /// Same JSON-decode fixture pattern as TrayAuditMenuTests, so a future
    /// `ServerStatus` field addition cannot silently break either file.
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
