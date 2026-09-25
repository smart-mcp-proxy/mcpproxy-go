import XCTest
@testable import MCPProxy

/// FR-012 (spec 091 T024): Home's connect control must go through the shared
/// Connect Client presentation path, and the legacy preview-less sheet —
/// which wrote a client config straight from a button, with no preview and no
/// backup disclosure — must be gone.
///
/// Spec 109 FR-051/T064: `DashboardView.swift` was renamed `HomeView.swift`
/// (Dashboard → Home) and its `AttentionRow`/`performAction` were rebuilt on
/// `AttentionItem`/`AttentionFix` (Spec 109 FR-001/FR-005) rather than
/// `ServerStatus`/`HealthAction` — this file's source-reading assertions
/// travel with that rename and rebuild.
@MainActor
final class HomeRoutingTests: XCTestCase {

    /// Home now opens the form instead of connecting: activating its control
    /// posts exactly the route the tray menu item posts.
    func testTheHomeConnectControlPostsTheSharedPresentationRoute() {
        let received = expectation(description: "presentation route posted")
        let token = NotificationCenter.default.addObserver(
            forName: ConnectClientPresentation.route, object: nil, queue: .main
        ) { _ in received.fulfill() }
        defer { NotificationCenter.default.removeObserver(token) }

        DashboardConnectControl.activate()

        wait(for: [received], timeout: 1)
    }

    /// The control is a doorway, not an action: its title says so, so nobody
    /// expects a write from pressing it.
    func testTheHomeControlIsLabelledAsOpeningTheForm() {
        XCTAssertEqual(DashboardConnectControl.title, "Connect Clients…")
        XCTAssertFalse(DashboardConnectControl.title.isEmpty)
    }

    /// The preview-less flow is deleted, not merely bypassed: a dormant sheet
    /// would be one `showConnectClients = true` away from writing configs again.
    func testTheLegacyPreviewLessConnectFlowIsGone() throws {
        let source = try homeSource()

        for forbidden in [
            "ConnectClientsSheet",      // the sheet itself
            "showConnectClients",       // its presentation flag
            "connectToClient(",         // the preview-less write
            "disconnectFromClient("     // and its unconfirmed counterpart
        ] {
            XCTAssertFalse(source.contains(forbidden),
                           "HomeView.swift still references \(forbidden)")
        }
        XCTAssertTrue(source.contains("DashboardConnectControl"),
                      "Home must route through the shared presentation path")
    }

    /// The legacy API methods went with it — a preview-less connect is not a
    /// capability this app keeps lying around.
    func testTheLegacyPreviewLessAPIMethodsAreGone() throws {
        let source = try apiClientSource()

        XCTAssertFalse(source.contains("func connectToClient("),
                       "APIClient still exposes the preview-less connect")
        XCTAssertFalse(source.contains("func disconnectFromClient("),
                       "APIClient still exposes the unconfirmed disconnect")
    }

    /// FR-014 (spec 109): the Web UI, CLI and macOS Servers/tray views all
    /// bind a server's primary action label through the ONE
    /// `HealthStatus.actionLabels` table. Home's AttentionRow instead binds
    /// through `AttentionFix.label`, which the CORE already renders through
    /// the same cross-surface wording (contracts/rest-api.md#attention) —
    /// there is no second label table to drift here.
    func testAttentionRowRendersTheServerSuppliedFixLabel() throws {
        let source = try homeSource()

        XCTAssertTrue(source.contains("Button(item.fix.label)"),
                      "AttentionRow must render the core's own fix.label verbatim (Spec 109), not a private label table")
    }

    /// Spec 109 FR-005: `login`/`restart`/`enable` run in place (the same
    /// three verbs the tray executes via `TrayServerAction.fromHealthAction`);
    /// every other verb this PR ships (`set_secret`, `configure`/`edit_url`,
    /// `view_logs`, `review`, `reload_hint`) must be handled explicitly, never
    /// fall into `default: break` silently.
    func testAttentionRowPerformFixHandlesEveryShippedVerb() throws {
        let source = try homeAttentionActionSource()
        let body = try performFixBody(in: source)

        for verb in ["set_secret", "configure", "edit_url", "view_logs", "review", "reload_hint"] {
            let pattern = #"case[^:]*"\#(verb)"[^:]*:"#
            let matches = body.range(of: pattern, options: .regularExpression)
            XCTAssertNotNil(matches,
                            "performFix must handle verb \"\(verb)\" explicitly, not fall into default: break")
        }
    }

    /// Routes each verb to the tab FR-005 names — not merely somewhere.
    func testAttentionRowPerformFixRoutesToTheCorrectTab() throws {
        let source = try homeAttentionActionSource()
        let body = try performFixBody(in: source)

        let configCase = try caseBody(labelContaining: "\"edit_url\"", in: body)
        XCTAssertTrue(configCase.contains("navigateToServerDetail(item.subject.name, tab: .config)"),
                      "set_secret/configure/edit_url must open the Config tab")

        let logsCase = try caseBody(labelContaining: "\"view_logs\"", in: body)
        XCTAssertTrue(logsCase.contains("navigateToServerDetail(item.subject.name, tab: .logs)"),
                      "view_logs must open the Logs tab, not .config")

        let reviewCase = try caseBody(labelContaining: "\"review\"", in: body)
        XCTAssertTrue(reviewCase.contains("navigateToServerDetail(item.subject.name, tab: .tools)"),
                      "review must open the server's existing per-tool review (interim fix target), never a one-click approve")
        XCTAssertFalse(reviewCase.contains("approveTools"),
                       "review must not call approveTools directly")

        let navigateBody = try functionBody(named: "navigateToServerDetail", in: source)
        XCTAssertTrue(navigateBody.contains("ServerDetailTarget(serverName: serverName, tab: tab)"),
                      "navigateToServerDetail must post a typed ServerDetailTarget carrying the requested tab")
        XCTAssertFalse(navigateBody.contains("object: serverName)"),
                       "navigateToServerDetail must not post a bare server-name String — ServersView would then default the tab to .tools regardless of which verb fired")
    }

    /// Spec 109 FR-005 / T064 pin: Home's review action never calls
    /// `approveTools`/`unquarantineServer` — that one-click approve
    /// (`DashboardView.swift:1066` in the old file) is removed by this PR.
    /// `HomeReviewActionTests.swift` pins the same fact behaviourally, over
    /// HTTP; this pins it at the source level so a future edit cannot
    /// reintroduce the call without also breaking a readable assertion here.
    func testHomeViewNeverCallsApproveToolsOrUnquarantineServer() throws {
        for (source, file) in [(try homeSource(), "HomeView.swift"), (try homeAttentionActionSource(), "HomeAttentionAction.swift")] {
            XCTAssertFalse(source.contains("approveTools("),
                           "\(file) must not call approveTools — review is a human decision on its own screen")
            XCTAssertFalse(source.contains("unquarantineServer("),
                           "\(file) must not call unquarantineServer — review is a human decision on its own screen")
        }
    }

    // MARK: - Helpers

    /// Isolates the body of `HomeAttentionAction.performFix` so assertions
    /// about its switch cases can't accidentally match an unrelated `case`
    /// elsewhere in the file.
    private func performFixBody(in source: String) throws -> String {
        guard let start = source.range(of: "static func performFix") else {
            XCTFail("could not find performFix in HomeAttentionAction.swift")
            return ""
        }
        return String(source[start.lowerBound...])
    }

    /// Extracts the body of one `switch` case — from a label containing
    /// `needle` up to (but not including) the next `case` label or the
    /// switch's closing brace — so an assertion about what a specific case
    /// *does* can't be satisfied by a sibling case that merely mentions the
    /// same tab elsewhere.
    private func caseBody(labelContaining needle: String, in source: String) throws -> String {
        // No trailing \b: `needle` is a quoted string literal (e.g. "edit_url"),
        // so the character before the case's terminating `:` or `,` is the
        // closing quote — a non-word character — and \b never matches between
        // two non-word characters, unlike the enum-case (`.editURL`) form this
        // pattern was adapted from.
        let pattern = "case[^:]*\\Q\(needle)\\E[^:]*:([\\s\\S]*?)(?=\\n\\s*case |\\n\\s*\\})"
        let regex = try NSRegularExpression(pattern: pattern)
        let nsSource = source as NSString
        guard let match = regex.firstMatch(in: source, range: NSRange(location: 0, length: nsSource.length)),
              match.numberOfRanges > 1 else {
            XCTFail("could not find a case label containing \(needle) in performFix")
            return ""
        }
        return nsSource.substring(with: match.range(at: 1))
    }

    /// Isolates one private function's body by name, bounded by the next
    /// top-level `private func`/`func` or the end of the file.
    private func functionBody(named name: String, in source: String) throws -> String {
        guard let start = source.range(of: "func \(name)(") else {
            XCTFail("could not find func \(name) in HomeView.swift")
            return ""
        }
        return String(source[start.lowerBound...])
    }

    private func homeSource() throws -> String {
        try source(at: "MCPProxy/Views/HomeView.swift")
    }

    private func homeAttentionActionSource() throws -> String {
        try source(at: "MCPProxy/State/HomeAttentionAction.swift")
    }

    private func apiClientSource() throws -> String {
        try source(at: "MCPProxy/API/APIClient.swift")
    }

    /// Reads a source file from the package the tests were compiled from, so the
    /// guard travels with the checkout rather than with a build product.
    private func source(at relativePath: String) throws -> String {
        let packageRoot = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent()   // MCPProxyTests
            .deletingLastPathComponent()   // package root
        let url = packageRoot.appendingPathComponent(relativePath)
        XCTAssertTrue(FileManager.default.fileExists(atPath: url.path),
                      "missing source file at \(url.path)")
        return try String(contentsOf: url, encoding: .utf8)
    }
}
