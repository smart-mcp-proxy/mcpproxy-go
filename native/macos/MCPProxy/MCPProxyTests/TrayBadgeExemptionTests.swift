// TrayBadgeExemptionTests.swift
// MCPProxyTests
//
// A quarantined server used to tint the menu-bar badge red forever. The
// fixtures here are the live v0.61.0 /api/v1/servers payloads that did it.

import XCTest
@testable import MCPProxy

final class TrayBadgeExemptionTests: XCTestCase {

    // MARK: - Fixtures (verbatim shapes from a live install)

    /// A quarantined server that mcpproxy connected for scanner inspection and
    /// failed to reach. Note the contradiction inside ONE payload: the health
    /// block says healthy/quarantined, the diagnostic says error.
    private static func quarantinedServer(name: String) -> ServerStatus {
        decode("""
        {
            "id": "\(name)", "name": "\(name)", "protocol": "http",
            "enabled": true, "connected": false, "quarantined": true, "tool_count": 0,
            "error_code": "MCPX_UNKNOWN_UNCLASSIFIED",
            "diagnostic": {"code": "MCPX_UNKNOWN_UNCLASSIFIED", "severity": "error", "summary": "boom"},
            "health": {"level": "healthy", "admin_state": "quarantined",
                       "summary": "Quarantined for review", "action": "approve"}
        }
        """)
    }

    private static func loginRequiredServer(name: String) -> ServerStatus {
        decode("""
        {
            "id": "\(name)", "name": "\(name)", "protocol": "http",
            "enabled": true, "connected": false, "quarantined": false, "tool_count": 0,
            "error_code": "MCPX_OAUTH_LOGIN_REQUIRED",
            "diagnostic": {"code": "MCPX_OAUTH_LOGIN_REQUIRED", "severity": "error", "summary": "sign in"},
            "health": {"level": "degraded", "admin_state": "enabled",
                       "summary": "Sign-in required", "action": "login"}
        }
        """)
    }

    /// A genuinely broken server: `command: "npx"` with no args.
    private static func brokenServer(name: String) -> ServerStatus {
        decode("""
        {
            "id": "\(name)", "name": "\(name)", "protocol": "stdio",
            "enabled": true, "connected": false, "quarantined": false, "tool_count": 0,
            "error_code": "MCPX_CONFIG_INVALID_COMMAND",
            "diagnostic": {"code": "MCPX_CONFIG_INVALID_COMMAND", "severity": "error", "summary": "no args"},
            "health": {"level": "unhealthy", "admin_state": "enabled",
                       "summary": "failed to connect", "action": "restart"}
        }
        """)
    }

    private static func decode(_ json: String) -> ServerStatus {
        // swiftlint:disable:next force_try
        try! JSONDecoder().decode(ServerStatus.self, from: json.data(using: .utf8)!)
    }

    // MARK: - The bug

    /// The reported symptom: a red menu-bar dot that nothing the user did could
    /// clear, on an install whose only error-severity diagnostics belonged to
    /// quarantined servers.
    @MainActor
    func testAQuarantinedServerDoesNotTintTheBadge() {
        let state = AppState()
        state.servers = [Self.quarantinedServer(name: "everything")]

        XCTAssertNil(state.worstDiagnosticSeverity,
                     "a server held for review is an intentional state, not a fault")
        XCTAssertEqual(state.diagnosticCount(severity: "error"), 0)
        XCTAssertEqual(
            TrayStatusIcon.badge(isStopped: false, hasCoreError: false,
                                 worstDiagnosticSeverity: state.worstDiagnosticSeverity),
            .none)
    }

    /// The same exemption that already existed for sign-in, still working —
    /// this is the precedent the quarantine case was missing.
    @MainActor
    func testALoginRequiredServerStillDoesNotTintTheBadge() {
        let state = AppState()
        state.servers = [Self.loginRequiredServer(name: "cloudflare-graphql")]
        XCTAssertNil(state.worstDiagnosticSeverity)
    }

    /// The exemption must not swallow real faults: a misconfigured server is
    /// exactly what the badge exists to show.
    @MainActor
    func testAGenuinelyBrokenServerStillTintsTheBadgeRed() {
        let state = AppState()
        state.servers = [
            Self.quarantinedServer(name: "everything"),
            Self.loginRequiredServer(name: "cloudflare-graphql"),
            Self.brokenServer(name: "demo-filesystem"),
        ]
        XCTAssertEqual(state.worstDiagnosticSeverity, "error")
        XCTAssertEqual(state.diagnosticCount(severity: "error"), 1,
                       "only the misconfigured server counts toward the badge")
    }

    /// The two surfaces disagreeing is what made this so confusing to read: the
    /// menu header drew a calm yellow dot from `serversNeedingAttention` while
    /// the menu-bar badge drew red from `worstDiagnosticSeverity`, off the same
    /// payload. Quarantine must still ask for attention — just not in red.
    @MainActor
    func testQuarantineStillAsksForAttentionJustNotInRed() {
        let state = AppState()
        state.servers = [Self.quarantinedServer(name: "everything")]

        XCTAssertEqual(state.serversNeedingAttention.count, 1,
                       "the user still has to approve it — the menu must say so")
        XCTAssertNil(state.worstDiagnosticSeverity,
                     "but the menu bar must not scream about it")
    }

    /// `diagnosticCount` and `worstDiagnosticSeverity` must filter identically,
    /// or the tooltip says "1 server error" over a badge that is not drawn.
    @MainActor
    func testTheSpokenCountAgreesWithTheBadgeUnderExemptions() {
        let state = AppState()
        state.servers = [
            Self.quarantinedServer(name: "q1"),
            Self.quarantinedServer(name: "q2"),
            Self.loginRequiredServer(name: "l1"),
        ]
        XCTAssertNil(state.worstDiagnosticSeverity)
        XCTAssertEqual(state.diagnosticCount(severity: "error"), 0,
                       "nothing is badged, so nothing may be counted")
    }

    /// The composition the whole fix set rests on, and the one shape that was
    /// untested: new servers are quarantined by default
    /// (`config_load_admission_gate.go` sets `Quarantined = true` on add), so
    /// the PR's own live fixture — an `npx`-with-no-args server — arrives
    /// quarantined AND misconfigured. The new MCPX_CONFIG_INVALID_COMMAND
    /// therefore does NOT tint the badge in the commonest case.
    ///
    /// That is deliberate, not an oversight: quarantine review is the first
    /// thing the user must do, and it is surfaced calmly. But it is worth
    /// pinning, because "fix a config error" and "approve this server" are
    /// exactly the two states someone might later assume compose differently.
    @MainActor
    func testANewlyAddedBrokenServerAsksForReviewNotAlarm() {
        let broken = Self.decode("""
        {
            "id": "demo", "name": "demo", "protocol": "stdio",
            "enabled": true, "connected": false, "quarantined": true, "tool_count": 0,
            "error_code": "MCPX_CONFIG_INVALID_COMMAND",
            "diagnostic": {"code": "MCPX_CONFIG_INVALID_COMMAND", "severity": "error", "summary": "no args"},
            "health": {"level": "healthy", "admin_state": "quarantined",
                       "summary": "Quarantined for review", "action": "approve"}
        }
        """)
        let state = AppState()
        state.servers = [broken]

        XCTAssertNil(state.worstDiagnosticSeverity,
                     "a server awaiting review must not raise a red badge, even when broken")
        XCTAssertEqual(state.serversNeedingAttention.count, 1,
                       "but it must still be listed — the user has to act on it")
        XCTAssertEqual(broken.health?.action, "approve",
                       "and the action offered is review, not restart")
    }

    /// Once reviewed and released from quarantine, the SAME broken server must
    /// raise the badge — otherwise the exemption would hide the fault forever.
    @MainActor
    func testTheSameServerAlarmsOnceItLeavesQuarantine() {
        let state = AppState()
        state.servers = [Self.brokenServer(name: "demo")]
        XCTAssertEqual(state.worstDiagnosticSeverity, "error")
        XCTAssertEqual(state.diagnosticCount(severity: "error"), 1)
    }

    /// The three filters that must agree. `serversWithDiagnostic` was left on
    /// the narrower OAuth-only predicate when `isBadgeExempt` was introduced.
    @MainActor
    func testAllThreeDiagnosticFiltersAgreeOnExemptions() {
        let state = AppState()
        state.servers = [
            Self.quarantinedServer(name: "q"),
            Self.loginRequiredServer(name: "l"),
        ]
        XCTAssertNil(state.worstDiagnosticSeverity)
        XCTAssertEqual(state.diagnosticCount(severity: "error"), 0)
        XCTAssertTrue(state.serversWithDiagnostic.isEmpty,
                      "serversWithDiagnostic must use the same exemption as the badge")
    }

    /// `quarantined: true` alone is enough, even if the health block is absent
    /// or stale — the two signals are checked independently on purpose.
    @MainActor
    func testTheQuarantineFlagAloneIsEnough() {
        let server = Self.decode("""
        {
            "id": "q", "name": "q", "protocol": "http",
            "enabled": true, "connected": false, "quarantined": true, "tool_count": 0,
            "diagnostic": {"code": "MCPX_TEST", "severity": "error", "summary": "boom"}
        }
        """)
        XCTAssertTrue(server.isQuarantineReview)
        XCTAssertTrue(server.isBadgeExempt)

        let state = AppState()
        state.servers = [server]
        XCTAssertNil(state.worstDiagnosticSeverity)
    }
}
