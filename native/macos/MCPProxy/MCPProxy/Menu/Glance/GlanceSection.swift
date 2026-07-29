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

    // MARK: Configuration

    /// Character budget for a row label before middle truncation kicks in.
    private static let labelBudget = 34

    // MARK: Owned items (kept so rows can be rewritten in place)

    private var summaryItem: NSMenuItem?
    private var activityRows: [NSMenuItem] = []

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
        activityRows = []
        guard isVisible(for: state) else { return [] }

        var items: [NSMenuItem] = []

        let summary = disabledItem(titled: summaryTitle(for: state))
        summaryItem = summary
        items.append(summary)
        items.append(.separator())

        items.append(disabledItem(titled: "Recent"))
        let entries = GlanceSelection.activityRows(from: state.glanceActivity)
        if entries.isEmpty {
            items.append(disabledItem(titled: "No tool calls yet"))
        } else {
            for entry in entries {
                let row = actionableItem()
                apply(entry, to: row, now: now)
                activityRows.append(row)
                items.append(row)
            }
        }

        let openActivity = actionableItem()
        openActivity.title = "Open Activity…"
        openActivity.image = NSImage(systemSymbolName: "list.bullet.rectangle",
                                     accessibilityDescription: "activity log")
        items.append(openActivity)

        return items
    }

    // MARK: Row rendering

    /// Rewrite an activity row so its title, icon, tooltip, accessibility label
    /// and click payload all describe `entry`.
    private func apply(_ entry: ActivityEntry, to item: NSMenuItem, now: Date) {
        let fullLabel = GlanceFormatting.rowLabel(for: entry)
        let label = GlanceFormatting.middleTruncated(fullLabel, limit: Self.labelBudget)
        let age = GlanceFormatting.relativeTime(entry.timestamp, now: now)
        let failed = entry.status != "success"
        let detail = failed ? Self.firstClause(of: entry.errorMessage) : nil

        if let detail {
            item.title = "\(label) · \(detail) — \(age)"
            item.setAccessibilityLabel("\(fullLabel), failed: \(detail), \(age) ago")
        } else {
            item.title = "\(label) — \(age)"
            item.setAccessibilityLabel("\(fullLabel), \(failed ? "failed" : "succeeded"), \(age) ago")
        }

        item.image = NSImage(systemSymbolName: GlanceFormatting.statusSymbolName(for: entry),
                             accessibilityDescription: failed ? "failed" : "succeeded")

        if let message = entry.errorMessage, !message.isEmpty {
            item.toolTip = "\(fullLabel)\n\(message)"
        } else {
            item.toolTip = fullLabel
        }

        item.representedObject = entry.sessionId
    }

    /// First clause of an error message — everything up to the first newline,
    /// period or colon — so a multi-sentence backend error still fits one row.
    /// The full message stays in the tooltip.
    static func firstClause(of message: String?) -> String? {
        guard let message else { return nil }
        let trimmed = message.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }
        let head = trimmed.components(separatedBy: CharacterSet(charactersIn: ".:\n")).first ?? trimmed
        let clause = head.trimmingCharacters(in: .whitespaces)
        return clause.isEmpty ? trimmed : clause
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

    private func actionableItem() -> NSMenuItem {
        let item = NSMenuItem(title: "", action: clickAction, keyEquivalent: "")
        item.target = clickTarget
        return item
    }
}
