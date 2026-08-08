// FeedUpdater.swift
// MCPProxy
//
// Spec 092 FR-010 / FR-012 / FR-015 / FR-016 — the one-click updater.
//
// Sparkle 2.9.3 has been declared in Package.swift, dynamically linked, bundled
// into Contents/Frameworks and code-signed by both build scripts since Spec 037
// — and never imported by a single Swift file. `import Sparkle` was verified to
// resolve under plain `swift build` (no Xcode project) before this file was
// written; that was the one unproven assumption in the decision report.
//
// Everything Sparkle-specific lives behind `FeedUpdating` for two reasons that
// are not "testability" alone:
//
//   · the framework cannot be instantiated outside a real .app bundle (it reads
//     SUFeedURL / SUPublicEDKey from the main bundle's Info.plist), so a
//     `swift test` run has no way to exercise the real object at all;
//   · a misconfigured bundle — which is precisely what ships until the CI
//     appcast job runs with a real EdDSA key — must degrade to the legacy
//     GitHub path instead of putting a Sparkle error alert in front of a user.
//
// Hence `startingUpdater: false` plus an explicit `start()` we can catch. The
// stock `SPUStandardUpdaterController(startingUpdater: true, …)` logs the error
// and shows a "contact the developer" alert a few seconds later, which is the
// single worst outcome available here.

import Foundation

// MARK: - Protocol

/// The tray's view of a feed-based updater. One implementation (Sparkle), one
/// stub (tests).
protocol FeedUpdating: AnyObject {
    /// Whether a feed updater is actually usable right now. False for dev
    /// builds run outside a bundle, and for a bundle Sparkle refused to start.
    var isAvailable: Bool { get }

    /// Why the updater is unavailable, when it is. Surfaced in the menu rather
    /// than swallowed (FR-016).
    var unavailableReason: String? { get }

    /// Apply the effective policy: gates the SCHEDULED check only (FR-015 —
    /// a user-initiated check stays available regardless) and selects the feed
    /// channel.
    func apply(policy: EffectiveUpdatePolicy)

    /// Run a check. `userInitiated` bypasses the policy's automatic-check gate
    /// and lets Sparkle show its own progress UI.
    func check(userInitiated: Bool)
}

/// Callbacks from the updater. Delivered on the main thread.
protocol FeedUpdaterObserver: AnyObject {
    /// A version is available from the feed.
    func feedUpdater(didFindVersion version: String)

    /// The feed has nothing newer (or the offer was withdrawn).
    func feedUpdaterDidNotFindUpdate()

    /// A check or install failed in a way the user should see (FR-016).
    func feedUpdater(didFailWith message: String)

    /// FR-012: the bundle is about to be replaced. MUST stop the tray-managed
    /// core before returning — this call is synchronous and the installer runs
    /// as soon as it comes back.
    func feedUpdaterWillInstallUpdate()
}

// MARK: - Sparkle implementation

#if canImport(Sparkle)

import AppKit
import Sparkle

/// The real updater.
///
/// Not `@MainActor`: every method here is either called BY Sparkle on the main
/// thread (the delegate callbacks) or from the main thread (the menu). Adding
/// the isolation would make the `@objc` delegate conformances non-isolated
/// mismatches without buying anything.
final class SparkleFeedUpdater: NSObject, FeedUpdating {

    private weak var observer: FeedUpdaterObserver?
    private var controller: SPUStandardUpdaterController?
    private var policy: EffectiveUpdatePolicy = .permissive
    private(set) var unavailableReason: String?

    /// The bundle Sparkle reads its configuration from. Captured at `start`.
    private var hostBundle: Bundle = .main

    /// The last version the feed offered. Kept by us rather than read back out
    /// of Sparkle, because Sparkle's update *session* ends when the user
    /// dismisses the window while the update itself remains available — and
    /// FR-010's menu item is supposed to stay there, gently, until it is
    /// installed.
    private(set) var offeredVersion: String?

    var isAvailable: Bool { controller != nil }

    init(observer: FeedUpdaterObserver?) {
        self.observer = observer
        super.init()
    }

    /// Create and start the updater. Separate from `init` so a failure has
    /// somewhere to be reported instead of leaving a half-built object.
    ///
    /// - Parameter bundle: injected only so the bundle precondition can be
    ///   exercised; production passes `Bundle.main`.
    func start(bundle: Bundle = .main, policy: EffectiveUpdatePolicy) {
        self.policy = policy
        self.hostBundle = bundle

        // Sparkle reads SUFeedURL / SUPublicEDKey from the host bundle. Running
        // from `.build/debug/MCPProxy` there is no host bundle to read, and the
        // framework's own diagnostics would fire. Say so plainly instead.
        guard bundle.bundleURL.pathExtension == "app", bundle.bundleIdentifier != nil else {
            unavailableReason = "not running from an app bundle"
            NSLog("[MCPProxy] Sparkle not started: %@", unavailableReason ?? "")
            return
        }

        let controller = SPUStandardUpdaterController(
            startingUpdater: false,
            updaterDelegate: self,
            userDriverDelegate: self
        )
        do {
            try controller.updater.start()
        } catch {
            // Misconfiguration (placeholder EdDSA key, missing feed URL, an
            // unsigned bundle). The legacy GitHub path keeps working; the menu
            // shows the reason instead of a one-click item that cannot run.
            unavailableReason = error.localizedDescription
            NSLog("[MCPProxy] Sparkle failed to start: %@", error.localizedDescription)
            return
        }

        self.controller = controller
        apply(policy: policy)
        NSLog("[MCPProxy] Sparkle updater started (feed channel=%@, scheduled checks=%@)",
              policy.channel.rawValue,
              policy.automaticChecksAllowed ? "on" : "off")
    }

    // MARK: FeedUpdating

    func apply(policy: EffectiveUpdatePolicy) {
        self.policy = policy
        guard let updater = controller?.updater else { return }
        // FR-015: the kill switch governs the SCHEDULED cycle. `check(userInitiated:)`
        // deliberately does not consult it.
        updater.automaticallyChecksForUpdates = policy.automaticChecksAllowed
        // Changing the channel set mid-session needs a cycle reset for the next
        // scheduled check to use it (`allowedChannelsForUpdater:` is consulted
        // per check, but the schedule is not).
        updater.resetUpdateCycle()
    }

    func check(userInitiated: Bool) {
        guard let updater = controller?.updater else { return }
        if userInitiated {
            // Always allowed (FR-015, last sentence).
            updater.checkForUpdates()
            return
        }
        guard policy.automaticChecksAllowed else { return }
        updater.checkForUpdatesInBackground()
    }
}

// MARK: - SPUUpdaterDelegate

extension SparkleFeedUpdater: SPUUpdaterDelegate {

    /// FR-013 / decision-report open item #4: ask for the feed that matches
    /// this machine's architecture. Returning nil means "use Info.plist", which
    /// is what happens for any operator-set feed URL that is not the default
    /// name — see `SparkleFeedURL`.
    func feedURLString(for updater: SPUUpdater) -> String? {
        guard let configured = hostBundle.object(forInfoDictionaryKey: "SUFeedURL") as? String,
              !configured.isEmpty else { return nil }
        let resolved = SparkleFeedURL.archSpecific(
            configured, arch: UpdateService.hostArchToken()
        )
        return resolved == configured ? nil : resolved
    }

    /// FR-014: stable users must never be offered RCs. An empty set means
    /// "default channel only", which is exactly the stable feed; RC users also
    /// accept the `beta` channel the prerelease pipeline tags.
    func allowedChannels(for updater: SPUUpdater) -> Set<String> {
        policy.channel.allowedSparkleChannels
    }

    func updater(_ updater: SPUUpdater, didFindValidUpdate item: SUAppcastItem) {
        let version = item.displayVersionString
        offeredVersion = version
        onMain { [weak self] in self?.observer?.feedUpdater(didFindVersion: version) }
    }

    func updaterDidNotFindUpdate(_ updater: SPUUpdater) {
        offeredVersion = nil
        onMain { [weak self] in self?.observer?.feedUpdaterDidNotFindUpdate() }
    }

    /// FR-012 — the hook that matters.
    ///
    /// "Called immediately before installing the specified update": the last
    /// point at which the old bundle is still the one on disk. Synchronous, so
    /// the core is down before the swap rather than racing it. Chosen over
    /// `shouldPostponeRelaunchForUpdate:` (too late — the bundle is already
    /// replaced) and over `updaterWillRelaunchApplication:` (also too late,
    /// though it is kept below as a belt-and-braces second stop, which is
    /// harmless because the stop is idempotent).
    func updater(_ updater: SPUUpdater, willInstallUpdate item: SUAppcastItem) {
        observer?.feedUpdaterWillInstallUpdate()
    }

    func updaterWillRelaunchApplication(_ updater: SPUUpdater) {
        observer?.feedUpdaterWillInstallUpdate()
    }

    func updater(_ updater: SPUUpdater, didAbortWithError error: Error) {
        let nsError = error as NSError
        // "You already have the newest version" arrives here as an abort too.
        // It is not a failure and must not become a menu-visible error.
        if nsError.domain == SUSparkleErrorDomain,
           nsError.code == Int(SUError.noUpdateError.rawValue) {
            offeredVersion = nil
            onMain { [weak self] in self?.observer?.feedUpdaterDidNotFindUpdate() }
            return
        }
        let message = error.localizedDescription
        NSLog("[MCPProxy] Sparkle aborted: %@", message)
        onMain { [weak self] in self?.observer?.feedUpdater(didFailWith: message) }
    }

    private func onMain(_ work: @escaping () -> Void) {
        if Thread.isMainThread {
            work()
        } else {
            DispatchQueue.main.async(execute: work)
        }
    }
}

// MARK: - SPUStandardUserDriverDelegate (gentle reminders)

extension SparkleFeedUpdater: SPUStandardUserDriverDelegate {

    /// FR-010: "a gentle, non-interrupting menu item". A menu-bar app has no
    /// business throwing a window at someone because a background timer fired.
    var supportsGentleScheduledUpdateReminders: Bool { true }

    /// Never let a SCHEDULED check put a window on screen; the menu item is the
    /// notification. A user-initiated check is a different matter — Sparkle's
    /// own progress and release-notes UI is the right answer there, and this
    /// method is not consulted for it.
    func standardUserDriverShouldHandleShowingScheduledUpdate(
        _ update: SUAppcastItem,
        andInImmediateFocus immediateFocus: Bool
    ) -> Bool {
        false
    }

    /// Sparkle tells us whether IT will show the update. When it will not, the
    /// menu item is the only thing the user will ever see, so publish it.
    func standardUserDriverWillHandleShowingUpdate(
        _ handleShowingUpdate: Bool,
        forUpdate update: SUAppcastItem,
        state: SPUUserUpdateState
    ) {
        guard !handleShowingUpdate else { return }
        let version = update.displayVersionString
        offeredVersion = version
        observer?.feedUpdater(didFindVersion: version)
    }
}

#endif
