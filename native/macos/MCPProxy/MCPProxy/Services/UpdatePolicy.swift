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

    /// What a core that predates Spec 092 means when it reports no
    /// `update_policy` at all: the behaviour those builds already had.
    ///
    /// Stamped by the tray at the moment it has actually TALKED to such a core
    /// (`CoreProcessManager.connectToCore`), which is the only place that can
    /// tell "an old core said nothing" apart from "we have not asked yet".
    /// Without that distinction the absent field and the absent core look the
    /// same, and FR-015's "not inferred from missing data" is unenforceable.
    static let legacyDefault = CoreUpdatePolicy(
        enabled: true, channel: "stable", nudgesSuppressed: false
    )
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

    /// Whether the feed updater may download an update before the user asks
    /// for it.
    ///
    /// Always false, for every policy — deliberately not a setting. In Sparkle
    /// this single flag also selects the update driver, and the pre-downloading
    /// one arms a silent install-on-quit that no delegate can refuse, so the
    /// bundle would be replaced without anyone having confirmed the managed
    /// core is down. The full reasoning, with the Sparkle source references, is
    /// on `SparkleFeedUpdater.apply(policy:)`.
    ///
    /// It lives here, as a property rather than a literal at the call site, so
    /// the decision has one name and one test — the Sparkle class itself cannot
    /// be instantiated outside a real .app bundle.
    var automaticDownloadsAllowed: Bool { false }

    /// Automatic checks on, nothing suppressed, stable channel.
    static let permissive = EffectiveUpdatePolicy(
        automaticChecksAllowed: true,
        nudgesSuppressed: false,
        channel: .stable,
        disabledReason: ""
    )

    /// The policy in force before any core has been reached.
    ///
    /// Restrictive on purpose. The tray publishes the core's version and the
    /// core's update policy from the same `/api/v1/info` response, and Combine
    /// delivers the version first — so a permissive default let the launch
    /// check fire on the OLD policy, checking for updates for a user who had
    /// switched them off. Nothing unattended runs until the contract arrives;
    /// a user-initiated "Check for Updates" is unaffected (FR-015), and the
    /// wait is one round trip to a socket on the same machine.
    static let awaitingCore = EffectiveUpdatePolicy(
        automaticChecksAllowed: false,
        nudgesSuppressed: false,
        channel: .stable,
        disabledReason: "no core has reported its update policy yet"
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
            // Nobody has told us anything yet. Not "permissive by default" —
            // see EffectiveUpdatePolicy.awaitingCore. A core that predates
            // Spec 092 is NOT this case: the tray stamps
            // CoreUpdatePolicy.legacyDefault for it on connect.
            return EffectiveUpdatePolicy(
                automaticChecksAllowed: false,
                nudgesSuppressed: false,
                channel: channel,
                disabledReason: EffectiveUpdatePolicy.awaitingCore.disabledReason
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
