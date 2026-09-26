// ScopeFilter.swift
// MCPProxy
//
// Spec 109-k (T122): the native half of the URL filter contract
// (specs/109-ux-navigation-consistency/contracts/url-filter-contract.md).
// One value type carries every contract parameter; `restRequest(for:)` maps it
// to exactly the REST query the Web composable (`useScopeQuery.ts`) and the
// CLI (`activity_cmd.go`) send, so the three surfaces ask the core the same
// question. It replaces the one-off `pendingActivitySessionFilter` hand-off.

import Foundation

/// The Activity view segments (FR-070), in display order. Default `.calls`.
enum ActivityViewMode: String, CaseIterable, Identifiable {
    case calls
    case sessions
    case system
    case all

    var id: String { rawValue }

    var label: String {
        switch self {
        case .calls: return "Tool calls"
        case .sessions: return "Sessions"
        case .system: return "System events"
        case .all: return "All"
        }
    }
}

/// The scope-aware native pages whose REST query the contract defines.
enum ScopePage {
    case activity
    case usage
    case tools
    case servers
}

/// A resolved request: path plus query items, in a stable order.
struct ScopeRequest: Equatable {
    let path: String
    let query: [URLQueryItem]

    /// The encoded query (no leading `?`); values escaped like every other
    /// tray request so a name with a space or `&` stays one parameter.
    var queryString: String {
        query.map { "\($0.name)=\(APIClient.escapeQueryValue($0.value ?? ""))" }
            .joined(separator: "&")
    }
}

struct ScopeFilter: Equatable {
    var view: ActivityViewMode = .calls
    /// Upstream server. REST on Activity/Usage; client-side on Tools/Review.
    var server: String?
    /// Canonical `server:tool` (or a bare tool name).
    var tool: String?
    /// Work session id (`ws-…`) or a legacy MCP transport session id.
    var session: String?
    /// Page-defined status. Activity/Usage: call outcome. Never sticky.
    var status: String?
    /// RFC 3339 or relative (`-24h`, `-7d`, `-30m`). Sticky.
    var from: String?
    var to: String?
    /// Explicit comma-separated type set; overrides `view` (not in Sessions).
    var type: String?
    /// `admin` | `agent`.
    var authType: String?
    /// Free-text search (client-side everywhere it applies natively).
    var q: String?
    /// Tools tier (`risk` alias); client-side.
    var tier: String?
    /// Tools approval; client-side.
    var approval: String?
    /// Spec 108 scope parameters: carried, but hidden and never sent until
    /// `GET /api/v1/status` lists `features.scope_filters`.
    var profile: String?
    var client: String?
    var token: String?

    // MARK: Type sets (FR-070; same derivation as the CLI's activitySystemTypes)

    static let callTypes = ["tool_call", "internal_tool_call"]

    /// Every known activity type (storage.ValidActivityTypes), in its order.
    static let knownTypes = [
        "tool_call", "policy_decision", "quarantine_change", "server_change",
        "system_start", "system_stop", "internal_tool_call", "config_change",
        "tool_quarantine_change", "security_scan", "credential_broker",
        "preflight", "prompt_get",
    ]

    static let systemTypes = knownTypes.filter { !callTypes.contains($0) }

    /// The Web-only "Other / internal" status bucket — never sent to REST.
    static let otherStatus = "other"

    // MARK: Construction

    init() {}

    /// Builds a filter from a Web URL's query parameters (unknown names are
    /// ignored here; the caller keeps its URL). `risk` is an alias of `tier`.
    init(query: [String: String]) {
        func value(_ name: String) -> String? {
            guard let v = query[name], !v.isEmpty else { return nil }
            return v
        }
        view = value("view").flatMap(ActivityViewMode.init(rawValue:)) ?? .calls
        server = value("server")
        tool = value("tool")
        session = value("session")
        status = value("status")
        from = value("from")
        to = value("to")
        type = value("type")
        authType = value("auth_type")
        q = value("q")
        tier = value("tier") ?? value("risk")
        approval = value("approval")
        profile = value("profile")
        client = value("client")
        token = value("token")
    }

    /// Tray glance client row → that client's calls.
    static func forSession(_ id: String) -> ScopeFilter {
        var f = ScopeFilter()
        f.session = id
        return f
    }

    /// Clients row link (requires `scope_filters`).
    static func forClient(_ id: String) -> ScopeFilter {
        var f = ScopeFilter()
        f.client = id
        return f
    }

    /// Token row link (requires `scope_filters`).
    static func forToken(_ name: String) -> ScopeFilter {
        var f = ScopeFilter()
        f.token = name
        return f
    }

    /// Sessions-view row → that session's calls (link map): its
    /// `work_session_id`, or its transport `id` for a legacy row.
    static func forSessionRow(_ row: APIClient.MCPSession) -> ScopeFilter {
        var f = ScopeFilter()
        f.view = .calls
        f.session = (row.workSessionId?.isEmpty == false) ? row.workSessionId : row.id
        return f
    }

    /// Whether `row` is the session this filter selects (Sessions view
    /// highlight): either id may be in the URL.
    func highlights(_ row: APIClient.MCPSession) -> Bool {
        guard let session, !session.isEmpty else { return false }
        return session == row.id || session == row.workSessionId
    }

    // MARK: Contract rules

    /// Splits `tool` per the contract: `server:tool` → (server, bare tool).
    /// `conflict` when an explicit `server` names a different server (rule 8).
    static func splitTool(_ tool: String?, server: String?) -> (server: String?, tool: String?, conflict: Bool) {
        let explicit = (server?.isEmpty ?? true) ? nil : server
        guard let tool, !tool.isEmpty else { return (explicit, nil, false) }
        guard let colon = tool.firstIndex(of: ":") else { return (explicit, tool, false) }
        let prefix = String(tool[..<colon])
        let bare = String(tool[tool.index(after: colon)...])
        if let explicit, explicit != prefix { return (nil, nil, true) }
        return (explicit ?? prefix, bare, false)
    }

    /// `ws-` prefix → `work_session_id`, anything else → `session_id`.
    static func sessionRestParam(_ session: String) -> String {
        session.hasPrefix("ws-") ? "work_session_id" : "session_id"
    }

    /// Resolves a relative `-Nm|-Nh|-Nd` against `now`; anything else passes
    /// through unchanged (an absolute RFC 3339 value).
    static func resolveTime(_ value: String, now: Date = Date()) -> String {
        guard value.hasPrefix("-"), let unit = value.last, "mhd".contains(unit),
              let amount = Int(value.dropFirst().dropLast()), amount >= 0 else { return value }
        let seconds: TimeInterval
        switch unit {
        case "m": seconds = TimeInterval(amount) * 60
        case "h": seconds = TimeInterval(amount) * 3600
        default: seconds = TimeInterval(amount) * 86400
        }
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime]
        return formatter.string(from: now.addingTimeInterval(-seconds))
    }

    /// Usage accepts only `window=24h|7d|all`. Returns nil for a range that
    /// none of the three presets expresses.
    static func usageWindow(from: String?, to: String?) -> String? {
        if let to, !to.isEmpty { return nil }
        guard let from, !from.isEmpty else { return "all" }
        switch from {
        case "-24h": return "24h"
        case "-7d": return "7d"
        default: return nil
        }
    }

    /// Rule 8: a URL `server` that differs from the `tool` prefix.
    var hasConflict: Bool { Self.splitTool(tool, server: server).conflict }

    /// The conflict empty-state text (rule 8).
    var conflictMessage: String? {
        guard hasConflict, let server, let tool else { return nil }
        return "No calls match: server \(server) and tool \(tool) name different servers"
    }

    /// Contract parameters that are set but that `page` (in the current
    /// `view`) cannot apply — rendered as disabled "not applicable here" chips
    /// (rule 5), never silently dropped.
    func inapplicableParams(for page: ScopePage) -> [String] {
        var out: [String] = []
        func add(_ name: String, _ value: String?) {
            if let value, !value.isEmpty { out.append(name) }
        }
        switch page {
        case .activity:
            guard view == .sessions else { return [] }
            add("from", from); add("to", to); add("server", server); add("tool", tool)
            add("status", status); add("type", type); add("auth_type", authType)
        case .usage:
            if Self.usageWindow(from: from, to: to) == nil {
                add("from", from); add("to", to)
            }
        case .tools, .servers:
            add("from", from); add("to", to)
        }
        return out
    }

    /// The Spec 108 parameters a UI may show, in contract order: none until
    /// the core advertises `features.scope_filters`.
    func visibleScopeParams(scopeFiltersAvailable: Bool) -> [String] {
        guard scopeFiltersAvailable else { return [] }
        return [("profile", profile), ("client", client), ("token", token)]
            .compactMap { ($0.1?.isEmpty ?? true) ? nil : $0.0 }
    }

    // MARK: REST mapping

    /// The request `page` issues for this filter, or `nil` when the filter is
    /// contradictory (rule 8) and no request may be made.
    func restRequest(for page: ScopePage, scopeFiltersAvailable: Bool, now: Date = Date()) -> ScopeRequest? {
        var items: [URLQueryItem] = []
        func add(_ name: String, _ value: String?) {
            if let value, !value.isEmpty { items.append(URLQueryItem(name: name, value: value)) }
        }
        func addScope(_ names: Set<String>) {
            guard scopeFiltersAvailable else { return }
            if names.contains("profile") { add("profile", profile) }
            if names.contains("client") { add("client", client) }
            if names.contains("token") { add("token", token) }
        }
        func addServerAndTool() -> Bool {
            let split = Self.splitTool(tool, server: server)
            if split.conflict { return false }
            add("server", split.server)
            add("tool", split.tool)
            return true
        }
        func addStatus() {
            if status != Self.otherStatus { add("status", status) }
        }

        switch page {
        case .activity where view == .sessions:
            // `session` only selects/highlights a row here; everything else of
            // the Activity table is inapplicable (see inapplicableParams).
            addScope(["profile", "client", "token"])
            return ScopeRequest(path: "/api/v1/sessions", query: items)

        case .activity:
            if let type, !type.isEmpty {
                add("type", type)
            } else {
                switch view {
                case .calls: add("type", Self.callTypes.joined(separator: ","))
                case .system: add("type", Self.systemTypes.joined(separator: ","))
                case .all, .sessions: break
                }
            }
            guard addServerAndTool() else { return nil }
            if let session, !session.isEmpty { add(Self.sessionRestParam(session), session) }
            addStatus()
            add("start_time", from.map { Self.resolveTime($0, now: now) })
            add("end_time", to.map { Self.resolveTime($0, now: now) })
            add("auth_type", authType)
            addScope(["profile", "client", "token"])
            return ScopeRequest(path: "/api/v1/activity", query: items)

        case .usage:
            guard addServerAndTool() else { return nil }
            addStatus()
            // An unmappable range is shown as not applied; `all` says so
            // honestly, where omitting it would silently mean the 24h default.
            add("window", Self.usageWindow(from: from, to: to) ?? "all")
            addScope(["profile", "client", "token"])
            return ScopeRequest(path: "/api/v1/activity/usage", query: items)

        case .tools:
            // server/status/q/tier/approval are client-side: GET /tools parses
            // no filter parameters.
            addScope(["profile", "client"])
            return ScopeRequest(path: "/api/v1/tools", query: items)

        case .servers:
            addScope(["profile"])
            return ScopeRequest(path: "/api/v1/servers", query: items)
        }
    }
}
