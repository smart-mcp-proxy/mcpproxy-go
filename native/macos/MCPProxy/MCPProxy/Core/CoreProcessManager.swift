// CoreProcessManager.swift
// MCPProxy
//
// Manages the lifecycle of the mcpproxy core process: launching, monitoring,
// SSE event streaming, state refresh, and graceful shutdown.
//
// The manager is an actor to ensure all state mutations are serialized.

import Foundation

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
    private var apiClient: APIClient?
    private var sseClient: SSEClient?
    private var sseTask: Task<Void, Never>?
    private var refreshTask: Task<Void, Never>?
    private var retryCount: Int = 0
    private let maxRetries: Int = 3
    private let notificationService: NotificationService
    private let reconnectionPolicy: ReconnectionPolicy

    /// Captured stderr output from the core process for error diagnostics.
    private var stderrBuffer: String = ""

    /// The resolved path to the mcpproxy binary.
    private var coreBinaryPath: String?

    /// API key generated for this session's core communication.
    private var sessionAPIKey: String?

    /// Socket path for the core process.
    private let socketPath: String

    // MARK: - Initialization

    init(
        appState: AppState,
        notificationService: NotificationService,
        reconnectionPolicy: ReconnectionPolicy = .default
    ) {
        self.appState = appState
        self.notificationService = notificationService
        self.reconnectionPolicy = reconnectionPolicy

        // Compute socket path: ~/.mcpproxy/mcpproxy.sock
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        self.socketPath = "\(home)/.mcpproxy/mcpproxy.sock"
    }

    // MARK: - Public API

    /// Start the core process and connect to it.
    func start() async {
        // Check if an external core is already running on the socket
        if SocketTransport.isSocketAvailable(path: socketPath) {
            await attachToExternalCore()
            return
        }

        // Launch our own core
        await MainActor.run { appState.ownership = .trayManaged }
        await launchAndConnect()
    }

    /// Gracefully shut down the core process and all connections.
    func shutdown() async {
        await transitionState(to: .shuttingDown)

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

    /// Retry launching the core after an error.
    func retry() async {
        retryCount = 0
        stderrBuffer = ""

        // Clean up any existing process
        if let process, process.isRunning {
            kill(process.processIdentifier, SIGTERM)
            process.waitUntilExit()
        }
        self.process = nil

        await launchAndConnect()
    }

    // MARK: - Private: Attach to External Core

    /// Attach to an already-running core process on the socket.
    private func attachToExternalCore() async {
        await MainActor.run { appState.ownership = .externalAttached }
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

            // Generate a session API key
            let apiKey = generateAPIKey()
            sessionAPIKey = apiKey

            // Launch the process
            try await launchCore(binaryPath: binaryPath, apiKey: apiKey)

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
            await handleCoreError(error)
        } catch {
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
    private func launchCore(binaryPath: String, apiKey: String) async throws {
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: binaryPath)
        proc.arguments = ["serve"]

        // Pass environment with the generated API key
        var env = ProcessInfo.processInfo.environment
        env["MCPPROXY_API_KEY"] = apiKey
        // Enable socket communication
        env["MCPPROXY_SOCKET"] = "true"
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
        let client = APIClient(
            socketPath: socketPath,
            baseURL: "http://127.0.0.1:8080",
            apiKey: sessionAPIKey
        )

        // Verify the core is ready
        let ready = try await client.ready()
        guard ready else {
            throw CoreError.general("Core reported not ready")
        }

        // Fetch version info
        let info = try await client.info()
        await MainActor.run {
            appState.version = info.version
            if let update = info.update, update.available, let latest = update.latestVersion {
                appState.updateAvailable = latest
            }
        }

        apiClient = client

        // Create SSE client
        sseClient = SSEClient(
            socketPath: socketPath,
            baseURL: "http://127.0.0.1:8080",
            apiKey: sessionAPIKey
        )
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
    private func handleSSEEvent(_ event: SSEEvent) async {
        switch event.event {
        case "status":
            // Full status snapshot; refresh all server state
            await refreshState()

        case "servers.changed":
            // Server list changed; re-fetch servers
            await refreshServers()

        case "config.reloaded":
            // Configuration reloaded; refresh everything
            await refreshState()

        case "activity":
            // New activity; refresh recent activity
            await refreshActivity()

        case "ping":
            // Keepalive; no action needed
            break

        default:
            // Unknown event type; refresh servers as a safe default
            await refreshServers()
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
            await transitionState(to: .reconnecting(attempt: 1))
            await attemptReconnection()
        }
    }

    // MARK: - Private: State Refresh

    /// Start a periodic refresh task that polls servers every 30 seconds.
    private func startPeriodicRefresh() {
        refreshTask?.cancel()
        refreshTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 30_000_000_000) // 30s
                guard !Task.isCancelled else { break }
                await self?.refreshState()
            }
        }
    }

    /// Fetch full state from the core and update appState.
    private func refreshState() async {
        await refreshServers()
        await refreshActivity()
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

    // MARK: - Private: Process Exit Handling

    /// Handle the core process exiting.
    private func handleProcessExit(status: Int32) async {
        let stderr = stderrBuffer

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
            // External core is gone and we don't own it
            await transitionState(to: .error(.general("External core process is no longer available")))
        }
    }

    // MARK: - Private: State Transition

    /// Transition the core state via the main actor.
    private func transitionState(to newState: CoreState) async {
        await appState.transition(to: newState)
    }

    // MARK: - Private: API Key Generation

    /// Generate a cryptographically secure random API key (32 bytes, hex-encoded).
    private func generateAPIKey() -> String {
        var bytes = [UInt8](repeating: 0, count: 32)
        _ = SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes)
        return bytes.map { String(format: "%02x", $0) }.joined()
    }
}
