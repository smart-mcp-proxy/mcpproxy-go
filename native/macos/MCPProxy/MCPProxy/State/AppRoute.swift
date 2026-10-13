// AppRoute.swift
// MCPProxy
//
// Spec 108-k (K12, K13, K19): one hand-off for "show this in that view", the
// generalisation of `pendingAddAction` and `scopeFilter`. A fix button, a
// Profiles card link, a tray item or the access explainer sets
// `AppState.pendingRoute` BEFORE the sidebar switches; the destination view
// consumes the kinds it owns on appear (a view created by the switch) or on
// change (one already showing). A notification alone cannot carry it: a view
// created by the click subscribes too late.

import Foundation

/// The two tabs of the Clients hub that routes can land on.
enum ClientsTab: Int, Equatable {
    case clients = 0
    case endpoint = 1
    case tokens = 2
}

/// What the Settings window should bring into view (K13, K20).
enum SettingsFocus: Equatable {
    /// Security tab, scrolled to `require_mcp_auth`.
    case requireMCPAuth
    /// Security tab, Anonymous callers picker, optionally PRESELECTED (never
    /// saved by the fix button: the operator reviews the change and saves).
    case anonymousProfile(preselect: String?)
    /// Any other setting key (`read_only_mode`, `disable_management`, …): the
    /// Security tab opens, nothing is scrolled to.
    case setting(String)
}

/// A request to show something in a specific native view.
enum AppRoute: Equatable {
    /// Home, its usage summary and sessions scoped by a filter (K18).
    case home(filter: ScopeFilter?)
    case profiles
    /// The profile editor; `focusTool` scrolls to and selects a `server:tool` row.
    case profileEditor(name: String, focusTool: String?)
    /// The Clients hub on a tab, optionally filtered (profile chip + clear).
    case clients(tab: ClientsTab, filter: ScopeFilter?)
    /// A client row's detail, with the profile picker focused.
    case clientDetail(id: String)
    /// The connect sheet, preselected on a client (nil = no preselection).
    case connectSheet(clientId: String?)
    /// The admin-key upgrade sheet.
    case upgradeAdminKeys
    case tools(filter: ScopeFilter?)
    case servers(filter: ScopeFilter?)
    case serverDetail(name: String)
    case reviewQueue
    case settings(SettingsFocus)
    /// The access explainer sheet with a subject and tool preset.
    case explain(subject: ExplainerSubject, tool: String?)

    /// The sidebar section that hosts the destination; nil for Settings (its
    /// own window).
    var sidebarItem: SidebarItem? {
        switch self {
        case .home: return .home
        case .profiles, .profileEditor: return .profiles
        case .clients, .clientDetail, .connectSheet, .upgradeAdminKeys: return .clients
        case .tools: return .tools
        case .servers, .serverDetail: return .servers
        case .reviewQueue: return .review
        case .explain: return nil
        case .settings: return nil
        }
    }
}

extension AppState {
    /// Hand a route to its destination: publish first, then the window's own
    /// observers switch the sidebar (`MainWindow`) and the view consumes it.
    func navigate(_ route: AppRoute) {
        pendingRoute = route
        if case .settings = route {
            NotificationCenter.default.post(name: .openSettings, object: nil)
        }
    }

    /// Take the pending route if `match` recognises it, clearing it. A view
    /// consumes only the routes it owns, so the Clients hub cannot swallow a
    /// Tools route.
    func consumeRoute<T>(_ match: (AppRoute) -> T?) -> T? {
        guard let route = pendingRoute, let value = match(route) else { return nil }
        pendingRoute = nil
        return value
    }

    /// Follow an in-app link to a scope-aware page with its filter (K12). The
    /// Activity page keeps 109-k's own channel; every other page goes through
    /// the route.
    func openScoped(page: ScopePage, filter: ScopeFilter) {
        switch page {
        case .activity: openActivity(with: filter)
        case .tools: navigate(.tools(filter: filter))
        case .servers: navigate(.servers(filter: filter))
        case .clients: navigate(.clients(tab: .clients, filter: filter))
        case .tokens: navigate(.clients(tab: .tokens, filter: filter))
        case .usage: navigate(.home(filter: filter))
        }
    }

    /// Open the profile editor on a profile, optionally on one tool row
    /// (used by the explainer's fixes).
    func openProfileEditor(name: String, focusTool: String? = nil) {
        navigate(.profileEditor(name: name, focusTool: focusTool))
    }
}

extension Notification.Name {
    /// Posted by `AppState.navigate(.settings)`; observed by the app delegate,
    /// which shows the Settings window.
    static let openSettings = Notification.Name("MCPProxy.openSettings")
}
