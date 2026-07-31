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

    // MARK: - Helpers

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
