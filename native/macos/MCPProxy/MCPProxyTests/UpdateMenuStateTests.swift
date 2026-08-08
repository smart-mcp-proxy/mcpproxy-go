// UpdateMenuStateTests.swift
// MCPProxyTests
//
// Spec 092 FR-016 / FR-017 — one owner for the update item, and never a
// one-click action the app cannot perform.

import XCTest
@testable import MCPProxy

final class UpdateMenuStateTests: XCTestCase {

    private func entries(
        feed: String? = nil,
        legacy: String? = nil,
        blocked: UpdateBlockedReason? = nil,
        suppressed: Bool = false
    ) -> [UpdateMenuEntry] {
        UpdateMenuState.entries(
            feedVersion: feed, legacyVersion: legacy, blocked: blocked,
            nudgesSuppressed: suppressed
        )
    }

    private let blockedFixture = UpdateBlockedReason(
        menuTitle: "Can’t update", explanation: "because", fallback: "do this instead"
    )

    // MARK: - Nothing to say

    func testNoSourcesProduceNoEntries() {
        XCTAssertTrue(entries().isEmpty)
    }

    func testABlockedReasonIsNotAnnouncedWhenThereIsNoUpdate() {
        XCTAssertTrue(entries(blocked: blockedFixture).isEmpty,
                      "telling an up-to-date user their app cannot be updated is noise")
    }

    // MARK: - Single source

    func testFeedOnlyOwnsTheOneClickItem() {
        XCTAssertEqual(entries(feed: "0.55.0"), [.oneClick(version: "0.55.0")])
    }

    func testLegacyOnlyPresentsAsBrowserGuidance() {
        XCTAssertEqual(entries(legacy: "0.55.0"), [.browserGuidance(version: "0.55.0")],
                       "FR-017: never a one-click action the legacy check cannot perform")
    }

    // MARK: - Both sources (the FR-017 dedupe)

    func testEqualVersionsDeduplicateToASingleItem() {
        XCTAssertEqual(entries(feed: "0.55.0", legacy: "0.55.0"),
                       [.oneClick(version: "0.55.0")])
    }

    func testEqualVersionsAcrossTheVPrefixStillDeduplicate() {
        XCTAssertEqual(entries(feed: "0.55.0", legacy: "v0.55.0").count, 1)
    }

    func testALowerLegacyVersionIsSwallowedByTheFeed() {
        XCTAssertEqual(entries(feed: "0.55.0", legacy: "0.54.1"),
                       [.oneClick(version: "0.55.0")],
                       "FR-017: no competing nudge for the same or lower version")
    }

    func testANewerLegacyVersionIsOfferedAlongsideTheInstallableOne() {
        // The feed lags behind GitHub (the appcast job has not run yet). Both
        // facts are true and neither may masquerade as the other.
        XCTAssertEqual(entries(feed: "0.55.0", legacy: "0.56.0"),
                       [.oneClick(version: "0.55.0"), .browserGuidance(version: "0.56.0")])
    }

    func testPrereleaseOrderingUsesSemVerPrecedence() {
        // rc.10 > rc.2 — the bug FR-006 fixed must not come back through here.
        XCTAssertEqual(entries(feed: "0.55.0-rc.10", legacy: "0.55.0-rc.2"),
                       [.oneClick(version: "0.55.0-rc.10")])
    }

    // MARK: - FR-016

    func testABlockedAppNeverGetsAOneClickItem() {
        let result = entries(feed: "0.55.0", blocked: blockedFixture)
        XCTAssertEqual(result, [.blocked(blockedFixture), .browserGuidance(version: "0.55.0")],
                       "an item that cannot install must not pretend it can")
    }

    func testTheBlockedReasonAccompaniesLegacyGuidanceToo() {
        let result = entries(legacy: "0.55.0", blocked: blockedFixture)
        XCTAssertEqual(result, [.blocked(blockedFixture), .browserGuidance(version: "0.55.0")])
    }

    // MARK: - FR-015 suppression

    func testSuppressedNudgesHideEveryOffer() {
        XCTAssertTrue(entries(feed: "0.55.0", legacy: "0.56.0", suppressed: true).isEmpty)
    }

    func testSuppressionStillReportsWhyUpdatingIsImpossible() {
        XCTAssertEqual(entries(feed: "0.55.0", blocked: blockedFixture, suppressed: true),
                       [.blocked(blockedFixture)],
                       "FR-016 forbids failing silently; that is not a nudge")
    }
}
