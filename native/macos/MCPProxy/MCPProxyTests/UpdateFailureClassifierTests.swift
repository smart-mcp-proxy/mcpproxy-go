// UpdateFailureClassifierTests.swift
// MCPProxyTests
//
// Spec 095 FR-007 / FR-008 — the whole normative classification table, driven
// through the pure function so no Sparkle object, no bundle and no network is
// involved. The classifier is the only thing that decides what leaves this
// machine (a four-value enum), so every row is asserted rather than sampled.

import XCTest
@testable import MCPProxy

final class UpdateFailureClassifierTests: XCTestCase {

    private let sparkle = UpdateFailureClassifier.sparkleErrorDomain

    private func classify(
        _ code: Int,
        domain: String? = nil,
        provenance: Bool = false,
        underlying: [(String, Int)] = []
    ) -> UpdateFailureStage? {
        UpdateFailureClassifier.classify(
            downloadProvenance: provenance,
            identity: UpdateFailureErrorIdentity(
                domain: domain ?? sparkle,
                code: code,
                underlying: underlying.map { .init(domain: $0.0, code: $0.1) }
            )
        )
    }

    // MARK: - Exclusions (FR-008): never an occurrence

    func testNoUpdateFoundIsNotAnOccurrence() {
        XCTAssertNil(classify(1001), "SUNoUpdateError is 'you are up to date', not a failure")
    }

    func testInstallationCanceledIsNotAnOccurrence() {
        XCTAssertNil(classify(4007))
    }

    func testAuthorizeLaterIsNotAnOccurrence() {
        XCTAssertNil(classify(4008))
    }

    /// The exclusions come FIRST in the precedence table: a session that saw a
    /// download failure and then ended on a cancellation is still not counted.
    func testAnExcludedCodeBeatsTheDownloadProvenanceLatch() {
        XCTAssertNil(classify(4007, provenance: true))
    }

    func testAnExcludedCodeIsRecognizedThroughTheUnderlyingChain() {
        XCTAssertNil(classify(4, domain: NSCocoaErrorDomain, underlying: [(sparkle, 1001)]))
    }

    // MARK: - Provenance latch (row 2)

    func testTheDownloadProvenanceLatchWinsOverTheErrorCode() {
        XCTAssertEqual(classify(3001, provenance: true), .download,
                       "failedToDownloadUpdate fired for this session, so the stage is "
                       + "download whatever the abort code says")
        XCTAssertEqual(classify(1000, provenance: true), .download)
        XCTAssertEqual(classify(-1009, domain: NSURLErrorDomain, provenance: true), .download)
    }

    // MARK: - Appcast codes (row 3)

    func testAppcastCodesClassifyAsAppcast() {
        for code in [1000, 1002, 1004, 3, 4] {
            XCTAssertEqual(classify(code), .appcast, "SUSparkleErrorDomain \(code)")
        }
    }

    // MARK: - Download codes (row 4)

    func testDownloadCodesClassifyAsDownload() {
        for code in [2000, 2001] {
            XCTAssertEqual(classify(code), .download, "SUSparkleErrorDomain \(code)")
        }
    }

    // MARK: - Install codes (row 5)

    /// 4000 (SUFileCopyFailure) and 4001 (SUAuthenticationFailure) are in the
    /// install range and are the two most likely to be dropped when the range is
    /// written as 4002…4012.
    func testInstallCodesClassifyAsInstall() {
        for code in [3000, 3001, 3002, 4000, 4001, 4002, 4003, 4004, 4005, 4006, 4009, 4010, 4012] {
            XCTAssertEqual(classify(code), .install, "SUSparkleErrorDomain \(code)")
        }
    }

    // MARK: - Underlying-chain recursion (row 6)

    func testASparkleCodeInsideAnotherDomainsErrorIsStillClassified() {
        XCTAssertEqual(
            classify(-1001, domain: NSURLErrorDomain, underlying: [(sparkle, 2001)]),
            .download
        )
        XCTAssertEqual(
            classify(4, domain: NSCocoaErrorDomain, underlying: [(NSPOSIXErrorDomain, 2), (sparkle, 3001)]),
            .install
        )
    }

    /// The FIRST Sparkle code in the chain decides; a second one further down
    /// must not override it.
    func testTheFirstSparkleCodeInTheChainWins() {
        XCTAssertEqual(
            classify(-1, domain: NSURLErrorDomain, underlying: [(sparkle, 1000), (sparkle, 3001)]),
            .appcast
        )
    }

    // MARK: - Fallback (row 7)

    func testUnknownDomainsAndCodesFallBackToOther() {
        XCTAssertEqual(classify(-1009, domain: NSURLErrorDomain), .other,
                       "a bare network error with no download provenance is not "
                       + "attributable to a stage")
        XCTAssertEqual(classify(5000), .other, "API misuse")
        XCTAssertEqual(classify(1003), .other, "running from a disk image")
        XCTAssertEqual(classify(1005), .other, "translocated")
        XCTAssertEqual(classify(1006), .other)
        XCTAssertEqual(classify(1007), .other)
        XCTAssertEqual(classify(1), .other)
        XCTAssertEqual(classify(2), .other)
        XCTAssertEqual(classify(4011), .other, "the commented-out installer code is not ours to map")
        XCTAssertEqual(classify(99999, domain: "com.example.whatever"), .other)
    }

    // MARK: - Wire vocabulary

    func testStageRawValuesAreTheWireVocabulary() {
        XCTAssertEqual(UpdateFailureStage.allCases.map(\.rawValue),
                       ["appcast", "download", "install", "other"])
    }

    // MARK: - NSError bridging

    func testIdentityIsExtractedFromAnNSErrorChain() {
        let inner = NSError(domain: sparkle, code: 2001)
        let outer = NSError(domain: NSURLErrorDomain, code: -1009,
                            userInfo: [NSUnderlyingErrorKey: inner])

        let identity = UpdateFailureErrorIdentity(outer)

        XCTAssertEqual(identity.domain, NSURLErrorDomain)
        XCTAssertEqual(identity.code, -1009)
        XCTAssertEqual(identity.underlying, [.init(domain: sparkle, code: 2001)])
        XCTAssertEqual(UpdateFailureClassifier.classify(downloadProvenance: false, identity: identity),
                       .download)
    }

    /// A cyclic or absurdly deep chain must not hang the classifier.
    func testUnderlyingChainExtractionIsBounded() {
        var error = NSError(domain: "com.example.deep", code: 0)
        for depth in 1...64 {
            error = NSError(domain: "com.example.deep", code: depth,
                            userInfo: [NSUnderlyingErrorKey: error])
        }

        let identity = UpdateFailureErrorIdentity(error)

        XCTAssertLessThanOrEqual(identity.underlying.count, 16)
        XCTAssertEqual(UpdateFailureClassifier.classify(downloadProvenance: false, identity: identity),
                       .other)
    }
}
