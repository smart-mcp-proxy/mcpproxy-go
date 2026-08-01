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

    /// SSE event name for a policy decision (`internal/runtime/events.go`).
    ///
    /// The only notice a blocked call ever produces: it never dispatches, so no
    /// completion event follows it. Without this the glance could not show a
    /// block at all until the next poll — and a block is precisely the outcome
    /// the user has no other way to notice (spec 090 US3).
    static let policyDecision = "activity.policy_decision"

    /// Build an `ActivityEntry` from an SSE envelope.
    ///
    /// Returns nil for any other event name (notably
    /// `activity.tool_call.started`) and for a payload that does not parse.
    static func adapt(eventName: String, data: Data) -> ActivityEntry? {
        let type: String
        switch eventName {
        case upstreamCompleted: type = "tool_call"
        case internalCompleted: type = "internal_tool_call"
        case policyDecision: type = ActivityEntry.policyDecisionType
        default: return nil
        }

        guard let envelope = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let payload = envelope["payload"] as? [String: Any] else { return nil }

        let toolName: String?
        let serverName: String?
        if type == "internal_tool_call" {
            toolName = nonEmptyString(payload["internal_tool_name"])
            serverName = nonEmptyString(payload["target_server"])
        } else {
            // Upstream calls and policy decisions name the same fields
            // (event_bus.go `EmitActivityPolicyDecision`).
            toolName = nonEmptyString(payload["tool_name"])
            serverName = nonEmptyString(payload["server_name"])
        }

        // The persisted record's status IS the decision
        // (`activity_service.go handlePolicyDecision`), so a live row and the
        // polled one it reconciles with must agree on that, not on a
        // convenience value: "warn" has to stay distinguishable from "blocked",
        // or a warning would take one of the five rows.
        let status: String
        if type == ActivityEntry.policyDecisionType {
            status = nonEmptyString(payload["decision"]) ?? "blocked"
        } else {
            status = nonEmptyString(payload["status"]) ?? "success"
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
            status: status,
            errorMessage: nonEmptyString(payload["error_message"]),
            durationMs: (payload["duration_ms"] as? NSNumber)?.int64Value,
            timestamp: timestamp,
            sessionId: nonEmptyString(payload["session_id"]),
            requestId: requestId,
            metadata: contextMetadata(from: payload),
            hasSensitiveData: nil,
            detectionTypes: nil,
            maxSeverity: nil
        )
    }

    /// The contextual metadata a live row carries, and nothing else.
    ///
    /// It is deliberately the SAME whitelist the polled projection keeps
    /// (`exclude_payloads=true`, contracts/api-deltas.md §1): a row must read
    /// identically before and after the 30-second reconcile, so a reason that
    /// only the poll could produce would blink into existence half a minute
    /// late — and one only the event could produce would blink out again.
    ///
    /// Copying the whole payload instead would be worse than redundant: it
    /// carries `arguments` and `response`, the two fields the projection exists
    /// to strip, into the backing model of a menu row that renders neither.
    private static func contextMetadata(from payload: [String: Any]) -> [String: JSONValue]? {
        var metadata: [String: JSONValue] = [:]

        if let intent = payload["intent"] as? [String: Any] {
            var kept: [String: JSONValue] = [:]
            if let reason = nonEmptyString(intent["reason"]) { kept["reason"] = .string(reason) }
            if let operation = nonEmptyString(intent["operation_type"]) {
                kept["operation_type"] = .string(operation)
            }
            // An intent map that carried nothing we render is no context at
            // all: `ActivityEntry.intent` would otherwise answer an empty
            // object, which reads as "there is context here" downstream.
            if !kept.isEmpty { metadata["intent"] = .object(kept) }
        }

        // Read unconditionally rather than only for policy events: these two
        // keys exist on no other activity payload, and a branch here would be a
        // second place for "which event carries what" to drift from the switch
        // above. `handlePolicyDecision` persists them in exactly this shape.
        if let decision = nonEmptyString(payload["decision"]) {
            metadata["decision"] = .string(decision)
        }
        if let reason = nonEmptyString(payload["reason"]) {
            metadata["reason"] = .string(reason)
        }

        return metadata.isEmpty ? nil : metadata
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
