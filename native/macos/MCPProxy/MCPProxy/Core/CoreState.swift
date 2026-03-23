import Foundation

// MARK: - Core Process Lifecycle States

/// State machine for the mcpproxy core process lifecycle.
/// Transitions are validated — only legal state changes are permitted.
enum CoreState: Equatable {
    case idle
    case launching
    case waitingForCore
    case connected
    case reconnecting(attempt: Int)
    case error(CoreError)
    case shuttingDown

    // MARK: - Transition Helpers

    /// Whether a launch can be initiated from the current state.
    var canLaunch: Bool {
        switch self {
        case .idle, .error:
            return true
        default:
            return false
        }
    }

    /// Whether the core is considered operational (connected or reconnecting).
    var isOperational: Bool {
        switch self {
        case .connected, .reconnecting:
            return true
        default:
            return false
        }
    }

    /// Whether shutdown can be initiated from the current state.
    var canShutDown: Bool {
        switch self {
        case .idle, .shuttingDown:
            return false
        default:
            return true
        }
    }

    /// Human-readable description suitable for menu bar display.
    var displayName: String {
        switch self {
        case .idle:
            return "Stopped"
        case .launching:
            return "Launching..."
        case .waitingForCore:
            return "Waiting for Core..."
        case .connected:
            return "Connected"
        case .reconnecting(let attempt):
            return "Reconnecting (\(attempt))..."
        case .error(let coreError):
            return "Error: \(coreError.userMessage)"
        case .shuttingDown:
            return "Shutting Down..."
        }
    }

    /// SF Symbol name for tray icon state.
    var sfSymbolName: String {
        switch self {
        case .idle:
            return "circle"
        case .launching, .waitingForCore:
            return "circle.dashed"
        case .connected:
            return "circle.fill"
        case .reconnecting:
            return "arrow.triangle.2.circlepath"
        case .error:
            return "exclamationmark.circle.fill"
        case .shuttingDown:
            return "xmark.circle"
        }
    }

    // MARK: - State Transitions

    /// Attempt to transition to `.launching`. Returns the new state or nil if invalid.
    func transitionToLaunching() -> CoreState? {
        guard canLaunch else { return nil }
        return .launching
    }

    /// Attempt to transition to `.waitingForCore`. Valid from `.launching`.
    func transitionToWaitingForCore() -> CoreState? {
        switch self {
        case .launching:
            return .waitingForCore
        default:
            return nil
        }
    }

    /// Attempt to transition to `.connected`. Valid from `.waitingForCore` or `.reconnecting`.
    func transitionToConnected() -> CoreState? {
        switch self {
        case .waitingForCore, .reconnecting:
            return .connected
        default:
            return nil
        }
    }

    /// Attempt to transition to `.reconnecting`. Valid from `.connected` or `.reconnecting`.
    func transitionToReconnecting(attempt: Int) -> CoreState? {
        switch self {
        case .connected, .reconnecting:
            return .reconnecting(attempt: attempt)
        default:
            return nil
        }
    }

    /// Transition to `.error`. Valid from any non-idle, non-shuttingDown state.
    func transitionToError(_ error: CoreError) -> CoreState? {
        switch self {
        case .idle, .shuttingDown:
            return nil
        default:
            return .error(error)
        }
    }

    /// Transition to `.shuttingDown`. Valid from any operational or error state.
    func transitionToShuttingDown() -> CoreState? {
        guard canShutDown else { return nil }
        return .shuttingDown
    }

    /// Transition to `.idle`. Valid from `.shuttingDown` or `.error`.
    func transitionToIdle() -> CoreState? {
        switch self {
        case .shuttingDown, .error:
            return .idle
        default:
            return nil
        }
    }
}

// MARK: - Core Error

/// Specific error types mapped from mcpproxy exit codes.
/// See CLAUDE.md "Exit Codes" section for the canonical mapping.
enum CoreError: Error, Equatable {
    /// Port already in use (exit code 2)
    case portConflict
    /// Database file locked by another process (exit code 3)
    case databaseLocked
    /// Invalid configuration file (exit code 4)
    case configError
    /// Insufficient filesystem permissions (exit code 5)
    case permissionError
    /// Any other exit code or runtime failure
    case general(String)
    /// Core did not become ready within the timeout window
    case startupTimeout
    /// Reconnection attempts exhausted
    case maxRetriesExceeded

    /// Map a process exit code to a typed error.
    /// - Parameters:
    ///   - code: The process exit status.
    ///   - stderr: Captured standard error output, used for `.general` messages.
    static func fromExitCode(_ code: Int32, stderr: String = "") -> CoreError {
        switch code {
        case 2:
            return .portConflict
        case 3:
            return .databaseLocked
        case 4:
            return .configError
        case 5:
            return .permissionError
        default:
            let message = stderr.trimmingCharacters(in: .whitespacesAndNewlines)
            return .general(message.isEmpty ? "Exit code \(code)" : message)
        }
    }

    /// Short, user-facing description of the error.
    var userMessage: String {
        switch self {
        case .portConflict:
            return "Port is already in use"
        case .databaseLocked:
            return "Database is locked by another process"
        case .configError:
            return "Configuration file is invalid"
        case .permissionError:
            return "Insufficient permissions"
        case .general(let message):
            return message
        case .startupTimeout:
            return "Core did not start in time"
        case .maxRetriesExceeded:
            return "Maximum reconnection attempts exceeded"
        }
    }

    /// Actionable hint shown to the user below the error message.
    var remediationHint: String {
        switch self {
        case .portConflict:
            return "Another instance of MCPProxy may be running. Check Activity Monitor or change the listen port in settings."
        case .databaseLocked:
            return "Close any other MCPProxy instances. If the problem persists, delete ~/.mcpproxy/config.db and restart."
        case .configError:
            return "Check ~/.mcpproxy/mcp_config.json for syntax errors. Run 'mcpproxy doctor' for diagnostics."
        case .permissionError:
            return "Ensure the current user has read/write access to ~/.mcpproxy/. Check disk permissions in System Settings."
        case .general:
            return "Check the logs at ~/.mcpproxy/logs/main.log for details."
        case .startupTimeout:
            return "The core process started but did not respond to health checks. Check logs and try restarting."
        case .maxRetriesExceeded:
            return "MCPProxy could not reconnect after multiple attempts. Try restarting from the menu."
        }
    }

    /// Whether the error condition may resolve on its own or with a simple retry.
    var isRetryable: Bool {
        switch self {
        case .portConflict:
            return false // needs manual intervention (kill other process or change port)
        case .databaseLocked:
            return true  // other process may release the lock
        case .configError:
            return false // config must be fixed by the user
        case .permissionError:
            return false // permissions must be fixed by the user
        case .general:
            return true  // transient failures may resolve
        case .startupTimeout:
            return true  // process may just be slow
        case .maxRetriesExceeded:
            return false // already retried many times
        }
    }
}

// MARK: - Core Ownership

/// Describes who owns the core process lifecycle.
enum CoreOwnership: Equatable {
    /// The tray application launched and owns the core process.
    /// On quit, the tray will terminate the core.
    case trayManaged

    /// The core was already running when the tray attached.
    /// On quit, the tray will detach without stopping the core.
    case externalAttached
}

// MARK: - Reconnection Policy

/// Configures exponential backoff for reconnection attempts.
struct ReconnectionPolicy {
    /// Base delay between attempts (doubled each retry).
    let baseDelay: TimeInterval

    /// Maximum delay cap.
    let maxDelay: TimeInterval

    /// Maximum number of reconnection attempts before giving up.
    let maxAttempts: Int

    /// Random jitter factor (0.0 to 1.0) added to each delay.
    let jitterFactor: Double

    /// Default policy: 1s base, 30s max, 10 attempts, 20% jitter.
    static let `default` = ReconnectionPolicy(
        baseDelay: 1.0,
        maxDelay: 30.0,
        maxAttempts: 10,
        jitterFactor: 0.2
    )

    /// Calculate the delay for a given attempt number (1-based).
    func delay(forAttempt attempt: Int) -> TimeInterval {
        let exponential = baseDelay * pow(2.0, Double(attempt - 1))
        let capped = min(exponential, maxDelay)
        let jitter = capped * jitterFactor * Double.random(in: 0.0...1.0)
        return capped + jitter
    }
}
