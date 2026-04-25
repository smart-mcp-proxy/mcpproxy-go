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
    ///
    /// Strategy: always try to launch our own core first. If the socket already
    /// exists, probe it with an actual API call — a stale socket file from a
    /// killed process will fail the probe, so we remove it and launch fresh.
    func start() async {
        // If socket file exists, check if a real core is behind it
        if SocketTransport.isSocketAvailable(path: socketPath) {
            if await probeExternalCore() {
                // Real core is running — attach to it
                await attachToExternalCore()
                return
            }
            // Stale socket — remove it so our new core can create a fresh one
            try? FileManager.default.removeItem(atPath: socketPath)
        }

        // Launch our own core as a subprocess
        await MainActor.run { appState.ownership = .trayManaged }
        await launchAndConnect()
    }

    /// Probe an existing socket to see if a live core is behind it.
    /// Returns true only if the core responds to an API call.
    private func probeExternalCore() async -> Bool {
        let probeClient = APIClient(socketPath: socketPath)
        do {
            let ready = try await probeClient.ready()
            return ready
        } catch {
            return false
        }
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
        await MainActor.run { appState.apiClient = nil }

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
            // Status events contain inline stats.
            // When connected count changes, re-fetch the full server list
            // to get accurate counts (SSE stats can lag behind actual state).
            if let data = event.data.data(using: .utf8),
               let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let stats = json["upstream_stats"] as? [String: Any] {
                let connected = stats["connected_servers"] as? Int ?? 0
                let total = stats["total_servers"] as? Int ?? 0
                let tools = stats["total_tools"] as? Int ?? 0
                let oldConnected = await MainActor.run { appState.connectedCount }
                // If counts changed, do a full server refresh for accuracy
                if connected != oldConnected {
                    await refreshServers()
                } else {
                    await MainActor.run {
                        if appState.totalServers != total { appState.totalServers = total }
                        if appState.totalTools != tools { appState.totalTools = tools }
                    }
                }
            }

        case "servers.changed":
            // Server list actually changed; re-fetch once
            let oldQuarantined = await MainActor.run { appState.quarantinedToolsCount }
            await refreshServers()
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
            // Configuration reloaded; refresh everything once
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
        await refreshSessions()
        await refreshTokenMetrics()
        await refreshSecurityStatus()
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
                    // Servers are connected — Docker must be working if isolation is enabled
                    // Check via the status endpoint which shows connected servers in containers
                    let servers = try? await apiClient.servers()
                    let hasStdioServers = servers?.contains(where: { $0.connected && $0.protocol == "stdio" }) ?? false
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
    private func handleProcessExit(status: Int32) async {
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
