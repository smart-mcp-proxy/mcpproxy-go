import XCTest
@testable import MCPProxy

/// Spec 109 FR-073 (T166, #1394 item 1): the Home hub badge says "estimate"
/// while the core reports `ServerTokenMetrics.estimated`, exactly as the Token
/// Savings card and the Web Home chip do. Both Home surfaces read the one
/// `HomeTokenSavingsBadge`, so they cannot drift apart.
final class HomeTokenSavingsBadgeTests: XCTestCase {

    private func metrics(percent: Double, estimated: Bool) -> TokenMetrics {
        TokenMetrics(
            totalServerToolListSize: 4540,
            averageQueryResultSize: 340,
            savedTokens: 4200,
            savedTokensPercentage: percent,
            perServerToolListSizes: nil,
            estimated: estimated
        )
    }

    func testPercentTextFormatting() {
        XCTAssertEqual(HomeTokenSavingsBadge(metrics: metrics(percent: 99.996, estimated: false)).percentText, "99.99%")
        XCTAssertEqual(HomeTokenSavingsBadge(metrics: metrics(percent: 42.04, estimated: false)).percentText, "42.0%")
    }

    func testShowsEstimateFollowsMetrics() {
        let estimated = HomeTokenSavingsBadge(metrics: metrics(percent: 92.5, estimated: true))
        XCTAssertTrue(estimated.showsEstimate)
        XCTAssertEqual(HomeTokenSavingsBadge.estimateLabel, "estimate")

        let measured = HomeTokenSavingsBadge(metrics: metrics(percent: 92.5, estimated: false))
        XCTAssertFalse(measured.showsEstimate)
    }

    func testAccessibilityLabelNamesTheEstimate() {
        let estimated = HomeTokenSavingsBadge(metrics: metrics(percent: 92.5, estimated: true))
        XCTAssertTrue(estimated.accessibilityLabel.contains("estimate"))

        let measured = HomeTokenSavingsBadge(metrics: metrics(percent: 92.5, estimated: false))
        XCTAssertFalse(measured.accessibilityLabel.contains("estimate"))
        XCTAssertTrue(measured.accessibilityLabel.contains("92.5%"))
    }

    /// Source-reading guard (same style as `HomeRoutingTests`): the hub badge and
    /// the Token Savings card both go through the shared presentation, so the
    /// help text literal exists exactly once.
    func testHubBadgeUsesThePresentation() throws {
        let source = try homeSource()
        let hub = try XCTUnwrap(section(of: source, from: "private var hubSection", to: "private var"))
        XCTAssertTrue(hub.contains("HomeTokenSavingsBadge"),
                      "hubSection must render the shared HomeTokenSavingsBadge")
        XCTAssertTrue(hub.contains("HomeTokenSavingsBadge.estimateHelp"),
                      "the hub estimate capsule must use the shared help text")
        XCTAssertTrue(source.contains("HomeTokenSavingsBadge.estimateLabel"),
                      "the Token Savings card must reuse the shared estimate label")
        XCTAssertEqual(source.components(separatedBy: "simulated estimate from the current tool catalog").count - 1, 1,
                       "the estimate help text must live in one place")
    }

    private func section(of source: String, from start: String, to end: String) -> String? {
        guard let startRange = source.range(of: start) else { return nil }
        let rest = source[startRange.upperBound...]
        guard let endRange = rest.range(of: end) else { return String(rest) }
        return String(rest[..<endRange.lowerBound])
    }

    private func homeSource() throws -> String {
        let packageRoot = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent()   // MCPProxyTests
            .deletingLastPathComponent()   // package root
        let url = packageRoot.appendingPathComponent("MCPProxy/Views/HomeView.swift")
        XCTAssertTrue(FileManager.default.fileExists(atPath: url.path), "missing source file at \(url.path)")
        return try String(contentsOf: url, encoding: .utf8)
    }
}
