import XCTest
@testable import MCPProxy

/// The Activity detail panel renders a code_execution's `code` argument as
/// source, not as an escaped JSON string — and one-line agent scripts are
/// broken at statement boundaries for readability.
final class ActivityCodeFormattingTests: XCTestCase {

    func testOneLineScriptBreaksAfterSemicolons() {
        let code = "var a = call_tool('s', 't', {x: 1}); var b = 2; ({a: a, b: b})"
        let formatted = ActivityDetailView.formatScriptForDisplay(code)
        XCTAssertEqual(formatted.components(separatedBy: "\n").count, 3,
                       "two statement ends -> three lines, got: \(formatted)")
        XCTAssertTrue(formatted.hasPrefix("var a = call_tool('s', 't', {x: 1});\n"))
    }

    func testSemicolonInsideAStringLiteralNeverSplits() {
        let code = "var s = 'a; b'; done(s)"
        let formatted = ActivityDetailView.formatScriptForDisplay(code)
        XCTAssertEqual(formatted, "var s = 'a; b';\ndone(s)")
    }

    func testEscapedQuoteInsideStringDoesNotEndTheString() {
        let code = "var s = 'it\\'s; fine'; next()"
        let formatted = ActivityDetailView.formatScriptForDisplay(code)
        XCTAssertEqual(formatted, "var s = 'it\\'s; fine';\nnext()")
    }

    func testSemicolonInsideABlockIndents() {
        let code = "for (var i = 0; i < 3; i++) { push(i); } done()"
        let formatted = ActivityDetailView.formatScriptForDisplay(code)
        XCTAssertTrue(formatted.contains("{ push(i);\n  }"),
                      "statement inside a block gets one indent level, got: \(formatted)")
        XCTAssertTrue(formatted.hasPrefix("for (var i = 0; i < 3; i++) {"),
                      "a for-header's semicolons are inside parens and must not split, got: \(formatted)")
    }

    func testAlreadyFormattedScriptIsShownVerbatim() {
        let code = "line1();\nline2();"
        XCTAssertEqual(ActivityDetailView.formatScriptForDisplay(code), code)
    }

    func testShortCorrelationIdPassesThroughAndLongOneKeepsTheTail() {
        XCTAssertEqual(ActivityView.shortCorrelationId("abc-42"), "abc-42")
        let long = "1787315011981233000-code_execution-17"
        let short = ActivityView.shortCorrelationId(long)
        XCTAssertTrue(short.hasPrefix("…"))
        XCTAssertTrue(long.hasSuffix(short.dropFirst()))
    }
}
