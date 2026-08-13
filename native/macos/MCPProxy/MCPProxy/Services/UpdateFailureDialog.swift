// UpdateFailureDialog.swift
// MCPProxy
//
// Spec 095 FR-001 / FR-003 / FR-004 / FR-005 — what the user sees instead of
// Sparkle's "Update Error! … Cancel Update", which is a dead end: it names no
// stage, offers no retry, and leaves the browser download — the one path that
// always works — unreachable from the moment it is needed.
//
// Everything in this file except `UpdateFailureAlertPresenter` is pure: no
// AppKit, no I/O, no Sparkle. The browser open and the retry are injected
// closures, so the two things that can actually be wrong — the order candidates
// are tried in, and whether a retry outruns the failed session's teardown — are
// unit-tested rather than eyeballed in a live rig.

import Foundation

// MARK: - Candidates

/// Where a download URL came from. Ordered by FR-005 precedence.
enum UpdateFailureURLSource: String, Equatable {
    case githubInstaller
    case feedRelease
    case releasesPage
}

struct UpdateFailureCandidate: Equatable {
    let url: URL
    let source: UpdateFailureURLSource
}

/// The signed installer URL the GitHub-releases check resolved, stamped with the
/// check cycle that produced it.
///
/// The cycle stamp is the whole point: `UpdateService.downloadURL` is a
/// best-effort "somewhere to download from" with no identity, and offering it
/// for a session it has nothing to do with is how a recovery action turns into
/// a downgrade.
struct GitHubInstallerCapture: Equatable {
    let cycleID: Int
    let version: String
    let url: URL
}

/// Spec 095 US2: hands one occurrence to the core.
///
/// Async and `@Sendable` by design — the caller is on its way to putting a
/// dialog on screen and must not wait for a core that may not be there.
typealias UpdateFailureRecorder = @Sendable (UpdateFailureStage) async -> Void

/// FR-005: candidate assembly, validation and fall-through.
enum UpdateFailureDownload {

    /// Valid by construction, and therefore always the last thing tried.
    static let releasesPage = URL(string: "https://github.com/smart-mcp-proxy/mcpproxy-go/releases")!

    /// Build the ordered candidate list for one occurrence.
    ///
    /// - Parameters:
    ///   - installer: the architecture-specific installer from the GitHub check.
    ///   - currentCycleID: the check cycle the failed session belongs to.
    ///   - offeredVersion: the version the failed session was updating to, when
    ///     it got far enough to know one.
    ///   - feedInfoURL: the offered item's release-page link from the feed.
    static func candidates(
        installer: GitHubInstallerCapture?,
        currentCycleID: Int,
        offeredVersion: String?,
        feedInfoURL: URL?
    ) -> [UpdateFailureCandidate] {
        var candidates: [UpdateFailureCandidate] = []
        if let installer, isEligible(installer, currentCycleID: currentCycleID, offeredVersion: offeredVersion) {
            candidates.append(.init(url: installer.url, source: .githubInstaller))
        }
        if let feedInfoURL {
            candidates.append(.init(url: feedInfoURL, source: .feedRelease))
        }
        candidates.append(.init(url: releasesPage, source: .releasesPage))
        return candidates
    }

    private static func isEligible(
        _ installer: GitHubInstallerCapture,
        currentCycleID: Int,
        offeredVersion: String?
    ) -> Bool {
        guard let offeredVersion else {
            // No version was ever offered (a feed-stage failure), so the only
            // thing tying the installer to this failure is the check cycle.
            return installer.cycleID == currentCycleID
        }
        return UpdateMenuState.normalized(installer.version) == UpdateMenuState.normalized(offeredVersion)
    }

    /// Whether a candidate may be handed to the browser at all (FR-005).
    static func isUsable(_ url: URL) -> Bool {
        url.scheme?.lowercased() == "https" && !(url.host ?? "").isEmpty
    }

    /// Open the first usable candidate the opener accepts, falling through on
    /// both invalid candidates and failed opens. The releases page is attempted
    /// last even when the caller left it out.
    @discardableResult
    static func open(
        _ candidates: [UpdateFailureCandidate],
        opener: (URL) -> Bool
    ) -> UpdateFailureCandidate? {
        var ordered = candidates
        if !ordered.contains(where: { $0.url == releasesPage }) {
            ordered.append(.init(url: releasesPage, source: .releasesPage))
        }
        for candidate in ordered where isUsable(candidate.url) {
            if opener(candidate.url) {
                return candidate
            }
        }
        return nil
    }
}

// MARK: - Content

/// The three fixed choices (FR-001). `tryAgain` is the default action.
enum UpdateFailureAction: Equatable, CaseIterable {
    case tryAgain
    case downloadFromWebsite
    case cancel

    /// Presentation order, default first.
    static let ordered: [UpdateFailureAction] = [.tryAgain, .downloadFromWebsite, .cancel]

    var title: String {
        switch self {
        case .tryAgain: return "Try Again"
        case .downloadFromWebsite: return "Download from Website"
        case .cancel: return "Cancel"
        }
    }
}

/// What the dialog says. The primary sentence is derived from the STAGE; the
/// operating system's error description is secondary, local-only text (FR-003)
/// and never reaches telemetry.
struct UpdateFailureDialogContent: Equatable {
    let messageText: String
    let informativeText: String?

    static func make(stage: UpdateFailureStage, detail: String?) -> UpdateFailureDialogContent {
        let message: String
        switch stage {
        case .appcast:
            message = "MCPProxy couldn’t check for a newer version."
        case .download:
            message = "MCPProxy couldn’t download the update."
        case .install:
            message = "MCPProxy downloaded the update but couldn’t install it."
        case .other:
            message = "MCPProxy couldn’t complete the update."
        }
        let trimmed = detail?.trimmingCharacters(in: .whitespacesAndNewlines)
        return UpdateFailureDialogContent(
            messageText: message,
            informativeText: (trimmed?.isEmpty ?? true) ? nil : trimmed
        )
    }
}

// MARK: - Controller

/// One dialog, one occurrence.
///
/// The controller holds the FR-004 ordering guarantee: "Try Again" may not start
/// a new session before the updater has finished tearing the failed one down. In
/// practice the dialog is presented FROM the terminal signal, so the retry fires
/// immediately; the queue is the guard for the other ordering, and both are
/// tested.
///
/// Not `@MainActor`, for the same reason `SparkleFeedUpdater` is not: everything
/// here is called either BY the updater on the main thread or from
/// `UpdateService`, which hops to main itself. Adding the isolation would only
/// move the hop somewhere it cannot be reasoned about.
final class UpdateFailureDialogController {

    /// Puts the dialog on screen and calls back with the chosen action exactly
    /// once. Injected so the suite never opens a window.
    typealias Presenter = (UpdateFailureDialogContent, @escaping (UpdateFailureAction) -> Void) -> Void

    private let content: UpdateFailureDialogContent
    private let candidates: [UpdateFailureCandidate]
    private let opener: (URL) -> Bool
    private let retry: () -> Void
    private let presenter: Presenter

    private var sessionIsTerminal: Bool
    private var presented = false
    private var retryPending = false
    private var retryFired = false
    private var tornDown = false

    init(
        content: UpdateFailureDialogContent,
        candidates: [UpdateFailureCandidate],
        sessionIsTerminal: Bool,
        opener: @escaping (URL) -> Bool,
        retry: @escaping () -> Void,
        present: @escaping Presenter
    ) {
        self.content = content
        self.candidates = candidates
        self.sessionIsTerminal = sessionIsTerminal
        self.opener = opener
        self.retry = retry
        self.presenter = present
    }

    /// Show the dialog. Idempotent — FR-001 allows at most one per occurrence.
    func present() {
        guard !presented else { return }
        presented = true
        presenter(content) { [weak self] action in
            self?.handle(action)
        }
    }

    /// The updater delivered the failed session's terminal-completion signal.
    func sessionDidFinish() {
        sessionIsTerminal = true
        guard retryPending, !tornDown else { return }
        retryPending = false
        fireRetry()
    }

    /// The app is going away: a queued retry dies with it (FR-004).
    func teardown() {
        tornDown = true
        retryPending = false
    }

    private func handle(_ action: UpdateFailureAction) {
        switch action {
        case .tryAgain:
            requestRetry()
        case .downloadFromWebsite:
            UpdateFailureDownload.open(candidates, opener: opener)
        case .cancel:
            break
        }
    }

    private func requestRetry() {
        guard !retryFired, !retryPending, !tornDown else { return }
        if sessionIsTerminal {
            fireRetry()
        } else {
            retryPending = true
        }
    }

    private func fireRetry() {
        guard !retryFired else { return }
        retryFired = true
        retry()
    }
}

#if canImport(AppKit)

import AppKit

// MARK: - AppKit shell

/// The only part of the dialog that touches AppKit: three buttons and a modal
/// run. Kept apart from the controller so the ordering and precedence rules
/// above stay testable without a window server.
enum UpdateFailureAlertPresenter {

    /// A `Presenter` backed by `NSAlert`.
    @MainActor
    static func present(
        _ content: UpdateFailureDialogContent,
        respond: @escaping (UpdateFailureAction) -> Void
    ) {
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = content.messageText
        if let informativeText = content.informativeText {
            alert.informativeText = informativeText
        }
        for action in UpdateFailureAction.ordered {
            alert.addButton(withTitle: action.title)
        }
        NSApp.activate(ignoringOtherApps: true)
        let response = alert.runModal()
        let index = response.rawValue - NSApplication.ModalResponse.alertFirstButtonReturn.rawValue
        let action = UpdateFailureAction.ordered.indices.contains(index)
            ? UpdateFailureAction.ordered[index]
            : .cancel
        respond(action)
    }

    /// The browser open, as the controller's `opener` seam.
    @MainActor
    static func openInBrowser(_ url: URL) -> Bool {
        NSWorkspace.shared.open(url)
    }
}

#endif
