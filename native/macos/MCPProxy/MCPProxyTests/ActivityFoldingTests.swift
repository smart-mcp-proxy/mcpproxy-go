import XCTest
@testable import MCPProxy

/// Spec 109-k FR-071 (macOS half of parity row 20): in System events,
/// consecutive records of the same type and server within 60 s fold into one
/// summary row that expands in place. Presentation only — export is unfolded.
final class ActivityFoldingTests: XCTestCase {

    private func entry(_ id: String, type: String, server: String?, status: String = "success",
                       at timestamp: String) -> ActivityEntry {
        var json: [String: Any] = ["id": id, "type": type, "status": status, "timestamp": timestamp]
        if let server { json["server_name"] = server }
        let data = try! JSONSerialization.data(withJSONObject: json)
        return try! JSONDecoder().decode(ActivityEntry.self, from: data)
    }

    /// Newest first, as the API returns them.
    private func approvals(_ n: Int, server: String = "filesystem") -> [ActivityEntry] {
        (0..<n).map { i in
            entry("q\(i)", type: "tool_quarantine_change", server: server, status: "approved",
                  at: String(format: "2026-09-26T12:00:%02dZ", 59 - i))
        }
    }

    func testFourteenApprovalsFoldIntoOneSummaryRow() {
        let rows = ActivityFolding.fold(approvals(14))
        XCTAssertEqual(rows.count, 1)
        XCTAssertEqual(rows[0].count, 14)
        XCTAssertEqual(rows[0].summary, "filesystem: 14 tools approved")
        XCTAssertEqual(rows[0].members.map(\.id), (0..<14).map { "q\($0)" }, "order preserved for expansion")
    }

    func testDifferentServerBreaksTheRun() {
        let rows = ActivityFolding.fold(approvals(2) + approvals(2, server: "github").map {
            entry("g" + $0.id, type: $0.type, server: "github", status: "approved", at: "2026-09-26T12:00:40Z")
        })
        XCTAssertEqual(rows.map(\.count), [2, 2])
        XCTAssertEqual(rows[1].summary, "github: 2 tools approved")
    }

    func testDifferentTypeBreaksTheRun() {
        let rows = ActivityFolding.fold([
            entry("a", type: "config_change", server: nil, at: "2026-09-26T12:00:10Z"),
            entry("b", type: "server_change", server: nil, at: "2026-09-26T12:00:09Z"),
        ])
        XCTAssertEqual(rows.map(\.count), [1, 1])
        XCTAssertNil(rows[0].summary, "a single record is shown as itself")
    }

    func testGapOverSixtySecondsBreaksTheRun() {
        let rows = ActivityFolding.fold([
            entry("a", type: "server_change", server: "github", at: "2026-09-26T12:02:00Z"),
            entry("b", type: "server_change", server: "github", at: "2026-09-26T12:01:30Z"),
            entry("c", type: "server_change", server: "github", at: "2026-09-26T12:00:29Z"),
        ])
        XCTAssertEqual(rows.map(\.count), [2, 1])
        XCTAssertEqual(rows[0].summary, "github: 2 server change events")
    }

    func testQuarantineBatchNeedsTheSameAction() {
        let rows = ActivityFolding.fold([
            entry("a", type: "tool_quarantine_change", server: "fs", status: "approved", at: "2026-09-26T12:00:10Z"),
            entry("b", type: "tool_quarantine_change", server: "fs", status: "changed", at: "2026-09-26T12:00:09Z"),
        ])
        XCTAssertEqual(rows.map(\.count), [1, 1], "a summary must not print one verb for mixed actions")
    }

    func testRunIdSurvivesANewerMemberJoining() {
        let before = ActivityFolding.fold(approvals(3))
        let newer = entry("new", type: "tool_quarantine_change", server: "filesystem",
                          status: "approved", at: "2026-09-26T12:01:00Z")
        let after = ActivityFolding.fold([newer] + approvals(3))
        XCTAssertEqual(after.count, 1)
        XCTAssertEqual(after[0].count, 4)
        XCTAssertEqual(after[0].id, before[0].id, "an expanded run must stay expanded across a live reload")
    }

    func testDisabledFoldingReturnsOneRowPerRecord() {
        XCTAssertEqual(ActivityFolding.fold(approvals(5), enabled: false).map(\.count), [1, 1, 1, 1, 1])
    }
}
