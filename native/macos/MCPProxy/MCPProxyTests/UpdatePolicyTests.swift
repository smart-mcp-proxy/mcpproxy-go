// UpdatePolicyTests.swift
// MCPProxyTests
//
// Spec 092 FR-015 — the kill switches. Every branch of the precedence between
// the tray's environment, the CI rule and the core-reported policy, plus the
// one rule that is NOT in the resolver: a user-initiated check is always
// allowed.

import XCTest
@testable import MCPProxy

final class UpdatePolicyTests: XCTestCase {

    private func resolve(
        core: CoreUpdatePolicy? = nil,
        env: [String: String] = [:]
    ) -> EffectiveUpdatePolicy {
        UpdatePolicyResolver.resolve(core: core, environment: env)
    }

    // MARK: - No core yet

    func testNoCorePolicyBlocksUnattendedChecksUntilOneArrives() {
        let policy = resolve()
        XCTAssertFalse(policy.automaticChecksAllowed,
                       "FR-015: the policy is a contract, not an inference from missing "
                       + "data. Checking before it arrives is checking under a policy the "
                       + "user may have switched off")
        XCTAssertFalse(policy.disabledReason.isEmpty, "the wait must be loggable")
        XCTAssertFalse(policy.nudgesSuppressed,
                       "nothing has been found yet, so there is nothing to suppress")
        XCTAssertEqual(policy.channel, .stable)
        XCTAssertEqual(policy, EffectiveUpdatePolicy.awaitingCore)
    }

    func testAPre092CoreKeepsItsPreviousPermissiveBehaviour() {
        // The tray stamps this the moment it reaches a core that reports no
        // update_policy, which is how "an old core said nothing" stays
        // distinguishable from "we have not asked yet".
        let policy = resolve(core: .legacyDefault)
        XCTAssertTrue(policy.automaticChecksAllowed,
                      "disabling updates on a version skew would be a worse failure than "
                      + "checking once")
        XCTAssertFalse(policy.nudgesSuppressed)
        XCTAssertEqual(policy.channel, .stable)
    }

    // MARK: - Tray environment kill switch

    func testDisableEnvironmentVariableStopsAutomaticChecks() {
        let policy = resolve(
            core: CoreUpdatePolicy(enabled: true, channel: "stable", nudgesSuppressed: false),
            env: ["MCPPROXY_DISABLE_AUTO_UPDATE": "true"]
        )
        XCTAssertFalse(policy.automaticChecksAllowed)
        XCTAssertTrue(policy.nudgesSuppressed)
        XCTAssertEqual(policy.disabledReason, "MCPPROXY_DISABLE_AUTO_UPDATE=true")
    }

    func testDisableEnvironmentVariableWinsOverAnEnabledCore() {
        let policy = resolve(
            core: CoreUpdatePolicy(enabled: true, channel: "rc", nudgesSuppressed: false),
            env: ["MCPPROXY_DISABLE_AUTO_UPDATE": "true"]
        )
        XCTAssertFalse(policy.automaticChecksAllowed)
        XCTAssertEqual(policy.channel, .rc, "the channel survives — only checking is off")
    }

    func testOtherValuesOfTheEnvironmentVariableAreNotAKillSwitch() {
        // The core compares against exactly "true"; the tray must not be more
        // eager than the process it mirrors.
        for value in ["1", "yes", "TRUE ", ""] {
            let policy = resolve(core: .legacyDefault,
                                 env: ["MCPPROXY_DISABLE_AUTO_UPDATE": value])
            XCTAssertTrue(policy.automaticChecksAllowed,
                          "\"\(value)\" must not disable updates")
        }
    }

    // MARK: - CI

    func testCISuppressesNudgesAndUnattendedChecks() {
        for value in ["true", "TRUE", "1"] {
            let policy = resolve(env: ["CI": value])
            XCTAssertFalse(policy.automaticChecksAllowed,
                           "CI=\(value): a scheduled check exists to produce a nudge nobody "
                           + "will read")
            XCTAssertTrue(policy.nudgesSuppressed)
        }
    }

    func testNonCIValuesDoNotSuppress() {
        let policy = resolve(core: .legacyDefault, env: ["CI": "false"])
        XCTAssertTrue(policy.automaticChecksAllowed)
        XCTAssertFalse(policy.nudgesSuppressed)
    }

    // MARK: - Core-reported policy

    func testCoreDisabledStopsAutomaticChecks() {
        let policy = resolve(
            core: CoreUpdatePolicy(enabled: false, channel: "stable", nudgesSuppressed: false)
        )
        XCTAssertFalse(policy.automaticChecksAllowed)
        XCTAssertTrue(policy.nudgesSuppressed)
        XCTAssertFalse(policy.disabledReason.isEmpty, "the reason must be loggable")
    }

    func testCoreNudgeSuppressionDoesNotDisableChecking() {
        let policy = resolve(
            core: CoreUpdatePolicy(enabled: true, channel: "stable", nudgesSuppressed: true)
        )
        XCTAssertTrue(policy.automaticChecksAllowed,
                      "Spec 079 FR-019: suppression is about UI, not about the check")
        XCTAssertTrue(policy.nudgesSuppressed)
    }

    // MARK: - Channels (FR-014)

    func testStableChannelAcceptsOnlyTheDefaultSparkleChannel() {
        let policy = resolve(
            core: CoreUpdatePolicy(enabled: true, channel: "stable", nudgesSuppressed: false)
        )
        XCTAssertEqual(policy.channel, .stable)
        XCTAssertTrue(policy.channel.allowedSparkleChannels.isEmpty,
                      "an empty set is Sparkle's 'default channel only' — stable users must "
                      + "never be offered an RC")
    }

    func testRCChannelAlsoAcceptsBeta() {
        let policy = resolve(
            core: CoreUpdatePolicy(enabled: true, channel: "rc", nudgesSuppressed: false)
        )
        XCTAssertEqual(policy.channel, .rc)
        XCTAssertEqual(policy.channel.allowedSparkleChannels, ["beta"])
    }

    func testUnknownChannelFallsBackToStable() {
        for value in ["nightly", "", "STABLE", "beta"] {
            let channel = UpdateChannel(apiValue: value)
            if value.lowercased() == "rc" { continue }
            XCTAssertEqual(channel, .stable,
                           "\"\(value)\" must not be read as 'offer prereleases'")
        }
        XCTAssertEqual(UpdateChannel(apiValue: "RC"), .rc, "case-insensitive")
        XCTAssertEqual(UpdateChannel(apiValue: nil), .stable)
    }
}
