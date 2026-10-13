// UpdateFailureDialogTests.swift
// MCPProxyTests
//
// Spec 095 FR-001 / FR-003 / FR-004 / FR-005 — the recovery dialog's two pieces
// of real logic: which download URL is opened (and in what order the candidates
// are tried), and when a "Try Again" actually starts a new update session.
//
// Both are driven through injected seams, so nothing here puts an NSAlert on
// screen or opens a browser: the assertions are about INVOCATION ORDER, which is
// the part we own — the OS-controlled outcome of an open is not ours to assert.

import XCTest
@testable import MCPProxy

@MainActor
final class UpdateFailureDialogTests: XCTestCase {

    private let releasesPage = UpdateFailureDownload.releasesPage
    private let installerURL = URL(string: "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/download/v0.55.0/mcpproxy-0.55.0-darwin-arm64-installer.dmg")!
    private let feedInfoURL = URL(string: "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/tag/v0.55.0")!

    /// Records every URL handed to the browser and answers each open with a
    /// scripted result, so fall-through is observable.
    private final class OpenerSpy {
        private(set) var opened: [URL] = []
        var results: [Bool] = []

        func open(_ url: URL) -> Bool {
            opened.append(url)
            return results.isEmpty ? true : results.removeFirst()
        }
    }

    private func capture(cycleID: Int, version: String) -> GitHubInstallerCapture {
        GitHubInstallerCapture(cycleID: cycleID, version: version, url: installerURL)
    }

    // MARK: - FR-005 candidate assembly

    func testTheInstallerIsUsedWhenItsVersionMatchesTheOfferedOne() {
        let candidates = UpdateFailureDownload.candidates(
            installer: capture(cycleID: 3, version: "0.55.0"),
            currentCycleID: 3,
            offeredVersion: "0.55.0",
            feedInfoURL: feedInfoURL
        )

        XCTAssertEqual(candidates.map(\.source), [.githubInstaller, .feedRelease, .releasesPage])
        XCTAssertEqual(candidates.first?.url, installerURL)
    }

    /// A `v` prefix on one side is a spelling, not a different version.
    func testTheVersionMatchIgnoresALeadingV() {
        let candidates = UpdateFailureDownload.candidates(
            installer: capture(cycleID: 1, version: "0.55.0"),
            currentCycleID: 1,
            offeredVersion: "v0.55.0",
            feedInfoURL: nil
        )

        XCTAssertEqual(candidates.map(\.source), [.githubInstaller, .releasesPage])
    }

    func testAnInstallerForADifferentVersionIsNotOffered() {
        let candidates = UpdateFailureDownload.candidates(
            installer: capture(cycleID: 3, version: "0.54.0"),
            currentCycleID: 3,
            offeredVersion: "0.55.0",
            feedInfoURL: feedInfoURL
        )

        XCTAssertEqual(candidates.map(\.source), [.feedRelease, .releasesPage],
                       "sending someone to an installer for a version the feed did not "
                       + "offer is a downgrade dressed as a recovery")
    }

    /// A feed-stage failure never learns a version — the same check cycle's
    /// GitHub result is then the best evidence available.
    func testWithNoOfferedVersionTheSameCycleInstallerQualifies() {
        let candidates = UpdateFailureDownload.candidates(
            installer: capture(cycleID: 7, version: "0.55.0"),
            currentCycleID: 7,
            offeredVersion: nil,
            feedInfoURL: nil
        )

        XCTAssertEqual(candidates.map(\.source), [.githubInstaller, .releasesPage])
    }

    func testWithNoOfferedVersionAStaleCycleInstallerIsSkipped() {
        let candidates = UpdateFailureDownload.candidates(
            installer: capture(cycleID: 6, version: "0.55.0"),
            currentCycleID: 7,
            offeredVersion: nil,
            feedInfoURL: nil
        )

        XCTAssertEqual(candidates.map(\.source), [.releasesPage],
                       "an installer URL from an earlier cycle has no relationship to "
                       + "the session that just failed")
    }

    func testTheReleasesPageIsAlwaysTheLastCandidate() {
        let candidates = UpdateFailureDownload.candidates(
            installer: nil, currentCycleID: 0, offeredVersion: nil, feedInfoURL: nil
        )

        XCTAssertEqual(candidates.map(\.source), [.releasesPage])
        XCTAssertEqual(candidates.last?.url, releasesPage)
    }

    // MARK: - FR-005 opening order

    func testTheFirstCandidateWins() {
        let spy = OpenerSpy()
        let candidates = UpdateFailureDownload.candidates(
            installer: capture(cycleID: 1, version: "0.55.0"),
            currentCycleID: 1, offeredVersion: "0.55.0", feedInfoURL: feedInfoURL
        )

        let opened = UpdateFailureDownload.open(candidates, opener: spy.open)

        XCTAssertEqual(spy.opened, [installerURL], "no candidate after a successful open")
        XCTAssertEqual(opened?.source, .githubInstaller)
    }

    func testAFailedOpenFallsThroughToTheNextCandidate() {
        let spy = OpenerSpy()
        spy.results = [false, false, true]
        let candidates = UpdateFailureDownload.candidates(
            installer: capture(cycleID: 1, version: "0.55.0"),
            currentCycleID: 1, offeredVersion: "0.55.0", feedInfoURL: feedInfoURL
        )

        let opened = UpdateFailureDownload.open(candidates, opener: spy.open)

        XCTAssertEqual(spy.opened, [installerURL, feedInfoURL, releasesPage])
        XCTAssertEqual(opened?.source, .releasesPage)
    }

    func testNonHTTPSAndRelativeCandidatesAreNeverOpened() {
        let spy = OpenerSpy()
        let candidates = [
            UpdateFailureCandidate(url: URL(string: "http://example.com/insecure.dmg")!,
                                   source: .githubInstaller),
            UpdateFailureCandidate(url: URL(string: "/releases/tag/v0.55.0")!,
                                   source: .feedRelease),
            UpdateFailureCandidate(url: releasesPage, source: .releasesPage),
        ]

        let opened = UpdateFailureDownload.open(candidates, opener: spy.open)

        XCTAssertEqual(spy.opened, [releasesPage],
                       "FR-005: every candidate must be an absolute HTTPS URL")
        XCTAssertEqual(opened?.source, .releasesPage)
    }

    /// The fallback is valid by construction, so it is attempted even when a
    /// caller assembled a list without it.
    func testTheReleasesPageIsAttemptedEvenIfItIsMissingFromTheList() {
        let spy = OpenerSpy()
        spy.results = [false]
        let candidates = [UpdateFailureCandidate(url: installerURL, source: .githubInstaller)]

        let opened = UpdateFailureDownload.open(candidates, opener: spy.open)

        XCTAssertEqual(spy.opened, [installerURL, releasesPage])
        XCTAssertEqual(opened?.source, .releasesPage)
    }

    // MARK: - Dialog content (FR-001 / FR-003)

    func testEachStageGetsItsOwnPlainLanguageSentence() {
        let sentences = UpdateFailureStage.allCases.map {
            UpdateFailureDialogContent.make(stage: $0, detail: nil).messageText
        }

        XCTAssertEqual(Set(sentences).count, UpdateFailureStage.allCases.count,
                       "FR-003: a generic message for every stage is exactly what this "
                       + "dialog replaces")
        for sentence in sentences {
            XCTAssertFalse(sentence.isEmpty)
        }
    }

    func testTheOperatingSystemDescriptionIsSecondaryAndOptional() {
        let withDetail = UpdateFailureDialogContent.make(stage: .download, detail: "The operation timed out.")
        let without = UpdateFailureDialogContent.make(stage: .download, detail: nil)

        XCTAssertEqual(withDetail.informativeText, "The operation timed out.")
        XCTAssertNil(without.informativeText)
        XCTAssertEqual(withDetail.messageText, without.messageText,
                       "the primary sentence is stage-specific, never error text")
    }

    func testTheDialogOffersExactlyThreeActionsWithTryAgainFirst() {
        XCTAssertEqual(UpdateFailureAction.ordered, [.tryAgain, .downloadFromWebsite, .cancel])
        XCTAssertEqual(UpdateFailureAction.ordered.map(\.title).count, 3)
    }

    // MARK: - FR-004 retry ordering

    private func makeController(
        sessionIsTerminal: Bool,
        candidates: [UpdateFailureCandidate] = [],
        opener: @escaping (URL) -> Bool = { _ in true },
        retry: @escaping () -> Void,
        present: @escaping UpdateFailureDialogController.Presenter
    ) -> UpdateFailureDialogController {
        UpdateFailureDialogController(
            content: .make(stage: .download, detail: nil),
            candidates: candidates,
            sessionIsTerminal: sessionIsTerminal,
            opener: opener,
            retry: retry,
            present: present
        )
    }

    /// Captures the dialog's callback so a test can "click" a button.
    private final class PresenterSpy {
        private(set) var presentations = 0
        private var respond: ((UpdateFailureAction) -> Void)?

        func present(_ content: UpdateFailureDialogContent,
                     _ respond: @escaping (UpdateFailureAction) -> Void) {
            presentations += 1
            self.respond = respond
        }

        func click(_ action: UpdateFailureAction) { respond?(action) }
    }

    func testTryAgainBeforeTheTerminalSignalQueuesExactlyOneRetry() {
        var retries = 0
        let presenter = PresenterSpy()
        let controller = makeController(sessionIsTerminal: false,
                                        retry: { retries += 1 },
                                        present: presenter.present)
        controller.present()

        presenter.click(.tryAgain)
        XCTAssertEqual(retries, 0,
                       "FR-004: a retry started before the failed session finishes races "
                       + "the updater's own teardown")

        controller.sessionDidFinish()
        XCTAssertEqual(retries, 1)

        controller.sessionDidFinish()
        XCTAssertEqual(retries, 1, "the signal is consumed once")
    }

    func testTryAgainAfterTheTerminalSignalStartsImmediately() {
        var retries = 0
        let presenter = PresenterSpy()
        let controller = makeController(sessionIsTerminal: true,
                                        retry: { retries += 1 },
                                        present: presenter.present)
        controller.present()

        presenter.click(.tryAgain)

        XCTAssertEqual(retries, 1, "the session was already terminal when the dialog appeared")
    }

    func testRepeatedTryAgainNeverStacksRetries() {
        var retries = 0
        let presenter = PresenterSpy()
        let controller = makeController(sessionIsTerminal: false,
                                        retry: { retries += 1 },
                                        present: presenter.present)
        controller.present()

        presenter.click(.tryAgain)
        presenter.click(.tryAgain)
        controller.sessionDidFinish()
        presenter.click(.tryAgain)

        XCTAssertEqual(retries, 1)
    }

    func testCancelStartsNothing() {
        var retries = 0
        let spy = OpenerSpy()
        let presenter = PresenterSpy()
        let controller = makeController(sessionIsTerminal: true,
                                        candidates: [.init(url: installerURL, source: .githubInstaller)],
                                        opener: spy.open,
                                        retry: { retries += 1 },
                                        present: presenter.present)
        controller.present()

        presenter.click(.cancel)
        controller.sessionDidFinish()

        XCTAssertEqual(retries, 0)
        XCTAssertEqual(spy.opened, [], "parity with the stock alert: Cancel does nothing")
    }

    func testDownloadFromWebsiteOpensTheBrowserAndNeverRetries() {
        var retries = 0
        let spy = OpenerSpy()
        let presenter = PresenterSpy()
        let controller = makeController(sessionIsTerminal: true,
                                        candidates: [.init(url: installerURL, source: .githubInstaller)],
                                        opener: spy.open,
                                        retry: { retries += 1 },
                                        present: presenter.present)
        controller.present()

        presenter.click(.downloadFromWebsite)
        controller.sessionDidFinish()

        XCTAssertEqual(spy.opened, [installerURL])
        XCTAssertEqual(retries, 0)
    }

    func testAQueuedRetryIsDroppedOnTeardown() {
        var retries = 0
        let presenter = PresenterSpy()
        let controller = makeController(sessionIsTerminal: false,
                                        retry: { retries += 1 },
                                        present: presenter.present)
        controller.present()
        presenter.click(.tryAgain)

        controller.teardown()
        controller.sessionDidFinish()

        XCTAssertEqual(retries, 0, "a queued retry must not outlive the app that queued it")
    }

    func testTheDialogIsPresentedExactlyOncePerOccurrence() {
        let presenter = PresenterSpy()
        let controller = makeController(sessionIsTerminal: true,
                                        retry: {},
                                        present: presenter.present)

        controller.present()
        controller.present()

        XCTAssertEqual(presenter.presentations, 1, "FR-001: at most one dialog per occurrence")
    }
}
