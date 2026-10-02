// SettingsEffectiveStateTests.swift
// MCPProxyTests
//
// Spec 109 FR-044a / FR-044b (codex first-run user test F-03 and F-10):
// macOS Settings names the core it is actually connected to, adopts that
// core's running listen address when the config omits it, notes a pending
// restart, and locks the telemetry toggle when an environment variable forces
// it off.

import Foundation
import XCTest
@testable import MCPProxy

/// Path-keyed stub: unlike the other stubs here, which replay one body for every
/// path, Settings loads /api/v1/config AND /api/v1/status.
final class SettingsStubURLProtocol: URLProtocol {
    nonisolated(unsafe) static var bodies: [String: String] = [:]

    static func makeClient() -> APIClient {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [SettingsStubURLProtocol.self]
        return APIClient(
            session: URLSession(configuration: config),
            baseURL: "http://127.0.0.1:8080",
            apiKey: nil,
            transportKind: .unixSocket)
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let path = request.url?.path ?? ""
        let body = Self.bodies[path]
        let response = HTTPURLResponse(
            url: request.url ?? URL(string: "http://127.0.0.1:8080")!,
            statusCode: body == nil ? 404 : 200,
            httpVersion: "HTTP/1.1",
            headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data((body ?? "{}").utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

@MainActor
final class SettingsEffectiveStateTests: XCTestCase {

    private func envelope(_ json: String) -> String { "{\"success\":true,\"data\":\(json)}" }

    private func makeStore(
        configJSON: String, statusJSON: String, version: String = "v0.1.0"
    ) -> ConfigStore {
        SettingsStubURLProtocol.bodies = [
            "/api/v1/config": envelope("{\"config\":\(configJSON)}"),
            "/api/v1/status": envelope(statusJSON),
        ]
        let appState = AppState()
        appState.apiClient = SettingsStubURLProtocol.makeClient()
        appState.version = version
        return ConfigStore(appState: appState)
    }

    private func status(_ json: String) throws -> StatusResponse {
        try JSONDecoder().decode(StatusResponse.self, from: Data(json.utf8))
    }

    private let envLock = """
    {"running":true,"telemetry":{"enabled":false,"source":"env","disabled_by":"MCPPROXY_TELEMETRY=false"}}
    """

    // MARK: F-10

    func testLoadShowsTheConnectedCoresListenAddress() async {
        let store = makeStore(
            configJSON: #"{"listen":"127.0.0.1:18666"}"#,
            statusJSON: #"{"running":true,"listen_addr":"127.0.0.1:18666"}"#)
        await store.load()
        XCTAssertEqual(store.stringBinding("listen").wrappedValue, "127.0.0.1:18666")
        XCTAssertEqual(store.runningListenAddr, "127.0.0.1:18666")
    }

    func testBlankListenAdoptsTheRunningAddressWithoutDirtying() async {
        let store = makeStore(
            configJSON: #"{"quarantine_enabled":true}"#,
            statusJSON: #"{"running":true,"listen_addr":"127.0.0.1:18666"}"#)
        await store.load()
        XCTAssertEqual(store.stringBinding("listen").wrappedValue, "127.0.0.1:18666",
                       "never the catalogue placeholder")
        XCTAssertFalse(store.isDirty("listen"))
        XCTAssertTrue(store.dirtyKeys(in: SettingsCatalog.security).isEmpty,
                      "a Save must never PATCH an adopted listen")
    }

    func testListenNoteNamesTheConnectedCore() async throws {
        let store = makeStore(
            configJSON: #"{"listen":"127.0.0.1:18666"}"#,
            statusJSON: #"{"running":true,"listen_addr":"127.0.0.1:18666"}"#)
        await store.load()
        XCTAssertEqual(store.listenNote, "Connected core v0.1.0 is listening on 127.0.0.1:18666.")

        let pending = makeStore(
            configJSON: #"{"listen":"127.0.0.1:8080"}"#,
            statusJSON: #"{"running":true,"listen_addr":"127.0.0.1:18666"}"#)
        await pending.load()
        XCTAssertEqual(
            pending.listenNote,
            "Connected core v0.1.0 is listening on 127.0.0.1:18666. The saved address 127.0.0.1:8080 takes effect after a restart.")

        let unknown = makeStore(configJSON: #"{"listen":"127.0.0.1:8080"}"#, statusJSON: "{}")
        SettingsStubURLProtocol.bodies.removeValue(forKey: "/api/v1/status")
        await unknown.load()
        XCTAssertNil(unknown.listenNote, "no status, no claim about the connected core")
    }

    /// The Settings window outlives a core restart, so the note must follow the
    /// core now connected rather than the one the first load saw.
    func testListenNoteFollowsTheCoreAfterARestart() async {
        let store = makeStore(
            configJSON: #"{"listen":"127.0.0.1:9090"}"#,
            statusJSON: #"{"running":true,"listen_addr":"127.0.0.1:8080"}"#)
        await store.load()
        XCTAssertEqual(
            store.listenNote,
            "Connected core v0.1.0 is listening on 127.0.0.1:8080. The saved address 127.0.0.1:9090 takes effect after a restart.")

        // The core restarts on the saved address.
        SettingsStubURLProtocol.bodies["/api/v1/status"] =
            envelope(#"{"running":true,"listen_addr":"127.0.0.1:9090"}"#)
        await store.refreshStatus()
        XCTAssertEqual(store.runningListenAddr, "127.0.0.1:9090")
        XCTAssertEqual(store.listenNote, "Connected core v0.1.0 is listening on 127.0.0.1:9090.")
    }

    func testANewConnectionRefreshesTheStatusByItself() async throws {
        let appState = AppState()
        appState.apiClient = SettingsStubURLProtocol.makeClient()
        appState.version = "v0.1.0"
        let store = ConfigStore(appState: appState)
        SettingsStubURLProtocol.bodies = [
            "/api/v1/config": envelope(#"{"config":{"listen":"127.0.0.1:9090"}}"#),
            "/api/v1/status": envelope(#"{"running":true,"listen_addr":"127.0.0.1:8080"}"#),
        ]
        await store.load()
        XCTAssertEqual(store.runningListenAddr, "127.0.0.1:8080")

        SettingsStubURLProtocol.bodies["/api/v1/status"] =
            envelope(#"{"running":true,"listen_addr":"127.0.0.1:9090"}"#)
        appState.coreState = .connected  // a real state change bumps connectionGeneration

        for _ in 0..<100 where store.runningListenAddr != "127.0.0.1:9090" {
            try await Task.sleep(nanoseconds: 50_000_000)
        }
        XCTAssertEqual(store.runningListenAddr, "127.0.0.1:9090")
    }

    func testListenNoteOmitsAnEmptyVersion() async {
        let store = makeStore(
            configJSON: #"{"listen":"127.0.0.1:18666"}"#,
            statusJSON: #"{"running":true,"listen_addr":"127.0.0.1:18666"}"#,
            version: "")
        await store.load()
        XCTAssertEqual(store.listenNote, "Connected core is listening on 127.0.0.1:18666.")
    }

    // MARK: F-03

    func testTelemetryLockedByEnv() throws {
        let store = ConfigStore(appState: AppState())
        store.hydrate(from: ["telemetry": ["enabled": true]])
        XCTAssertTrue(store.boolBinding("telemetry.enabled").wrappedValue, "precondition: stored value is on")

        store.applyStatus(try status(envLock))

        XCTAssertEqual(
            store.lockReason("telemetry.enabled"),
            "Off — disabled by MCPPROXY_TELEMETRY=false in the environment. Unset it and restart MCPProxy to change this setting.")
        XCTAssertFalse(store.boolBinding("telemetry.enabled").wrappedValue)
        store.setValue("telemetry.enabled", true)
        store.boolBinding("telemetry.enabled").wrappedValue = true
        XCTAssertFalse(store.dirtyKeys(in: SettingsCatalog.general).contains("telemetry.enabled"))
        XCTAssertFalse(store.boolBinding("telemetry.enabled").wrappedValue)
    }

    func testConfigLevelTelemetryHasNoLock() throws {
        let store = ConfigStore(appState: AppState())
        store.hydrate(from: ["telemetry": ["enabled": true]])
        store.applyStatus(try status(#"{"running":true,"telemetry":{"enabled":true,"source":"config"}}"#))
        XCTAssertNil(store.lockReason("telemetry.enabled"))
        XCTAssertTrue(store.boolBinding("telemetry.enabled").wrappedValue)
        store.boolBinding("telemetry.enabled").wrappedValue = false
        XCTAssertTrue(store.isDirty("telemetry.enabled"))
    }

    func testTheLockClearsWhenTheEnvironmentOptOutGoesAway() throws {
        let store = ConfigStore(appState: AppState())
        store.hydrate(from: ["telemetry": ["enabled": true]])
        store.applyStatus(try status(envLock))
        XCTAssertNotNil(store.lockReason("telemetry.enabled"))
        store.applyStatus(try status(#"{"running":true,"telemetry":{"enabled":true,"source":"default"}}"#))
        XCTAssertNil(store.lockReason("telemetry.enabled"))
    }

    // MARK: view wiring (source-reading, as HomeRoutingTests does)

    func testFieldRowHonoursLocksAndRendersTheListenNote() throws {
        var url = URL(fileURLWithPath: #filePath)
        url.deleteLastPathComponent()
        url.deleteLastPathComponent()
        url.appendPathComponent("MCPProxy/Settings/ConfigSettingsView.swift")
        let source = try String(contentsOf: url, encoding: .utf8)
        XCTAssertTrue(source.contains("store.lockReason(field.key)"))
        XCTAssertTrue(source.contains(".disabled(store.lockReason(field.key) != nil)"))
        XCTAssertTrue(source.contains("setting-locked-"))
        XCTAssertTrue(source.contains("store.listenNote"))
        XCTAssertTrue(source.contains("setting-listen-running"))
    }
}
