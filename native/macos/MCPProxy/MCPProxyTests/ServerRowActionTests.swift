// ServerRowActionTests.swift
// MCPProxyTests
//
// Spec 109 FR-013/FR-014 (PR 109-e, T069): the Servers-table row's primary
// action and context-menu items, per fixture. Pins that no row or
// context-menu action calls `approveTools`/`unquarantine` directly — a
// quarantined row's primary action (and its context-menu review item) OPEN
// the review location instead, so this PR cannot reintroduce the pre-X2
// one-click approve path whatever its merge order with 109-f (which deletes
// the Web/tray copies of the same call).

import XCTest
@testable import MCPProxy

final class ServerRowActionTests: XCTestCase {

    // MARK: - Primary action

    func testPrimaryActionMirrorsTrayPresentation() {
        for action in ["login", "restart", "enable", "approve", "set_secret", "configure", "edit_url", "view_logs"] {
            let server = Self.server(health: ("healthy", "", action))
            XCTAssertEqual(
                ServerRowPresentation.primaryAction(for: server),
                TrayPrimaryPresentation.primaryItem(for: server),
                "\(action): the row must make the identical decision as the tray")
        }
    }

    func testReadyServerHasNoPrimaryAction() {
        let server = Self.server(health: nil)
        XCTAssertNil(ServerRowPresentation.primaryAction(for: server))
    }

    func testQuarantinedRowPrimaryActionOpensReviewNeverApproves() {
        let server = Self.server(quarantined: true, health: ("healthy", "Quarantined for review", "approve"))
        let primary = ServerRowPresentation.primaryAction(for: server)
        XCTAssertEqual(primary?.kind, .open(.review))
        XCTAssertNotEqual(primary?.kind, .execute(.approve))
    }

    // MARK: - Context menu

    func testContextMenuAlwaysOffersToggleAndRestart() {
        let server = Self.server(health: nil)
        let actions = ServerRowPresentation.contextMenuActions(for: server)
        XCTAssertEqual(actions.first, .toggleEnabled(enable: false), "enabled server offers Disable, not Enable")
        XCTAssertTrue(actions.contains(.restart))
        XCTAssertTrue(actions.contains(.viewDetails))
        XCTAssertTrue(actions.contains(.viewLogs))
        XCTAssertTrue(actions.contains(.delete))
    }

    func testDisabledServerOffersEnableNotDisable() {
        let server = Self.server(enabled: false, health: nil)
        let actions = ServerRowPresentation.contextMenuActions(for: server)
        XCTAssertEqual(actions.first, .toggleEnabled(enable: true))
    }

    func testSignInOnlyWhenOAuthLoginRequired() {
        let plain = Self.server(health: nil)
        XCTAssertFalse(ServerRowPresentation.contextMenuActions(for: plain).contains(.signIn))

        let needsAuth = Self.server(health: ("degraded", "", "login"))
        XCTAssertTrue(ServerRowPresentation.contextMenuActions(for: needsAuth).contains(.signIn))
    }

    // The pin: a quarantined server's context menu carries `.openReview` —
    // there is no case in `ServerRowMenuAction` that means "call approveTools
    // directly", so this is a structural guarantee, not just a behavioral one.
    func testQuarantinedServerContextMenuOffersOpenReviewOnly() {
        let server = Self.server(quarantined: true, health: ("healthy", "Quarantined for review", "approve"))
        let actions = ServerRowPresentation.contextMenuActions(for: server)
        XCTAssertTrue(actions.contains(.openReview))
        // Every case in the enum, enumerated here so a future case addition
        // must update this list — the point is there is no "approve" case.
        for action in actions {
            switch action {
            case .toggleEnabled, .restart, .signIn, .openReview, .viewDetails, .viewLogs, .delete:
                continue
            }
        }
    }

    func testToolLevelQuarantineAlsoOffersOpenReview() {
        // Server-level trusted, but tools pending approval (Spec 032).
        let server = Self.server(quarantined: false, pendingApprovalCount: 2, health: nil)
        XCTAssertTrue(ServerRowPresentation.contextMenuActions(for: server).contains(.openReview))
    }

    func testHealthyTrustedServerHasNoReviewItem() {
        let server = Self.server(health: nil)
        XCTAssertFalse(ServerRowPresentation.contextMenuActions(for: server).contains(.openReview))
    }

    /// Review round 2 (109-e medium finding): TrayAuditMenuTests pins this
    /// exact fixture (FR-010's `actions = ["login", "approve"]`) on the tray
    /// submenu only. The Servers-row context menu makes the identical
    /// independent-gating decision (`.signIn` on `isOAuthLoginRequired`,
    /// `.openReview` on `quarantined`, neither conditioned on the other) but
    /// had no fixture of its own — a regression that coupled the two here
    /// would have shipped with the whole suite green.
    func testQuarantinedRowThatAlsoNeedsLoginOffersBothSignInAndReview() {
        let server = Self.server(quarantined: true, health: ("degraded", "Sign-in required", "login"))
        let actions = ServerRowPresentation.contextMenuActions(for: server)
        XCTAssertTrue(actions.contains(.signIn), "the primary is Sign in, but Review must not be dropped")
        XCTAssertTrue(actions.contains(.openReview), "a quarantined server always needs a path to review")
    }

    // MARK: - Helpers

    private static func server(enabled: Bool = true,
                                quarantined: Bool = false,
                                pendingApprovalCount: Int = 0,
                                health: (level: String, summary: String, action: String)?) -> ServerStatus {
        var healthJSON = ""
        if let health {
            healthJSON = """
            , "health": {"level": "\(health.level)", "admin_state": "enabled",
                          "summary": "\(health.summary)", "action": "\(health.action)"}
            """
        }
        var quarantineJSON = ""
        if pendingApprovalCount > 0 {
            quarantineJSON = """
            , "quarantine": {"pending_count": \(pendingApprovalCount), "changed_count": 0}
            """
        }
        let json = """
        {
            "id": "srv", "name": "srv", "protocol": "http", "enabled": \(enabled),
            "connected": \(health == nil && enabled), "quarantined": \(quarantined), "tool_count": 1
            \(healthJSON)\(quarantineJSON)
        }
        """.data(using: .utf8)!
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(ServerStatus.self, from: json)
    }
}
