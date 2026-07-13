// CoreLaunchPolicyTests.swift
// MCPProxyTests
//
// GH #410 — "Start Core when app opens" preference.
//
// The tray is a launcher as well as a UI. This preference is the ONE bit of
// launcher state it persists: may it SPAWN a core? It deliberately says nothing
// about whether a core is running — that is discovered live from the socket on
// every launch, so the tray still holds no core state.
//
// Invariants under test:
//   1. Unset  => spawning allowed (existing users see no behaviour change).
//   2. Off    => spawning refused, and the choice round-trips through UserDefaults.
//   3. The MCPPROXY_TRAY_SKIP_CORE env var (parity with the Go tray) pins the
//      policy OFF regardless of the stored preference.
//   4. Attaching to an already-running core is NEVER gated by this policy.

import XCTest
@testable import MCPProxy

final class CoreLaunchPolicyTests: XCTestCase {

    private func makeDefaults(_ name: String) -> UserDefaults {
        let suite = "MCPProxyTests.CoreLaunchPolicy.\(name)"
        let defaults = UserDefaults(suiteName: suite)!
        defaults.removePersistentDomain(forName: suite)
        addTeardownBlock { defaults.removeSuite(named: suite) }
        return defaults
    }

    // MARK: - Default

    /// Back-compat: an existing install has no stored preference, and MUST keep
    /// starting the core on launch exactly as before.
    func testDefaultsToStartingTheCore() {
        let policy = CoreLaunchPolicy(defaults: makeDefaults("default"), environment: [:])

        XCTAssertTrue(policy.startCoreOnLaunch, "unset preference must default to ON")
        XCTAssertTrue(policy.maySpawnCore, "an existing install must keep auto-starting the core")
        XCTAssertFalse(policy.isPinnedOffByEnvironment)
    }

    // MARK: - Round-trip

    func testPreferenceRoundTrips() {
        let defaults = makeDefaults("roundtrip")
        var policy = CoreLaunchPolicy(defaults: defaults, environment: [:])

        policy.startCoreOnLaunch = false
        XCTAssertFalse(policy.maySpawnCore, "with the toggle off the tray must not spawn a core")

        // A fresh policy over the same domain sees the persisted choice — this is
        // the whole point of #410: the choice must survive a relaunch.
        let reloaded = CoreLaunchPolicy(defaults: defaults, environment: [:])
        XCTAssertFalse(reloaded.startCoreOnLaunch, "the choice must survive a relaunch")
        XCTAssertFalse(reloaded.maySpawnCore)

        policy.startCoreOnLaunch = true
        XCTAssertTrue(CoreLaunchPolicy(defaults: defaults, environment: [:]).maySpawnCore)
    }

    // MARK: - Environment override

    /// MCPPROXY_TRAY_SKIP_CORE is the Go tray's existing flag (main.go
    /// shouldSkipCoreLaunch). Honour the same name so a user who runs the core
    /// under launchd/brew can pin the tray to attach-only, and surface that the
    /// setting is env-pinned so the UI can disable the toggle rather than lie.
    func testEnvironmentPinsPolicyOff() {
        for raw in ["1", "true", "TRUE", " true "] {
            let policy = CoreLaunchPolicy(
                defaults: makeDefaults("env-\(raw.trimmingCharacters(in: .whitespaces))"),
                environment: ["MCPPROXY_TRAY_SKIP_CORE": raw]
            )
            XCTAssertFalse(policy.maySpawnCore, "MCPPROXY_TRAY_SKIP_CORE=\(raw) must forbid spawning")
            XCTAssertTrue(policy.isPinnedOffByEnvironment, "the UI must be able to show the toggle as env-pinned")
        }
    }

    /// The env var must override even an explicitly-ON stored preference.
    func testEnvironmentOverridesStoredPreference() {
        let defaults = makeDefaults("env-overrides")
        var policy = CoreLaunchPolicy(defaults: defaults, environment: [:])
        policy.startCoreOnLaunch = true

        let pinned = CoreLaunchPolicy(defaults: defaults, environment: ["MCPPROXY_TRAY_SKIP_CORE": "1"])
        XCTAssertTrue(pinned.startCoreOnLaunch, "the stored preference itself is unchanged")
        XCTAssertFalse(pinned.maySpawnCore, "but the environment wins for the effective policy")
    }

    /// Values that are not truthy leave the policy alone.
    func testUnsetOrFalsyEnvironmentIsIgnored() {
        for raw in ["0", "false", "", "no"] {
            let policy = CoreLaunchPolicy(
                defaults: makeDefaults("env-falsy-\(raw)"),
                environment: ["MCPPROXY_TRAY_SKIP_CORE": raw]
            )
            XCTAssertTrue(policy.maySpawnCore, "MCPPROXY_TRAY_SKIP_CORE=\(raw) must not disable the core")
            XCTAssertFalse(policy.isPinnedOffByEnvironment)
        }
    }
}

// MARK: - Ownership invariants (the two latent bugs #410 would make reachable)

final class CoreOwnershipTests: XCTestCase {

    /// The tray must terminate ONLY a core it spawned. Today this holds by
    /// accident — `process` is nil for an attached core — rather than by an
    /// ownership check. Make it explicit so no refactor can turn tray-quit into
    /// "kill the user's launchd-managed core".
    func testOnlyTrayManagedCoreIsTerminated() {
        XCTAssertTrue(CoreOwnership.trayManaged.shouldTerminateOnShutdown)
        XCTAssertFalse(CoreOwnership.externalAttached.shouldTerminateOnShutdown,
                       "a core we did not spawn must survive tray quit")
    }

    /// "Stop MCPProxy Core" on an ATTACHED core cannot actually stop it (the
    /// core exposes no shutdown endpoint, and we hold no PID for it). Today the
    /// menu claims to stop it and silently leaves it running. The label must
    /// tell the truth instead.
    func testStopActionLabelsMatchOwnership() {
        XCTAssertEqual(CoreOwnership.trayManaged.stopActionTitle, "Stop MCPProxy Core")
        XCTAssertEqual(CoreOwnership.externalAttached.stopActionTitle, "Disconnect from Core")
    }
}
