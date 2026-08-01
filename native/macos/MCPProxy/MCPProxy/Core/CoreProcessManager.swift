// CoreProcessManager.swift
// MCPProxy
//
// Manages the lifecycle of the mcpproxy core process: launching, monitoring,
// SSE event streaming, state refresh, and graceful shutdown.
//
// The manager is an actor to ensure all state mutations are serialized.

import Foundation

/// How often idle mode polls the socket for a core to attach to (GH #410).
/// Cheap: a file-exists check plus, only when that passes, one `/ready` call.
private let attachWatchInterval: TimeInterval = 2.0

// MARK: - Core Process Manager

/// Actor responsible for the full lifecycle of the mcpproxy core subprocess.
///
/// Lifecycle flow:
/// 1. Resolve the bundled binary (inside .app or on PATH)
/// 2. Launch the core process with `serve` arguments
/// 3. Poll the Unix socket until the core is ready
/// 4. Connect the APIClient and SSEClient
/// 5. Stream SSE events and periodically refresh state
/// 6. Handle process exit, errors, and reconnection
/// 7. Graceful shutdown with SIGTERM, escalating to SIGKILL
actor CoreProcessManager {

    // MARK: - Properties

    /// Exposed for synchronous termination in applicationWillTerminate.
    /// Safe to read from any isolation context since Process is thread-safe for terminate().
    nonisolated(unsafe) var managedProcess: Process?

    private var process: Process? {
        didSet { managedProcess = process }
    }
    private let appState: AppState
    /// Exposed for menu actions (enable/disable/restart/login servers).
    private(set) var apiClient: APIClient?

    /// Non-isolated accessor for menu action dispatch.
    nonisolated var apiClientForActions: APIClient? {
        // Safe because APIClient is an actor — all its methods are isolated
        get async { await apiClient }
    }
    private var sseClient: (any SSEStreaming)?
    private var sseTask: Task<Void, Never>?
    private var refreshTask: Task<Void, Never>?
    /// Poll for an external core while core autostart is off (GH #410).
    private var attachWatchTask: Task<Void, Never>?

    /// Set when this manager has been replaced by a newer one. A superseded
    /// manager must never publish to the shared AppState again: the app creates a
    /// FRESH CoreProcessManager on an explicit "Start MCPProxy Core", and the old
    /// one may still be inside its idle attach-watch. Without this, the old
    /// manager could observe the socket the NEW manager's core just created and
    /// mislabel that tray-spawned core as `.externalAttached` — after which the
    /// tray would refuse to stop it and leak it on quit.
    private var superseded: Bool = false

    /// True while an operation that establishes a connection to a core is
    /// running: an attach, or a reconnection.
    ///
    /// Every such operation SUSPENDS (probe, backoff sleep, connect, relaunch),
    /// and there are four things that can start one — the liveness tick, the SSE
    /// disconnect handler, the process-exit handler, and the attach watcher
    /// racing a manual retry. Without a gate, two can run at once: two
    /// `connectToCore()` calls replacing each other's clients and tasks, a late
    /// failure from one overwriting the other's `.connected` with `.error`, or —
    /// for a core we own — two `launchAndConnect()` calls that each spawn a core
    /// and orphan one holding the BBolt lock.
    ///
    /// Acquire it ONLY through `beginConnectionWork()`, which is synchronous and
    /// therefore atomic under actor isolation. A check followed by an `await`
    /// followed by a set guards nothing: another invocation runs during the
    /// suspension and passes the same check.
    private var connectionWorkInFlight: Bool = false
    /// Rungs of the relaunch ladder used so far. Readable for tests, which need
    /// to see that a successful relaunch clears it.
    private(set) var retryCount: Int = 0
    private let maxRetries: Int = 3
    private let notificationService: NotificationService
    private let reconnectionPolicy: ReconnectionPolicy

    /// Captured stderr output from the core process for error diagnostics.
    private var stderrBuffer: String = ""

    /// The resolved path to the mcpproxy binary.
    private var coreBinaryPath: String?

    /// API key generated for this session's core communication.
    /// API key for the current session. Exposed for Web UI URL construction.
    private(set) var sessionAPIKey: String?

    /// Non-isolated accessor for the API key (for menu actions).
    nonisolated var currentAPIKey: String? {
        get async { await sessionAPIKey }
    }

    /// Socket path for the core process.
    private let socketPath: String

    /// The bbolt database whose lock answers "is a core alive here?" when the
    /// socket has stopped answering (GH #933). Derived from `socketPath`.
    private let dataDirectoryLockPath: String

    /// How often the periodic refresh tick runs. Injectable so tests can drive
    /// the tick — and the liveness probe on it — without waiting 30 seconds.
    private let refreshInterval: TimeInterval

    /// Per-probe timeout. A liveness probe must fail fast: it runs on the
    /// refresh tick and everything behind it waits. The API client's own 30s
    /// request timeout is right for a real call and far too long here — it
    /// would let one slow sample stall the detector for a whole tick.
    ///
    /// 5s: three orders of magnitude above a healthy socket round-trip
    /// (sub-millisecond) and the same order as the core's own advertised SSE
    /// retry interval, so a core that is merely busy still gets to answer.
    private let probeTimeout: TimeInterval

    /// Consecutive missed readiness checks required before the tray concludes a
    /// still-listening core is lost.
    ///
    /// 2, not 1: readiness is a sample, and one miss is not evidence — a
    /// saturated core, a GC pause, or a laptop resuming from sleep can all eat
    /// one. It is not higher because each strike costs a full refresh interval,
    /// and 2 already bounds worst-case detection at ~1 minute for this case.
    /// The unambiguous case (socket gone) is not subject to the budget at all.
    private let probeFailureBudget: Int = 2

    /// Missed readiness checks since the last successful one. Reset on any
    /// success, so the budget counts CONSECUTIVE misses — otherwise a healthy
    /// core that blips once an hour would eventually be declared dead.
    private var consecutiveProbeFailures: Int = 0

    /// Short-timeout client used only for liveness probes.
    private var probeClient: APIClient?

    /// Set by `shutdown()` before it does anything else, and never cleared: a
    /// manager is single-use, like `superseded`.
    ///
    /// Setting `.idle` is not enough to stop work already in flight. A ladder
    /// sitting in its backoff sleep will wake up and launch a core after the
    /// user pressed Stop, leaving the tray showing "Stopped" while owning a
    /// running process — worse than showing an error, because nothing invites
    /// the user to look. Every step that can lead to a spawn checks this.
    private var shutdownRequested: Bool = false

    /// How many core processes this manager has actually started.
    ///
    /// The authoritative count for the invariant "never two cores on one data
    /// directory": it is incremented inside the one function that can create a
    /// process, so it cannot be fooled by a launched process that dies before it
    /// records itself. (An earlier version of these tests counted lines written
    /// by the launched script and reported ZERO while four cores were being
    /// spawned and reaped.)
    private(set) var coresLaunched: Int = 0

    /// Which launch a termination callback belongs to. A process we gave up on
    /// and reaped must not, when its callback finally lands, drive a relaunch.
    private var launchGeneration: Int = 0

    /// Fires when a core has been holding the socket without answering for too
    /// long, turning an indefinite wait into something the user can act on.
    private var unresponsiveDeadlineTask: Task<Void, Never>?

    /// What the socket probes of the CURRENT connection episode have shown.
    ///
    /// The wait path deliberately refuses to act on a single refused probe. That
    /// leaves one question open at the deadline: is this a live core we must keep
    /// our hands off, or a file a dead one left behind? Nobody else can answer
    /// it — the tray no longer unlinks anything, and the core's own
    /// `cleanupStaleSocket` (internal/server/listener_unix.go) does not run until
    /// a core is launched — so a stale file would deadlock startup forever.
    /// Accumulating the probes turns "one sample" into evidence.
    private var socketEvidence = SocketEvidence()

    /// Whether the wait we are currently in is allowed to end in a spawn. Carried
    /// from `start(maySpawn:)` so the escalation below cannot become a back door
    /// around the launch policy (#410) sixty seconds after it was applied.
    private var waitMaySpawn: Bool = false

    /// How long to wait for a core that holds the socket but will not answer
    /// before saying so. Matches `waitForSocket`'s 60s budget for a core we
    /// launched ourselves: a real core can be slow to start (Docker pulls, a
    /// large config) and this must not undercut that, but waiting forever is
    /// safe for the database and unusable for the person.
    private let unresponsiveCoreTimeout: TimeInterval

    /// How long a core we launched has to create its socket before the attempt
    /// is abandoned (and the process reaped). Injectable so a test can exercise
    /// the ladder without waiting a real minute per rung.
    private let socketWaitTimeout: TimeInterval

    // MARK: - Initialization

    /// - Parameters:
    ///   - socketPath: Path of the core's Unix socket. Defaults to
    ///     `~/.mcpproxy/mcpproxy.sock`. Injectable so a test (or a second app
    ///     instance) can be pointed at an isolated core instead of contending
    ///     with the user's live one — without it, nothing about attach mode is
    ///     testable, because attaching IS "probe this socket" (GH #926).
    ///   - refreshInterval: Period of the background refresh/liveness tick.
    ///   - probeTimeout: How long a single liveness probe may take before it
    ///     counts as a miss. See `probeTimeout` above.
    init(
        appState: AppState,
        notificationService: NotificationService,
        reconnectionPolicy: ReconnectionPolicy = .default,
        socketPath: String? = nil,
        refreshInterval: TimeInterval = 30.0,
        probeTimeout: TimeInterval = 5.0,
        unresponsiveCoreTimeout: TimeInterval = 60.0,
        socketWaitTimeout: TimeInterval = 60.0
    ) {
        self.appState = appState
        self.notificationService = notificationService
        self.reconnectionPolicy = reconnectionPolicy
        self.refreshInterval = refreshInterval
        self.probeTimeout = probeTimeout
        self.unresponsiveCoreTimeout = unresponsiveCoreTimeout
        self.socketWaitTimeout = socketWaitTimeout

        // `~/.mcpproxy/mcpproxy.sock`, or wherever this instance has been
        // relocated to (GH #936).
        let resolvedSocket = socketPath ?? InstancePaths.socketPath
        self.socketPath = resolvedSocket
        // The database of the core that owns THAT socket — see
        // `DataDirectoryLock.path(forSocket:)` for why it is derived from the
        // socket rather than from this instance's own root.
        self.dataDirectoryLockPath = DataDirectoryLock.path(forSocket: resolvedSocket)
    }

    // MARK: - Public API

    /// Start the core process and connect to it.
    ///
    /// Strategy: prefer a core that is ALREADY running — if the socket exists,
    /// probe it with a real API call and attach on success. Otherwise (a stale
    /// socket from a killed process fails that probe) launch our own.
    ///
    /// - Parameter maySpawn: whether the tray is permitted to launch a core
    ///   (GH #410). Attaching to a running core is never gated by this — only
    ///   spawning is. When spawning is refused and no core is running, the tray
    ///   goes idle and watches for one to appear, so a core the user starts
    ///   later (CLI, launchd, brew services) is picked up without a restart.
    func start(maySpawn: Bool = true) async {
        // The gate covers the WHOLE of start, spawn included. `retry()` kills the
        // managed process and then calls this; that process's termination
        // handler is on its way to starting a reconnection at the same time.
        // Without the gate around the spawn, both launch — two cores on one data
        // directory.
        guard !shutdownRequested else { return }
        guard beginConnectionWork() else {
            NSLog("[MCPProxy] start: another connection attempt is already running")
            return
        }
        defer { endConnectionWork() }

        // A fresh attempt reasons from fresh probes: whatever the socket did
        // during an earlier episode says nothing about this one.
        beginSocketEvidence()

        switch await attachIfCoreIsRunningLocked() {
        case .attached:
            return
        case .inFlight:
            // Cannot happen while we hold the gate, but treat it as "someone
            // else owns this" rather than as an invitation to spawn.
            return
        case .unusable(let reason):
            // Something is, or may be, on the other end of that socket. Do NOT
            // unlink it and do NOT start a competing core against the same data
            // directory. Wait instead — the attach watch re-probes and attaches
            // the moment it answers, and a deadline turns "waiting" into
            // something the user can act on if it never does.
            await waitForTheCoreThatIsAlreadyThere(reason: reason, maySpawn: maySpawn)
            return
        case .noCore:
            break
        }

        guard maySpawn else {
            await awaitExternalCore()
            return
        }

        // Nothing answered — but "nothing answered once" is not proof. Confirm
        // over a short window before doing anything destructive: a live core
        // will accept at least one connection, a stale socket file will refuse
        // every one.
        guard await confirmNothingIsListening() else {
            await waitForTheCoreThatIsAlreadyThere(
                reason: "something is listening on the socket", maySpawn: maySpawn
            )
            return
        }
        guard !shutdownRequested else { return }

        // Confirmed empty — and the tray does NOT unlink the socket file here.
        // A socket file left over from a dead core never reaches this line: it
        // probes `.refused`, which `attachIfCoreIsRunningLocked()` reports as
        // `.unusable` above, sending us down the wait path. So the only file
        // that could be here is one that appeared AFTER the probes — i.e. one
        // something just bound — and unlinking that strands a live core.
        //
        // Cleaning up a genuinely stale file is the core's job anyway: it
        // removes one it can prove nobody is listening on (see
        // `cleanupStaleSocket` in internal/server/listener_unix.go) and refuses
        // to start when the socket really is in use.

        // Launch our own core as a subprocess
        await MainActor.run {
            appState.isStopped = false
            appState.ownership = .trayManaged
        }
        await launchWithRetries()
    }

    /// A core we cannot use is holding the socket. Wait for it rather than
    /// fighting it — but not forever (see `unresponsiveCoreTimeout`).
    ///
    /// - Parameter maySpawn: whether this wait is allowed to end in a launch if
    ///   the deadline expires having never seen anything accept a connection.
    private func waitForTheCoreThatIsAlreadyThere(reason: String, maySpawn: Bool) async {
        NSLog("[MCPProxy] Not taking over %@: %@ — waiting", socketPath, reason)
        waitMaySpawn = maySpawn
        await MainActor.run { appState.isStopped = false }
        await transitionState(to: .waitingForCore)
        startAttachWatch()
        armUnresponsiveCoreDeadline(reason: reason)
    }

    /// Retire this manager: stop watching and never touch AppState again. Called
    /// before the app swaps in a replacement manager.
    func supersede() {
        superseded = true
        cancelAttachWatch()
        cancelUnresponsiveCoreDeadline()
    }

    /// Whether `shutdown()` has been called. Read by background loops so they
    /// stop instead of waking up into a spawn.
    var isShuttingDown: Bool { shutdownRequested }

    // MARK: - Private: Connection-work gate

    /// Claim the right to establish a connection. Synchronous on purpose: under
    /// actor isolation a check-and-set with no `await` between them cannot be
    /// interleaved, which is exactly what makes this a guard rather than a hint.
    ///
    /// Internal rather than private only so tests can take the gate before
    /// calling `preflightLaunch()` — its first check is that the gate is held.
    func beginConnectionWork() -> Bool {
        guard !connectionWorkInFlight else { return false }
        connectionWorkInFlight = true
        return true
    }

    func endConnectionWork() {
        connectionWorkInFlight = false
    }

    // MARK: - Is a core alive at this socket, and how sure are we?

    /// One classification of the socket probe, shared by everything that asks.
    /// Startup and the attach watcher consult `assessCore()` itself; the
    /// liveness tick switches on `probeSocket()` directly because it counts
    /// strikes per case, but it applies the SAME mapping — absent = act now,
    /// localFailure = not evidence either way, refused = a strike, connectable =
    /// go ask `/ready`.
    ///
    /// That mapping used to differ between the two: the tick classified errno
    /// carefully while startup flattened everything that was not connectable
    /// into "no core", so a full listen backlog or `EMFILE` at startup still led
    /// to unlinking a live core's socket and spawning over it.
    enum CoreLiveness: Equatable {
        /// Socket connectable AND `/ready` answered. A core is definitely there.
        case alive
        /// Connectable, but no answer. Something IS listening — never take its
        /// socket.
        case listeningNoAnswer
        /// We cannot tell: refused connection (stale socket file OR a live core
        /// with a full listen queue) or a local failure (EMFILE/ENOMEM/EACCES).
        /// Never act destructively on this without confirmation over time.
        case indeterminate(String)
        /// No socket file at all. A running core always owns its socket file.
        case gone

        /// Whether it is categorically unsafe to take this socket over.
        var somethingIsListening: Bool {
            switch self {
            case .alive, .listeningNoAnswer: return true
            case .indeterminate, .gone: return false
            }
        }
    }

    /// Everything the probes have said about this socket since the current
    /// connection episode began. Deliberately conservative: it can only ever
    /// conclude "nothing has EVER been there", never "something is there".
    struct SocketEvidence: Equatable {
        /// Probes that found a socket file and were refused by it.
        private(set) var refusals: Int = 0

        /// Whether any probe got a connection accepted. One is enough, and it is
        /// never forgotten within the episode: a core that answered a minute ago
        /// and refuses now (a full listen backlog looks exactly like a stale
        /// file) is a core, and taking its socket is two writers on one database.
        private(set) var somethingAccepted: Bool = false

        /// Whether a live process was found holding the data directory this
        /// socket belongs to (GH #933).
        ///
        /// Separate from `somethingAccepted` because it is a different KIND of
        /// evidence — it does not come from the socket at all — and because it
        /// is the only thing that can speak for a core that has refused every
        /// probe since the episode began. `flock` is released by the kernel on
        /// process death, so unlike a leftover socket file it cannot be stale.
        private(set) var dataDirectoryHeld: Bool = false

        mutating func record(_ lock: DataDirectoryLock) {
            if case .heldByALiveProcess = lock { dataDirectoryHeld = true }
        }

        mutating func record(_ probe: SocketTransport.SocketProbe) {
            switch probe {
            case .connectable:
                somethingAccepted = true
            case .refused:
                refusals += 1
            case .absent:
                // No file to be wrong about. Not evidence of a live core, and
                // not counted as a refusal either.
                break
            case .localFailure:
                // Descriptor exhaustion, ENOMEM, EACCES: our problem, not the
                // core's. It says nothing about the other end, so it can neither
                // prove staleness nor disprove it.
                break
            }
        }

        /// A socket FILE exists, not one connection has ever been accepted on
        /// it, and no live process holds the data directory behind it. The only
        /// reading of that which is consistent with everything we have seen is:
        /// the process that created it is gone.
        ///
        /// The lock clause is what separates a dead core's leftovers from a
        /// live core with a saturated listen backlog — the two are identical to
        /// every socket probe there is (GH #933).
        var onlyEverRefused: Bool {
            refusals > 0 && !somethingAccepted && !dataDirectoryHeld
        }
    }

    /// Fold one probe into the episode's evidence. Called from the two places
    /// that probe on behalf of a decision — the liveness assessment and the
    /// confirmation window — so the deadline reasons over every sample, not just
    /// the last one.
    private func record(probe: SocketTransport.SocketProbe) {
        socketEvidence.record(probe)
    }

    /// Forget what earlier episodes saw. A core that was alive before it died
    /// must not leave `somethingAccepted` behind to veto the recovery of the
    /// socket file it left on disk.
    private func beginSocketEvidence() {
        socketEvidence = SocketEvidence()
    }

    /// Probe the socket, then (only if something answered the connect) ask
    /// `/ready`. The single source of truth for core presence.
    private func assessCore() async -> CoreLiveness {
        let probe = SocketTransport.probeSocket(path: socketPath)
        record(probe: probe)
        switch probe {
        case .absent:
            return .gone
        case .localFailure(let code):
            return .indeterminate("liveness probe could not run (errno \(code))")
        case .refused:
            return .indeterminate("socket refused the connection")
        case .connectable:
            return await probeIsReady() ? .alive : .listeningNoAnswer
        }
    }

    /// Confirm over time that nothing is listening, before doing anything
    /// destructive (unlinking the socket, spawning a core).
    ///
    /// A single failed connect cannot distinguish a stale socket file from a
    /// live core whose listen queue is momentarily full — but a core that is
    /// really there will accept at least one connection across several attempts,
    /// and a stale file will refuse every one. This is the only honest way to
    /// turn `.indeterminate` into a decision, and being wrong here means two
    /// writers on one database.
    ///
    /// Internal rather than private so a test can start a listener between two
    /// of its probes and pin that it notices.
    ///
    /// - Parameter afterEachProbe: called with the index of each probe once its
    ///   result is in. The seam exists for the test of the race this function is
    ///   here to win: to exercise it the listener must bind strictly BETWEEN two
    ///   probes, and a test that starts a task and sleeps cannot establish that
    ///   ordering — if the task runs late its first probe already sees the
    ///   listener and the race is never run. Awaiting here lets a test hold the
    ///   confirmation at a known probe while it binds. Nil in production.
    func confirmNothingIsListening(
        attempts: Int = 3,
        interval: TimeInterval = 0.3,
        afterEachProbe: (@Sendable (Int) async -> Void)? = nil
    ) async -> Bool {
        for attempt in 0..<attempts {
            if attempt > 0 {
                do {
                    try await Task.sleep(nanoseconds: UInt64(interval * 1_000_000_000))
                } catch {
                    return false // cancelled — assume the worst and do nothing
                }
            }
            if shutdownRequested { return false }
            let probe = SocketTransport.probeSocket(path: socketPath)
            record(probe: probe)
            await afterEachProbe?(attempt)
            if probe == .connectable {
                NSLog("[MCPProxy] Something answered on %@ — not taking the socket over", socketPath)
                return false
            }
        }
        return true
    }

    /// What happened when we looked for a core to attach to.
    ///
    /// Three of these are emphatically NOT `noCore`, and conflating them is how
    /// a tray ends up destroying a running core:
    /// - `inFlight`: another task is already attaching. Spawning or going idle
    ///   would fight the attach under way.
    /// - `unusable`: something is, or may be, on the other end of that socket.
    ///   Never unlink it and never spawn against it — that is a second writer on
    ///   one BBolt database.
    enum AttachOutcome: Equatable {
        case attached
        case inFlight
        case unusable(reason: String)
        case noCore
    }

    /// Attach to a core that is already up, taking the connection gate.
    /// Used by the attach watcher, which is not already holding it.
    private func attachIfCoreIsRunningGated() async -> AttachOutcome {
        guard beginConnectionWork() else { return .inFlight }
        defer { endConnectionWork() }
        return await attachIfCoreIsRunningLocked()
    }

    /// Attach to a core that is already up. Caller MUST hold the connection gate.
    private func attachIfCoreIsRunningLocked() async -> AttachOutcome {
        guard !superseded, !shutdownRequested else { return .noCore }

        switch await assessCore() {
        case .alive:
            guard !superseded, !shutdownRequested else { return .noCore }
            await attachToExternalCore()
            return .attached
        case .listeningNoAnswer:
            return .unusable(reason: "a core is listening but did not answer")
        case .indeterminate(let why):
            // Cannot tell whether a core is there. The caller must not treat
            // this as an empty socket.
            return .unusable(reason: why)
        case .gone:
            return .noCore
        }
    }

    /// Idle mode (#410): no core, and we are not allowed to start one. Sit in the
    /// stopped state and poll for a core to attach to.
    private func awaitExternalCore() async {
        NSLog("[MCPProxy] Core autostart is off — idle, watching for an external core")
        await MainActor.run {
            appState.isStopped = true
            appState.coreState = .idle
        }
        startAttachWatch()
    }

    /// Poll the socket until a core shows up, then attach to it.
    private func startAttachWatch() {
        attachWatchTask?.cancel()
        attachWatchTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: UInt64(attachWatchInterval * 1_000_000_000))
                guard !Task.isCancelled, let self else { return }
                guard await !self.isShuttingDown else { return }
                // Keep polling while another task is mid-attach: if that attach
                // fails, this watch is what picks the core up next time round.
                if case .attached = await self.attachIfCoreIsRunningGated() { return }
            }
        }
    }

    /// Stop watching for an external core (we are starting or shutting down).
    ///
    /// MUST only be called from OUTSIDE the watch task (shutdown, supersede,
    /// spawn). Calling it from within the attach path cancels the task that is
    /// performing the attach, so the connect it awaits fails with
    /// CancellationError — see attachToExternalCore, which drops the reference
    /// instead of cancelling.
    private func cancelAttachWatch() {
        attachWatchTask?.cancel()
        attachWatchTask = nil
    }

    /// Gracefully shut down the core process and all connections.
    ///
    /// A core we merely ATTACHED to is left running: we disconnect from it and
    /// stop there. That rule is now enforced by the ownership check below rather
    /// than by the fact that `process` happens to be nil for an attached core.
    func shutdown() async {
        // FIRST, before any suspension: stop work that is already in flight.
        // A ladder asleep in its backoff, an attach watch mid-probe, or a
        // reconnection waiting to retry will all otherwise wake up after this
        // returns and launch a core the user just asked us to stop — leaving the
        // tray showing "Stopped" while owning a running process. Never cleared:
        // a manager is single-use after shutdown.
        shutdownRequested = true

        await transitionState(to: .shuttingDown)
        cancelAttachWatch()
        cancelUnresponsiveCoreDeadline()

        // Disconnect SSE
        sseTask?.cancel()
        sseTask = nil
        refreshTask?.cancel()
        refreshTask = nil

        if let sseClient {
            await sseClient.disconnect()
        }
        sseClient = nil
        apiClient = nil
        probeClient = nil
        consecutiveProbeFailures = 0
        await MainActor.run { appState.apiClient = nil }

        let ownsCore = await MainActor.run { appState.ownership.shouldTerminateOnShutdown }
        guard ownsCore else {
            NSLog("[MCPProxy] Disconnected from an externally-managed core — leaving it running")
            self.process = nil
            await transitionState(to: .idle)
            return
        }

        // Terminate the process if we own it
        if let process, process.isRunning {
            // SIGINT first: Go handles it gracefully.
            process.interrupt()
        }
        await terminateManagedProcess(reason: "shutdown")

        // A launch may have been in flight when we set the flag: it checks the
        // flag before running the binary, but if it got past that check we sweep
        // it up here rather than leaving an orphan behind a "Stopped" icon.
        if let stray = self.process, stray.isRunning {
            await terminateManagedProcess(reason: "shutdown sweep")
        }

        await transitionState(to: .idle)
    }

    /// Retry after an error.
    ///
    /// This must not become a back door around the launch policy (#410). If the
    /// core we lost was an EXTERNAL one and the user has core autostart off,
    /// retrying re-attaches or returns to idle — it does not silently spawn a
    /// tray-managed core the user asked us not to start. A core we own is
    /// relaunched as before.
    func retry() async {
        retryCount = 0
        stderrBuffer = ""

        // Clean up any existing process. Goes through the reaper so the exit
        // callback for the core WE just killed is recognised as belonging to an
        // abandoned launch — otherwise it lands as "the core crashed" and races
        // the start() below for the right to launch a replacement.
        await terminateManagedProcess(reason: "retry requested")

        let ownership = await MainActor.run { appState.ownership }
        let maySpawn = CoreLaunchPolicy.retryMaySpawn(
            ownership: ownership,
            policyAllowsSpawn: CoreLaunchPolicy().maySpawnCore
        )
        await start(maySpawn: maySpawn)
    }

    // MARK: - Private: Attach to External Core

    /// Attach to an already-running core process on the socket.
    private func attachToExternalCore() async {
        // Drop the watch WITHOUT cancelling it. This attach is usually running
        // INSIDE the watch task (that is how the core was noticed), so cancelling
        // here would cancel the very work we are about to await: connectToCore()
        // would throw CancellationError and the tray would sit in
        // "Failed to connect to external core: cancelled" while a perfectly
        // healthy core was running. The watch loop exits on its own the moment
        // attachIfCoreIsRunning() reports success.
        attachWatchTask = nil
        cancelUnresponsiveCoreDeadline()
        await MainActor.run {
            appState.ownership = .externalAttached
            // A core IS running, so we are not in the stopped state — even if we
            // were forbidden from starting one ourselves (#410 idle mode).
            appState.isStopped = false
        }
        await transitionState(to: .waitingForCore)

        do {
            try await connectToCore()
            await transitionState(to: .connected)
            await refreshState()
            startSSEStream()
            startPeriodicRefresh()
        } catch {
            await transitionState(
                to: .error(.general("Failed to connect to external core: \(error.localizedDescription)"))
            )
        }
    }

    // MARK: - Private: Launch and Connect

    /// One launch attempt. THROWS on failure instead of publishing a terminal
    /// error, so a caller running the retry ladder can decide whether there is
    /// another rung. On success the ladder is cleared: a core that came back and
    /// is running normally must not carry old strikes into its next crash.
    private func launchAndConnectOnce() async throws {
        await transitionState(to: .launching)

        // Resolve the core binary
        let binaryPath = try resolveBinary()
        coreBinaryPath = binaryPath

        // Launch the process (core uses its own config API key)
        try await launchCore(binaryPath: binaryPath)

        // Wait for the socket to become available
        await transitionState(to: .waitingForCore)
        try await waitForSocket(timeout: socketWaitTimeout)

        // Connect API and SSE clients
        try await connectToCore()

        await transitionState(to: .connected)
        retryCount = 0
        await refreshState()
        startSSEStream()
        startPeriodicRefresh()
    }

    /// Terminate and REAP a process we launched, and return only once it is
    /// gone. Bumping the generation first means its termination callback — which
    /// lands asynchronously — is recognised as belonging to a launch we have
    /// already abandoned, so it cannot drive a relaunch.
    private func terminateManagedProcess(reason: String) async {
        guard let proc = process else { return }
        launchGeneration += 1
        process = nil

        guard proc.isRunning else { return }
        NSLog("[MCPProxy] Terminating PID %d (%@)", proc.processIdentifier, reason)
        AppLifecycle.shared.recordCoreTerminated(pid: proc.processIdentifier, reason: reason)
        kill(proc.processIdentifier, SIGTERM)

        let deadline = Date().addingTimeInterval(5.0)
        while proc.isRunning && Date() < deadline {
            try? await Task.sleep(nanoseconds: 100_000_000)
        }
        if proc.isRunning {
            NSLog("[MCPProxy] PID %d ignored SIGTERM — SIGKILL", proc.processIdentifier)
            kill(proc.processIdentifier, SIGKILL)
            proc.waitUntilExit()
        }
    }

    /// Start the clock on a core that holds the socket but will not answer.
    ///
    /// Refusing to fight it is right for the database and, on its own, unusable
    /// for the person: `.waitingForCore` offers neither Stop nor Retry, so a
    /// permanently silent listener would leave quitting the app as the only
    /// move. The deadline turns it into an error the menu can act on — while the
    /// attach watch keeps running underneath, so a core that does eventually
    /// answer is still picked up without the user doing anything.
    private func armUnresponsiveCoreDeadline(reason: String) {
        unresponsiveDeadlineTask?.cancel()
        let timeout = unresponsiveCoreTimeout
        unresponsiveDeadlineTask = Task { [weak self] in
            try? await Task.sleep(nanoseconds: UInt64(timeout * 1_000_000_000))
            guard !Task.isCancelled, let self else { return }
            await self.reportUnresponsiveCore(reason: reason)
        }
    }

    private func cancelUnresponsiveCoreDeadline() {
        unresponsiveDeadlineTask?.cancel()
        unresponsiveDeadlineTask = nil
    }

    /// Still waiting when the deadline expired. Either a core really is there and
    /// silent — say so — or nothing ever was, and the wait is a deadlock we have
    /// to break ourselves.
    private func reportUnresponsiveCore(reason: String) async {
        guard !superseded, !shutdownRequested else { return }
        guard case .waitingForCore = await MainActor.run(body: { appState.coreState }) else { return }

        // This task IS the deadline; drop the handle before doing anything that
        // might cancel it, or we would cancel ourselves mid-launch.
        unresponsiveDeadlineTask = nil

        // Before concluding "stale", ask the one question the socket cannot
        // answer: is a live process holding this core's data directory? A
        // saturated core refuses every probe exactly as a dead core's leftover
        // file does, and only the lock tells them apart (GH #933).
        if socketEvidence.onlyEverRefused {
            socketEvidence.record(DataDirectoryLock.probe(path: dataDirectoryLockPath))
        }
        if socketEvidence.dataDirectoryHeld {
            // Keep waiting rather than launching or erroring: the attach watch
            // is still running underneath, so the core is picked up the moment
            // it drains its backlog — which is what the tray failed to do when
            // this was reproduced live, staying wedged on an error for 75
            // seconds while the core answered /ready normally.
            NSLog("[MCPProxy] %@ refuses every probe, but a live process holds %@ — "
                  + "a busy core, not a stale socket. Waiting again (%@)",
                  socketPath, dataDirectoryLockPath, reason)
            armUnresponsiveCoreDeadline(reason: reason)
            return
        }

        if socketEvidence.onlyEverRefused, await escalateToLaunchOverAStaleSocket(reason: reason) {
            return
        }

        NSLog("[MCPProxy] Giving up waiting for the core on %@ (%@)", socketPath, reason)
        await transitionState(to: .error(.general(
            "A core is holding \(socketPath) but is not responding (\(reason)). "
            + "Quit that process, then use Retry."
        )))
    }

    /// The deadline expired and not one connection has been accepted on this
    /// socket in the whole window: a file is on disk and the process that made it
    /// is gone. Launch a core rather than telling the user to quit a process that
    /// does not exist — the error we would otherwise show is unactionable, and
    /// Retry would repeat this same wait forever, because nothing else in the
    /// system removes that file.
    ///
    /// Why this cannot resurrect the double-spawn hazard the tray-side unlink
    /// had. It never unlinks anything: the dangerous step is done by the core we
    /// spawn, which dials the socket immediately before removing it (a far
    /// tighter window than anything the tray can manage) and fails with "socket
    /// is in use by another process" when that dial succeeds — see
    /// `cleanupStaleSocket` in internal/server/listener_unix.go. Everything in
    /// front of that is unchanged: the connection gate is taken here, the
    /// confirmation window runs again as a last look, and the launch goes through
    /// `launchWithRetries` into the one choke point (`preflightLaunch`), behind
    /// which bbolt's exclusive lock on the data directory is the final backstop.
    /// And if a core has accepted so much as one connection during the window we
    /// are not here at all: `onlyEverRefused` is false, and stays false for the
    /// rest of the episode.
    ///
    /// Returns true when it has taken the episode over (nothing further to
    /// report), false when the caller should fall through to the error.
    private func escalateToLaunchOverAStaleSocket(reason: String) async -> Bool {
        guard waitMaySpawn else { return false }

        // The attach watch takes the gate for each of its probes and holds it for
        // microseconds. Wait for it rather than reporting an error we would have
        // to retract — but do not wait indefinitely, or a wedged connection
        // attempt would silence the deadline altogether.
        guard await claimConnectionWork(waitingUpTo: 1.0) else {
            NSLog("[MCPProxy] Deadline expired while a connection attempt is in flight — waiting again")
            armUnresponsiveCoreDeadline(reason: reason)
            return true
        }
        defer { endConnectionWork() }

        // A last look, with the gate held so no probe of ours is in flight: a
        // core that is merely slow accepts at least one of these, and that answer
        // ends the escalation. Same window the spawn path uses.
        guard await confirmNothingIsListening() else { return false }
        guard !superseded, !shutdownRequested else { return false }

        NSLog("[MCPProxy] Nothing has ever answered on %@ (%@) — treating it as a socket file "
              + "left behind by a dead core and launching", socketPath, reason)

        // Called from the deadline task, never from inside the watch itself, so
        // cancelling here cannot cancel an attach in progress.
        cancelAttachWatch()
        await MainActor.run {
            appState.isStopped = false
            appState.ownership = .trayManaged
        }
        await launchWithRetries()
        return true
    }

    /// Take the connection gate, waiting up to `seconds` for a short-lived holder
    /// to finish. Polls rather than queues: the gate is a flag, not a lock, and
    /// the only thing that ever holds it briefly is a probe.
    private func claimConnectionWork(waitingUpTo seconds: TimeInterval) async -> Bool {
        let deadline = Date().addingTimeInterval(seconds)
        while true {
            if beginConnectionWork() { return true }
            guard Date() < deadline, !shutdownRequested else { return false }
            do {
                try await Task.sleep(nanoseconds: 50_000_000)
            } catch {
                return false
            }
        }
    }

    /// Launch the core, climbing the retry ladder until it comes up or the
    /// rungs run out. Caller MUST hold the connection gate.
    ///
    /// The ladder lives with the gate holder, not in the process-termination
    /// callback. A core that dies during `launchAndConnectOnce()` fires that
    /// callback while this task still holds the gate, so the callback stands
    /// down — as it must, or it would launch a second core. If the ladder lived
    /// there, `maxRetries` would silently collapse to a single attempt.
    private func launchWithRetries() async {
        while true {
            guard !shutdownRequested else { return }

            // Never ladder INTO a socket somebody owns. A core that came up sick
            // — listening but not answering — makes connectToCore() fail, and
            // without this the reconnection path would happily relaunch on top
            // of it. The rung check is here as well as in launchCore() because
            // this one can do something useful about it: wait.
            if await assessCore().somethingIsListening {
                await waitForTheCoreThatIsAlreadyThere(
                    reason: "a core is already listening on the socket",
                    // We are inside the launch path, so spawning is permitted —
                    // but this wait can never escalate anyway: a probe just
                    // accepted, which is the one thing that rules it out.
                    maySpawn: true
                )
                return
            }

            do {
                try await launchAndConnectOnce()
                return // success; launchAndConnectOnce cleared the ladder
            } catch {
                let coreError = (error as? CoreError) ?? .general(error.localizedDescription)

                // A rung that gave up may have left a process RUNNING: a core
                // that hung without ever becoming ready is still a core. Reap it
                // before the next rung, or the ladder is a core factory.
                await terminateManagedProcess(reason: "launch attempt failed")

                guard !shutdownRequested else { return }
                guard coreError.isRetryable else {
                    // A config error or a port conflict will not fix itself.
                    await handleCoreError(coreError)
                    return
                }
                guard retryCount < maxRetries else {
                    await handleCoreError(.maxRetriesExceeded)
                    return
                }

                retryCount += 1
                NSLog("[MCPProxy] Core launch failed (%@) — retry %d/%d",
                      coreError.userMessage, retryCount, maxRetries)
                await transitionState(to: .reconnecting(attempt: retryCount))

                let delay = reconnectionPolicy.delay(forAttempt: retryCount)
                do {
                    try await Task.sleep(nanoseconds: UInt64(delay * 1_000_000_000))
                } catch {
                    return // cancelled
                }
            }
        }
    }

    // MARK: - Private: Error Handling

    /// Handle a core error by transitioning state and sending a notification.
    private func handleCoreError(_ error: CoreError) async {
        await transitionState(to: .error(error))
        await notificationService.sendCoreError(error: error)
    }

    // MARK: - Private: Binary Resolution

    /// Resolve the mcpproxy binary, checking multiple locations.
    private func resolveBinary() throws -> String {
        // 1. MCPPROXY_CORE_PATH environment override
        if let override = ProcessInfo.processInfo.environment["MCPPROXY_CORE_PATH"],
           !override.isEmpty {
            let fm = FileManager.default
            if fm.isExecutableFile(atPath: override) {
                return override
            }
            throw CoreError.general("MCPPROXY_CORE_PATH does not point to a valid binary: \(override)")
        }

        let fm = FileManager.default

        // 2. Bundled binary inside .app/Contents/Resources/bin/mcpproxy
        if let execPath = Bundle.main.executablePath {
            let execURL = URL(fileURLWithPath: execPath)
            let macOSDir = execURL.deletingLastPathComponent()
            let contentsDir = macOSDir.deletingLastPathComponent()
            if contentsDir.lastPathComponent == "Contents" {
                let bundled = contentsDir
                    .appendingPathComponent("Resources")
                    .appendingPathComponent("bin")
                    .appendingPathComponent("mcpproxy")
                if fm.isExecutableFile(atPath: bundled.path) {
                    return bundled.path
                }
            }
        }

        // 3. Managed binary in Application Support
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let managedPath = "\(home)/Library/Application Support/mcpproxy/bin/mcpproxy"
        if fm.isExecutableFile(atPath: managedPath) {
            return managedPath
        }

        // 4. ~/.mcpproxy/bin/mcpproxy
        let dotPath = "\(home)/.mcpproxy/bin/mcpproxy"
        if fm.isExecutableFile(atPath: dotPath) {
            return dotPath
        }

        // 5. Common package manager locations
        let commonPaths = [
            "/opt/homebrew/bin/mcpproxy",
            "/usr/local/bin/mcpproxy",
        ]
        for path in commonPaths {
            if fm.isExecutableFile(atPath: path) {
                return path
            }
        }

        // 6. PATH lookup via `which`
        let whichProcess = Process()
        whichProcess.executableURL = URL(fileURLWithPath: "/usr/bin/which")
        whichProcess.arguments = ["mcpproxy"]
        let pipe = Pipe()
        whichProcess.standardOutput = pipe
        whichProcess.standardError = FileHandle.nullDevice
        try? whichProcess.run()
        whichProcess.waitUntilExit()
        if whichProcess.terminationStatus == 0 {
            let data = pipe.fileHandleForReading.readDataToEndOfFile()
            if let path = String(data: data, encoding: .utf8)?
                .trimmingCharacters(in: .whitespacesAndNewlines),
               fm.isExecutableFile(atPath: path) {
                return path
            }
        }

        throw CoreError.general("mcpproxy binary not found. Install via Homebrew or download from mcpproxy.app")
    }

    // MARK: - Private: Process Launch

    /// THE choke point's preflight. Every path that wants a core arrives at
    /// `launchCore` — start(), each rung of the ladder, retry(), the
    /// process-exit handler — so the invariant "never two cores on one data
    /// directory" is enforced HERE, once, instead of being a property of six
    /// call paths all staying disciplined. Three rounds of review have shown
    /// that discipline is not a thing to rely on.
    ///
    /// Every check is synchronous, so no other task on this actor can interleave
    /// between them and `proc.run()`. That is the whole of what actor isolation
    /// buys, and it is worth being honest about the rest: an EXTERNAL process
    /// can still bind the socket in the milliseconds after the last probe here.
    /// The backstops for that race are core-side, not tray-side — bbolt takes an
    /// exclusive lock on the data directory and the loser exits with code 3, and
    /// a core whose socket is already in use refuses to start at all.
    ///
    /// Throws rather than returning a Bool so the reason reaches the caller's
    /// error handling (and the user) unchanged.
    func preflightLaunch() throws {
        guard connectionWorkInFlight else {
            throw CoreError.general("internal error: tried to launch a core without the connection gate")
        }
        guard !shutdownRequested else {
            throw CoreError.general("shutting down")
        }
        if let existing = process, existing.isRunning {
            throw CoreError.general(
                "internal error: tried to launch a core while PID \(existing.processIdentifier) is still running"
            )
        }
        if SocketTransport.probeSocket(path: socketPath) == .connectable {
            throw CoreError.general("A core is already running on \(socketPath)")
        }
    }

    /// Launch the mcpproxy core process.
    private func launchCore(binaryPath: String) async throws {
        try preflightLaunch()

        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: binaryPath)
        // `["serve"]` unless this instance has been relocated, in which case the
        // core is told the same root the tray resolved — a core writing the real
        // ~/.mcpproxy while the tray watches a scratch socket is the accident
        // GH #936 is about.
        proc.arguments = InstancePaths.coreArguments

        // Let core use its own config API key (or auto-generate one).
        // We fetch the key from core via socket after it starts.
        var env = ProcessInfo.processInfo.environment
        env.removeValue(forKey: "MCPPROXY_API_KEY")
        // Enable socket communication
        env["MCPPROXY_SOCKET"] = "true"
        // Tray-launched core is allowed to write to macOS Keychain — user
        // explicitly opened the GUI app, so OS prompts are expected.
        // See issue #409 / internal/secret/keyring_provider.go.
        env["MCPPROXY_KEYRING_WRITE"] = "1"
        // Tell the core it was launched by the tray, so telemetry's launch_source
        // can say so. Without this a tray-spawned core is unclassifiable: its
        // parent is this app (not launchd, so not login_item) and it has no TTY
        // (so not cli), leaving launch_source "unknown".
        //
        // The DMG installer launches this app with MCPPROXY_LAUNCHED_BY=installer
        // (packaging/macos/postinstall.sh); that first-run attribution outranks
        // "tray", so do not overwrite it.
        if env["MCPPROXY_LAUNCHED_BY"] != "installer" {
            env["MCPPROXY_LAUNCHED_BY"] = "tray"
        }
        proc.environment = env

        // Capture stderr for error diagnostics
        let stderrPipe = Pipe()
        proc.standardError = stderrPipe
        proc.standardOutput = FileHandle.nullDevice

        // Monitor stderr in the background
        stderrBuffer = ""
        let stderrHandle = stderrPipe.fileHandleForReading
        Task { [weak self] in
            for try await line in stderrHandle.bytes.lines {
                await self?.appendStderr(line)
            }
        }

        // Set up process group for clean termination
        proc.qualityOfService = .userInitiated

        // Handle unexpected termination. The generation stamps WHICH launch this
        // callback belongs to: a process we gave up on and reaped must not, when
        // its callback finally lands, drive a fresh relaunch.
        launchGeneration += 1
        let generation = launchGeneration
        proc.terminationHandler = { [weak self] terminatedProcess in
            let status = terminatedProcess.terminationStatus
            Task {
                await self?.handleProcessExit(status: status, generation: generation)
            }
        }

        do {
            try proc.run()
        } catch {
            throw CoreError.general("Failed to launch core: \(error.localizedDescription)")
        }

        coresLaunched += 1
        process = proc
        // On the record before anything can go wrong with it: "which tray
        // started this core, and when" is the first question a dropped MCP
        // session raises (#862).
        AppLifecycle.shared.recordCoreLaunched(
            pid: proc.processIdentifier,
            reason: "tray launched core (\(binaryPath))"
        )
    }

    /// Append a line to the stderr buffer (called from background task).
    private func appendStderr(_ line: String) {
        // Keep the last 100 lines for diagnostics
        let lines = stderrBuffer.components(separatedBy: "\n")
        if lines.count > 100 {
            stderrBuffer = lines.suffix(100).joined(separator: "\n")
        }
        stderrBuffer += line + "\n"
    }

    // MARK: - Private: Socket Wait

    /// Poll the Unix socket until it becomes available or the timeout expires.
    private func waitForSocket(timeout: TimeInterval) async throws {
        let deadline = Date().addingTimeInterval(timeout)
        let pollInterval: UInt64 = 250_000_000 // 250ms

        while Date() < deadline {
            // Check if the process has already exited
            if let process, !process.isRunning {
                let code = process.terminationStatus
                let stderr = stderrBuffer
                throw CoreError.fromExitCode(code, stderr: stderr)
            }

            if SocketTransport.isSocketAvailable(path: socketPath) {
                return
            }

            try await Task.sleep(nanoseconds: pollInterval)
        }

        throw CoreError.startupTimeout
    }

    // MARK: - Private: Connect to Core

    /// Create API and SSE clients connected to the core via the Unix socket.
    private func connectToCore() async throws {
        // First connect via socket (no API key needed — socket is trusted)
        NSLog("[MCPProxy] connectToCore: creating APIClient via socket=%@", socketPath)

        let client = APIClient(
            socketPath: socketPath,
            baseURL: "http://127.0.0.1:8080",
            apiKey: nil
        )

        // Verify the core is ready
        NSLog("[MCPProxy] connectToCore: calling /ready...")
        let ready = try await client.ready()
        guard ready else {
            throw CoreError.general("Core reported not ready")
        }
        NSLog("[MCPProxy] connectToCore: core is ready")

        // Fetch version info and extract API key from web_ui_url
        NSLog("[MCPProxy] connectToCore: calling /api/v1/info...")
        let info = try await client.info()
        NSLog("[MCPProxy] connectToCore: got version=%@", info.version)

        // Extract API key from web_ui_url (e.g. "http://127.0.0.1:8080/ui/?apikey=abc123")
        if let urlComponents = URLComponents(string: info.webUiUrl),
           let apikeyItem = urlComponents.queryItems?.first(where: { $0.name == "apikey" }),
           let key = apikeyItem.value, !key.isEmpty {
            sessionAPIKey = key
            NSLog("[MCPProxy] connectToCore: extracted API key from core (prefix=%@...)", String(key.prefix(8)))
        } else {
            NSLog("[MCPProxy] connectToCore: WARNING - no API key found in web_ui_url: %@", info.webUiUrl)
        }

        // Extract the base URL (scheme + host + port) from the web_ui_url
        // so the tray can open the Web UI on the correct port.
        let webUIBase: String
        if let comps = URLComponents(string: info.webUiUrl),
           let scheme = comps.scheme, let host = comps.host {
            let port = comps.port.map { ":\($0)" } ?? ""
            webUIBase = "\(scheme)://\(host)\(port)"
        } else {
            webUIBase = "http://127.0.0.1:8080"
        }

        await MainActor.run {
            appState.version = info.version
            appState.webUIBaseURL = webUIBase
            if let update = info.update, update.available, let latest = update.latestVersion {
                appState.updateAvailable = latest.hasPrefix("v") ? String(latest.dropFirst()) : latest
            }
        }

        apiClient = client
        // Separate client for liveness probes: same socket, much shorter
        // timeout, and never shared with the UI so a probe can never be queued
        // behind a slow user-facing request.
        consecutiveProbeFailures = 0
        await MainActor.run { appState.apiClient = client }

        // Create SSE client — uses TCP (not socket) so needs the API key
        NSLog("[MCPProxy] connectToCore: creating SSEClient (TCP, apiKey=%@, base=%@)",
              sessionAPIKey != nil ? "set" : "nil", webUIBase)
        sseClient = SSEClient(
            baseURL: webUIBase,
            apiKey: sessionAPIKey
        )
        NSLog("[MCPProxy] connectToCore: done")
    }

    // MARK: - Private: SSE Streaming

    /// Install the stream source. Production sets this in `connectToCore`; the
    /// only other caller is the test that drives `startSSEStream` end to end,
    /// which is what pins the generation capture to the real wiring rather than
    /// to `SSEStreamSession` alone.
    func installSSEClient(_ client: any SSEStreaming) {
        sseClient = client
    }

    /// Start consuming the SSE event stream.
    ///
    /// Internal rather than private so that test can exist. Everything it does
    /// is one call to `SSEStreamSession`; the reason that matters is in the
    /// comment inside.
    func startSSEStream() {
        guard let sseClient else { return }

        sseTask?.cancel()
        sseTask = Task { [weak self] in
            // The generation is captured where the STREAM opens, not where an
            // event arrives — a stream belongs to exactly one connection, and an
            // arrival-time read after a reconnect returns the new generation and
            // passes the guard it was meant to fail. That rule lives in
            // SSEStreamSession so it is testable; keep this body a single call.
            await SSEStreamSession.run(
                captureGeneration: { await self?.currentConnectionGeneration() },
                open: { await sseClient.connect() },
                handle: { event, generation in
                    await self?.handleSSEEvent(event, generation: generation)
                }
            )
            // Stream ended -- trigger reconnection if still connected
            guard !Task.isCancelled else { return }
            await self?.handleSSEDisconnect()
        }
    }

    /// Handle a single SSE event.
    ///
    /// IMPORTANT: SSE `status` events fire frequently (every few seconds).
    /// We must NOT re-fetch the full server list on each one — that would
    /// trigger @Published updates which cause MenuBarExtra to duplicate items.
    /// Instead, only update lightweight counters from the inline status data.
    /// The generation of the connection the tray is on right now.
    private func currentConnectionGeneration() async -> Int {
        await MainActor.run { appState.connectionGeneration }
    }

    /// - Parameter generation: the connection whose stream delivered this event,
    ///   captured when that stream was opened. Glance publishes are rejected if
    ///   the tray has since reconnected.
    /// Internal, not private, so the glance routing test can drive the real
    /// dispatch: an adapter for an event name this switch never routes is dead
    /// code, and nothing else in the app would notice.
    func handleSSEEvent(_ event: SSEEvent, generation: Int) async {
        switch event.event {
        case "status":
            // Status events contain inline stats. Spec 048: any change that
            // would also flip connected_count emits a `servers.changed`
            // event (delivered within ~50 ms by spec 047's coalescer) which
            // already updates the per-server appState. Stat aggregates are
            // updated unconditionally from the inline payload — no refetch.
            if let data = event.data.data(using: .utf8),
               let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let stats = json["upstream_stats"] as? [String: Any] {
                let total = stats["total_servers"] as? Int ?? 0
                let tools = stats["total_tools"] as? Int ?? 0
                await MainActor.run {
                    if appState.totalServers != total { appState.totalServers = total }
                    if appState.totalTools != tools { appState.totalTools = tools }
                }
            }

        case "servers.changed":
            // Spec 047: prefer the embedded server list in the event payload
            // and skip the GET /api/v1/servers refetch entirely. Falls back to
            // refetch when running against an older core that publishes
            // notify-only events (no `servers` field).
            let oldQuarantined = await MainActor.run { appState.quarantinedToolsCount }
            var consumedFromPayload = false
            if let data = event.data.data(using: .utf8),
               let envelope = try? JSONDecoder().decode(ServersChangedEnvelope.self, from: data),
               let servers = envelope.payload.servers {
                await appState.updateServers(servers)
                consumedFromPayload = true
            }
            if !consumedFromPayload {
                await refreshServers()
            }
            await MainActor.run { appState.serversVersion += 1 }
            let newQuarantined = await MainActor.run { appState.quarantinedToolsCount }
            // Notify on new quarantine events
            if newQuarantined > oldQuarantined {
                await notificationService.sendQuarantineAlert(
                    server: "upstream",
                    toolCount: newQuarantined
                )
            }

        case "config.reloaded":
            // Configuration reloaded; refresh everything once.
            // A re-init loop re-emits config.reloaded each cycle even when the
            // SSE connection stays up, so treat it as an instability signal:
            // this re-arms the settle gate and suppresses the replay-driven
            // quarantine/sensitive notifications for the duration (MCP-2328).
            await notificationService.markConnectionUnsettled()
            await refreshState()
            await MainActor.run {
                appState.serversVersion += 1
                appState.activityVersion += 1
            }

        case GlanceEvent.upstreamCompleted, GlanceEvent.internalCompleted, GlanceEvent.policyDecision:
            // Tray Glance: adapt the payload into a row and prepend it.
            // `activity.policy_decision` is here because a blocked call emits
            // nothing else — it never dispatches, so no completion event
            // follows — and a block is the one outcome that must not wait 30
            // seconds for the poll to reveal it.
            // Deliberately NO refreshActivity() here — a REST GET per event is
            // network amplification, not push. The 30s reconciling poll
            // (refreshGlanceActivity) replaces these optimistic rows with the
            // storage-assigned records. `activity.tool_call.started` is ignored:
            // the core does not persist started events, so a row built from one
            // would never be reconciled. `activityVersion` is deliberately NOT
            // bumped either — ActivityView reloads on that counter
            // (ActivityView.swift → loadSummary + loadActivities), so a
            // per-event bump is the same amplification through a second door
            // whenever the Activity window is open.
            guard let data = event.data.data(using: .utf8),
                  let entry = GlanceEvent.adapt(eventName: event.event, data: data) else {
                break
            }
            await appState.prependGlanceActivity(entry, generation: generation)

        case "active_profile.changed":
            // Profiles v2 T5: the server-level default active profile was switched
            // (possibly by another client). Refetch profiles + active so the tray
            // submenu reflects it. The payload carries active_profile, but a
            // refetch also picks up tool-count/profile-set changes uniformly.
            await refreshProfiles()

        case "ping":
            // Keepalive; no action needed
            break

        default:
            break
        }
    }

    /// Handle SSE disconnect by attempting reconnection.
    private func handleSSEDisconnect() async {
        guard case .connected = await MainActor.run(body: { appState.coreState }) else { return }

        // Check if the socket is still alive
        if SocketTransport.isSocketAvailable(path: socketPath) {
            // Socket is fine; just reconnect SSE
            startSSEStream()
        } else {
            // Socket gone; core likely crashed
            await handleCoreLoss()
        }
    }

    // MARK: - Private: State Refresh

    /// Start a periodic refresh task that polls the core every `refreshInterval`.
    ///
    /// The tick is also the tray's liveness detector for a core it does not own
    /// (GH #926). See `coreIsAlive()`.
    private func startPeriodicRefresh() {
        refreshTask?.cancel()
        let interval = refreshInterval
        refreshTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: UInt64(interval * 1_000_000_000))
                guard !Task.isCancelled, let self else { break }
                // Stop refreshing the moment the core is gone: handleCoreLoss()
                // takes over (and restarts this task if it reconnects).
                guard await self.coreIsAlive() else { break }
                await self.refreshState()
            }
        }
    }

    /// Liveness probe for the periodic tick — the ONLY thing that notices a
    /// dead core we did not spawn (GH #926).
    ///
    /// A core the tray launched is watched by `Process.terminationHandler`. An
    /// ATTACHED core has no Process and no PID, and the other two candidate
    /// detectors are inert: `SSEClient` retries forever without ever finishing
    /// its stream (so `handleSSEDisconnect()` never runs), and every refresh
    /// helper swallows its errors by design. The result was a tray that showed
    /// a healthy icon indefinitely after the core died.
    ///
    /// The probe is the same one `attachIfCoreIsRunning()` uses to decide a core
    /// is there in the first place — socket, then a real API call — so "we think
    /// we are connected" is checked against exactly the evidence that made us
    /// connect. Returns true when the core answered (or when we are not in a
    /// state where the question applies); returns false after handing off to
    /// reconnection.
    func coreIsAlive() async -> Bool {
        // Only meaningful while we believe we are connected. Any other state is
        // already being driven by launch/reconnect/shutdown logic.
        guard case .connected = await MainActor.run(body: { appState.coreState }) else { return true }

        switch SocketTransport.probeSocket(path: socketPath) {
        case .absent:
            // No socket FILE. A running core always owns its socket file, so
            // this one is unambiguous — act on it immediately. This is the
            // reporter's case (`kill -9` plus a cleaned-up socket).
            NSLog("[MCPProxy] Core socket %@ is gone — reconnecting", socketPath)
            consecutiveProbeFailures = 0
            await handleCoreLoss()
            return false

        case .localFailure(let code):
            // Descriptor exhaustion, out of memory, permissions. Our problem,
            // not the core's — it is not evidence either way, so it must not
            // even count as a strike.
            NSLog("[MCPProxy] Liveness probe could not run (errno %d) — not treating as core loss", code)
            return true

        case .refused:
            // AMBIGUOUS: a dead process leaves a stale socket file that refuses
            // connections, and so does a live core whose listen backlog is
            // momentarily full. Same evidence, opposite conclusions — so it gets
            // the same tolerance as a missed readiness check rather than an
            // immediate verdict.
            return await recordProbeFailure(reason: "socket refused connections")

        case .connectable:
            // Something is listening. Whether it is HEALTHY is a softer
            // question: a saturated core can miss a readiness check and be
            // perfectly fine a second later. Condemning it on one sample would
            // put "reconnecting" in the menu bar every time the core got busy.
            if await probeIsReady() {
                consecutiveProbeFailures = 0
                return true
            }
            return await recordProbeFailure(reason: "no answer from /ready")
        }
    }

    /// Count one inconclusive probe. Returns true while the budget holds (treat
    /// the core as alive), false once it is spent — having handed off to
    /// reconnection.
    private func recordProbeFailure(reason: String) async -> Bool {
        consecutiveProbeFailures += 1
        guard consecutiveProbeFailures >= probeFailureBudget else {
            NSLog("[MCPProxy] Core liveness check missed (%d/%d): %@",
                  consecutiveProbeFailures, probeFailureBudget, reason)
            return true
        }

        NSLog("[MCPProxy] Core failed %d consecutive liveness checks (%@) — reconnecting",
              consecutiveProbeFailures, reason)
        consecutiveProbeFailures = 0
        await handleCoreLoss()
        return false
    }

    /// One readiness call, bounded by `probeTimeout` rather than the API
    /// client's generous request timeout.
    private func probeIsReady() async -> Bool {
        // ONE probe client for the manager's whole life: the same socket and the
        // same short timeout are wanted before the attach (is a core there?) and
        // after it (is it still there?). Creating one per probe would also mint a
        // routing token per probe, and the attach watcher probes every 2s.
        let client: APIClient
        if let existing = probeClient {
            client = existing
        } else {
            client = APIClient(socketPath: socketPath, requestTimeout: probeTimeout)
            probeClient = client
        }
        return (try? await client.ready()) == true
    }

    /// The core we were connected to is gone. Reuse the reconnection path, which
    /// re-attaches to a core that comes back and refuses to spawn a replacement
    /// for one we never owned.
    func handleCoreLoss() async {
        // Read state first (this suspends), THEN claim the gate — the claim must
        // be the last thing before the work, with no suspension between claim
        // and use.
        guard case .connected = await MainActor.run(body: { appState.coreState }) else { return }
        guard beginConnectionWork() else { return }
        defer { endConnectionWork() }

        await transitionState(to: .reconnecting(attempt: 1))
        await attemptReconnection()
    }

    /// Fetch full state from the core and update appState.
    /// Spec 048: dropped the per-tick refreshServers() call. The server list
    /// is now SSE-driven (spec 047 servers.changed payload). MCPProxyApp
    /// installs a separate 5-min safety-net timer for missed-event recovery.
    private func refreshState() async {
        await refreshActivity()
        await refreshSessions()
        await refreshGlanceActivity()
        await refreshGlanceSessions()
        await refreshUsage()
        await refreshTokenMetrics()
        await refreshSecurityStatus()
        await refreshProfiles()
        // Bump activityVersion so ActivityView reloads. Still needed after the
        // glance's SSE work: the bus emits `activity.tool_call.completed` and
        // `activity.internal_tool_call.completed` (internal/runtime/events.go),
        // which feed the glance rows only — there is no bare "activity" event,
        // and nothing else republishes the full log ActivityView renders.
        await MainActor.run { appState.activityVersion += 1 }
    }

    /// Fetch Docker and quarantine status from the API.
    private func refreshSecurityStatus() async {
        guard let apiClient else { return }
        do {
            let dockerOK = try await apiClient.dockerStatus()
            if !dockerOK {
                // The Docker health checker can get stuck at "max retries exceeded"
                // even when Docker is running fine. Check the config as a fallback:
                // if docker_isolation.enabled is true in the running config, treat as available.
                let configEnabled = await MainActor.run { appState.totalServers > 0 }
                if configEnabled {
                    // Servers are connected — Docker must be working if isolation is enabled.
                    // Spec 048: appState.servers is SSE-fed, so read it directly instead
                    // of issuing another GET /api/v1/servers on every periodic refresh.
                    let hasStdioServers = await MainActor.run {
                        appState.servers.contains(where: { $0.connected && $0.protocol == "stdio" })
                    }
                    await MainActor.run {
                        appState.dockerAvailable = hasStdioServers || dockerOK
                    }
                } else {
                    await MainActor.run {
                        if appState.dockerAvailable != dockerOK { appState.dockerAvailable = dockerOK }
                    }
                }
            } else {
                await MainActor.run {
                    if appState.dockerAvailable != dockerOK { appState.dockerAvailable = dockerOK }
                }
            }
        } catch {
            // Non-fatal
        }
        do {
            let diag = try await apiClient.diagnostics()
            await MainActor.run {
                if let q = diag.quarantineEnabled {
                    if appState.quarantineEnabled != q { appState.quarantineEnabled = q }
                }
            }
        } catch {
            // Non-fatal
        }
    }

    /// Fetch the server list and update appState.
    private func refreshServers() async {
        guard let apiClient else { return }
        do {
            let servers = try await apiClient.servers()
            await appState.updateServers(servers)
        } catch {
            // Non-fatal; we'll retry on the next refresh
        }
    }

    /// Spec 048: long-cadence safety-net wrapper around `refreshServers`.
    /// Called by a 5-minute Combine timer in `MCPProxyApp` to guard against
    /// missed `servers.changed` SSE events. Separate name documents intent —
    /// this is *not* the on-demand refresh path (that's been retired).
    func refreshServersForSafetyNet() async {
        await refreshServers()
    }

    /// Fetch the configured profiles + active profile and update appState
    /// (Profiles v2 T5). Driven on connect, on the periodic refresh, and on the
    /// `active_profile.changed` SSE event so a switch made by another client
    /// (Web UI, CLI, the Go tray) is reflected in the macOS tray submenu.
    func refreshProfiles() async {
        guard let apiClient else { return }
        do {
            let profiles = try await apiClient.profiles()
            let active = try await apiClient.activeProfile()
            await MainActor.run {
                if appState.profiles != profiles { appState.profiles = profiles }
                if appState.activeProfile != active { appState.activeProfile = active }
            }
        } catch {
            // Non-fatal; we'll retry on the next refresh or SSE event.
        }
    }

    /// Fetch recent activity and update appState.
    private func refreshActivity() async {
        guard let apiClient else { return }
        do {
            let activity = try await apiClient.recentActivity(limit: 10)
            await appState.updateActivity(activity)
        } catch {
            // Non-fatal; we'll retry on the next refresh
        }
    }

    /// Fetch recent MCP sessions and update appState.
    private func refreshSessions() async {
        guard let apiClient else { return }
        do {
            let sessions = try await apiClient.sessions(limit: 5)
            await MainActor.run { appState.recentSessions = sessions }
        } catch {
            // Non-fatal; we'll retry on the next refresh
        }
    }

    /// Tray Glance: fetch the type-filtered tool-call feed for the menu's
    /// "Recent" rows. Separate from `refreshActivity()` on purpose — that feed
    /// stays broad because the native Dashboard renders the full activity log.
    ///
    /// The body lives on `AppState` behind `GlanceDataSource`, like the other
    /// two glance fetches: a catch block reachable only from here is a catch
    /// block no test can see, which is how this one came to swallow its errors.
    private func refreshGlanceActivity() async {
        guard let apiClient else { return }
        await appState.refreshGlanceActivity(from: apiClient)
    }

    /// Tray Glance: fetch active-only sessions for the menu's "Clients" rows.
    /// Separate from `refreshSessions()`, which must keep closed sessions so
    /// ActivityView can resolve session ids to client names.
    private func refreshGlanceSessions() async {
        guard let apiClient else { return }
        await appState.refreshGlanceSessions(from: apiClient)
    }

    /// Tray Glance: fetch the 24h usage aggregate that backs both the header
    /// count and the histogram submenu.
    /// Non-fatal, and retried on the next refresh — but a failure is RECORDED
    /// rather than swallowed. Silently keeping the loading state would tell the
    /// user a fetch that is never coming back is still in flight; `refreshUsage`
    /// publishes the failure so the submenu can say so.
    private func refreshUsage() async {
        guard let apiClient else { return }
        await appState.refreshUsage(from: apiClient)
    }

    /// Fetch token metrics from the status endpoint and update appState.
    private func refreshTokenMetrics() async {
        guard let apiClient else { return }
        do {
            let status = try await apiClient.status()
            if let metrics = status.upstreamStats?.tokenMetrics {
                await MainActor.run { appState.tokenMetrics = metrics }
            }
        } catch {
            // Non-fatal; token metrics are optional
        }
    }

    // MARK: - Private: Process Exit Handling

    /// Handle the core process exiting.
    func handleProcessExit(status: Int32, generation: Int? = nil) async {
        // A callback from a launch we already abandoned says nothing about the
        // core we have now. Acting on it is how a reaped attempt turns into an
        // extra spawn.
        if let generation, generation != launchGeneration {
            NSLog("[MCPProxy] Ignoring exit of superseded launch %d (current %d)",
                  generation, launchGeneration)
            return
        }

        let stderr = stderrBuffer

        // Recorded whatever we decide to do about it, and recorded here rather
        // than only in the retry branches: an exit that leads to no retry (the
        // user stopped it, a clean shutdown) is exactly the one that otherwise
        // leaves no trace at all (#862).
        AppLifecycle.shared.recordCoreExited(
            pid: process?.processIdentifier ?? 0,
            status: status,
            reason: "core process exited"
        )

        // If stopped by user, don't retry — this is intentional
        let isStopped = await MainActor.run { appState.isStopped }
        if isStopped {
            NSLog("[MCPProxy] handleProcessExit: stopped by user, not retrying")
            return
        }

        // Normal exit (0) during shutdown is expected
        if status == 0 {
            let currentState = await MainActor.run { appState.coreState }
            if case .shuttingDown = currentState {
                return // Expected during shutdown
            }
        }

        let error = CoreError.fromExitCode(status, stderr: stderr)

        // Send notification for non-trivial errors
        await notificationService.sendCoreError(error: error)

        if error.isRetryable && retryCount < maxRetries {
            // Honour the same gate as every other reconnection entry point. If
            // one is already running it is ALREADY relaunching this core, and it
            // owns the whole retry ladder — standing down here loses nothing.
            guard beginConnectionWork() else {
                NSLog("[MCPProxy] handleProcessExit: a reconnection is already in flight")
                return
            }
            defer { endConnectionWork() }
            await transitionState(to: .reconnecting(attempt: retryCount + 1))
            await attemptReconnection()
        } else {
            await transitionState(to: .error(error))
        }
    }

    // MARK: - Private: Reconnection

    /// Attempt to reconnect after a failure.
    /// Run the retry ladder to completion. Caller MUST hold the connection gate.
    ///
    /// The ladder lives HERE, in the task that holds the gate, rather than in
    /// the process-termination callback. It used to depend on each relaunched
    /// core's exit callback starting the next attempt — but that callback now
    /// (correctly) stands down when a reconnection is already running, which
    /// silently reduced `maxRetries` to a single relaunch for a core that
    /// crashes instantly. Owning the loop here keeps both properties: exactly
    /// one launch at a time, AND every rung of the ladder.
    private func attemptReconnection() async {
        // The core we were connected to answered plenty of probes; none of that
        // is evidence about the socket we are about to reason over now.
        beginSocketEvidence()
        while true {
            guard !shutdownRequested else { return }
            let delay = reconnectionPolicy.delay(forAttempt: max(retryCount, 1))
            do {
                try await Task.sleep(nanoseconds: UInt64(delay * 1_000_000_000))
            } catch {
                return // Cancelled
            }
            guard !shutdownRequested else { return }

            // If a core is up on the socket — ours or someone else's — attach to
            // it rather than starting another.
            if SocketTransport.isSocketAvailable(path: socketPath) {
                do {
                    try await connectToCore()
                    await transitionState(to: .connected)
                    retryCount = 0
                    await refreshState()
                    startSSEStream()
                    startPeriodicRefresh()
                    return
                } catch {
                    // Fall through to relaunch
                }
            }

            let ownership = await MainActor.run { appState.ownership }
            guard ownership == .trayManaged else {
                // External core is gone and we don't own it. Say so — and keep
                // watching the socket, so a core that comes back (launchd, brew
                // services, the user re-running `mcpproxy serve`) is picked up
                // without needing a menu click. Same cheap poll idle mode uses.
                await transitionState(
                    to: .error(.general("External core process is no longer available"))
                )
                startAttachWatch()
                return
            }

            await launchWithRetries()
            return
        }
    }

    // MARK: - Private: State Transition

    /// Transition the core state via the main actor.
    private func transitionState(to newState: CoreState) async {
        await appState.transition(to: newState)

        // Signal connection instability so replay-driven notifications
        // (quarantine, sensitive-data) are suppressed until the connection
        // settles. Every reconnect / relaunch / crash funnels through here,
        // so during a backend re-init loop the gate is re-armed each cycle and
        // never settles — breaking the notification storm (MCP-2328).
        // `.connected` is the steady state and is intentionally NOT marked.
        switch newState {
        case .launching, .waitingForCore, .reconnecting, .error:
            await notificationService.markConnectionUnsettled()
        case .idle, .connected, .shuttingDown:
            break
        }
    }

    // MARK: - Private: API Key Generation

    /// Generate a cryptographically secure random API key (32 bytes, hex-encoded).
    private func generateAPIKey() -> String {
        var bytes = [UInt8](repeating: 0, count: 32)
        _ = SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes)
        return bytes.map { String(format: "%02x", $0) }.joined()
    }
}
