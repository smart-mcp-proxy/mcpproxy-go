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
    private var sseClient: SSEClient?
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
    private var retryCount: Int = 0
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
        probeTimeout: TimeInterval = 5.0
    ) {
        self.appState = appState
        self.notificationService = notificationService
        self.reconnectionPolicy = reconnectionPolicy
        self.refreshInterval = refreshInterval
        self.probeTimeout = probeTimeout

        // Compute socket path: ~/.mcpproxy/mcpproxy.sock
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        self.socketPath = socketPath ?? "\(home)/.mcpproxy/mcpproxy.sock"
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
        switch await attachIfCoreIsRunning() {
        case .attached:
            return
        case .inFlight:
            // Someone else is attaching to the same core. Leave them to it —
            // spawning or going idle here would fight that attach.
            return
        case .noCore:
            break
        }

        guard maySpawn else {
            await awaitExternalCore()
            return
        }

        // Stale socket — remove it so our new core can create a fresh one.
        if SocketTransport.isSocketAvailable(path: socketPath) {
            try? FileManager.default.removeItem(atPath: socketPath)
        }

        // Launch our own core as a subprocess
        await MainActor.run {
            appState.isStopped = false
            appState.ownership = .trayManaged
        }
        await launchAndConnect()
    }

    /// Retire this manager: stop watching and never touch AppState again. Called
    /// before the app swaps in a replacement manager.
    func supersede() {
        superseded = true
        cancelAttachWatch()
    }

    // MARK: - Private: Connection-work gate

    /// Claim the right to establish a connection. Synchronous on purpose: under
    /// actor isolation a check-and-set with no `await` between them cannot be
    /// interleaved, which is exactly what makes this a guard rather than a hint.
    private func beginConnectionWork() -> Bool {
        guard !connectionWorkInFlight else { return false }
        connectionWorkInFlight = true
        return true
    }

    private func endConnectionWork() {
        connectionWorkInFlight = false
    }

    /// What happened when we looked for a core to attach to.
    ///
    /// `inFlight` is NOT `noCore`: another task is already attaching, and the
    /// caller must not react by spawning a core or declaring itself idle — that
    /// would fight the attach that is under way.
    enum AttachOutcome {
        case attached
        case inFlight
        case noCore
    }

    /// Attach to a core that is already up.
    private func attachIfCoreIsRunning() async -> AttachOutcome {
        guard !superseded else { return .noCore }
        // Everything up to the acquisition is synchronous, so the claim below is
        // atomic with respect to the checks above it.
        guard SocketTransport.isSocketAvailable(path: socketPath) else { return .noCore }
        guard beginConnectionWork() else { return .inFlight }
        defer { endConnectionWork() }

        guard await probeExternalCore() else { return .noCore }
        guard !superseded else { return .noCore } // a replacement took over while we probed
        await attachToExternalCore()
        return .attached
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
                // Keep polling while another task is mid-attach: if that attach
                // fails, this watch is what picks the core up next time round.
                if case .attached = await self.attachIfCoreIsRunning() { return }
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

    /// Probe an existing socket to see if a live core is behind it.
    /// Returns true only if the core responds to an API call.
    private func probeExternalCore() async -> Bool {
        // Same short timeout as the liveness probe: this is the same question,
        // asked before we are attached rather than after.
        let client = APIClient(socketPath: socketPath, requestTimeout: probeTimeout)
        do {
            let ready = try await client.ready()
            return ready
        } catch {
            return false
        }
    }

    /// Gracefully shut down the core process and all connections.
    ///
    /// A core we merely ATTACHED to is left running: we disconnect from it and
    /// stop there. That rule is now enforced by the ownership check below rather
    /// than by the fact that `process` happens to be nil for an attached core.
    func shutdown() async {
        await transitionState(to: .shuttingDown)
        cancelAttachWatch()

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
            // Send SIGTERM for graceful shutdown
            process.interrupt() // sends SIGINT on macOS, which Go handles gracefully

            // Also send SIGTERM explicitly
            kill(process.processIdentifier, SIGTERM)

            // Wait up to 10 seconds for graceful exit
            let deadline = Date().addingTimeInterval(10.0)
            while process.isRunning && Date() < deadline {
                try? await Task.sleep(nanoseconds: 100_000_000) // 100ms
            }

            // Force kill if still running
            if process.isRunning {
                kill(process.processIdentifier, SIGKILL)
                process.waitUntilExit()
            }
        }
        self.process = nil

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

        // Clean up any existing process
        if let process, process.isRunning {
            kill(process.processIdentifier, SIGTERM)
            process.waitUntilExit()
        }
        self.process = nil

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

    /// Full launch sequence: resolve binary, start process, wait for socket, connect.
    private func launchAndConnect() async {
        do {
            await transitionState(to: .launching)

            // Resolve the core binary
            let binaryPath = try resolveBinary()
            coreBinaryPath = binaryPath

            // Launch the process (core uses its own config API key)
            try await launchCore(binaryPath: binaryPath)

            // Wait for the socket to become available
            await transitionState(to: .waitingForCore)
            try await waitForSocket(timeout: 60.0)

            // Connect API and SSE clients
            try await connectToCore()

            await transitionState(to: .connected)
            await refreshState()
            startSSEStream()
            startPeriodicRefresh()

        } catch let error as CoreError {
            NSLog("[MCPProxy] launchAndConnect FAILED (CoreError): %@", error.userMessage)
            await handleCoreError(error)
        } catch {
            NSLog("[MCPProxy] launchAndConnect FAILED: %@", error.localizedDescription)
            await handleCoreError(.general(error.localizedDescription))
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

    /// Launch the mcpproxy core process.
    private func launchCore(binaryPath: String) async throws {
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: binaryPath)
        proc.arguments = ["serve"]

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

        // Handle unexpected termination
        proc.terminationHandler = { [weak self] terminatedProcess in
            let status = terminatedProcess.terminationStatus
            Task {
                await self?.handleProcessExit(status: status)
            }
        }

        do {
            try proc.run()
        } catch {
            throw CoreError.general("Failed to launch core: \(error.localizedDescription)")
        }

        process = proc
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
        probeClient = APIClient(socketPath: socketPath, requestTimeout: probeTimeout)
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

    /// Start consuming the SSE event stream.
    private func startSSEStream() {
        guard let sseClient else { return }

        sseTask?.cancel()
        sseTask = Task { [weak self] in
            let stream = await sseClient.connect()
            for await event in stream {
                guard !Task.isCancelled else { break }
                await self?.handleSSEEvent(event)
            }
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
    private func handleSSEEvent(_ event: SSEEvent) async {
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

        case "activity":
            // New activity; refresh and check for sensitive data
            let oldSensitive = await MainActor.run { appState.sensitiveDataAlertCount }
            await refreshActivity()
            await MainActor.run { appState.activityVersion += 1 }
            let newSensitive = await MainActor.run { appState.sensitiveDataAlertCount }
            // Notify on new sensitive data detections
            if newSensitive > oldSensitive {
                if let latest = await MainActor.run(body: {
                    appState.recentActivity.first(where: { $0.hasSensitiveData == true })
                }) {
                    await notificationService.sendSensitiveDataAlert(
                        server: latest.serverName ?? "unknown",
                        tool: latest.toolName ?? "unknown",
                        category: "sensitive data"
                    )
                }
            }

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

        // The socket is the unambiguous signal: a process that exited cannot
        // hold one open, and a stale file left by `kill -9` fails the connect
        // probe. No tolerance needed — that core is gone.
        guard SocketTransport.isSocketAvailable(path: socketPath) else {
            NSLog("[MCPProxy] Core socket %@ is gone — reconnecting", socketPath)
            consecutiveProbeFailures = 0
            await handleCoreLoss()
            return false
        }

        // The socket is up, so the core is running. Whether it is HEALTHY is a
        // softer question: a saturated core can miss a readiness check and be
        // perfectly fine a second later. Condemning it on one sample would put
        // "reconnecting" in the menu bar every time the core got busy.
        if await probeIsReady() {
            consecutiveProbeFailures = 0
            return true
        }

        consecutiveProbeFailures += 1
        guard consecutiveProbeFailures >= probeFailureBudget else {
            NSLog("[MCPProxy] Core missed a readiness check (%d/%d)",
                  consecutiveProbeFailures, probeFailureBudget)
            return true
        }

        NSLog("[MCPProxy] Core stopped answering on %@ after %d consecutive checks — reconnecting",
              socketPath, consecutiveProbeFailures)
        consecutiveProbeFailures = 0
        await handleCoreLoss()
        return false
    }

    /// One readiness call, bounded by `probeTimeout` rather than the API
    /// client's generous request timeout.
    private func probeIsReady() async -> Bool {
        guard let probeClient else { return false }
        return (try? await probeClient.ready()) == true
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
        await refreshTokenMetrics()
        await refreshSecurityStatus()
        await refreshProfiles()
        // Bump activityVersion so ActivityView reloads
        // (SSE doesn't emit "activity" events, so periodic refresh is needed)
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
    func handleProcessExit(status: Int32) async {
        let stderr = stderrBuffer

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
            // one is already running it is ALREADY relaunching this core;
            // starting a second would race it into two live cores.
            guard beginConnectionWork() else {
                NSLog("[MCPProxy] handleProcessExit: a reconnection is already in flight")
                return
            }
            defer { endConnectionWork() }
            retryCount += 1
            await transitionState(to: .reconnecting(attempt: retryCount))
            await attemptReconnection()
        } else {
            await transitionState(to: .error(error))
        }
    }

    // MARK: - Private: Reconnection

    /// Attempt to reconnect after a failure.
    private func attemptReconnection() async {
        let delay = reconnectionPolicy.delay(forAttempt: retryCount)
        let delayNs = UInt64(delay * 1_000_000_000)

        do {
            try await Task.sleep(nanoseconds: delayNs)
        } catch {
            return // Cancelled
        }

        // If an external core came up, attach to it
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

        // If we own the core, relaunch it
        let ownership = await MainActor.run { appState.ownership }
        if ownership == .trayManaged {
            if retryCount < maxRetries {
                await launchAndConnect()
            } else {
                await transitionState(to: .error(.maxRetriesExceeded))
                await notificationService.sendCoreError(error: .maxRetriesExceeded)
            }
        } else {
            // External core is gone and we don't own it. Say so — and keep
            // watching the socket, so a core that comes back (launchd, brew
            // services, the user re-running `mcpproxy serve`) is picked up
            // without needing a menu click. Same cheap poll idle mode uses.
            await transitionState(to: .error(.general("External core process is no longer available")))
            startAttachWatch()
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
