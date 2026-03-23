import XCTest
@testable import MCPProxy

final class CoreStateTests: XCTestCase {

    // MARK: - Valid Transitions

    func testIdleToLaunching() {
        let state = CoreState.idle
        let next = state.transitionToLaunching()
        XCTAssertEqual(next, .launching)
    }

    func testLaunchingToWaitingForCore() {
        let state = CoreState.launching
        let next = state.transitionToWaitingForCore()
        XCTAssertEqual(next, .waitingForCore)
    }

    func testWaitingForCoreToConnected() {
        let state = CoreState.waitingForCore
        let next = state.transitionToConnected()
        XCTAssertEqual(next, .connected)
    }

    func testConnectedToReconnecting() {
        let state = CoreState.connected
        let next = state.transitionToReconnecting(attempt: 1)
        XCTAssertEqual(next, .reconnecting(attempt: 1))
    }

    func testReconnectingToReconnectingNextAttempt() {
        let state = CoreState.reconnecting(attempt: 1)
        let next = state.transitionToReconnecting(attempt: 2)
        XCTAssertEqual(next, .reconnecting(attempt: 2))
    }

    func testReconnectingToConnected() {
        let state = CoreState.reconnecting(attempt: 3)
        let next = state.transitionToConnected()
        XCTAssertEqual(next, .connected)
    }

    func testConnectedToShuttingDown() {
        let state = CoreState.connected
        let next = state.transitionToShuttingDown()
        XCTAssertEqual(next, .shuttingDown)
    }

    func testShuttingDownToIdle() {
        let state = CoreState.shuttingDown
        let next = state.transitionToIdle()
        XCTAssertEqual(next, .idle)
    }

    func testErrorToIdle() {
        let state = CoreState.error(.portConflict)
        let next = state.transitionToIdle()
        XCTAssertEqual(next, .idle)
    }

    func testErrorToLaunching() {
        let state = CoreState.error(.configError)
        let next = state.transitionToLaunching()
        XCTAssertEqual(next, .launching)
    }

    func testLaunchingToError() {
        let state = CoreState.launching
        let next = state.transitionToError(.startupTimeout)
        XCTAssertEqual(next, .error(.startupTimeout))
    }

    func testWaitingForCoreToError() {
        let state = CoreState.waitingForCore
        let next = state.transitionToError(.portConflict)
        XCTAssertEqual(next, .error(.portConflict))
    }

    func testConnectedToError() {
        let state = CoreState.connected
        let next = state.transitionToError(.databaseLocked)
        XCTAssertEqual(next, .error(.databaseLocked))
    }

    func testReconnectingToError() {
        let state = CoreState.reconnecting(attempt: 5)
        let next = state.transitionToError(.maxRetriesExceeded)
        XCTAssertEqual(next, .error(.maxRetriesExceeded))
    }

    func testLaunchingToShuttingDown() {
        let state = CoreState.launching
        let next = state.transitionToShuttingDown()
        XCTAssertEqual(next, .shuttingDown)
    }

    func testErrorToShuttingDown() {
        let state = CoreState.error(.general("something went wrong"))
        let next = state.transitionToShuttingDown()
        XCTAssertEqual(next, .shuttingDown)
    }

    // MARK: - Invalid Transitions

    func testIdleToConnectedIsInvalid() {
        let state = CoreState.idle
        XCTAssertNil(state.transitionToConnected())
    }

    func testIdleToWaitingForCoreIsInvalid() {
        let state = CoreState.idle
        XCTAssertNil(state.transitionToWaitingForCore())
    }

    func testIdleToReconnectingIsInvalid() {
        let state = CoreState.idle
        XCTAssertNil(state.transitionToReconnecting(attempt: 1))
    }

    func testIdleToErrorIsInvalid() {
        let state = CoreState.idle
        XCTAssertNil(state.transitionToError(.portConflict))
    }

    func testIdleToShuttingDownIsInvalid() {
        let state = CoreState.idle
        XCTAssertNil(state.transitionToShuttingDown())
    }

    func testIdleToIdleIsInvalid() {
        let state = CoreState.idle
        XCTAssertNil(state.transitionToIdle())
    }

    func testLaunchingToConnectedIsInvalid() {
        let state = CoreState.launching
        XCTAssertNil(state.transitionToConnected())
    }

    func testLaunchingToReconnectingIsInvalid() {
        let state = CoreState.launching
        XCTAssertNil(state.transitionToReconnecting(attempt: 1))
    }

    func testLaunchingToLaunchingIsInvalid() {
        let state = CoreState.launching
        XCTAssertNil(state.transitionToLaunching())
    }

    func testWaitingForCoreToLaunchingIsInvalid() {
        let state = CoreState.waitingForCore
        XCTAssertNil(state.transitionToLaunching())
    }

    func testWaitingForCoreToReconnectingIsInvalid() {
        let state = CoreState.waitingForCore
        XCTAssertNil(state.transitionToReconnecting(attempt: 1))
    }

    func testConnectedToLaunchingIsInvalid() {
        let state = CoreState.connected
        XCTAssertNil(state.transitionToLaunching())
    }

    func testConnectedToWaitingForCoreIsInvalid() {
        let state = CoreState.connected
        XCTAssertNil(state.transitionToWaitingForCore())
    }

    func testConnectedToIdleIsInvalid() {
        let state = CoreState.connected
        XCTAssertNil(state.transitionToIdle())
    }

    func testShuttingDownToLaunchingIsInvalid() {
        let state = CoreState.shuttingDown
        XCTAssertNil(state.transitionToLaunching())
    }

    func testShuttingDownToConnectedIsInvalid() {
        let state = CoreState.shuttingDown
        XCTAssertNil(state.transitionToConnected())
    }

    func testShuttingDownToErrorIsInvalid() {
        let state = CoreState.shuttingDown
        XCTAssertNil(state.transitionToError(.portConflict))
    }

    func testShuttingDownToShuttingDownIsInvalid() {
        let state = CoreState.shuttingDown
        XCTAssertNil(state.transitionToShuttingDown())
    }

    // MARK: - CoreError.fromExitCode

    func testExitCode2IsPortConflict() {
        let error = CoreError.fromExitCode(2)
        XCTAssertEqual(error, .portConflict)
    }

    func testExitCode3IsDatabaseLocked() {
        let error = CoreError.fromExitCode(3)
        XCTAssertEqual(error, .databaseLocked)
    }

    func testExitCode4IsConfigError() {
        let error = CoreError.fromExitCode(4)
        XCTAssertEqual(error, .configError)
    }

    func testExitCode5IsPermissionError() {
        let error = CoreError.fromExitCode(5)
        XCTAssertEqual(error, .permissionError)
    }

    func testExitCode1IsGeneralWithStderr() {
        let error = CoreError.fromExitCode(1, stderr: "unexpected failure")
        XCTAssertEqual(error, .general("unexpected failure"))
    }

    func testExitCode1IsGeneralWithFallbackMessage() {
        let error = CoreError.fromExitCode(1, stderr: "")
        XCTAssertEqual(error, .general("Exit code 1"))
    }

    func testExitCode0IsGeneralWithFallbackMessage() {
        let error = CoreError.fromExitCode(0, stderr: "")
        XCTAssertEqual(error, .general("Exit code 0"))
    }

    func testExitCode127IsGeneralWithStderr() {
        let error = CoreError.fromExitCode(127, stderr: "  command not found  \n")
        XCTAssertEqual(error, .general("command not found"))
    }

    func testExitCodeWhitespaceOnlyStderrUsesFallback() {
        let error = CoreError.fromExitCode(99, stderr: "   \n  \t  ")
        XCTAssertEqual(error, .general("Exit code 99"))
    }

    // MARK: - CoreError.isRetryable

    func testPortConflictIsNotRetryable() {
        XCTAssertFalse(CoreError.portConflict.isRetryable)
    }

    func testDatabaseLockedIsRetryable() {
        XCTAssertTrue(CoreError.databaseLocked.isRetryable)
    }

    func testConfigErrorIsNotRetryable() {
        XCTAssertFalse(CoreError.configError.isRetryable)
    }

    func testPermissionErrorIsNotRetryable() {
        XCTAssertFalse(CoreError.permissionError.isRetryable)
    }

    func testGeneralIsRetryable() {
        XCTAssertTrue(CoreError.general("some failure").isRetryable)
    }

    func testStartupTimeoutIsRetryable() {
        XCTAssertTrue(CoreError.startupTimeout.isRetryable)
    }

    func testMaxRetriesExceededIsNotRetryable() {
        XCTAssertFalse(CoreError.maxRetriesExceeded.isRetryable)
    }

    // MARK: - CoreError.userMessage

    func testAllErrorsHaveNonEmptyUserMessage() {
        let errors: [CoreError] = [
            .portConflict,
            .databaseLocked,
            .configError,
            .permissionError,
            .general("test error"),
            .startupTimeout,
            .maxRetriesExceeded,
        ]
        for error in errors {
            XCTAssertFalse(error.userMessage.isEmpty, "userMessage should not be empty for \(error)")
        }
    }

    func testPortConflictUserMessage() {
        XCTAssertEqual(CoreError.portConflict.userMessage, "Port is already in use")
    }

    func testDatabaseLockedUserMessage() {
        XCTAssertEqual(CoreError.databaseLocked.userMessage, "Database is locked by another process")
    }

    func testConfigErrorUserMessage() {
        XCTAssertEqual(CoreError.configError.userMessage, "Configuration file is invalid")
    }

    func testPermissionErrorUserMessage() {
        XCTAssertEqual(CoreError.permissionError.userMessage, "Insufficient permissions")
    }

    func testGeneralUserMessageUsesProvidedString() {
        XCTAssertEqual(CoreError.general("custom failure").userMessage, "custom failure")
    }

    func testStartupTimeoutUserMessage() {
        XCTAssertEqual(CoreError.startupTimeout.userMessage, "Core did not start in time")
    }

    func testMaxRetriesExceededUserMessage() {
        XCTAssertEqual(CoreError.maxRetriesExceeded.userMessage, "Maximum reconnection attempts exceeded")
    }

    // MARK: - CoreError.remediationHint

    func testAllErrorsHaveNonEmptyRemediationHint() {
        let errors: [CoreError] = [
            .portConflict,
            .databaseLocked,
            .configError,
            .permissionError,
            .general("test"),
            .startupTimeout,
            .maxRetriesExceeded,
        ]
        for error in errors {
            XCTAssertFalse(error.remediationHint.isEmpty, "remediationHint should not be empty for \(error)")
        }
    }

    func testPortConflictRemediationHintMentionsActivityMonitor() {
        XCTAssertTrue(CoreError.portConflict.remediationHint.contains("Activity Monitor"))
    }

    func testDatabaseLockedRemediationHintMentionsConfigDB() {
        XCTAssertTrue(CoreError.databaseLocked.remediationHint.contains("config.db"))
    }

    func testConfigErrorRemediationHintMentionsDoctor() {
        XCTAssertTrue(CoreError.configError.remediationHint.contains("mcpproxy doctor"))
    }

    func testPermissionErrorRemediationHintMentionsSystemSettings() {
        XCTAssertTrue(CoreError.permissionError.remediationHint.contains("System Settings"))
    }

    func testGeneralRemediationHintMentionsLogs() {
        XCTAssertTrue(CoreError.general("fail").remediationHint.contains("logs"))
    }

    // MARK: - canLaunch

    func testCanLaunchFromIdle() {
        XCTAssertTrue(CoreState.idle.canLaunch)
    }

    func testCanLaunchFromError() {
        XCTAssertTrue(CoreState.error(.portConflict).canLaunch)
    }

    func testCannotLaunchFromLaunching() {
        XCTAssertFalse(CoreState.launching.canLaunch)
    }

    func testCannotLaunchFromWaitingForCore() {
        XCTAssertFalse(CoreState.waitingForCore.canLaunch)
    }

    func testCannotLaunchFromConnected() {
        XCTAssertFalse(CoreState.connected.canLaunch)
    }

    func testCannotLaunchFromReconnecting() {
        XCTAssertFalse(CoreState.reconnecting(attempt: 1).canLaunch)
    }

    func testCannotLaunchFromShuttingDown() {
        XCTAssertFalse(CoreState.shuttingDown.canLaunch)
    }

    // MARK: - isOperational

    func testConnectedIsOperational() {
        XCTAssertTrue(CoreState.connected.isOperational)
    }

    func testReconnectingIsOperational() {
        XCTAssertTrue(CoreState.reconnecting(attempt: 2).isOperational)
    }

    func testIdleIsNotOperational() {
        XCTAssertFalse(CoreState.idle.isOperational)
    }

    func testLaunchingIsNotOperational() {
        XCTAssertFalse(CoreState.launching.isOperational)
    }

    func testWaitingForCoreIsNotOperational() {
        XCTAssertFalse(CoreState.waitingForCore.isOperational)
    }

    func testErrorIsNotOperational() {
        XCTAssertFalse(CoreState.error(.configError).isOperational)
    }

    func testShuttingDownIsNotOperational() {
        XCTAssertFalse(CoreState.shuttingDown.isOperational)
    }

    // MARK: - canShutDown

    func testCanShutDownFromConnected() {
        XCTAssertTrue(CoreState.connected.canShutDown)
    }

    func testCanShutDownFromLaunching() {
        XCTAssertTrue(CoreState.launching.canShutDown)
    }

    func testCanShutDownFromWaitingForCore() {
        XCTAssertTrue(CoreState.waitingForCore.canShutDown)
    }

    func testCanShutDownFromReconnecting() {
        XCTAssertTrue(CoreState.reconnecting(attempt: 1).canShutDown)
    }

    func testCanShutDownFromError() {
        XCTAssertTrue(CoreState.error(.portConflict).canShutDown)
    }

    func testCannotShutDownFromIdle() {
        XCTAssertFalse(CoreState.idle.canShutDown)
    }

    func testCannotShutDownFromShuttingDown() {
        XCTAssertFalse(CoreState.shuttingDown.canShutDown)
    }

    // MARK: - displayName

    func testIdleDisplayName() {
        XCTAssertEqual(CoreState.idle.displayName, "Stopped")
    }

    func testLaunchingDisplayName() {
        XCTAssertEqual(CoreState.launching.displayName, "Launching...")
    }

    func testWaitingForCoreDisplayName() {
        XCTAssertEqual(CoreState.waitingForCore.displayName, "Waiting for Core...")
    }

    func testConnectedDisplayName() {
        XCTAssertEqual(CoreState.connected.displayName, "Connected")
    }

    func testReconnectingDisplayName() {
        XCTAssertEqual(CoreState.reconnecting(attempt: 3).displayName, "Reconnecting (3)...")
    }

    func testErrorDisplayNameContainsUserMessage() {
        let state = CoreState.error(.portConflict)
        XCTAssertTrue(state.displayName.contains("Port is already in use"))
    }

    func testShuttingDownDisplayName() {
        XCTAssertEqual(CoreState.shuttingDown.displayName, "Shutting Down...")
    }

    // MARK: - sfSymbolName

    func testIdleSFSymbol() {
        XCTAssertEqual(CoreState.idle.sfSymbolName, "circle")
    }

    func testLaunchingSFSymbol() {
        XCTAssertEqual(CoreState.launching.sfSymbolName, "circle.dashed")
    }

    func testWaitingForCoreSFSymbol() {
        XCTAssertEqual(CoreState.waitingForCore.sfSymbolName, "circle.dashed")
    }

    func testConnectedSFSymbol() {
        XCTAssertEqual(CoreState.connected.sfSymbolName, "circle.fill")
    }

    func testReconnectingSFSymbol() {
        XCTAssertEqual(CoreState.reconnecting(attempt: 1).sfSymbolName, "arrow.triangle.2.circlepath")
    }

    func testErrorSFSymbol() {
        XCTAssertEqual(CoreState.error(.configError).sfSymbolName, "exclamationmark.circle.fill")
    }

    func testShuttingDownSFSymbol() {
        XCTAssertEqual(CoreState.shuttingDown.sfSymbolName, "xmark.circle")
    }

    // MARK: - CoreOwnership

    func testCoreOwnershipEquatable() {
        XCTAssertEqual(CoreOwnership.trayManaged, CoreOwnership.trayManaged)
        XCTAssertEqual(CoreOwnership.externalAttached, CoreOwnership.externalAttached)
        XCTAssertNotEqual(CoreOwnership.trayManaged, CoreOwnership.externalAttached)
    }

    // MARK: - ReconnectionPolicy

    func testDefaultPolicyValues() {
        let policy = ReconnectionPolicy.default
        XCTAssertEqual(policy.baseDelay, 1.0)
        XCTAssertEqual(policy.maxDelay, 30.0)
        XCTAssertEqual(policy.maxAttempts, 10)
        XCTAssertEqual(policy.jitterFactor, 0.2)
    }

    func testPolicyDelayFirstAttemptIsNearBaseDelay() {
        let policy = ReconnectionPolicy(baseDelay: 1.0, maxDelay: 30.0, maxAttempts: 10, jitterFactor: 0.0)
        let delay = policy.delay(forAttempt: 1)
        // With 0 jitter, attempt 1 should be exactly baseDelay * 2^0 = 1.0
        XCTAssertEqual(delay, 1.0, accuracy: 0.001)
    }

    func testPolicyDelayExponentialGrowth() {
        let policy = ReconnectionPolicy(baseDelay: 1.0, maxDelay: 100.0, maxAttempts: 10, jitterFactor: 0.0)
        // attempt 1: 1*2^0 = 1.0
        // attempt 2: 1*2^1 = 2.0
        // attempt 3: 1*2^2 = 4.0
        // attempt 4: 1*2^3 = 8.0
        XCTAssertEqual(policy.delay(forAttempt: 1), 1.0, accuracy: 0.001)
        XCTAssertEqual(policy.delay(forAttempt: 2), 2.0, accuracy: 0.001)
        XCTAssertEqual(policy.delay(forAttempt: 3), 4.0, accuracy: 0.001)
        XCTAssertEqual(policy.delay(forAttempt: 4), 8.0, accuracy: 0.001)
    }

    func testPolicyDelayCappedAtMaxDelay() {
        let policy = ReconnectionPolicy(baseDelay: 1.0, maxDelay: 5.0, maxAttempts: 10, jitterFactor: 0.0)
        // attempt 10: 1*2^9 = 512, capped to 5.0
        let delay = policy.delay(forAttempt: 10)
        XCTAssertEqual(delay, 5.0, accuracy: 0.001)
    }

    func testPolicyDelayWithJitterIsGreaterOrEqualToBase() {
        let policy = ReconnectionPolicy(baseDelay: 2.0, maxDelay: 60.0, maxAttempts: 10, jitterFactor: 0.2)
        // With jitter factor 0.2, the delay should be at least the capped exponential value
        // (jitter is additive: capped + capped * 0.2 * random(0...1))
        for attempt in 1...5 {
            let delay = policy.delay(forAttempt: attempt)
            let exponential = min(2.0 * pow(2.0, Double(attempt - 1)), 60.0)
            XCTAssertGreaterThanOrEqual(delay, exponential,
                "Delay for attempt \(attempt) should be >= exponential base")
            XCTAssertLessThanOrEqual(delay, exponential * 1.2,
                "Delay for attempt \(attempt) should be <= exponential * 1.2")
        }
    }

    // MARK: - Full Lifecycle Transition Chain

    func testFullHappyPathLifecycle() {
        var state = CoreState.idle
        state = state.transitionToLaunching()!
        XCTAssertEqual(state, .launching)

        state = state.transitionToWaitingForCore()!
        XCTAssertEqual(state, .waitingForCore)

        state = state.transitionToConnected()!
        XCTAssertEqual(state, .connected)

        state = state.transitionToShuttingDown()!
        XCTAssertEqual(state, .shuttingDown)

        state = state.transitionToIdle()!
        XCTAssertEqual(state, .idle)
    }

    func testReconnectionCycleLifecycle() {
        var state = CoreState.connected

        state = state.transitionToReconnecting(attempt: 1)!
        XCTAssertEqual(state, .reconnecting(attempt: 1))

        state = state.transitionToReconnecting(attempt: 2)!
        XCTAssertEqual(state, .reconnecting(attempt: 2))

        state = state.transitionToConnected()!
        XCTAssertEqual(state, .connected)
    }

    func testErrorRecoveryLifecycle() {
        var state = CoreState.launching

        state = state.transitionToError(.startupTimeout)!
        XCTAssertEqual(state, .error(.startupTimeout))

        state = state.transitionToLaunching()!
        XCTAssertEqual(state, .launching)

        state = state.transitionToWaitingForCore()!
        XCTAssertEqual(state, .waitingForCore)

        state = state.transitionToConnected()!
        XCTAssertEqual(state, .connected)
    }
}
