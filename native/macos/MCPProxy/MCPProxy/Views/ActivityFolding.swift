// ActivityFolding.swift
// MCPProxy
//
// Spec 109-k FR-071 (parity row 20): in System events, consecutive records of
// the same type and server within 60 s fold into one summary row
// ("filesystem: 14 tools approved") that expands in place. Presentation only:
// the export request is built from the filter, never from these rows.

import Foundation

/// One displayed row: a single record, or a folded run of them.
struct ActivityFoldRow: Identifiable, Equatable {
    /// Newest first, as the API returned them; `members[0]` is the lead.
    let members: [ActivityEntry]

    /// Keyed on the OLDEST member: live events join a run at the front, so a
    /// lead-keyed id would change and re-collapse an expanded run.
    var id: String { members[members.count - 1].id }
    var lead: ActivityEntry { members[0] }
    var count: Int { members.count }

    /// The collapsed line for a run; nil for a single record, which is shown
    /// as itself.
    var summary: String? {
        guard count > 1 else { return nil }
        let prefix = lead.serverName.map { "\($0): " } ?? ""
        if lead.type == "tool_quarantine_change" {
            // One record per tool in a server's baseline: report the batch.
            // `status` is the action the core stamps ("approved", "changed").
            return "\(prefix)\(count) tools \(lead.status)"
        }
        return "\(prefix)\(count) \(lead.type.replacingOccurrences(of: "_", with: " ")) events"
    }
}

enum ActivityFolding {
    /// Fold window between neighbouring members of a run.
    static let window: TimeInterval = 60

    /// Order-preserving fold of `entries` (newest first). `enabled: false`
    /// returns one row per record.
    static func fold(_ entries: [ActivityEntry], enabled: Bool = true) -> [ActivityFoldRow] {
        guard enabled else { return entries.map { ActivityFoldRow(members: [$0]) } }
        var runs: [[ActivityEntry]] = []
        for entry in entries {
            if let last = runs.last?.last, belongTogether(last, entry) {
                runs[runs.count - 1].append(entry)
            } else {
                runs.append([entry])
            }
        }
        return runs.map { ActivityFoldRow(members: $0) }
    }

    private static func belongTogether(_ a: ActivityEntry, _ b: ActivityEntry) -> Bool {
        guard a.type == b.type, a.serverName == b.serverName else { return false }
        // A batch summary prints one verb, so every member must share it.
        if a.type == "tool_quarantine_change", a.status != b.status { return false }
        guard let ta = date(a.timestamp), let tb = date(b.timestamp) else { return false }
        return abs(ta.timeIntervalSince(tb)) <= window
    }

    private static func date(_ timestamp: String) -> Date? {
        let fractional = ISO8601DateFormatter()
        fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let d = fractional.date(from: timestamp) { return d }
        return ISO8601DateFormatter().date(from: timestamp)
    }
}
