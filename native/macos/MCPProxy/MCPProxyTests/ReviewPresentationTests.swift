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

    func testTrustedServerWithNoCapturedToolsReadsAsApproved() throws {
        let trusted = try ReviewPresentation.headline(review(quarantined: false, tools: []))
        XCTAssertEqual(trusted.state, .approved)
        XCTAssertEqual(trusted.title, "fixture is approved")
        XCTAssertTrue(trusted.subtitle.contains("No tool definitions"))

        let quarantined = try ReviewPresentation.headline(review(quarantined: true, tools: []))
        XCTAssertEqual(quarantined.state, .review)
    }

    func testQuarantineEscapesTheServerNameInThePath() throws {
        let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
        let source = try String(contentsOf: root.appendingPathComponent("MCPProxy/API/APIClient.swift"))
        XCTAssertTrue(source.contains(#"/api/v1/servers/\(Self.escapePathComponent(id))/quarantine"#))
        XCTAssertFalse(source.contains(#"/api/v1/servers/\(id)/quarantine"#))
    }

    // MARK: Default selection (Spec 109 fix-review-defaults, D43)

    private func selectionTool(_ name: String, defaultAllowed: Bool?, description: String = "d", verdict: String = "clean", heldReason: String? = nil, heldSignals: [String]? = nil) throws -> ReviewTool {
        let field = defaultAllowed.map { ",\"default_allowed\":\($0)" } ?? ""
        let held = (heldReason.map { ",\"held_reason\":\"\($0)\"" } ?? "") + (heldSignals.map { ",\"held_signals\":[" + $0.map { "\"\($0)\"" }.joined(separator: ",") + "]" } ?? "")
        let json = "{\"name\":\"\(name)\",\"description\":\"\(description)\",\"tier\":\"read\",\"approval_status\":\"pending\",\"disabled\":false,\"scan_verdict\":\"\(verdict)\"\(held)\(field)}"
        return try JSONDecoder().decode(ReviewTool.self, from: Data(json.utf8))
    }

    func testDefaultSelectionFollowsDefaultAllowed() throws {
        let tools = [
            try selectionTool("read_a", defaultAllowed: true),
            try selectionTool("write_a", defaultAllowed: false),
            // An older core sends no field: it reads as false, so a mismatched core fails closed.
            try selectionTool("old_core", defaultAllowed: nil),
        ]
        XCTAssertEqual(tools[0].defaultAllowed, true)
        XCTAssertNil(tools[2].defaultAllowed)
        XCTAssertEqual(ReviewPresentation.initialSelection(tools), ["read_a"])
    }

    func testMergeSelectionKeepsUnchecksAndDropsStaleChecks() throws {
        let readA = try selectionTool("read_a", defaultAllowed: true)
        let writeA = try selectionTool("write_a", defaultAllowed: false)

        // An explicit uncheck always survives a reload.
        XCTAssertEqual(ReviewPresentation.mergeSelection([readA], choices: ["read_a": .init(allowed: false, tool: readA)]), [])
        // An explicit check survives while the payload is the one the user saw.
        XCTAssertEqual(ReviewPresentation.mergeSelection([writeA], choices: ["write_a": .init(allowed: true, tool: writeA)]), ["write_a"])
        // A changed definition or verdict falls back to the default.
        let redefined = try selectionTool("write_a", defaultAllowed: false, description: "now also deletes")
        XCTAssertEqual(ReviewPresentation.mergeSelection([redefined], choices: ["write_a": .init(allowed: true, tool: writeA)]), [])
        let rescanned = try selectionTool("write_a", defaultAllowed: false, verdict: "warnings")
        XCTAssertEqual(ReviewPresentation.mergeSelection([rescanned], choices: ["write_a": .init(allowed: true, tool: writeA)]), [])
        // A hold that appears after the click (held_reason / held_signals only) is a changed payload too.
        let held = try selectionTool("write_a", defaultAllowed: false, heldReason: "scan_findings", heldSignals: ["tpa.x"])
        XCTAssertEqual(held.heldReason, "scan_findings")
        XCTAssertEqual(held.heldSignals, ["tpa.x"])
        XCTAssertEqual(ReviewPresentation.mergeSelection([held], choices: ["write_a": .init(allowed: true, tool: writeA)]), [])
        let reheld = try selectionTool("write_a", defaultAllowed: false, heldReason: "scan_coverage", heldSignals: ["tpa.x"])
        XCTAssertEqual(ReviewPresentation.mergeSelection([reheld], choices: ["write_a": .init(allowed: true, tool: held)]), [])
        // A choice for a tool that is gone is ignored.
        XCTAssertEqual(ReviewPresentation.mergeSelection([readA], choices: ["gone": .init(allowed: true, tool: writeA)]), ["read_a"])
    }

    func testApproveLabels() {
        XCTAssertEqual(ReviewPresentation.approveLabel(selected: 3, total: 9, definitionsCaptured: true), "Approve Server (3 of 9 tools)")
        XCTAssertEqual(ReviewPresentation.approveLabel(selected: 0, total: 9, definitionsCaptured: true), "Approve Server (0 of 9 tools)")
        XCTAssertEqual(ReviewPresentation.approveLabel(selected: 1, total: 1, definitionsCaptured: true), "Approve Server (1 of 1 tool)")
        XCTAssertEqual(ReviewPresentation.approveLabel(selected: 0, total: 0, definitionsCaptured: true), "Approve Without Seeing Tools")
        XCTAssertEqual(ReviewPresentation.approveLabel(selected: 0, total: 5, definitionsCaptured: false), "Approve Without Seeing Tools")
        XCTAssertEqual(ReviewPresentation.approveAllLabel(total: 9), "Approve All (9 tools)")
        XCTAssertEqual(ReviewPresentation.approveAllLabel(total: 1), "Approve All (1 tool)")
    }

    func testSelectionHintMatchesWeb() throws {
        var url = URL(fileURLWithPath: #filePath)
        for _ in 0..<5 { url.deleteLastPathComponent() }
        let web = try String(contentsOf: url.appendingPathComponent("frontend/src/utils/reviewPresentation.ts"))
        XCTAssertTrue(web.contains("'\(ReviewPresentation.selectionHint)'"), "the macOS hint must be the Web sentence")
        XCTAssertEqual(ReviewPresentation.selectionHint, "Only read-only tools with a clean scan start checked. Unchecked tools stay blocked after approval until you enable them on the Tools tab.")
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
