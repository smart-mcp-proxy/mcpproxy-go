// SparkleFeedURLTests.swift
// MCPProxyTests
//
// Spec 092 FR-013 — the per-architecture feed. A Sparkle appcast has no
// architecture selector, so this rewrite is the only thing standing between an
// Intel user and a successful "update" to an Apple-Silicon build. It is also
// half of a contract with `.github/workflows/release.yml`, which names the
// generated files `appcast-arm64.xml` / `appcast-amd64.xml`.

import XCTest
@testable import MCPProxy

final class SparkleFeedURLTests: XCTestCase {

    func testTheDefaultFeedIsRewrittenPerArchitecture() {
        XCTAssertEqual(
            SparkleFeedURL.archSpecific("https://mcpproxy.app/appcast.xml", arch: "arm64"),
            "https://mcpproxy.app/appcast-arm64.xml"
        )
        XCTAssertEqual(
            SparkleFeedURL.archSpecific("https://mcpproxy.app/appcast.xml", arch: "amd64"),
            "https://mcpproxy.app/appcast-amd64.xml"
        )
    }

    /// Both channels × both architectures. The RC row is the one that was
    /// wrong: `prerelease.yml` publishes `appcast-beta-<arch>.xml`, so an RC
    /// client asking for `appcast-<arch>.xml` reads the stable feed — which by
    /// FR-014's design never carries an RC — and is never offered anything.
    func testTheChannelSelectsTheFeedFile() {
        let cases: [(UpdateChannel, String, String)] = [
            (.stable, "arm64", "https://mcpproxy.app/appcast-arm64.xml"),
            (.stable, "amd64", "https://mcpproxy.app/appcast-amd64.xml"),
            (.rc, "arm64", "https://mcpproxy.app/appcast-beta-arm64.xml"),
            (.rc, "amd64", "https://mcpproxy.app/appcast-beta-amd64.xml")
        ]
        for (channel, arch, expected) in cases {
            XCTAssertEqual(
                SparkleFeedURL.archSpecific("https://mcpproxy.app/appcast.xml",
                                            arch: arch, channel: channel),
                expected,
                "\(channel) / \(arch)"
            )
        }
    }

    /// The file names are half a contract with the two release workflows.
    func testFileNamesMatchWhatTheWorkflowsGenerate() {
        XCTAssertEqual(SparkleFeedURL.fileName(channel: .stable, arch: "arm64"), "appcast-arm64.xml")
        XCTAssertEqual(SparkleFeedURL.fileName(channel: .rc, arch: "amd64"), "appcast-beta-amd64.xml")
    }

    func testNestedPathsKeepTheirDirectory() {
        XCTAssertEqual(
            SparkleFeedURL.archSpecific(
                "https://github.com/o/r/releases/latest/download/appcast.xml", arch: "arm64"
            ),
            "https://github.com/o/r/releases/latest/download/appcast-arm64.xml"
        )
    }

    func testAnOperatorSuppliedFeedNameIsLeftAlone() {
        // Rewriting a URL the operator chose deliberately would be worse than
        // fetching it: they may already be serving a merged or universal feed.
        for url in [
            "https://example.com/feeds/mcpproxy.xml",
            "https://example.com/appcast-arm64.xml",
            "https://example.com/appcast.rss"
        ] {
            XCTAssertEqual(SparkleFeedURL.archSpecific(url, arch: "arm64"), url)
        }
    }

    func testQueryStringsSurviveTheRewrite() {
        XCTAssertEqual(
            SparkleFeedURL.archSpecific("https://example.com/appcast.xml?t=1", arch: "amd64"),
            "https://example.com/appcast-amd64.xml?t=1"
        )
    }

    func testGarbageInputIsReturnedUnchanged() {
        XCTAssertEqual(SparkleFeedURL.archSpecific("", arch: "arm64"), "")
        XCTAssertEqual(SparkleFeedURL.archSpecific("appcast.xml", arch: "arm64"),
                       "appcast-arm64.xml",
                       "a bare relative name still resolves; Sparkle rejects it later")
    }
}
