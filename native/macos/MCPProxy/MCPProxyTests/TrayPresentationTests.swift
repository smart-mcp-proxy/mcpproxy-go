// TrayPresentationTests.swift
// MCPProxyTests
//
// The decision half of the 2026-08 tray UX audit fixes: what the menu-bar icon
// says (F1/F2), how a transport is named (F12), how a profile that resolves to
// nothing is labelled (F11), how servers are filed in the submenu (F15), and
// what a failed action tells the user (F3).

import XCTest
@testable import MCPProxy

final class TrayPresentationTests: XCTestCase {

    // MARK: - F1 · The icon shows server health

    /// The finding itself: five servers needing attention left the menu bar
    /// looking exactly like all-healthy.
    func testServerDiagnosticsBadgeTheMenuBarIcon() {
        XCTAssertEqual(
            TrayStatusIcon.badge(isStopped: false, hasCoreError: false, worstDiagnosticSeverity: "error"),
            .severity(.error))
        XCTAssertEqual(
            TrayStatusIcon.badge(isStopped: false, hasCoreError: false, worstDiagnosticSeverity: "warn"),
            .severity(.warn))
    }

    func testAHealthyProxyKeepsThePlainIcon() {
        XCTAssertEqual(
            TrayStatusIcon.badge(isStopped: false, hasCoreError: false, worstDiagnosticSeverity: nil),
            .none)
        XCTAssertEqual(TrayStatusIcon.glyph(for: .none), "")
    }

    /// A stopped or erroring core makes every server verdict stale — it must
    /// not be told twice, more quietly, by an amber dot.
    func testCoreStateOutranksServerSeverity() {
        XCTAssertEqual(
            TrayStatusIcon.badge(isStopped: true, hasCoreError: false, worstDiagnosticSeverity: "error"),
            .stopped)
        XCTAssertEqual(
            TrayStatusIcon.badge(isStopped: false, hasCoreError: true, worstDiagnosticSeverity: "warn"),
            .coreError)
    }

    /// The two CORE states keep distinct full-size glyphs; server severity does
    /// not use a glyph at all any more (it is a corner dot), so the uniqueness
    /// rule applies to the states that still draw text.
    func testEveryCoreBadgeHasItsOwnGlyph() {
        let glyphs = [TrayIconBadge.stopped, .coreError].map(TrayStatusIcon.glyph(for:))
        XCTAssertEqual(Set(glyphs).count, glyphs.count, "two states drawn identically is no state at all")
        XCTAssertFalse(glyphs.contains(""), "a badged state with no glyph is invisible")
    }

    /// The whole point of the corner-dot change: a server severity must NOT
    /// widen the status item with a full-size "● " beside the icon.
    func testServerSeverityDrawsNoGlyphBesideTheIcon() {
        XCTAssertEqual(TrayStatusIcon.glyph(for: .severity(.error)), "")
        XCTAssertEqual(TrayStatusIcon.glyph(for: .severity(.warn)), "")
    }

    func testServerSeverityDrawsACornerDot() {
        XCTAssertEqual(TrayStatusIcon.dotSeverity(for: .severity(.error)), .error)
        XCTAssertEqual(TrayStatusIcon.dotSeverity(for: .severity(.warn)), .warn)
    }

    /// A core outage is about the proxy, not one server — it keeps the wider
    /// glyph rather than collapsing into the same dot, which would lose the
    /// distinction between "nothing works" and "one server is unhappy".
    func testCoreStatesDoNotUseTheCornerDot() {
        XCTAssertNil(TrayStatusIcon.dotSeverity(for: .stopped))
        XCTAssertNil(TrayStatusIcon.dotSeverity(for: .coreError))
        XCTAssertNil(TrayStatusIcon.dotSeverity(for: .none))
    }

    /// Moving a state between the glyph and the dot must never drop it: every
    /// badge has to be visible through one channel or the other.
    func testNoBadgeIsInvisible() {
        for badge: TrayIconBadge in [.none, .stopped, .coreError, .severity(.warn), .severity(.error)] {
            XCTAssertTrue(TrayStatusIcon.isVisible(badge), "\(badge) is drawn nowhere")
        }
    }

    // MARK: - F2 · Spoken, not just drawn

    func testTheStatusItemSaysWhatIsWrongInWords() {
        let label = TrayStatusIcon.accessibilityLabel(
            for: .severity(.error), summary: "13/29 servers, 942 tools", attentionCount: 5)
        XCTAssertTrue(label.hasPrefix("MCPProxy"))
        XCTAssertTrue(label.contains("13/29 servers"))
        XCTAssertTrue(label.contains("5 server errors"), "got: \(label)")
    }

    func testOneFailingServerIsNotPluralised() {
        let label = TrayStatusIcon.accessibilityLabel(
            for: .severity(.warn), summary: "1/1 servers, 4 tools", attentionCount: 1)
        XCTAssertTrue(label.contains("1 server warning"), "got: \(label)")
        XCTAssertFalse(label.contains("warnings"))
    }

    /// The spoken count must be of the severity being badged, over the same
    /// enabled, non-OAuth set `worstDiagnosticSeverity` uses — otherwise the
    /// status item says "4 server errors" over a badge that counted three
    /// (cross-model review, round 1).
    @MainActor
    func testTheSpokenCountMatchesTheBadgedSeverity() {
        let state = AppState()
        state.servers = [
            Self.diagnosingServer(name: "a", enabled: true, severity: "error"),
            Self.diagnosingServer(name: "b", enabled: true, severity: "error"),
            Self.diagnosingServer(name: "c", enabled: true, severity: "warn"),
            Self.diagnosingServer(name: "d", enabled: false, severity: "error"),
        ]
        XCTAssertEqual(state.worstDiagnosticSeverity, "error")
        XCTAssertEqual(state.diagnosticCount(severity: "error"), 2,
                       "the disabled server is not part of the badge")
        XCTAssertEqual(state.diagnosticCount(severity: "warn"), 1)
        XCTAssertEqual(state.serversWithDiagnostic.count, 4,
                       "the old input counted every diagnostic, enabled or not")
    }

    private static func diagnosingServer(name: String, enabled: Bool, severity: String) -> ServerStatus {
        let json = """
        {
            "id": "\(name)", "name": "\(name)", "protocol": "http",
            "enabled": \(enabled), "connected": false, "quarantined": false, "tool_count": 0,
            "error_code": "MCPX_TEST",
            "diagnostic": {"code": "MCPX_TEST", "severity": "\(severity)", "summary": "boom"}
        }
        """.data(using: .utf8)!
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(ServerStatus.self, from: json)
    }

    func testAStoppedCoreIsNamedNotJustGlyphed() {
        let label = TrayStatusIcon.accessibilityLabel(for: .stopped, summary: "Stopped", attentionCount: 0)
        XCTAssertTrue(label.contains("core stopped"), "got: \(label)")
    }

    // MARK: - F12 · Transport names

    func testTheThreeHTTPSpellingsReadAsOneFamily() {
        XCTAssertEqual(TrayProtocolDisplay.label(for: "streamable-http"), "HTTP (streamable)")
        XCTAssertEqual(TrayProtocolDisplay.label(for: "sse"), "HTTP (SSE)")
        XCTAssertEqual(TrayProtocolDisplay.label(for: "http"), "HTTP")
        XCTAssertEqual(TrayProtocolDisplay.label(for: "stdio"), "stdio")
    }

    /// An unknown transport must survive verbatim rather than be swallowed —
    /// a blank protocol line is worse than an ugly one.
    func testAnUnknownProtocolIsShownAsGiven() {
        XCTAssertEqual(TrayProtocolDisplay.label(for: "websocket"), "websocket")
    }

    // MARK: - F11 · Profiles that resolve to nothing

    func testAProfileWhoseServersAreNotConfiguredSaysSo() {
        let label = TrayProfileDisplay.label(
            name: "research", servers: ["github", "gitlab"], toolCount: 0,
            knownServers: ["everything", "ElevenLabs"])
        XCTAssertEqual(label, "research — no servers")
    }

    func testAWorkingProfileShowsServersAndTools() {
        let label = TrayProfileDisplay.label(
            name: "deploy", servers: ["github", "k8s"], toolCount: 27,
            knownServers: ["github", "k8s", "jira"])
        XCTAssertEqual(label, "deploy (2 servers · 27 tools)")
    }

    /// The count is the EFFECTIVE one: a profile listing three servers of which
    /// one exists scopes agents to one server, and must say one.
    func testOnlyConfiguredServersCount() {
        let label = TrayProfileDisplay.label(
            name: "solo", servers: ["github", "ghost", "phantom"], toolCount: 4,
            knownServers: ["github"])
        XCTAssertEqual(label, "solo (1 server · 4 tools)")
    }

    // MARK: - F15 · Filing the Servers submenu

    func testDisabledServersAreFiledAwayFromWorkingOnes() {
        XCTAssertEqual(TrayServerGrouping.group(enabled: false, needsAttention: false), .disabled)
        XCTAssertEqual(TrayServerGrouping.group(enabled: true, needsAttention: true), .needsAttention)
        XCTAssertEqual(TrayServerGrouping.group(enabled: true, needsAttention: false), .active)
    }

    /// A disabled server that also has a health action is still disabled: the
    /// admin state is the user's own decision and outranks the symptom.
    func testDisabledWinsOverAttention() {
        XCTAssertEqual(TrayServerGrouping.group(enabled: false, needsAttention: true), .disabled)
    }

    // MARK: - F3 · Failures that say something

    func testAFailedActionNamesTheServerAndTheVerb() {
        let title = TrayServerActionFailure.title(action: .restart, server: "github")
        XCTAssertEqual(title, "Couldn’t restart github")
    }

    func testTheMessageCarriesTheErrorAndAWayForward() {
        struct Boom: LocalizedError { var errorDescription: String? { "500 upstream refused" } }
        let message = TrayServerActionFailure.message(action: .enable, server: "jira", error: Boom())
        XCTAssertTrue(message.contains("500 upstream refused"))
        XCTAssertTrue(message.contains("jira"))
    }

    /// An alert body that is empty (or just whitespace from a bare error) reads
    /// as a bug in the tray rather than a failure of the action.
    func testAnErrorWithNothingToSayStillProducesABody() {
        struct Silent: LocalizedError { var errorDescription: String? { "   " } }
        let message = TrayServerActionFailure.message(action: .login, server: "cloudflare", error: Silent())
        XCTAssertTrue(message.contains("did not say why"), "got: \(message)")
    }

    // MARK: - F4/F7 · One verb per operation

    func testHealthActionsMapToTheVerbsTheMenuShows() {
        XCTAssertEqual(TrayServerAction.fromHealthAction("login"), .login)
        XCTAssertEqual(TrayServerAction.fromHealthAction("restart"), .restart)
        XCTAssertEqual(TrayServerAction.fromHealthAction("enable"), .enable)
    }

    /// Quarantine approval is a human decision. If it ever became a dispatchable
    /// action, a Needs Attention row would approve a quarantined server on a
    /// click that reads as navigation — exactly the F4 failure mode.
    func testQuarantineApprovalIsNeverDispatched() {
        XCTAssertNil(TrayServerAction.fromHealthAction("approve"))
        XCTAssertNil(TrayServerAction.fromHealthAction("set_secret"))
        XCTAssertNil(TrayServerAction.fromHealthAction(""))
    }
}
