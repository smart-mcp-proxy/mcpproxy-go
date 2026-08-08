// SemanticVersionTests.swift
// MCPProxyTests
//
// Spec 092 FR-006. Every version comparison that can stop a process or offer
// an update goes through `SemanticVersion`, so the ordering it implements is
// pinned here rather than left to the two call sites to get right.
//
// The headline case is `rc.10` vs `rc.2`: the comparison this type replaced
// sorted prerelease identifiers as strings, which made 0.54.0-rc.10 look
// OLDER than 0.54.0-rc.2. On the update path that offers a downgrade as an
// "update"; on the supersede path it kills a newer core to start an older one.

import XCTest
@testable import MCPProxy

final class SemanticVersionTests: XCTestCase {

    // MARK: - The bug this type exists for

    func testNumericPrereleaseIdentifiersCompareNumerically() {
        XCTAssertEqual(SemanticVersion.compare("1.0.0-rc.10", "1.0.0-rc.2"), 1,
                       "rc.10 must outrank rc.2 — as strings it does not")
        XCTAssertEqual(SemanticVersion.compare("1.0.0-rc.2", "1.0.0-rc.10"), -1)
        XCTAssertEqual(SemanticVersion.compare("0.54.0-rc.9", "0.54.0-rc.11"), -1)
    }

    /// The public adapter the menu uses must inherit the fix, not just the
    /// type behind it.
    func testUpdateServiceComparisonInheritsNumericOrdering() {
        XCTAssertGreaterThan(UpdateService.compareSemver("1.0.0-rc.10", "1.0.0-rc.2"), 0)
        XCTAssertLessThan(UpdateService.compareSemver("1.0.0-rc.2", "1.0.0-rc.10"), 0)
    }

    // MARK: - §11.3 prerelease < release

    func testPrereleaseRanksBelowTheMatchingRelease() {
        XCTAssertEqual(SemanticVersion.compare("1.0.0-rc.1", "1.0.0"), -1)
        XCTAssertEqual(SemanticVersion.compare("1.0.0", "1.0.0-rc.1"), 1)
        XCTAssertEqual(SemanticVersion.compare("1.0.0-rc.1", "1.0.1-rc.1"), -1,
                       "core precedence is decided before prerelease is even looked at")
    }

    // MARK: - Core ordering

    func testCoreOrdering() {
        XCTAssertEqual(SemanticVersion.compare("1.2.3", "1.2.3"), 0)
        XCTAssertEqual(SemanticVersion.compare("1.2.4", "1.2.3"), 1)
        XCTAssertEqual(SemanticVersion.compare("1.3.0", "1.2.9"), -1 * -1) // 1.3.0 > 1.2.9
        XCTAssertEqual(SemanticVersion.compare("2.0.0", "10.0.0"), -1,
                       "major is numeric, not lexicographic")
        XCTAssertEqual(SemanticVersion.compare("0.54", "0.54.0"), 0,
                       "a two-component core is padded, not rejected")
    }

    // MARK: - Tolerated forms

    func testLeadingVIsTolerated() {
        XCTAssertEqual(SemanticVersion.compare("v1.2.3", "1.2.3"), 0)
        XCTAssertEqual(SemanticVersion.compare("V1.2.4", "v1.2.3"), 1)
        XCTAssertEqual(SemanticVersion.parse("v0.54.0-rc.1")?.description, "0.54.0-rc.1")
    }

    func testBuildMetadataIsIgnoredForPrecedence() {
        XCTAssertEqual(SemanticVersion.compare("1.2.3+build.5", "1.2.3+build.9"), 0,
                       "SemVer §10: build metadata does not affect precedence")
        XCTAssertEqual(SemanticVersion.compare("1.2.3+abc", "1.2.3"), 0)
        XCTAssertEqual(SemanticVersion.compare("1.0.0-rc.2+sha.deadbee", "1.0.0-rc.10+sha.0000000"), -1,
                       "metadata is stripped before the prerelease comparison")
        XCTAssertEqual(SemanticVersion.parse("1.2.3+abc")?.description, "1.2.3",
                       "metadata is discarded, not retained")
    }

    // MARK: - Malformed input yields NO decision

    func testMalformedVersionsReturnNil() {
        for bad in ["", "   ", "development", "dev", "1.2.3.4", "1..3", "abc.def.ghi",
                    "1.2.x", "-1.2.3", "1.2.3-", "v", "1.2.3-rc..1", "1.2.3+"] {
            XCTAssertNil(SemanticVersion.compare(bad, "1.2.3"),
                         "\(bad.debugDescription) must not be comparable")
            XCTAssertNil(SemanticVersion.parse(bad),
                         "\(bad.debugDescription) must not parse")
        }
    }

    /// The adapter's documented lenient behaviour: unknown → 0 → "no update".
    /// Pinned so the leniency stays confined to the nudge path.
    func testUpdateServiceAdapterTreatsMalformedAsNoUpdate() {
        XCTAssertEqual(UpdateService.compareSemver("development", "1.2.3"), 0)
        XCTAssertEqual(UpdateService.compareSemver("1.2.3", "not-a-version"), 0)
    }

    // MARK: - §11.4 identifier rules

    func testAlphanumericIdentifiersUseASCIIOrder() {
        XCTAssertEqual(SemanticVersion.compare("1.0.0-alpha", "1.0.0-beta"), -1)
        XCTAssertEqual(SemanticVersion.compare("1.0.0-beta", "1.0.0-alpha"), 1)
    }

    func testNumericIdentifiersRankBelowAlphanumericOnes() {
        // §11.4.3
        XCTAssertEqual(SemanticVersion.compare("1.0.0-1", "1.0.0-alpha"), -1)
        XCTAssertEqual(SemanticVersion.compare("1.0.0-alpha", "1.0.0-1"), 1)
    }

    func testLargerIdentifierSetWinsWhenPrefixesMatch() {
        // §11.4.4
        XCTAssertEqual(SemanticVersion.compare("1.0.0-rc.1", "1.0.0-rc.1.1"), -1)
        XCTAssertEqual(SemanticVersion.compare("1.0.0-alpha", "1.0.0-alpha.1"), -1)
    }

    /// The canonical example chain from semver.org §11.
    func testSpecExampleChainIsStrictlyIncreasing() {
        let chain = [
            "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta",
            "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0"
        ]
        for (lower, higher) in zip(chain, chain.dropFirst()) {
            XCTAssertEqual(SemanticVersion.compare(lower, higher), -1,
                           "\(lower) must sort below \(higher)")
        }
    }

    // MARK: - §9: numeric prerelease identifiers may not have leading zeros

    func testLeadingZeroNumericPrereleaseIdentifiersAreMalformed() {
        // Accepting "rc.01" would make it a second spelling of "rc.1", and
        // whichever way the two compared, one answer would be wrong. FR-006
        // says malformed means no decision, so parsing must refuse.
        for raw in ["1.0.0-rc.01", "1.0.0-01", "1.0.0-0123", "1.0.0-rc.1.007"] {
            XCTAssertNil(SemanticVersion.parse(raw), "\(raw) is not a SemVer version")
            XCTAssertNil(SemanticVersion.compare(raw, "1.0.0-rc.1"),
                         "\(raw) must produce no decision, not a guess")
        }
    }

    func testASingleZeroIdentifierIsStillValid() {
        // §9 bans leading zeros, not the number zero.
        XCTAssertNotNil(SemanticVersion.parse("1.0.0-rc.0"))
        XCTAssertEqual(SemanticVersion.compare("1.0.0-rc.0", "1.0.0-rc.1"), -1)
        // Alphanumeric identifiers are unaffected: "0abc" is not numeric.
        XCTAssertNotNil(SemanticVersion.parse("1.0.0-0abc"))
        // Build metadata is exempt (§10) and never affects precedence.
        XCTAssertEqual(SemanticVersion.compare("1.0.0+007", "1.0.0+8"), 0)
    }

    // MARK: - §11.4.1: numeric identifiers are unbounded

    func testHugeNumericIdentifiersCompareNumerically() {
        // Beyond Int64. Coercing to a bounded integer overflows to nil, and the
        // fallback ASCII comparison would order "10000000000000000000000" below
        // "9999999999999999999999" — a downgrade offered as an update.
        XCTAssertEqual(
            SemanticVersion.compare("1.0.0-rc.9999999999999999999999",
                                    "1.0.0-rc.10000000000000000000000"), -1)
        XCTAssertEqual(
            SemanticVersion.compare("1.0.0-rc.10000000000000000000000",
                                    "1.0.0-rc.9999999999999999999999"), 1)
        XCTAssertEqual(
            SemanticVersion.compare("1.0.0-rc.99999999999999999999999",
                                    "1.0.0-rc.99999999999999999999999"), 0)
        // A huge numeric identifier still ranks below an alphanumeric one.
        XCTAssertEqual(
            SemanticVersion.compare("1.0.0-99999999999999999999999", "1.0.0-alpha"), -1)
    }

    // MARK: - Comparable conformance agrees with compare()

    func testComparableConformanceMatchesCompare() {
        let older = SemanticVersion.parse("0.54.0-rc.2")!
        let newer = SemanticVersion.parse("0.54.0-rc.10")!
        XCTAssertTrue(older < newer)
        XCTAssertFalse(newer < older)
        XCTAssertEqual([newer, older].sorted(), [older, newer])
        XCTAssertTrue(newer.isPrerelease)
        XCTAssertFalse(SemanticVersion.parse("0.54.0")!.isPrerelease)
    }
}
