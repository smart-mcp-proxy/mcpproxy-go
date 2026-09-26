// ScopeFilter.swift
// MCPProxy
//
// Spec 109-k (activity-scope-filters), macOS section of
// specs/109-ux-navigation-consistency/contracts/url-filter-contract.md:
// "`AppState.scopeFilter: ScopeFilter` (struct with every parameter above,
// `profile`, `client` and `token` included and hidden until
// `features.scope_filters`) replaces `pendingActivitySessionFilter`."
//
// This is the macOS carrier for the same contract the Web `useScopeQuery`
// composable implements: an in-app link (a tray glance row, a Tools row's
// "Calls" action, ...) SETS this on `AppState` before switching the sidebar
// selection; the destination view reads it on `.task`/`onAppear` and clears
// it once applied — the same "consume, then clear" rule
// `pendingActivitySessionFilter` already had (a notification alone cannot
// carry a hand-off to a view a click is about to create, since that view's
// `onReceive` observers subscribe only once it appears).
//
// `profile`/`client`/`token` are part of the struct (contract: "included and
// hidden until features.scope_filters") but this app has no `features`
// plumbing yet (Spec 108-e/109-h are not merged into this build) — they are
// carried for forward-compat and never read by a view today, exactly like
// the Web composable's `requires: "scope_filters"` parameters are registered
// before Spec 108-e lands.
struct ScopeFilter: Equatable {
    var view: String? // "calls" | "sessions" | "system" | "all"
    var server: String?
    var tool: String? // bare tool name, or "server:tool" (split by restQuery())
    var session: String?
    var status: String?
    var authType: String?
    // Hidden until `features.scope_filters` exists on this app (see file doc).
    var profile: String?
    var client: String?
    var token: String?

    init(
        view: String? = nil,
        server: String? = nil,
        tool: String? = nil,
        session: String? = nil,
        status: String? = nil,
        authType: String? = nil,
        profile: String? = nil,
        client: String? = nil,
        token: String? = nil
    ) {
        self.view = view
        self.server = server
        self.tool = tool
        self.session = session
        self.status = status
        self.authType = authType
        self.profile = profile
        self.client = client
        self.token = token
    }

    var isEmpty: Bool {
        self == ScopeFilter()
    }

    /// The two "call" types vs. every other known activity type
    /// (contracts/url-filter-contract.md `view` row). Mirrors
    /// frontend/src/utils/activity.ts ACTIVITY_CALL_TYPES.
    static let callTypes = ["tool_call", "internal_tool_call"]
    /// Every other type this app's Activity filter dropdown knows
    /// (ActivityView.typeOptions) plus the ones only the Web/CLI table shows
    /// today (tool_quarantine_change, security_scan, credential_broker,
    /// prompt_get, preflight) — kept as one list so `system` can never
    /// silently drop a backend type, the same drift `activityViewTypes()`
    /// guards against on the Web side.
    static let systemTypes = [
        "system_start", "system_stop", "config_change", "policy_decision",
        "quarantine_change", "server_change", "tool_quarantine_change",
        "security_scan", "credential_broker", "prompt_get", "preflight",
    ]

    /// The REST query this filter maps to, or `nil` when it is contradictory
    /// (rule 8: an explicit `server` that disagrees with `tool`'s own server
    /// prefix — their intersection is empty, so no request could express
    /// both). `view == "sessions"` returns an empty query: contract "view ->
    /// REST" says a Sessions request carries no `from`/`to`, `server`,
    /// `tool`, `status`, `type` or `auth_type` at all (only `session`, which
    /// GET /sessions itself does not filter by, and profile/client/token
    /// once available).
    func restQuery() -> [String: String]? {
        if view == "sessions" {
            return [:]
        }

        var out: [String: String] = [:]

        if let session, !session.isEmpty {
            out[session.hasPrefix("ws-") ? "work_session_id" : "session_id"] = session
        }
        if let status, !status.isEmpty {
            out["status"] = status
        }
        if let authType, !authType.isEmpty {
            out["auth_type"] = authType
        }

        if let tool, !tool.isEmpty {
            if let colonIndex = tool.firstIndex(of: ":") {
                let toolServer = String(tool[tool.startIndex..<colonIndex])
                let toolName = String(tool[tool.index(after: colonIndex)...])
                if let server, !server.isEmpty, server != toolServer {
                    return nil // rule 8: contradictory, no request
                }
                out["server"] = toolServer
                out["tool"] = toolName
            } else {
                if let server, !server.isEmpty {
                    out["server"] = server
                }
                out["tool"] = tool
            }
        } else if let server, !server.isEmpty {
            out["server"] = server
        }

        switch view {
        case "calls":
            out["type"] = Self.callTypes.joined(separator: ",")
        case "system":
            out["type"] = Self.systemTypes.joined(separator: ",")
        default:
            break // "all" or nil: no type filter
        }

        return out
    }
}
