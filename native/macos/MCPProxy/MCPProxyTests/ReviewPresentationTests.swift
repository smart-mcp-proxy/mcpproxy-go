import XCTest
@testable import MCPProxy

/// Spec 109 fix-review-screen: the macOS review sheet shows the same scan
/// coverage banner, headline and tool state as the Web review screen.
final class ReviewPresentationTests: XCTestCase {
    private func scan(_ json: String) throws -> ReviewScan {
        try JSONDecoder().decode(ReviewScan.self, from: Data(json.utf8))
    }

    private func review(quarantined: Bool, tools: [(String, String, Bool)]) throws -> ServerReviewResponse {
        let toolJSON = tools.map { name, status, disabled in
            "{\"name\":\"\(name)\",\"description\":\"d\",\"tier\":\"read\",\"approval_status\":\"\(status)\",\"disabled\":\(disabled),\"scan_verdict\":\"clean\"}"
        }.joined(separator: ",")
        let json = "{\"server\":{\"name\":\"fixture\",\"quarantined\":\(quarantined),\"definitions_captured\":true},\"tools\":[\(toolJSON)]}"
        return try JSONDecoder().decode(ServerReviewResponse.self, from: Data(json.utf8))
    }

    func testScanDecodesCoverageFieldsAndOldPayloadsStillDecode() throws {
        let covered = try scan(#"{"verdict":"clean","risk_score":0,"coverage":"stale","tools_scanned":5,"unscanned_tools":["notes"]}"#)
        XCTAssertEqual(covered.coverage, "stale")
        XCTAssertEqual(covered.toolsScanned, 5)
        XCTAssertEqual(covered.unscannedTools, ["notes"])

        let old = try scan(#"{"verdict":"clean","risk_score":0,"report_id":"scan-1"}"#)
        XCTAssertNil(old.coverage)
        XCTAssertNil(old.toolsScanned)
        XCTAssertNil(old.unscannedTools)
        // A payload without coverage reads as "no completed scan", never as a covering clean scan.
        let banner = try XCTUnwrap(ReviewPresentation.scanBanner(old, definitionsCaptured: true))
        XCTAssertEqual(banner.text, "Not scanned yet.")
        XCTAssertEqual(banner.action, .scanNow)
    }

    func testScanBannerForEveryCoverage() throws {
        let current = try XCTUnwrap(ReviewPresentation.scanBanner(
            scan(#"{"verdict":"clean","risk_score":0,"coverage":"current","tools_scanned":5}"#), definitionsCaptured: true))
        XCTAssertEqual(current.text, "Baseline scan: clean · risk 0/100 · covers all 5 tools")
        XCTAssertEqual(current.severity, .success)
        XCTAssertEqual(current.action, .none)

        let warnings = try XCTUnwrap(ReviewPresentation.scanBanner(
            scan(#"{"verdict":"warnings","risk_score":30,"coverage":"current","tools_scanned":5}"#), definitionsCaptured: true))
        XCTAssertEqual(warnings.severity, .warning)
        XCTAssertTrue(warnings.text.contains("risk 30/100"))

        let dangerous = try XCTUnwrap(ReviewPresentation.scanBanner(
            scan(#"{"verdict":"dangerous","risk_score":90,"coverage":"current","tools_scanned":5}"#), definitionsCaptured: true))
        XCTAssertEqual(dangerous.severity, .error)

        let stale = try XCTUnwrap(ReviewPresentation.scanBanner(
            scan(#"{"verdict":"clean","risk_score":0,"coverage":"stale","tools_scanned":5,"unscanned_tools":["a","b"]}"#), definitionsCaptured: true))
        XCTAssertEqual(stale.text, "Scan out of date: 2 tool definitions changed or were added after the last scan (a, b). Last result: clean.")
        XCTAssertEqual(stale.severity, .warning)
        XCTAssertEqual(stale.action, .rescan)
        XCTAssertFalse(stale.text.contains("risk"))

        let staleOne = try XCTUnwrap(ReviewPresentation.scanBanner(
            scan(#"{"verdict":"clean","coverage":"stale","unscanned_tools":["notes"]}"#), definitionsCaptured: true))
        XCTAssertEqual(staleOne.text, "Scan out of date: 1 tool definition changed or was added after the last scan (notes). Last result: clean.")

        let notCaptured = try XCTUnwrap(ReviewPresentation.scanBanner(
            scan(#"{"verdict":"clean","risk_score":0,"coverage":"not_captured"}"#), definitionsCaptured: false))
        XCTAssertEqual(notCaptured.text, "Scan not checked against tool definitions: they have not been captured yet.")
        XCTAssertEqual(notCaptured.severity, .warning)
        XCTAssertEqual(notCaptured.action, .fetchDefinitions)

        // definitions_captured:false wins over whatever coverage the payload claims.
        let forced = try XCTUnwrap(ReviewPresentation.scanBanner(
            scan(#"{"verdict":"clean","risk_score":0,"coverage":"current","tools_scanned":5}"#), definitionsCaptured: false))
        XCTAssertEqual(forced.action, .fetchDefinitions)

        let toolsNotScanned = try XCTUnwrap(ReviewPresentation.scanBanner(
            scan(#"{"verdict":"clean","risk_score":0,"coverage":"tools_not_scanned"}"#), definitionsCaptured: true))
        XCTAssertEqual(toolsNotScanned.text, "The last scan did not analyse tool definitions (0 exported).")
        XCTAssertEqual(toolsNotScanned.severity, .warning)
        XCTAssertEqual(toolsNotScanned.action, .rescan)

        let scanning = try XCTUnwrap(ReviewPresentation.scanBanner(
            scan(#"{"verdict":"not_scanned","coverage":"scanning"}"#), definitionsCaptured: true))
        XCTAssertEqual(scanning.text, "Scan in progress…")
        XCTAssertEqual(scanning.severity, .info)
        XCTAssertEqual(scanning.action, .none)

        let none = try XCTUnwrap(ReviewPresentation.scanBanner(
            scan(#"{"verdict":"not_scanned","coverage":"none"}"#), definitionsCaptured: true))
        XCTAssertEqual(none.text, "Not scanned yet.")
        XCTAssertEqual(none.severity, .warning)
        XCTAssertEqual(none.action, .scanNow)

        XCTAssertNil(ReviewPresentation.scanBanner(nil, definitionsCaptured: true))
    }

    func testHeadlineForTheThreeStates() throws {
        let quarantined = try ReviewPresentation.headline(review(quarantined: true, tools: [("a", "pending", false)]))
        XCTAssertEqual(quarantined.state, .review)
        XCTAssertEqual(quarantined.title, "Review fixture")
        XCTAssertEqual(quarantined.subtitle, "Review tool definitions before changing what agents can call.")

        let mixed = try ReviewPresentation.headline(review(quarantined: false, tools: [("a", "approved", false), ("b", "changed", false)]))
        XCTAssertEqual(mixed.state, .review)
        XCTAssertEqual(mixed.title, "Review fixture")
        XCTAssertEqual(mixed.subtitle, "1 tool needs review. Agents cannot call it until approved.")

        let approved = try ReviewPresentation.headline(review(quarantined: false, tools: [("a", "approved", false), ("b", "approved", false), ("c", "approved", true)]))
        XCTAssertEqual(approved.state, .approved)
        XCTAssertEqual(approved.title, "fixture is approved")
        XCTAssertEqual(approved.subtitle, "All 3 tools approved (1 blocked). New or changed tools come back here for review.")
    }

    func testToolStateSelectsTheControl() throws {
        let tools = try review(quarantined: false, tools: [("p", "pending", false), ("c", "changed", false), ("a", "approved", false), ("b", "approved", true)]).tools
        XCTAssertEqual(ReviewPresentation.toolState(tools[0], quarantined: false), .approveReject)
        XCTAssertEqual(ReviewPresentation.toolState(tools[1], quarantined: false), .approveReject)
        XCTAssertEqual(ReviewPresentation.toolState(tools[2], quarantined: false), .approved)
        XCTAssertEqual(ReviewPresentation.toolState(tools[3], quarantined: false), .blocked)
        for tool in tools { XCTAssertEqual(ReviewPresentation.toolState(tool, quarantined: true), .allowToggle) }
    }
}
