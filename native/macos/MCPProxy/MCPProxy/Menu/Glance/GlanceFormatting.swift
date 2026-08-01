// GlanceFormatting.swift
// MCPProxy
//
// Pure presentation helpers for the tray glance section: status symbol,
// row label, middle truncation, and compact relative age.
// Salvaged from the retired Menu/TrayMenu.swift.

import Foundation

/// Pure, AppKit-free formatting helpers for glance rows.
enum GlanceFormatting {

    // MARK: - Status symbol

    /// SF Symbol name for an outcome.
    ///
    /// Shape carries the meaning (never colour alone), so a checkmark, a
    /// cross and an exclamation mark are three distinct glyphs.
    ///
    /// Keyed on the status *string* rather than a record, because a glance row
    /// is a run of records and the outcome it shows is the run's worst
    /// (`GlanceRun.worstStatus`), which may belong to none of the run's other
    /// records.
    /// A block gets a triangle, not a differently-coloured circle: a policy
    /// block and an upstream failure are different events, and the difference
    /// has to survive greyscale and a red-green deficiency (spec 090 FR-011).
    static func statusSymbolName(forStatus status: String) -> String {
        switch status {
        case "success":
            return "checkmark.circle"
        case "error":
            return "xmark.circle"
        case _ where ActivityEntry.blockingDecisions.contains(status):
            return "exclamationmark.triangle"
        default:
            return "exclamationmark.circle"
        }
    }

    /// Convenience for a single record.
    static func statusSymbolName(for entry: ActivityEntry) -> String {
        statusSymbolName(forStatus: entry.status)
    }

    // MARK: - Row label

    /// Record types whose `server_name` names an upstream server, so the label
    /// composes `server:tool`. An `internal_tool_call` is deliberately absent:
    /// its `server_name` is the server it was dispatched AT, not what ran.
    private static let upstreamNamedTypes: Set<String> = [
        "tool_call", ActivityEntry.policyDecisionType
    ]

    /// Compose the row's primary label.
    ///
    /// Upstream calls read `server:tool`; discovery/execution built-ins read
    /// just the built-in's name because they have no upstream server.
    ///
    /// A blocked policy decision names the upstream call it stopped, so it reads
    /// `server:tool` too (spec 090 US3) — a blocked row that named only the tool
    /// would leave out which server the user was being protected from.
    static func rowLabel(for entry: ActivityEntry) -> String {
        let tool = entry.toolName ?? ""
        let server = entry.serverName ?? ""

        if upstreamNamedTypes.contains(entry.type), !server.isEmpty, !tool.isEmpty {
            // Guard against a tool name that already carries the prefix.
            if tool.hasPrefix("\(server):") {
                return tool
            }
            return "\(server):\(tool)"
        }
        if !tool.isEmpty {
            return tool
        }
        if !server.isEmpty {
            return server
        }
        return entry.type
    }

    // MARK: - Truncation

    /// Middle-truncate `text` to at most `limit` characters, keeping the head
    /// (the server prefix) and the tail (the end of the tool name).
    static func middleTruncated(_ text: String, limit: Int) -> String {
        guard limit > 0 else { return "" }
        guard text.count > limit else { return text }
        let keep = limit - 1                 // one slot for the ellipsis
        let head = keep / 2 + keep % 2
        let tail = keep - head
        let prefix = String(text.prefix(head))
        let suffix = tail > 0 ? String(text.suffix(tail)) : ""
        return prefix + "\u{2026}" + suffix
    }

    // MARK: - Budgets

    /// Character budget for a row's reason subtitle (spec 090 FR-006).
    ///
    /// Independent of the label's budget on purpose: the two lines are truncated
    /// separately, so a long `server:tool` never shortens the explanation and a
    /// long explanation never shortens the name of what ran.
    static let reasonBudget = 60

    /// Character budget for the error clause on a failed row's title line
    /// (FR-011a). Deliberately smaller than the reason's: it shares the title
    /// with the label and the age, and the full message is in the tooltip.
    static let errorClauseBudget = 40

    /// Tail-truncate `text` to at most `limit` characters, keeping the head.
    ///
    /// The opposite end from `middleTruncated`, and for a reason: a `server:tool`
    /// label carries information at both ends, while a sentence — a caller's
    /// intent, an error message — carries it at the front and trails off into
    /// detail.
    static func tailTruncated(_ text: String, limit: Int) -> String {
        guard limit > 0 else { return "" }
        guard text.count > limit else { return text }
        return String(text.prefix(limit - 1)) + "\u{2026}"
    }

    // MARK: - Time

    private static let fractionalParser: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }()

    private static let plainParser: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        return f
    }()

    /// Parse an RFC3339/ISO-8601 timestamp with or without fractional seconds.
    static func parseTimestamp(_ timestamp: String) -> Date? {
        if let date = fractionalParser.date(from: timestamp) { return date }
        return plainParser.date(from: timestamp)
    }

    /// Compact, locale-independent age string: `12s`, `3m`, `5h`, `2d`.
    static func compactAge(_ seconds: TimeInterval) -> String {
        let total = max(0, Int(seconds.rounded()))
        if total < 60 { return "\(total)s" }
        let minutes = total / 60
        if minutes < 60 { return "\(minutes)m" }
        let hours = minutes / 60
        if hours < 24 { return "\(hours)h" }
        return "\(hours / 24)d"
    }

    /// Relative age of an activity timestamp, falling back to the raw string
    /// when it cannot be parsed.
    static func relativeTime(_ timestamp: String, now: Date = Date()) -> String {
        guard let date = parseTimestamp(timestamp) else { return timestamp }
        return compactAge(now.timeIntervalSince(date))
    }
}
