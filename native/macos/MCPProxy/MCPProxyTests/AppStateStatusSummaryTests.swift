import XCTest
@testable import MCPProxy

@MainActor
final class AppStateStatusSummaryTests: XCTestCase {

    /// The cold-start case the user reported: core just transitioned to
    /// `.connected` but the first `/api/v1/servers` response hasn't arrived
    /// yet. The header must say "Loading…" — not the misleading
    /// "No servers configured".
    func testConnectedBeforeFirstFetchShowsLoading() {
        let state = AppState()
        state.coreState = .connected
        // serversLoaded defaults to false, totalServers to 0
        XCTAssertEqual(state.statusSummary, "Loading…")
    }

    /// Once a (possibly empty) server list has come back from the API, the
    /// header should reflect reality: the user really has zero servers
    /// configured, and we should say so.
    func testConnectedAfterEmptyFetchShowsNoServersConfigured() {
        let state = AppState()
        state.coreState = .connected
        state.updateServers([])
        XCTAssertTrue(state.serversLoaded)
        XCTAssertEqual(state.statusSummary, "No servers configured")
    }

    /// After a real fetch returns a populated list the header switches to
    /// the standard "X/Y servers, Z tools" format.
    func testConnectedAfterFetchWithServersShowsCounts() throws {
        let state = AppState()
        state.coreState = .connected
        state.updateServers([Self.makeServer(name: "s1", connected: true, toolCount: 7)])
        XCTAssertEqual(state.statusSummary, "1/1 servers, 7 tools")
    }

    /// The explicit "Stopped" state must always win over loading/connected
    /// substates so the user always sees the truth when they have manually
    /// stopped the core.
    func testStoppedTakesPrecedenceOverLoading() {
        let state = AppState()
        state.coreState = .connected
        state.isStopped = true
        XCTAssertEqual(state.statusSummary, "Stopped")
    }

    // MARK: - Helpers

    private static func makeServer(name: String, connected: Bool, toolCount: Int) -> ServerStatus {
        let json = """
        {
            "id": "\(name)",
            "name": "\(name)",
            "protocol": "http",
            "enabled": true,
            "connected": \(connected),
            "quarantined": false,
            "tool_count": \(toolCount)
        }
        """.data(using: .utf8)!
        // Decoding via the canonical Codable path keeps this test robust to
        // future field additions on ServerStatus.
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(ServerStatus.self, from: json)
    }
}
