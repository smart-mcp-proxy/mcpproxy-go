// GlancePresence.swift
// MCPProxy
//
// Who has been using the proxy, and how recently — the model behind the tray
// glance "Clients" section. Pure functions over APIClient.MCPSession, no AppKit,
// for the same reason GlanceSelection is: presentation policy this fiddly
// (three boundaries, a dedupe, a cap and a count) has to be testable without a
// menu.
//
// Why presence at all, rather than "connected clients": MCP over HTTP is
// stateless. A session is a server-side record that closes after 30 minutes of
// silence and is only persisted once real work happens, so "currently
// connected" is not a thing the transport can answer. Listing only `active`
// sessions therefore left the section empty most of the day and told the user
// "nothing is connected" about a proxy three clients had used that morning.
// Recency is the honest question, and it has an honest answer.

import Foundation

/// How recently a client used the proxy.
///
/// Three states, not a boolean: "used it a minute ago", "quiet but recent" and
/// "was here earlier today" are different enough to act on, and collapsing them
/// is what made the old section misleading.
enum ClientPresence: String, Equatable {
    /// Last activity under 5 minutes ago — working right now.
    case active
    /// 5 to 30 minutes, inclusive at both ends. The upper bound is the session
    /// inactivity timeout: past it the session is gone server-side, so "idle"
    /// would start describing a client that has nothing open at all.
    case idle
    /// Over 30 minutes and within the 24-hour lookback.
    case seen
}

/// One client in the Clients section: a deduplicated view of everything one
/// application has been doing, derived from its most recent session.
struct ClientPresenceRow: Equatable, Identifiable {

    /// The session the row was derived from — the client's most recent. Kept
    /// whole rather than projected: the row renders its call count and its id is
    /// the click payload that opens the Web UI filtered to it.
    let session: APIClient.MCPSession

    /// Display name, never empty (`GlancePresence.unknownClientName` stands in).
    let name: String

    /// Client version, when it reported one. Part of the dedupe key: two
    /// versions of one client are two things running.
    let version: String?

    /// When the client was last heard from — `last_activity`, or the session's
    /// start time when it never recorded any.
    let lastActivity: Date

    /// Seconds since `lastActivity`, clamped at zero. Clock skew between the
    /// core and the tray can put a timestamp in the future, and "-3s ago" is
    /// worse than useless.
    let age: TimeInterval

    let state: ClientPresence

    var id: String { session.id }
}

/// Presence policy for the tray glance Clients section. Pure and synchronous.
enum GlancePresence {

    // MARK: - Policy

    /// Below this age a client counts as working right now.
    static let activeWindow: TimeInterval = 5 * 60

    /// Up to and including this age a client is merely quiet. Matches the
    /// server-side session inactivity timeout.
    static let idleWindow: TimeInterval = 30 * 60

    /// How far back the section looks at all. Beyond it a client is not shown:
    /// "yesterday" is history, and this section is about today.
    static let lookback: TimeInterval = 24 * 60 * 60

    /// How many client rows the section shows.
    static let rowLimit = 5

    /// Stand-in for a client that never sent a name in its initialize request.
    static let unknownClientName = "Unknown client"

    /// Classify an age, or nil when it falls outside the lookback.
    ///
    /// Boundaries are inclusive-idle (5:00 and 30:00 are both idle) so the two
    /// thresholds read as "the first five minutes" and "the first half hour"
    /// rather than leaving two one-second holes nobody would ever notice was
    /// misclassified.
    static func state(forAge age: TimeInterval) -> ClientPresence? {
        if age < activeWindow { return .active }
        if age <= idleWindow { return .idle }
        if age <= lookback { return .seen }
        return nil
    }

    // MARK: - Rows

    /// The section's rows: every retained session inside the lookback,
    /// deduplicated per client, newest activity first, capped at `limit`.
    ///
    /// `status` is deliberately not consulted (FR-017). A closed session is the
    /// normal case this section exists for — see the file header.
    ///
    /// Pass `limit: Int.max` for the full deduped set, which is what the summary
    /// counts over: a cap is a display limit, not a fact about the world.
    static func clients(
        from sessions: [APIClient.MCPSession],
        now: Date,
        limit: Int = rowLimit
    ) -> [ClientPresenceRow] {
        var newestPerClient: [String: ClientPresenceRow] = [:]

        for session in sessions {
            guard let stamp = lastActivity(of: session) else { continue }
            let age = max(0, now.timeIntervalSince(stamp))
            guard let state = state(forAge: age) else { continue }

            let name = displayName(of: session)
            let version = session.clientVersion.flatMap { $0.isEmpty ? nil : $0 }
            let row = ClientPresenceRow(session: session, name: name, version: version,
                                        lastActivity: stamp, age: age, state: state)

            // Name + version, so two versions of one client stay two rows while
            // one client's reconnections collapse into its most recent.
            let key = "\(name)\u{0000}\(version ?? "")"
            if let existing = newestPerClient[key], existing.lastActivity >= stamp { continue }
            newestPerClient[key] = row
        }

        let ordered = newestPerClient.values.sorted { left, right in
            // Ties broken by session id so the order is total and stable — an
            // unstable order would reshuffle rows under the cursor on a poll
            // that changed nothing.
            if left.lastActivity != right.lastActivity {
                return left.lastActivity > right.lastActivity
            }
            return left.session.id < right.session.id
        }
        return Array(ordered.prefix(limit))
    }

    /// The client's display name, never empty.
    static func displayName(of session: APIClient.MCPSession) -> String {
        guard let name = session.clientName, !name.isEmpty else { return unknownClientName }
        return name
    }

    /// When the client was last heard from: `last_activity`, falling back to the
    /// session's start time. A session with neither parseable is excluded —
    /// there is no honest age to show for it, and guessing one would put an
    /// invented row in a section whose whole purpose is to be trusted.
    private static func lastActivity(of session: APIClient.MCPSession) -> Date? {
        if let stamp = session.lastActivity, let date = GlanceFormatting.parseTimestamp(stamp) {
            return date
        }
        if let start = session.startTime, let date = GlanceFormatting.parseTimestamp(start) {
            return date
        }
        return nil
    }

    // MARK: - Summary

    /// The Clients part of the glance summary line — "2 active · 1 idle" — or
    /// nil when no client is either (FR-019).
    ///
    /// `seen` is counted nowhere on purpose: it keeps its row, because naming a
    /// client the user worked with this morning is the point of the lookback,
    /// but folding it into a headline count would overstate what is going on
    /// right now.
    static func summaryText(for rows: [ClientPresenceRow]) -> String? {
        let active = rows.filter { $0.state == .active }.count
        let idle = rows.filter { $0.state == .idle }.count

        var parts: [String] = []
        if active > 0 { parts.append("\(active) active") }
        if idle > 0 { parts.append("\(idle) idle") }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }
}
