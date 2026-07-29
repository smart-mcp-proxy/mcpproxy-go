// GlanceSection.swift
// MCPProxy
//
// Builds the "glance" block at the top of the tray menu: a one-line summary,
// the most recent qualifying tool calls, the active MCP clients, and the
// 24h histogram submenu.
//
// Every text row is a plain NSMenuItem. Custom (view-backed) menu items receive
// mouse events but NOT keyboard events, so building the rows as hosted SwiftUI
// would silently cost keyboard navigation and VoiceOver. Only the histogram —
// which genuinely needs drawing — is view-backed, and it lives alone inside its
// own submenu.
//
// This component never builds a Web UI URL. It is handed only AppState, whose
// webUIBaseURL is scheme/host/port by design, while the API key lives on the
// core manager. Rows therefore carry a target/action pair plus a
// representedObject holding the record's session id, and the app delegate opens
// the authenticated URL through the same path as every other menu action.
//
// Deliberately NOT @MainActor: AppController (the NSApplicationDelegate that
// will call this, MCPProxyApp.swift:15) is not actor-isolated, and this SDK does
// not infer MainActor from NSApplicationDelegate conformance, so annotating it
// would make rebuildMenu() fail to compile.

import AppKit

final class GlanceSection {

    // MARK: Click routing

    private weak var clickTarget: AnyObject?
    private let clickAction: Selector

    // MARK: Owned items (kept so rows can be rewritten in place)

    private var summaryItem: NSMenuItem?

    init(target: AnyObject?, action: Selector) {
        self.clickTarget = target
        self.clickAction = action
    }

    // MARK: Building

    /// Whether the glance block should appear at all. When the core is stopped
    /// or disconnected the block is hidden entirely, rather than presenting the
    /// previous core's numbers as if they were live.
    func isVisible(for state: AppState) -> Bool {
        state.isConnected && !state.isStopped
    }

    /// Build the whole block, ordered top to bottom and ending with a separator
    /// so the caller can splice it into the menu in one go. Returns an empty
    /// array when the block is hidden.
    func items(for state: AppState, now: Date = Date()) -> [NSMenuItem] {
        summaryItem = nil
        guard isVisible(for: state) else { return [] }

        var items: [NSMenuItem] = []
        let summary = disabledItem(titled: summaryTitle(for: state))
        summaryItem = summary
        items.append(summary)
        items.append(.separator())
        return items
    }

    // MARK: Header

    private func summaryTitle(for state: AppState) -> String {
        var parts: [String] = []
        if let calls = state.callsThisHour {
            parts.append(calls == 1 ? "1 call this hour" : "\(calls) calls this hour")
        }
        let clients = GlanceSelection.activeClients(from: state.glanceSessions, limit: Int.max).count
        parts.append(clients == 1 ? "1 client" : "\(clients) clients")
        return parts.joined(separator: " · ")
    }

    // MARK: Item factories

    private func disabledItem(titled title: String) -> NSMenuItem {
        let item = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        item.isEnabled = false
        return item
    }
}
