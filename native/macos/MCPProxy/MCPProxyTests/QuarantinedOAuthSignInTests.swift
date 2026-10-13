// QuarantinedOAuthSignInTests.swift
// MCPProxyTests
//
// A remote OAuth server imported with quarantine on (GitHub MCP at
// api.githubcopilot.com) reports `health.action == "approve"` — quarantine
// review outranks everything in the core's health calculator — while its
// diagnostic says MCPX_OAUTH_LOGIN_REQUIRED. The tray keyed sign-in on
// `health.action == "login"` alone, so it offered "Review quarantine…" and no
// way to sign in. The Web UI's `oauthSignInState` already reads the
// diagnostic code; these pin the tray to the same rule.

import XCTest
@testable import MCPProxy

final class QuarantinedOAuthSignInTests: XCTestCase {

    // MARK: - Fixtures

    /// The live shape of a quarantined GitHub MCP server awaiting sign-in.
    private static func quarantinedAwaitingSignIn(
        code: String = "MCPX_OAUTH_LOGIN_REQUIRED",
        level: String = "degraded",
        summary: String = "Quarantined — Sign-in required"
    ) -> ServerStatus {
        decode("""
        {
            "id": "github", "name": "github", "protocol": "http",
            "enabled": true, "connected": false, "quarantined": true, "tool_count": 0,
            "error_code": "\(code)",
            "diagnostic": {"code": "\(code)", "severity": "error", "summary": "sign in"},
            "health": {"level": "\(level)", "admin_state": "quarantined",
                       "summary": "\(summary)", "action": "approve"}
        }
        """)
    }

    /// A quarantined server whose connect failed for a non-OAuth reason.
    private static func quarantinedTransportFault() -> ServerStatus {
        decode("""
        {
            "id": "broken", "name": "broken", "protocol": "stdio",
            "enabled": true, "connected": false, "quarantined": true, "tool_count": 0,
            "error_code": "MCPX_STDIO_SPAWN_ENOENT",
            "diagnostic": {"code": "MCPX_STDIO_SPAWN_ENOENT", "severity": "error", "summary": "no binary"},
            "health": {"level": "unhealthy", "admin_state": "quarantined",
                       "summary": "Quarantined — command not found", "action": "approve"}
        }
        """)
    }

    /// A DISABLED server can keep the diagnostic from its last connect attempt.
    /// Its next step is Enable, not Sign in — the Web UI server card skips it too.
    private static func disabledWithStaleSignInDiagnostic() -> ServerStatus {
        decode("""
        {
            "id": "old", "name": "old", "protocol": "http",
            "enabled": false, "connected": false, "quarantined": false, "tool_count": 0,
            "error_code": "MCPX_OAUTH_LOGIN_REQUIRED",
            "diagnostic": {"code": "MCPX_OAUTH_LOGIN_REQUIRED", "severity": "error", "summary": "sign in"},
            "health": {"level": "healthy", "admin_state": "disabled",
                       "summary": "Disabled", "action": "enable"}
        }
        """)
    }

    private static func loginActionServer() -> ServerStatus {
        decode("""
        {
            "id": "notion", "name": "notion", "protocol": "http",
            "enabled": true, "connected": false, "quarantined": false, "tool_count": 0,
            "health": {"level": "degraded", "admin_state": "enabled",
                       "summary": "Sign-in required", "action": "login"}
        }
        """)
    }

    /// Verbatim `GET /api/v1/servers` entry from a scratch core on main
    /// (638fa805a) with `quarantine_enabled: true` and the GitHub MCP server
    /// imported quarantined. Note the real severity is "warn", and the health
    /// block is still healthy/approve: nothing in it mentions sign-in.
    private static func liveQuarantinedGitHub() -> ServerStatus {
        decode("""
        {
          "id": "github",
          "name": "github",
          "url": "https://api.githubcopilot.com/mcp/",
          "protocol": "http",
          "oauth": {
            "auth_url": "",
            "token_url": "",
            "client_id": ""
          },
          "enabled": true,
          "quarantined": true,
          "connected": false,
          "connecting": false,
          "status": "pending auth",
          "last_error": "failed to connect: OAuth authentication required for github: login available via Web UI, system tray menu, or 'mcpproxy auth login' CLI command",
          "reconnect_count": 0,
          "tool_count": 0,
          "authenticated": false,
          "health": {
            "level": "healthy",
            "admin_state": "quarantined",
            "summary": "Quarantined for review",
            "action": "approve"
          },
          "diagnostic": {
            "code": "MCPX_OAUTH_LOGIN_REQUIRED",
            "severity": "warn",
            "cause": "failed to connect: OAuth authentication required for github: login available via Web UI, system tray menu, or 'mcpproxy auth login' CLI command",
            "detected_at": "2026-09-25T16:33:11.461724+03:00",
            "user_message": "This server needs you to sign in before it can connect.",
            "fix_steps": [
              {
                "type": "button",
                "label": "Sign in",
                "fixer_key": "oauth_reauth"
              },
              {
                "type": "link",
                "label": "How OAuth sign-in works",
                "url": "https://docs.mcpproxy.app/errors/MCPX_OAUTH_LOGIN_REQUIRED"
              }
            ],
            "docs_url": "https://docs.mcpproxy.app/errors/MCPX_OAUTH_LOGIN_REQUIRED"
          },
          "error_code": "MCPX_OAUTH_LOGIN_REQUIRED"
        }
        """)
    }

    private static func decode(_ json: String) -> ServerStatus {
        // swiftlint:disable:next force_try
        try! JSONDecoder().decode(ServerStatus.self, from: json.data(using: .utf8)!)
    }

    // MARK: - Sign-in detection

    func testQuarantinedServerAwaitingSignInIsRecognised() {
        let server = Self.quarantinedAwaitingSignIn()
        XCTAssertTrue(server.isOAuthLoginRequired,
                      "MCPX_OAUTH_LOGIN_REQUIRED must mean sign-in even when health.action is approve")
        XCTAssertTrue(server.isQuarantineReview, "the quarantine review must not be lost")
    }

    /// Same payload as a pre-fix core sends it (level healthy, generic summary).
    func testRecognisedWhateverTheHealthLevelSays() {
        let server = Self.quarantinedAwaitingSignIn(level: "healthy", summary: "Quarantined for review")
        XCTAssertTrue(server.isOAuthLoginRequired)
    }

    func testReauthCodesAreSignInToo() {
        for code in ["MCPX_OAUTH_REAUTH_REQUIRED", "MCPX_OAUTH_REFRESH_EXPIRED", "MCPX_OAUTH_REFRESH_403"] {
            XCTAssertTrue(Self.quarantinedAwaitingSignIn(code: code, level: "unhealthy").isOAuthLoginRequired,
                          code)
        }
    }

    /// Only the sign-in codes: a discovery failure or callback mismatch is not
    /// fixed by clicking "Sign in" again, and `oauthSignInState` excludes them.
    func testOtherOAuthFaultsAreNotSignIn() {
        for code in ["MCPX_OAUTH_DISCOVERY_FAILED", "MCPX_OAUTH_CALLBACK_MISMATCH", "MCPX_OAUTH_CALLBACK_TIMEOUT"] {
            XCTAssertFalse(Self.quarantinedAwaitingSignIn(code: code).isOAuthLoginRequired, code)
        }
    }

    func testTransportFaultIsNotSignIn() {
        XCTAssertFalse(Self.quarantinedTransportFault().isOAuthLoginRequired)
    }

    func testDisabledServerWithStaleDiagnosticIsNotSignIn() {
        XCTAssertFalse(Self.disabledWithStaleSignInDiagnostic().isOAuthLoginRequired)
    }

    func testLoginActionStillCounts() {
        XCTAssertTrue(Self.loginActionServer().isOAuthLoginRequired)
    }

    func testLivePayloadIsRecognised() {
        let server = Self.liveQuarantinedGitHub()
        XCTAssertTrue(server.isOAuthLoginRequired)
        XCTAssertTrue(server.isQuarantineReview)
        XCTAssertEqual(TrayServerAction.leadingMenuActions(for: server), [.login, .approve])
        XCTAssertEqual(TrayServerAction.forAttention(server), .login)
    }

    // MARK: - Calm presentation

    func testAwaitingSignInGetsTheCalmTintNotRed() {
        let server = Self.quarantinedAwaitingSignIn(level: "unhealthy")
        XCTAssertEqual(server.menuStatusNSColor, .controlAccentColor)
    }

    // MARK: - Menu actions

    /// Sign in is offered, and Review quarantine stays beside it.
    func testServerSubmenuOffersSignInBesideReview() {
        XCTAssertEqual(TrayServerAction.leadingMenuActions(for: Self.quarantinedAwaitingSignIn()),
                       [.login, .approve])
    }

    func testPlainQuarantineOffersOnlyReview() {
        XCTAssertEqual(TrayServerAction.leadingMenuActions(for: Self.quarantinedTransportFault()),
                       [.approve])
    }

    func testLoginOnlyServerOffersOnlySignIn() {
        XCTAssertEqual(TrayServerAction.leadingMenuActions(for: Self.loginActionServer()), [.login])
    }

    // MARK: - Dashboard "Servers Needing Attention" card

    /// The card keyed its one button on `health.action`, so this server got a
    /// lone "Approve" and no way to sign in.
    func testDashboardCardOffersSignInBesideApprove() {
        XCTAssertEqual(Self.quarantinedAwaitingSignIn().attentionActions, [.login, .approve])
        XCTAssertEqual(Self.liveQuarantinedGitHub().attentionActions, [.login, .approve])
    }

    func testDashboardCardDoesNotDuplicateSignIn() {
        XCTAssertEqual(Self.loginActionServer().attentionActions, [.login])
    }

    func testDashboardCardKeepsPlainQuarantineAsIs() {
        XCTAssertEqual(Self.quarantinedTransportFault().attentionActions, [.approve])
    }

    /// The Needs Attention row dispatches Sign in, not nothing.
    func testNeedsAttentionRowSignsIn() {
        XCTAssertEqual(TrayServerAction.forAttention(Self.quarantinedAwaitingSignIn()), .login)
    }

    /// Approval stays a human decision reached through Server Detail.
    func testNeedsAttentionRowNeverApproves() {
        XCTAssertNil(TrayServerAction.forAttention(Self.quarantinedTransportFault()))
    }

    func testNeedsAttentionRowFallsBackToHealthAction() {
        XCTAssertEqual(TrayServerAction.forAttention(Self.loginActionServer()), .login)
    }
}
