// GlanceSelection.swift
// MCPProxy
//
// Display rules for the tray glance section: which activity records qualify
// as rows, how duplicates collapse, and which sessions count as clients.
// Pure functions over ActivityEntry / APIClient.MCPSession — no AppKit.

import Foundation

/// Presentation policy for the glance section. Pure and synchronous.
enum GlanceSelection {

    /// Proxy administration built-ins. Never shown, whatever their status.
    static let managementBuiltIns: Set<String> = ["upstream_servers", "quarantine_security"]

    /// Discovery/execution built-ins that are worth a row even on success.
    static let glanceInternalTools: Set<String> = ["retrieve_tools", "code_execution", "describe_tool"]

    /// How many rows each list shows.
    static let rowLimit = 5

    // MARK: - Rules 1-3

    /// Whether a single record qualifies for a glance row.
    static func qualifies(_ entry: ActivityEntry) -> Bool {
        let tool = entry.toolName ?? ""

        // Rule 1 — management built-ins are excluded, whatever the status.
        if managementBuiltIns.contains(tool) { return false }

        // Rule 2 — every real upstream call.
        if entry.type == "tool_call" { return true }

        // Rule 3 — discovery/execution built-ins, plus any internal failure
        // (a wrapper that died before dispatch has no upstream record).
        if entry.type == "internal_tool_call" {
            return glanceInternalTools.contains(tool) || entry.status != "success"
        }

        return false
    }

    // MARK: - Rule 4

    /// Collapse records sharing a `request_id`, keeping the `tool_call` one.
    ///
    /// The surviving record is emitted at the position of the first record of
    /// its group so recency ordering is preserved. Records with no request id
    /// are never collapsed. When a group holds several `tool_call` records the
    /// first one encountered wins — later ones are dropped, not merged.
    ///
    /// Why this is safe today: the core mints a request id per dispatch
    /// (`internal/server/mcp.go`, both `requestID := fmt.Sprintf("%d-%s-%s", …)`
    /// sites under "Generate requestID for activity tracking"), so two distinct
    /// upstream calls carry distinct ids structurally. A group is therefore a
    /// wrapper plus its upstream partner, never a fan-out.
    ///
    /// What would invalidate that: `internal/server/mcp_code_execution.go` sets
    /// `RequestID: u.executionID` ("Use execution ID as request ID to link
    /// nested calls") on every nested call a `code_execution` script makes, so
    /// all of them share ONE id. Those records are currently invisible here —
    /// the nested caller only writes to the legacy history via
    /// `storage.RecordToolCall` and emits no activity event. If nested calls
    /// are ever wired into the activity stream, this function would collapse an
    /// entire multi-tool script down to a single row. At that point rule 4 must
    /// narrow to "collapse a wrapper with its upstream partner" rather than
    /// deduplicating a whole request id.
    static func collapseByRequestID(_ entries: [ActivityEntry]) -> [ActivityEntry] {
        var winners: [String: ActivityEntry] = [:]
        for entry in entries {
            guard let rid = entry.requestId, !rid.isEmpty else { continue }
            guard let existing = winners[rid] else {
                winners[rid] = entry
                continue
            }
            if existing.type != "tool_call" && entry.type == "tool_call" {
                winners[rid] = entry
            }
        }

        var emitted = Set<String>()
        var result: [ActivityEntry] = []
        for entry in entries {
            guard let rid = entry.requestId, !rid.isEmpty else {
                result.append(entry)
                continue
            }
            if emitted.contains(rid) { continue }
            emitted.insert(rid)
            result.append(winners[rid] ?? entry)
        }
        return result
    }

    // MARK: - Public entry points

    /// Rules 1-4 applied in order, then the first `limit` survivors.
    static func activityRows(from entries: [ActivityEntry], limit: Int = rowLimit) -> [ActivityEntry] {
        let qualified = entries.filter(qualifies)
        return Array(collapseByRequestID(qualified).prefix(limit))
    }

    /// Sessions currently connected, capped at `limit`, input order preserved.
    static func activeClients(
        from sessions: [APIClient.MCPSession],
        limit: Int = rowLimit
    ) -> [APIClient.MCPSession] {
        Array(sessions.filter { $0.status == "active" }.prefix(limit))
    }
}
