// GlanceEvent.swift
// MCPProxy
//
// Adapts `activity.*.completed` SSE payloads into `ActivityEntry` values so the
// tray glance section can render live rows without issuing a REST request per
// event.

import Foundation

/// Maps runtime activity SSE payloads onto `ActivityEntry`.
enum GlanceEvent {
    /// SSE event name for a completed upstream tool call.
    static let upstreamCompleted = "activity.tool_call.completed"

    /// SSE event name for a completed internal (built-in) tool call.
    static let internalCompleted = "activity.internal_tool_call.completed"

    /// Build an `ActivityEntry` from an SSE envelope.
    ///
    /// Returns nil for any other event name (notably
    /// `activity.tool_call.started`) and for a payload that does not parse.
    static func adapt(eventName: String, data: Data) -> ActivityEntry? {
        let type: String
        switch eventName {
        case upstreamCompleted: type = "tool_call"
        case internalCompleted: type = "internal_tool_call"
        default: return nil
        }

        guard let envelope = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let payload = envelope["payload"] as? [String: Any] else { return nil }

        let toolName: String?
        let serverName: String?
        if type == "tool_call" {
            toolName = nonEmptyString(payload["tool_name"])
            serverName = nonEmptyString(payload["server_name"])
        } else {
            toolName = nonEmptyString(payload["internal_tool_name"])
            serverName = nonEmptyString(payload["target_server"])
        }

        let requestId = nonEmptyString(payload["request_id"])
        // Composite, not a bare request id: one failing call emits BOTH events
        // under a single request id, and `ActivityEntry` derives identity from
        // `id` alone, so the two records would collide before
        // `GlanceSelection.collapseByRequestID` could choose between them.
        let provisionalId = requestId.map { "\($0):\(type)" } ?? "sse-\(UUID().uuidString):\(type)"

        let seconds = (envelope["timestamp"] as? NSNumber)?.doubleValue
            ?? Date().timeIntervalSince1970
        let timestamp = isoFormatter.string(from: Date(timeIntervalSince1970: seconds))

        return ActivityEntry(
            id: provisionalId,
            type: type,
            source: nonEmptyString(payload["source"]),
            serverName: serverName,
            toolName: toolName,
            arguments: nil,
            response: nil,
            responseTruncated: nil,
            status: nonEmptyString(payload["status"]) ?? "success",
            errorMessage: nonEmptyString(payload["error_message"]),
            durationMs: (payload["duration_ms"] as? NSNumber)?.int64Value,
            timestamp: timestamp,
            sessionId: nonEmptyString(payload["session_id"]),
            requestId: requestId,
            metadata: nil,
            hasSensitiveData: nil,
            detectionTypes: nil,
            maxSeverity: nil
        )
    }

    private static func nonEmptyString(_ value: Any?) -> String? {
        guard let text = value as? String, !text.isEmpty else { return nil }
        return text
    }

    /// Emits fractional seconds, which `GlanceFormatting.parseTimestamp`
    /// accepts on its first attempt (it falls back to a non-fractional parser
    /// for polled records).
    private static let isoFormatter: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        formatter.timeZone = TimeZone(secondsFromGMT: 0)
        return formatter
    }()
}
