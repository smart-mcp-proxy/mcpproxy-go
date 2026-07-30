import XCTest
@testable import MCPProxy

final class GlanceSelectionTests: XCTestCase {

    // MARK: - Rule 2

    func testUpstreamToolCallIsAlwaysIncluded() {
        let entry = Self.entry(id: "1", type: "tool_call", server: "github", tool: "create_issue")
        XCTAssertTrue(GlanceSelection.qualifies(entry))
    }

    // MARK: - Rule 3

    func testDiscoveryAndExecutionBuiltInsAreIncluded() {
        for tool in ["retrieve_tools", "code_execution", "describe_tool"] {
            let entry = Self.entry(id: tool, type: "internal_tool_call", tool: tool)
            XCTAssertTrue(GlanceSelection.qualifies(entry), "\(tool) should qualify")
        }
    }

    func testSuccessfulCallToolWrapperIsExcluded() {
        let entry = Self.entry(id: "w", type: "internal_tool_call", tool: "call_tool_write")
        XCTAssertFalse(GlanceSelection.qualifies(entry))
    }

    func testFailedCallToolWrapperIsIncluded() {
        let entry = Self.entry(id: "w", type: "internal_tool_call", tool: "call_tool_write", status: "error")
        XCTAssertTrue(GlanceSelection.qualifies(entry), "pre-dispatch failures have no upstream record")
    }

    // MARK: - Rule 1 beats rule 3

    func testManagementBuiltInsAreExcludedEvenWhenTheyFail() {
        for tool in ["upstream_servers", "quarantine_security"] {
            let ok = Self.entry(id: "\(tool)-ok", type: "internal_tool_call", tool: tool)
            let bad = Self.entry(id: "\(tool)-bad", type: "internal_tool_call", tool: tool, status: "error")
            XCTAssertFalse(GlanceSelection.qualifies(ok), "\(tool) success must be excluded")
            XCTAssertFalse(GlanceSelection.qualifies(bad), "\(tool) failure must be excluded (rule 1 beats rule 3)")
        }
    }

    func testNonActivityTypesAreExcluded() {
        XCTAssertFalse(GlanceSelection.qualifies(Self.entry(id: "s", type: "security_scan")))
        XCTAssertFalse(GlanceSelection.qualifies(Self.entry(id: "o", type: "oauth_event", status: "error")))
    }

    // MARK: - Helpers

    static func entry(
        id: String,
        type: String,
        server: String? = nil,
        tool: String? = nil,
        status: String = "success",
        requestId: String? = nil
    ) -> ActivityEntry {
        var json: [String: Any] = [
            "id": id,
            "type": type,
            "status": status,
            "timestamp": "2027-01-15T08:00:00Z"
        ]
        if let server { json["server_name"] = server }
        if let tool { json["tool_name"] = tool }
        if let requestId { json["request_id"] = requestId }
        let data = try! JSONSerialization.data(withJSONObject: json)
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(ActivityEntry.self, from: data)
    }

    static func session(id: String, status: String, clientName: String = "Claude Code") -> APIClient.MCPSession {
        let json: [String: Any] = [
            "id": id,
            "status": status,
            "client_name": clientName,
            "tool_call_count": 3
        ]
        let data = try! JSONSerialization.data(withJSONObject: json)
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(APIClient.MCPSession.self, from: data)
    }
}
