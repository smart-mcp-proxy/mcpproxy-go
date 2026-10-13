// UpdateServiceCandidateTests.swift
// MCPProxyTests
//
// Spec 095 FR-005 — the service side of "Download from Website": which GitHub
// result is allowed to represent a failed update session, and what the dialog
// does with it.
//
// The rule this suite exists to protect: `downloadURL` is a best-effort link
// with no identity (installer DMG, else any DMG, else the release page), so it
// cannot be offered as the recovery for a session it may have nothing to do
// with. Only a true `-installer.dmg`, stamped with the cycle that produced it,
// qualifies.

import XCTest
@testable import MCPProxy

@MainActor
final class UpdateServiceCandidateTests: XCTestCase {

    private let installerURL = "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/download/v0.55.0/mcpproxy-0.55.0-darwin-arm64-installer.dmg"
    private let plainDMGURL = "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/download/v0.55.0/mcpproxy-0.55.0-darwin-arm64.dmg"
    private let htmlURL = "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/tag/v0.55.0"

    private func makeService(feed: StubFeedUpdater? = StubFeedUpdater()) -> (UpdateService, StubFeedUpdater?) {
        let service = UpdateService(
            environment: [:],
            hostBundleURL: URL(fileURLWithPath: "/Applications/MCPProxy.app"),
            feedUpdater: feed,
            legacyCheck: {}
        )
        service.applyCorePolicy(.legacyDefault)
        feed?.resetRecordings()
        return (service, feed)
    }

    private func asset(_ name: String, _ url: String) -> [String: Any] {
        ["name": name, "browser_download_url": url]
    }

    // MARK: - Cycle identity

    func testEveryCheckStartsANewCycle() {
        let (service, _) = makeService()
        let start = service.checkCycleID

        service.checkForUpdates()
        service.checkForUpdatesInBackground()

        XCTAssertEqual(service.checkCycleID, start + 2)
    }

    func testASkippedUnattendedCheckIsNotACycle() {
        let service = UpdateService(
            environment: ["MCPPROXY_DISABLE_AUTO_UPDATE": "true"],
            hostBundleURL: URL(fileURLWithPath: "/Applications/MCPProxy.app"),
            feedUpdater: StubFeedUpdater(),
            legacyCheck: {}
        )
        let start = service.checkCycleID

        service.checkForUpdatesInBackground()

        XCTAssertEqual(service.checkCycleID, start, "no check ran, so no cycle happened")
    }

    // MARK: - Asset selection

    func testOnlyATrueInstallerAssetIsCapturedAsTheInstaller() throws {
        let selection = UpdateService.selectDownloadTargets(
            assets: [asset("mcpproxy-0.55.0-darwin-arm64.dmg", plainDMGURL),
                     asset("mcpproxy-0.55.0-darwin-arm64-installer.dmg", installerURL)],
            arch: "arm64",
            htmlURL: htmlURL
        )

        XCTAssertEqual(selection.downloadURL, installerURL)
        XCTAssertEqual(selection.installerURL, URL(string: installerURL))
    }

    /// The existing preference order — installer, else any matching DMG, else
    /// the release page — is unchanged; only the separate installer capture is
    /// new.
    func testAPlainDMGStillDrivesTheDownloadURLButIsNotAnInstaller() {
        let selection = UpdateService.selectDownloadTargets(
            assets: [asset("mcpproxy-0.55.0-darwin-arm64.dmg", plainDMGURL)],
            arch: "arm64",
            htmlURL: htmlURL
        )

        XCTAssertEqual(selection.downloadURL, plainDMGURL)
        XCTAssertNil(selection.installerURL,
                     "an unsigned DMG is not the installer FR-005 candidate 1 means")
    }

    func testAssetsForAnotherArchitectureAreIgnored() {
        let selection = UpdateService.selectDownloadTargets(
            assets: [asset("mcpproxy-0.55.0-darwin-amd64-installer.dmg", installerURL)],
            arch: "arm64",
            htmlURL: htmlURL
        )

        XCTAssertEqual(selection.downloadURL, htmlURL)
        XCTAssertNil(selection.installerURL)
    }

    func testNoAssetsFallsBackToTheReleasePage() {
        let selection = UpdateService.selectDownloadTargets(assets: [], arch: "arm64", htmlURL: htmlURL)

        XCTAssertEqual(selection.downloadURL, htmlURL)
        XCTAssertNil(selection.installerURL)
    }

    // MARK: - Applying a GitHub result

    func testApplyingAResultPublishesTheLegacyStateAndStampsTheInstaller() throws {
        let (service, _) = makeService()
        service.checkForUpdates()
        let cycle = service.checkCycleID

        service.applyGitHubResult(
            cycleID: cycle,
            version: "0.55.0",
            selection: .init(downloadURL: installerURL, installerURL: URL(string: installerURL)),
            releaseNotes: "notes"
        )

        XCTAssertEqual(service.latestVersion, "0.55.0")
        XCTAssertEqual(service.downloadURL, installerURL)
        XCTAssertEqual(service.releaseNotes, "notes")
        let capture = try XCTUnwrap(service.latestGitHubInstaller)
        XCTAssertEqual(capture.cycleID, cycle)
        XCTAssertEqual(capture.version, "0.55.0")
    }

    func testAResultWithoutAnInstallerCapturesNothing() {
        let (service, _) = makeService()
        service.checkForUpdates()

        service.applyGitHubResult(
            cycleID: service.checkCycleID,
            version: "0.55.0",
            selection: .init(downloadURL: plainDMGURL, installerURL: nil),
            releaseNotes: nil
        )

        XCTAssertEqual(service.downloadURL, plainDMGURL)
        XCTAssertNil(service.latestGitHubInstaller)
    }

    // MARK: - Candidate assembly at occurrence time

    private func serviceWithInstaller() -> UpdateService {
        let (service, _) = makeService()
        service.checkForUpdates()
        service.applyGitHubResult(
            cycleID: service.checkCycleID,
            version: "0.55.0",
            selection: .init(downloadURL: installerURL, installerURL: URL(string: installerURL)),
            releaseNotes: nil
        )
        return service
    }

    func testTheInstallerIsOfferedForTheVersionTheSessionWasUpdatingTo() {
        let service = serviceWithInstaller()

        let candidates = service.failureDownloadCandidates(offeredVersion: "0.55.0", feedInfoURL: nil)

        XCTAssertEqual(candidates.map(\.source), [.githubInstaller, .releasesPage])
    }

    func testTheInstallerIsNotOfferedForADifferentVersion() {
        let service = serviceWithInstaller()

        let candidates = service.failureDownloadCandidates(offeredVersion: "0.56.0", feedInfoURL: nil)

        XCTAssertEqual(candidates.map(\.source), [.releasesPage])
    }

    func testAnInstallerFromAnEarlierCycleIsNotOfferedToAFeedStageFailure() {
        let service = serviceWithInstaller()
        service.checkForUpdates()   // a new cycle, with no GitHub result yet

        let candidates = service.failureDownloadCandidates(offeredVersion: nil, feedInfoURL: nil)

        XCTAssertEqual(candidates.map(\.source), [.releasesPage])
    }

    func testTheFeedReleasePageSitsBetweenTheInstallerAndTheFallback() {
        let service = serviceWithInstaller()
        let infoURL = URL(string: htmlURL)!

        let candidates = service.failureDownloadCandidates(offeredVersion: "0.55.0", feedInfoURL: infoURL)

        XCTAssertEqual(candidates.map(\.source), [.githubInstaller, .feedRelease, .releasesPage])
    }

    // MARK: - Driving the dialog (FR-001 / FR-002 / FR-004 / FR-005)

    /// Captures the dialog and lets a test click one of its buttons.
    private final class DialogSpy {
        private(set) var contents: [UpdateFailureDialogContent] = []
        private var respond: ((UpdateFailureAction) -> Void)?

        func present(_ content: UpdateFailureDialogContent,
                     _ respond: @escaping (UpdateFailureAction) -> Void) {
            contents.append(content)
            self.respond = respond
        }

        func click(_ action: UpdateFailureAction) { respond?(action) }
    }

    private func failure(
        stage: UpdateFailureStage = .download,
        offeredVersion: String? = "0.55.0",
        feedInfoURL: URL? = nil,
        dialogRequested: Bool = true
    ) -> FeedUpdateFailure {
        FeedUpdateFailure(stage: stage, offeredVersion: offeredVersion, feedInfoURL: feedInfoURL,
                          dialogRequested: dialogRequested, errorDescription: "The download failed.")
    }

    func testASuppressedOccurrenceNeverShowsADialog() {
        let service = serviceWithInstaller()
        let dialog = DialogSpy()
        service.presentFailureDialog = dialog.present

        service.feedUpdater(didFailSession: failure(dialogRequested: false))

        XCTAssertEqual(dialog.contents.count, 0,
                       "FR-002: a scheduled session that showed no UI records only")
    }

    func testAVisibleOccurrenceShowsTheStageSpecificDialog() throws {
        let service = serviceWithInstaller()
        let dialog = DialogSpy()
        service.presentFailureDialog = dialog.present

        service.feedUpdater(didFailSession: failure(stage: .install))

        XCTAssertEqual(dialog.contents.count, 1)
        let content = try XCTUnwrap(dialog.contents.first)
        XCTAssertEqual(content, .make(stage: .install, detail: "The download failed."))
    }

    func testDownloadFromWebsiteOpensTheAssembledCandidate() throws {
        let service = serviceWithInstaller()
        let dialog = DialogSpy()
        var opened: [URL] = []
        service.presentFailureDialog = dialog.present
        service.openFailureDownload = { opened.append($0); return true }

        service.feedUpdater(didFailSession: failure())
        dialog.click(.downloadFromWebsite)

        XCTAssertEqual(opened, [URL(string: installerURL)!])
    }

    func testTryAgainStartsExactlyOneNewUserInitiatedCheck() {
        let (service, feed) = makeService()
        let dialog = DialogSpy()
        service.presentFailureDialog = dialog.present
        feed?.resetRecordings()

        service.feedUpdater(didFailSession: failure())
        dialog.click(.tryAgain)

        XCTAssertEqual(feed?.checks, [true],
                       "FR-004: Try Again is a user-initiated check, and exactly one")
    }

    /// The occurrence arrives FROM the terminal-completion callback, so by the
    /// time the dialog exists the failed session is already finished.
    func testTheRetryDoesNotWaitForAFurtherSignal() {
        let (service, feed) = makeService()
        let dialog = DialogSpy()
        service.presentFailureDialog = dialog.present
        feed?.resetRecordings()

        service.feedUpdater(didFailSession: failure())
        dialog.click(.tryAgain)
        dialog.click(.tryAgain)

        XCTAssertEqual(feed?.checks, [true])
    }

    // MARK: - Recording (FR-009 / FR-010)

    /// Collects the stages handed to the recorder from whatever thread the
    /// detached recording task ends up on.
    private final class RecorderSpy: @unchecked Sendable {
        private let lock = NSLock()
        private var stages: [UpdateFailureStage] = []
        private let received: XCTestExpectation?

        init(expecting: XCTestExpectation? = nil) { self.received = expecting }

        func record(_ stage: UpdateFailureStage) {
            lock.lock()
            stages.append(stage)
            lock.unlock()
            received?.fulfill()
        }

        var all: [UpdateFailureStage] {
            lock.lock()
            defer { lock.unlock() }
            return stages
        }
    }

    func testEveryOccurrenceIsRecordedOnceWithItsClassifiedStage() {
        let (service, _) = makeService()
        let expectation = expectation(description: "both occurrences recorded")
        expectation.expectedFulfillmentCount = 2
        let recorder = RecorderSpy(expecting: expectation)
        service.recordUpdateFailure = { stage in recorder.record(stage) }

        service.feedUpdater(didFailSession: failure(stage: .install, dialogRequested: false))
        service.feedUpdater(didFailSession: failure(stage: .appcast, dialogRequested: false))

        wait(for: [expectation], timeout: 2)
        XCTAssertEqual(recorder.all, [.install, .appcast],
                       "a silent occurrence is exactly what the counters exist for")
    }

    /// The postpone notice and the on-quit tripwire are tray-synthesized
    /// advisories, not update-session failures: Spec 095 excludes them from
    /// classification, from the dialog, and from the counters.
    func testATraySynthesizedAdvisoryIsNeitherRecordedNorDialogued() {
        let (service, _) = makeService()
        let dialog = DialogSpy()
        let recorder = RecorderSpy()
        service.presentFailureDialog = dialog.present
        service.recordUpdateFailure = { stage in recorder.record(stage) }

        service.feedUpdater(didFailWith: "The update was not installed: the core is still running.")

        XCTAssertEqual(recorder.all, [])
        XCTAssertEqual(dialog.contents.count, 0)
        XCTAssertEqual(service.lastErrorMessage,
                       "The update was not installed: the core is still running.")
    }

    /// The recording is fire-and-forget on a path of its own: a core that never
    /// answers must not hold up the dialog for a moment.
    func testASlowRecorderDoesNotDelayTheDialog() {
        let (service, _) = makeService()
        let dialog = DialogSpy()
        service.presentFailureDialog = dialog.present
        service.recordUpdateFailure = { _ in
            try? await Task.sleep(nanoseconds: 30 * NSEC_PER_SEC)
        }

        service.feedUpdater(didFailSession: failure())

        XCTAssertEqual(dialog.contents.count, 1)
    }
}
