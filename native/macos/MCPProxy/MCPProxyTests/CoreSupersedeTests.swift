// CoreSupersedeTests.swift
// MCPProxyTests
//
// Spec 092 Phase 0 — issue #957 ("old version App still after upgrade").
//
// The decision table first, driven from fixtures, because every branch of it
// either kills a process or declines to; then the two properties that cannot
// be expressed as a pure function: that the manager publishes the consent
// prompt it decided on, and that it refuses to signal a pid that is not an
// mcpproxy process.

import XCTest
@testable import MCPProxy

final class CoreSupersedeDecisionTests: XCTestCase {

    /// One row of the decision table.
    private struct Fixture {
        let name: String
        var running: String = "0.53.0"
        var bundled: String? = "0.54.0"
        var launchedBy: String = ""
        var pid: Int32? = 4242
        var ownership: CoreOwnership = .externalAttached
        var alreadyAttempted: Bool = false
        let expected: CoreSupersedeAction
    }

    private func run(_ fixture: Fixture, file: StaticString = #filePath, line: UInt = #line) {
        let report = CoreVersionReport(
            runningVersion: fixture.running,
            launchedBy: fixture.launchedBy,
            pid: fixture.pid
        )
        let decision = CoreSupersede.decide(
            report: report,
            respawnVersion: fixture.bundled,
            ownership: fixture.ownership,
            alreadyAttempted: fixture.alreadyAttempted
        )
        XCTAssertEqual(decision.action, fixture.expected,
                       "\(fixture.name): \(decision.reason)", file: file, line: line)
        XCTAssertFalse(decision.reason.isEmpty,
                       "\(fixture.name): every verdict must carry a reason (FR-006)",
                       file: file, line: line)
    }

    // MARK: - FR-001 / FR-001a: automatic supersede

    func testTrayLaunchedOlderCoreIsSupersededAutomatically() {
        run(Fixture(
            name: "the #957 case: an older tray's core outliving that tray",
            launchedBy: "tray",
            expected: .stopAndRespawn(pid: 4242)
        ))
    }

    /// `installer` provenance means the PKG postinstall launched the app, and
    /// `CoreProcessManager.launchCore` deliberately does not overwrite that
    /// marker on the core it spawns. Treating it as external would leave the
    /// PKG upgrade path — half of #957 — asking for consent it should not need.
    func testInstallerProvenanceCountsAsTrayLaunched() {
        run(Fixture(
            name: "installer-launched core",
            launchedBy: "installer",
            expected: .stopAndRespawn(pid: 4242)
        ))
    }

    func testTrayManagedCoreUsesTheManagedRestartPath() {
        run(Fixture(
            name: "core this tray spawned this session",
            launchedBy: "tray",
            ownership: .trayManaged,
            expected: .restartManaged
        ))
        run(Fixture(
            name: "managed ownership outranks a missing provenance marker",
            launchedBy: "",
            pid: nil,
            ownership: .trayManaged,
            expected: .restartManaged
        ))
    }

    // MARK: - FR-002: consent

    func testUserLaunchedCoreOnlyGetsAnOffer() {
        run(Fixture(
            name: "user-launched core is never killed automatically",
            launchedBy: "",
            expected: .askForConsent(pid: 4242)
        ))
    }

    func testUnrecognisedProvenanceIsTreatedAsUserLaunched() {
        run(Fixture(
            name: "a marker we do not know is not permission",
            launchedBy: "launchd",
            expected: .askForConsent(pid: 4242)
        ))
    }

    func testMissingPIDDowngradesToInstructions() {
        run(Fixture(
            name: "tray provenance but no pid — nothing to signal",
            launchedBy: "tray",
            pid: nil,
            expected: .askForConsent(pid: nil)
        ))
        run(Fixture(
            name: "a pid of 1 or 0 is never a core",
            launchedBy: "tray",
            pid: 1,
            expected: .askForConsent(pid: nil)
        ))
        run(Fixture(
            name: "user-launched core with an unusable pid",
            launchedBy: "",
            pid: 0,
            expected: .askForConsent(pid: nil)
        ))
    }

    // MARK: - FR-005: idempotent, no loops, no downgrades

    func testMatchingVersionsDoNothing() {
        run(Fixture(name: "versions match", running: "0.54.0", bundled: "0.54.0", expected: .none))
        run(Fixture(name: "match modulo a v prefix and build metadata",
                    running: "v0.54.0+abc", bundled: "0.54.0", expected: .none))
        run(Fixture(name: "match, tray-managed", running: "0.54.0", bundled: "0.54.0",
                    launchedBy: "tray", ownership: .trayManaged, expected: .none))
    }

    func testNewerRunningCoreIsNeverDowngraded() {
        run(Fixture(name: "running core is newer", running: "0.55.0", bundled: "0.54.0",
                    launchedBy: "tray", expected: .none))
        run(Fixture(name: "the release outranks the bundled RC",
                    running: "0.54.0", bundled: "0.54.0-rc.3",
                    launchedBy: "tray", ownership: .trayManaged, expected: .none))
    }

    /// FR-006 in the place it matters most: with a string comparison
    /// `0.54.0-rc.10` looks older than `0.54.0-rc.2`, and this row would be a
    /// kill instead of a no-op.
    func testNumericPrereleaseOrderingDrivesTheVerdict() {
        run(Fixture(name: "rc.10 running, rc.2 bundled — no supersede",
                    running: "0.54.0-rc.10", bundled: "0.54.0-rc.2",
                    launchedBy: "tray", expected: .none))
        run(Fixture(name: "rc.2 running, rc.10 bundled — supersede",
                    running: "0.54.0-rc.2", bundled: "0.54.0-rc.10",
                    launchedBy: "tray", expected: .stopAndRespawn(pid: 4242)))
    }

    func testOneAttemptPerSession() {
        run(Fixture(name: "already superseded once", launchedBy: "tray",
                    alreadyAttempted: true, expected: .none))
        run(Fixture(name: "already superseded once, managed", launchedBy: "tray",
                    ownership: .trayManaged, alreadyAttempted: true, expected: .none))
    }

    // MARK: - FR-006: no decision without comparable versions

    func testUncomparableVersionsMeanNoAction() {
        run(Fixture(name: "development build running", running: "development",
                    launchedBy: "tray", expected: .none))
        run(Fixture(name: "core reported no version", running: "",
                    launchedBy: "tray", expected: .none))
        run(Fixture(name: "bundled version unreadable", bundled: "dev",
                    launchedBy: "tray", expected: .none))
    }

    func testNoBundledCoreMeansNothingToSupersedeInto() {
        run(Fixture(name: "no bundled core (dev build / PATH core)", bundled: nil,
                    launchedBy: "tray", expected: .none))
        run(Fixture(name: "empty bundled version", bundled: "",
                    launchedBy: "tray", expected: .none))
    }

    // MARK: - The prompt the menu renders

    func testPromptIsOnlyProducedForTheConsentVerdict() {
        let report = CoreVersionReport(runningVersion: "0.53.0", launchedBy: "", pid: 4242)
        let decision = CoreSupersede.decide(report: report, respawnVersion: "0.54.0",
                                            ownership: .externalAttached, alreadyAttempted: false)
        let prompt = CoreSupersede.prompt(for: decision, report: report, respawnVersion: "0.54.0")
        XCTAssertEqual(prompt, StaleCorePrompt(runningVersion: "0.53.0",
                                               bundledVersion: "0.54.0", pid: 4242))
        XCTAssertEqual(prompt?.menuTitle, "Old core v0.53.0 running — Restart into v0.54.0")

        let quiet = CoreSupersede.decide(report: report, respawnVersion: "0.53.0",
                                         ownership: .externalAttached, alreadyAttempted: false)
        XCTAssertNil(CoreSupersede.prompt(for: quiet, report: report, respawnVersion: "0.53.0"),
                     "a no-action verdict must not leave an offer in the menu")

        let automatic = CoreSupersede.decide(
            report: CoreVersionReport(runningVersion: "0.53.0", launchedBy: "tray", pid: 4242),
            respawnVersion: "0.54.0", ownership: .externalAttached, alreadyAttempted: false
        )
        XCTAssertNil(CoreSupersede.prompt(for: automatic, report: report, respawnVersion: "0.54.0"),
                     "the automatic branch acts; it must not also ask")
    }
}

// MARK: - Killing by pid, safely

final class CoreProcessIdentityTests: XCTestCase {

    /// The guard that stands between "the core died and its pid was recycled"
    /// and "the tray SIGKILLed an unrelated process".
    func testOnlyMCPProxyProcessesAreRecognised() {
        let ownPID = ProcessInfo.processInfo.processIdentifier
        XCTAssertFalse(CoreProcessIdentity.isMCPProxyCore(pid: ownPID),
                       "the test runner is not an mcpproxy core")
        XCTAssertNotNil(CoreProcessIdentity.executablePath(ofPID: ownPID),
                        "the path of our own process must be readable")
    }

    func testDegenerateAndDeadPIDsAreRejected() {
        XCTAssertFalse(CoreProcessIdentity.isMCPProxyCore(pid: 0))
        XCTAssertFalse(CoreProcessIdentity.isMCPProxyCore(pid: 1), "launchd is not a core")
        XCTAssertFalse(CoreProcessIdentity.isRunning(pid: 0))
        XCTAssertFalse(CoreProcessIdentity.isRunning(pid: -1))
    }

    func testLiveProcessIsReportedRunning() {
        XCTAssertTrue(CoreProcessIdentity.isRunning(pid: ProcessInfo.processInfo.processIdentifier))
    }

    func testExitedProcessIsNotReportedRunning() throws {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/bin/sh")
        process.arguments = ["-c", "exit 0"]
        try process.run()
        process.waitUntilExit()
        // The pid is reaped by Foundation, so it no longer names a process.
        XCTAssertFalse(CoreProcessIdentity.isRunning(pid: process.processIdentifier))
    }
}

// MARK: - Which version would a restart bring?

final class BundledCoreResolutionTests: XCTestCase {

    /// An operator who pinned `MCPPROXY_CORE_PATH` has said which binary runs.
    /// Superseding would relaunch that same binary in a circle.
    func testCorePathOverrideDisablesSupersede() {
        XCTAssertNil(BundledCore.respawnVersion(environment: ["MCPPROXY_CORE_PATH": "/tmp/x"]))
    }

    /// The test bundle is not an app bundle, so there is no bundled core — and
    /// the honest answer is nil, not the test runner's version.
    func testNoAppBundleMeansNoBundledCore() {
        XCTAssertNil(BundledCore.binaryPath(bundle: Bundle(for: BundledCoreResolutionTests.self)))
        XCTAssertNil(BundledCore.respawnVersion(bundle: Bundle(for: BundledCoreResolutionTests.self),
                                                environment: [:]))
    }

    func testVersionIsReadByAskingTheBinary() throws {
        let dir = URL(fileURLWithPath: NSTemporaryDirectory())
            .appendingPathComponent("mcpproxy-bundledcore-\(UUID().uuidString.prefix(8))")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        addTeardownBlock { try? FileManager.default.removeItem(at: dir) }

        let binary = dir.appendingPathComponent("mcpproxy")
        try """
        #!/bin/sh
        [ "$1" = version ] || exit 2
        echo '{"version":"v0.54.0","edition":"personal"}'
        """.write(to: binary, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: binary.path)

        XCTAssertEqual(CoreBinaryVersion.read(at: binary.path), "v0.54.0")
    }

    func testUnreadableOrFailingBinaryYieldsNoVersion() throws {
        XCTAssertNil(CoreBinaryVersion.read(at: "/nonexistent/mcpproxy"))

        let dir = URL(fileURLWithPath: NSTemporaryDirectory())
            .appendingPathComponent("mcpproxy-badcore-\(UUID().uuidString.prefix(8))")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        addTeardownBlock { try? FileManager.default.removeItem(at: dir) }

        let failing = dir.appendingPathComponent("mcpproxy")
        try "#!/bin/sh\nexit 3\n".write(to: failing, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: failing.path)
        XCTAssertNil(CoreBinaryVersion.read(at: failing.path))

        let noisy = dir.appendingPathComponent("mcpproxy-noisy")
        try "#!/bin/sh\necho not json\n".write(to: noisy, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: noisy.path)
        XCTAssertNil(CoreBinaryVersion.read(at: noisy.path),
                     "output that is not the version JSON must not be guessed at")
    }
}
