// AttentionWarningAction.swift
// MCPProxy
//
// Spec 109-l (P9, FR-093): the Spec 108 warnings reach the needs-attention list
// as items whose `fix.verb` is a WarningAction kind (`change_setting`,
// `upgrade_admin_key_holders`, `edit_token`, `move_client`, `reconnect_client`).
// This pure mapper turns such an item back into the WarningAction the Clients
// banner already dispatches (108-k K13: `ClientWarningNavigation`), so Home and
// the tray run the SAME navigation as the banner. The target is derived from
// the item's subject, exactly as the Web UI's `fix.target` encodes it:
//   - the binding guard (subject `setting`)  -> `require_mcp_auth`
//   - `edit_token`                           -> the token `client-<id>`
//   - the other client verbs                 -> the client id
// Every route only opens a screen or a sheet. Nothing here mutates config or a
// credential.

import Foundation

enum AttentionWarningAction {
    /// The WarningAction an attention item stands for, or nil for a verb that is
    /// not a Spec 108 warning action (server verbs, `reload_hint`, ...).
    static func from(_ item: AttentionItem) -> WarningAction? {
        switch item.fix.verb {
        case "change_setting":
            return WarningAction(kind: .changeSetting, target: item.subject.id)
        case "upgrade_admin_key_holders":
            return WarningAction(kind: .upgradeAdminKeyHolders, target: nil)
        case "edit_token":
            return WarningAction(kind: .editToken, target: "client-\(item.subject.id)")
        case "move_client":
            return WarningAction(kind: .moveClient, target: item.subject.id)
        case "reconnect_client":
            return WarningAction(kind: .reconnectClient, target: item.subject.id)
        default:
            return nil
        }
    }

    /// The navigation for an item through the K13 dispatcher, nil when the item
    /// is not a Spec 108 warning.
    static func route(for item: AttentionItem) -> AppRoute? {
        guard let action = from(item) else { return nil }
        let warning = ClientWarning(
            code: item.kind,
            clientId: item.subject.type == "client" ? item.subject.id : nil,
            message: item.summary,
            action: action
        )
        return ClientWarningNavigation.route(for: warning)
    }

    /// Whether the tray can act on this row (a client or setting item that maps).
    static func isActionable(_ item: AttentionItem) -> Bool {
        route(for: item) != nil
    }
}
