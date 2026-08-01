import XCTest
@testable import MCPProxy

/// A live core whose listen backlog is saturated must not be launched over
/// (GH #933).
///
/// `onlyEverRefused` — refusals with nothing ever accepted during the episode —
/// was taken as proof that a socket file is stale. It is sound for a crashed
/// core and wrong for a live one that was already saturated when the episode
/// began: it refuses every probe too, never records an acceptance, and after the
/// 60-second deadline the tray escalated to a launch. The escalation's own
/// confirmation window and the launch preflight see only refusals as well, so
/// nothing vetoed it. Reproduced live on real processes (`SIGSTOP` a core,
/// fill the backlog past `kern.ipc.somaxconn`): the tray launched four doomed
/// cores in 39 seconds, each dying on bbolt's lock, and ended on a
/// retry-exhaustion error about a core that was alive and healthy.
///
/// `CoreLivenessTests.testACoreThatAcceptedEarlierInTheWindowIsNeverEscalatedOver`
/// covers the easier half — a core that accepted once and then stopped. The
/// uncovered case, and the one here, is a core that refuses from the very first
/// probe.
final class SaturatedCoreEscalationTests: XCTestCase {

    private var manager: CoreProcessManager?
    private var directory: URL!
    private var heldDescriptor: Int32 = -1

    override func setUpWithError() throws {
        // /tmp, not NSTemporaryDirectory(): the socket bound below has to fit
        // in sun_path's 104 bytes, and /var/folders/… already spends half of it.
        directory = URL(fileURLWithPath: "/tmp/mcpproxy-sat-\(UUID().uuidString.prefix(8))",
                        isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
    }

    override func tearDown() async throws {
        if let manager { await manager.shutdown() }
        manager = nil
        if heldDescriptor >= 0 {
            flock(heldDescriptor, LOCK_UN)
            close(heldDescriptor)
            heldDescriptor = -1
        }
        try? FileManager.default.removeItem(at: directory)
        try await super.tearDown()
    }

    /// The regression itself. Every probe refuses from the start, so the socket
    /// evidence says "stale" — but a live process holds the data directory, and
    /// that is a fact no dead core can fake (the kernel drops `flock` when a
    /// process dies). The tray must keep waiting instead of spawning.
    func testASaturatedButLiveCoreIsNeverLaunchedOver() async throws {
        let fakeCore = try FakeCoreBinary(behaviour: "sleep 30")
        let restoreEnv = fakeCore.install()
        defer { restoreEnv() }

        let socketPath = try refusingSocket()
        holdTheDataDirectoryLock()

        let appState = await MainActor.run { AppState() }
        let manager = makeManager(appState: appState, socketPath: socketPath)
        self.manager = manager

        await manager.start(maySpawn: true)

        // Well past the deadline and its confirmation window, and past several
        // attach-watch probes.
        try await Task.sleep(nanoseconds: 3_000_000_000)

        let launched = await manager.coresLaunched
        XCTAssertEqual(launched, 0,
                       "a live process holds the data directory: that core is there, "
                       + "whatever its listen queue is doing")
        XCTAssertEqual(fakeCore.launchCount(), 0)
        XCTAssertNil(manager.managedProcess)

        // …and the tray stays in the state whose attach watch will pick the
        // core up the moment it drains, rather than dead-ending on an error
        // about a process that is perfectly healthy.
        let state = await MainActor.run { appState.coreState }
        XCTAssertEqual(state, .waitingForCore)
    }

    /// The other half of the same rule: a socket file with NO live holder is
    /// still escalated over, exactly as before. Without this the fix would have
    /// traded a rare bad launch for a permanent deadlock on every stale socket —
    /// nothing else in the system removes that file.
    func testAGenuinelyStaleSocketIsStillLaunchedOver() async throws {
        let fakeCore = try FakeCoreBinary(behaviour: "sleep 30")
        let restoreEnv = fakeCore.install()
        defer { restoreEnv() }

        let socketPath = try refusingSocket()
        // A database exists beside it — a real data directory — but nobody
        // holds it, because the core that did is gone.
        FileManager.default.createFile(
            atPath: directory.appendingPathComponent("config.db").path, contents: Data()
        )

        let appState = await MainActor.run { AppState() }
        let manager = makeManager(appState: appState, socketPath: socketPath)
        self.manager = manager

        await manager.start(maySpawn: true)
        try await Task.sleep(nanoseconds: 3_000_000_000)

        let launched = await manager.coresLaunched
        XCTAssertGreaterThan(launched, 0,
                             "an unheld data directory beside a refusing socket is a dead "
                             + "core's leftovers; refusing to launch would deadlock startup")
    }

    /// The saturated core then dies — SIGKILL, jetsam, `kill -9` — leaving its
    /// socket file behind. The lock is gone the instant the process is (the
    /// kernel drops `flock`), so the very next deadline must see a stale socket
    /// and escalate.
    ///
    /// The regression this pins: the "a live process holds it" verdict was
    /// latched for the lifetime of the episode and the lock was only ever
    /// re-probed while the evidence still LOOKED stale — which that same verdict
    /// made impossible. Every subsequent deadline re-armed itself on a fact that
    /// stopped being true minutes ago, and `.waitingForCore` offers neither Stop
    /// nor Retry, so quitting the app was the only way out. Before this change
    /// the same sequence recovered at the first deadline.
    func testACoreThatDiesAfterTheFirstDeadlineIsEventuallyEscalatedOver() async throws {
        let fakeCore = try FakeCoreBinary(behaviour: "sleep 30")
        let restoreEnv = fakeCore.install()
        defer { restoreEnv() }

        let socketPath = try refusingSocket()
        holdTheDataDirectoryLock()

        let appState = await MainActor.run { AppState() }
        let manager = makeManager(appState: appState, socketPath: socketPath)
        self.manager = manager

        await manager.start(maySpawn: true)

        // Past the first deadline: the lock is held, so nothing is launched.
        try await Task.sleep(nanoseconds: 1_200_000_000)
        var launched = await manager.coresLaunched
        XCTAssertEqual(launched, 0, "precondition: a held lock still parks the tray")

        // The core dies. Its socket file survives it; its lock cannot.
        releaseTheDataDirectoryLock()

        try await Task.sleep(nanoseconds: 3_000_000_000)
        launched = await manager.coresLaunched
        XCTAssertGreaterThan(launched, 0,
                             "nothing holds the data directory any more — that socket is a "
                             + "dead core's leftovers and the tray has to get past it on its own")
    }

    // MARK: - Fixtures

    private func makeManager(appState: AppState, socketPath: String) -> CoreProcessManager {
        CoreProcessManager(
            appState: appState,
            notificationService: NotificationService(deliveryEnabled: false),
            reconnectionPolicy: ReconnectionPolicy(
                baseDelay: 0.05, maxDelay: 0.1, maxAttempts: 3, jitterFactor: 0.0
            ),
            socketPath: socketPath,
            refreshInterval: 60,
            probeTimeout: 0.3,
            unresponsiveCoreTimeout: 0.5,
            socketWaitTimeout: 1.0
        )
    }

    /// A socket file that refuses every connection — what a saturated backlog
    /// and a dead core's leftovers look like from the outside, identically.
    private func refusingSocket() throws -> String {
        let stub = UnixSocketHTTPStub.healthyCore(
            at: directory.appendingPathComponent("mcpproxy.sock").path
        )
        try stub.start()
        stub.stop(unlinkSocket: false)
        let path = stub.path
        guard SocketTransport.probeSocket(path: path) == .refused else {
            throw XCTSkip("precondition: the socket must refuse every probe")
        }
        return path
    }

    /// Hold the lock a running core holds: bbolt takes it on the data directory
    /// before it ever creates a listener. `flock` is per open file description,
    /// so this conflicts with the probe even from inside this process.
    private func holdTheDataDirectoryLock() {
        let path = directory.appendingPathComponent("config.db").path
        FileManager.default.createFile(atPath: path, contents: Data())
        heldDescriptor = open(path, O_RDONLY)
        XCTAssertGreaterThanOrEqual(heldDescriptor, 0)
        XCTAssertEqual(flock(heldDescriptor, LOCK_EX | LOCK_NB), 0,
                       "precondition: this test holds the lock a live core would hold")
    }

    /// What the kernel does when the holding process dies.
    private func releaseTheDataDirectoryLock() {
        guard heldDescriptor >= 0 else { return }
        flock(heldDescriptor, LOCK_UN)
        close(heldDescriptor)
        heldDescriptor = -1
    }
}
