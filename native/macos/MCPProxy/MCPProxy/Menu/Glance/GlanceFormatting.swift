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

    /// SF Symbol name for an activity record's outcome.
    ///
    /// Shape carries the meaning (never colour alone), so a checkmark, a
    /// cross and an exclamation mark are three distinct glyphs.
    static func statusSymbolName(for entry: ActivityEntry) -> String {
        switch entry.status {
        case "success":
            return "checkmark.circle"
        case "error":
            return "xmark.circle"
        default:
            return "exclamationmark.circle"
        }
    }

    // MARK: - Row label

    /// Compose the row's primary label.
    ///
    /// Upstream calls read `server:tool`; discovery/execution built-ins read
    /// just the built-in's name because they have no upstream server.
    static func rowLabel(for entry: ActivityEntry) -> String {
        let tool = entry.toolName ?? ""
        let server = entry.serverName ?? ""

        if entry.type == "tool_call", !server.isEmpty, !tool.isEmpty {
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
