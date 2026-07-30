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
// then take that core away — or, harder and more realistic, leave it listening
// while it stops answering.

import XCTest
@testable import MCPProxy

final class CoreLivenessTests: XCTestCase {

    private var stub: UnixSocketHTTPStub?
    private var manager: CoreProcessManager?

    /// Async teardown so shutdown actually completes before the next test runs:
    /// a fire-and-forget `Task { await shutdown() }` would race the next test's
    /// stub over sockets and background tasks.
    override func tearDown() async throws {
        if let manager {
            await manager.shutdown()
        }
        manager = nil
        stub?.stop()
        stub = nil
        try await super.tearDown()
    }

    /// Bring up a stub core and attach a manager to it.
    /// `maySpawn: false` guarantees the test can never launch a real binary.
    @discardableResult
    private func attachToStubCore(
        refreshInterval: TimeInterval = 60,
        probeTimeout: TimeInterval = 5.0,
        baseDelay: TimeInterval = 0.05,
        ready: ReadyBehaviour = ReadyBehaviour()
    ) async throws -> (CoreProcessManager, AppState, UnixSocketHTTPStub) {
        let stub = UnixSocketHTTPStub.healthyCore(ready: ready)
        try stub.start()
        self.stub = stub

        let appState = await MainActor.run { AppState() }
        let manager = CoreProcessManager(
            appState: appState,
            // Notification delivery needs a real app bundle; keep it off so the
            // core-exit path is exercisable without killing the test process.
            notificationService: NotificationService(deliveryEnabled: false),
            // Keep reconnect backoff short so the test is fast; the production
            // policy only changes how long the user waits, not the outcome.
            reconnectionPolicy: ReconnectionPolicy(
                baseDelay: baseDelay, maxDelay: baseDelay * 2, maxAttempts: 3, jitterFactor: 0.0
            ),
            socketPath: stub.path,
            refreshInterval: refreshInterval,
            probeTimeout: probeTimeout
        )
        self.manager = manager
        await manager.start(maySpawn: false)
        return (manager, appState, stub)
    }

    private func coreState(_ appState: AppState) async -> CoreState {
        await MainActor.run { appState.coreState }
    }

    /// Run a shell pipeline and read back an integer (process counting).
    private func shellCount(_ command: String) throws -> Int {
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/bin/sh")
        proc.arguments = ["-c", command]
        let pipe = Pipe()
        proc.standardOutput = pipe
        proc.standardError = FileHandle.nullDevice
        try proc.run()
        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        proc.waitUntilExit()
        let text = String(data: data, encoding: .utf8)?
            .trimmingCharacters(in: .whitespacesAndNewlines) ?? "0"
        return Int(text) ?? 0
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
        let (_, appState, _) = try await attachToStubCore()

        let state = await coreState(appState)
        XCTAssertEqual(state, .connected,
                       "the manager must attach to a live core on the injected socket")
        let ownership = await MainActor.run { appState.ownership }
        XCTAssertEqual(ownership, .externalAttached,
                       "a core we did not spawn must be marked external")
        let isStopped = await MainActor.run { appState.isStopped }
        XCTAssertFalse(isStopped)
    }

    /// Two managers on two sockets must not redirect each other. The socket path
    /// used to live in a mutable static, so constructing the second client
    /// silently repointed the first — a liveness detector reporting another
    /// core's health as its own.
    func testTwoManagersOnDifferentSocketsDoNotRedirectEachOther() async throws {
        let (_, firstState, firstStub) = try await attachToStubCore()

        let secondStub = UnixSocketHTTPStub.healthyCore()
        try secondStub.start()
        defer { secondStub.stop() }

        let secondState = await MainActor.run { AppState() }
        let secondManager = CoreProcessManager(
            appState: secondState,
            notificationService: NotificationService(deliveryEnabled: false),
            socketPath: secondStub.path,
            refreshInterval: 60
        )
        await secondManager.start(maySpawn: false)

        let connected = await coreState(secondState)
        XCTAssertEqual(connected, .connected)
        XCTAssertGreaterThan(secondStub.requestCount(path: "/api/v1/info"), 0,
                             "the second manager must talk to its OWN socket")

        // The first manager's client must still reach the first stub, not the
        // one created later.
        let firstProbesBefore = firstStub.requestCount(path: "/ready")
        let stillAlive = await self.manager?.coreIsAlive()
        XCTAssertEqual(stillAlive, true)
        XCTAssertEqual(firstStub.requestCount(path: "/ready"), firstProbesBefore + 1,
                       "the first manager's probe must still land on the first socket")
        let firstStillConnected = await coreState(firstState)
        XCTAssertEqual(firstStillConnected, .connected)

        await secondManager.shutdown()
    }

    // MARK: - GH #926: the core disappears

    /// The regression under test: kill an externally-attached core and the tray
    /// must stop reporting `.connected`. Before the fix this assertion failed —
    /// the state stayed `.connected` for as long as anyone cared to watch
    /// (the reporter measured 5.5 minutes).
    func testDeadExternalCoreLeavesConnectedState() async throws {
        let (_, appState, stub) = try await attachToStubCore(refreshInterval: 0.2)
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
        let (_, appState, stub) = try await attachToStubCore(refreshInterval: 0.2)

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

    // MARK: - GH #926: the core stops ANSWERING (socket still up)

    /// The half the probe actually exists for. The socket is connectable
    /// throughout, so a socket-existence-only implementation sees nothing wrong
    /// forever. Only a real `/ready` call notices.
    ///
    /// It also pins the hysteresis: ONE missed readiness check is not evidence
    /// of a dead core — a busy core can miss a sample — so the tray must not
    /// flip the icon on it. Two consecutive misses are.
    func testOneReadinessMissIsToleratedAndTwoAreNot() async throws {
        let ready = ReadyBehaviour()
        let (manager, appState, stub) = try await attachToStubCore(ready: ready)

        ready.current = .json(#"{"success":false,"error":"busy"}"#, status: 503)
        let probesBefore = stub.requestCount(path: "/ready")

        let afterFirstMiss = await manager.coreIsAlive()
        XCTAssertTrue(afterFirstMiss,
                      "a single readiness miss must not condemn a core whose socket is still up")
        let stillConnected = await coreState(appState)
        XCTAssertEqual(stillConnected, .connected)
        XCTAssertEqual(stub.requestCount(path: "/ready"), probesBefore + 1,
                       "the probe must actually call /ready — the socket alone proves nothing")

        let afterSecondMiss = await manager.coreIsAlive()
        XCTAssertFalse(afterSecondMiss,
                       "two consecutive readiness misses must be treated as core loss")
        let lost = await coreState(appState)
        XCTAssertNotEqual(lost, .connected,
                          "a listening-but-unresponsive core must leave the connected state")
    }

    /// A recovered core clears the strike count: misses must be CONSECUTIVE,
    /// otherwise a healthy core that blips once an hour eventually gets killed.
    func testAReadinessRecoveryResetsTheFailureCount() async throws {
        let ready = ReadyBehaviour()
        let (manager, appState, _) = try await attachToStubCore(ready: ready)

        ready.current = .json(#"{"success":false}"#, status: 503)
        _ = await manager.coreIsAlive()               // strike one
        ready.current = .json(#"{"success":true}"#)
        let recovered = await manager.coreIsAlive()
        XCTAssertTrue(recovered, "the core answered again")

        ready.current = .json(#"{"success":false}"#, status: 503)
        let afterIsolatedMiss = await manager.coreIsAlive()
        XCTAssertTrue(afterIsolatedMiss,
                      "an isolated miss after a recovery must not be strike two")
        let state = await coreState(appState)
        XCTAssertEqual(state, .connected)
    }

    /// A wedged core accepts the connection and never answers. The probe must
    /// give up on its own schedule instead of inheriting the API client's 30s
    /// request timeout — 30 seconds of blocking per sample makes the detector
    /// useless and stalls the refresh loop behind it.
    func testProbeGivesUpQuicklyOnAnUnresponsiveCore() async throws {
        let ready = ReadyBehaviour()
        let (manager, _, _) = try await attachToStubCore(probeTimeout: 0.3, ready: ready)

        ready.current = .hang(seconds: 5.0)

        let started = Date()
        let alive = await manager.coreIsAlive()
        let elapsed = Date().timeIntervalSince(started)

        XCTAssertTrue(alive, "first miss is tolerated (see hysteresis)")
        XCTAssertLessThan(elapsed, 2.0,
                          "the probe must use its own short timeout, not the 30s request timeout")
    }

    // MARK: - GH #926: one reconnection at a time

    /// Two detectors can see the same dead core at the same moment — the
    /// liveness tick and the SSE disconnect handler, or the tick and the
    /// process-exit handler. `attemptReconnection()` suspends, so two
    /// overlapping runs can each connect (and, for a core we own, each LAUNCH),
    /// overwrite `process`, and orphan one holding the BBolt lock.
    ///
    /// `connectToCore()` fetches `/api/v1/info` exactly once, so counting that
    /// request counts reconnections.
    func testConcurrentLossSignalsRunExactlyOneReconnection() async throws {
        let (manager, _, stub) = try await attachToStubCore(baseDelay: 0.2)
        let connectsAfterAttach = stub.requestCount(path: "/api/v1/info")
        XCTAssertEqual(connectsAfterAttach, 1, "precondition: one connect for the attach")

        // Four detectors fire at once.
        await withTaskGroup(of: Void.self) { group in
            for _ in 0..<4 {
                group.addTask { await manager.handleCoreLoss() }
            }
        }

        XCTAssertEqual(stub.requestCount(path: "/api/v1/info"), connectsAfterAttach + 1,
                       "concurrent loss signals must produce exactly ONE reconnection")
    }

    /// The process-exit handler is the second reconnection entry point. It must
    /// honour the same in-flight guard: a managed core dying while the liveness
    /// tick is already reconnecting must not start a second attempt.
    func testProcessExitDuringAReconnectionDoesNotStartASecond() async throws {
        let (manager, _, stub) = try await attachToStubCore(baseDelay: 2.0)
        let connectsAfterAttach = stub.requestCount(path: "/api/v1/info")

        // Start a reconnection and let it reach its backoff sleep (1s at
        // baseDelay 2.0), so it is provably still in flight below.
        let reconnecting = Task { await manager.handleCoreLoss() }
        try await Task.sleep(nanoseconds: 300_000_000)

        await manager.handleProcessExit(status: 1)
        await reconnecting.value

        // Give a second (wrongly started) reconnection time to land.
        try await Task.sleep(nanoseconds: 1_500_000_000)

        XCTAssertEqual(stub.requestCount(path: "/api/v1/info"), connectsAfterAttach + 1,
                       "a process exit during an in-flight reconnection must not start a second one")
    }

    // MARK: - Never destroy or duplicate a live core

    /// The most dangerous failure available here: a core that is UP but slow to
    /// answer `/ready` must not have its socket unlinked and a second core
    /// started against the same data directory. That is two writers on one
    /// BBolt database, not a UI glitch.
    ///
    /// Hysteresis does not help at startup — there is no established connection
    /// to be tolerant about — so the startup path has to reason from the socket
    /// itself: something is listening, therefore hands off.
    func testAnUnresponsiveCoreIsNeverUnlinkedOrReplaced() async throws {
        // If the code under test does try to spawn, spawn something harmless.
        let fakeCore = try FakeCoreBinary(behaviour: "exit 1")
        let restoreEnv = fakeCore.install()
        defer { restoreEnv() }

        let ready = ReadyBehaviour()
        ready.current = .json(#"{"success":false,"error":"still starting"}"#, status: 503)
        let stub = UnixSocketHTTPStub.healthyCore(ready: ready)
        try stub.start()
        self.stub = stub

        let appState = await MainActor.run { AppState() }
        let manager = CoreProcessManager(
            appState: appState,
            notificationService: NotificationService(deliveryEnabled: false),
            socketPath: stub.path,
            refreshInterval: 0.2,
            probeTimeout: 0.3
        )
        self.manager = manager

        await manager.start(maySpawn: true)

        XCTAssertTrue(FileManager.default.fileExists(atPath: stub.path),
                      "a socket a live core is listening on must never be unlinked")
        XCTAssertTrue(SocketTransport.isSocketAvailable(path: stub.path),
                      "the live core's socket must still be connectable")
        let launched = await manager.coresLaunched
        XCTAssertEqual(launched, 0,
                       "a competing core must not be started against the same data directory")
        XCTAssertNil(manager.managedProcess,
                     "no process may be launched while another core holds the socket")

        // And when it finishes starting up, we attach — no user action needed.
        ready.current = .json(#"{"success":true}"#)
        let state = await waitForState(appState, timeout: 10.0) { $0 == .connected }
        XCTAssertEqual(state, .connected,
                       "once the core answers, the tray must attach to it")
    }

    /// `retry()` kills the managed core and immediately calls `start()`, while
    /// that core's termination handler is on its way to starting a reconnection.
    /// The gate has to cover the SPAWN path, not just attach and reconnect, or
    /// both launch.
    func testStartStandsDownWhileAReconnectionIsAlreadyRunning() async throws {
        let fakeCore = try FakeCoreBinary(behaviour: "sleep 30")
        let restoreEnv = fakeCore.install()
        defer { restoreEnv() }

        let appState = await MainActor.run { AppState() }
        let manager = CoreProcessManager(
            appState: appState,
            notificationService: NotificationService(deliveryEnabled: false),
            // 2s base delay: the reconnection is provably still in its backoff
            // when start() runs below.
            reconnectionPolicy: ReconnectionPolicy(
                baseDelay: 2.0, maxDelay: 2.0, maxAttempts: 3, jitterFactor: 0.0
            ),
            socketPath: "/tmp/mcpproxy-test-\(UUID().uuidString.prefix(8)).sock",
            refreshInterval: 60
        )
        self.manager = manager

        let reconnecting = Task { await manager.handleProcessExit(status: 1) }
        try await Task.sleep(nanoseconds: 300_000_000)

        await manager.start(maySpawn: true)
        XCTAssertEqual(fakeCore.launchCount(), 0,
                       "start() must not launch a core while a reconnection is already running")

        reconnecting.cancel()
        _ = await reconnecting.value
    }

    /// Standing down on process exit must not eat the retry ladder. The ladder
    /// belongs to whoever holds the gate: it relaunches, sees its replacement
    /// fail, and climbs to the next rung itself instead of waiting for a
    /// termination callback that is (correctly) refusing to start a second
    /// reconnection.
    func testTheRelaunchLadderRunsEveryRung() async throws {
        let fakeCore = try FakeCoreBinary(behaviour: "exit 1")
        let restoreEnv = fakeCore.install()
        defer { restoreEnv() }

        let appState = await MainActor.run { AppState() }
        let manager = CoreProcessManager(
            appState: appState,
            notificationService: NotificationService(deliveryEnabled: false),
            reconnectionPolicy: ReconnectionPolicy(
                baseDelay: 0.02, maxDelay: 0.05, maxAttempts: 3, jitterFactor: 0.0
            ),
            socketPath: "/tmp/mcpproxy-test-\(UUID().uuidString.prefix(8)).sock",
            refreshInterval: 60
        )
        self.manager = manager

        // One launch from start(), then the ladder: maxRetries relaunches.
        await manager.start(maySpawn: true)

        let deadline = Date().addingTimeInterval(20)
        while await manager.coresLaunched < 4 && Date() < deadline {
            try await Task.sleep(nanoseconds: 100_000_000)
        }

        let launched = await manager.coresLaunched
        XCTAssertEqual(launched, 4,
                       "one initial launch plus three ladder rungs — no more, no fewer")
        let state = await waitForState(appState, timeout: 5.0) {
            $0 == .error(.maxRetriesExceeded)
        }
        XCTAssertEqual(state, .error(.maxRetriesExceeded),
                       "the ladder must run out honestly rather than stopping after one try")
    }

    /// A core that crashes, is relaunched, and then runs normally must not carry
    /// its old strikes: the next crash gets the full ladder again.
    func testASuccessfulRelaunchClearsTheLadder() async throws {
        let fakeCore = try FakeCoreBinary.failingThenHealthy(failures: 1)
        let restoreEnv = fakeCore.install()
        defer { restoreEnv() }

        let socketPath = "/tmp/mcpproxy-test-\(UUID().uuidString.prefix(8)).sock"
        let appState = await MainActor.run { AppState() }
        let manager = CoreProcessManager(
            appState: appState,
            notificationService: NotificationService(deliveryEnabled: false),
            reconnectionPolicy: ReconnectionPolicy(
                baseDelay: 0.02, maxDelay: 0.05, maxAttempts: 3, jitterFactor: 0.0
            ),
            socketPath: socketPath,
            refreshInterval: 60
        )
        self.manager = manager

        // Rung 1 fails; rung 2 launches a core that stays up.
        let reconnecting = Task { await manager.handleProcessExit(status: 1) }

        // The second launch is the one that survives — give it its socket, as a
        // real core would create.
        let deadline = Date().addingTimeInterval(20)
        while fakeCore.launchCount() < 2 && Date() < deadline {
            try await Task.sleep(nanoseconds: 50_000_000)
        }
        XCTAssertGreaterThanOrEqual(fakeCore.launchCount(), 2, "precondition: rung 1 failed, rung 2 launched")

        let stub = UnixSocketHTTPStub.healthyCore(at: socketPath)
        try stub.start()
        self.stub = stub

        _ = await reconnecting.value

        let state = await coreState(appState)
        XCTAssertEqual(state, .connected, "the relaunched core came up and we connected to it")
        let remainingStrikes = await manager.retryCount
        XCTAssertEqual(remainingStrikes, 0,
                       "a successful relaunch must clear the ladder, or a later crash gets no retries")
    }

    /// The routing hint rides on `httpAdditionalHeaders`, which Foundation
    /// attaches to EVERY request the session makes — including ones this
    /// protocol declines to intercept, and ones following a redirect. So it must
    /// carry an opaque token rather than the socket path (which contains the
    /// user's home directory), and it must never reach the wire.
    func testTheRouteHintNeverLeaksThePathOrTheHeader() async throws {
        let (_, _, stub) = try await attachToStubCore()

        let head = stub.lastRequestHead() ?? ""
        XCTAssertFalse(head.isEmpty, "precondition: the stub saw a request")
        XCTAssertFalse(head.lowercased().contains(SocketURLProtocol.routeHeader.lowercased()),
                       "the transport routing header must be stripped before the wire")
        XCTAssertFalse(head.contains(stub.path),
                       "the socket path must never appear in an outgoing request")

        let token = SocketURLProtocol.makeRoute(to: stub.path)
        XCTAssertNotEqual(token, stub.path,
                          "the header value must be an opaque token, not the path itself")
        XCTAssertEqual(SocketURLProtocol.routes(for: token), stub.path,
                       "and it must resolve back to the socket it was minted for")
    }

    // MARK: - The property: never two cores on one data directory

    /// The errno classification must be honoured by STARTUP, not only by the
    /// liveness tick. A socket that refuses connections — a full listen queue on
    /// a live core looks exactly like this — must not lead to unlinking the
    /// socket and spawning over whatever is behind it.
    func testAnIndeterminateSocketAtStartupNeitherUnlinksNorSpawns() async throws {
        let fakeCore = try FakeCoreBinary(behaviour: "exit 1")
        let restoreEnv = fakeCore.install()
        defer { restoreEnv() }

        // A socket file that refuses connections. From out here this is
        // indistinguishable from a live core with a full backlog.
        let stub = UnixSocketHTTPStub.healthyCore()
        try stub.start()
        stub.stop(unlinkSocket: false)
        defer { unlink(stub.path) }
        XCTAssertEqual(SocketTransport.probeSocket(path: stub.path), .refused,
                       "precondition: the socket refuses connections")

        let appState = await MainActor.run { AppState() }
        let manager = CoreProcessManager(
            appState: appState,
            notificationService: NotificationService(deliveryEnabled: false),
            socketPath: stub.path,
            refreshInterval: 60,
            probeTimeout: 0.3,
            unresponsiveCoreTimeout: 0.5
        )
        self.manager = manager

        await manager.start(maySpawn: true)

        let launched = await manager.coresLaunched
        XCTAssertEqual(launched, 0,
                       "a socket we cannot classify must not be spawned over")
        XCTAssertNil(manager.managedProcess)

        // ...and the user is told, rather than left staring at a healthy icon.
        let state = await waitForState(appState, timeout: 5.0) {
            if case .error = $0 { return true }
            return false
        }
        guard case .error = state else {
            return XCTFail("an unusable core must become an actionable error, got \(state)")
        }
    }

    /// A rung that gives up on a launch must terminate and reap it. A core that
    /// hung without ever becoming ready is still a core; leaving it running
    /// while the next rung launches makes the ladder a core factory.
    func testAbandonedLaunchAttemptsAreReapedBeforeTheNextRung() async throws {
        // Stays alive but never creates a socket: waitForSocket gives up on it
        // while the process is still running.
        let fakeCore = try FakeCoreBinary(behaviour: "sleep 30")
        let restoreEnv = fakeCore.install()
        defer { restoreEnv() }

        let appState = await MainActor.run { AppState() }
        let manager = CoreProcessManager(
            appState: appState,
            notificationService: NotificationService(deliveryEnabled: false),
            reconnectionPolicy: ReconnectionPolicy(
                baseDelay: 0.02, maxDelay: 0.05, maxAttempts: 3, jitterFactor: 0.0
            ),
            socketPath: "/tmp/mcpproxy-test-\(UUID().uuidString.prefix(8)).sock",
            refreshInterval: 60,
            probeTimeout: 0.3,
            unresponsiveCoreTimeout: 0.5,
            // Give up on a socket that never appears after a second, so the
            // ladder actually climbs during the test.
            socketWaitTimeout: 1.0
        )
        self.manager = manager

        let launching = Task { await manager.start(maySpawn: true) }

        // Sample while the ladder is climbing: by now at least two rungs have
        // been tried, and each abandoned attempt is still sleeping unless it was
        // reaped.
        var worstCase = 0
        let deadline = Date().addingTimeInterval(6)
        while Date() < deadline {
            worstCase = max(worstCase, try shellCount("pgrep -f '\(fakeCore.path)' | wc -l"))
            if await manager.coresLaunched >= 3 { break }
            try await Task.sleep(nanoseconds: 200_000_000)
        }

        let climbed = await manager.coresLaunched
        XCTAssertGreaterThanOrEqual(climbed, 2,
                                    "precondition: the ladder climbed past its first rung")
        XCTAssertLessThanOrEqual(worstCase, 1,
                                 "at most one launched core may be alive at any moment — "
                                 + "an abandoned attempt must be terminated and reaped")

        launching.cancel()
        _ = await launching.value
        let leftovers = try shellCount("pgrep -f '\(fakeCore.path)' | wc -l")
        XCTAssertEqual(leftovers, 0, "no launched core may outlive the ladder")
    }

    /// Stop must be able to cancel work already in flight. A ladder asleep in
    /// its backoff will otherwise wake up and launch a core after the user
    /// pressed Stop, leaving the tray showing "Stopped" while owning a process.
    func testShutdownCancelsAnInFlightLaunchLadder() async throws {
        let fakeCore = try FakeCoreBinary(behaviour: "exit 1")
        let restoreEnv = fakeCore.install()
        defer { restoreEnv() }

        let appState = await MainActor.run { AppState() }
        let manager = CoreProcessManager(
            appState: appState,
            notificationService: NotificationService(deliveryEnabled: false),
            // 2s backoff: the ladder is provably asleep when we shut down.
            reconnectionPolicy: ReconnectionPolicy(
                baseDelay: 2.0, maxDelay: 2.0, maxAttempts: 3, jitterFactor: 0.0
            ),
            socketPath: "/tmp/mcpproxy-test-\(UUID().uuidString.prefix(8)).sock",
            refreshInterval: 60
        )
        self.manager = manager

        let launching = Task { await manager.start(maySpawn: true) }
        // Wait for the first launch to happen and fail, putting the ladder to sleep.
        let deadline = Date().addingTimeInterval(10)
        while await manager.coresLaunched < 1 && Date() < deadline {
            try await Task.sleep(nanoseconds: 50_000_000)
        }
        let beforeShutdown = await manager.coresLaunched
        XCTAssertEqual(beforeShutdown, 1, "precondition: one launch, ladder now in backoff")

        await manager.shutdown()
        let afterShutdown = await manager.coresLaunched

        // Well past the 2s backoff the ladder was sleeping through.
        try await Task.sleep(nanoseconds: 3_000_000_000)
        let finalCount = await manager.coresLaunched
        XCTAssertEqual(finalCount, afterShutdown,
                       "a ladder must not wake up and launch a core after shutdown")

        let state = await coreState(appState)
        XCTAssertEqual(state, .idle)
        XCTAssertNil(manager.managedProcess,
                     "the tray must not say Stopped while owning a running process")

        launching.cancel()
        _ = await launching.value
    }

    /// The choke point: every path that wants a core goes through `launchCore`,
    /// so the invariant is enforced there rather than depending on six callers
    /// staying disciplined. With a core listening on the socket, no path may
    /// produce a second one.
    func testNoPathSpawnsWhileACoreIsListening() async throws {
        let fakeCore = try FakeCoreBinary(behaviour: "sleep 30")
        let restoreEnv = fakeCore.install()
        defer { restoreEnv() }

        // A core that is UP but sick: it holds the socket and never answers
        // /ready. This is the dangerous shape — connectToCore() fails, so every
        // recovery path is tempted to launch a replacement on top of it.
        let ready = ReadyBehaviour()
        ready.current = .json(#"{"success":false,"error":"wedged"}"#, status: 503)
        let stub = UnixSocketHTTPStub.healthyCore(ready: ready)
        try stub.start()
        self.stub = stub

        let appState = await MainActor.run { AppState() }
        // As if this core were one WE launched — the ownership under which the
        // recovery paths are allowed to relaunch.
        await MainActor.run { appState.ownership = .trayManaged }

        let manager = CoreProcessManager(
            appState: appState,
            notificationService: NotificationService(deliveryEnabled: false),
            reconnectionPolicy: ReconnectionPolicy(
                baseDelay: 0.02, maxDelay: 0.05, maxAttempts: 3, jitterFactor: 0.0
            ),
            socketPath: stub.path,
            refreshInterval: 60,
            probeTimeout: 0.3,
            unresponsiveCoreTimeout: 0.5
        )
        self.manager = manager

        // Every entry point that can reach a spawn.
        await manager.start(maySpawn: true)
        await manager.handleProcessExit(status: 1)
        await manager.retry()
        await manager.handleCoreLoss()

        let launched = await manager.coresLaunched
        XCTAssertEqual(launched, 0,
                       "no entry point may spawn a core while one is listening on the socket")
        XCTAssertNil(manager.managedProcess)
        XCTAssertTrue(FileManager.default.fileExists(atPath: stub.path),
                      "and none of them may unlink its socket")
        XCTAssertEqual(SocketTransport.probeSocket(path: stub.path), .connectable,
                       "the core that was there must still be there")
    }

    // MARK: - What a failed socket connect does and does not prove

    /// `isSocketAvailable` says false for three different situations, and only
    /// one of them means "the core is gone".
    func testSocketProbeDistinguishesAbsentFromRefused() throws {
        let missing = "/tmp/mcpproxy-test-\(UUID().uuidString.prefix(8)).sock"
        XCTAssertEqual(SocketTransport.probeSocket(path: missing), .absent,
                       "no socket file at all: a running core always owns its socket")

        let stub = UnixSocketHTTPStub.healthyCore()
        try stub.start()
        self.stub = stub
        XCTAssertEqual(SocketTransport.probeSocket(path: stub.path), .connectable)

        // Leave the file behind with nothing listening — what `kill -9` leaves,
        // and also what a full listen queue looks like from out here.
        stub.stop(unlinkSocket: false)
        XCTAssertEqual(SocketTransport.probeSocket(path: stub.path), .refused)
        unlink(stub.path)
    }

    /// A refused connection is ambiguous — a stale socket from a killed process
    /// and a live core with a full listen backlog are indistinguishable — so it
    /// gets the same tolerance as a missed readiness check, not an instant
    /// verdict.
    func testARefusedSocketIsToleratedOnceLikeAReadinessMiss() async throws {
        let (manager, appState, stub) = try await attachToStubCore()

        stub.stop(unlinkSocket: false)
        defer { unlink(stub.path) }
        XCTAssertEqual(SocketTransport.probeSocket(path: stub.path), .refused,
                       "precondition: the socket file is still there, refusing connections")

        let afterFirst = await manager.coreIsAlive()
        XCTAssertTrue(afterFirst,
                      "one refused connection could be a full listen queue on a healthy core")
        let stillConnected = await coreState(appState)
        XCTAssertEqual(stillConnected, .connected)

        let afterSecond = await manager.coreIsAlive()
        XCTAssertFalse(afterSecond, "two in a row is enough")
    }

    /// The attach path is the third entry point. A manual retry racing the idle
    /// attach watcher used to run two `attachToExternalCore()` calls, each
    /// replacing the other's clients and tasks — and a late failure from one
    /// could overwrite the other's successful `.connected` with `.error`.
    func testConcurrentAttachesAttachOnce() async throws {
        let stub = UnixSocketHTTPStub.healthyCore()
        try stub.start()
        self.stub = stub

        let appState = await MainActor.run { AppState() }
        let manager = CoreProcessManager(
            appState: appState,
            notificationService: NotificationService(deliveryEnabled: false),
            socketPath: stub.path,
            refreshInterval: 60
        )
        self.manager = manager

        await withTaskGroup(of: Void.self) { group in
            for _ in 0..<4 {
                group.addTask { await manager.start(maySpawn: false) }
            }
        }

        XCTAssertEqual(stub.requestCount(path: "/api/v1/info"), 1,
                       "concurrent attaches must produce exactly ONE attachment")
        let state = await coreState(appState)
        XCTAssertEqual(state, .connected,
                       "a concurrent attach must not leave the tray idle or errored")
    }

    // MARK: - The choke point's preflight, tested directly

    /// The four checks in front of `proc.run()` are what keeps two cores off one
    /// data directory. The tests above reach them through whole entry points
    /// (start/retry/handleProcessExit), which proves the paths but leaves the
    /// checks themselves only indirectly covered — a reordering or a dropped
    /// guard could still pass them. These call the preflight directly.
    private func makeManager(
        socketPath: String,
        appState: AppState
    ) -> CoreProcessManager {
        let manager = CoreProcessManager(
            appState: appState,
            notificationService: NotificationService(deliveryEnabled: false),
            socketPath: socketPath,
            refreshInterval: 60,
            probeTimeout: 0.3
        )
        self.manager = manager
        return manager
    }

    /// Run `body` with the connection gate held, exactly as every real caller of
    /// `launchCore` does.
    private func withConnectionGate(
        _ manager: CoreProcessManager,
        _ body: (CoreProcessManager) async -> Void
    ) async {
        let took = await manager.beginConnectionWork()
        XCTAssertTrue(took, "precondition: the gate was free")
        await body(manager)
        await manager.endConnectionWork()
    }

    /// The check that matters most: something is listening, so there is nothing
    /// to launch — whatever the caller believes about its own state.
    func testPreflightRefusesToLaunchWhileSomethingIsListening() async throws {
        let stub = UnixSocketHTTPStub.healthyCore()
        try stub.start()
        self.stub = stub

        let appState = await MainActor.run { AppState() }
        let manager = makeManager(socketPath: stub.path, appState: appState)

        await withConnectionGate(manager) { manager in
            do {
                try await manager.preflightLaunch()
                XCTFail("a connectable socket must never be launched over")
            } catch {
                XCTAssertEqual(error as? CoreError,
                               .general("A core is already running on \(stub.path)"))
            }
        }
    }

    /// A core we already own is still a core. The process check comes before the
    /// socket check on purpose: a managed core that has not yet created its
    /// socket would otherwise pass the preflight and get a sibling.
    func testPreflightRefusesToLaunchWhileAManagedProcessIsRunning() async throws {
        let fakeCore = try FakeCoreBinary(behaviour: "sleep 30")
        let restoreEnv = fakeCore.install()
        defer { restoreEnv() }

        let socketPath = "/tmp/mcpproxy-test-\(UUID().uuidString.prefix(8)).sock"
        let appState = await MainActor.run { AppState() }
        let manager = makeManager(socketPath: socketPath, appState: appState)

        // Let the tray spawn its core, then give that core its socket — as a
        // real one would — so the launch completes and the process survives.
        let starting = Task { await manager.start(maySpawn: true) }
        let deadline = Date().addingTimeInterval(10)
        while fakeCore.launchCount() < 1 && Date() < deadline {
            try await Task.sleep(nanoseconds: 50_000_000)
        }
        let stub = UnixSocketHTTPStub.healthyCore(at: socketPath)
        try stub.start()
        self.stub = stub
        await starting.value

        let running = try XCTUnwrap(manager.managedProcess, "precondition: the tray owns a core")
        XCTAssertTrue(running.isRunning, "precondition: that core is still up")

        await withConnectionGate(manager) { manager in
            do {
                try await manager.preflightLaunch()
                XCTFail("a running managed core must never get a sibling")
            } catch {
                XCTAssertEqual(
                    error as? CoreError,
                    .general("internal error: tried to launch a core while "
                             + "PID \(running.processIdentifier) is still running")
                )
            }
        }
    }

    /// The other half of the same coin, and the reason the tray no longer
    /// unlinks anything: a socket FILE that nothing accepts on must not block a
    /// launch. The core we spawn cleans that file up itself.
    func testPreflightAllowsALaunchOverASocketFileNobodyAnswers() async throws {
        let stub = UnixSocketHTTPStub.healthyCore()
        try stub.start()
        stub.stop(unlinkSocket: false)
        defer { unlink(stub.path) }
        XCTAssertTrue(FileManager.default.fileExists(atPath: stub.path),
                      "precondition: the socket file is still on disk")
        XCTAssertEqual(SocketTransport.probeSocket(path: stub.path), .refused,
                       "precondition: nothing accepts on it")

        let appState = await MainActor.run { AppState() }
        let manager = makeManager(socketPath: stub.path, appState: appState)

        await withConnectionGate(manager) { manager in
            do {
                try await manager.preflightLaunch()
            } catch {
                XCTFail("a stale socket file must not be mistaken for a live core — threw \(error)")
            }
        }
    }

    /// Without the gate there is no serialisation, so the preflight's own
    /// answer would be stale before `proc.run()`. It refuses instead.
    func testPreflightRefusesToLaunchWithoutTheConnectionGate() async throws {
        let socketPath = "/tmp/mcpproxy-test-\(UUID().uuidString.prefix(8)).sock"
        let appState = await MainActor.run { AppState() }
        let manager = makeManager(socketPath: socketPath, appState: appState)

        do {
            try await manager.preflightLaunch()
            XCTFail("a launch outside the connection gate must be refused")
        } catch {
            XCTAssertEqual(
                error as? CoreError,
                .general("internal error: tried to launch a core without the connection gate")
            )
        }
    }

    /// The confirmation window exists for exactly this: a core that binds the
    /// socket while we are still deciding whether the socket is empty. One
    /// sample would have missed it and spawned a second writer.
    func testConfirmationNoticesACoreThatAppearsBetweenProbes() async throws {
        let socketPath = "/tmp/mcpproxy-test-\(UUID().uuidString.prefix(8)).sock"
        let appState = await MainActor.run { AppState() }
        let manager = makeManager(socketPath: socketPath, appState: appState)
        XCTAssertEqual(SocketTransport.probeSocket(path: socketPath), .absent,
                       "precondition: the first probe sees nothing at all")

        // Start the confirmation first, then bind the socket underneath it —
        // the same order the race happens in.
        let confirming = Task { await manager.confirmNothingIsListening(attempts: 6, interval: 0.1) }
        try await Task.sleep(nanoseconds: 150_000_000)
        let stub = UnixSocketHTTPStub.healthyCore(at: socketPath)
        try stub.start()
        self.stub = stub

        let empty = await confirming.value
        XCTAssertFalse(empty,
                       "a core that binds mid-confirmation must be seen — the whole point "
                       + "of confirming over time rather than on one sample")
    }
}
