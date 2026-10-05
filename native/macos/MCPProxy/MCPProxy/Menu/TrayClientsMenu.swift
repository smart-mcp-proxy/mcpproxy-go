// TrayClientsMenu.swift
// MCPProxy
//
// Spec 108-k (FR-048, K14): the tray "Clients" submenu that replaces the v2
// "Profile:" switcher. The rows and their items are a pure function of the
// clients and profiles lists, so the whole shape (which clients appear, the
// title text, the checkmark, Lock/Unlock and the no-credential CTA) is
// testable without AppKit. `AppController` only renders the model.

import AppKit
import Foundation

/// One item of a client's submenu.
enum TrayClientMenuItem: Equatable {
    /// A profile choice; `profile == ""` is "All servers". `tooltip` flags a
    /// profile none of whose servers are configured: choosing it would leave the
    /// client with no tools (the F11 finding, kept from the v2 switcher).
    case profile(title: String, profile: String, isCurrent: Bool, tooltip: String?)
    case separator
    /// Lock / Unlock: sets the mode, keeping the current profile. Disabled on
    /// All servers, where only `switchable` is valid.
    case lock(title: String, mode: BindingMode, isEnabled: Bool)
    /// A client without an active client credential cannot be bound: the only
    /// item is the way to get one.
    case upgrade(title: String)
}

/// One client row of the Clients submenu.
struct TrayClientMenuRow: Equatable {
    let clientId: String
    /// `Cursor — Work · Read-only 🔒`.
    let title: String
    let items: [TrayClientMenuItem]
}

/// The payload of a Clients-submenu `NSMenuItem` (`representedObject`).
final class TrayClientAction: NSObject {
    let clientId: String
    /// The profile to bind (`""` = All servers); nil for a lock or upgrade item.
    let profile: String?
    /// The mode to set; nil keeps the credential's own.
    let mode: BindingMode?

    init(clientId: String, profile: String?, mode: BindingMode?) {
        self.clientId = clientId
        self.profile = profile
        self.mode = mode
    }
}

enum TrayClientsMenu {
    /// Renders one client row's submenu. Auto-enabling is OFF: with it on AppKit
    /// re-validates every item against its target and overrides `isEnabled`,
    /// which enabled Lock on an All-servers row (a locked client needs a profile).
    @MainActor
    static func render(
        _ row: TrayClientMenuRow, target: AnyObject,
        profileAction: Selector, lockAction: Selector, upgradeAction: Selector
    ) -> NSMenu {
        let menu = NSMenu()
        menu.autoenablesItems = false
        for entry in row.items {
            switch entry {
            case .separator:
                menu.addItem(.separator())
            case .profile(let title, let profile, let isCurrent, let tooltip):
                let item = NSMenuItem(title: title, action: profileAction, keyEquivalent: "")
                item.target = target
                item.representedObject = TrayClientAction(clientId: row.clientId, profile: profile, mode: nil)
                item.state = isCurrent ? .on : .off
                item.toolTip = tooltip
                menu.addItem(item)
            case .lock(let title, let mode, let isEnabled):
                let item = NSMenuItem(title: title, action: lockAction, keyEquivalent: "")
                item.target = target
                item.representedObject = TrayClientAction(clientId: row.clientId, profile: nil, mode: mode)
                item.isEnabled = isEnabled
                if !isEnabled { item.toolTip = "Choose a profile to lock" }
                menu.addItem(item)
            case .upgrade(let title):
                let item = NSMenuItem(title: title, action: upgradeAction, keyEquivalent: "")
                item.target = target
                item.representedObject = TrayClientAction(clientId: row.clientId, profile: nil, mode: nil)
                menu.addItem(item)
            }
        }
        return menu
    }

    /// The top-level menu title.
    static let title = "Clients"
    static let allServersTitle = "All servers"

    /// Clients that hold an active client credential OR are connected, sorted by
    /// display name. Nothing else belongs in the tray: an uninstalled or
    /// never-connected client is noise in a menu.
    static func build(
        clients: [ClientPresenceRecord], profiles: [ProfileView], knownServers: Set<String>? = nil
    ) -> [TrayClientMenuRow] {
        clients
            .filter { $0.hasClientCredential || $0.connected }
            .sorted { $0.displayName.localizedCaseInsensitiveCompare($1.displayName) == .orderedAscending }
            .map { row(for: $0, profiles: profiles, knownServers: knownServers) }
    }

    private static func tooltip(for profile: ProfileView, knownServers: Set<String>?) -> String? {
        guard let knownServers else { return nil }
        let servers = profile.reachableServers
        guard servers.allSatisfy({ !knownServers.contains($0) }) else { return nil }
        return "None of this profile’s servers (\(servers.joined(separator: ", "))) "
            + "are in the configuration. Choosing it would leave the client with no tools."
    }

    /// Two profiles may share a title (`Work Read-only` twice); the menu would
    /// then show indistinguishable entries, so a shared title carries the slug.
    static func distinctTitle(for profile: ProfileView, in profiles: [ProfileView]) -> String {
        distinctTitle(profile.displayTitle, name: profile.name, in: profiles)
    }

    private static func distinctTitle(_ title: String, name: String, in profiles: [ProfileView]) -> String {
        let clashes = profiles.filter { $0.displayTitle == title }.count > 1
        return clashes && title != name ? "\(title) (\(name))" : title
    }

    private static func row(
        for client: ClientPresenceRecord, profiles: [ProfileView], knownServers: Set<String>?
    ) -> TrayClientMenuRow {
        let title = "\(client.displayName) — \(profileLabel(for: client, profiles: profiles))"
            + (client.isLocked ? " 🔒" : "")
        guard client.hasClientCredential else {
            // The same words as the row's button on the Clients page: only an
            // admin-key holder is an "upgrade".
            let cta = ClientBindingControlsState(client).cta ?? .connect
            return TrayClientMenuRow(clientId: client.id, title: title, items: [.upgrade(title: cta.title)])
        }
        let current = client.boundProfile
        let missing = client.profileMissing == true
        var items: [TrayClientMenuItem] = [
            .profile(title: allServersTitle, profile: "", isCurrent: current.isEmpty && !missing, tooltip: nil),
            .separator,
        ]
        for profile in profiles {
            items.append(.profile(
                title: distinctTitle(for: profile, in: profiles), profile: profile.name,
                isCurrent: profile.name == current && !missing,
                tooltip: tooltip(for: profile, knownServers: knownServers)))
        }
        items.append(.separator)
        if client.isLocked {
            items.append(.lock(title: "Unlock", mode: .switchable, isEnabled: true))
        } else {
            items.append(.lock(title: "Lock", mode: .locked, isEnabled: !current.isEmpty))
        }
        return TrayClientMenuRow(clientId: client.id, title: title, items: items)
    }

    /// `profileTitle ?? profile`, "All servers" for "", `<name> (missing)` for a
    /// profile that no longer exists. A client without a credential is not
    /// confined by any profile, which "All servers" states truthfully.
    static func profileLabel(for client: ClientPresenceRecord, profiles: [ProfileView]) -> String {
        // A client without an active client credential is not confined by any
        // profile (a revoked credential's stale binding included), so it reads
        // "All servers" whatever the record still carries.
        guard client.hasClientCredential else { return allServersTitle }
        let name = client.boundProfile
        if client.profileMissing == true { return "\(name) (missing)" }
        if name.isEmpty { return allServersTitle }
        if let title = client.profileTitle, !title.isEmpty { return distinctTitle(title, name: name, in: profiles) }
        if let listed = profiles.first(where: { $0.name == name }) { return distinctTitle(for: listed, in: profiles) }
        return name
    }
}

/// Spec 108-k K14: where a glance "Clients" row leads. With attribution
/// available it is the client's activity (`client=<id>`); without it, or for a
/// session that carries no client id, the session's own.
enum GlanceClientLink {
    static func filter(
        forSession sessionId: String, sessions: [APIClient.MCPSession], scopeFiltersAvailable: Bool
    ) -> ScopeFilter {
        if scopeFiltersAvailable,
           let session = sessions.first(where: { $0.id == sessionId || $0.workSessionId == sessionId }),
           let clientId = session.clientId, !clientId.isEmpty {
            return .forClient(clientId)
        }
        return .forSession(sessionId)
    }
}
