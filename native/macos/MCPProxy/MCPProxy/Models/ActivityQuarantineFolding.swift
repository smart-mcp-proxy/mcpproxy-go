// ActivityQuarantineFolding.swift
// MCPProxy
//
// Spec 109-k (activity-scope-filters), acceptance scenario 6: "Given /activity
// on a fresh instance after approving a 14-tool server, Then ... Switching to
// 'System events' shows one folded row 'filesystem: 14 tools approved' that
// expands..." (macOS has no run-expansion UI, unlike the Web table's F5
// folding — this folds the batch into one summary row, still real
// information instead of 14 near-identical lines).
//
// A tool-level quarantine change is emitted once PER TOOL in a server's
// baseline (`internal/runtime/activity_service.go`'s
// `handleToolQuarantineChange`, one `ActivityRecord` per tool): 14 tools
// approved on connect is 14 records that agree on everything except which
// tool. Folding on `tool_quarantine_change` alone still fires (the "System
// events" view's own type filter is what narrows to it), regardless of
// server/status matching — the batch is defined by the caller's own type
// filter already having selected only quarantine-change rows for the same run.

import Foundation

enum ActivityQuarantineFolding {
    /// Folds consecutive `tool_quarantine_change` records that agree on
    /// server and status (the two fields the summary line reports) into one
    /// synthetic entry. Pure and order-preserving — everything else in
    /// `entries` passes through untouched.
    static func fold(_ entries: [ActivityEntry]) -> [ActivityEntry] {
        var result: [ActivityEntry] = []
        var index = 0
        while index < entries.count {
            let entry = entries[index]
            guard entry.type == "tool_quarantine_change" else {
                result.append(entry)
                index += 1
                continue
            }

            var end = index + 1
            while end < entries.count,
                  entries[end].type == "tool_quarantine_change",
                  entries[end].serverName == entry.serverName,
                  entries[end].status == entry.status {
                end += 1
            }

            let count = end - index
            result.append(count > 1 ? batchSummary(entry, count: count) : entry)
            index = end
        }
        return result
    }

    /// "filesystem: 14 tools approved" — the lead record's identity (id,
    /// timestamp, server, status) stands for the run; `toolName` becomes the
    /// batch summary rather than any one member's tool.
    private static func batchSummary(_ lead: ActivityEntry, count: Int) -> ActivityEntry {
        let serverPrefix = lead.serverName.map { "\($0): " } ?? ""
        let verb = lead.status.isEmpty ? "changed" : lead.status
        return ActivityEntry(
            id: lead.id,
            type: lead.type,
            source: lead.source,
            serverName: lead.serverName,
            toolName: "\(serverPrefix)\(count) tools \(verb)",
            arguments: nil,
            response: nil,
            responseTruncated: nil,
            status: lead.status,
            errorMessage: nil,
            durationMs: nil,
            timestamp: lead.timestamp,
            sessionId: nil,
            requestId: nil,
            parentId: nil,
            metadata: nil,
            hasSensitiveData: false,
            detectionTypes: nil,
            maxSeverity: nil
        )
    }
}
