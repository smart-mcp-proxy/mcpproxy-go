import XCTest
@testable import MCPProxy

/// FR-012 (spec 091 T024): the dashboard's connect control must go through the
/// shared Connect Client presentation path, and the legacy preview-less sheet —
/// which wrote a client config straight from a button, with no preview and no
/// backup disclosure — must be gone.
@MainActor
final class DashboardRoutingTests: XCTestCase {

    /// The dashboard now opens the form instead of connecting: activating its
    /// control posts exactly the route the tray menu item posts.
    func testTheDashboardConnectControlPostsTheSharedPresentationRoute() {
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
    func testTheDashboardControlIsLabelledAsOpeningTheForm() {
        XCTAssertEqual(DashboardConnectControl.title, "Connect Clients…")
        XCTAssertFalse(DashboardConnectControl.title.isEmpty)
    }

    /// The preview-less flow is deleted, not merely bypassed: a dormant sheet
    /// would be one `showConnectClients = true` away from writing configs again.
    func testTheLegacyPreviewLessConnectFlowIsGone() throws {
        let source = try dashboardSource()

        for forbidden in [
            "ConnectClientsSheet",      // the sheet itself
            "showConnectClients",       // its presentation flag
            "connectToClient(",         // the preview-less write
            "disconnectFromClient("     // and its unconfirmed counterpart
        ] {
            XCTAssertFalse(source.contains(forbidden),
                           "DashboardView.swift still references \(forbidden)")
        }
        XCTAssertTrue(source.contains("DashboardConnectControl"),
                      "the dashboard must route through the shared presentation path")
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

    /// FR-014 (spec 109): the Web UI, CLI and macOS all bind a server's
    /// primary action label through the ONE `HealthStatus.actionLabels`
    /// table (e.g. "Review", "Add secret") — this PR wired that table into
    /// the detail-view "Suggested Action" row but left the Dashboard's own
    /// AttentionRow primary CTA reading the older, differently-worded
    /// `HealthAction.label` enum ("Approve", "Set Secret"), so macOS button
    /// text diverged from the other two surfaces for those actions.
    func testAttentionRowButtonUsesTheSharedActionLabelTable() throws {
        let source = try dashboardSource()

        XCTAssertFalse(source.contains("Button(action.label)"),
                       "AttentionRow must render actions[0] through HealthStatus.actionLabels (FR-014), not the private HealthAction.label enum")
        XCTAssertTrue(source.contains("HealthStatus.actionLabels"),
                      "AttentionRow must bind through the shared cross-surface action-label table")
    }

    /// Round 1 added `HealthAction.editURL` and made AttentionRow render its
    /// button, but `performAction`'s switch had no `.editURL` case (nor
    /// `.setSecret`/`.configure`/`.viewLogs`, which pre-date that diff) and
    /// fell into `default: break` — clicking the new, prominent "Edit URL"
    /// CTA did nothing, silently. None of those four actions completes via a
    /// single core API call, so each must instead navigate the user to the
    /// server's own detail view (Config for edit_url/set_secret/configure,
    /// Logs for view_logs) rather than no-op.
    func testAttentionRowPerformActionHandlesEveryHealthAction() throws {
        let source = try dashboardSource()
        let body = try performActionBody(in: source)

        for action in ["editURL", "setSecret", "configure", "viewLogs"] {
            // Matches `.\(action)` inside any `case ... :` label — including
            // a combined label like `case .setSecret, .configure, .editURL:`
            // — so the assertion isn't defeated by how the cases are grouped.
            let pattern = #"case[^:]*\.\#(action)\b[^:]*:"#
            let matches = body.range(of: pattern, options: .regularExpression)
            XCTAssertNotNil(matches,
                            "performAction must handle HealthAction.\(action) explicitly, not fall into default: break")
        }
    }

    // MARK: - Helpers

    /// Isolates the body of `performAction` (the last function in the file)
    /// so assertions about its switch cases can't accidentally match an
    /// unrelated `case .foo` elsewhere in DashboardView.swift.
    private func performActionBody(in source: String) throws -> String {
        guard let start = source.range(of: "private func performAction") else {
            XCTFail("could not find performAction in DashboardView.swift")
            return ""
        }
        return String(source[start.lowerBound...])
    }

    private func dashboardSource() throws -> String {
        try source(at: "MCPProxy/Views/DashboardView.swift")
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
