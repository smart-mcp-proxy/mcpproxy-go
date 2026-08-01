import XCTest
@testable import MCPProxy

/// Shared fixtures for the tray-glance menu tests.
/// `GlanceSectionTests` predates this file and keeps its own copies.
enum GlanceFixtures {

    /// Fixed clock so relative ages are deterministic.
    static let now = GlanceFormatting.parseTimestamp("2027-01-15T08:00:00Z")!

    /// A connected core with two qualifying calls and one active client.
    /// The second call is deliberately unattributed (no `session_id`).
    static func connectedState() -> AppState {
        let state = AppState()
        // coreState first: its didSet clears the glance feeds on any non-connected state.
        state.coreState = .connected
        state.callsThisHour = 12
        state.glanceActivity = [
            entry(id: "a", server: "github", tool: "create_issue",
                  timestamp: "2027-01-15T07:59:30Z", session: "sess-a"),
            entry(id: "b", server: "jira", tool: "get_issue",
                  timestamp: "2027-01-15T07:58:00Z", session: nil)
        ]
        state.glanceSessions = [
            session(id: "sess-a", name: "Claude Code", calls: 8,
                    lastActivity: "2027-01-15T07:59:00Z")
        ]
        return state
    }

    static func entry(
        id: String,
        server: String,
        tool: String,
        timestamp: String,
        session: String?,
        reason: String? = nil
    ) -> ActivityEntry {
        var json: [String: Any] = [
            "id": id,
            "type": "tool_call",
            "status": "success",
            "timestamp": timestamp,
            "request_id": "req-\(id)",
            "server_name": server,
            "tool_name": tool
        ]
        if let session { json["session_id"] = session }
        // Wire shape: a call's reason rides under `metadata.intent`, which is
        // what the projection whitelist keeps and the SSE adapter writes.
        if let reason { json["metadata"] = ["intent": ["reason": reason]] }
        let data = try! JSONSerialization.data(withJSONObject: json)
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(ActivityEntry.self, from: data)
    }

    static func session(
        id: String,
        name: String,
        calls: Int,
        lastActivity: String
    ) -> APIClient.MCPSession {
        let json: [String: Any] = [
            "id": id,
            "status": "active",
            "client_name": name,
            "tool_call_count": calls,
            "last_activity": lastActivity
        ]
        let data = try! JSONSerialization.data(withJSONObject: json)
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(APIClient.MCPSession.self, from: data)
    }
}
