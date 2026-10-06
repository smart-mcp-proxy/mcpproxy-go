// HomeAttentionAction.swift
// MCPProxy
//
// Home's needs-attention list fix dispatch (Spec 109 FR-001/FR-005),
// extracted from AttentionRow's view body so it is a plain, testable model —
// `HomeReviewActionTests` drives it directly, with a URL-stubbed APIClient,
// without instantiating SwiftUI.

import Foundation

/// Runs one `AttentionItem`'s fix. `login`/`restart`/`enable` execute in
/// place, mirroring the tray's `TrayServerAction.fromHealthAction` (the same
/// three verbs it runs itself). Every other verb opens the form or screen
/// that performs it. `review` always opens the informed-review sheet.
enum HomeAttentionAction {
    @MainActor
    static func performFix(_ item: AttentionItem, appState: AppState) async {
        switch item.fix.verb {
        case "login", "restart", "enable":
            guard let client = appState.apiClient else { return }
            do {
                switch item.fix.verb {
                case "login":
                    try await client.loginServer(item.subject.id)
                case "restart":
                    try await client.restartServer(item.subject.id)
                case "enable":
                    try await client.enableServer(item.subject.id)
                default:
                    break
                }
            } catch {
                // Action errors are visible via the attention list's own refresh.
            }
        case "set_secret", "configure":
            // These require a form (the secret value or configuration
            // fields), so take the user to the Config tab instead of
            // no-op'ing.
            navigateToServerDetail(item.subject.name, tab: .config)
        case "edit_url":
            // The endpoint itself is invalid. Open its edit form and put the
            // cursor in the concrete field that needs correction.
            navigateToServerDetail(item.subject.name, tab: .config, focusField: .endpoint)
        case "view_logs":
            navigateToServerDetail(item.subject.name, tab: .logs)
        case "review":
            NotificationCenter.default.post(name: .switchToSidebarTab, object: SidebarItem.review.rawValue)
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.2) {
                NotificationCenter.default.post(name: .showReview, object: item.subject.name)
            }
        case "reload_hint":
            // 109-h: the client's `/clients?focus=<id>` screen doesn't exist
            // yet. Never a dead link (contracts/rest-api.md#attention): until
            // that screen ships, surface the item's own restart guidance
            // (`detail`) as an alert instead of doing nothing. HomeView
            // observes `pendingReloadHint` and presents it.
            appState.pendingReloadHint = item
        default:
            // Spec 109-l: the Spec 108 warning verbs (`change_setting`,
            // `upgrade_admin_key_holders`, `edit_token`, `move_client`,
            // `reconnect_client`) navigate through the same dispatcher as the
            // Clients banner. They open a screen or a sheet and never mutate.
            if let route = AttentionWarningAction.route(for: item) {
                appState.navigate(route)
            }
        }
    }

    /// Reuses the same "switch sidebar, then select the server" route Home's
    /// other links already use, so a `.showServerDetail` observer set up
    /// once in ServersView handles every doorway into server detail.
    @MainActor
    private static func navigateToServerDetail(
        _ serverName: String,
        tab: ServerDetailTab,
        focusField: TrayConfigFocusField? = nil
    ) {
        NotificationCenter.default.post(name: .switchToServers, object: nil)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.3) {
            NotificationCenter.default.post(
                name: .showServerDetail,
                object: ServerDetailTarget(serverName: serverName, tab: tab, focusField: focusField)
            )
        }
    }
}
