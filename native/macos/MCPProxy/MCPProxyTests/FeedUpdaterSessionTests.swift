// FeedUpdaterSessionTests.swift
// MCPProxyTests
//
// Spec 095 — the update SESSION, which is the unit both halves of this feature
// count in: exactly one occurrence per failed session, whether or not anyone saw
// a window.
//
// Sparkle itself cannot be exercised here (it reads SUFeedURL/SUPublicEDKey from
// a real .app bundle, which `swift test` has none of), so the bookkeeping lives
// in a Sparkle-free tracker that the delegate callbacks merely forward into. The
// one thing that cannot be proven this way — that the delegate names still match
// Sparkle's selectors — is asserted directly against the ObjC runtime at the
// bottom of this file, because a Swift method whose imported name drifts
// compiles fine and is then simply never called.

import XCTest
@testable import MCPProxy

final class FeedUpdaterSessionTests: XCTestCase {

    private let sparkle = UpdateFailureClassifier.sparkleErrorDomain

    private func sparkleError(_ code: Int, description: String = "Sparkle said no") -> NSError {
        NSError(domain: sparkle, code: code,
                userInfo: [NSLocalizedDescriptionKey: description])
    }

    // MARK: - Occurrence emission

    /// The failure nobody sees is the one telemetry exists for: a scheduled
    /// session that never presented UI aborts without the driver being called at
    /// all, and must still be counted (FR-002's suppress row records).
    func testASilentScheduledFailureIsStillAnOccurrence() {
        let session = UpdateSessionTracker()

        let failure = session.sessionDidFinish(error: sparkleError(2001))

        XCTAssertEqual(failure?.stage, .download)
        XCTAssertEqual(failure?.dialogRequested, false,
                       "the updater never asked for an error to be shown")
    }

    /// The driver was called, so Sparkle's own visibility matrix said "show" —
    /// the tray inherits that decision rather than re-deriving it.
    func testAStashedDriverErrorRequestsTheDialogAtCycleEnd() {
        let session = UpdateSessionTracker()
        session.driverDidShowError(sparkleError(2001, description: "The download failed."))

        let failure = session.sessionDidFinish(error: sparkleError(2001))

        XCTAssertEqual(failure?.dialogRequested, true)
        XCTAssertEqual(failure?.errorDescription, "The download failed.",
                       "FR-003: the OS description is carried for the dialog only")
    }

    func testOneSessionProducesAtMostOneOccurrence() {
        let session = UpdateSessionTracker()
        session.driverDidShowError(sparkleError(3001))

        XCTAssertNotNil(session.sessionDidFinish(error: sparkleError(3001)))
        XCTAssertNil(session.sessionDidFinish(error: sparkleError(3001)),
                     "a repeated terminal callback describes the SAME failure")
    }

    // MARK: - Provenance latch

    func testTheDownloadCallbackDecidesTheStage() {
        let session = UpdateSessionTracker()
        session.downloadDidFail()

        let failure = session.sessionDidFinish(error: NSError(domain: NSURLErrorDomain, code: -1005))

        XCTAssertEqual(failure?.stage, .download,
                       "a generic abort after failedToDownloadUpdate is still a download failure")
    }

    func testTheLatchDoesNotLeakIntoTheNextSession() {
        let session = UpdateSessionTracker()
        session.downloadDidFail()
        _ = session.sessionDidFinish(error: NSError(domain: NSURLErrorDomain, code: -1005))

        session.sessionDidStart()
        let failure = session.sessionDidFinish(error: NSError(domain: NSURLErrorDomain, code: -1005))

        XCTAssertEqual(failure?.stage, .other)
    }

    /// Even without an explicit start — a scheduled session Sparkle begins on
    /// its own timer — the first sign of activity belongs to the NEW session,
    /// and the previous one's evidence is already gone.
    func testTheLatchIsResetByTheEndOfTheSessionItBelongsTo() {
        let session = UpdateSessionTracker()
        session.downloadDidFail()
        _ = session.sessionDidFinish(error: sparkleError(2001))

        session.driverDidShowError(sparkleError(3000))
        let failure = session.sessionDidFinish(error: NSError(domain: "com.example", code: 1))

        XCTAssertEqual(failure?.stage, .other)
    }

    // MARK: - Non-occurrences

    /// A canceled download ends the cycle with NO error (Sparkle 2.9.3 has no
    /// download-canceled code), which is the only signal that it was a choice.
    func testACycleThatEndsWithoutAnErrorIsNotAnOccurrence() {
        let session = UpdateSessionTracker()
        session.downloadDidFail()

        XCTAssertNil(session.sessionDidFinish(error: nil))
    }

    func testNoUpdateFoundIsNotAnOccurrence() {
        let session = UpdateSessionTracker()

        XCTAssertNil(session.sessionDidFinish(error: sparkleError(1001)))
    }

    func testAStashedErrorDoesNotSurviveASessionThatEndedCleanly() {
        let session = UpdateSessionTracker()
        session.driverDidShowError(sparkleError(2001))
        XCTAssertNil(session.sessionDidFinish(error: nil))

        session.sessionDidStart()
        let failure = session.sessionDidFinish(error: sparkleError(3000))

        XCTAssertEqual(failure?.dialogRequested, false,
                       "the stash belongs to the session that produced it")
    }

    // MARK: - What the occurrence carries

    func testTheOfferedItemIsCarriedIntoTheOccurrence() throws {
        let infoURL = URL(string: "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/tag/v0.55.0")!
        let session = UpdateSessionTracker()
        session.didFindValidUpdate(version: "0.55.0", infoURL: infoURL)

        let failure = try XCTUnwrap(session.sessionDidFinish(error: sparkleError(2001)))

        XCTAssertEqual(failure.offeredVersion, "0.55.0")
        XCTAssertEqual(failure.feedInfoURL, infoURL)
    }

    func testAFeedStageFailureCarriesNoOfferedVersion() throws {
        let session = UpdateSessionTracker()

        let failure = try XCTUnwrap(session.sessionDidFinish(error: sparkleError(1002)))

        XCTAssertEqual(failure.stage, .appcast)
        XCTAssertNil(failure.offeredVersion)
        XCTAssertNil(failure.feedInfoURL)
    }

    func testTheOfferedItemIsResetForTheNextSession() throws {
        let session = UpdateSessionTracker()
        session.didFindValidUpdate(version: "0.55.0", infoURL: nil)
        _ = session.sessionDidFinish(error: sparkleError(2001))

        session.sessionDidStart()
        let failure = try XCTUnwrap(session.sessionDidFinish(error: sparkleError(1002)))

        XCTAssertNil(failure.offeredVersion)
    }

    // MARK: - Delegate wiring

    /// A Swift delegate method whose imported name drifts from Sparkle's
    /// selector still compiles — Objective-C optional protocol methods are
    /// matched by selector at runtime, not by the compiler. These three carry the
    /// whole feature, so they are checked against the runtime.
    func testTheSparkleDelegateSelectorsAreActuallyImplemented() {
        #if canImport(Sparkle)
        for name in SparkleFeedUpdater.requiredDelegateSelectors {
            XCTAssertTrue(
                SparkleFeedUpdater.instancesRespond(to: NSSelectorFromString(name)),
                "Sparkle would never call \(name) — the Swift name no longer maps to it"
            )
        }
        #endif
    }
}
