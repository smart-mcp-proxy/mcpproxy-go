import XCTest
@testable import MCPProxy

/// Tray Glance — SSE activity adapter.
///
/// The core emits `activity.tool_call.completed` /
/// `activity.internal_tool_call.completed`, never `activity`, and the payload is
/// an envelope `{payload, timestamp}` whose timestamp is Unix SECONDS while
/// `ActivityEntry.timestamp` is an ISO-8601 string. These tests pin the mapping,
/// and assert it against the real consumers (`GlanceSelection`,
/// `GlanceFormatting`) rather than a locally rebuilt copy of their logic.
@MainActor
final class GlanceEventTests: XCTestCase {

    /// Upstream calls carry `server_name` / `tool_name` (event_bus.go
    /// EmitActivityToolCallCompleted, payload literal at :444-454).
    func testUpstreamCompletedPayloadBecomesToolCallEntry() throws {
        let json = """
        {"payload":{"server_name":"github","tool_name":"create_issue",
        "session_id":"sess-1","request_id":"req-1","source":"mcp","status":"success",
        "error_message":"","duration_ms":142},"timestamp":1753800000}
        """
        let entry = try XCTUnwrap(GlanceEvent.adapt(
            eventName: "activity.tool_call.completed",
            data: Data(json.utf8)
        ))

        XCTAssertEqual(entry.type, "tool_call")
        XCTAssertEqual(entry.serverName, "github")
        XCTAssertEqual(entry.toolName, "create_issue")
        XCTAssertEqual(entry.status, "success")
        XCTAssertEqual(entry.durationMs, 142)
        XCTAssertEqual(entry.sessionId, "sess-1")
        XCTAssertEqual(entry.requestId, "req-1")
        XCTAssertNil(entry.errorMessage, "empty error_message must not become a failure detail")
    }

    /// Internal calls carry `internal_tool_name`, and `target_server` only when
    /// non-empty (event_bus.go EmitActivityInternalToolCall, :552-565). Reading
    /// `tool_name` here would produce a row with no tool at all — and the row
    /// must survive the selection rules and the relative-time formatter, not
    /// merely hold the right field values.
    func testInternalCompletedPayloadUsesInternalToolNameAndTargetServer() throws {
        let json = """
        {"payload":{"internal_tool_name":"call_tool_read","target_server":"jira",
        "target_tool":"get_issue","session_id":"sess-2","request_id":"req-2",
        "status":"error","error_message":"auth failed","duration_ms":9},
        "timestamp":1753800000}
        """
        let entry = try XCTUnwrap(GlanceEvent.adapt(
            eventName: "activity.internal_tool_call.completed",
            data: Data(json.utf8)
        ))

        XCTAssertEqual(entry.type, "internal_tool_call")
        XCTAssertEqual(entry.toolName, "call_tool_read")
        XCTAssertEqual(entry.serverName, "jira")
        XCTAssertEqual(entry.status, "error")
        XCTAssertTrue(GlanceSelection.qualifies(entry),
                      "a failed wrapper qualifies under rule 3")
        XCTAssertEqual(GlanceFormatting.relativeTime(
            entry.timestamp,
            now: Date(timeIntervalSince1970: 1_753_800_012)
        ), "12s", "the tray's own parser must accept the timestamp we emit")
    }

    /// `target_server` is omitted for discovery built-ins such as
    /// retrieve_tools, so the row must tolerate its absence.
    func testInternalPayloadWithoutTargetServerHasNilServerName() throws {
        let json = """
        {"payload":{"internal_tool_name":"retrieve_tools","session_id":"sess-3",
        "request_id":"req-3","status":"success","duration_ms":4},
        "timestamp":1753800000}
        """
        let entry = try XCTUnwrap(GlanceEvent.adapt(
            eventName: "activity.internal_tool_call.completed",
            data: Data(json.utf8)
        ))

        XCTAssertEqual(entry.toolName, "retrieve_tools")
        XCTAssertNil(entry.serverName)
    }

    /// The core does not persist started events (activity_service.go), so a row
    /// built from one would never be reconciled by the poll.
    func testStartedEventIsIgnored() {
        let json = """
        {"payload":{"server_name":"github","tool_name":"create_issue",
        "request_id":"req-4"},"timestamp":1753800000}
        """
        XCTAssertNil(GlanceEvent.adapt(
            eventName: "activity.tool_call.started",
            data: Data(json.utf8)
        ))
    }

    /// A failed upstream call emits BOTH events under ONE request id, and
    /// `ActivityEntry` derives identity and equality from `id` alone — so a bare
    /// request id would make the two records collide before rule 4 could pick
    /// the `tool_call` one. The last assertion is the point of the composite id.
    func testPairedEventsUnderOneRequestIdGetDistinctIds() throws {
        let upstream = """
        {"payload":{"server_name":"jira","tool_name":"get_issue",
        "request_id":"req-5","status":"error","error_message":"auth failed"},
        "timestamp":1753800000}
        """
        let wrapper = """
        {"payload":{"internal_tool_name":"call_tool_read","target_server":"jira",
        "request_id":"req-5","status":"error","error_message":"auth failed"},
        "timestamp":1753800000}
        """
        let a = try XCTUnwrap(GlanceEvent.adapt(
            eventName: "activity.tool_call.completed", data: Data(upstream.utf8)))
        let b = try XCTUnwrap(GlanceEvent.adapt(
            eventName: "activity.internal_tool_call.completed", data: Data(wrapper.utf8)))

        XCTAssertEqual(a.id, "req-5:tool_call")
        XCTAssertEqual(b.id, "req-5:internal_tool_call")
        XCTAssertNotEqual(a, b)
        XCTAssertEqual(a.requestId, b.requestId, "the shared request id is what rule 4 collapses on")
        XCTAssertEqual(GlanceSelection.activityRows(from: [a, b]).map(\.id),
                       ["req-5:tool_call"],
                       "rule 4 keeps the record that names the real server:tool")
    }

    /// The payload key is `error_message`, matching `ActivityEntry` — a read
    /// keyed on `error` would render a failed call as if it had no detail.
    func testFailureDetailComesFromErrorMessageKey() throws {
        let json = """
        {"payload":{"server_name":"jira","tool_name":"get_issue","request_id":"req-6",
        "status":"error","error_message":"auth failed: token expired"},
        "timestamp":1753800000}
        """
        let entry = try XCTUnwrap(GlanceEvent.adapt(
            eventName: "activity.tool_call.completed",
            data: Data(json.utf8)
        ))

        XCTAssertEqual(entry.errorMessage, "auth failed: token expired")
    }

    /// The envelope timestamp is Unix seconds; the entry must carry a string the
    /// tray's OWN parser accepts. Rebuilding an ISO8601DateFormatter here would
    /// let the test pass while GlanceFormatting rejected the string.
    func testEnvelopeUnixSecondsBecomeParsableISO8601() throws {
        let json = """
        {"payload":{"server_name":"github","tool_name":"create_issue",
        "request_id":"req-7","status":"success"},"timestamp":1753800000}
        """
        let entry = try XCTUnwrap(GlanceEvent.adapt(
            eventName: "activity.tool_call.completed",
            data: Data(json.utf8)
        ))

        let parsed = try XCTUnwrap(GlanceFormatting.parseTimestamp(entry.timestamp))
        XCTAssertEqual(parsed.timeIntervalSince1970, 1_753_800_000, accuracy: 0.001)
    }
}
