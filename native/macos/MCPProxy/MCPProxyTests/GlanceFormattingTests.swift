import XCTest
@testable import MCPProxy

final class GlanceFormattingTests: XCTestCase {

    // MARK: - Status symbol

    func testStatusSymbolDistinguishesSuccessErrorAndOther() {
        XCTAssertEqual(GlanceFormatting.statusSymbolName(for: Self.entry(status: "success")), "checkmark.circle")
        XCTAssertEqual(GlanceFormatting.statusSymbolName(for: Self.entry(status: "error")), "xmark.circle")
        XCTAssertEqual(GlanceFormatting.statusSymbolName(for: Self.entry(status: "blocked")), "exclamationmark.circle")
    }

    // MARK: - Row label

    func testUpstreamCallLabelIsServerColonTool() {
        let entry = Self.entry(type: "tool_call", server: "github", tool: "create_issue")
        XCTAssertEqual(GlanceFormatting.rowLabel(for: entry), "github:create_issue")
    }

    func testBuiltInLabelIsJustTheToolName() {
        let entry = Self.entry(type: "internal_tool_call", tool: "retrieve_tools")
        XCTAssertEqual(GlanceFormatting.rowLabel(for: entry), "retrieve_tools")
    }

    func testAlreadyPrefixedToolNameIsNotDoubled() {
        let entry = Self.entry(type: "tool_call", server: "github", tool: "github:create_issue")
        XCTAssertEqual(GlanceFormatting.rowLabel(for: entry), "github:create_issue")
    }

    func testBuiltInWithAServerNameStillLabelsAsTheBareBuiltIn() {
        // A failed call_tool_* wrapper is persisted carrying BOTH a tool_name
        // and the server_name it was dispatched at. Only `type == "tool_call"`
        // composes `server:tool`, so this must stay the bare built-in name.
        let entry = Self.entry(type: "internal_tool_call", server: "fixture", tool: "call_tool_read", status: "error")
        XCTAssertEqual(GlanceFormatting.rowLabel(for: entry), "call_tool_read")
    }

    func testLabelFallsBackToTypeWhenNothingIsNamed() {
        let entry = Self.entry(type: "oauth_event")
        XCTAssertEqual(GlanceFormatting.rowLabel(for: entry), "oauth_event")
    }

    // MARK: - Truncation

    func testShortTextIsNotTruncated() {
        XCTAssertEqual(GlanceFormatting.middleTruncated("github:create", limit: 20), "github:create")
    }

    func testMiddleTruncationKeepsHeadAndTailAtExactlyTheLimit() {
        let result = GlanceFormatting.middleTruncated("github:create_issue_from_template", limit: 12)
        XCTAssertEqual(result.count, 12)
        XCTAssertTrue(result.hasPrefix("github"), "kept the server prefix, got \(result)")
        XCTAssertTrue(result.hasSuffix("late"), "kept the tool tail, got \(result)")
        XCTAssertTrue(result.contains("\u{2026}"))
    }

    func testTextExactlyAtTheLimitIsNotTruncated() {
        // Pins `>` rather than `>=`: equal length must pass through untouched.
        XCTAssertEqual(GlanceFormatting.middleTruncated("abcdef", limit: 6), "abcdef")
    }

    func testTruncationToZeroIsEmpty() {
        XCTAssertEqual(GlanceFormatting.middleTruncated("abcdef", limit: 0), "")
    }

    func testTruncationToOneCharacterIsJustTheEllipsis() {
        XCTAssertEqual(GlanceFormatting.middleTruncated("abcdef", limit: 1), "\u{2026}")
    }

    // MARK: - Relative time

    func testCompactAgeUnits() {
        XCTAssertEqual(GlanceFormatting.compactAge(0), "0s")
        XCTAssertEqual(GlanceFormatting.compactAge(12), "12s")
        XCTAssertEqual(GlanceFormatting.compactAge(59), "59s")
        XCTAssertEqual(GlanceFormatting.compactAge(60), "1m")
        XCTAssertEqual(GlanceFormatting.compactAge(3599), "59m")
        XCTAssertEqual(GlanceFormatting.compactAge(3600), "1h")
        XCTAssertEqual(GlanceFormatting.compactAge(86_400), "1d")
        XCTAssertEqual(GlanceFormatting.compactAge(-5), "0s")
    }

    func testRelativeTimeParsesFractionalAndPlainTimestamps() {
        let fractional = "2027-01-15T08:00:00.123Z"
        let plain = "2027-01-15T08:00:00Z"
        XCTAssertNotNil(GlanceFormatting.parseTimestamp(fractional))
        XCTAssertNotNil(GlanceFormatting.parseTimestamp(plain))

        let base = GlanceFormatting.parseTimestamp(plain)!
        XCTAssertEqual(GlanceFormatting.relativeTime(plain, now: base.addingTimeInterval(12)), "12s")
        XCTAssertEqual(GlanceFormatting.relativeTime(fractional, now: base.addingTimeInterval(180)), "3m")
    }

    func testUnparseableTimestampFallsBackToTheRawString() {
        XCTAssertEqual(GlanceFormatting.relativeTime("not-a-date"), "not-a-date")
    }

    // MARK: - Helpers

    static func entry(
        id: String = "a1",
        type: String = "tool_call",
        server: String? = nil,
        tool: String? = nil,
        status: String = "success"
    ) -> ActivityEntry {
        var json: [String: Any] = [
            "id": id,
            "type": type,
            "status": status,
            "timestamp": "2027-01-15T08:00:00Z"
        ]
        if let server { json["server_name"] = server }
        if let tool { json["tool_name"] = tool }
        let data = try! JSONSerialization.data(withJSONObject: json)
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(ActivityEntry.self, from: data)
    }
}
