import XCTest
import AppKit
@testable import MCPProxy

/// Tests for MCP-1822 (T3): a server in the OAuth login-required state
/// (`health.action == "login"`) must surface as a calm, actionable "Sign in"
/// affordance — never as a hard error. On the tray that means it is excluded
/// from the red "Fix issues" diagnostic section and does not tint the tray
/// icon badge, while still appearing in the calm "Needs Attention" group.
final class AppStateOAuthSignInTests: XCTestCase {

    // MARK: - Helpers

    private func decodeServer(_ jsonString: String) throws -> ServerStatus {
        let data = jsonString.data(using: .utf8)!
        return try JSONDecoder().decode(ServerStatus.self, from: data)
    }

    /// A server in the OAuth login-required state. Pre-T1 the backend
    /// classifies this as an `error`-severity `MCPX_UNKNOWN_UNCLASSIFIED`
    /// diagnostic; the tray must still treat it as a sign-in affordance, not
    /// a hard error, regardless of the diagnostic the backend attaches.
    private func loginRequiredServer(name: String = "github") throws -> ServerStatus {
        try decodeServer("""
        {
            "id": "\(name)",
            "name": "\(name)",
            "protocol": "http",
            "enabled": true,
            "connected": false,
            "quarantined": false,
            "tool_count": 0,
            "error_code": "MCPX_UNKNOWN_UNCLASSIFIED",
            "diagnostic": {
                "code": "MCPX_UNKNOWN_UNCLASSIFIED",
                "severity": "error",
                "user_message": "Server error"
            },
            "health": {
                "level": "degraded",
                "admin_state": "enabled",
                "summary": "Sign in required",
                "action": "login"
            }
        }
        """)
    }

    /// A server with a genuine, non-OAuth failure that should still surface
    /// as a hard error in the tray.
    private func brokenServer(name: String = "filesystem") throws -> ServerStatus {
        try decodeServer("""
        {
            "id": "\(name)",
            "name": "\(name)",
            "protocol": "stdio",
            "enabled": true,
            "connected": false,
            "quarantined": false,
            "tool_count": 0,
            "error_code": "MCPX_STDIO_SPAWN_ENOENT",
            "diagnostic": {
                "code": "MCPX_STDIO_SPAWN_ENOENT",
                "severity": "error",
                "user_message": "Command not found"
            },
            "health": {
                "level": "unhealthy",
                "admin_state": "enabled",
                "summary": "Failed to start",
                "action": "restart"
            }
        }
        """)
    }

    // MARK: - serversWithDiagnostic (Fix Issues section)

    func testLoginRequiredServerExcludedFromFixIssues() throws {
        let state = AppState()
        state.servers = [try loginRequiredServer()]
        XCTAssertTrue(
            state.serversWithDiagnostic.isEmpty,
            "A login-required server must not appear in the red 'Fix issues' diagnostic section"
        )
    }

    func testGenuineErrorStillSurfacesInFixIssues() throws {
        let state = AppState()
        state.servers = [try brokenServer()]
        XCTAssertEqual(
            state.serversWithDiagnostic.map(\.name), ["filesystem"],
            "A genuine non-OAuth error must still surface in 'Fix issues'"
        )
    }

    func testMixedServersOnlySurfaceGenuineError() throws {
        let state = AppState()
        state.servers = [try loginRequiredServer(), try brokenServer()]
        XCTAssertEqual(
            state.serversWithDiagnostic.map(\.name), ["filesystem"],
            "Only the genuinely broken server should be in 'Fix issues'; the sign-in server is calm"
        )
    }

    // MARK: - worstDiagnosticSeverity (tray icon badge tint)

    func testLoginRequiredServerDoesNotTintBadge() throws {
        let state = AppState()
        state.servers = [try loginRequiredServer()]
        XCTAssertNil(
            state.worstDiagnosticSeverity,
            "A sign-in-required server must not tint the tray icon badge red"
        )
    }

    func testGenuineErrorTintsBadgeRed() throws {
        let state = AppState()
        state.servers = [try loginRequiredServer(), try brokenServer()]
        XCTAssertEqual(
            state.worstDiagnosticSeverity, "error",
            "A genuine error must still tint the badge even when a sign-in server is present"
        )
    }

    // MARK: - serversNeedingAttention (calm Sign in path)

    func testLoginRequiredServerStillNeedsAttention() throws {
        let state = AppState()
        state.servers = [try loginRequiredServer()]
        XCTAssertEqual(
            state.serversNeedingAttention.map(\.name), ["github"],
            "A sign-in-required server must remain in the calm 'Needs Attention' group"
        )
    }

    // MARK: - menuStatusNSColor (active AppKit tray dot, MCP-1856)

    /// The active AppKit tray (`MCPProxyApp.swift`) draws its server dot from
    /// `menuStatusNSColor`. A login-required server must get the calm accent
    /// tint, never the red `statusNSColor` error treatment, so the shipping
    /// menu no longer renders a red error dot for the pure sign-in state.
    func testLoginRequiredServerUsesCalmAccentDot() throws {
        let server = try loginRequiredServer()
        XCTAssertEqual(
            server.menuStatusNSColor, NSColor.controlAccentColor,
            "A login-required server's tray dot must use the calm accent tint, not statusNSColor"
        )
        XCTAssertNotEqual(
            server.menuStatusNSColor, NSColor.systemRed,
            "A login-required server's tray dot must never be the red error color"
        )
    }

    /// A genuine (non-login) failure must keep the red `statusNSColor` tint in
    /// the tray menu — only the sign-in state is calmed.
    func testGenuineErrorKeepsRedDot() throws {
        let server = try brokenServer()
        XCTAssertEqual(
            server.statusNSColor, NSColor.systemRed,
            "Precondition: a broken server's health color is red"
        )
        XCTAssertEqual(
            server.menuStatusNSColor, server.statusNSColor,
            "A genuine error must keep its statusNSColor tint in the tray menu"
        )
    }
}
