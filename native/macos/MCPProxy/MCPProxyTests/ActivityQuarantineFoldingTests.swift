import XCTest
@testable import MCPProxy

// Spec 109-k (activity-scope-filters), acceptance scenario 6: "Switching to
// 'System events' shows one folded row 'filesystem: 14 tools approved'" —
// live QA found the macOS/Web Activity views entirely missing this folding
// (T122).
final class ActivityQuarantineFoldingTests: XCTestCase {
    private func quarantineEntry(
        id: String,
        server: String,
        tool: String,
        status: String = "approved",
        timestamp: String = "2026-09-20T09:00:00Z"
    ) -> ActivityEntry {
        ActivityEntry(
            id: id, type: "tool_quarantine_change", source: nil, serverName: server,
            toolName: tool, arguments: nil, response: nil, responseTruncated: nil,
            status: status, errorMessage: nil, durationMs: nil, timestamp: timestamp,
            sessionId: nil, requestId: nil, parentId: nil, metadata: nil,
            hasSensitiveData: false, detectionTypes: nil, maxSeverity: nil
        )
    }

    private func toolCallEntry(id: String, server: String, tool: String) -> ActivityEntry {
        ActivityEntry(
            id: id, type: "tool_call", source: nil, serverName: server, toolName: tool,
            arguments: nil, response: nil, responseTruncated: nil, status: "success",
            errorMessage: nil, durationMs: 5, timestamp: "2026-09-20T10:00:00Z",
            sessionId: nil, requestId: nil, parentId: nil, metadata: nil,
            hasSensitiveData: false, detectionTypes: nil, maxSeverity: nil
        )
    }

    func testFoldsFourteenPerToolApprovalsIntoOneSummaryRow() {
        let batch = (1...14).map {
            quarantineEntry(id: "qc-\($0)", server: "filesystem", tool: "tool_\($0)")
        }
        let folded = ActivityQuarantineFolding.fold(batch)

        XCTAssertEqual(folded.count, 1)
        XCTAssertEqual(folded[0].toolName, "filesystem: 14 tools approved")
        XCTAssertEqual(folded[0].id, "qc-1") // the lead's own id
        XCTAssertEqual(folded[0].serverName, "filesystem")
        XCTAssertEqual(folded[0].status, "approved")
    }

    func testDoesNotFoldASingleQuarantineChange() {
        let folded = ActivityQuarantineFolding.fold([quarantineEntry(id: "qc-1", server: "filesystem", tool: "read")])
        XCTAssertEqual(folded.count, 1)
        XCTAssertEqual(folded[0].toolName, "read") // untouched — not a batch
    }

    func testDoesNotFoldAcrossDifferentServers() {
        let entries = [
            quarantineEntry(id: "qc-1", server: "filesystem", tool: "a"),
            quarantineEntry(id: "qc-2", server: "filesystem", tool: "b"),
            quarantineEntry(id: "qc-3", server: "github", tool: "c"),
        ]
        let folded = ActivityQuarantineFolding.fold(entries)
        XCTAssertEqual(folded.count, 2)
        XCTAssertEqual(folded[0].toolName, "filesystem: 2 tools approved")
        XCTAssertEqual(folded[1].toolName, "c") // github's lone record, unfolded
    }

    func testDoesNotFoldAcrossDifferentStatuses() {
        let entries = [
            quarantineEntry(id: "qc-1", server: "filesystem", tool: "a", status: "approved"),
            quarantineEntry(id: "qc-2", server: "filesystem", tool: "b", status: "blocked"),
        ]
        let folded = ActivityQuarantineFolding.fold(entries)
        XCTAssertEqual(folded.count, 2)
        XCTAssertEqual(folded[0].toolName, "a")
        XCTAssertEqual(folded[1].toolName, "b")
    }

    func testLeavesOtherActivityTypesEntirelyAlone() {
        let entries = [
            toolCallEntry(id: "act-1", server: "filesystem", tool: "read"),
            quarantineEntry(id: "qc-1", server: "filesystem", tool: "a"),
            quarantineEntry(id: "qc-2", server: "filesystem", tool: "b"),
        ]
        let folded = ActivityQuarantineFolding.fold(entries)
        XCTAssertEqual(folded.count, 2)
        XCTAssertEqual(folded[0].id, "act-1")
        XCTAssertEqual(folded[0].type, "tool_call")
        XCTAssertEqual(folded[1].toolName, "filesystem: 2 tools approved")
    }

    func testEmptyInputFoldsToEmptyOutput() {
        XCTAssertEqual(ActivityQuarantineFolding.fold([]).count, 0)
    }

    // Verified zcode review finding: server+status agreement with no time
    // bound could fold two genuinely separate actions together whenever
    // nothing of a different type happened to land between them. macOS has
    // no run-expansion UI (unlike the Web table), so a wrong fold here is
    // not recoverable.
    func testDoesNotFoldTwoBatchesMoreThanFiveMinutesApart() {
        let entries = [
            quarantineEntry(id: "a", server: "filesystem", tool: "x", timestamp: "2026-08-21T10:00:00Z"),
            quarantineEntry(id: "b", server: "filesystem", tool: "y", timestamp: "2026-08-21T10:01:00Z"),
            quarantineEntry(id: "c", server: "filesystem", tool: "z", timestamp: "2026-08-21T10:30:00Z"),
        ]
        let folded = ActivityQuarantineFolding.fold(entries)
        XCTAssertEqual(folded.count, 2)
        XCTAssertEqual(folded[0].toolName, "filesystem: 2 tools approved")
        XCTAssertEqual(folded[1].id, "c")
        XCTAssertEqual(folded[1].toolName, "z")
    }

    func testFoldsWithinTheFiveMinuteWindowDespiteAGapBetweenRecords() {
        let entries = [
            quarantineEntry(id: "a", server: "filesystem", tool: "x", timestamp: "2026-08-21T10:00:00Z"),
            quarantineEntry(id: "b", server: "filesystem", tool: "y", timestamp: "2026-08-21T10:04:00Z"),
        ]
        XCTAssertEqual(ActivityQuarantineFolding.fold(entries).count, 1)
    }
}
