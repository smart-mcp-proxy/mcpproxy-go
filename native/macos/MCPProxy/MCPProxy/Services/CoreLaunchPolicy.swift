// CoreLaunchPolicy.swift
// MCPProxy
//
// GH #410 — whether the tray may START a core when it opens.
//
// The tray and the core are decoupled: they talk only over the Unix socket /
// HTTP. That rules out storing this setting in the core's config, because the
// setting's entire purpose is to be read when NO core is running — there would
// be nothing to ask. So it is tray-local, like `fontScale` and
// `MCPProxy.firstRunCompleted`.
//
// This does not make the tray stateful about the core. The tray never remembers
// whether a core is running; it discovers that live from the socket on every
// launch. The only thing it remembers is its own permission to SPAWN one.
//
//   attach to a running core  — always allowed, never gated by this policy
//   spawn a new core          — gated by this policy

import Foundation

/// The tray's launcher preference: may it spawn a core process?
struct CoreLaunchPolicy {

    /// UserDefaults key for the persisted preference.
    static let startCoreOnLaunchKey = "MCPProxy.startCoreOnLaunch"

    /// Environment override, named to match the Go tray's existing flag
    /// (`cmd/mcpproxy-tray/main.go` shouldSkipCoreLaunch) so a user who runs the
    /// core under launchd/brew can pin the tray to attach-only in both trays.
    static let skipCoreEnvVar = "MCPPROXY_TRAY_SKIP_CORE"

    private let defaults: UserDefaults
    private let environment: [String: String]

    init(
        defaults: UserDefaults = .standard,
        environment: [String: String] = ProcessInfo.processInfo.environment
    ) {
        self.defaults = defaults
        self.environment = environment
    }

    /// The user's stored preference. Defaults to `true` when never set, so an
    /// existing install behaves exactly as it did before #410.
    var startCoreOnLaunch: Bool {
        get { defaults.object(forKey: Self.startCoreOnLaunchKey) as? Bool ?? true }
        nonmutating set { defaults.set(newValue, forKey: Self.startCoreOnLaunchKey) }
    }

    /// True when the environment forces attach-only mode. Surfaced so the
    /// Settings toggle can be shown disabled instead of silently disagreeing
    /// with the app's actual behaviour.
    var isPinnedOffByEnvironment: Bool {
        guard let raw = environment[Self.skipCoreEnvVar]?
            .trimmingCharacters(in: .whitespacesAndNewlines)
            .lowercased()
        else { return false }
        return raw == "1" || raw == "true"
    }

    /// The effective policy: may the tray spawn a core process right now?
    /// The environment wins over the stored preference.
    var maySpawnCore: Bool {
        if isPinnedOffByEnvironment { return false }
        return startCoreOnLaunch
    }
}
