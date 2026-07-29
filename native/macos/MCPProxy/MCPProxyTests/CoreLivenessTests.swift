// CoreLivenessTests.swift
// MCPProxyTests
//
// GH #926 — "menu bar icon never detects externally managed core stopping".
//
// When the tray ATTACHES to a core it did not spawn, there is no `Process` and
// therefore no `terminationHandler` — which was the only thing in the app that
// ever noticed a dead core. The SSE client retries forever without ever
// finishing its stream, and every periodic refresh swallows its errors, so
// `coreState` could never leave `.connected`: the menu bar icon kept claiming
// the core was healthy indefinitely.
//
// These tests drive the real attach path against a stub core on a Unix socket,
// then take that core away.

import XCTest
@testable import MCPProxy

final class CoreLivenessTests: XCTestCase {

    private var stub: UnixSocketHTTPStub?

    override func tearDown() {
        stub?.stop()
        stub = nil
        // Leave the shared URLProtocol override pointing at nothing, so no later
        // test can accidentally route a request into the developer's real core.
        SocketURLProtocol.overrideSocketPath = "/nonexistent/mcpproxy-tests.sock"
        super.tearDown()
    }

    /// Bring up a stub core and attach a manager to it.
    /// `maySpawn: false` guarantees the test can never launch a real binary.
    private func attachToStubCore(
        refreshInterval: TimeInterval
    ) async throws -> (CoreProcessManager, AppState, UnixSocketHTTPStub) {
        let stub = UnixSocketHTTPStub.healthyCore()
        try stub.start()
        self.stub = stub

        let appState = await MainActor.run { AppState() }
        let manager = CoreProcessManager(
            appState: appState,
            notificationService: NotificationService(),
            // Keep reconnect backoff short so the test is fast; the production
            // policy only changes how long the user waits, not the outcome.
            reconnectionPolicy: ReconnectionPolicy(
                baseDelay: 0.05, maxDelay: 0.1, maxAttempts: 3, jitterFactor: 0.0
            ),
            socketPath: stub.path,
            refreshInterval: refreshInterval
        )
        await manager.start(maySpawn: false)
        return (manager, appState, stub)
    }

    private func coreState(_ appState: AppState) async -> CoreState {
        await MainActor.run { appState.coreState }
    }

    /// Poll until `predicate` holds or the deadline passes. Returns the last state.
    private func waitForState(
        _ appState: AppState,
        timeout: TimeInterval,
        until predicate: @escaping (CoreState) -> Bool
    ) async -> CoreState {
        let deadline = Date().addingTimeInterval(timeout)
        var last = await coreState(appState)
        while Date() < deadline {
            if predicate(last) { return last }
            try? await Task.sleep(nanoseconds: 50_000_000) // 50ms
            last = await coreState(appState)
        }
        return last
    }

    // MARK: - The seam itself

    /// Baseline: with an injected socket path the manager attaches to a core it
    /// did not spawn, exactly as it does against `~/.mcpproxy/mcpproxy.sock`.
    /// Everything below depends on this being a faithful attach.
    func testAttachesToAnExternalCoreOverAnInjectedSocket() async throws {
        let (manager, appState, _) = try await attachToStubCore(refreshInterval: 60)
        defer { Task { await manager.shutdown() } }

        let state = await coreState(appState)
        XCTAssertEqual(state, .connected,
                       "the manager must attach to a live core on the injected socket")
        let ownership = await MainActor.run { appState.ownership }
        XCTAssertEqual(ownership, .externalAttached,
                       "a core we did not spawn must be marked external")
        let isStopped = await MainActor.run { appState.isStopped }
        XCTAssertFalse(isStopped)
    }

    // MARK: - GH #926

    /// The regression under test: kill an externally-attached core and the tray
    /// must stop reporting `.connected`. Before the fix this assertion failed —
    /// the state stayed `.connected` for as long as anyone cared to watch
    /// (the reporter measured 5.5 minutes).
    func testDeadExternalCoreLeavesConnectedState() async throws {
        let (manager, appState, stub) = try await attachToStubCore(refreshInterval: 0.2)
        defer { Task { await manager.shutdown() } }
        let attached = await coreState(appState)
        XCTAssertEqual(attached, .connected, "precondition: attached")

        // The core dies: socket closed and removed, as after `kill -9`.
        stub.stop()

        let state = await waitForState(appState, timeout: 5.0) { $0 != .connected }
        XCTAssertNotEqual(state, .connected,
                          "the tray must notice an externally-managed core that stopped")

        // And it must land somewhere the tray icon can render as "not fine" —
        // never silently back in `.connected`.
        let settled = await waitForState(appState, timeout: 5.0) {
            if case .error = $0 { return true }
            return false
        }
        XCTAssertEqual(settled, .error(.general("External core process is no longer available")),
                       "a core we do not own and cannot find must surface as an error")
    }

    /// The tray must not spawn a replacement for a core it never owned, and must
    /// pick the core back up by itself when it returns (launchd/brew restart).
    func testReattachesWhenTheExternalCoreComesBack() async throws {
        let (manager, appState, stub) = try await attachToStubCore(refreshInterval: 0.2)
        defer { Task { await manager.shutdown() } }

        stub.stop()
        let lost = await waitForState(appState, timeout: 5.0) {
            if case .error = $0 { return true }
            return false
        }
        guard case .error = lost else {
            return XCTFail("precondition: the tray must first notice the core is gone, got \(lost)")
        }

        let ownership = await MainActor.run { appState.ownership }
        XCTAssertEqual(ownership, .externalAttached,
                       "losing an external core must not make the tray claim ownership")

        // The core comes back on the same socket.
        let revived = UnixSocketHTTPStub.healthyCore(at: stub.path)
        try revived.start()
        self.stub = revived

        let state = await waitForState(appState, timeout: 10.0) { $0 == .connected }
        XCTAssertEqual(state, .connected, "the tray must re-attach when the core returns")
    }
}
