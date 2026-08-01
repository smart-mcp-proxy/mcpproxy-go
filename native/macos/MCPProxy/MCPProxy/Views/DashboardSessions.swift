// DashboardSessions.swift
// MCPProxy
//
// The Recent Sessions list of the dashboard: which client rows to show, in what
// order, and how old each one reads. Pure functions over APIClient.MCPSession,
// extracted from DashboardView for the same reason GlancePresence lives apart
// from the menu — a dedupe, a three-key sort and an age string are exactly the
// kind of policy that is invisible in a screenshot and needs pinning by test.

import Foundation

enum DashboardSessions {

    /// One row per client: the most representative session for each, most
    /// recently active first.
    ///
    /// Ties break by tool-call count and then by session id, because
    /// `Dictionary.values` iterates in a random order and an unstable sort would
    /// reshuffle the table under the cursor on a poll that changed nothing.
    static func rows(from sessions: [APIClient.MCPSession]) -> [APIClient.MCPSession] {
        var byClient: [String: APIClient.MCPSession] = [:]

        for session in sessions {
            let key = session.clientName ?? "unknown"
            guard let existing = byClient[key] else {
                byClient[key] = session
                continue
            }

            let existingCalls = existing.toolCallCount ?? 0
            let newCalls = session.toolCallCount ?? 0

            // Prefer active sessions, then most tool calls, then most recent.
            if session.status == "active" && existing.status != "active" {
                byClient[key] = session
            } else if newCalls > existingCalls {
                byClient[key] = session
            } else if newCalls == existingCalls,
                      activityDate(of: session) > activityDate(of: existing) {
                byClient[key] = session
            }
        }

        return byClient.values.sorted { lhs, rhs in
            let lTime = activityDate(of: lhs)
            let rTime = activityDate(of: rhs)
            if lTime != rTime { return lTime > rTime }

            let lCalls = lhs.toolCallCount ?? 0
            let rCalls = rhs.toolCallCount ?? 0
            if lCalls != rCalls { return lCalls > rCalls }

            return lhs.id < rhs.id
        }
    }

    /// How long ago the session was last heard from, or "-" when it never
    /// recorded a usable timestamp.
    static func relativeTime(for session: APIClient.MCPSession, now: Date = Date()) -> String {
        guard let stamp = GlancePresence.lastActivity(of: session) else { return "-" }

        let interval = now.timeIntervalSince(stamp)
        if interval < 60 { return "just now" }
        if interval < 3600 { return "\(Int(interval / 60))m ago" }
        if interval < 86400 { return "\(Int(interval / 3600))h ago" }
        return "\(Int(interval / 86400))d ago"
    }

    /// Sort key: the session's last activity, with sessions that have no usable
    /// timestamp sorting last rather than being dropped from the table.
    private static func activityDate(of session: APIClient.MCPSession) -> Date {
        GlancePresence.lastActivity(of: session) ?? .distantPast
    }
}
