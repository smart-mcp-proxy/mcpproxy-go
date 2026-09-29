import XCTest
@testable import MCPProxy

final class ClientsPresenceTests: XCTestCase {

    func testConnectSheetReloadsTheNativeClientsListWhenDismissed() throws {
        let packageRoot = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent()
            .deletingLastPathComponent()
        let sourceURL = packageRoot.appendingPathComponent("MCPProxy/Views/ClientsView.swift")
        let source = try String(contentsOf: sourceURL, encoding: .utf8)

        XCTAssertTrue(source.contains(".sheet(isPresented: $showConnect, onDismiss: {"),
                      "closing the connect sheet must refresh the native Clients hub after a completed write")
        XCTAssertTrue(source.contains("onDismiss: {\n            Task { await load() }"),
                      "the connect-sheet dismissal must reload the Clients hub from the authoritative API")
    }

    func testListPresenceDecodesLightweightRowsAndSharedStateLabels() throws {
        let data = Data(#"{"clients":[{"id":"cursor","display_name":"Cursor","kind":"supported","state":"installed","installed":true,"connected":false,"connection_unverified":true,"active_sessions":0,"calls_24h":2},{"id":"other:zed","display_name":"Zed","kind":"other","state":"other","installed":false,"connected":false,"active_sessions":1,"calls_24h":0}]}"#.utf8)
        let response = try JSONDecoder().decode(ClientsResponse.self, from: data)

        XCTAssertEqual(response.clients.map(\.id), ["cursor", "other:zed"])
        XCTAssertEqual(response.clients[0].stateLabel, "Installed — connection unverified")
        XCTAssertNil(response.clients[0].sessions, "the list contract must not materialize detail sessions")
        XCTAssertEqual(response.clients[1].stateLabel, "Observed")
    }

    func testDetailPresenceDecodesWorkSessionAndReloadHint() throws {
        let data = Data(#"{"id":"claude-code","display_name":"Claude Code","kind":"supported","state":"connected_seen","installed":true,"connected":true,"active_sessions":1,"calls_24h":17,"display_path":"~/.claude.json","reload_hint":"Restart Claude Code","sessions":[{"id":"transport-1","work_session_id":"ws-1","started_at":"2026-09-29T08:00:00Z","last_activity":"2026-09-29T09:00:00Z"}]}"#.utf8)
        let row = try JSONDecoder().decode(ClientPresenceRecord.self, from: data)

        XCTAssertEqual(row.stateLabel, "Connected")
        XCTAssertEqual(row.effectiveDisplayPath, "~/.claude.json")
        XCTAssertEqual(row.reloadHint, "Restart Claude Code")
        XCTAssertEqual(row.sessions?.first?.displayID, "ws-1")
    }
}
