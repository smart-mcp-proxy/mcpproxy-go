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

    /// Whether an update is already downloaded and waiting to be installed, so
    /// activating the menu item costs a confirmation rather than a download
    /// (FR-010). See `SparkleFeedUpdater.updateIsReadyToInstall`.
    var updateIsReadyToInstall: Bool { get }
}

extension FeedUpdating {
    /// Most implementations (the test stub, the no-Sparkle fallback) never
    /// pre-download anything.
    var updateIsReadyToInstall: Bool { false }
}

// MARK: - Update sessions (Spec 095)

/// One terminal failure of one update session — the unit both the recovery
/// dialog and the failure counters are measured in (Spec 095 Definitions).
struct FeedUpdateFailure: Equatable {
    let stage: UpdateFailureStage

    /// The version the failed session was updating to, when it got far enough
    /// to know one. Nil for a feed-stage failure.
    let offeredVersion: String?

    /// The offered item's release-page link from the feed (FR-005 candidate 2).
    let feedInfoURL: URL?

    /// Whether the updater asked for an error to be shown. Sparkle's own drivers
    /// already implement FR-002's visibility matrix, so this is inherited rather
    /// than re-derived here.
    let dialogRequested: Bool

    /// The operating system's error description, for the dialog's secondary text
    /// (FR-003). Displayed locally and NEVER transmitted.
    let errorDescription: String?
}

/// Per-session failure bookkeeping, free of Sparkle so it can be tested.
///
/// The session is the unit of counting: several callbacks may describe the same
/// failure, and exactly one occurrence must come out of them (FR-001/FR-009).
/// State resets when a session ends, which is what keeps a scheduled session
/// Sparkle starts on its own timer from inheriting the previous one's evidence.
final class UpdateSessionTracker {

    private var downloadProvenance = false
    private var offeredVersion: String?
    private var feedInfoURL: URL?
    private var stashedErrorDescription: String?
    private var finished = false

    /// A new check is starting.
    func sessionDidStart() {
        reset()
        finished = false
    }

    /// The feed offered an update (`didFindValidUpdate`).
    func didFindValidUpdate(version: String, infoURL: URL?) {
        finished = false
        offeredVersion = version
        feedInfoURL = infoURL
    }

    /// `failedToDownloadUpdate` fired: the provenance latch of FR-007's first
    /// evidence row.
    func downloadDidFail() {
        finished = false
        downloadProvenance = true
    }

    /// The user driver was asked to show an error. Stashing it (rather than
    /// alerting inside that call) is what lets Sparkle tear its own windows down
    /// first; the dialog is presented from the cycle-finished callback below.
    func driverDidShowError(_ error: NSError) {
        finished = false
        stashedErrorDescription = error.localizedDescription
    }

    /// The update cycle finished — the occurrence point, the terminal-completion
    /// signal, and the end of this session's state.
    ///
    /// Returns the occurrence, or nil when this was not an eligible failure
    /// (clean finish, cancellation, "no update found", or a repeat callback).
    func sessionDidFinish(error: NSError?) -> FeedUpdateFailure? {
        defer { reset(); finished = true }
        guard !finished, let error else { return nil }
        guard let stage = UpdateFailureClassifier.classify(
            downloadProvenance: downloadProvenance,
            identity: UpdateFailureErrorIdentity(error)
        ) else { return nil }

        return FeedUpdateFailure(
            stage: stage,
            offeredVersion: offeredVersion,
            feedInfoURL: feedInfoURL,
            dialogRequested: stashedErrorDescription != nil,
            errorDescription: stashedErrorDescription ?? error.localizedDescription
        )
    }

    private func reset() {
        downloadProvenance = false
        offeredVersion = nil
        feedInfoURL = nil
        stashedErrorDescription = nil
    }
}

/// Callbacks from the updater. Delivered on the main thread.
protocol FeedUpdaterObserver: AnyObject {
    /// A version is available from the feed.
    func feedUpdater(didFindVersion version: String)

    /// The feed has nothing newer (or the offer was withdrawn).
    func feedUpdaterDidNotFindUpdate()

    /// A check or install failed in a way the user should see (FR-016).
    ///
    /// Tray-SYNTHESIZED advisories only (the postpone notice and the on-quit
    /// tripwire). Spec 095 excludes them from classification and counting: they
    /// are not update-session failures. Session failures arrive below.
    func feedUpdater(didFailWith message: String)

    /// Spec 095: one terminal failure of one update session.
    func feedUpdater(didFailSession failure: FeedUpdateFailure)

    /// FR-012: the bundle is about to be replaced. MUST stop the tray-managed
    /// core before returning — this call is synchronous and the installer runs
    /// as soon as it comes back.
    ///
    /// Returns whether the core is CONFIRMED down. `false` means the swap must
    /// not go ahead: a core still executing from the bundle that is about to be
    /// replaced is issue #957 itself.
    @discardableResult
    func feedUpdaterWillInstallUpdate() -> Bool
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
    private var updater: SPUUpdater?

    /// The user driver, retained here because `SPUUpdater` does not own it.
    /// Spec 095: a subclass whose only override is the failure alert.
    private var driver: RecoveryUserDriver?

    /// Spec 095: this session's failure evidence.
    private let session = UpdateSessionTracker()

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

    /// Whether Sparkle has the update downloaded and is only waiting to be told
    /// to install it.
    ///
    /// True only for a RESUMED session — the user started a check, then
    /// dismissed Sparkle's window while the download stayed on disk. Scheduled
    /// checks never pre-download; see `apply(policy:)` for why that is off.
    ///
    /// FR-010 asks for one click doing download → verify → swap → relaunch.
    /// Sparkle's PUBLIC API cannot deliver a literal single click on top of
    /// `SPUStandardUserDriver`: there is no "install the update you are holding"
    /// method on `SPUUpdater` (see SPUUpdater.h — the only entry points are the
    /// three check methods), so the menu item has to resume the session with
    /// `checkForUpdates()`, and the standard driver then shows its
    /// confirmation. Honest click count: ONE on our menu item, plus ONE on
    /// Sparkle's install confirmation, with the download in between. Removing
    /// the second click needs a custom `SPUUserDriver` implementation, which
    /// replaces every piece of update UI Sparkle ships and is out of scope
    /// here; removing the download needs the on-quit installer we refuse to arm.
    private(set) var updateIsReadyToInstall: Bool = false

    var isAvailable: Bool { updater != nil }

    /// The Sparkle selectors this class MUST answer, asserted against the ObjC
    /// runtime in the suite.
    ///
    /// Every one of these is an OPTIONAL protocol method matched by selector at
    /// runtime, so a Swift name that no longer maps to the selector — an
    /// importer change, a rename, a typo — compiles cleanly and is then simply
    /// never called. Silence is exactly what an update-failure path cannot
    /// afford.
    static let requiredDelegateSelectors = [
        "updater:mayPerformUpdateCheck:error:",
        "updater:didFinishUpdateCycleForUpdateCheck:error:",
        "updater:failedToDownloadUpdate:error:",
        "updater:didFindValidUpdate:",
        "updater:shouldPostponeRelaunchForUpdate:untilInvokingBlock:",
    ]

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

        // Spec 095: NOT SPUStandardUpdaterController. That adapter hardwires the
        // stock user driver, and the stock driver's failure alert ("Update
        // Error! … Cancel Update") is a dead end with no retry and no download
        // path. Building the updater directly is the only way to hand it a
        // driver subclass. Everything else here is the adapter's own behaviour:
        // same delegate, same driver class, same deferred start.
        let driver = RecoveryUserDriver(hostBundle: bundle, delegate: self)
        driver.failureSink = self
        let updater = SPUUpdater(
            hostBundle: bundle,
            applicationBundle: bundle,
            userDriver: driver,
            delegate: self
        )
        do {
            try updater.start()
        } catch {
            // Misconfiguration (placeholder EdDSA key, missing feed URL, an
            // unsigned bundle). The legacy GitHub path keeps working; the menu
            // shows the reason instead of a one-click item that cannot run.
            unavailableReason = error.localizedDescription
            NSLog("[MCPProxy] Sparkle failed to start: %@", error.localizedDescription)
            return
        }

        self.driver = driver
        self.updater = updater
        apply(policy: policy)
        NSLog("[MCPProxy] Sparkle updater started (feed channel=%@, scheduled checks=%@)",
              policy.channel.rawValue,
              policy.automaticChecksAllowed ? "on" : "off")
    }

    // MARK: FeedUpdating

    func apply(policy: EffectiveUpdatePolicy) {
        self.policy = policy
        guard let updater else { return }
        // FR-015: the kill switch governs the SCHEDULED cycle. `check(userInitiated:)`
        // deliberately does not consult it.
        updater.automaticallyChecksForUpdates = policy.automaticChecksAllowed

        // ALWAYS false, and not a preference we are willing to expose.
        //
        // Turning it on looks like a free win — the scheduled check
        // pre-downloads, so FR-010's click has nothing left to wait for — but
        // it also switches Sparkle from SPUScheduledUpdateDriver to
        // SPUAutomaticUpdateDriver (SPUUpdater.m: the driver is chosen on this
        // flag alone), and that driver ARMS a silent install-on-quit: it hands
        // the update to the external installer tool, which replaces the bundle
        // once this process exits.
        //
        // Nothing can call that off afterwards. `willInstallUpdateOnQuit:` is
        // not a veto — its own documentation says "In either case Sparkle will
        // always attempt to install the update when the app terminates" — and
        // `shouldPostponeRelaunchForUpdate:`, the hook that CAN refuse, lives
        // in SPUInstallerDriver.installWithToolAndRelaunch:, which the on-quit
        // path never calls. So the bundle would be replaced without anyone
        // having confirmed the managed core is down, which is issue #957.
        //
        // The price is that FR-010's click downloads before it installs. That
        // is a slower click; the alternative is an install we cannot refuse.
        //
        // Assigned unconditionally rather than left alone: the flag is backed
        // by a user default Sparkle's own permission prompt can write, so not
        // setting it is not the same as it being off.
        updater.automaticallyDownloadsUpdates = policy.automaticDownloadsAllowed
        // Changing the channel set mid-session needs a cycle reset for the next
        // scheduled check to use it (`allowedChannelsForUpdater:` is consulted
        // per check, but the schedule is not).
        updater.resetUpdateCycle()
    }

    func check(userInitiated: Bool) {
        guard let updater else { return }
        if userInitiated {
            // Always allowed (FR-015, last sentence).
            session.sessionDidStart()
            updater.checkForUpdates()
            return
        }
        guard policy.automaticChecksAllowed else { return }
        session.sessionDidStart()
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
        // The channel is part of the file name, not only of the item tags: the
        // two pipelines publish two sets of files (FR-014), so an RC client
        // that asked for the stable feed would be told there is no update.
        let resolved = SparkleFeedURL.archSpecific(
            configured, arch: UpdateService.hostArchToken(), channel: policy.channel
        )
        NSLog("[MCPProxy] Sparkle check: channel=%@ feed=%@",
              policy.channel.rawValue, resolved == configured ? "(Info.plist default)" : resolved)
        return resolved == configured ? nil : resolved
    }

    /// Diagnostic seam: one line per loaded appcast saying what Sparkle SAW.
    /// Without it, "no update" is indistinguishable from "wrong feed",
    /// "filtered channel", and "version comparison surprise" — which cost a
    /// live debugging session on the v0.54.0-rc.3 rehearsal.
    func updater(_ updater: SPUUpdater, didFinishLoading appcast: SUAppcast) {
        let items = appcast.items.map { item in
            "\(item.displayVersionString)(v=\(item.versionString),ch=\(item.channel ?? "default"))"
        }.joined(separator: ", ")
        NSLog("[MCPProxy] Sparkle loaded appcast: %d item(s): %@", appcast.items.count, items)
    }

    /// FR-014: stable users must never be offered RCs. An empty set means
    /// "default channel only", which is exactly the stable feed; RC users also
    /// accept the `beta` channel the prerelease pipeline tags.
    func allowedChannels(for updater: SPUUpdater) -> Set<String> {
        let allowed = policy.channel.allowedSparkleChannels
        NSLog("[MCPProxy] Sparkle allowedChannels consulted: channel=%@ -> %@",
              policy.channel.rawValue, allowed.isEmpty ? "(default only)" : allowed.sorted().joined(separator: ","))
        return allowed
    }

    /// FR-006 — Sparkle MUST compare versions by SemVer 2.0 precedence.
    ///
    /// `SUStandardVersionComparator` treats the whole prerelease suffix as
    /// noise: it reports 0.54.0-rc.2, 0.54.0-rc.3 AND plain 0.54.0 as EQUAL
    /// (verified against Sparkle 2.9.3 directly). Every RC→RC and RC→stable
    /// update is therefore invisible to it — "You're up to date" on a feed
    /// that plainly carries a newer item, found live on the v0.54.0-rc.3
    /// dress rehearsal. The shared SemanticVersion util already implements
    /// the correct precedence (rc.2 < rc.3 < stable); this delegate is the
    /// missing plumbing that hands it to Sparkle.
    func versionComparator(for updater: SPUUpdater) -> SUVersionComparison? {
        SemVerSparkleComparator.shared
    }

    func updater(_ updater: SPUUpdater, didFindValidUpdate item: SUAppcastItem) {
        let version = item.displayVersionString
        offeredVersion = version
        // Spec 095 FR-005 candidate 2: the release page the feed itself points
        // at, which is the only download URL that is certainly about THIS offer.
        session.didFindValidUpdate(version: version, infoURL: item.infoURL)
        onMain { [weak self] in self?.observer?.feedUpdater(didFindVersion: version) }
    }

    /// Spec 095 — the session-start signal, and the ONLY one that covers the
    /// sessions Sparkle starts on its own timer (`check(userInitiated:)` sees
    /// only ours). Sparkle calls it at the top of every check; never deferring,
    /// so it stays a pure notification.
    func updater(_ updater: SPUUpdater, mayPerform updateCheck: SPUUpdateCheck) throws {
        session.sessionDidStart()
    }

    /// Spec 095 FR-007, first evidence row: whatever the abort error ends up
    /// saying, a session that got here failed at the download stage.
    func updater(_ updater: SPUUpdater, failedToDownloadUpdate item: SUAppcastItem, error: Error) {
        session.downloadDidFail()
    }

    /// Spec 095 — the occurrence point.
    ///
    /// Fires exactly once per update session, including the silent ones that
    /// never reach the user driver, and carries the terminal error. It is also
    /// FR-004's terminal-completion signal: by the time the dialog goes up the
    /// failed session is already torn down, so Try Again can start immediately.
    func updater(
        _ updater: SPUUpdater,
        didFinishUpdateCycleFor updateCheck: SPUUpdateCheck,
        error: Error?
    ) {
        guard let failure = session.sessionDidFinish(error: error as NSError?) else { return }
        NSLog("[MCPProxy] Sparkle update session failed: stage=%@ dialog=%@",
              failure.stage.rawValue, failure.dialogRequested ? "yes" : "no")
        onMain { [weak self] in self?.observer?.feedUpdater(didFailSession: failure) }
    }

    func updaterDidNotFindUpdate(_ updater: SPUUpdater) {
        offeredVersion = nil
        updateIsReadyToInstall = false
        onMain { [weak self] in self?.observer?.feedUpdaterDidNotFindUpdate() }
    }

    /// FR-012 — the hook that matters, and the only one that can say no.
    ///
    /// Despite the name, Sparkle calls this at the TOP of
    /// `-[SPUInstallerDriver installWithToolAndRelaunch:displayingUserInterface:]`,
    /// before the installer is contacted and therefore before the bundle is
    /// touched (verified against Sparkle 2.9.3's SPUInstallerDriver.m). That
    /// makes it the last point at which the update can still be called off,
    /// which `willInstallUpdate:` — a `void` notification — cannot do.
    ///
    /// Returning `true` without ever invoking `installHandler` leaves the
    /// update downloaded and uninstalled: the menu item stays, the running
    /// version keeps working, and the next attempt starts from a clean state.
    /// That is the right answer when the core will not die, because replacing
    /// the bundle under a live core is issue #957 happening again.
    func updater(
        _ updater: SPUUpdater,
        shouldPostponeRelaunchForUpdate item: SUAppcastItem,
        untilInvokingBlock installHandler: @escaping () -> Void
    ) -> Bool {
        guard let observer else { return false }
        if observer.feedUpdaterWillInstallUpdate() {
            return false   // core is down; let Sparkle carry straight on
        }
        observer.feedUpdater(didFailWith:
            "The update was not installed: the MCPProxy core is still running and could not "
            + "be stopped. Quit it and try again.")
        NSLog("[MCPProxy] Postponing the update: the managed core could not be confirmed stopped")
        return true
    }

    /// Tripwire. This is only ever called by `SPUAutomaticUpdateDriver`, which
    /// `apply(policy:)` makes sure is never constructed — so reaching it means
    /// something turned `automaticallyDownloadsUpdates` back on and a silent
    /// install-on-quit has ALREADY been armed by the external installer tool.
    ///
    /// Neither return value can call that off ("In either case Sparkle will
    /// always attempt to install the update when the app terminates"), so this
    /// declines to take control and says so loudly rather than pretending to
    /// have handled it. `false` at least leaves Sparkle's normal cycle running,
    /// so the update can still be presented through the path that can be
    /// refused.
    func updater(
        _ updater: SPUUpdater,
        willInstallUpdateOnQuit item: SUAppcastItem,
        immediateInstallationBlock immediateInstallHandler: @escaping () -> Void
    ) -> Bool {
        NSLog("[MCPProxy] WARNING: Sparkle armed a silent install-on-quit for %@. "
              + "This path cannot be vetoed and bypasses the managed-core stop.",
              item.displayVersionString)
        observer?.feedUpdater(didFailWith:
            "An update was staged to install when MCPProxy quits, bypassing the usual "
            + "shutdown of the core. Quit the core before quitting MCPProxy.")
        return false
    }

    /// Belt and braces for the paths that never reach the postpone hook. Both
    /// of these are `void` — Sparkle is telling us, not asking — so a stop that
    /// fails here cannot stop anything; the most it can do is be visible
    /// instead of silent. The hook that CAN refuse is the postpone one above,
    /// and with the automatic driver disabled every install goes through it.
    func updater(_ updater: SPUUpdater, willInstallUpdate item: SUAppcastItem) {
        reportUnvetoableStop(from: "willInstallUpdate")
    }

    func updaterWillRelaunchApplication(_ updater: SPUUpdater) {
        reportUnvetoableStop(from: "updaterWillRelaunchApplication")
    }

    private func reportUnvetoableStop(from hook: String) {
        guard let observer else { return }
        guard !observer.feedUpdaterWillInstallUpdate() else { return }
        NSLog("[MCPProxy] WARNING: the managed core is still running at %@, which cannot "
              + "refuse the install. The bundle may be replaced under it.", hook)
    }

    /// Diagnostics only, since Spec 095.
    ///
    /// The failure itself is reported from the cycle-finished callback below,
    /// which Sparkle always delivers right after this one (SPUUpdater.m's
    /// driver-completion block calls both, in that order) and which — unlike
    /// this callback — also covers the sessions that abort with no error at all.
    /// Reporting from both would count and announce one failure twice.
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
        NSLog("[MCPProxy] Sparkle aborted: %@", error.localizedDescription)
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

    /// Sparkle tells us whether IT will show the update, and at what stage the
    /// update session is. The stage is what makes FR-010's click cheap: at
    /// `.downloaded` or `.installing` the bytes are already on disk and
    /// verified, so resuming the session goes straight to the install prompt.
    func standardUserDriverWillHandleShowingUpdate(
        _ handleShowingUpdate: Bool,
        forUpdate update: SUAppcastItem,
        state: SPUUserUpdateState
    ) {
        updateIsReadyToInstall = state.stage == .downloaded || state.stage == .installing
        guard !handleShowingUpdate else { return }
        let version = update.displayVersionString
        offeredVersion = version
        observer?.feedUpdater(didFindVersion: version)
    }
}

// MARK: - Recovery user driver (Spec 095)

extension SparkleFeedUpdater {
    /// Take the error Sparkle wanted to alert about. Called by the driver below,
    /// on the main thread, and consumed at the end of the cycle.
    ///
    /// The presence of a stashed error IS the FR-002 visibility decision: the
    /// stock drivers only reach `showUpdaterError` when their own matrix says
    /// the failure should be shown (user-initiated always; scheduled only once
    /// update UI has been presented), so the tray inherits that policy instead
    /// of reimplementing it.
    func stashUpdaterError(_ error: NSError) {
        session.driverDidShowError(error)
    }
}

/// The stock user driver with exactly one behaviour replaced: its failure alert.
///
/// Sparkle 2.9.3 offers no delegate hook to suppress or replace that alert
/// (`didAbortWithError` is notification-only), so the alert has to be overridden
/// where it is written. Everything else — found-update prompt, progress, release
/// notes, gentle reminders — stays stock (FR-006).
///
/// The override acknowledges IMMEDIATELY and shows nothing. Sparkle's own
/// implementation closes the checking and status windows before it alerts;
/// blocking here with a modal of our own would leave those windows up behind it.
/// Acknowledging lets the abort continue into `dismissUpdateInstallation`, which
/// closes them, and the recovery dialog is presented from the cycle-finished
/// callback instead.
final class RecoveryUserDriver: SPUStandardUserDriver {

    weak var failureSink: SparkleFeedUpdater?

    override func showUpdaterError(_ error: Error, acknowledgement: @escaping () -> Void) {
        failureSink?.stashUpdaterError(error as NSError)
        acknowledgement()
    }
}

/// SUVersionComparison backed by the shared SemVer 2.0 util (FR-006).
///
/// Malformed versions fall back to Sparkle's own comparator rather than
/// guessing: `SemanticVersion.compare` returns nil for anything that is not a
/// version, and a fabricated "equal" there is precisely the bug this class
/// exists to fix.
final class SemVerSparkleComparator: NSObject, SUVersionComparison {
    static let shared = SemVerSparkleComparator()

    func compareVersion(_ versionA: String, toVersion versionB: String) -> ComparisonResult {
        if let cmp = SemanticVersion.compare(versionA, versionB) {
            return cmp < 0 ? .orderedAscending : cmp > 0 ? .orderedDescending : .orderedSame
        }
        NSLog("[MCPProxy] Sparkle version compare fell back to the standard comparator: %@ vs %@",
              versionA, versionB)
        return SUStandardVersionComparator.default.compareVersion(versionA, toVersion: versionB)
    }
}

#endif
