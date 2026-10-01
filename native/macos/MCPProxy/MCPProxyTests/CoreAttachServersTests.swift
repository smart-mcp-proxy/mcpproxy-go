// CoreAttachServersTests.swift
// MCPProxyTests
//
// Spec 108-k live-QA finding: the server list is SSE-driven (Spec 048), so a tray
// that attaches to a core that is already settled never heard a `servers.changed`
// event and `appState.servers` stayed empty for up to five minutes. The Profile
// editor's server checklist reads that list ("No servers configured.") and Home
// read "0 connected". Connecting must fetch the list once.

import XCTest
@testable import MCPProxy

final class CoreAttachServersTests: XCTestCase {

    private var stub: UnixSocketHTTPStub?
    private var manager: CoreProcessManager?

    override func tearDown() async throws {
        if let manager { await manager.shutdown() }
        manager = nil
        stub?.stop()
        stub = nil
        try await super.tearDown()
    }

    private static let serversBody = """
    {"success":true,"data":{"servers":[
      {"id":"github","name":"github","protocol":"stdio","enabled":true,"quarantined":false,
       "connected":true,"connecting":false,"status":"ready","tool_count":5,"authenticated":false},
      {"id":"notion","name":"notion","protocol":"stdio","enabled":true,"quarantined":false,
       "connected":true,"connecting":false,"status":"ready","tool_count":1,"authenticated":false}
    ]}}
    """

    func testAttachingToASettledCoreLoadsTheServerList() async throws {
        let stub = UnixSocketHTTPStub(at: nil) { _, path in
            switch path {
            case "/ready":
                return .json(#"{"ready":true}"#)
            case "/api/v1/info":
                return .json("""
                {"success":true,"data":{"version":"0.0.0-test",
                  "web_ui_url":"http://127.0.0.1:1/ui/?apikey=test-api-key",
                  "listen_addr":"127.0.0.1:1","endpoints":{"http":"http://127.0.0.1:1","socket":"unix"}}}
                """)
            case "/api/v1/servers":
                return .json(Self.serversBody)
            default:
                return .notFound
            }
        }
        try stub.start()
        self.stub = stub

        let appState = await MainActor.run { AppState() }
        let manager = CoreProcessManager(
            appState: appState,
            notificationService: NotificationService(deliveryEnabled: false),
            reconnectionPolicy: ReconnectionPolicy(
                baseDelay: 0.05, maxDelay: 0.1, maxAttempts: 2, jitterFactor: 0.0
            ),
            socketPath: stub.path,
            refreshInterval: 60,
            respawnVersionProvider: { nil }
        )
        self.manager = manager
        await manager.start(maySpawn: false)

        let names = await MainActor.run { appState.servers.map(\.name) }
        let connected = await MainActor.run { appState.connectedCount }
        XCTAssertEqual(names, ["github", "notion"], "the list must not wait for a servers.changed event")
        XCTAssertEqual(connected, 2)
    }
}
