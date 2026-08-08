// CoreSupersedeAttachTests.swift
// MCPProxyTests
//
// Spec 092 FR-001/FR-002 through the REAL attach path: a core on a Unix socket
// answering `/api/v1/info`, the manager attaching to it, and the supersede
// verdict landing in `AppState` (or not).
//
// The decision table is covered by `CoreSupersedeDecisionTests`. What can only
// be shown here is the wiring: that a version report actually reaches the
// decision, that the prompt is published where the menu reads it, and that a
// verdict which would signal a process refuses to signal one that is not a
// core.

import XCTest
@testable import MCPProxy

final class CoreSupersedeAttachTests: XCTestCase {

    private var stub: UnixSocketHTTPStub?
    private var manager: CoreProcessManager?

    override func tearDown() async throws {
        if let manager { await manager.shutdown() }
        manager = nil
        stub?.stop()
        stub = nil
        try await super.tearDown()
    }

    /// Attach to a stub core with a controllable info payload.
    /// `maySpawn: false` guarantees no real binary can ever be launched here.
    @discardableResult
    private func attach(
        coreVersion: String,
        launchedBy: String?,
        pid: Int32?,
        bundledVersion: String?
    ) async throws -> AppState {
        let stub = UnixSocketHTTPStub.healthyCore(
            version: coreVersion, launchedBy: launchedBy, pid: pid
        )
        try stub.start()
        self.stub = stub

        let appState = await MainActor.run { AppState() }
        let manager = CoreProcessManager(
            appState: appState,
            notificationService: NotificationService(deliveryEnabled: false),
            reconnectionPolicy: ReconnectionPolicy(
                baseDelay: 0.05, maxDelay: 0.1, maxAttempts: 2, jitterFactor: 0.0
            ),
            socketPath: stub.path,
            refreshInterval: 60,
            respawnVersionProvider: { bundledVersion }
        )
        self.manager = manager
        await manager.start(maySpawn: false)
        return appState
    }

    // MARK: - FR-002: the consent offer

    func testAttachingToAnOlderUserLaunchedCorePublishesTheOffer() async throws {
        let appState = try await attach(
            coreVersion: "0.53.0", launchedBy: "", pid: 4242, bundledVersion: "0.54.0"
        )

        let prompt = await MainActor.run { appState.staleCorePrompt }
        XCTAssertEqual(prompt?.runningVersion, "0.53.0")
        XCTAssertEqual(prompt?.bundledVersion, "0.54.0")
        XCTAssertEqual(prompt?.pid, 4242)
        let connected = await MainActor.run { appState.coreState }
        XCTAssertEqual(connected, .connected,
                       "offering a restart must not disturb the connection")
        let attempted = await manager?.didAttemptSupersede
        XCTAssertEqual(attempted, false, "FR-002: no action without an explicit click")
    }

    /// A core from before Spec 092 sends neither field. It must be treated as
    /// user-launched (consent), never as tray-owned.
    func testPre092CoreWithoutProvenanceGetsTheConsentPathOnly() async throws {
        let appState = try await attach(
            coreVersion: "0.53.0", launchedBy: nil, pid: nil, bundledVersion: "0.54.0"
        )

        let prompt = await MainActor.run { appState.staleCorePrompt }
        XCTAssertNotNil(prompt, "an old core must still be surfaced")
        XCTAssertNil(prompt?.pid, "no pid on the wire means the action can only instruct")
        let connected = await MainActor.run { appState.coreState }
        XCTAssertEqual(connected, .connected)
    }

    // MARK: - FR-005: silence when versions match

    func testMatchingVersionsLeaveNoPrompt() async throws {
        let appState = try await attach(
            coreVersion: "0.54.0", launchedBy: "tray", pid: 4242, bundledVersion: "v0.54.0"
        )

        let prompt = await MainActor.run { appState.staleCorePrompt }
        XCTAssertNil(prompt, "FR-005: matching versions must produce no prompt and no churn")
        let connected = await MainActor.run { appState.coreState }
        XCTAssertEqual(connected, .connected)
    }

    func testNoBundledCoreMeansNoPrompt() async throws {
        let appState = try await attach(
            coreVersion: "0.1.0", launchedBy: "tray", pid: 4242, bundledVersion: nil
        )
        let prompt = await MainActor.run { appState.staleCorePrompt }
        XCTAssertNil(prompt, "with nothing better to offer, offering a restart is a lie")
    }

    // MARK: - The kill guard

    /// The automatic branch resolves to `stopAndRespawn`, and the pid it is
    /// handed belongs to the test runner. The tray must recognise that it is
    /// not an mcpproxy process and refuse — surfacing an error instead of
    /// signalling something it does not own.
    func testAutomaticSupersedeRefusesAPIDThatIsNotACore() async throws {
        let foreignPID = ProcessInfo.processInfo.processIdentifier
        let appState = try await attach(
            coreVersion: "0.53.0", launchedBy: "tray", pid: foreignPID, bundledVersion: "0.54.0"
        )

        // Still alive: the guard fired before any signal.
        XCTAssertTrue(CoreProcessIdentity.isRunning(pid: foreignPID))

        let state = await MainActor.run { appState.coreState }
        guard case .error = state else {
            return XCTFail("a refused supersede must surface, not fail silently (got \(state))")
        }
        let attempted = await manager?.didAttemptSupersede
        XCTAssertEqual(attempted, true,
                       "the budget is consumed even on refusal — FR-005 forbids retry loops")
    }

    // MARK: - The consent action

    func testConsentActionDeclinesWhenThereIsNothingToActOn() async throws {
        _ = try await attach(
            coreVersion: "0.54.0", launchedBy: "tray", pid: 4242, bundledVersion: "0.54.0"
        )
        let acted = await manager?.supersedeWithConsent()
        XCTAssertEqual(acted, false, "no prompt means nothing to restart")
    }

    /// A prompt with no pid must report failure so the caller shows
    /// instructions rather than pretending it did something (FR-002).
    func testConsentActionReportsFailureWithoutAPID() async throws {
        _ = try await attach(
            coreVersion: "0.53.0", launchedBy: nil, pid: nil, bundledVersion: "0.54.0"
        )
        let acted = await manager?.supersedeWithConsent()
        XCTAssertEqual(acted, false, "with no pid the tray can only instruct")
    }

    /// Shutting down retracts the offer: a pid nobody re-checked is not
    /// something the menu may still invite the user to signal.
    func testShutdownClearsTheOffer() async throws {
        let appState = try await attach(
            coreVersion: "0.53.0", launchedBy: "", pid: 4242, bundledVersion: "0.54.0"
        )
        let offered = await MainActor.run { appState.staleCorePrompt }
        XCTAssertNotNil(offered)

        await manager?.shutdown()
        manager = nil
        let retracted = await MainActor.run { appState.staleCorePrompt }
        XCTAssertNil(retracted)
    }
}
