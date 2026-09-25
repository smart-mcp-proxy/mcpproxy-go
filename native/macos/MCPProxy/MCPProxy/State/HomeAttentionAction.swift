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
/// three verbs it runs itself). Every other verb — including `review`, which
/// is NEVER a one-click approve — opens the location that performs it: today
/// the server's detail view (its existing per-tool review for `review`);
/// `/review/<n>` and `/clients?focus=` get their own native screens in
/// 109-g/109-h.
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
        case "set_secret", "configure", "edit_url":
            // None of these complete via a single API call — they need a
            // form (the secret value, the new URL, isolation fields). Take
            // the user to the server's Config tab instead of no-op'ing.
            navigateToServerDetail(item.subject.name, tab: .config)
        case "view_logs":
            navigateToServerDetail(item.subject.name, tab: .logs)
        case "review":
            // Interim fix target (contracts/rest-api.md#attention): opens the
            // server's existing per-tool review, never a one-click approve.
            navigateToServerDetail(item.subject.name, tab: .tools)
        case "reload_hint":
            // 109-h: the client's reload hint has no native screen yet.
            break
        default:
            break
        }
    }

    /// Reuses the same "switch sidebar, then select the server" route Home's
    /// other links already use, so a `.showServerDetail` observer set up
    /// once in ServersView handles every doorway into server detail.
    @MainActor
    private static func navigateToServerDetail(_ serverName: String, tab: ServerDetailTab) {
        NotificationCenter.default.post(name: .switchToServers, object: nil)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.3) {
            NotificationCenter.default.post(
                name: .showServerDetail,
                object: ServerDetailTarget(serverName: serverName, tab: tab)
            )
        }
    }
}
