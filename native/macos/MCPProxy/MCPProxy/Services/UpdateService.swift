// UpdateService.swift
// MCPProxy
//
// Spec 092 Phase 1 (FR-010 / FR-015 / FR-016 / FR-017) — the single owner of
// everything the tray knows about updates.
//
// Two update brains exist and both are legitimate:
//
//   · the FEED (Sparkle appcast) — can download, verify and install; the only
//     one that can deliver FR-010's one click;
//   · the LEGACY check (the core's `/api/v1/info` + a direct GitHub call) —
//     always reachable, but can only open a browser.
//
// Before Phase 1 they were rendered independently by the menu, which is what
// FR-017 forbids. This service now resolves them into ONE ordered list of menu
// entries (`UpdateMenuState`), gates every unattended check on the effective
// policy (`UpdatePolicyResolver`), and stops the managed core before Sparkle
// replaces the bundle (`ManagedCoreStop`).

import Foundation
import AppKit
import Darwin

// MARK: - Update Service

/// Manages software update checks and owns the update section of the menu.
final class UpdateService: ObservableObject {

    /// Whether an update check can be performed. A user-initiated check is
    /// always allowed (FR-015) — including when the policy has disabled the
    /// automatic ones.
    var canCheckForUpdates: Bool { true }

    /// Whether an update check is currently in progress.
    @Published private(set) var isChecking: Bool = false

    /// Latest version the LEGACY check knows about (nil if current or unknown).
    @Published private(set) var latestVersion: String?

    /// Latest version the FEED offers (nil when Sparkle has nothing, or is
    /// unavailable).
    @Published private(set) var feedVersion: String?

    /// URL to download the latest release.
    @Published private(set) var downloadURL: String?

    /// Release notes for the latest version.
    @Published private(set) var releaseNotes: String?

    /// FR-016: why an in-place update cannot work here, when it cannot.
    @Published private(set) var blockedReason: UpdateBlockedReason?

    /// Last error worth telling the user about (feed check/install failures).
    @Published private(set) var lastErrorMessage: String?

    /// The effective policy in force (FR-015).
    @Published private(set) var policy: EffectiveUpdatePolicy = .permissive

    /// The feed updater, when one could be built. Injected in tests.
    private var feedUpdater: (any FeedUpdating)?

    /// Stops the tray-managed core synchronously before the bundle is replaced
    /// (FR-012), returning whether it is CONFIRMED down. Set by the app
    /// delegate, which is the only thing that knows the managed process. Nil in
    /// tests and before the core starts.
    var stopManagedCore: (() -> Bool)?

    /// GitHub API endpoint for latest release.
    private let githubReleaseURL = "https://api.github.com/repos/smart-mcp-proxy/mcpproxy-go/releases/latest"

    /// Current version from the core (set by AppController).
    var currentVersion: String = ""

    /// Environment used for policy resolution; injected in tests.
    private let environment: [String: String]

    /// Bundle whose in-place updatability is evaluated; injected in tests.
    private let hostBundleURL: URL

    /// The legacy GitHub check. Injected in tests so the suite issues no
    /// network requests — nil means "use the real one".
    private let legacyCheck: (() -> Void)?

    // MARK: - Initialization

    init(
        environment: [String: String] = ProcessInfo.processInfo.environment,
        hostBundleURL: URL = Bundle.main.bundleURL,
        feedUpdater: (any FeedUpdating)? = nil,
        legacyCheck: (() -> Void)? = nil
    ) {
        self.environment = environment
        self.hostBundleURL = hostBundleURL
        self.feedUpdater = feedUpdater
        self.legacyCheck = legacyCheck
        self.policy = UpdatePolicyResolver.resolve(core: nil, environment: environment)
        self.blockedReason = UpdateInstallability.evaluate(bundleURL: hostBundleURL)
    }

    // MARK: - Wiring

    /// Build and start the real Sparkle updater. Called once at launch by the
    /// app delegate; a no-op when Sparkle is not compiled in, and harmless when
    /// the bundle is not one Sparkle can work with (it reports why).
    func startFeedUpdater() {
        guard feedUpdater == nil else { return }
        #if canImport(Sparkle)
        let sparkle = SparkleFeedUpdater(observer: self)
        sparkle.start(policy: policy)
        feedUpdater = sparkle
        if !sparkle.isAvailable, let reason = sparkle.unavailableReason {
            NSLog("[MCPProxy] One-click updates unavailable: %@", reason)
        }
        #endif
    }

    /// Inject a feed updater (tests, and the fallback build).
    func installFeedUpdater(_ updater: any FeedUpdating) {
        feedUpdater = updater
        updater.apply(policy: policy)
    }

    /// Whether the feed updater can actually install (FR-017: only then may the
    /// menu offer a one-click item).
    var feedUpdaterAvailable: Bool { feedUpdater?.isAvailable ?? false }

    /// FR-015: apply the policy the core reports. Idempotent; safe to call on
    /// every connect and on every config hot-reload.
    func applyCorePolicy(_ corePolicy: CoreUpdatePolicy?) {
        let resolved = UpdatePolicyResolver.resolve(core: corePolicy, environment: environment)
        guard resolved != policy else { return }
        policy = resolved
        feedUpdater?.apply(policy: resolved)
        if !resolved.automaticChecksAllowed {
            NSLog("[MCPProxy] Automatic update checks are off: %@", resolved.disabledReason)
            // Anything already advertised was advertised under the old policy.
            // Retract it rather than leave a nudge the operator just disabled.
            feedVersion = nil
            latestVersion = nil
        }
    }

    // MARK: - Public API

    /// A check nobody asked for: the launch check and the periodic one. Gated
    /// on the effective policy (FR-015).
    func checkForUpdatesInBackground() {
        guard policy.automaticChecksAllowed else {
            AppLifecycle.shared.recordUpdateCheck("skipped — \(policy.disabledReason)")
            return
        }
        runCheck(userInitiated: false)
    }

    /// The menu's "Check for Updates". Always allowed (FR-015).
    func checkForUpdates() {
        runCheck(userInitiated: true)
    }

    private func runCheck(userInitiated: Bool) {
        lastErrorMessage = nil
        // Re-evaluate: the app may have been moved into /Applications since
        // launch, which is exactly the fallback FR-016's message asks for.
        blockedReason = UpdateInstallability.evaluate(bundleURL: hostBundleURL)

        if let feedUpdater, feedUpdater.isAvailable {
            feedUpdater.check(userInitiated: userInitiated)
            // The legacy check still runs: it is the FR-017 fallback for a
            // version the feed does not carry, and the only source of the
            // browser-download URL.
        }
        if let legacyCheck {
            legacyCheck()
        } else {
            checkWithGitHub()
        }
    }

    /// FR-010: activating the one-click item.
    ///
    /// `check(userInitiated:)` RESUMES the session Sparkle already has open for
    /// the update it found during the gentle-reminder check, and drives
    /// download → verify → replace → relaunch from wherever that session got to.
    ///
    /// Click count, honestly: one here, plus one on Sparkle's own confirmation.
    /// `SPUUpdater`'s public surface has no "install the update you are already
    /// holding" method (only the three check entry points), so the standard
    /// user driver's prompt cannot be skipped without writing a bespoke
    /// `SPUUserDriver` — a replacement for every piece of update UI Sparkle
    /// ships. What the pre-download in `SparkleFeedUpdater.apply(policy:)` buys
    /// is that the second click installs immediately instead of starting a
    /// download: see `updateIsReadyToInstall`.
    func installFeedUpdate() {
        guard let feedUpdater, feedUpdater.isAvailable else {
            openDownloadPage()
            return
        }
        AppLifecycle.shared.note(
            feedUpdater.updateIsReadyToInstall
                ? "installing the pre-downloaded update"
                : "resuming the update session (nothing downloaded yet)"
        )
        feedUpdater.check(userInitiated: true)
    }

    /// The update section of the tray menu, resolved from both sources
    /// (FR-017).
    var menuEntries: [UpdateMenuEntry] {
        UpdateMenuState.entries(
            feedVersion: feedUpdaterAvailable ? feedVersion : nil,
            legacyVersion: latestVersion,
            blocked: blockedReason,
            nudgesSuppressed: policy.nudgesSuppressed
        )
    }

    /// Merge the legacy version reported by the core (`/api/v1/info` →
    /// `update.latest_version`). Kept behind a setter so the FR-017 resolution
    /// has exactly one input path per source.
    func setCoreReportedVersion(_ version: String?) {
        guard !policy.nudgesSuppressed else { return }
        guard let version, !version.isEmpty else { return }
        let normalized = UpdateMenuState.normalized(version)
        if let existing = latestVersion,
           let order = SemanticVersion.compare(existing, normalized), order >= 0 {
            return
        }
        latestVersion = normalized
    }

    /// Returns the release-asset architecture token for the host machine
    /// ("arm64" on Apple Silicon, "amd64" on Intel). Rosetta-translated
    /// processes report the underlying Apple Silicon machine so the user
    /// is offered the native build.
    static func hostArchToken() -> String {
        // Detect Rosetta: Intel binary running on Apple Silicon.
        var translated: Int32 = 0
        var translatedSize = MemoryLayout<Int32>.size
        if sysctlbyname("sysctl.proc_translated", &translated, &translatedSize, nil, 0) == 0,
           translated == 1 {
            return "arm64"
        }
        // Read hw.machine — "arm64" on Apple Silicon, "x86_64" on Intel.
        var size: size_t = 0
        sysctlbyname("hw.machine", nil, &size, nil, 0)
        if size > 0 {
            var buf = [CChar](repeating: 0, count: size)
            if sysctlbyname("hw.machine", &buf, &size, nil, 0) == 0 {
                let machine = String(cString: buf)
                if machine == "arm64" || machine.hasPrefix("arm") {
                    return "arm64"
                }
            }
        }
        return "amd64"
    }

    /// Compare two semver version strings. Returns:
    /// - positive if `a` > `b`
    /// - negative if `a` < `b`
    /// - zero if equal or unparseable
    ///
    /// A thin adapter over `SemanticVersion.compare` (Spec 092 FR-006). The
    /// hand-rolled comparison that used to live here compared prerelease
    /// identifiers as whole strings, which ordered `rc.10` *below* `rc.2` — so
    /// an RC user was offered a downgrade as an "update". The shared type
    /// compares numeric identifiers numerically; see its header.
    ///
    /// Unparseable input keeps mapping to `0` HERE, and only here: the update
    /// nudge treats "not greater" as "no update", so an unknown version can
    /// only ever suppress a nudge. Decisions that can stop a process must use
    /// `SemanticVersion.compare(_:_:) -> Int?` and handle nil explicitly.
    static func compareSemver(_ a: String, _ b: String) -> Int {
        SemanticVersion.compare(a, b) ?? 0
    }

    /// Open the download page in the browser.
    func openDownloadPage() {
        let urlString = downloadURL ?? "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/latest"
        if let url = URL(string: urlString) {
            NSWorkspace.shared.open(url)
        }
    }

    // MARK: - GitHub Releases API

    private func checkWithGitHub() {
        guard !isChecking else { return }
        isChecking = true
        // One line per check, on the record (#862 ask 3). The updater could
        // neither be confirmed nor excluded during the original investigation
        // because its hourly work logged nothing whatsoever.
        AppLifecycle.shared.recordUpdateCheck(
            "checking GitHub releases (running \(currentVersion.isEmpty ? "unknown" : currentVersion))"
        )

        Task {
            defer { DispatchQueue.main.async { self.isChecking = false } }

            guard let url = URL(string: githubReleaseURL) else { return }
            var request = URLRequest(url: url)
            request.setValue("application/vnd.github+json", forHTTPHeaderField: "Accept")
            request.timeoutInterval = 10

            do {
                let (data, response) = try await URLSession.shared.data(for: request)
                guard let httpResponse = response as? HTTPURLResponse,
                      httpResponse.statusCode == 200 else { return }

                guard let json = try JSONSerialization.jsonObject(with: data) as? [String: Any] else { return }

                let tagName = json["tag_name"] as? String ?? ""
                let body = json["body"] as? String ?? ""
                let htmlURL = json["html_url"] as? String ?? ""

                // Strip "v" prefix for comparison
                let remoteVersion = tagName.hasPrefix("v") ? String(tagName.dropFirst()) : tagName
                let localVersion = currentVersion.hasPrefix("v") ? String(currentVersion.dropFirst()) : currentVersion

                // Only suggest the remote version when it is *newer* than the running build.
                // String inequality would otherwise allow a downgrade if GitHub `releases/latest`
                // happens to lag behind a freshly published version.
                if !remoteVersion.isEmpty && !localVersion.isEmpty &&
                   Self.compareSemver(remoteVersion, localVersion) > 0 {
                    // Find macOS DMG asset matching the host CPU architecture.
                    // Release assets are published as:
                    //   mcpproxy-<ver>-darwin-arm64.dmg / -amd64.dmg            (unsigned)
                    //   mcpproxy-<ver>-darwin-arm64-installer.dmg / -amd64-installer.dmg  (signed + notarized)
                    let arch = Self.hostArchToken()
                    var dmgURL = htmlURL
                    if let assets = json["assets"] as? [[String: Any]] {
                        let matches: [(name: String, url: String)] = assets.compactMap { asset in
                            guard let name = asset["name"] as? String,
                                  let url = asset["browser_download_url"] as? String,
                                  name.contains("darwin"),
                                  name.contains(arch),
                                  name.hasSuffix(".dmg") else { return nil }
                            return (name, url)
                        }
                        // Prefer signed & notarized installer DMG when available.
                        if let installer = matches.first(where: { $0.name.contains("-installer.dmg") }) {
                            dmgURL = installer.url
                        } else if let first = matches.first {
                            dmgURL = first.url
                        }
                    }

                    DispatchQueue.main.async {
                        self.latestVersion = remoteVersion
                        self.downloadURL = dmgURL
                        self.releaseNotes = body
                    }
                }
            } catch {
                // Silently fail — update checks are non-critical
            }
        }
    }
}

// MARK: - FeedUpdaterObserver

extension UpdateService: FeedUpdaterObserver {

    func feedUpdater(didFindVersion version: String) {
        let normalized = UpdateMenuState.normalized(version)
        onMain {
            self.feedVersion = normalized
            AppLifecycle.shared.recordUpdateCheck("feed offers v\(normalized)")
        }
    }

    func feedUpdaterDidNotFindUpdate() {
        onMain { self.feedVersion = nil }
    }

    func feedUpdater(didFailWith message: String) {
        onMain {
            self.lastErrorMessage = message
            AppLifecycle.shared.recordUpdateCheck("feed check failed: \(message)")
        }
    }

    /// Publish on the main thread — synchronously when already there.
    ///
    /// The synchronous branch is not an optimization: Sparkle's callbacks
    /// arrive on the main thread, and an unconditional `async` hop would leave
    /// the menu one run-loop turn behind the state that produced it (and make
    /// every test of this path a waiting game).
    private func onMain(_ work: @escaping () -> Void) {
        if Thread.isMainThread {
            work()
        } else {
            DispatchQueue.main.async(execute: work)
        }
    }

    /// FR-012. Synchronous by contract: Sparkle proceeds with the install the
    /// moment this returns, so the core has to already be down.
    ///
    /// No core to stop is a stopped core: the tray runs without one, and an
    /// update must not be blocked by the absence of the thing it was going to
    /// shut down.
    @discardableResult
    func feedUpdaterWillInstallUpdate() -> Bool {
        AppLifecycle.shared.note("stopping the managed core before the update is installed")
        guard let stopManagedCore else { return true }
        let stopped = stopManagedCore()
        if !stopped {
            AppLifecycle.shared.note("the managed core could not be stopped — update postponed")
        }
        return stopped
    }
}
