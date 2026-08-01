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

    // MARK: - Rule 6: policy blocks (spec 090 FR-012)

    /// A block is the proxy doing its job, and it has no other trace: the call
    /// never dispatched, so there is no `tool_call` record to stand in for it.
    func testABlockedPolicyDecisionQualifies() {
        let entry = Self.entry(id: "p1", type: "policy_decision", server: "jira", tool: "delete_issue",
                               status: "blocked", decision: "blocked",
                               reason: "Destructive operation blocked")
        XCTAssertTrue(GlanceSelection.qualifies(entry))
        XCTAssertEqual(entry.reason, "Destructive operation blocked")
    }

    /// "blocked" is what the runtime emits today; `event_bus.go` still treats
    /// the older "block" as a block, and records carrying it are already on
    /// disk.
    func testTheLegacyBlockDecisionAlsoQualifies() {
        XCTAssertTrue(GlanceSelection.qualifies(
            Self.entry(id: "p2", type: "policy_decision", server: "jira", tool: "get_issue",
                       status: "block", decision: "block", reason: "Quarantined server")))
    }

    /// A record whose metadata was dropped (a projection, an old record) still
    /// qualifies: the persisted status IS the decision.
    func testABlockedRecordWithoutDecisionMetadataQualifiesOnItsStatus() {
        XCTAssertTrue(GlanceSelection.qualifies(
            Self.entry(id: "p3", type: "policy_decision", server: "jira", tool: "get_issue",
                       status: "blocked")))
    }

    /// Warnings and redactions let the call through — the call's own record is
    /// the row. Admitting them would spend the five rows on decisions that
    /// changed nothing (FR-012).
    func testWarningsAndRedactionsDoNotQualify() {
        for decision in ["warn", "redacted", "allow"] {
            let entry = Self.entry(id: decision, type: "policy_decision", server: "jira",
                                   tool: "get_issue", status: decision, decision: decision,
                                   reason: "\(decision) decision")
            XCTAssertFalse(GlanceSelection.qualifies(entry), "\(decision) must not take a row")
        }
    }

    /// Rule 1 still beats it: proxy administration is never glance material,
    /// whatever a policy decided about it.
    func testABlockedManagementBuiltInIsStillExcluded() {
        XCTAssertFalse(GlanceSelection.qualifies(
            Self.entry(id: "p4", type: "policy_decision", tool: "quarantine_security",
                       status: "blocked", decision: "blocked", reason: "nope")))
    }

    /// US3 scenario 5: "27 blocked attempts at a tool" and "27 calls to it" are
    /// different stories, so blocked records never join a run of calls — even
    /// when they are adjacent and name the same tool.
    func testBlockedRecordsNeverMergeWithCallsToTheSameTool() {
        let rows = GlanceSelection.activityRows(from: [
            Self.entry(id: "b1", type: "policy_decision", server: "jira", tool: "get_issue",
                       status: "blocked", decision: "blocked", reason: "Quarantined server"),
            Self.entry(id: "b2", type: "policy_decision", server: "jira", tool: "get_issue",
                       status: "blocked", decision: "blocked", reason: "Quarantined server"),
            Self.entry(id: "c1", type: "tool_call", server: "jira", tool: "get_issue"),
            Self.entry(id: "c2", type: "tool_call", server: "jira", tool: "get_issue")
        ])

        XCTAssertEqual(rows.count, 2)
        XCTAssertEqual(rows.map(\.count), [2, 2])
        XCTAssertEqual(rows.map(\.key.outcomeClass), [.blocked, .call])
        XCTAssertEqual(rows.first?.displayReason, "Quarantined server")
    }

    // MARK: - Helpers

    static func entry(
        id: String,
        type: String,
        server: String? = nil,
        tool: String? = nil,
        status: String = "success",
        requestId: String? = nil,
        decision: String? = nil,
        reason: String? = nil
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
        // A policy decision's own reason sits at the top of metadata, beside the
        // decision — the shape `activity_service.go` persists and the projection
        // whitelist keeps.
        var metadata: [String: Any] = [:]
        if let decision { metadata["decision"] = decision }
        if let reason { metadata["reason"] = reason }
        if !metadata.isEmpty { json["metadata"] = metadata }
        let data = try! JSONSerialization.data(withJSONObject: json)
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(ActivityEntry.self, from: data)
    }

}
