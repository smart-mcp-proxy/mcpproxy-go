// UpdatePolicy.swift
// MCPProxy
//
// Spec 092 FR-015 — the update policy, as an explicit contract.
//
// Three parties get a vote on whether the tray may check for updates:
//
//   1. the core, over `GET /api/v1/info` → `update_policy` (config
//      `update_check.enabled` / `channel`, plus the core's own environment
//      overrides, already resolved for us);
//   2. the tray's OWN environment — a user who exports
//      MCPPROXY_DISABLE_AUTO_UPDATE expects the menu-bar app to obey it too,
//      and the tray can (and does) run with no core attached at all;
//   3. the context — CI / non-interactive, where a nudge has no one to read it.
//
// Resolving them is a pure function so the precedence is testable without a
// Sparkle framework, a core, or a menu. The one rule that is NOT expressed here
// is the one FR-015 states last: a user-initiated "Check for Updates" is always
// allowed. That is enforced at the call site (`UpdateService.checkForUpdates`),
// because the policy has nothing to say about an action the user just took.

import Foundation

// MARK: - Channel

/// The release channel this install tracks. Mirrors the core's
/// `update_policy.channel` and `docs/prerelease-builds.md`.
enum UpdateChannel: String, Equatable {
    case stable
    case rc

    /// Unrecognized values fall back to `stable`: a channel the tray does not
    /// understand must never be read as "offer this user prereleases".
    init(apiValue: String?) {
        switch apiValue?.lowercased() {
        case "rc": self = .rc
        default: self = .stable
        }
    }

    /// Sparkle channel names allowed for this release channel. Sparkle treats
    /// the EMPTY set as "default channel only" — items with no `sparkle:channel`
    /// tag — which is exactly what a stable user must see. RC users additionally
    /// accept the `beta` channel the prerelease pipeline tags.
    var allowedSparkleChannels: Set<String> {
        switch self {
        case .stable: return []
        case .rc: return ["beta"]
        }
    }
}

// MARK: - What the core said

/// `update_policy` from `GET /api/v1/info` (Spec 092 FR-015).
struct CoreUpdatePolicy: Codable, Equatable {
    let enabled: Bool
    let channel: String
    let nudgesSuppressed: Bool

    enum CodingKeys: String, CodingKey {
        case enabled
        case channel
        case nudgesSuppressed = "nudges_suppressed"
    }
}

// MARK: - The resolved answer

/// What the tray is actually allowed to do right now.
struct EffectiveUpdatePolicy: Equatable {
    /// May the tray run checks nobody asked for (Sparkle's scheduled cycle, and
    /// the legacy GitHub poll)? A user-initiated check ignores this.
    let automaticChecksAllowed: Bool

    /// Must UI surfaces stay quiet? Suppresses the nudge item itself, so an
    /// update found by a user-initiated check is still installable — the user is
    /// standing right there — but nothing appears unasked.
    let nudgesSuppressed: Bool

    /// Which feed channel to accept.
    let channel: UpdateChannel

    /// Why automatic checks are off, for the log and the tooltip. Empty when
    /// they are on.
    let disabledReason: String

    /// The policy in force before a core has been reached, and for cores older
    /// than Spec 092 that report no `update_policy` at all. Permissive, because
    /// that is the behaviour those builds already had — silently disabling
    /// updates on a version skew would be a worse failure than checking once.
    static let permissive = EffectiveUpdatePolicy(
        automaticChecksAllowed: true,
        nudgesSuppressed: false,
        channel: .stable,
        disabledReason: ""
    )
}

// MARK: - Resolution

enum UpdatePolicyResolver {

    /// Environment variable name shared with the core
    /// (`internal/updatecheck.EnvDisableAutoUpdate`). Same name, same meaning:
    /// one export silences both processes.
    static let disableEnvKey = "MCPPROXY_DISABLE_AUTO_UPDATE"

    /// Resolve the effective policy.
    ///
    /// - Parameters:
    ///   - core: `update_policy` from the attached core, or nil when no core has
    ///     been reached yet / the core predates the field.
    ///   - environment: the tray process environment (injected for tests).
    static func resolve(
        core: CoreUpdatePolicy?,
        environment: [String: String]
    ) -> EffectiveUpdatePolicy {
        let channel = UpdateChannel(apiValue: core?.channel)

        // The tray's own kill switch. Checked first because it is the most
        // explicit statement of intent available, and it works with no core.
        if environment[disableEnvKey]?.lowercased() == "true" {
            return EffectiveUpdatePolicy(
                automaticChecksAllowed: false,
                nudgesSuppressed: true,
                channel: channel,
                disabledReason: "\(disableEnvKey)=true"
            )
        }

        // CI / non-interactive. The core's rule (Spec 079 FR-019) is
        // "machine-readable facts stay, UI nudges go". The tray has no
        // machine-readable surface — everything it does with an update check
        // ends in a menu item — so a suppressed nudge makes the scheduled check
        // pointless work, and it is switched off with it. A user-initiated
        // check still runs: someone typed it.
        let ciValue = environment["CI"]?.lowercased()
        if ciValue == "true" || ciValue == "1" {
            return EffectiveUpdatePolicy(
                automaticChecksAllowed: false,
                nudgesSuppressed: true,
                channel: channel,
                disabledReason: "running in CI (CI=\(environment["CI"] ?? ""))"
            )
        }

        guard let core else {
            // No core yet, or a pre-092 core. Keep the previous behaviour.
            return EffectiveUpdatePolicy(
                automaticChecksAllowed: true,
                nudgesSuppressed: false,
                channel: channel,
                disabledReason: ""
            )
        }

        if !core.enabled {
            return EffectiveUpdatePolicy(
                automaticChecksAllowed: false,
                nudgesSuppressed: true,
                channel: channel,
                disabledReason: "the core reports update checks are disabled"
            )
        }

        return EffectiveUpdatePolicy(
            automaticChecksAllowed: true,
            nudgesSuppressed: core.nudgesSuppressed,
            channel: channel,
            disabledReason: ""
        )
    }
}
